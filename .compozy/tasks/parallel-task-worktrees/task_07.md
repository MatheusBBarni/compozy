---
status: completed
title: Align run observation surfaces and end-to-end coverage
type: backend
complexity: high
dependencies:
  - task_04
  - task_05
  - task_06
---

# Task 07: Align run observation surfaces and end-to-end coverage

## Overview

This is an execution-only task against an already approved design. Apply the approved observation model to snapshots, CLI/TUI behavior, visibility rules, and coverage so sequential and parallel `--multiple` execution are observable and testable without reopening design work.

<critical>
- ALWAYS READ the PRD and TechSpec before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- PRD AND TECHSPEC ARE ALREADY APPROVED — do not reopen design review
- DO NOT INVOKE BRAINSTORMING OR CREATE NEW DESIGN DOCS — proceed directly to implementation and tests
- MINIMIZE EXPLORATION — prefer direct reads of the listed files over spawning exploratory subagents unless blocked
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — every task MUST include tests in deliverables
</critical>

<requirements>
- 1. Parent-child run observation surfaces MUST preserve selected-task order and accurately reflect the new outcome model.
- 2. Snapshot, query, and CLI/TUI visibility behavior MUST remain coherent when parent and child runs coexist.
- 3. Integration and E2E coverage MUST verify the existing `--multiple` flow in both `multiple = "sequential"` and `multiple = "parallel"` modes.
- 4. Coverage MUST verify that parallel mode creates one retained worktree folder per selected task and keeps the source workspace unchanged during child execution.
</requirements>

## Subtasks
- [x] 7.1 Align snapshot and read-model behavior with the new parent-child execution semantics.
- [x] 7.2 Update CLI/TUI observation and visibility behavior to preserve ordered parent-child run presentation.
- [x] 7.3 Add integration coverage for sequential and parallel multi-task observation paths.
- [x] 7.4 Add end-to-end coverage that exercises operator-visible behavior for the new execution model.

## Implementation Details

Reference the TechSpec sections "Monitoring and Observability", "Impact Analysis", and "Testing Approach". This task should validate the whole operator experience rather than only low-level coordinator logic.

Primary edit surface for this task should stay bounded to snapshot/query/observe/UI coverage files such as `internal/daemon/task_multi.go`, `internal/daemon/query_service.go`, `internal/cli/run_observe.go`, `internal/core/run/ui/multi_remote.go`, and the listed integration/E2E tests.

### Relevant Files
- `internal/daemon/task_multi.go` — parent-child event emission and snapshot reconstruction.
- `internal/daemon/query_service.go` — run visibility and dashboard filtering for parent/child relationships.
- `internal/daemon/run_snapshot.go` — single-run and compact snapshot construction logic.
- `internal/cli/run_observe.go` — CLI attach, warm snapshot behavior, and rendered parent lifecycle text.
- `internal/core/run/ui/multi_remote.go` — tabbed multi-run UI behavior for parent/child streams.
- `web/e2e/daemon-ui.smoke.spec.ts` — current daemon-served UI E2E baseline.
- `web/src/routes/-runs.integration.test.tsx` — current run-observation integration tests.

### Dependent Files
- `pkg/compozy/events/kinds/task.go` — observation surfaces depend on stable task-multi payload semantics.
- `internal/api/contract/types.go` — snapshots and detail contracts may need richer parent-child fields.
- `internal/api/client/runs.go` — remote watch decoding depends on snapshot/event payload shape.
- `pkg/compozy/runs/remote_watch.go` — reusable remote watch semantics for CLI observation.
- `internal/daemon/task_multi_test.go` — primary daemon-level orchestration coverage.
- `internal/core/run/ui/multi_remote_test.go` — tab order and attach behavior coverage.
- `internal/cli/root_command_execution_test.go` — CLI `--multiple --stream` behavior belongs here.

### Related ADRs
- [ADR-003: Orchestrate Parallel `--multiple` Runs as Parent and Child Runs](adrs/adr-003.md) — The visible tab/lane model is a direct requirement of this ADR.
- [ADR-005: Defer Source Workflow Mutation and Persist the Handoff Prompt as a Run Artifact](adrs/adr-005.md) — Observation surfaces must present execution truth without implying source reconciliation.

## Deliverables
- Updated observation surfaces for snapshots, CLI/TUI behavior, and parent-child visibility.
- Integration coverage for sequential and parallel selected-task execution.
- End-to-end coverage for operator-visible parent-child behavior and final summaries.
- Unit tests with 80%+ coverage **(REQUIRED)**
- Integration tests for observation and visibility behavior **(REQUIRED)**

## Tests
- Unit tests:
  - [x] Parent-child snapshot items remain in selected-task order across reconstruction and reattach.
  - [x] Visibility logic hides or shows child runs correctly based on parent state.
  - [x] CLI/TUI observation renders the expected final parent summary and child outcome transitions.
- Integration tests:
  - [x] Sequential `--multiple` observation shows only the selected tasks and correct terminal summaries.
  - [x] Parallel mode observation shows exactly one child lane per selected task and one retained worktree folder per selected task.
  - [x] Failure or unchanged child outcomes remain visible without implying source reconciliation.
  - [x] Parallel execution keeps the source workspace unchanged while child runs operate in separate worktree folders.
- Test coverage target: >=80%
- All tests must pass

## Verification Evidence

- `env -u GOROOT go test ./internal/daemon ./internal/cli ./internal/core/run/ui` passed after targeted edits.
- `env -u GOROOT make verify` passed: frontend lint/typecheck/tests/build, Go fmt/lint/tests/build, and frontend E2E all succeeded.

## Success Criteria
- All tests passing
- Test coverage >=80%
- Operators can observe sequential and parallel multi-task runs without ambiguity about task order or outcome state.
- End-to-end coverage proves the final user-visible execution model, not just isolated internal helpers.
