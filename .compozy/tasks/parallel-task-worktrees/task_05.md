---
status: pending
title: Prevent source-workflow mutation during parallel child execution
type: backend
complexity: high
dependencies:
  - task_02
  - task_03
  - task_04
---

# Task 05: Prevent source-workflow mutation during parallel child execution

## Overview

This task enforces the strongest trust invariant in the feature: child runs may succeed, fail, or do nothing, but they must not mutate source workflow state while they execute. It separates child execution truth from later source reconciliation by blocking task-file completion and source-root sync assumptions in parallel mode.

<critical>
- ALWAYS READ the PRD and TechSpec before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — every task MUST include tests in deliverables
</critical>

<requirements>
- 1. Parallel child runs MUST NOT mark source workflow task files completed during execution.
- 2. Source-root sync and watcher behavior MUST NOT mutate workflow metadata as a side effect of child execution.
- 3. Child outcomes MUST distinguish execution success from later source reconciliation.
- 4. Unchanged child worktree outcomes SHOULD remain evidence-based using the existing workspace snapshot model.
</requirements>

## Subtasks
- [ ] 5.1 Gate task-file completion behavior for parallel child runs.
- [ ] 5.2 Remove or redirect source-root sync/watch assumptions from child execution paths.
- [ ] 5.3 Define the parent-side truth model for completed, failed, canceled, and unchanged child outcomes.
- [ ] 5.4 Add regression coverage that proves source workflow files remain unchanged during parallel execution.

## Implementation Details

Reference the TechSpec sections "Deferred Fan-In Boundary", "Known Risks", and "Technical Considerations". This task must protect against both direct task-file completion and indirect workflow mutations caused by sync or watcher paths.

### Relevant Files
- `internal/core/run/executor/review_hooks.go` — current PRD success path marks source task files completed.
- `internal/core/tasks/store.go` — source task-file rewrite implementation.
- `internal/core/run/executor/runner.go` — pre-run workspace snapshot capture consumed by completion hooks.
- `internal/core/run/internal/worktree/snapshot.go` — unchanged detection and git-based evidence model.
- `internal/daemon/run_manager.go` — pre-run sync and watcher setup currently assume source workflow roots.
- `internal/core/sync.go` — workflow metadata sync logic can mutate workflow files and catalog state.
- `internal/daemon/watchers.go` — live workflow watcher behavior that must not interfere with child execution isolation.

### Dependent Files
- `internal/daemon/task_multi.go` — parent aggregation depends on the new child outcome truth model.
- `internal/api/contract/types.go` — snapshots may need richer outcome/status projection.
- `internal/core/run/ui/multi_remote.go` — UI currently knows only queued/running/completed/failed/canceled states.
- `internal/daemon/query_service.go` — dashboard visibility and run status projection depend on parent-child state.
- `internal/core/run/executor/execution_test.go` — existing task completion tests must gain parallel-mode coverage.
- `internal/daemon/watchers_test.go` — watcher regression coverage is relevant to source-workflow protection.

### Related ADRs
- [ADR-001: Bound V1 to Configurable Parallel Worktree Mode With Agent Handoff](adrs/adr-001.md) — Source workspace protection is a hard V1 constraint.
- [ADR-005: Defer Source Workflow Mutation and Persist the Handoff Prompt as a Run Artifact](adrs/adr-005.md) — Directly defines this task’s mutation and truth-model boundary.

## Deliverables
- Parallel child execution paths that leave source workflow task files unchanged.
- Source-root sync/watch behavior that no longer mutates workflow state as part of child execution.
- Explicit outcome rules for completed, failed, canceled, and unchanged child runs.
- Unit tests with 80%+ coverage **(REQUIRED)**
- Integration tests for source-workflow protection **(REQUIRED)**

## Tests
- Unit tests:
  - [ ] Parallel child success does not call source task completion logic.
  - [ ] Unchanged child worktree results produce the expected non-reconciled outcome.
  - [ ] Parallel child execution does not trigger source-root workflow mutation through sync/watch hooks.
- Integration tests:
  - [ ] Source workflow task files remain byte-for-byte unchanged while parallel child runs execute.
  - [ ] Parent outcomes distinguish child execution success from source workflow reconciliation state.
- Test coverage target: >=80%
- All tests must pass

## Success Criteria
- All tests passing
- Test coverage >=80%
- Source workflow files remain unchanged during parallel child execution.
- Parallel-mode truth is represented without falsely implying source reconciliation already happened.
