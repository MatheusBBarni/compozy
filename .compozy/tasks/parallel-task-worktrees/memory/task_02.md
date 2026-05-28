# Task Memory: task_02.md

Keep only task-local execution context here. Do not duplicate facts that are obvious from the repository, task file, PRD documents, or git history.

## Objective Snapshot

- Implement task-scoped `tasks run <workflow> --multiple ...` sequential execution through the normal task-run path, preserving explicit selected-task order and validation semantics from the TechSpec.

## Important Decisions

- No conflicts found between Task 02, PRD, TechSpec, and ADRs. Sequential mode must remain the safe default; parent/child worktree orchestration remains out of scope for this task.
- Workflow-scoped `--multiple` with effective `multiple = "sequential"` now calls the normal single task-run endpoint with `selected_tasks`; cross-workflow multi-run compatibility remains on the task-multi path.
- Selected-task validation is centralized in `internal/core/tasks.SelectTaskEntries` so daemon validation, planner filtering, and selected-order job preparation use the same matching rules.

## Learnings

- Pre-change static signal: `internal/cli/daemon_commands.go` routes all `--multiple` invocations through `StartTaskRunMultiple`; workflow-scoped sequential mode does not currently reuse `StartTaskRun`.
- Pre-change command signal: targeted `go test ./internal/cli ...` could not run because `go` reports missing GOROOT at `/Users/matheusbbarni/.local/go` in the current environment.
- Verification requires unsetting the stale local `GOROOT`; `env -u GOROOT make verify` completed successfully after implementation.

## Files / Surfaces

- Expected surfaces: CLI task run command routing, daemon task-run request validation, planner selected-task filtering/order, prompt/job ordering, task-runtime ID matching tests.
- Touched surfaces: `internal/core/tasks/selection.go`, `internal/core/plan/input.go`, `internal/core/plan/prepare.go`, `internal/cli/daemon_commands.go`, `internal/daemon/run_manager.go`, and matching CLI/planner/daemon tests.

## Errors / Corrections

- First full verification failed on `gocyclo` for `runTaskWorkflowsMultiple`; extracted sequential/multiple start helpers and reran full verification cleanly.

## Ready for Next Run

- Task 02 implementation verified with `env -u GOROOT make verify`; no task-local blockers remain.
