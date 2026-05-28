---
status: pending
title: Implement parallel parent-child orchestration for selected tasks
type: backend
complexity: critical
dependencies:
  - task_02
  - task_03
---

# Task 04: Implement parallel parent-child orchestration for selected tasks

## Overview

This task delivers the core parallel behavior. It adds the parent-child coordinator that launches one child run per selected task in its assigned worktree, preserves task order for visible tabs, and aggregates the parent run outcome from terminal child states.

<critical>
- ALWAYS READ the PRD and TechSpec before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — every task MUST include tests in deliverables
</critical>

<requirements>
- 1. Parallel mode MUST create exactly one child run per selected task.
- 2. Parallel mode MUST use a one-to-one mapping between selected task, retained worktree folder, and child run.
- 3. Parent-child orchestration MUST preserve selected-task order for snapshots, tabs, and outcome aggregation.
- 4. Cancellation, wait, and failure handling MUST remain explicit and observable at the parent level.
</requirements>

## Subtasks
- [ ] 4.1 Extend the daemon task-run start path to branch into parent-child orchestration when multiple-mode is parallel.
- [ ] 4.2 Launch exactly one child run per selected task, using that task’s assigned worktree folder and parent linkage.
- [ ] 4.3 Aggregate terminal child outcomes into a stable parent run summary and snapshot model.
- [ ] 4.4 Preserve ordered parent-child observation behavior for tabs, waits, and cancellation.

## Implementation Details

Reference the TechSpec sections "Parallel Task Coordinator", "Data Flow", and "Development Sequencing". Keep parallelism in daemon orchestration rather than inside the PRD task executor.

### Relevant Files
- `internal/daemon/task_multi.go` — closest existing parent queue/orchestration pattern.
- `internal/daemon/run_manager.go` — task start preparation, run-mode dispatch, and child run start seams.
- `internal/core/run/ui/multi_remote.go` — tabbed multi-run behavior that depends on ordered parent-child state.
- `internal/cli/run_observe.go` — CLI attach and stream rendering for multi-run lifecycles.
- `pkg/compozy/events/kinds/task.go` — task-multi event payloads consumed by CLI and UI.
- `internal/api/contract/types.go` — snapshot and transport types for parent-child state.

### Dependent Files
- `internal/core/model/runtime_config.go` — child runs rely on correct parent linkage and selected-task state.
- `internal/core/plan/input.go` — parent orchestration depends on selected-task filtering already being correct.
- `internal/daemon/query_service.go` — child visibility and parent summary behavior depend on parent-child linkage.
- `internal/daemon/task_multi_test.go` — parent/child lifecycle coverage will expand here.
- `internal/core/run/ui/multi_remote_test.go` — tab ordering and attach semantics depend on stable event order.

### Related ADRs
- [ADR-003: Orchestrate Parallel `--multiple` Runs as Parent and Child Runs](adrs/adr-003.md) — Directly defines this architecture choice and its trade-offs.
- [ADR-004: Carry Parallel Mode and Explicit Task Selection as First-Class Run Inputs](adrs/adr-004.md) — The coordinator depends on explicit multiple-mode and selected-task inputs.

## Deliverables
- Parent-child daemon orchestration for selected-task parallel runs.
- Stable parent snapshots and event flow that preserve selected-task order.
- Coverage for child launch, terminal aggregation, cancellation, and mixed-result scenarios.
- Unit tests with 80%+ coverage **(REQUIRED)**
- Integration tests for parent-child orchestration **(REQUIRED)**

## Tests
- Unit tests:
  - [ ] Parent coordination preserves request order when building child run state.
  - [ ] Parent aggregation derives completed, failed, and canceled outcomes from child terminal states.
  - [ ] Parent cancellation propagates correctly to running and queued child runs.
- Integration tests:
  - [ ] Parallel mode launches one child run per selected task with correct parent linkage.
  - [ ] Mixed child outcomes produce the expected parent snapshot and terminal state.
- Test coverage target: >=80%
- All tests must pass

## Success Criteria
- All tests passing
- Test coverage >=80%
- Parallel mode uses daemon parent-child orchestration instead of executor-level PRD parallelism.
- Parent snapshots, tabs, and terminal outcomes remain ordered and explainable to the operator.
