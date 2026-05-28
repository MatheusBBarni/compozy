---
status: pending
title: Make existing `--multiple` run correctly in sequential mode
type: backend
complexity: high
dependencies:
  - task_01
---

# Task 02: Make existing `--multiple` run correctly in sequential mode

## Overview

This task makes the non-parallel path work first. It ensures `--multiple` can execute an explicit ordered subset of tasks through the normal sequential task-run pipeline, which gives the feature a stable baseline before worktree orchestration is introduced.

<critical>
- ALWAYS READ the PRD and TechSpec before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — every task MUST include tests in deliverables
</critical>

<requirements>
- 1. MUST implement selected-task execution through the existing `compozy tasks run --multiple` flag rather than adding a new selector flag.
- 2. MUST make `multiple = "sequential"` execute only the explicitly selected tasks within one workflow.
- 3. MUST preserve caller order for selected tasks through planning and prompt preparation.
- 4. MUST reject missing, completed, or duplicate selected tasks according to the TechSpec rules.
</requirements>

## Subtasks
- [ ] 2.1 Add selected-task validation against discovered workflow task entries.
- [ ] 2.2 Update planner and prompt preparation paths to use the explicit ordered subset when present.
- [ ] 2.3 Reuse the normal sequential task-run path for validated multi-task execution.
- [ ] 2.4 Confirm task-runtime rule behavior remains correct for selected-task runs.

## Implementation Details

Reference the TechSpec sections "Data Flow", "Task Run Request Additions", and "Development Sequencing". This task should land a working sequential path without depending on worktree or parent-child orchestration.

### Relevant Files
- `internal/core/plan/input.go` — current task discovery still reads all pending tasks from `TasksDir`.
- `internal/core/plan/prepare.go` — current planning and grouping logic must honor explicit subsets.
- `internal/core/prompt/common.go` — prompt preparation currently relies on discovered task ordering.
- `internal/core/tasks/walker.go` — canonical task discovery and current sorting behavior.
- `internal/core/model/task_runtime.go` — existing task-runtime rules should align with selected-task identity.
- `internal/daemon/run_manager.go` — current sequential task-run path that should be reused.
- `internal/cli/task_runtime_form.go` — useful precedent for task-specific identifier handling in CLI flows.

### Dependent Files
- `internal/core/model/runtime_config.go` — sequential selected-task data must flow through runtime config.
- `internal/core/workspace/config_types.go` — multiple-mode config influences whether this path remains sequential.
- `internal/cli/daemon_commands.go` — CLI behavior must feed the selected-task list into the sequential task-run path.
- `internal/api/contract/types.go` — request structs must carry selected-task data to the daemon.
- `internal/core/tasks/walker_test.go` — ordering and task identity coverage will need explicit selected-task cases.
- `internal/core/plan/prepare_test.go` — planner tests should validate order preservation and error cases.

### Related ADRs
- [ADR-001: Bound V1 to Configurable Parallel Worktree Mode With Agent Handoff](adrs/adr-001.md) — Keeps sequential execution as the safe default.
- [ADR-004: Carry Parallel Mode and Explicit Task Selection as First-Class Run Inputs](adrs/adr-004.md) — Governs selected-task identity and explicit request semantics.

## Deliverables
- A working sequential `--multiple` path for selected tasks within one workflow.
- Planner behavior that preserves explicit task order and validates task selection boundaries.
- Updated tests for selected-task validation, ordering, and sequential execution.
- Unit tests with 80%+ coverage **(REQUIRED)**
- Integration tests for selected-task sequential execution **(REQUIRED)**

## Tests
- Unit tests:
  - [ ] Explicit selected-task order is preserved through planning and prompt preparation.
  - [ ] Duplicate, missing, and completed selected tasks produce the expected validation errors.
  - [ ] Task-runtime rules still match the correct selected task identifiers.
- Integration tests:
  - [ ] `tasks run --multiple` executes only the selected tasks in sequential mode through the normal task-run path.
  - [ ] `tasks run --multiple` with `multiple = "sequential"` preserves selected-task order from CLI selection through execution.
  - [ ] Sequential selected-task execution leaves unselected workflow tasks untouched.
- Test coverage target: >=80%
- All tests must pass

## Success Criteria
- All tests passing
- Test coverage >=80%
- Sequential `--multiple` behaves correctly before any parallel orchestration is enabled.
- Explicit task order is preserved end-to-end for sequential selected-task runs.
