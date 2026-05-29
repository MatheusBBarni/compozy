package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	compozyconfig "github.com/compozy/compozy/internal/config"
)

const taskMultiWorktreeDirName = "task-worktrees"

const taskMultiWorktreeTaskHashBytes = 6

// TaskMultiWorktreeMetadata captures the retained worktree lane for one selected task.
type TaskMultiWorktreeMetadata struct {
	ParentRunID          string `json:"parent_run_id"`
	WorkflowSlug         string `json:"workflow_slug"`
	TaskName             string `json:"task_name"`
	SourceRepositoryRoot string `json:"source_repository_root"`
	SourceWorkspaceRoot  string `json:"source_workspace_root"`
	WorktreePath         string `json:"worktree_path"`
	BranchName           string `json:"branch_name"`
	ChildRunID           string `json:"child_run_id,omitempty"`
}

// TaskMultiWorktreeManifest captures all retained worktree lanes for a parent run.
type TaskMultiWorktreeManifest struct {
	ParentRunID          string                      `json:"parent_run_id"`
	WorkflowSlug         string                      `json:"workflow_slug"`
	SourceRepositoryRoot string                      `json:"source_repository_root"`
	SourceWorkspaceRoot  string                      `json:"source_workspace_root"`
	Worktrees            []TaskMultiWorktreeMetadata `json:"worktrees"`
}

type taskMultiWorktreeRequest struct {
	ParentRunID         string
	WorkflowSlug        string
	SourceWorkspaceRoot string
	SelectedTasks       []string
}

type taskMultiWorktreeProvisioner struct {
	run taskMultiWorktreeGitRunner
}

type taskMultiWorktreeGitRunner func(ctx context.Context, dir string, args ...string) (string, error)

func newTaskMultiWorktreeProvisioner() taskMultiWorktreeProvisioner {
	return taskMultiWorktreeProvisioner{run: runTaskMultiWorktreeGitCommand}
}

func (p taskMultiWorktreeProvisioner) Provision(
	ctx context.Context,
	req taskMultiWorktreeRequest,
) (TaskMultiWorktreeManifest, error) {
	if p.run == nil {
		return TaskMultiWorktreeManifest{}, errors.New("task multi worktree git runner is required")
	}
	parentRunID := strings.TrimSpace(req.ParentRunID)
	if parentRunID == "" {
		return TaskMultiWorktreeManifest{}, errors.New("task multi worktree parent run id is required")
	}
	workflowSlug := strings.TrimSpace(req.WorkflowSlug)
	if workflowSlug == "" {
		return TaskMultiWorktreeManifest{}, errors.New("task multi worktree workflow slug is required")
	}
	selectedTasks, err := normalizeTaskMultiWorktreeTasks(req.SelectedTasks)
	if err != nil {
		return TaskMultiWorktreeManifest{}, err
	}
	repoRoot, sourceRoot, err := p.preflight(ctx, req.SourceWorkspaceRoot)
	if err != nil {
		return TaskMultiWorktreeManifest{}, err
	}
	baseDir, err := taskMultiWorktreeBaseDir()
	if err != nil {
		return TaskMultiWorktreeManifest{}, err
	}
	if err := ensurePathOutsideRoot(baseDir, sourceRoot); err != nil {
		return TaskMultiWorktreeManifest{}, err
	}

	manifest := TaskMultiWorktreeManifest{
		ParentRunID:          parentRunID,
		WorkflowSlug:         workflowSlug,
		SourceRepositoryRoot: repoRoot,
		SourceWorkspaceRoot:  sourceRoot,
		Worktrees:            make([]TaskMultiWorktreeMetadata, 0, len(selectedTasks)),
	}
	for _, taskName := range selectedTasks {
		metadata, err := deriveTaskMultiWorktreeMetadata(
			baseDir,
			repoRoot,
			sourceRoot,
			parentRunID,
			workflowSlug,
			taskName,
		)
		if err != nil {
			return TaskMultiWorktreeManifest{}, err
		}
		if err := p.ensureWorktree(ctx, repoRoot, metadata); err != nil {
			return TaskMultiWorktreeManifest{}, err
		}
		manifest.Worktrees = append(manifest.Worktrees, metadata)
	}
	return manifest, nil
}

func normalizeTaskMultiWorktreeTasks(selectedTasks []string) ([]string, error) {
	seen := make(map[string]struct{}, len(selectedTasks))
	normalized := make([]string, 0, len(selectedTasks))
	for _, selectedTask := range selectedTasks {
		taskName := strings.TrimSpace(selectedTask)
		if taskName == "" {
			return nil, errors.New("task multi worktree selected task is required")
		}
		if _, ok := seen[taskName]; ok {
			return nil, fmt.Errorf("task multi worktree duplicate selected task %q", taskName)
		}
		seen[taskName] = struct{}{}
		normalized = append(normalized, taskName)
	}
	if len(normalized) == 0 {
		return nil, errors.New("task multi worktree selected tasks are required")
	}
	return normalized, nil
}

func (p taskMultiWorktreeProvisioner) preflight(
	ctx context.Context,
	sourceWorkspaceRoot string,
) (string, string, error) {
	sourceRoot, err := compozyconfig.ResolvePath(sourceWorkspaceRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve source workspace root: %w", err)
	}
	if strings.TrimSpace(sourceRoot) == "" {
		return "", "", errors.New("task multi worktree source workspace root is required")
	}
	info, err := os.Stat(sourceRoot)
	if err != nil {
		return "", "", fmt.Errorf("stat source workspace root %q: %w", sourceRoot, err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("source workspace root %q is not a directory", sourceRoot)
	}
	repoRoot, err := p.run(ctx, sourceRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("source workspace must be a git repository: %w", err)
	}
	repoRoot, err = compozyconfig.ResolvePath(repoRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve git repository root: %w", err)
	}
	if strings.TrimSpace(repoRoot) == "" {
		return "", "", errors.New("git repository root is empty")
	}
	if _, err := p.run(ctx, repoRoot, "rev-parse", "--verify", "HEAD"); err != nil {
		return "", "", fmt.Errorf("source git repository must have a committed HEAD: %w", err)
	}
	status, err := p.run(ctx, sourceRoot, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "", "", fmt.Errorf("inspect source workspace status: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return "", "", errors.New("source workspace must be clean before parallel worktree provisioning")
	}
	return repoRoot, sourceRoot, nil
}

func (p taskMultiWorktreeProvisioner) ensureWorktree(
	ctx context.Context,
	repoRoot string,
	metadata TaskMultiWorktreeMetadata,
) error {
	if info, err := os.Stat(metadata.WorktreePath); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("retained worktree path %q is not a directory", metadata.WorktreePath)
		}
		branch, err := p.run(ctx, metadata.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return fmt.Errorf("inspect retained worktree %q: %w", metadata.WorktreePath, err)
		}
		if strings.TrimSpace(branch) != metadata.BranchName {
			return fmt.Errorf(
				"retained worktree %q is on branch %q, want %q",
				metadata.WorktreePath,
				strings.TrimSpace(branch),
				metadata.BranchName,
			)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat retained worktree path %q: %w", metadata.WorktreePath, err)
	}
	if err := os.MkdirAll(filepath.Dir(metadata.WorktreePath), 0o755); err != nil {
		return fmt.Errorf("create retained worktree parent: %w", err)
	}
	if _, err := p.run(
		ctx,
		repoRoot,
		"worktree",
		"add",
		"-b",
		metadata.BranchName,
		metadata.WorktreePath,
		"HEAD",
	); err != nil {
		return fmt.Errorf("create worktree for %s at %s: %w", metadata.TaskName, metadata.WorktreePath, err)
	}
	return nil
}

func taskMultiWorktreeBaseDir() (string, error) {
	homePaths, err := compozyconfig.ResolveHomePaths()
	if err != nil {
		return "", fmt.Errorf("resolve task multi worktree base: %w", err)
	}
	return filepath.Join(homePaths.CacheDir, taskMultiWorktreeDirName), nil
}

func deriveTaskMultiWorktreeMetadata(
	baseDir string,
	repoRoot string,
	sourceRoot string,
	parentRunID string,
	workflowSlug string,
	taskName string,
) (TaskMultiWorktreeMetadata, error) {
	safeParent := safeTaskMultiWorktreeSegment(parentRunID)
	safeWorkflow := safeTaskMultiWorktreeSegment(workflowSlug)
	taskSegment := taskMultiWorktreeTaskSegment(taskName)
	if safeParent == "" || safeWorkflow == "" || taskSegment == "" {
		return TaskMultiWorktreeMetadata{}, errors.New(
			"task multi worktree metadata requires parent run, workflow, and task",
		)
	}
	worktreePath, err := compozyconfig.ResolvePath(
		filepath.Join(baseDir, safeParent, safeWorkflow, taskSegment),
	)
	if err != nil {
		return TaskMultiWorktreeMetadata{}, fmt.Errorf("resolve retained worktree path: %w", err)
	}
	if err := ensurePathOutsideRoot(worktreePath, sourceRoot); err != nil {
		return TaskMultiWorktreeMetadata{}, err
	}
	return TaskMultiWorktreeMetadata{
		ParentRunID:          strings.TrimSpace(parentRunID),
		WorkflowSlug:         strings.TrimSpace(workflowSlug),
		TaskName:             strings.TrimSpace(taskName),
		SourceRepositoryRoot: strings.TrimSpace(repoRoot),
		SourceWorkspaceRoot:  strings.TrimSpace(sourceRoot),
		WorktreePath:         worktreePath,
		BranchName: fmt.Sprintf(
			"compozy/parallel/%s/%s/%s",
			safeParent,
			safeWorkflow,
			taskSegment,
		),
	}, nil
}

func taskMultiWorktreeTaskSegment(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	safe := safeTaskMultiWorktreeSegment(trimmed)
	if safe == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(trimmed))
	return fmt.Sprintf("%s-%x", safe, hash[:taskMultiWorktreeTaskHashBytes])
}

func ensurePathOutsideRoot(path string, root string) error {
	absPath, err := resolveTaskMultiComparablePath(path)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", path, err)
	}
	absRoot, err := resolveTaskMultiComparablePath(root)
	if err != nil {
		return fmt.Errorf("resolve root %q: %w", root, err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return fmt.Errorf("compare path %q with source root %q: %w", absPath, absRoot, err)
	}
	if rel == "." || rel == "" || (!strings.HasPrefix(rel, "..") && rel != "..") {
		return fmt.Errorf("worktree path %q must be outside source workspace root %q", absPath, absRoot)
	}
	return nil
}

func resolveTaskMultiComparablePath(path string) (string, error) {
	absPath, err := compozyconfig.ResolvePath(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		return resolved, nil
	}
	parent := filepath.Dir(absPath)
	if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolvedParent, filepath.Base(absPath)), nil
	}
	return absPath, nil
}

func safeTaskMultiWorktreeSegment(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(trimmed))
	lastDash := false
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.':
			builder.WriteRune(r)
			lastDash = false
		case r == '-':
			builder.WriteRune(r)
			lastDash = true
		default:
			if lastDash {
				continue
			}
			builder.WriteByte('-')
			lastDash = true
		}
	}
	safe := strings.Trim(builder.String(), "-.")
	if safe == "" {
		return ""
	}
	return safe
}

func runTaskMultiWorktreeGitCommand(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = strings.TrimSpace(dir)
	cmd.Env = append(sanitizedTaskMultiWorktreeGitEnv(), "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message != "" {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, message)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func sanitizedTaskMultiWorktreeGitEnv() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "GIT_DIR="),
			strings.HasPrefix(kv, "GIT_WORK_TREE="),
			strings.HasPrefix(kv, "GIT_COMMON_DIR="),
			strings.HasPrefix(kv, "GIT_INDEX_FILE="),
			strings.HasPrefix(kv, "GIT_NAMESPACE="):
			continue
		}
		filtered = append(filtered, kv)
	}
	return filtered
}
