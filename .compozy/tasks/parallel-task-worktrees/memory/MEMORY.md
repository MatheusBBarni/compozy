# Workflow Memory

Keep only durable, cross-task context here. Do not duplicate facts that are obvious from the repository, PRD documents, or git history.

## Current State

- Task 01 is complete and verified. The baseline contract now represents selected task IDs and multi-run mode as first-class fields across config, CLI, API/client, runtime config, daemon event/snapshot payloads, OpenAPI, generated web types, and docs.
- Task 02 is implemented and verified. Workflow-scoped `--multiple` in effective sequential mode now reuses the normal task-run path with explicit selected-task validation and caller-order preservation.
- Task 03 is implemented and verified. Parallel mode now provisions retained daemon-home git worktrees before child launch and exposes per-task worktree metadata on prepared multi-run state.
- Task 04 is implemented and verified. Parallel selected-task runs now use daemon parent-child orchestration: all selected child runs launch against their assigned retained worktrees, parent events observe child terminal states as they arrive, and final snapshots/summaries remain selected-task ordered.
- Task 05 is implemented and verified. Parallel child task runs now defer workflow task-file reconciliation, skip daemon workflow sync/watch setup, and report parent-facing display outcomes separately from child run lifecycle status.
- Task 06 is implemented and verified. Parallel parent runs now write `parallel-handoff.md`, `parallel-summary.json`, and `parallel-worktrees.json` under the parent run artifacts directory and surface handoff summaries through terminal run payloads.

## Shared Decisions

- Use `[tasks.run] multiple = "sequential" | "parallel"`; `sequential` is the default for the new task-scoped contract.
- `selected_tasks` is task-scoped and requires `workflow_slug`; legacy cross-workflow queue compatibility remains via `slugs`.
- Multi-run snapshots/events should distinguish task identity with `selected_task` so multiple selected tasks in one workflow do not collapse under one workflow slug.
- Selected-task validation/order is centralized through `internal/core/tasks.SelectTaskEntries`; future planner/daemon/parallel code should reuse it rather than reimplement selector matching.
- Retained parallel worktrees live under the daemon home cache (`.compozy/cache/task-worktrees/...`) and are intentionally left for later inspection/fan-in.
- Parallel child run truth is two-phase: child lifecycle `status` reports execution completion/failure/cancelation, while parent-facing `display_status` can report `unchanged` when git snapshot evidence shows no workspace changes.
- Parent cancellation must not overwrite child truth: future parent/observer changes should preserve any child run that is already terminal instead of replacing it with a coordinator-level canceled item state.
- Failed parallel parent runs can carry a terminal `summary_message` on `RunFailedPayload` so operators still see the handoff artifact pointer when one or more children failed after artifacts were written.

## Shared Learnings

- Playwright daemon fixture roots on macOS must keep daemon socket paths short. The default e2e temp root is `/tmp/cz-pw-<pid>`.
- Parallel child workspace roots must be mapped from the source repository root into the retained checkout root; do not assume the workspace root equals the git repository root.
- Local validation may need `env -u GOROOT` because this environment can inherit a stale `GOROOT=/Users/matheusbbarni/.local/go` while `/opt/homebrew/bin/go` is the working Go toolchain.

## Open Risks

- Task 07 still needs to align observation surfaces and broader end-to-end coverage on top of the completed handoff artifact behavior.

## Handoffs
