# Task Memory: task_03.md

Keep only task-local execution context here. Do not duplicate facts that are obvious from the repository, task file, PRD documents, or git history.

## Objective Snapshot

- Implement the daemon-local git worktree lifecycle substrate for parallel selected-task runs: preflight, deterministic branch/path metadata, retained worktree creation, and tests.

## Important Decisions

- Keep the V1 helper in `internal/daemon` rather than extracting a reusable package.
- Provision worktrees after the parent run exists so the deterministic branch/path can use the actual parent run ID, and before any child run launches.
- Use the daemon home cache as the retained worktree base so generated worktrees stay outside the source workspace root.

## Learnings

- Existing `parallel` mode is only a contract; `runTaskMultiCoordinator` still starts children serially.
- Child runtime roots must be mapped from the source repository root into the retained checkout root. Do not assume the source workspace root equals the git repository root; nested-workspace/monorepo layouts need `WorkspaceRoot` mapped separately from the checkout root.
- Workflow-scoped parallel runs need the real parent workflow slug carried on the parent prepared state; use the synthetic `multi-task` label only for legacy cross-workflow slug queues.

## Files / Surfaces

- Touched code: `internal/daemon/task_multi.go`, `internal/daemon/task_multi_worktree.go`, `internal/daemon/task_multi_worktree_test.go`, `internal/daemon/task_multi_test.go`, `internal/cli/root_command_execution_test.go`.
- Tracking/memory files updated after `make verify` passed.

## Errors / Corrections

- Initial integration mapped child roots directly under the checkout root and lost workflow slug context for workflow-scoped parent runs. Oracle review caught both; fixed by carrying `SourceRepositoryRoot` metadata, mapping nested workspace paths, and storing the real parent workflow slug.
- The environment has a stale `GOROOT=/Users/matheusbbarni/.local/go`; validation commands used `env -u GOROOT ...` so `/opt/homebrew/bin/go` could resolve its actual GOROOT.

## Ready for Next Run

- Task 04 can use `preparedTaskMulti.worktrees` and each `preparedTaskMultiItem.worktree` as the handoff point for child launch metadata.
- Final verification evidence: `env -u GOROOT make verify` passed with 3421 Go tests (3 skipped), frontend lint/typecheck/tests/build, Go lint/test/build, and Playwright e2e all green.
