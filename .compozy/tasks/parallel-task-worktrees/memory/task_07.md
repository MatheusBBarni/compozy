# Task Memory: task_07.md

Keep only task-local execution context here. Do not duplicate facts that are obvious from the repository, task file, PRD documents, or git history.

## Objective Snapshot

- Align parent/child run observation surfaces for parallel task worktrees: ordered snapshots, dashboard/query visibility, CLI/TUI presentation, and integration/E2E coverage for sequential and parallel `--multiple` flows.
- Loaded PRD, TechSpec, ADRs, repository guidance, shared memory, and task memory before code edits.

## Important Decisions

- Treat task_04/task_06 tracking status as stale until proven otherwise by repository implementation; actual code already contains parent/child orchestration, display statuses, source-mutation deferral, and handoff artifact references.
- PRD and TechSpec are already approved; this task should go straight to observation and coverage work without reopening brainstorming.

## Learnings

- Targeted baseline before edits passed: `env -u GOROOT go test ./internal/daemon ./internal/cli ./internal/core/run/ui`.
- CLI stream rendering previously preferred child lifecycle `status` over parent-facing `display_status`; unchanged child outcomes need `display_status` to be rendered when present.
- TUI child bootstrap/terminal child-run events can arrive after parent task-multi item events; terminal parent-facing display statuses such as `unchanged` must not be overwritten by raw child run lifecycle completion.
- Snapshot reconstruction should use task-multi payload indexes when present so out-of-order child terminal events still reconstruct selected-task order.
- Oracle review identified two blocking coverage gaps: the parallel isolation test used `--dry-run` and did not prove real child worktree execution, and `multiple = "sequential"` lacked streamed operator-visible coverage.
- Parallel CLI coverage now runs real in-process child execution that writes inside child worktrees, asserts each retained worktree contains the child output file, asserts source files stay unchanged, and checks source workspace `git status --porcelain` remains clean after execution.
- Sequential config coverage now exercises `tasks run <workflow> --multiple task_02,task_01 --stream`, verifies operator-visible stream output includes sequential selection lines for `task_02` then `task_01`, and captures runtime config to prove `MultipleMode=sequential` and selected task order are preserved.
- Targeted post-fix verification passed: `gofmt -w internal/cli/root_command_execution_test.go && env -u GOROOT go test ./internal/daemon ./internal/cli ./internal/core/run/ui`.
- After follow-up oracle review, added `writeSequentialSelectedTaskRun`/`handleStartedSequentialSelectedTaskRun` in `internal/cli/daemon_commands.go` so streamed workflow-scoped sequential `--multiple` visibly prints selected task order before watching run events. Targeted verification passed again: `gofmt -w internal/cli/daemon_commands.go internal/cli/root_command_execution_test.go && env -u GOROOT go test ./internal/daemon ./internal/cli ./internal/core/run/ui`.
- Full final verification passed: `env -u GOROOT make verify`. Initial plain `make verify` failed at Go fmt because the shell had stale `GOROOT=/Users/matheusbbarni/.local/go`; rerunning with `GOROOT` unset used Go 1.26.3 and completed all checks.

## Files / Surfaces

- Initial target surfaces: `internal/daemon/task_multi.go`, `internal/daemon/query_service.go`, `internal/daemon/run_snapshot.go`, `internal/cli/run_observe.go`, `internal/core/run/ui/multi_remote.go`, `internal/daemon/task_multi_test.go`, `internal/core/run/ui/multi_remote_test.go`, `internal/cli/root_command_execution_test.go`, `web/src/routes/-runs.integration.test.tsx`, `web/e2e/daemon-ui.smoke.spec.ts`.
- Current code/test edits: `internal/daemon/task_multi.go`, `internal/daemon/task_multi_test.go`, `internal/daemon/query_helpers_test.go`, `internal/cli/run_observe.go`, `internal/cli/run_observe_test.go`, `internal/cli/root_command_execution_test.go`, `internal/core/run/ui/multi_remote.go`, `internal/core/run/ui/multi_remote_test.go`.

## Ready for Next Run

- Stay within the listed observation/test surfaces first and avoid long read-only exploration or brainstorming unless a concrete blocker appears.

## Errors / Corrections

- Initial sequential stream test incorrectly expected task-multi queue output and a prompt artifact; actual `multiple = "sequential"` workflow mode is a single streamed task run with selected tasks in runtime config. Corrected the test to assert the real operator-visible stream contract and config propagation instead of a nonexistent parent queue artifact.
- Follow-up oracle review correctly noted generic start/completion output was not positive selected-task stream coverage; fixed by adding explicit sequential selection stream lines and assertions for caller order.

## Ready for Next Run
