# Task Memory: task_01.md

Keep only task-local execution context here. Do not duplicate facts that are obvious from the repository, task file, PRD documents, or git history.

## Objective Snapshot

- Completed the contract baseline for selected-task multi-runs: config, CLI, API/client, daemon runtime, OpenAPI, generated TS types, docs, and tests now expose selected tasks and `multiple = "sequential" | "parallel"` explicitly.

## Important Decisions

- New config key is `[tasks.run] multiple`; legacy `run_multiple_mode` is rejected at config validation.
- API keeps legacy cross-workflow `slugs` compatibility, but task-scoped `selected_tasks` requires `workflow_slug` at the boundary.
- Multi-run event/snapshot identity uses selected task IDs for workflow-scoped runs and carries `selected_task` separately from workflow slug to avoid collapsed tabs/state.
- Child PRD-task planning filters `RuntimeConfig.SelectedTasks` before batching so task-scoped children execute only the requested task file.

## Learnings

- macOS daemon e2e startup is sensitive to Unix socket path length. Use a short per-process `/tmp/cz-pw-<pid>` Playwright fixture root, not the default long `/var/folders/...` temp path.

## Files / Surfaces

- Core selected-task filtering: `internal/core/plan/input.go`, covered by `internal/core/plan/prepare_test.go`.
- Runtime/API fields: `internal/core/model/runtime_config.go`, `internal/core/model/task_runtime.go`, `sdk/extension/types.go`, `internal/api/contract/types.go`.
- Boundary and daemon flow: `internal/api/core/handlers.go`, `internal/api/client/client.go`, `internal/cli/daemon_commands.go`, `internal/daemon/run_manager.go`, `internal/daemon/task_multi.go`.
- Public schema/docs: `openapi/compozy-daemon.json`, `web/src/generated/compozy-openapi.d.ts`, `README.md`, CLI help golden.

## Errors / Corrections

- Oracle review found selected task fields were initially write-only; fixed by adding planning-time filtering and selected-task event identity.
- Initial e2e temp path moved to `os.tmpdir()` remained too long on macOS; corrected to short per-process `/tmp/cz-pw-<pid>` path.
- Legacy cross-workflow `--multiple alpha,beta` must send `slugs`, not `selected_tasks`; otherwise API correctly requires a `workflow_slug`.

## Ready for Next Run

- Verification evidence: `env -u GOROOT make verify` passed with all checks, including frontend lint/typecheck/test/build, Go fmt/lint/tests/build, and Playwright e2e (`5 passed`).
- Task 02 can build on the explicit contract. Sequential child execution still exists through the daemon parent/child queue; deeper behavioral worktree orchestration remains for later tasks.
