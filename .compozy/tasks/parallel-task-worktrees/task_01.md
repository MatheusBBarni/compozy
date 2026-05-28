---
status: completed
title: Align multi-task run contracts and config semantics
type: backend
complexity: high
dependencies: []
---

# Task 01: Align multi-task run contracts and config semantics

## Overview

This task establishes the contract that all later work depends on. It aligns config, the existing `compozy tasks run --multiple` entrypoint, daemon request models, and runtime config so selected-task execution and `multiple = "sequential" | "parallel"` are represented explicitly instead of being inferred from older multi-run behavior.

<critical>
- ALWAYS READ the PRD and TechSpec before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — every task MUST include tests in deliverables
</critical>

<requirements>
- 1. MUST align the task-run config contract with `[tasks.run] multiple = "sequential" | "parallel"`.
- 2. MUST preserve `sequential` as the default behavior for the existing `compozy tasks run --multiple` flow.
- 3. MUST expose selected-task and multiple-mode inputs as first-class CLI, API, and runtime fields rather than encoding them only inside `runtime_overrides`.
- 4. MUST reject malformed or duplicate selected-task input at the earliest practical boundary.
</requirements>

## Subtasks
- [x] 1.1 Replace the current cross-workflow `--multiple` contract assumptions with the task-scoped contract defined in the TechSpec.
- [x] 1.2 Add explicit config and runtime fields for multiple-mode and selected-task inputs.
- [x] 1.3 Update request, route, and client contracts so daemon task runs receive the new first-class fields.
- [x] 1.4 Align validation rules, defaults, and public help/schema output with the approved contract.

## Implementation Details

Focus on the TechSpec sections "System Architecture", "Implementation Design", and "Data Models". This task is the contract baseline for all later sequential, worktree, and parent-child orchestration tasks.

### Relevant Files
- `internal/core/workspace/config_types.go` — current task-run config model and the `run_multiple_mode` field.
- `internal/core/workspace/config_validate.go` — central validation for task-run config values.
- `internal/core/workspace/config_merge.go` — merge behavior for workspace and global task-run config.
- `internal/cli/daemon_commands.go` — current `--multiple` CLI handling and request shaping.
- `internal/api/contract/types.go` — current task-run and task-multi request types.
- `internal/api/core/routes.go` — exposed daemon route layout for task runs.
- `internal/api/client/client.go` — client-side request/route behavior that must stay aligned.
- `internal/core/model/runtime_config.go` — runtime struct that must gain the first-class fields.

### Dependent Files
- `internal/api/core/handlers.go` — request binding and transport validation depend on the updated contract.
- `internal/daemon/run_manager.go` — runtime assembly depends on the new config and request fields.
- `internal/daemon/task_multi.go` — current multi-run coordinator semantics are affected by the contract change.
- `internal/cli/run_observe.go` — user-visible language may need alignment with the new semantics.
- `openapi/compozy-daemon.json` — public schema must reflect the updated request model.
- `web/src/generated/compozy-openapi.d.ts` — generated types must stay in sync with schema changes.
- `README.md` — config/help docs currently describe the old `enqueued|parallel` semantics.

### Related ADRs
- [ADR-003: Orchestrate Parallel `--multiple` Runs as Parent and Child Runs](adrs/adr-003.md) — Defines the parent/child run approach that depends on this contract layer.
- [ADR-004: Carry Parallel Mode and Explicit Task Selection as First-Class Run Inputs](adrs/adr-004.md) — Directly governs the field and validation design for this task.

## Deliverables
- Updated task-run config, CLI, API, and runtime contracts aligned with the TechSpec.
- Validation coverage for defaults, invalid values, and duplicate input rejection.
- Updated schema/help output for the new multi-task contract.
- Unit tests with 80%+ coverage **(REQUIRED)**
- Integration tests for contract and handler/client alignment **(REQUIRED)**

## Tests
- Unit tests:
  - [x] Config parsing accepts the sequential default and explicit parallel opt-in for task runs.
  - [x] Validation rejects duplicate selected-task identifiers and invalid multiple-mode values.
  - [x] Runtime config cloning preserves the new selected-task and multiple-mode fields.
- Integration tests:
  - [x] CLI request construction sends the new first-class fields to the daemon task-run endpoint.
  - [x] API handler and client contract tests confirm request and schema alignment for the new fields.
- Test coverage target: >=80%
- All tests must pass

## Success Criteria
- All tests passing
- Test coverage >=80%
- Config, CLI, API, and runtime layers all expose the same task-scoped multi-run contract.
- No remaining public contract path requires selected-task semantics to be inferred from slug lists alone.
