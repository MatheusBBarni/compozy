---
status: pending
title: Persist handoff artifacts and final operator summaries
type: backend
complexity: high
dependencies:
  - task_04
  - task_05
---

# Task 06: Persist handoff artifacts and final operator summaries

## Overview

This task produces the user-visible finish line for parallel runs. It writes the durable handoff artifacts required by the TechSpec and ADRs, then surfaces a concise final summary plus a copy-paste-ready prompt pointer so the operator can move directly into fan-in and review work.

<critical>
- ALWAYS READ the PRD and TechSpec before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — every task MUST include tests in deliverables
</critical>

<requirements>
- 1. Parent parallel runs MUST persist `parallel-handoff.md`, `parallel-summary.json`, and `parallel-worktrees.json` under the run artifact directory.
- 2. `parallel-worktrees.json` MUST record the task-to-worktree-folder mapping for every selected task.
- 3. Final operator output MUST expose the handoff result in a concise form while keeping the durable artifact path discoverable.
- 4. Parent artifact content MUST summarize child outcomes, retained worktree locations, and the next fan-in step without mutating workflow files.
</requirements>

## Subtasks
- [ ] 6.1 Define and write the durable parent handoff artifact set in the run directory.
- [ ] 6.2 Build the machine-readable outcome summary and worktree manifest from parent-child results.
- [ ] 6.3 Surface a concise final summary and prompt pointer in operator-facing output.
- [ ] 6.4 Add regression coverage for artifact paths, contents, and final summary behavior.

## Implementation Details

Reference the TechSpec sections "Parent-Run Artifact Files", "Monitoring and Observability", and "Known Risks". Keep durable content in run artifacts and avoid shifting this output into `.compozy/tasks/<slug>`.

### Relevant Files
- `internal/core/model/artifacts.go` — canonical run artifact layout and helper paths.
- `internal/daemon/task_multi.go` — natural parent-run seam for aggregating and writing handoff output.
- `internal/core/plan/prepare.go` — existing prompt and run metadata persistence pattern.
- `internal/core/run/executor/result.go` — durable result artifact writer precedent.
- `internal/daemon/run_manager.go` — daemon terminal summary plumbing via `SummaryMessage`.
- `internal/cli/run_observe.go` — final operator-facing run summary rendering.

### Dependent Files
- `pkg/compozy/events/kinds/run.go` — terminal payloads may need summary or artifact pointer alignment.
- `pkg/compozy/events/kinds/task.go` — task-multi event/schema changes may be needed if parent summary state expands.
- `internal/api/contract/types.go` — snapshot/detail contracts may expose new handoff artifact metadata.
- `pkg/compozy/runs/run.go` — public run readers consume terminal events and artifact paths.
- `internal/core/run/ui/multi_remote.go` — completed parent tabs may need final summary visibility.
- `internal/core/plan/prepare_test.go` — existing artifact-path tests are a strong pattern for new handoff files.

### Related ADRs
- [ADR-001: Bound V1 to Configurable Parallel Worktree Mode With Agent Handoff](adrs/adr-001.md) — The product promise includes a provider-neutral handoff outcome.
- [ADR-002: Position V1 as a Safe Review Lane for Parallel Task Worktrees](adrs/adr-002.md) — The review-first framing depends on a clear post-run handoff.
- [ADR-005: Defer Source Workflow Mutation and Persist the Handoff Prompt as a Run Artifact](adrs/adr-005.md) — Directly defines the artifact location and output behavior.

## Deliverables
- Durable parent-run handoff artifacts written in the run directory.
- Final operator summary output that surfaces the prompt and artifact location without bloating event payloads.
- Coverage for artifact content, path layout, and summary rendering.
- Unit tests with 80%+ coverage **(REQUIRED)**
- Integration tests for handoff artifact persistence and final summary behavior **(REQUIRED)**

## Tests
- Unit tests:
  - [ ] Parent summary JSON contains selected tasks, child outcomes, and handoff artifact paths.
  - [ ] `parallel-worktrees.json` records the expected one-task-to-one-worktree-folder mapping, child run IDs, and retained worktree paths.
  - [ ] Final summary rendering trims and displays the expected artifact pointer text.
- Integration tests:
  - [ ] Completed parallel parent runs write all required handoff artifacts in the expected run directory.
  - [ ] Operator-facing output includes the final prompt pointer without requiring workflow-file mutation.
- Test coverage target: >=80%
- All tests must pass

## Success Criteria
- All tests passing
- Test coverage >=80%
- Parallel parent runs finish with durable handoff artifacts and usable operator output.
- No handoff artifact is written into source workflow files during parallel execution.
