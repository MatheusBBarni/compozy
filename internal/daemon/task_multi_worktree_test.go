package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveTaskMultiWorktreeMetadataIsDeterministic(t *testing.T) {
	sourceRoot := t.TempDir()
	repoRoot := sourceRoot
	baseDir := filepath.Join(t.TempDir(), "cache", "task-worktrees")

	first, err := deriveTaskMultiWorktreeMetadata(
		baseDir,
		repoRoot,
		sourceRoot,
		" parent/run 01 ",
		" Workflow Slug ",
		" task_03.md ",
	)
	if err != nil {
		t.Fatalf("deriveTaskMultiWorktreeMetadata() error = %v", err)
	}
	second, err := deriveTaskMultiWorktreeMetadata(
		baseDir,
		repoRoot,
		sourceRoot,
		" parent/run 01 ",
		" Workflow Slug ",
		" task_03.md ",
	)
	if err != nil {
		t.Fatalf("second deriveTaskMultiWorktreeMetadata() error = %v", err)
	}
	if first != second {
		t.Fatalf("metadata not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if first.ParentRunID != "parent/run 01" ||
		first.WorkflowSlug != "Workflow Slug" ||
		first.TaskName != "task_03.md" ||
		first.SourceRepositoryRoot != repoRoot {
		t.Fatalf("metadata linkage = %#v", first)
	}
	if first.BranchName != "compozy/parallel/parent-run-01/Workflow-Slug/task_03.md" {
		t.Fatalf("BranchName = %q", first.BranchName)
	}
	if !strings.HasPrefix(first.WorktreePath, baseDir) {
		t.Fatalf("WorktreePath = %q, want under %q", first.WorktreePath, baseDir)
	}
}

func TestEnsurePathOutsideRootRejectsSourceDescendants(t *testing.T) {
	sourceRoot := t.TempDir()

	cases := []struct {
		name string
		path string
	}{
		{name: "source root", path: sourceRoot},
		{name: "source child", path: filepath.Join(sourceRoot, "child")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := ensurePathOutsideRoot(tc.path, sourceRoot); err == nil {
				t.Fatalf("ensurePathOutsideRoot(%q, %q) error = nil, want rejection", tc.path, sourceRoot)
			}
		})
	}

	outside := filepath.Join(t.TempDir(), "worktree")
	if err := ensurePathOutsideRoot(outside, sourceRoot); err != nil {
		t.Fatalf("ensurePathOutsideRoot(outside) error = %v", err)
	}
}

func TestTaskMultiWorktreeProvisionerRejectsInvalidRequestsBeforeGit(t *testing.T) {
	runner := func(context.Context, string, ...string) (string, error) {
		return "", errors.New("git should not run for invalid request")
	}
	cases := []struct {
		name string
		req  taskMultiWorktreeRequest
	}{
		{
			name: "missing parent",
			req:  taskMultiWorktreeRequest{WorkflowSlug: "workflow", SelectedTasks: []string{"task_03"}},
		},
		{
			name: "missing workflow",
			req:  taskMultiWorktreeRequest{ParentRunID: "parent", SelectedTasks: []string{"task_03"}},
		},
		{name: "missing tasks", req: taskMultiWorktreeRequest{ParentRunID: "parent", WorkflowSlug: "workflow"}},
		{
			name: "duplicate tasks",
			req: taskMultiWorktreeRequest{
				ParentRunID:   "parent",
				WorkflowSlug:  "workflow",
				SelectedTasks: []string{"task_03", "task_03"},
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			provisioner := taskMultiWorktreeProvisioner{run: runner}
			if _, err := provisioner.Provision(context.Background(), tc.req); err == nil {
				t.Fatal("Provision(invalid) error = nil, want rejection")
			}
		})
	}
}

func TestMapTaskMultiWorktreePathPreservesWorkflowRelativePath(t *testing.T) {
	sourceRoot := t.TempDir()
	worktreeRoot := filepath.Join(t.TempDir(), "worktree")
	sourceWorkflow := filepath.Join(sourceRoot, ".compozy", "tasks", "workflow")
	if err := os.MkdirAll(sourceWorkflow, 0o755); err != nil {
		t.Fatalf("mkdir source workflow: %v", err)
	}

	got, err := mapTaskMultiWorktreePath(sourceRoot, sourceWorkflow, worktreeRoot)
	if err != nil {
		t.Fatalf("mapTaskMultiWorktreePath() error = %v", err)
	}
	want := filepath.Join(worktreeRoot, ".compozy", "tasks", "workflow")
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(worktreeRoot)); err == nil {
		want = filepath.Join(resolved, filepath.Base(worktreeRoot), ".compozy", "tasks", "workflow")
	}
	if got != want {
		t.Fatalf("mapTaskMultiWorktreePath() = %q, want %q", got, want)
	}

	_, err = mapTaskMultiWorktreePath(sourceRoot, filepath.Join(t.TempDir(), "external"), worktreeRoot)
	if err == nil {
		t.Fatal("mapTaskMultiWorktreePath(external) error = nil, want rejection")
	}
}

func TestTaskMultiWorktreePreflightRejectsUnsafeRepositories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}

	t.Run("non git workspace", func(t *testing.T) {
		provisioner := newTaskMultiWorktreeProvisioner()
		_, _, err := provisioner.preflight(context.Background(), t.TempDir())
		if err == nil {
			t.Fatal("preflight(non-git) error = nil, want rejection")
		}
		if !strings.Contains(err.Error(), "must be a git repository") {
			t.Fatalf("preflight(non-git) error = %v", err)
		}
	})

	t.Run("blank source workspace", func(t *testing.T) {
		provisioner := newTaskMultiWorktreeProvisioner()
		_, _, err := provisioner.preflight(context.Background(), "")
		if err == nil {
			t.Fatal("preflight(blank) error = nil, want rejection")
		}
	})

	t.Run("source workspace is a file", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "workspace-file")
		if err := os.WriteFile(filePath, []byte("not a dir"), 0o600); err != nil {
			t.Fatalf("write file workspace: %v", err)
		}
		provisioner := newTaskMultiWorktreeProvisioner()
		_, _, err := provisioner.preflight(context.Background(), filePath)
		if err == nil {
			t.Fatal("preflight(file) error = nil, want rejection")
		}
	})

	t.Run("empty git repository", func(t *testing.T) {
		repo := t.TempDir()
		runGitOutput(t, repo, "init", "--initial-branch=main")
		provisioner := newTaskMultiWorktreeProvisioner()
		_, _, err := provisioner.preflight(context.Background(), repo)
		if err == nil {
			t.Fatal("preflight(empty repo) error = nil, want rejection")
		}
	})

	t.Run("dirty source workspace", func(t *testing.T) {
		repo := initTaskMultiWorktreeGitRepo(t)
		if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
			t.Fatalf("write dirty file: %v", err)
		}
		provisioner := newTaskMultiWorktreeProvisioner()
		_, _, err := provisioner.preflight(context.Background(), repo)
		if err == nil {
			t.Fatal("preflight(dirty) error = nil, want rejection")
		}
		if !strings.Contains(err.Error(), "must be clean") {
			t.Fatalf("preflight(dirty) error = %v", err)
		}
	})
}

func TestTaskMultiWorktreeEnsureWorktreeRejectsConflictingRetainedPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	repo := initTaskMultiWorktreeGitRepo(t)
	lockTaskMultiWorktreeTestHome(t, t.TempDir())
	provisioner := newTaskMultiWorktreeProvisioner()
	manifest, err := provisioner.Provision(context.Background(), taskMultiWorktreeRequest{
		ParentRunID:         "parent-conflict",
		WorkflowSlug:        "parallel-task-worktrees",
		SourceWorkspaceRoot: repo,
		SelectedTasks:       []string{"task_03"},
	})
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	metadata := manifest.Worktrees[0]
	runGitOutput(t, metadata.WorktreePath, "checkout", "-b", "conflicting-branch")
	if err := provisioner.ensureWorktree(context.Background(), repo, metadata); err == nil {
		t.Fatal("ensureWorktree(conflicting branch) error = nil, want rejection")
	}
}

func TestTaskMultiWorktreeProvisionerCreatesRetainedWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	repo := initTaskMultiWorktreeGitRepo(t)
	lockTaskMultiWorktreeTestHome(t, t.TempDir())

	beforeHead := runGitOutput(t, repo, "rev-parse", "HEAD")
	beforeStatus := runGitOutput(t, repo, "status", "--porcelain=v1", "--untracked-files=all")
	provisioner := newTaskMultiWorktreeProvisioner()
	manifest, err := provisioner.Provision(context.Background(), taskMultiWorktreeRequest{
		ParentRunID:         "parent-123",
		WorkflowSlug:        "parallel-task-worktrees",
		SourceWorkspaceRoot: repo,
		SelectedTasks:       []string{"task_03", "task_04"},
	})
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if manifest.ParentRunID != "parent-123" || manifest.WorkflowSlug != "parallel-task-worktrees" {
		t.Fatalf("manifest linkage = %#v", manifest)
	}
	if len(manifest.Worktrees) != 2 {
		t.Fatalf("worktree count = %d, want 2", len(manifest.Worktrees))
	}
	seenPaths := make(map[string]struct{}, len(manifest.Worktrees))
	for _, metadata := range manifest.Worktrees {
		if metadata.ParentRunID != manifest.ParentRunID || metadata.WorkflowSlug != manifest.WorkflowSlug {
			t.Fatalf("metadata linkage = %#v, manifest = %#v", metadata, manifest)
		}
		if err := ensurePathOutsideRoot(metadata.WorktreePath, repo); err != nil {
			t.Fatalf("worktree path should be outside source: %v", err)
		}
		if _, ok := seenPaths[metadata.WorktreePath]; ok {
			t.Fatalf("duplicate worktree path %q", metadata.WorktreePath)
		}
		seenPaths[metadata.WorktreePath] = struct{}{}
		info, err := os.Stat(metadata.WorktreePath)
		if err != nil {
			t.Fatalf("stat worktree %q: %v", metadata.WorktreePath, err)
		}
		if !info.IsDir() {
			t.Fatalf("worktree path %q is not a directory", metadata.WorktreePath)
		}
		branch := runGitOutput(t, metadata.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD")
		if branch != metadata.BranchName {
			t.Fatalf("branch for %s = %q, want %q", metadata.TaskName, branch, metadata.BranchName)
		}
	}
	if afterHead := runGitOutput(t, repo, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("source HEAD changed: before %s after %s", beforeHead, afterHead)
	}
	if afterStatus := runGitOutput(
		t,
		repo,
		"status",
		"--porcelain=v1",
		"--untracked-files=all",
	); afterStatus != beforeStatus {
		t.Fatalf("source status changed: before %q after %q", beforeStatus, afterStatus)
	}
	list := runGitOutput(t, repo, "worktree", "list", "--porcelain")
	for path := range seenPaths {
		listedPath := path
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			listedPath = resolved
		}
		if !strings.Contains(list, "worktree "+listedPath) {
			t.Fatalf("git worktree list missing %q:\n%s", path, list)
		}
	}

	retained, err := provisioner.Provision(context.Background(), taskMultiWorktreeRequest{
		ParentRunID:         "parent-123",
		WorkflowSlug:        "parallel-task-worktrees",
		SourceWorkspaceRoot: repo,
		SelectedTasks:       []string{"task_03", "task_04"},
	})
	if err != nil {
		t.Fatalf("Provision() retained error = %v", err)
	}
	if len(retained.Worktrees) != len(manifest.Worktrees) {
		t.Fatalf("retained worktree count = %d, want %d", len(retained.Worktrees), len(manifest.Worktrees))
	}
}

func TestTaskMultiWorktreeProvisionerSupportsNestedWorkspaceRoots(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	repoRoot, workspaceRoot := initNestedTaskMultiWorktreeGitRepo(t)
	lockTaskMultiWorktreeTestHome(t, t.TempDir())

	provisioner := newTaskMultiWorktreeProvisioner()
	manifest, err := provisioner.Provision(context.Background(), taskMultiWorktreeRequest{
		ParentRunID:         "parent-nested",
		WorkflowSlug:        "nested-workflow",
		SourceWorkspaceRoot: workspaceRoot,
		SelectedTasks:       []string{"task_03"},
	})
	if err != nil {
		t.Fatalf("Provision(nested workspace) error = %v", err)
	}
	if len(manifest.Worktrees) != 1 {
		t.Fatalf("worktree count = %d, want 1", len(manifest.Worktrees))
	}
	metadata := manifest.Worktrees[0]
	wantRepoRoot := repoRoot
	if resolved, err := filepath.EvalSymlinks(repoRoot); err == nil {
		wantRepoRoot = resolved
	}
	if metadata.SourceRepositoryRoot != wantRepoRoot {
		t.Fatalf("SourceRepositoryRoot = %q, want %q", metadata.SourceRepositoryRoot, repoRoot)
	}
	childWorkspaceRoot, err := mapTaskMultiWorktreePath(
		metadata.SourceRepositoryRoot,
		workspaceRoot,
		metadata.WorktreePath,
	)
	if err != nil {
		t.Fatalf("map nested workspace root: %v", err)
	}
	wantChildWorkspaceRoot := filepath.Join(metadata.WorktreePath, "workspace")
	if resolved, err := filepath.EvalSymlinks(metadata.WorktreePath); err == nil {
		wantChildWorkspaceRoot = filepath.Join(resolved, "workspace")
	}
	if childWorkspaceRoot != wantChildWorkspaceRoot {
		t.Fatalf(
			"child workspace root = %q, want nested workspace under checkout %q",
			childWorkspaceRoot,
			metadata.WorktreePath,
		)
	}
	childTaskPath := filepath.Join(childWorkspaceRoot, ".compozy", "tasks", "nested-workflow", "task_03.md")
	if _, err := os.Stat(childTaskPath); err != nil {
		t.Fatalf("stat nested child task file: %v", err)
	}
}

func initTaskMultiWorktreeGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitOutput(t, repo, "init", "--initial-branch=main")
	runGitOutput(t, repo, "config", "user.email", "task-multi-worktree@example.com")
	runGitOutput(t, repo, "config", "user.name", "Task Multi Worktree Test")
	runGitOutput(t, repo, "config", "commit.gpgsign", "false")
	workflowDir := filepath.Join(repo, ".compozy", "tasks", "parallel-task-worktrees")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	for _, taskName := range []string{"task_03", "task_04"} {
		path := filepath.Join(workflowDir, taskName+".md")
		if err := os.WriteFile(path, []byte("---\nstatus: pending\n---\n# "+taskName+"\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", taskName, err)
		}
	}
	runGitOutput(t, repo, "add", ".")
	runGitOutput(t, repo, "commit", "-m", "initial workflow")
	return repo
}

func initNestedTaskMultiWorktreeGitRepo(t *testing.T) (string, string) {
	t.Helper()
	repoRoot := t.TempDir()
	workspaceRoot := filepath.Join(repoRoot, "workspace")
	workflowDir := filepath.Join(workspaceRoot, ".compozy", "tasks", "nested-workflow")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatalf("mkdir nested workflow dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(workflowDir, "task_03.md"),
		[]byte("---\nstatus: pending\n---\n# task_03\n"),
		0o600,
	); err != nil {
		t.Fatalf("write nested task: %v", err)
	}
	runGitOutput(t, repoRoot, "init", "--initial-branch=main")
	runGitOutput(t, repoRoot, "config", "user.email", "task-multi-worktree@example.com")
	runGitOutput(t, repoRoot, "config", "user.name", "Task Multi Worktree Test")
	runGitOutput(t, repoRoot, "config", "commit.gpgsign", "false")
	runGitOutput(t, repoRoot, "add", ".")
	runGitOutput(t, repoRoot, "commit", "-m", "initial nested workflow")
	return repoRoot, workspaceRoot
}

func lockTaskMultiWorktreeTestHome(t *testing.T, homeDir string) {
	t.Helper()
	runManagerTestHomeMu.Lock()
	previousHome, hadPreviousHome := os.LookupEnv("HOME")
	if err := os.Setenv("HOME", homeDir); err != nil {
		runManagerTestHomeMu.Unlock()
		t.Fatalf("Setenv(HOME) error = %v", err)
	}
	t.Cleanup(func() {
		if hadPreviousHome {
			_ = os.Setenv("HOME", previousHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
		runManagerTestHomeMu.Unlock()
	})
}
