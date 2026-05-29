# Task Memory: task_06.md

Keep only task-local execution context here. Do not duplicate facts that are obvious from the repository, task file, PRD documents, or git history.

## Objective Snapshot

- Implement Task 06: parent parallel task runs must persist `parallel-handoff.md`, `parallel-summary.json`, and `parallel-worktrees.json` under the parent run artifact directory and expose a concise final handoff summary without mutating workflow task files.

## Important Decisions

- Scope is run artifacts only; ADR-005 rejects writing handoff output into `.compozy/tasks/<slug>`.
- PRD and TechSpec are already approved; do not reopen brainstorming or create new design docs for this task.

## Learnings

- Existing worktree metadata already includes `ChildRunID` but is only in memory unless this task writes a parent manifest.
- Final operator text can flow through the existing terminal run summary plumbing via `SummaryMessage`; avoid bloating task event payloads.
- Mixed-outcome parallel parents can fail after handoff artifacts are written, so failed run payloads also need a trimmed `summary_message` for the operator-facing handoff pointer and prompt.

## Files / Surfaces

- Expected implementation surfaces: `internal/core/model/artifacts.go`, `internal/daemon/task_multi.go`, `internal/daemon/task_multi_worktree.go`, `internal/daemon/task_multi_test.go`, and CLI observe rendering tests if needed.
- Touched implementation/test surfaces: `pkg/compozy/events/kinds/run.go`, `internal/daemon/run_manager.go`, `internal/daemon/task_multi.go`, `internal/daemon/task_multi_test.go`, `internal/cli/run_observe.go`, `internal/cli/run_observe_test.go`, plus task tracking files.

## Ready for Next Run

- Implement directly in the listed artifact/summary surfaces and avoid exploratory subagents unless a concrete codebase blocker appears.

## Errors / Corrections

- Oracle review found that successful parent runs exposed the handoff summary, but mixed failed parent runs discarded it; fixed by carrying `summary_message` on failed run terminal payloads and rendering it in CLI observe output.

## Ready for Next Run
