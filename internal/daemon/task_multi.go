package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	apicore "github.com/compozy/compozy/internal/api/core"
	"github.com/compozy/compozy/internal/core/model"
	taskscore "github.com/compozy/compozy/internal/core/tasks"
	workspacecfg "github.com/compozy/compozy/internal/core/workspace"
	"github.com/compozy/compozy/internal/store/globaldb"
	eventspkg "github.com/compozy/compozy/pkg/compozy/events"
	"github.com/compozy/compozy/pkg/compozy/events/kinds"
)

const (
	taskMultiItemStatusQueued    = "queued"
	taskMultiItemStatusRunning   = "running"
	taskMultiItemStatusCompleted = "completed"
	taskMultiItemStatusFailed    = "failed"
	taskMultiItemStatusCanceled  = "canceled"
	taskMultiItemStatusUnchanged = "unchanged"

	taskMultiChildPollInterval = 100 * time.Millisecond
)

type taskMultiHandoffArtifacts struct {
	HandoffPath  string
	SummaryPath  string
	WorktreePath string
	Prompt       string
	SummaryLine  string
}

type taskMultiParentSummary struct {
	WorkflowSlug         string                  `json:"workflow_slug"`
	MultipleMode         string                  `json:"multiple_mode"`
	SelectedTasks        []string                `json:"selected_tasks"`
	SourceWorkspaceRoot  string                  `json:"source_workspace_root"`
	ChildOutcomes        []taskMultiChildOutcome `json:"child_outcomes"`
	HandoffPromptPath    string                  `json:"handoff_prompt_path"`
	SummaryPath          string                  `json:"summary_path"`
	WorktreeManifestPath string                  `json:"worktree_manifest_path"`
}

type taskMultiChildOutcome struct {
	TaskName         string `json:"task_name"`
	ChildRunID       string `json:"child_run_id"`
	WorktreePath     string `json:"worktree_path"`
	RunStatus        string `json:"run_status"`
	DisplayStatus    string `json:"display_status"`
	ChangedWorkspace bool   `json:"changed_workspace"`
	HeadRef          string `json:"head_ref"`
	BranchName       string `json:"branch_name"`
}

type taskMultiChildLaunch struct {
	item   preparedTaskMultiItem
	index  int
	runID  string
	active bool
}

type taskMultiChildResult struct {
	launch taskMultiChildLaunch
	row    globaldb.Run
	err    error
}

type preparedTaskMulti struct {
	workspace        globaldb.Workspace
	mode             string
	workflowSlug     string
	presentationMode string
	items            []preparedTaskMultiItem
	worktrees        *TaskMultiWorktreeManifest
}

type preparedTaskMultiItem struct {
	slug         string
	selectedTask string
	workflowSlug string
	workflowID   *string
	workflowRoot string
	runtimeCfg   *model.RuntimeConfig
	worktree     TaskMultiWorktreeMetadata
}

type taskMultiSnapshotBuilder struct {
	items []apicore.TaskRunMultipleItem
	index map[string]int
}

// StartTaskRunMultiple starts one daemon-owned parent for an ordered task queue.
func (m *RunManager) StartTaskRunMultiple(
	ctx context.Context,
	workspaceRef string,
	req apicore.TaskRunMultipleRequest,
) (apicore.Run, error) {
	selectedTasks := req.SelectedTasks
	if len(selectedTasks) == 0 {
		selectedTasks = req.Slugs
	}
	selectedTasks, err := normalizeTaskMultiSlugs(selectedTasks)
	if err != nil {
		return apicore.Run{}, err
	}
	modeText := req.MultipleMode
	if strings.TrimSpace(modeText) == "" {
		modeText = req.Mode
	}
	mode, err := resolveTaskMultiMode(modeText)
	if err != nil {
		return apicore.Run{}, err
	}
	childOverrides, err := taskMultiChildRuntimeOverrides(req.RuntimeOverrides)
	if err != nil {
		return apicore.Run{}, err
	}
	prepared, err := m.prepareTaskMultiStart(detachContext(ctx), workspaceRef, selectedTasks, mode, req, childOverrides)
	if err != nil {
		return apicore.Run{}, err
	}
	runtimeCfg, err := taskMultiParentRuntimeConfig(
		req.RuntimeOverrides,
		prepared.workspace.RootDir,
		mode,
		selectedTasks,
	)
	if err != nil {
		return apicore.Run{}, err
	}
	return m.startRun(ctx, startRunSpec{
		workspace:        prepared.workspace,
		workflowSlug:     prepared.workflowSlug,
		mode:             runModeTaskMulti,
		presentationMode: prepared.presentationMode,
		runtimeCfg:       runtimeCfg,
		taskMulti:        prepared,
	})
}

// RunMultipleSnapshot reconstructs the ordered child state for a parent multi-run.
func (m *RunManager) RunMultipleSnapshot(ctx context.Context, runID string) (apicore.TaskRunMultipleSnapshot, error) {
	listCtx := detachContext(ctx)
	row, err := m.globalDB.GetRun(listCtx, strings.TrimSpace(runID))
	if err != nil {
		return apicore.TaskRunMultipleSnapshot{}, err
	}
	if row.Mode != runModeTaskMulti {
		return apicore.TaskRunMultipleSnapshot{}, apicore.NewProblem(
			http.StatusUnprocessableEntity,
			"run_not_task_multi",
			"run is not a multi-task parent",
			map[string]any{"run_id": row.RunID, "mode": row.Mode},
			nil,
		)
	}
	runView, err := m.toCoreRun(listCtx, row, "")
	if err != nil {
		return apicore.TaskRunMultipleSnapshot{}, err
	}

	lease, err := m.acquireRunDB(listCtx, row.RunID)
	if err != nil {
		return apicore.TaskRunMultipleSnapshot{}, err
	}
	defer func() {
		_ = lease.Close()
	}()
	eventRows, err := lease.DB().ListEvents(listCtx, 0, 0)
	if err != nil {
		return apicore.TaskRunMultipleSnapshot{}, err
	}
	builder := newTaskMultiSnapshotBuilder()
	for _, event := range eventRows.Events {
		if err := builder.applyEvent(event); err != nil {
			return apicore.TaskRunMultipleSnapshot{}, err
		}
	}
	return apicore.TaskRunMultipleSnapshot{
		Run:   runView,
		Items: builder.snapshotItems(),
	}, nil
}

func (m *RunManager) prepareTaskMultiStart(
	ctx context.Context,
	workspaceRef string,
	selectedTasks []string,
	mode string,
	req apicore.TaskRunMultipleRequest,
	childOverrides json.RawMessage,
) (*preparedTaskMulti, error) {
	items := make([]preparedTaskMultiItem, 0, len(selectedTasks))
	var workspaceRow globaldb.Workspace
	var presentationMode string
	workflowSlug := strings.TrimSpace(req.WorkflowSlug)
	for idx, selectedTask := range selectedTasks {
		slug := strings.TrimSpace(selectedTask)
		childWorkflowSlug := slug
		itemSelectedTask := ""
		if workflowSlug != "" {
			childWorkflowSlug = workflowSlug
			itemSelectedTask = strings.TrimSpace(selectedTask)
		}
		var childSelectedTasks []string
		if itemSelectedTask != "" {
			childSelectedTasks = []string{itemSelectedTask}
		}
		row, workflowID, runtimeCfg, childPresentationMode, err := m.prepareTaskStart(
			ctx,
			workspaceRef,
			childWorkflowSlug,
			apicore.TaskRunRequest{
				Workspace:        req.Workspace,
				PresentationMode: req.PresentationMode,
				RuntimeOverrides: childOverrides,
				MultipleMode:     mode,
				SelectedTasks:    childSelectedTasks,
			},
		)
		if err != nil {
			return nil, err
		}
		if idx == 0 {
			workspaceRow = row
			presentationMode = childPresentationMode
		}
		items = append(items, preparedTaskMultiItem{
			slug:         strings.TrimSpace(slug),
			selectedTask: itemSelectedTask,
			workflowSlug: strings.TrimSpace(childWorkflowSlug),
			workflowID:   cloneStringPtr(workflowID),
			workflowRoot: strings.TrimSpace(runtimeCfg.TasksDir),
			runtimeCfg:   runtimeCfg,
		})
	}
	if len(items) == 0 {
		return nil, taskMultiValidationProblem("slugs_required", "slugs is required", "slugs")
	}
	if strings.TrimSpace(presentationMode) == "" {
		var err error
		presentationMode, err = normalizePresentationMode(req.PresentationMode)
		if err != nil {
			return nil, err
		}
	}
	return &preparedTaskMulti{
		workspace:        workspaceRow,
		mode:             mode,
		workflowSlug:     workflowSlug,
		presentationMode: presentationMode,
		items:            items,
	}, nil
}

func taskMultiParentRuntimeConfig(
	raw json.RawMessage,
	workspaceRoot string,
	multipleMode string,
	selectedTasks []string,
) (*model.RuntimeConfig, error) {
	overrides, err := parseRuntimeOverrides(raw)
	if err != nil {
		return nil, err
	}
	runtimeCfg := &model.RuntimeConfig{
		WorkspaceRoot: strings.TrimSpace(workspaceRoot),
		Name:          taskMultiRunName,
		Mode:          model.ExecutionModePRDTasks,
		DaemonOwned:   true,
	}
	if overrides.RunID != nil {
		runtimeCfg.RunID = strings.TrimSpace(*overrides.RunID)
	}
	runtimeCfg.MultipleMode = strings.TrimSpace(multipleMode)
	runtimeCfg.SelectedTasks = append([]string(nil), selectedTasks...)
	runtimeCfg.ApplyDefaults()
	runtimeCfg.TUI = false
	runtimeCfg.EnableExecutableExtensions = false
	return runtimeCfg, nil
}

func taskMultiChildRuntimeOverrides(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	if _, err := parseRuntimeOverrides(raw); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, apicore.NewProblem(
			http.StatusUnprocessableEntity,
			"invalid_runtime_overrides",
			fmt.Sprintf("runtime_overrides: %v", err),
			nil,
			err,
		)
	}
	delete(fields, "run_id")
	if len(fields) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("marshal child runtime overrides: %w", err)
	}
	return encoded, nil
}

func normalizeTaskMultiSlugs(values []string) ([]string, error) {
	slugs, err := taskscore.ParseCommaSeparatedSlugs(strings.Join(values, ","))
	if err != nil {
		return nil, apicore.NewProblem(
			http.StatusUnprocessableEntity,
			"invalid_task_slugs",
			err.Error(),
			map[string]any{"field": "slugs"},
			err,
		)
	}
	return slugs, nil
}

func resolveTaskMultiMode(raw string) (string, error) {
	switch strings.TrimSpace(raw) {
	case "", workspacecfg.TaskRunMultipleModeSequential:
		return workspacecfg.TaskRunMultipleModeSequential, nil
	case workspacecfg.TaskRunMultipleModeEnqueued:
		return workspacecfg.TaskRunMultipleModeEnqueued, nil
	case workspacecfg.TaskRunMultipleModeParallel:
		return workspacecfg.TaskRunMultipleModeParallel, nil
	default:
		return "", taskMultiValidationProblem(
			"invalid_run_multiple_mode",
			"multiple mode must be sequential or parallel",
			"multiple_mode",
		)
	}
}

func taskMultiValidationProblem(code string, message string, field string) error {
	return apicore.NewProblem(
		http.StatusUnprocessableEntity,
		code,
		message,
		map[string]any{"field": field},
		nil,
	)
}

func (m *RunManager) executeTaskMultiRun(active *activeRun, row globaldb.Run) {
	scope := active.scope
	var fallback terminalState

	if err := context.Cause(active.ctx); err != nil {
		fallback = cancelledTerminalState(err)
		m.finishRun(active, row, fallback)
		return
	}
	if err := startScopeRuntime(active.ctx, scope); err != nil {
		fallback = fallbackTerminalState(scope.RunArtifacts(), err, active.cancelWasRequested())
		m.finishRun(active, row, fallback)
		return
	}

	row.Status = runStatusRunning
	updated, err := m.globalDB.UpdateRun(detachContext(active.ctx), row)
	if err != nil {
		fallback = failedTerminalState(scope.RunArtifacts(), err)
		m.finishRun(active, row, fallback)
		return
	}
	row = updated
	m.publishRunWorkspaceEvent(active.ctx, row, active.workflowSlug, apicore.WorkspaceEventKindRunStatusChanged)

	handoff, err := m.runTaskMultiCoordinator(active)
	fallback = fallbackTerminalState(scope.RunArtifacts(), err, active.cancelWasRequested())
	if err == nil {
		fallback = completedTerminalState(scope.RunArtifacts(), handoff.SummaryLine)
	}
	m.finishRun(active, row, fallback)
}

func (m *RunManager) runTaskMultiCoordinator(active *activeRun) (taskMultiHandoffArtifacts, error) {
	if active == nil || active.taskMulti == nil {
		return taskMultiHandoffArtifacts{}, errors.New("task multi run is not configured")
	}
	prepared := active.taskMulti
	total := len(prepared.items)
	if prepared.mode == workspacecfg.TaskRunMultipleModeParallel {
		if err := m.provisionTaskMultiWorktrees(active, prepared); err != nil {
			return taskMultiHandoffArtifacts{}, err
		}
	}
	if err := m.emitTaskMultiEvent(active, eventspkg.EventKindTaskRunMultipleStarted, kinds.TaskRunMultiplePayload{
		Mode:   prepared.mode,
		Status: runStatusRunning,
		Slugs:  preparedTaskMultiSlugs(prepared.items),
		Total:  total,
	}); err != nil {
		return taskMultiHandoffArtifacts{}, err
	}
	for idx := range prepared.items {
		item := prepared.items[idx]
		if err := m.emitTaskMultiItemEvent(
			active,
			eventspkg.EventKindTaskRunMultipleItemQueued,
			item,
			idx,
			total,
			taskMultiItemStatusQueued,
			"",
			"",
			"",
		); err != nil {
			return taskMultiHandoffArtifacts{}, err
		}
	}
	if prepared.mode == workspacecfg.TaskRunMultipleModeParallel {
		return m.runTaskMultiParallelChildren(active, prepared, total)
	}
	for idx := range prepared.items {
		item := prepared.items[idx]
		if err := context.Cause(active.ctx); err != nil {
			if emitErr := m.cancelTaskMultiQueuedItems(active, prepared.items, idx, total, err); emitErr != nil {
				return taskMultiHandoffArtifacts{}, errors.Join(err, emitErr)
			}
			return taskMultiHandoffArtifacts{}, err
		}
		if err := m.runTaskMultiChildAt(active, prepared, item, idx, total); err != nil {
			return taskMultiHandoffArtifacts{}, err
		}
	}
	if err := m.emitTaskMultiEvent(
		active,
		eventspkg.EventKindTaskRunMultipleQueueCompleted,
		kinds.TaskRunMultiplePayload{
			Mode:   prepared.mode,
			Status: runStatusCompleted,
			Slugs:  preparedTaskMultiSlugs(prepared.items),
			Total:  total,
		},
	); err != nil {
		return taskMultiHandoffArtifacts{}, err
	}
	return taskMultiHandoffArtifacts{SummaryLine: "multi-task queue completed"}, nil
}

func (m *RunManager) runTaskMultiParallelChildren(
	active *activeRun,
	prepared *preparedTaskMulti,
	total int,
) (taskMultiHandoffArtifacts, error) {
	launches, err := m.launchTaskMultiParallelChildren(active, prepared, total)
	if err != nil {
		return taskMultiHandoffArtifacts{}, err
	}
	aborted, terminalErr := m.waitForTaskMultiParallelChildren(active, prepared, launches, total)
	if aborted {
		return taskMultiHandoffArtifacts{}, terminalErr
	}

	handoff, handoffErr := m.writeTaskMultiHandoffArtifacts(active, prepared)
	if handoffErr != nil {
		return taskMultiHandoffArtifacts{}, errors.Join(terminalErr, handoffErr)
	}
	if terminalErr != nil {
		return handoff, terminalErr
	}
	if err := m.emitTaskMultiEvent(
		active,
		eventspkg.EventKindTaskRunMultipleQueueCompleted,
		kinds.TaskRunMultiplePayload{
			Mode:   prepared.mode,
			Status: runStatusCompleted,
			Slugs:  preparedTaskMultiSlugs(prepared.items),
			Total:  total,
		},
	); err != nil {
		return taskMultiHandoffArtifacts{}, err
	}
	return handoff, nil
}

func (m *RunManager) launchTaskMultiParallelChildren(
	active *activeRun,
	prepared *preparedTaskMulti,
	total int,
) ([]taskMultiChildLaunch, error) {
	launches := make([]taskMultiChildLaunch, len(prepared.items))
	for idx := range prepared.items {
		item := prepared.items[idx]
		if err := context.Cause(active.ctx); err != nil {
			cancelErr := m.cancelTaskMultiParallelOutstanding(active, prepared.items, launches, nil, err)
			return nil, errors.Join(err, cancelErr)
		}
		childRun, err := m.startTaskMultiChild(active, prepared, item, idx, total)
		if err != nil {
			emitErr := m.emitTaskMultiItemEvent(
				active,
				eventspkg.EventKindTaskRunMultipleChildFailed,
				item,
				idx,
				total,
				taskMultiItemStatusFailed,
				"",
				"",
				err.Error(),
			)
			cancelErr := m.cancelTaskMultiParallelOutstanding(
				active,
				prepared.items,
				launches,
				map[int]struct{}{idx: {}},
				err,
			)
			return nil, errors.Join(err, emitErr, cancelErr)
		}
		updateTaskMultiWorktreeChildRunID(prepared, item, childRun.RunID)
		launches[idx] = taskMultiChildLaunch{item: item, index: idx, runID: childRun.RunID, active: true}
	}
	return launches, nil
}

func (m *RunManager) waitForTaskMultiParallelChildren(
	active *activeRun,
	prepared *preparedTaskMulti,
	launches []taskMultiChildLaunch,
	total int,
) (bool, error) {
	finalized := make(map[int]struct{}, len(launches))
	results := make(chan taskMultiChildResult, len(launches))
	var waits sync.WaitGroup
	for idx := range launches {
		launch := launches[idx]
		waits.Add(1)
		go func(launch taskMultiChildLaunch) {
			defer waits.Done()
			childRow, err := m.waitForTaskMultiChild(active.ctx, launch.runID)
			results <- taskMultiChildResult{launch: launch, row: childRow, err: err}
		}(launch)
	}

	var terminalErr error
	aborted := false
	for range launches {
		result := <-results
		if aborted {
			if _, ok := finalized[result.launch.index]; ok {
				continue
			}
			if result.err != nil {
				finished, finishErr := m.finishTaskMultiTerminalChildIfAvailable(
					active,
					result.launch,
					total,
					finalized,
				)
				if finished {
					terminalErr = errors.Join(terminalErr, finishErr)
				}
				continue
			}
			terminalErr = errors.Join(
				terminalErr,
				m.finishTaskMultiFinishedChild(active, result.launch, total, result.row, finalized),
				taskMultiChildTerminalError(result.launch, result.row),
			)
			continue
		}
		if result.err != nil {
			finished, finishErr := m.finishTaskMultiTerminalChildIfAvailable(active, result.launch, total, finalized)
			if finished {
				terminalErr = errors.Join(terminalErr, finishErr)
				continue
			}
			cancelErr := m.cancelTaskMultiParallelOutstanding(active, prepared.items, launches, finalized, result.err)
			terminalErr = errors.Join(result.err, cancelErr)
			aborted = true
			continue
		}
		if err := m.finishTaskMultiFinishedChild(active, result.launch, total, result.row, finalized); err != nil {
			cancelErr := m.cancelTaskMultiParallelOutstanding(active, prepared.items, launches, finalized, err)
			terminalErr = errors.Join(err, cancelErr)
			aborted = true
			continue
		}
		terminalErr = errors.Join(terminalErr, taskMultiChildTerminalError(result.launch, result.row))
	}
	waits.Wait()
	if aborted {
		return true, terminalErr
	}
	return false, terminalErr
}

func (m *RunManager) finishTaskMultiTerminalChildIfAvailable(
	active *activeRun,
	launch taskMultiChildLaunch,
	total int,
	finalized map[int]struct{},
) (bool, error) {
	childRow, err := m.globalDB.GetRun(detachContext(active.ctx), launch.runID)
	if err != nil || !isTerminalRunStatus(childRow.Status) {
		return false, nil
	}
	return true, errors.Join(
		m.finishTaskMultiFinishedChild(active, launch, total, childRow, finalized),
		taskMultiChildTerminalError(launch, childRow),
	)
}

func (m *RunManager) finishTaskMultiFinishedChild(
	active *activeRun,
	launch taskMultiChildLaunch,
	total int,
	childRow globaldb.Run,
	finalized map[int]struct{},
) error {
	finalized[launch.index] = struct{}{}
	return m.finishTaskMultiChild(active, launch.item, launch.index, total, childRow)
}

func taskMultiChildTerminalError(launch taskMultiChildLaunch, childRow globaldb.Run) error {
	switch childRow.Status {
	case runStatusCompleted:
		return nil
	case runStatusCancelled:
		return fmt.Errorf(
			"task multi child run %s for %s was canceled: %s",
			childRow.RunID,
			launch.item.slug,
			childRow.ErrorText,
		)
	default:
		return fmt.Errorf(
			"task multi child run %s for %s ended with status %s: %s",
			childRow.RunID,
			launch.item.slug,
			childRow.Status,
			childRow.ErrorText,
		)
	}
}

func (m *RunManager) cancelTaskMultiParallelOutstanding(
	active *activeRun,
	items []preparedTaskMultiItem,
	launches []taskMultiChildLaunch,
	finalized map[int]struct{},
	cause error,
) error {
	if finalized == nil {
		finalized = make(map[int]struct{})
	}
	var joined error
	for idx := range launches {
		launch := launches[idx]
		if !launch.active {
			continue
		}
		if _, ok := finalized[launch.index]; ok {
			continue
		}
		childRow, err := m.globalDB.GetRun(detachContext(active.ctx), launch.runID)
		if err == nil && isTerminalRunStatus(childRow.Status) {
			joined = errors.Join(
				joined,
				m.finishTaskMultiChild(active, launch.item, launch.index, len(items), childRow),
			)
			finalized[launch.index] = struct{}{}
			continue
		}
		joined = errors.Join(joined, m.Cancel(detachContext(active.ctx), launch.runID))
	}
	for idx := range items {
		if _, ok := finalized[idx]; ok {
			continue
		}
		joined = errors.Join(joined, m.emitTaskMultiItemEvent(
			active,
			eventspkg.EventKindTaskRunMultipleItemCanceled,
			items[idx],
			idx,
			len(items),
			taskMultiItemStatusCanceled,
			"",
			launchesRunIDAt(launches, idx),
			errorString(cause),
		))
	}
	joined = errors.Join(
		joined,
		m.emitTaskMultiEvent(active, eventspkg.EventKindTaskRunMultipleQueueCanceled, kinds.TaskRunMultiplePayload{
			Mode:   active.taskMulti.mode,
			Status: taskMultiItemStatusCanceled,
			Slugs:  preparedTaskMultiSlugs(items),
			Total:  len(items),
			Error:  errorString(cause),
		}),
	)
	return joined
}

func launchesRunIDAt(launches []taskMultiChildLaunch, index int) string {
	if index < 0 || index >= len(launches) || !launches[index].active {
		return ""
	}
	return launches[index].runID
}

func (m *RunManager) provisionTaskMultiWorktrees(active *activeRun, prepared *preparedTaskMulti) error {
	if active == nil || prepared == nil {
		return errors.New("task multi worktree provisioning requires an active run")
	}
	selectedTasks := make([]string, 0, len(prepared.items))
	for idx := range prepared.items {
		item := &prepared.items[idx]
		selectedTask := strings.TrimSpace(item.selectedTask)
		if selectedTask == "" {
			selectedTask = strings.TrimSpace(item.slug)
		}
		selectedTasks = append(selectedTasks, selectedTask)
	}
	workflowSlug := strings.TrimSpace(prepared.workflowSlug)
	if workflowSlug == "" {
		workflowSlug = strings.TrimSpace(active.workflowSlug)
	}
	if workflowSlug == "" {
		workflowSlug = "multi-task"
	}
	manifest, err := newTaskMultiWorktreeProvisioner().Provision(active.ctx, taskMultiWorktreeRequest{
		ParentRunID:         active.runID,
		WorkflowSlug:        workflowSlug,
		SourceWorkspaceRoot: prepared.workspace.RootDir,
		SelectedTasks:       selectedTasks,
	})
	if err != nil {
		return err
	}
	prepared.worktrees = &manifest
	byTask := make(map[string]TaskMultiWorktreeMetadata, len(manifest.Worktrees))
	for idx := range manifest.Worktrees {
		metadata := manifest.Worktrees[idx]
		byTask[metadata.TaskName] = metadata
	}
	for idx := range prepared.items {
		item := &prepared.items[idx]
		selectedTask := strings.TrimSpace(item.selectedTask)
		if selectedTask == "" {
			selectedTask = strings.TrimSpace(item.slug)
		}
		metadata, ok := byTask[selectedTask]
		if !ok {
			return fmt.Errorf("missing worktree metadata for selected task %q", selectedTask)
		}
		workspaceRoot, err := mapTaskMultiWorktreePath(
			metadata.SourceRepositoryRoot,
			prepared.workspace.RootDir,
			metadata.WorktreePath,
		)
		if err != nil {
			return err
		}
		workflowRoot, err := mapTaskMultiWorktreePath(
			metadata.SourceRepositoryRoot,
			item.workflowRoot,
			metadata.WorktreePath,
		)
		if err != nil {
			return err
		}
		item.worktree = metadata
		item.workflowRoot = workflowRoot
		if item.runtimeCfg != nil {
			item.runtimeCfg.WorkspaceRoot = workspaceRoot
			item.runtimeCfg.TasksDir = workflowRoot
		}
	}
	return nil
}

func mapTaskMultiWorktreePath(sourceRoot string, sourcePath string, worktreeRoot string) (string, error) {
	resolvedSourceRoot, err := resolveTaskMultiComparablePath(sourceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve source workspace root: %w", err)
	}
	resolvedSourcePath, err := resolveTaskMultiComparablePath(sourcePath)
	if err != nil {
		return "", fmt.Errorf("resolve source workflow root: %w", err)
	}
	resolvedWorktreeRoot, err := resolveTaskMultiComparablePath(worktreeRoot)
	if err != nil {
		return "", fmt.Errorf("resolve child worktree root: %w", err)
	}
	rel, err := filepath.Rel(resolvedSourceRoot, resolvedSourcePath)
	if err != nil {
		return "", fmt.Errorf("map source workflow root into worktree: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf(
			"source workflow root %q must be inside source workspace root %q",
			resolvedSourcePath,
			resolvedSourceRoot,
		)
	}
	return filepath.Join(resolvedWorktreeRoot, rel), nil
}

func (m *RunManager) runTaskMultiChildAt(
	active *activeRun,
	prepared *preparedTaskMulti,
	item preparedTaskMultiItem,
	index int,
	total int,
) error {
	childRun, err := m.startTaskMultiChild(active, prepared, item, index, total)
	if err != nil {
		emitErr := m.emitTaskMultiItemEvent(
			active,
			eventspkg.EventKindTaskRunMultipleChildFailed,
			item,
			index,
			total,
			taskMultiItemStatusFailed,
			"",
			"",
			err.Error(),
		)
		cancelErr := m.cancelTaskMultiQueuedItems(active, prepared.items, index+1, total, err)
		return errors.Join(err, emitErr, cancelErr)
	}
	updateTaskMultiWorktreeChildRunID(prepared, item, childRun.RunID)
	childRow, err := m.waitForTaskMultiChild(active.ctx, childRun.RunID)
	if err != nil {
		var childCancelErr error
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			childCancelErr = m.Cancel(detachContext(active.ctx), childRun.RunID)
		}
		emitErr := m.emitTaskMultiItemEvent(
			active,
			eventspkg.EventKindTaskRunMultipleItemCanceled,
			item,
			index,
			total,
			taskMultiItemStatusCanceled,
			childRun.RunID,
			"",
			err.Error(),
		)
		cancelErr := m.cancelTaskMultiQueuedItems(active, prepared.items, index+1, total, err)
		return errors.Join(err, childCancelErr, emitErr, cancelErr)
	}
	if err := m.finishTaskMultiChild(active, item, index, total, childRow); err != nil {
		return err
	}
	if childRow.Status == runStatusCompleted {
		return nil
	}
	if childRow.Status == runStatusCancelled && active.cancelWasRequested() {
		cause := context.Cause(active.ctx)
		if cause == nil {
			cause = context.Canceled
		}
		if emitErr := m.cancelTaskMultiQueuedItems(active, prepared.items, index+1, total, cause); emitErr != nil {
			return errors.Join(cause, emitErr)
		}
		return cause
	}
	childErr := fmt.Errorf(
		"task multi child run %s for %s ended with status %s: %s",
		childRow.RunID,
		item.slug,
		childRow.Status,
		childRow.ErrorText,
	)
	if emitErr := m.cancelTaskMultiQueuedItems(active, prepared.items, index+1, total, childErr); emitErr != nil {
		return errors.Join(childErr, emitErr)
	}
	return childErr
}

func updateTaskMultiWorktreeChildRunID(prepared *preparedTaskMulti, item preparedTaskMultiItem, childRunID string) {
	if prepared == nil || prepared.worktrees == nil {
		return
	}
	taskName := strings.TrimSpace(item.selectedTask)
	if taskName == "" {
		taskName = strings.TrimSpace(item.slug)
	}
	if taskName == "" {
		return
	}
	for idx := range prepared.worktrees.Worktrees {
		if prepared.worktrees.Worktrees[idx].TaskName == taskName {
			prepared.worktrees.Worktrees[idx].ChildRunID = strings.TrimSpace(childRunID)
			return
		}
	}
}

func (m *RunManager) startTaskMultiChild(
	active *activeRun,
	prepared *preparedTaskMulti,
	item preparedTaskMultiItem,
	index int,
	total int,
) (apicore.Run, error) {
	runtimeCfg := item.runtimeCfg.Clone()
	if runtimeCfg == nil {
		return apicore.Run{}, errors.New("task multi child runtime config is required")
	}
	runtimeCfg.ParentRunID = active.runID
	childRun, err := m.startRun(active.ctx, startRunSpec{
		workspace:        prepared.workspace,
		workflowID:       cloneStringPtr(item.workflowID),
		workflowSlug:     item.workflowSlug,
		workflowRoot:     item.workflowRoot,
		mode:             runModeTask,
		presentationMode: prepared.presentationMode,
		parentRunID:      active.runID,
		runtimeCfg:       runtimeCfg,
	})
	if err != nil {
		return apicore.Run{}, err
	}
	if err := m.emitTaskMultiItemEvent(
		active,
		eventspkg.EventKindTaskRunMultipleChildStarted,
		item,
		index,
		total,
		taskMultiItemStatusRunning,
		"",
		childRun.RunID,
		"",
	); err != nil {
		cancelErr := m.Cancel(detachContext(active.ctx), childRun.RunID)
		return apicore.Run{}, errors.Join(err, cancelErr)
	}
	return childRun, nil
}

func (m *RunManager) finishTaskMultiChild(
	active *activeRun,
	item preparedTaskMultiItem,
	index int,
	total int,
	childRow globaldb.Run,
) error {
	displayStatus := m.taskMultiChildDisplayStatus(active.ctx, childRow)
	switch childRow.Status {
	case runStatusCompleted:
		return m.emitTaskMultiItemEvent(
			active,
			eventspkg.EventKindTaskRunMultipleChildCompleted,
			item,
			index,
			total,
			taskMultiItemStatusCompleted,
			displayStatus,
			childRow.RunID,
			"",
		)
	case runStatusCancelled:
		return m.emitTaskMultiItemEvent(
			active,
			eventspkg.EventKindTaskRunMultipleItemCanceled,
			item,
			index,
			total,
			taskMultiItemStatusCanceled,
			displayStatus,
			childRow.RunID,
			childRow.ErrorText,
		)
	default:
		return m.emitTaskMultiItemEvent(
			active,
			eventspkg.EventKindTaskRunMultipleChildFailed,
			item,
			index,
			total,
			taskMultiItemStatusFailed,
			displayStatus,
			childRow.RunID,
			childRow.ErrorText,
		)
	}
}

func (m *RunManager) taskMultiChildDisplayStatus(ctx context.Context, childRow globaldb.Run) string {
	switch childRow.Status {
	case runStatusCancelled:
		return taskMultiItemStatusCanceled
	case runStatusCompleted:
		if m.taskMultiChildWasUnchanged(ctx, childRow.RunID) {
			return taskMultiItemStatusUnchanged
		}
		return taskMultiItemStatusCompleted
	default:
		return taskMultiItemStatusFailed
	}
}

func (m *RunManager) taskMultiChildWasUnchanged(ctx context.Context, childRunID string) bool {
	lease, err := m.acquireRunDB(detachContext(ctx), strings.TrimSpace(childRunID))
	if err != nil {
		return false
	}
	defer func() {
		_ = lease.Close()
	}()
	eventRows, err := lease.DB().ListEvents(detachContext(ctx), 0, 0)
	if err != nil {
		return false
	}
	for _, event := range eventRows.Events {
		if event.Kind != eventspkg.EventKindTaskFileSkipped {
			continue
		}
		var payload kinds.TaskFileSkippedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		if payload.Reason == kinds.TaskFileSkippedReasonNoWorkspaceChanges {
			return true
		}
	}
	return false
}

func (m *RunManager) writeTaskMultiHandoffArtifacts(
	active *activeRun,
	prepared *preparedTaskMulti,
) (taskMultiHandoffArtifacts, error) {
	if active == nil || active.scope == nil || prepared == nil || prepared.worktrees == nil {
		return taskMultiHandoffArtifacts{}, errors.New("parallel task handoff requires prepared worktree metadata")
	}
	artifacts := active.scope.RunArtifacts()
	if err := os.MkdirAll(artifacts.RunDir, 0o755); err != nil {
		return taskMultiHandoffArtifacts{}, fmt.Errorf("create parallel handoff artifact directory: %w", err)
	}
	manifest := cloneTaskMultiWorktreeManifest(*prepared.worktrees)
	if err := writeTaskMultiJSONArtifact(artifacts.ParallelWorktreesPath, manifest); err != nil {
		return taskMultiHandoffArtifacts{}, err
	}
	summary, err := m.buildTaskMultiParentSummary(active, prepared, manifest, artifacts)
	if err != nil {
		return taskMultiHandoffArtifacts{}, err
	}
	if err := writeTaskMultiJSONArtifact(artifacts.ParallelSummaryPath, summary); err != nil {
		return taskMultiHandoffArtifacts{}, err
	}
	prompt := renderTaskMultiHandoffPrompt(summary)
	if err := os.WriteFile(artifacts.ParallelHandoffPath, []byte(prompt), 0o600); err != nil {
		return taskMultiHandoffArtifacts{}, fmt.Errorf("write parallel handoff artifact: %w", err)
	}
	return taskMultiHandoffArtifacts{
		HandoffPath:  artifacts.ParallelHandoffPath,
		SummaryPath:  artifacts.ParallelSummaryPath,
		WorktreePath: artifacts.ParallelWorktreesPath,
		Prompt:       prompt,
		SummaryLine:  renderTaskMultiFinalSummary(summary, prompt),
	}, nil
}

func (m *RunManager) buildTaskMultiParentSummary(
	active *activeRun,
	prepared *preparedTaskMulti,
	manifest TaskMultiWorktreeManifest,
	artifacts model.RunArtifacts,
) (taskMultiParentSummary, error) {
	metadataByTask := make(map[string]TaskMultiWorktreeMetadata, len(manifest.Worktrees))
	for idx := range manifest.Worktrees {
		metadata := manifest.Worktrees[idx]
		metadataByTask[metadata.TaskName] = metadata
	}
	outcomes := make([]taskMultiChildOutcome, 0, len(prepared.items))
	for idx := range prepared.items {
		item := prepared.items[idx]
		taskName := taskMultiItemTaskName(item)
		metadata, ok := metadataByTask[taskName]
		if !ok {
			return taskMultiParentSummary{}, fmt.Errorf("missing parallel worktree metadata for %s", taskName)
		}
		childRunID := strings.TrimSpace(metadata.ChildRunID)
		if childRunID == "" {
			return taskMultiParentSummary{}, fmt.Errorf("missing parallel child run id for %s", taskName)
		}
		childRow, err := m.globalDB.GetRun(detachContext(active.ctx), childRunID)
		if err != nil {
			return taskMultiParentSummary{}, fmt.Errorf("load parallel child run %s: %w", childRunID, err)
		}
		displayStatus := m.taskMultiChildDisplayStatus(active.ctx, childRow)
		outcomes = append(outcomes, taskMultiChildOutcome{
			TaskName:         taskName,
			ChildRunID:       childRunID,
			WorktreePath:     metadata.WorktreePath,
			RunStatus:        childRow.Status,
			DisplayStatus:    displayStatus,
			ChangedWorkspace: displayStatus != taskMultiItemStatusUnchanged,
			HeadRef:          metadata.BranchName,
			BranchName:       metadata.BranchName,
		})
	}
	return taskMultiParentSummary{
		WorkflowSlug:         strings.TrimSpace(manifest.WorkflowSlug),
		MultipleMode:         prepared.mode,
		SelectedTasks:        taskMultiSelectedTasks(prepared.items),
		SourceWorkspaceRoot:  strings.TrimSpace(manifest.SourceWorkspaceRoot),
		ChildOutcomes:        outcomes,
		HandoffPromptPath:    artifacts.ParallelHandoffPath,
		SummaryPath:          artifacts.ParallelSummaryPath,
		WorktreeManifestPath: artifacts.ParallelWorktreesPath,
	}, nil
}

func writeTaskMultiJSONArtifact(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal parallel handoff artifact %s: %w", path, err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("write parallel handoff artifact %s: %w", path, err)
	}
	return nil
}

func renderTaskMultiHandoffPrompt(summary taskMultiParentSummary) string {
	var builder strings.Builder
	builder.WriteString("Review the retained parallel task worktrees and prepare the fan-in plan.\n\n")
	fmt.Fprintf(&builder, "Workflow: %s\n", summary.WorkflowSlug)
	fmt.Fprintf(&builder, "Source workspace: %s\n", summary.SourceWorkspaceRoot)
	builder.WriteString("\nChild outcomes:\n")
	for _, outcome := range summary.ChildOutcomes {
		fmt.Fprintf(&builder, "- %s: %s (run=%s, worktree=%s, branch=%s)\n",
			outcome.TaskName,
			outcome.DisplayStatus,
			outcome.ChildRunID,
			outcome.WorktreePath,
			outcome.BranchName)
	}
	builder.WriteString("\nNext step: inspect each retained worktree, compare the changes, ")
	builder.WriteString("and from the source workspace prepare reviewable branches or pull-request work. ")
	builder.WriteString("Do not mutate the source workflow files until the fan-in decision is explicit.\n")
	return builder.String()
}

func renderTaskMultiFinalSummary(summary taskMultiParentSummary, prompt string) string {
	return fmt.Sprintf(
		"parallel handoff ready\nhandoff: %s\nsummary: %s\nworktrees: %s\nprompt:\n%s",
		summary.HandoffPromptPath,
		summary.SummaryPath,
		summary.WorktreeManifestPath,
		strings.TrimSpace(prompt),
	)
}

func cloneTaskMultiWorktreeManifest(manifest TaskMultiWorktreeManifest) TaskMultiWorktreeManifest {
	manifest.Worktrees = append([]TaskMultiWorktreeMetadata(nil), manifest.Worktrees...)
	return manifest
}

func taskMultiSelectedTasks(items []preparedTaskMultiItem) []string {
	selected := make([]string, 0, len(items))
	for idx := range items {
		selected = append(selected, taskMultiItemTaskName(items[idx]))
	}
	return selected
}

func taskMultiItemTaskName(item preparedTaskMultiItem) string {
	if selectedTask := strings.TrimSpace(item.selectedTask); selectedTask != "" {
		return selectedTask
	}
	return strings.TrimSpace(item.slug)
}

func (m *RunManager) waitForTaskMultiChild(ctx context.Context, runID string) (globaldb.Run, error) {
	trimmedRunID := strings.TrimSpace(runID)
	ticker := time.NewTicker(taskMultiChildPollInterval)
	defer ticker.Stop()

	for {
		row, err := m.globalDB.GetRun(detachContext(ctx), trimmedRunID)
		if err != nil {
			return globaldb.Run{}, fmt.Errorf("load child run %s: %w", trimmedRunID, err)
		}
		if isTerminalRunStatus(row.Status) {
			return row, nil
		}
		select {
		case <-ctx.Done():
			if cancelErr := m.Cancel(detachContext(ctx), runID); cancelErr != nil {
				return globaldb.Run{}, errors.Join(ctx.Err(), fmt.Errorf("cancel child run %s: %w", runID, cancelErr))
			}
			return globaldb.Run{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *RunManager) cancelTaskMultiQueuedItems(
	active *activeRun,
	items []preparedTaskMultiItem,
	startIndex int,
	total int,
	cause error,
) error {
	var err error
	for idx := startIndex; idx < len(items); idx++ {
		item := items[idx]
		err = errors.Join(err, m.emitTaskMultiItemEvent(
			active,
			eventspkg.EventKindTaskRunMultipleItemCanceled,
			item,
			idx,
			total,
			taskMultiItemStatusCanceled,
			"",
			"",
			errorString(cause),
		))
	}
	err = errors.Join(
		err,
		m.emitTaskMultiEvent(active, eventspkg.EventKindTaskRunMultipleQueueCanceled, kinds.TaskRunMultiplePayload{
			Mode:   active.taskMulti.mode,
			Status: taskMultiItemStatusCanceled,
			Slugs:  preparedTaskMultiSlugs(items),
			Total:  total,
			Error:  errorString(cause),
		}),
	)
	return err
}

func (m *RunManager) emitTaskMultiItemEvent(
	active *activeRun,
	kind eventspkg.EventKind,
	item preparedTaskMultiItem,
	index int,
	total int,
	status string,
	displayStatus string,
	childRunID string,
	errorText string,
) error {
	return m.emitTaskMultiEvent(active, kind, kinds.TaskRunMultiplePayload{
		Slug:          item.slug,
		SelectedTask:  strings.TrimSpace(item.selectedTask),
		Index:         index,
		Total:         total,
		Status:        status,
		DisplayStatus: strings.TrimSpace(displayStatus),
		ChildRunID:    strings.TrimSpace(childRunID),
		Error:         strings.TrimSpace(errorText),
	})
}

func (m *RunManager) emitTaskMultiEvent(
	active *activeRun,
	kind eventspkg.EventKind,
	payload kinds.TaskRunMultiplePayload,
) error {
	if active == nil || active.scope == nil || active.scope.RunJournal() == nil {
		return nil
	}
	payload.RunID = active.runID
	if payload.Mode == "" && active.taskMulti != nil {
		payload.Mode = active.taskMulti.mode
	}
	if err := submitSyntheticEvent(
		detachContext(active.ctx),
		active.scope.RunJournal(),
		active.runID,
		kind,
		payload,
	); err != nil {
		return err
	}
	m.publishWorkspaceEvent(active.ctx, apicore.WorkspaceEvent{
		WorkspaceID: active.workspaceID,
		RunID:       active.runID,
		Mode:        active.mode,
		Status:      runStatusRunning,
		Kind:        apicore.WorkspaceEventKindRunStatusChanged,
	})
	return nil
}

func preparedTaskMultiSlugs(items []preparedTaskMultiItem) []string {
	slugs := make([]string, 0, len(items))
	for idx := range items {
		slugs = append(slugs, items[idx].slug)
	}
	return slugs
}

func newTaskMultiSnapshotBuilder() *taskMultiSnapshotBuilder {
	return &taskMultiSnapshotBuilder{
		index: make(map[string]int),
	}
}

func (b *taskMultiSnapshotBuilder) applyEvent(event eventspkg.Event) error {
	switch event.Kind {
	case eventspkg.EventKindTaskRunMultipleStarted:
		payload, err := decodeTaskMultiPayload(event)
		if err != nil {
			return err
		}
		for _, slug := range payload.Slugs {
			b.ensureItem(slug).Status = taskMultiItemStatusQueued
		}
	case eventspkg.EventKindTaskRunMultipleItemQueued,
		eventspkg.EventKindTaskRunMultipleChildStarted,
		eventspkg.EventKindTaskRunMultipleChildCompleted,
		eventspkg.EventKindTaskRunMultipleChildFailed,
		eventspkg.EventKindTaskRunMultipleItemCanceled:
		payload, err := decodeTaskMultiPayload(event)
		if err != nil {
			return err
		}
		item := b.ensureItem(payloadItemKey(payload))
		item.Slug = strings.TrimSpace(payload.Slug)
		item.SelectedTask = strings.TrimSpace(payload.SelectedTask)
		item.Status = strings.TrimSpace(payload.Status)
		item.DisplayStatus = strings.TrimSpace(payload.DisplayStatus)
		if childRunID := strings.TrimSpace(payload.ChildRunID); childRunID != "" {
			item.RunID = childRunID
		}
		if errorText := strings.TrimSpace(payload.Error); errorText != "" {
			item.ErrorText = errorText
		}
	}
	return nil
}

func decodeTaskMultiPayload(event eventspkg.Event) (kinds.TaskRunMultiplePayload, error) {
	var payload kinds.TaskRunMultiplePayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return kinds.TaskRunMultiplePayload{}, fmt.Errorf("daemon: decode %s payload: %w", event.Kind, err)
	}
	return payload, nil
}

func payloadItemKey(payload kinds.TaskRunMultiplePayload) string {
	if selectedTask := strings.TrimSpace(payload.SelectedTask); selectedTask != "" {
		return selectedTask
	}
	return strings.TrimSpace(payload.Slug)
}

func (b *taskMultiSnapshotBuilder) ensureItem(slug string) *apicore.TaskRunMultipleItem {
	trimmed := strings.TrimSpace(slug)
	if idx, ok := b.index[trimmed]; ok {
		return &b.items[idx]
	}
	b.items = append(b.items, apicore.TaskRunMultipleItem{
		Slug:   trimmed,
		Status: taskMultiItemStatusQueued,
	})
	idx := len(b.items) - 1
	b.index[trimmed] = idx
	return &b.items[idx]
}

func (b *taskMultiSnapshotBuilder) snapshotItems() []apicore.TaskRunMultipleItem {
	return append([]apicore.TaskRunMultipleItem(nil), b.items...)
}
