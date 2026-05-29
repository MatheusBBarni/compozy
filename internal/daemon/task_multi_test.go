package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apicore "github.com/compozy/compozy/internal/api/core"
	"github.com/compozy/compozy/internal/core/model"
	"github.com/compozy/compozy/internal/store/globaldb"
	eventspkg "github.com/compozy/compozy/pkg/compozy/events"
	"github.com/compozy/compozy/pkg/compozy/events/kinds"
)

func TestRunManagerTaskRunMultipleRunsChildrenSequentially(t *testing.T) {
	t.Parallel()

	t.Run("Should run children sequentially", func(t *testing.T) {
		started := make(chan string, 2)
		releaseAlpha := make(chan struct{})
		env := newRunManagerTestEnv(t, runManagerTestDeps{
			buildRunID: taskMultiRunIDBuilder("task-multi-sequential"),
			prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
				return &model.SolvePreparation{}, nil
			},
			execute: func(ctx context.Context, _ *model.SolvePreparation, cfg *model.RuntimeConfig) error {
				started <- cfg.Name + ":" + cfg.RunID
				if cfg.Name != "alpha" {
					return nil
				}
				select {
				case <-releaseAlpha:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		})
		writeTaskMultiWorkflow(t, env, "alpha", "pending")
		writeTaskMultiWorkflow(t, env, "beta", "pending")

		parent := startTaskMultiRun(t, env, "task-multi-sequential", []string{"alpha", "beta"})
		if parent.Mode != runModeTaskMulti {
			t.Fatalf("parent mode = %q, want %q", parent.Mode, runModeTaskMulti)
		}
		waitForString(t, started, "alpha:child-alpha")
		waitForCondition(t, 5*time.Second, "task_multi active-run accounting", func() bool {
			counts := env.manager.ActiveRunCountsByMode()
			return env.manager.ActiveRunCount() == 2 && counts[runModeTaskMulti] == 1 && counts[runModeTask] == 1
		})
		assertNoTaskMultiStart(t, started, "beta before alpha completes")

		close(releaseAlpha)
		waitForString(t, started, "beta:child-beta")
		parentRow := waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool {
			return row.Status == runStatusCompleted
		})
		if parentRow.Mode != runModeTaskMulti {
			t.Fatalf("parent row mode = %q, want %q", parentRow.Mode, runModeTaskMulti)
		}

		alpha := requireTaskMultiChildRow(t, env, "child-alpha", parent.RunID, runStatusCompleted)
		beta := requireTaskMultiChildRow(t, env, "child-beta", parent.RunID, runStatusCompleted)
		if alpha.Mode != runModeTask || beta.Mode != runModeTask {
			t.Fatalf("child modes = %q/%q, want %q", alpha.Mode, beta.Mode, runModeTask)
		}

		runs, err := env.manager.List(
			context.Background(),
			apicore.RunListQuery{Workspace: env.workspaceRoot, Limit: 10},
		)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(runs) != 3 {
			t.Fatalf("run count = %d, want parent plus two children", len(runs))
		}
		if !hasRunMode(runs, runModeTaskMulti) {
			t.Fatalf("runs missing %q mode: %#v", runModeTaskMulti, runs)
		}

		snapshot, err := env.manager.RunMultipleSnapshot(context.Background(), parent.RunID)
		if err != nil {
			t.Fatalf("RunMultipleSnapshot() error = %v", err)
		}
		wantItems := []apicore.TaskRunMultipleItem{
			{Slug: "alpha", Status: taskMultiItemStatusCompleted, RunID: "child-alpha"},
			{Slug: "beta", Status: taskMultiItemStatusCompleted, RunID: "child-beta"},
		}
		assertTaskMultiItems(t, snapshot.Items, wantItems)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleStarted)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleItemQueued)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleChildStarted)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleChildCompleted)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleQueueCompleted)
	})
}

func TestRunManagerParallelChildPreservesSourceWorkflowFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}

	env := newRunManagerTestEnv(t, runManagerTestDeps{
		buildRunID: taskMultiRunIDBuilder("task-multi-source-protection"),
		prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
			return &model.SolvePreparation{}, nil
		},
		execute: func(_ context.Context, _ *model.SolvePreparation, cfg *model.RuntimeConfig) error {
			return os.WriteFile(filepath.Join(cfg.WorkspaceRoot, "child-output.txt"), []byte("child output"), 0o600)
		},
	})
	taskPath := env.writeWorkflowFile(t, env.workflowSlug, "task_01.md", daemonTaskBody("pending", "Protected task"))
	metaPath := env.writeWorkflowFile(t, env.workflowSlug, "_meta.md", daemonLegacyWorkflowMetaBody())
	tasksPath := env.writeWorkflowFile(t, env.workflowSlug, "_tasks.md", "Legacy generated summary\n")
	sourceBefore := readFilesByPath(t, taskPath, metaPath, tasksPath)
	initAndCommitTaskMultiWorkspace(t, env.workspaceRoot)

	parent, err := env.manager.StartTaskRunMultiple(
		context.Background(),
		env.workspaceRoot,
		apicore.TaskRunMultipleRequest{
			Workspace:        env.workspaceRoot,
			WorkflowSlug:     env.workflowSlug,
			SelectedTasks:    []string{"task_01"},
			Mode:             "parallel",
			PresentationMode: defaultPresentationMode,
			RuntimeOverrides: rawJSON(t, `{"run_id":"task-multi-source-protection"}`),
		},
	)
	if err != nil {
		t.Fatalf("StartTaskRunMultiple(parallel) error = %v", err)
	}
	waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool {
		return row.Status == runStatusCompleted
	})

	assertFilesEqual(t, sourceBefore, taskPath, metaPath, tasksPath)
	snapshot, err := env.manager.RunMultipleSnapshot(context.Background(), parent.RunID)
	if err != nil {
		t.Fatalf("RunMultipleSnapshot() error = %v", err)
	}
	wantItems := []apicore.TaskRunMultipleItem{
		{
			Slug:          "task_01",
			SelectedTask:  "task_01",
			Status:        taskMultiItemStatusCompleted,
			DisplayStatus: taskMultiItemStatusCompleted,
			RunID:         "child-daemon-workflow",
		},
	}
	assertTaskMultiItems(t, snapshot.Items, wantItems)
}

func TestRunManagerParallelParentWritesHandoffArtifacts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}

	parentRunID := "task-multi-handoff-artifacts"
	env := newRunManagerTestEnv(t, runManagerTestDeps{
		buildRunID: func(cfg *model.RuntimeConfig) (string, error) {
			if cfg == nil {
				return "", errors.New("runtime config is required")
			}
			if runID := strings.TrimSpace(cfg.RunID); runID != "" {
				return runID, nil
			}
			if strings.TrimSpace(cfg.ParentRunID) == parentRunID && len(cfg.SelectedTasks) == 1 {
				return "child-" + strings.TrimSpace(cfg.SelectedTasks[0]), nil
			}
			return "generated-" + strings.TrimSpace(cfg.Name), nil
		},
		prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
			return &model.SolvePreparation{}, nil
		},
		execute: func(_ context.Context, _ *model.SolvePreparation, cfg *model.RuntimeConfig) error {
			return os.WriteFile(
				filepath.Join(cfg.WorkspaceRoot, "output-"+cfg.SelectedTasks[0]+".txt"),
				[]byte("child output"),
				0o600,
			)
		},
	})
	taskOnePath := env.writeWorkflowFile(t, env.workflowSlug, "task_01.md", daemonTaskBody("pending", "Task one"))
	taskTwoPath := env.writeWorkflowFile(t, env.workflowSlug, "task_02.md", daemonTaskBody("pending", "Task two"))
	sourceBefore := readFilesByPath(t, taskOnePath, taskTwoPath)
	initAndCommitTaskMultiWorkspace(t, env.workspaceRoot)

	parent, err := env.manager.StartTaskRunMultiple(
		context.Background(),
		env.workspaceRoot,
		apicore.TaskRunMultipleRequest{
			Workspace:        env.workspaceRoot,
			WorkflowSlug:     env.workflowSlug,
			SelectedTasks:    []string{"task_01", "task_02"},
			Mode:             "parallel",
			PresentationMode: defaultPresentationMode,
			RuntimeOverrides: rawJSON(t, fmt.Sprintf(`{"run_id":%q}`, parentRunID)),
		},
	)
	if err != nil {
		t.Fatalf("StartTaskRunMultiple(parallel) error = %v", err)
	}
	waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool {
		return row.Status == runStatusCompleted
	})
	assertFilesEqual(t, sourceBefore, taskOnePath, taskTwoPath)

	artifacts, err := model.ResolveHomeRunArtifacts(parent.RunID)
	if err != nil {
		t.Fatalf("ResolveHomeRunArtifacts() error = %v", err)
	}
	var summary taskMultiParentSummary
	readJSONFile(t, artifacts.ParallelSummaryPath, &summary)
	if summary.WorkflowSlug != env.workflowSlug || summary.MultipleMode != "parallel" {
		t.Fatalf("summary identity = %#v", summary)
	}
	if !slices.Equal(summary.SelectedTasks, []string{"task_01", "task_02"}) {
		t.Fatalf("summary selected tasks = %#v", summary.SelectedTasks)
	}
	if len(summary.ChildOutcomes) != 2 {
		t.Fatalf("child outcomes = %#v", summary.ChildOutcomes)
	}
	for _, outcome := range summary.ChildOutcomes {
		if outcome.ChildRunID == "" || outcome.WorktreePath == "" || outcome.BranchName == "" {
			t.Fatalf("incomplete child outcome: %#v", outcome)
		}
		if outcome.RunStatus != runStatusCompleted || outcome.DisplayStatus != taskMultiItemStatusCompleted {
			t.Fatalf("unexpected outcome status: %#v", outcome)
		}
		if !outcome.ChangedWorkspace {
			t.Fatalf("ChangedWorkspace = false for changed child outcome: %#v", outcome)
		}
	}
	var manifest TaskMultiWorktreeManifest
	readJSONFile(t, artifacts.ParallelWorktreesPath, &manifest)
	if len(manifest.Worktrees) != 2 {
		t.Fatalf("manifest worktrees = %#v", manifest.Worktrees)
	}
	for _, metadata := range manifest.Worktrees {
		if metadata.TaskName == "" || metadata.ChildRunID == "" || metadata.WorktreePath == "" {
			t.Fatalf("incomplete worktree metadata: %#v", metadata)
		}
		if strings.HasPrefix(metadata.WorktreePath, env.workspaceRoot) {
			t.Fatalf("worktree path %q is inside source workspace %q", metadata.WorktreePath, env.workspaceRoot)
		}
	}
	handoffBytes, err := os.ReadFile(artifacts.ParallelHandoffPath)
	if err != nil {
		t.Fatalf("read handoff artifact: %v", err)
	}
	handoff := string(handoffBytes)
	if !strings.Contains(handoff, "Review the retained parallel task worktrees") ||
		!strings.Contains(handoff, "task_01") ||
		!strings.Contains(handoff, "task_02") {
		t.Fatalf("handoff artifact missing expected prompt content:\n%s", handoff)
	}
	completedEvent := requireRunEvent(t, parent.RunID, eventspkg.EventKindRunCompleted)
	var payload kinds.RunCompletedPayload
	if err := json.Unmarshal(completedEvent.Payload, &payload); err != nil {
		t.Fatalf("decode completed payload: %v", err)
	}
	if !strings.Contains(payload.SummaryMessage, artifacts.ParallelHandoffPath) ||
		!strings.Contains(payload.SummaryMessage, "prompt:") {
		t.Fatalf("summary message missing handoff pointer/prompt:\n%s", payload.SummaryMessage)
	}
}

func TestRunManagerParallelTaskRunMultipleLaunchesOneChildPerSelectedTask(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}

	parentRunID := "task-multi-parallel-launch"
	started := make(chan string, 2)
	releaseFirst := make(chan struct{})
	env := newRunManagerTestEnv(t, runManagerTestDeps{
		buildRunID: selectedTaskRunIDBuilder(parentRunID),
		prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
			return &model.SolvePreparation{}, nil
		},
		execute: func(ctx context.Context, _ *model.SolvePreparation, cfg *model.RuntimeConfig) error {
			taskName := cfg.SelectedTasks[0]
			started <- taskName + ":" + cfg.RunID
			if taskName == "task_01" {
				select {
				case <-releaseFirst:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		},
	})
	env.writeWorkflowFile(t, env.workflowSlug, "task_01.md", daemonTaskBody("pending", "Task one"))
	env.writeWorkflowFile(t, env.workflowSlug, "task_02.md", daemonTaskBody("pending", "Task two"))
	initAndCommitTaskMultiWorkspace(t, env.workspaceRoot)

	parent, err := env.manager.StartTaskRunMultiple(
		context.Background(),
		env.workspaceRoot,
		apicore.TaskRunMultipleRequest{
			Workspace:        env.workspaceRoot,
			WorkflowSlug:     env.workflowSlug,
			SelectedTasks:    []string{"task_01", "task_02"},
			Mode:             "parallel",
			PresentationMode: defaultPresentationMode,
			RuntimeOverrides: rawJSON(t, fmt.Sprintf(`{"run_id":%q}`, parentRunID)),
		},
	)
	if err != nil {
		t.Fatalf("StartTaskRunMultiple(parallel) error = %v", err)
	}
	waitForString(t, started, "task_01:child-task_01")
	waitForString(t, started, "task_02:child-task_02")
	waitForRun(t, env.globalDB, "child-task_02", func(row globaldb.Run) bool {
		return row.Status == runStatusCompleted
	})
	waitForCondition(t, 5*time.Second, "parent snapshot to observe out-of-order child completion", func() bool {
		snapshot, snapshotErr := env.manager.RunMultipleSnapshot(context.Background(), parent.RunID)
		if snapshotErr != nil || len(snapshot.Items) != 2 {
			return false
		}
		return snapshot.Items[0].Status == taskMultiItemStatusRunning &&
			snapshot.Items[1].Status == taskMultiItemStatusCompleted
	})
	close(releaseFirst)

	waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool {
		return row.Status == runStatusCompleted
	})
	requireTaskMultiChildRow(t, env, "child-task_01", parent.RunID, runStatusCompleted)
	requireTaskMultiChildRow(t, env, "child-task_02", parent.RunID, runStatusCompleted)
	snapshot, err := env.manager.RunMultipleSnapshot(context.Background(), parent.RunID)
	if err != nil {
		t.Fatalf("RunMultipleSnapshot() error = %v", err)
	}
	wantItems := []apicore.TaskRunMultipleItem{
		{Slug: "task_01", SelectedTask: "task_01", Status: taskMultiItemStatusCompleted, RunID: "child-task_01"},
		{Slug: "task_02", SelectedTask: "task_02", Status: taskMultiItemStatusCompleted, RunID: "child-task_02"},
	}
	assertTaskMultiItems(t, snapshot.Items, wantItems)
}

func TestRunManagerParallelTaskRunMultipleAggregatesMixedChildOutcomes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}

	parentRunID := "task-multi-parallel-mixed"
	env := newRunManagerTestEnv(t, runManagerTestDeps{
		buildRunID: selectedTaskRunIDBuilder(parentRunID),
		prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
			return &model.SolvePreparation{}, nil
		},
		execute: func(_ context.Context, _ *model.SolvePreparation, cfg *model.RuntimeConfig) error {
			if cfg.SelectedTasks[0] == "task_02" {
				return errors.New("task_02 failed")
			}
			return os.WriteFile(
				filepath.Join(cfg.WorkspaceRoot, "output-"+cfg.SelectedTasks[0]+".txt"),
				[]byte("child output"),
				0o600,
			)
		},
	})
	env.writeWorkflowFile(t, env.workflowSlug, "task_01.md", daemonTaskBody("pending", "Task one"))
	env.writeWorkflowFile(t, env.workflowSlug, "task_02.md", daemonTaskBody("pending", "Task two"))
	env.writeWorkflowFile(t, env.workflowSlug, "task_03.md", daemonTaskBody("pending", "Task three"))
	initAndCommitTaskMultiWorkspace(t, env.workspaceRoot)

	parent, err := env.manager.StartTaskRunMultiple(
		context.Background(),
		env.workspaceRoot,
		apicore.TaskRunMultipleRequest{
			Workspace:        env.workspaceRoot,
			WorkflowSlug:     env.workflowSlug,
			SelectedTasks:    []string{"task_01", "task_02", "task_03"},
			Mode:             "parallel",
			PresentationMode: defaultPresentationMode,
			RuntimeOverrides: rawJSON(t, fmt.Sprintf(`{"run_id":%q}`, parentRunID)),
		},
	)
	if err != nil {
		t.Fatalf("StartTaskRunMultiple(parallel) error = %v", err)
	}
	parentRow := waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool {
		return row.Status == runStatusFailed
	})
	if !strings.Contains(parentRow.ErrorText, "task_02 failed") {
		t.Fatalf("parent error = %q, want failed child error", parentRow.ErrorText)
	}
	requireTaskMultiChildRow(t, env, "child-task_01", parent.RunID, runStatusCompleted)
	requireTaskMultiChildRow(t, env, "child-task_02", parent.RunID, runStatusFailed)
	requireTaskMultiChildRow(t, env, "child-task_03", parent.RunID, runStatusCompleted)

	snapshot, err := env.manager.RunMultipleSnapshot(context.Background(), parent.RunID)
	if err != nil {
		t.Fatalf("RunMultipleSnapshot() error = %v", err)
	}
	wantItems := []apicore.TaskRunMultipleItem{
		{
			Slug:          "task_01",
			SelectedTask:  "task_01",
			Status:        taskMultiItemStatusCompleted,
			DisplayStatus: taskMultiItemStatusCompleted,
			RunID:         "child-task_01",
		},
		{
			Slug:          "task_02",
			SelectedTask:  "task_02",
			Status:        taskMultiItemStatusFailed,
			DisplayStatus: taskMultiItemStatusFailed,
			RunID:         "child-task_02",
			ErrorText:     "task_02 failed",
		},
		{
			Slug:          "task_03",
			SelectedTask:  "task_03",
			Status:        taskMultiItemStatusCompleted,
			DisplayStatus: taskMultiItemStatusCompleted,
			RunID:         "child-task_03",
		},
	}
	assertTaskMultiItems(t, snapshot.Items, wantItems)

	artifacts, err := model.ResolveHomeRunArtifacts(parent.RunID)
	if err != nil {
		t.Fatalf("ResolveHomeRunArtifacts() error = %v", err)
	}
	var summary taskMultiParentSummary
	readJSONFile(t, artifacts.ParallelSummaryPath, &summary)
	gotStatuses := make([]string, 0, len(summary.ChildOutcomes))
	for _, outcome := range summary.ChildOutcomes {
		gotStatuses = append(gotStatuses, outcome.DisplayStatus)
	}
	wantStatuses := []string{taskMultiItemStatusCompleted, taskMultiItemStatusFailed, taskMultiItemStatusCompleted}
	if !slices.Equal(gotStatuses, wantStatuses) {
		t.Fatalf("summary display statuses = %#v, want %#v", gotStatuses, wantStatuses)
	}
	failedEvent := requireRunEvent(t, parent.RunID, eventspkg.EventKindRunFailed)
	var failedPayload kinds.RunFailedPayload
	if err := json.Unmarshal(failedEvent.Payload, &failedPayload); err != nil {
		t.Fatalf("decode failed payload: %v", err)
	}
	if !strings.Contains(failedPayload.SummaryMessage, artifacts.ParallelHandoffPath) ||
		!strings.Contains(failedPayload.SummaryMessage, "prompt:") {
		t.Fatalf("failed summary missing handoff pointer/prompt:\n%s", failedPayload.SummaryMessage)
	}
}

func TestRunManagerParallelTaskRunMultipleCancellationPreservesTerminalChildren(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}

	parentRunID := "task-multi-parallel-cancel"
	started := make(chan string, 2)
	env := newRunManagerTestEnv(t, runManagerTestDeps{
		buildRunID: selectedTaskRunIDBuilder(parentRunID),
		prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
			return &model.SolvePreparation{}, nil
		},
		execute: func(ctx context.Context, _ *model.SolvePreparation, cfg *model.RuntimeConfig) error {
			taskName := cfg.SelectedTasks[0]
			started <- taskName + ":" + cfg.RunID
			if taskName == "task_02" {
				return nil
			}
			<-ctx.Done()
			return ctx.Err()
		},
	})
	env.writeWorkflowFile(t, env.workflowSlug, "task_01.md", daemonTaskBody("pending", "Task one"))
	env.writeWorkflowFile(t, env.workflowSlug, "task_02.md", daemonTaskBody("pending", "Task two"))
	initAndCommitTaskMultiWorkspace(t, env.workspaceRoot)

	parent, err := env.manager.StartTaskRunMultiple(
		context.Background(),
		env.workspaceRoot,
		apicore.TaskRunMultipleRequest{
			Workspace:        env.workspaceRoot,
			WorkflowSlug:     env.workflowSlug,
			SelectedTasks:    []string{"task_01", "task_02"},
			Mode:             "parallel",
			PresentationMode: defaultPresentationMode,
			RuntimeOverrides: rawJSON(t, fmt.Sprintf(`{"run_id":%q}`, parentRunID)),
		},
	)
	if err != nil {
		t.Fatalf("StartTaskRunMultiple(parallel) error = %v", err)
	}
	waitForString(t, started, "task_01:child-task_01")
	waitForString(t, started, "task_02:child-task_02")
	waitForRun(
		t,
		env.globalDB,
		"child-task_02",
		func(row globaldb.Run) bool { return row.Status == runStatusCompleted },
	)
	if err := env.manager.Cancel(context.Background(), parent.RunID); err != nil {
		t.Fatalf("Cancel(parent) error = %v", err)
	}
	waitForRun(
		t,
		env.globalDB,
		"child-task_01",
		func(row globaldb.Run) bool { return row.Status == runStatusCancelled },
	)
	waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool { return row.Status == runStatusCancelled })
	snapshot, err := env.manager.RunMultipleSnapshot(context.Background(), parent.RunID)
	if err != nil {
		t.Fatalf("RunMultipleSnapshot() error = %v", err)
	}
	wantItems := []apicore.TaskRunMultipleItem{
		{Slug: "task_01", SelectedTask: "task_01", Status: taskMultiItemStatusCanceled, RunID: "child-task_01"},
		{Slug: "task_02", SelectedTask: "task_02", Status: taskMultiItemStatusCompleted, RunID: "child-task_02"},
	}
	assertTaskMultiItems(t, snapshot.Items, wantItems)
}

func TestRunManagerParallelChildOutcomeReportsUnchanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}

	env := newRunManagerTestEnv(t, runManagerTestDeps{
		buildRunID: taskMultiRunIDBuilder("task-multi-unchanged"),
		prepare: func(ctx context.Context, cfg *model.RuntimeConfig, scope model.RunScope) (*model.SolvePreparation, error) {
			if strings.TrimSpace(cfg.ParentRunID) != "" {
				submitEvent(
					ctx,
					t,
					scope.RunJournal(),
					cfg.RunID,
					eventspkg.EventKindTaskFileSkipped,
					kinds.TaskFileSkippedPayload{
						TasksDir:        cfg.TasksDir,
						TaskName:        "task_01.md",
						FilePath:        filepath.Join(cfg.TasksDir, "task_01.md"),
						PreservedStatus: "pending",
						Reason:          kinds.TaskFileSkippedReasonNoWorkspaceChanges,
					},
				)
			}
			return &model.SolvePreparation{}, nil
		},
		execute: func(context.Context, *model.SolvePreparation, *model.RuntimeConfig) error { return nil },
	})
	env.writeWorkflowFile(t, env.workflowSlug, "task_01.md", daemonTaskBody("pending", "Unchanged task"))
	initAndCommitTaskMultiWorkspace(t, env.workspaceRoot)

	parent, err := env.manager.StartTaskRunMultiple(
		context.Background(),
		env.workspaceRoot,
		apicore.TaskRunMultipleRequest{
			Workspace:        env.workspaceRoot,
			WorkflowSlug:     env.workflowSlug,
			SelectedTasks:    []string{"task_01"},
			Mode:             "parallel",
			PresentationMode: defaultPresentationMode,
			RuntimeOverrides: rawJSON(t, `{"run_id":"task-multi-unchanged"}`),
		},
	)
	if err != nil {
		t.Fatalf("StartTaskRunMultiple(parallel unchanged) error = %v", err)
	}
	waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool {
		return row.Status == runStatusCompleted
	})
	snapshot, err := env.manager.RunMultipleSnapshot(context.Background(), parent.RunID)
	if err != nil {
		t.Fatalf("RunMultipleSnapshot() error = %v", err)
	}
	wantItems := []apicore.TaskRunMultipleItem{
		{
			Slug:          "task_01",
			SelectedTask:  "task_01",
			Status:        taskMultiItemStatusCompleted,
			DisplayStatus: taskMultiItemStatusUnchanged,
			RunID:         "child-daemon-workflow",
		},
	}
	assertTaskMultiItems(t, snapshot.Items, wantItems)
}

func TestRunManagerTaskRunMultipleStopsOnFirstChildFailure(t *testing.T) {
	t.Parallel()

	t.Run("Should stop on first child failure", func(t *testing.T) {
		var betaStarted bool
		var mu sync.Mutex
		env := newRunManagerTestEnv(t, runManagerTestDeps{
			buildRunID: taskMultiRunIDBuilder("task-multi-failure"),
			prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
				return &model.SolvePreparation{}, nil
			},
			execute: func(_ context.Context, _ *model.SolvePreparation, cfg *model.RuntimeConfig) error {
				if cfg.Name == "beta" {
					mu.Lock()
					betaStarted = true
					mu.Unlock()
					return nil
				}
				return errors.New("alpha failed")
			},
		})
		writeTaskMultiWorkflow(t, env, "alpha", "pending")
		writeTaskMultiWorkflow(t, env, "beta", "pending")

		parent := startTaskMultiRun(t, env, "task-multi-failure", []string{"alpha", "beta"})
		parentRow := waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool {
			return row.Status == runStatusFailed
		})
		if !strings.Contains(parentRow.ErrorText, "alpha failed") {
			t.Fatalf("parent error = %q, want child failure text", parentRow.ErrorText)
		}
		requireTaskMultiChildRow(t, env, "child-alpha", parent.RunID, runStatusFailed)
		if _, err := env.globalDB.GetRun(context.Background(), "child-beta"); !errors.Is(err, globaldb.ErrRunNotFound) {
			t.Fatalf("GetRun(child-beta) error = %v, want ErrRunNotFound", err)
		}
		mu.Lock()
		started := betaStarted
		mu.Unlock()
		if started {
			t.Fatal("beta child started after first child failed")
		}

		snapshot, err := env.manager.RunMultipleSnapshot(context.Background(), parent.RunID)
		if err != nil {
			t.Fatalf("RunMultipleSnapshot() error = %v", err)
		}
		wantItems := []apicore.TaskRunMultipleItem{
			{Slug: "alpha", Status: taskMultiItemStatusFailed, RunID: "child-alpha", ErrorText: "alpha failed"},
			{Slug: "beta", Status: taskMultiItemStatusCanceled, ErrorText: "alpha failed"},
		}
		assertTaskMultiItems(t, snapshot.Items, wantItems)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleItemCanceled)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleQueueCanceled)
	})
}

func TestRunManagerTaskRunMultipleStartChildFailureCancelsQueuedItems(t *testing.T) {
	t.Parallel()

	t.Run("Should mark failed child and cancel queued items when child start fails", func(t *testing.T) {
		t.Parallel()

		parentRunID := "task-multi-start-child-failure"
		env := newRunManagerTestEnv(t, runManagerTestDeps{
			buildRunID: func(cfg *model.RuntimeConfig) (string, error) {
				if cfg != nil && strings.TrimSpace(cfg.ParentRunID) == parentRunID {
					return "", errors.New("child id allocation failed")
				}
				return taskMultiRunIDBuilder(parentRunID)(cfg)
			},
			prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
				return &model.SolvePreparation{}, nil
			},
		})
		writeTaskMultiWorkflow(t, env, "alpha", "pending")
		writeTaskMultiWorkflow(t, env, "beta", "pending")

		parent := startTaskMultiRun(t, env, parentRunID, []string{"alpha", "beta"})
		parentRow := waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool {
			return row.Status == runStatusFailed
		})
		if !strings.Contains(parentRow.ErrorText, "child id allocation failed") {
			t.Fatalf("parent error = %q, want child start failure", parentRow.ErrorText)
		}
		if _, err := env.globalDB.GetRun(
			context.Background(),
			"child-alpha",
		); !errors.Is(
			err,
			globaldb.ErrRunNotFound,
		) {
			t.Fatalf("GetRun(child-alpha) error = %v, want ErrRunNotFound", err)
		}
		if _, err := env.globalDB.GetRun(context.Background(), "child-beta"); !errors.Is(err, globaldb.ErrRunNotFound) {
			t.Fatalf("GetRun(child-beta) error = %v, want ErrRunNotFound", err)
		}

		snapshot, err := env.manager.RunMultipleSnapshot(context.Background(), parent.RunID)
		if err != nil {
			t.Fatalf("RunMultipleSnapshot() error = %v", err)
		}
		wantItems := []apicore.TaskRunMultipleItem{
			{Slug: "alpha", Status: taskMultiItemStatusFailed, ErrorText: "child id allocation failed"},
			{Slug: "beta", Status: taskMultiItemStatusCanceled, ErrorText: "child id allocation failed"},
		}
		assertTaskMultiItems(t, snapshot.Items, wantItems)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleChildFailed)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleItemCanceled)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleQueueCanceled)
	})
}

func TestRunManagerTaskRunMultipleCancellationCancelsActiveAndQueuedChildren(t *testing.T) {
	t.Parallel()

	t.Run("Should cancel active and queued children", func(t *testing.T) {
		started := make(chan string, 1)
		env := newRunManagerTestEnv(t, runManagerTestDeps{
			buildRunID: taskMultiRunIDBuilder("task-multi-cancel"),
			prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
				return &model.SolvePreparation{}, nil
			},
			execute: func(ctx context.Context, _ *model.SolvePreparation, cfg *model.RuntimeConfig) error {
				started <- cfg.Name + ":" + cfg.RunID
				<-ctx.Done()
				return ctx.Err()
			},
		})
		writeTaskMultiWorkflow(t, env, "alpha", "pending")
		writeTaskMultiWorkflow(t, env, "beta", "pending")

		parent := startTaskMultiRun(t, env, "task-multi-cancel", []string{"alpha", "beta"})
		waitForString(t, started, "alpha:child-alpha")
		if err := env.manager.Cancel(context.Background(), parent.RunID); err != nil {
			t.Fatalf("Cancel(parent) error = %v", err)
		}

		waitForRun(t, env.globalDB, "child-alpha", func(row globaldb.Run) bool {
			return row.Status == runStatusCancelled
		})
		parentRow := waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool {
			return row.Status == runStatusCancelled
		})
		if parentRow.EndedAt == nil {
			t.Fatal("parent EndedAt = nil, want terminal timestamp")
		}
		if _, err := env.globalDB.GetRun(context.Background(), "child-beta"); !errors.Is(err, globaldb.ErrRunNotFound) {
			t.Fatalf("GetRun(child-beta) error = %v, want ErrRunNotFound", err)
		}

		snapshot, err := env.manager.RunMultipleSnapshot(context.Background(), parent.RunID)
		if err != nil {
			t.Fatalf("RunMultipleSnapshot() error = %v", err)
		}
		wantItems := []apicore.TaskRunMultipleItem{
			{Slug: "alpha", Status: taskMultiItemStatusCanceled, RunID: "child-alpha"},
			{Slug: "beta", Status: taskMultiItemStatusCanceled},
		}
		assertTaskMultiItems(t, snapshot.Items, wantItems)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleItemCanceled)
		requireRunEvent(t, parent.RunID, eventspkg.EventKindTaskRunMultipleQueueCanceled)
	})
}

func TestRunManagerTaskRunMultiplePreflightRejectsInvalidInputBeforeParentRun(t *testing.T) {
	t.Parallel()

	t.Run("Should reject unsupported mode", func(t *testing.T) {
		t.Parallel()

		env := newRunManagerTestEnv(t, runManagerTestDeps{buildRunID: taskMultiRunIDBuilder("task-multi-invalid-mode")})
		writeTaskMultiWorkflow(t, env, "alpha", "pending")

		_, err := env.manager.StartTaskRunMultiple(
			context.Background(),
			env.workspaceRoot,
			apicore.TaskRunMultipleRequest{
				Workspace:        env.workspaceRoot,
				Slugs:            []string{"alpha"},
				Mode:             "unsupported",
				PresentationMode: defaultPresentationMode,
				RuntimeOverrides: rawJSON(t, `{"run_id":"task-multi-invalid-mode"}`),
			},
		)
		assertProblemStatus(t, err, http.StatusUnprocessableEntity)
		if _, err := env.globalDB.GetRun(
			context.Background(),
			"task-multi-invalid-mode",
		); !errors.Is(
			err,
			globaldb.ErrRunNotFound,
		) {
			t.Fatalf("GetRun(task-multi-invalid-mode) error = %v, want ErrRunNotFound", err)
		}
	})

	t.Run("Should accept parallel mode when git worktree prerequisites pass", func(t *testing.T) {
		t.Parallel()
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git binary not available")
		}

		env := newRunManagerTestEnv(
			t,
			runManagerTestDeps{buildRunID: taskMultiRunIDBuilder("task-multi-parallel-mode")},
		)
		writeTaskMultiWorkflow(t, env, "alpha", "pending")
		runGitOutput(t, env.workspaceRoot, "init", "--initial-branch=main")
		runGitOutput(t, env.workspaceRoot, "config", "user.email", "task-multi@example.com")
		runGitOutput(t, env.workspaceRoot, "config", "user.name", "Task Multi Test")
		runGitOutput(t, env.workspaceRoot, "config", "commit.gpgsign", "false")
		runGitOutput(t, env.workspaceRoot, "add", ".")
		runGitOutput(t, env.workspaceRoot, "commit", "-m", "initial workflow")

		_, err := env.manager.StartTaskRunMultiple(
			context.Background(),
			env.workspaceRoot,
			apicore.TaskRunMultipleRequest{
				Workspace:        env.workspaceRoot,
				Slugs:            []string{"alpha"},
				Mode:             "parallel",
				PresentationMode: defaultPresentationMode,
				RuntimeOverrides: rawJSON(t, `{"run_id":"task-multi-parallel-mode"}`),
			},
		)
		if err != nil {
			t.Fatalf("StartTaskRunMultiple(parallel) error = %v", err)
		}
	})

	t.Run("Should reject duplicate slugs", func(t *testing.T) {
		t.Parallel()

		env := newRunManagerTestEnv(t, runManagerTestDeps{buildRunID: taskMultiRunIDBuilder("task-multi-duplicate")})
		writeTaskMultiWorkflow(t, env, "alpha", "pending")

		_, err := env.manager.StartTaskRunMultiple(
			context.Background(),
			env.workspaceRoot,
			apicore.TaskRunMultipleRequest{
				Workspace:        env.workspaceRoot,
				Slugs:            []string{"alpha", "alpha"},
				PresentationMode: defaultPresentationMode,
				RuntimeOverrides: rawJSON(t, `{"run_id":"task-multi-duplicate"}`),
			},
		)
		assertProblemStatus(t, err, http.StatusUnprocessableEntity)
		if _, err := env.globalDB.GetRun(
			context.Background(),
			"task-multi-duplicate",
		); !errors.Is(
			err,
			globaldb.ErrRunNotFound,
		) {
			t.Fatalf("GetRun(task-multi-duplicate) error = %v, want ErrRunNotFound", err)
		}
	})

	t.Run("Should reject completed workflow before creating parent", func(t *testing.T) {
		t.Parallel()

		env := newRunManagerTestEnv(t, runManagerTestDeps{buildRunID: taskMultiRunIDBuilder("task-multi-completed")})
		writeTaskMultiWorkflow(t, env, "alpha", "pending")
		writeTaskMultiWorkflow(t, env, "beta", "completed")

		_, err := env.manager.StartTaskRunMultiple(
			context.Background(),
			env.workspaceRoot,
			apicore.TaskRunMultipleRequest{
				Workspace:        env.workspaceRoot,
				Slugs:            []string{"alpha", "beta"},
				PresentationMode: defaultPresentationMode,
				RuntimeOverrides: rawJSON(t, `{"run_id":"task-multi-completed"}`),
			},
		)
		assertProblemStatus(t, err, http.StatusConflict)
		if _, err := env.globalDB.GetRun(
			context.Background(),
			"task-multi-completed",
		); !errors.Is(
			err,
			globaldb.ErrRunNotFound,
		) {
			t.Fatalf("GetRun(task-multi-completed) error = %v, want ErrRunNotFound", err)
		}
	})
}

func TestResolveTaskMultiMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "Should default empty mode to sequential", want: "sequential"},
		{name: "Should preserve enqueued mode", raw: " enqueued ", want: "enqueued"},
		{name: "Should preserve parallel mode", raw: " parallel ", want: "parallel"},
		{name: "Should reject unsupported mode", raw: "unsupported", wantErr: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolveTaskMultiMode(tc.raw)
			if tc.wantErr {
				assertProblemStatus(t, err, http.StatusUnprocessableEntity)
				return
			}
			if err != nil {
				t.Fatalf("resolveTaskMultiMode(%q) error = %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("resolveTaskMultiMode(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestRunManagerTaskRunMultipleIncludeCompletedStartsCompletedWorkflow(t *testing.T) {
	t.Parallel()

	t.Run("Should start completed workflow when include_completed is true", func(t *testing.T) {
		started := make(chan string, 2)
		seenIncludeCompleted := make(chan bool, 2)
		env := newRunManagerTestEnv(t, runManagerTestDeps{
			buildRunID: taskMultiRunIDBuilder("task-multi-include-completed"),
			prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
				return &model.SolvePreparation{}, nil
			},
			execute: func(_ context.Context, _ *model.SolvePreparation, cfg *model.RuntimeConfig) error {
				started <- cfg.Name + ":" + cfg.RunID
				seenIncludeCompleted <- cfg.IncludeCompleted
				return nil
			},
		})
		writeTaskMultiWorkflow(t, env, "alpha", "pending")
		writeTaskMultiWorkflow(t, env, "beta", "completed")

		parent, err := env.manager.StartTaskRunMultiple(
			context.Background(),
			env.workspaceRoot,
			apicore.TaskRunMultipleRequest{
				Workspace:        env.workspaceRoot,
				Slugs:            []string{"alpha", "beta"},
				PresentationMode: defaultPresentationMode,
				RuntimeOverrides: rawJSON(t, `{"run_id":"task-multi-include-completed","include_completed":true}`),
			},
		)
		if err != nil {
			t.Fatalf("StartTaskRunMultiple(include completed) error = %v", err)
		}

		waitForString(t, started, "alpha:child-alpha")
		waitForString(t, started, "beta:child-beta")
		for range 2 {
			if !waitForBool(t, seenIncludeCompleted) {
				t.Fatal("child run saw IncludeCompleted=false, want true")
			}
		}
		waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool {
			return row.Status == runStatusCompleted
		})
		snapshot, err := env.manager.RunMultipleSnapshot(context.Background(), parent.RunID)
		if err != nil {
			t.Fatalf("RunMultipleSnapshot() error = %v", err)
		}
		wantItems := []apicore.TaskRunMultipleItem{
			{Slug: "alpha", Status: taskMultiItemStatusCompleted, RunID: "child-alpha"},
			{Slug: "beta", Status: taskMultiItemStatusCompleted, RunID: "child-beta"},
		}
		assertTaskMultiItems(t, snapshot.Items, wantItems)
	})
}

func TestTaskMultiSnapshotBuilderPreservesPayloadIndexOrder(t *testing.T) {
	t.Parallel()

	builder := newTaskMultiSnapshotBuilder()
	eventsToApply := []eventspkg.Event{
		mustTaskMultiEvent(t, eventspkg.EventKindTaskRunMultipleChildCompleted, kinds.TaskRunMultiplePayload{
			Slug:       "task_02",
			Index:      1,
			Total:      3,
			Status:     taskMultiItemStatusCompleted,
			ChildRunID: "child-task_02",
		}),
		mustTaskMultiEvent(t, eventspkg.EventKindTaskRunMultipleChildStarted, kinds.TaskRunMultiplePayload{
			Slug:       "task_01",
			Index:      0,
			Total:      3,
			Status:     taskMultiItemStatusRunning,
			ChildRunID: "child-task_01",
		}),
		mustTaskMultiEvent(t, eventspkg.EventKindTaskRunMultipleItemQueued, kinds.TaskRunMultiplePayload{
			Slug:   "task_03",
			Index:  2,
			Total:  3,
			Status: taskMultiItemStatusQueued,
		}),
	}
	for _, event := range eventsToApply {
		if err := builder.applyEvent(event); err != nil {
			t.Fatalf("applyEvent(%s) error = %v", event.Kind, err)
		}
	}

	items := builder.snapshotItems()
	wantSlugs := []string{"task_01", "task_02", "task_03"}
	gotSlugs := make([]string, 0, len(items))
	for _, item := range items {
		gotSlugs = append(gotSlugs, item.Slug)
	}
	if !slices.Equal(gotSlugs, wantSlugs) {
		t.Fatalf("snapshot slugs = %#v, want selected-task order %#v", gotSlugs, wantSlugs)
	}
	if items[1].RunID != "child-task_02" || items[1].Status != taskMultiItemStatusCompleted {
		t.Fatalf("task_02 item = %#v, want completed child at index 1", items[1])
	}
}

func mustTaskMultiEvent(t *testing.T, kind eventspkg.EventKind, payload kinds.TaskRunMultiplePayload) eventspkg.Event {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal task multi payload: %v", err)
	}
	return eventspkg.Event{RunID: "parent-run", Kind: kind, Payload: encoded}
}

func TestRunManagerTaskRunMultipleChildPollReturnsRunLookupErrors(t *testing.T) {
	t.Parallel()

	t.Run("Should return child run lookup errors from poll", func(t *testing.T) {
		env := newRunManagerTestEnv(t, runManagerTestDeps{})
		if err := env.globalDB.Close(); err != nil {
			t.Fatalf("Close global DB: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		_, err := env.manager.waitForTaskMultiChild(ctx, "child-alpha")
		if err == nil {
			t.Fatal("waitForTaskMultiChild() error = nil, want run lookup error")
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Fatalf("waitForTaskMultiChild() error = %v, want lookup error before context cancellation", err)
		}
		if !strings.Contains(err.Error(), "load child run child-alpha") {
			t.Fatalf("waitForTaskMultiChild() error = %v, want child lookup context", err)
		}
	})
}

func TestRunManagerTaskRunMultiplePollLookupErrorCancelsActiveChild(t *testing.T) {
	t.Parallel()

	t.Run("Should cancel active child after poll lookup error", func(t *testing.T) {
		childStarted := make(chan struct{})
		childCanceled := make(chan error, 1)
		var startedOnce sync.Once
		env := newRunManagerTestEnv(t, runManagerTestDeps{
			buildRunID: taskMultiRunIDBuilder("task-multi-poll-cancel"),
			prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
				return &model.SolvePreparation{}, nil
			},
			execute: func(ctx context.Context, _ *model.SolvePreparation, cfg *model.RuntimeConfig) error {
				if cfg.Name != "alpha" {
					return nil
				}
				startedOnce.Do(func() { close(childStarted) })
				<-ctx.Done()
				childCanceled <- ctx.Err()
				return ctx.Err()
			},
		})
		writeTaskMultiWorkflow(t, env, "alpha", "pending")
		parent := startTaskMultiRun(t, env, "task-multi-poll-cancel", []string{"alpha"})
		waitForClosed(t, childStarted, "alpha child start")
		parentActive := requireActiveRun(t, env.manager, parent.RunID)
		childActive := requireActiveRun(t, env.manager, "child-alpha")
		defer parentActive.cancel()
		defer childActive.cancel()

		if err := env.globalDB.Close(); err != nil {
			t.Fatalf("Close global DB: %v", err)
		}

		select {
		case err := <-childCanceled:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("child context error = %v, want context.Canceled", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("child was not canceled after parent %s hit child lookup error", parent.RunID)
		}
		waitForClosed(t, childActive.done, "alpha child cleanup")
		waitForClosed(t, parentActive.done, "parent multi-run cleanup")
	})
}

func TestRunManagerTaskRunMultipleSnapshotRejectsNonParentRun(t *testing.T) {
	t.Parallel()

	t.Run("Should reject snapshot for non-parent run", func(t *testing.T) {
		env := newRunManagerTestEnv(t, runManagerTestDeps{})

		run := env.startTaskRun(t, "task-run-not-multi", nil)
		waitForRun(t, env.globalDB, run.RunID, func(row globaldb.Run) bool {
			return isTerminalRunStatus(row.Status)
		})

		_, err := env.manager.RunMultipleSnapshot(context.Background(), run.RunID)
		assertProblemStatus(t, err, http.StatusUnprocessableEntity)
	})
}

func TestRunManagerTaskRunMultipleParentRuntimeStartFailureDoesNotStartChildren(t *testing.T) {
	t.Parallel()

	t.Run("Should not start children when parent runtime start fails", func(t *testing.T) {
		var childStarted atomic.Bool
		runtimeManager := &stubRuntimeManager{startErr: errors.New("runtime failed to start")}
		env := newRunManagerTestEnv(t, runManagerTestDeps{
			buildRunID:   taskMultiRunIDBuilder("task-multi-parent-start-failure"),
			openRunScope: newTestOpenRunScope(runtimeManager),
			prepare: func(context.Context, *model.RuntimeConfig, model.RunScope) (*model.SolvePreparation, error) {
				return &model.SolvePreparation{}, nil
			},
			execute: func(context.Context, *model.SolvePreparation, *model.RuntimeConfig) error {
				childStarted.Store(true)
				return nil
			},
		})
		writeTaskMultiWorkflow(t, env, "alpha", "pending")

		parent := startTaskMultiRun(t, env, "task-multi-parent-start-failure", []string{"alpha"})
		row := waitForRun(t, env.globalDB, parent.RunID, func(row globaldb.Run) bool {
			return row.Status == runStatusFailed
		})
		if !strings.Contains(row.ErrorText, "runtime failed to start") {
			t.Fatalf("parent error = %q, want runtime failure", row.ErrorText)
		}
		if childStarted.Load() {
			t.Fatal("child started after parent runtime start failure")
		}
		if _, err := env.globalDB.GetRun(
			context.Background(),
			"child-alpha",
		); !errors.Is(
			err,
			globaldb.ErrRunNotFound,
		) {
			t.Fatalf("GetRun(child-alpha) error = %v, want ErrRunNotFound", err)
		}
	})
}

func writeTaskMultiWorkflow(t *testing.T, env *runManagerTestEnv, slug string, status string) {
	t.Helper()
	env.writeWorkflowFile(t, slug, "task_01.md", daemonTaskBody(status, "Task "+slug))
}

func startTaskMultiRun(t *testing.T, env *runManagerTestEnv, runID string, slugs []string) apicore.Run {
	t.Helper()
	run, err := env.manager.StartTaskRunMultiple(
		context.Background(),
		env.workspaceRoot,
		apicore.TaskRunMultipleRequest{
			Workspace:        env.workspaceRoot,
			Slugs:            slugs,
			Mode:             "enqueued",
			PresentationMode: defaultPresentationMode,
			RuntimeOverrides: rawJSON(t, fmt.Sprintf(`{"run_id":%q}`, runID)),
		},
	)
	if err != nil {
		t.Fatalf("StartTaskRunMultiple(%v) error = %v", slugs, err)
	}
	return run
}

func taskMultiRunIDBuilder(parentRunID string) func(*model.RuntimeConfig) (string, error) {
	return func(cfg *model.RuntimeConfig) (string, error) {
		if cfg == nil {
			return "", errors.New("runtime config is required")
		}
		if runID := strings.TrimSpace(cfg.RunID); runID != "" {
			return runID, nil
		}
		if strings.TrimSpace(cfg.ParentRunID) == parentRunID {
			return "child-" + strings.TrimSpace(cfg.Name), nil
		}
		return "generated-" + strings.TrimSpace(cfg.Name), nil
	}
}

func selectedTaskRunIDBuilder(parentRunID string) func(*model.RuntimeConfig) (string, error) {
	return func(cfg *model.RuntimeConfig) (string, error) {
		if cfg == nil {
			return "", errors.New("runtime config is required")
		}
		if runID := strings.TrimSpace(cfg.RunID); runID != "" {
			return runID, nil
		}
		if strings.TrimSpace(cfg.ParentRunID) == parentRunID && len(cfg.SelectedTasks) == 1 {
			return "child-" + strings.TrimSpace(cfg.SelectedTasks[0]), nil
		}
		return "generated-" + strings.TrimSpace(cfg.Name), nil
	}
}

func initAndCommitTaskMultiWorkspace(t *testing.T, workspaceRoot string) {
	t.Helper()
	runGitOutput(t, workspaceRoot, "init", "--initial-branch=main")
	runGitOutput(t, workspaceRoot, "config", "user.email", "task-multi@example.com")
	runGitOutput(t, workspaceRoot, "config", "user.name", "Task Multi Test")
	runGitOutput(t, workspaceRoot, "config", "commit.gpgsign", "false")
	runGitOutput(t, workspaceRoot, "add", ".")
	runGitOutput(t, workspaceRoot, "commit", "-m", "initial workflow")
}

func readFilesByPath(t *testing.T, paths ...string) map[string]string {
	t.Helper()
	contents := make(map[string]string, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		contents[path] = string(content)
	}
	return contents
}

func assertFilesEqual(t *testing.T, want map[string]string, paths ...string) {
	t.Helper()
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s after run: %v", path, err)
		}
		if string(content) != want[path] {
			t.Fatalf(
				"%s changed during parallel child execution\nwant:\n%s\ngot:\n%s",
				path,
				want[path],
				string(content),
			)
		}
	}
}

func readJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(content, target); err != nil {
		t.Fatalf("decode %s: %v\n%s", path, err, string(content))
	}
}

func assertNoTaskMultiStart(t *testing.T, ch <-chan string, label string) {
	t.Helper()
	select {
	case got := <-ch:
		t.Fatalf("unexpected child start while checking %s: %s", label, got)
	case <-time.After(100 * time.Millisecond):
	}
}

func requireTaskMultiChildRow(
	t *testing.T,
	env *runManagerTestEnv,
	runID string,
	parentRunID string,
	status string,
) globaldb.Run {
	t.Helper()
	row := waitForRun(t, env.globalDB, runID, func(row globaldb.Run) bool {
		return row.Status == status
	})
	if row.ParentRunID != parentRunID {
		t.Fatalf("%s ParentRunID = %q, want %q", runID, row.ParentRunID, parentRunID)
	}
	return row
}

func hasRunMode(runs []apicore.Run, mode string) bool {
	return slices.ContainsFunc(runs, func(run apicore.Run) bool {
		return run.Mode == mode
	})
}

func assertTaskMultiItems(t *testing.T, got []apicore.TaskRunMultipleItem, want []apicore.TaskRunMultipleItem) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("items = %#v, want %#v", got, want)
	}
	for idx := range want {
		if got[idx].Slug != want[idx].Slug ||
			got[idx].Status != want[idx].Status ||
			got[idx].RunID != want[idx].RunID ||
			!strings.Contains(got[idx].ErrorText, want[idx].ErrorText) {
			t.Fatalf("item[%d] = %#v, want %#v", idx, got[idx], want[idx])
		}
		if want[idx].SelectedTask != "" && got[idx].SelectedTask != want[idx].SelectedTask {
			t.Fatalf("item[%d].SelectedTask = %q, want %q", idx, got[idx].SelectedTask, want[idx].SelectedTask)
		}
		if want[idx].DisplayStatus != "" && got[idx].DisplayStatus != want[idx].DisplayStatus {
			t.Fatalf("item[%d].DisplayStatus = %q, want %q", idx, got[idx].DisplayStatus, want[idx].DisplayStatus)
		}
	}
}
