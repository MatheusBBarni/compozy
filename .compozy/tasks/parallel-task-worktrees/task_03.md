---
status: completed
title: Implement one-worktree-per-task creation and lifecycle
type: backend
complexity: high
dependencies:
  - task_01
---

# Task 03: Implement one-worktree-per-task creation and lifecycle

## Overview

This task introduces the dedicated daemon-local worktree layer required by the parallel design. It establishes the one-selected-task to one-git-worktree to one-folder lifecycle so later orchestration can rely on a tested isolation substrate.

<critical>
- ALWAYS READ the PRD and TechSpec before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — every task MUST include tests in deliverables
</critical>

<requirements>
- 1. MUST create exactly one retained git worktree per selected task in parallel mode.
- 2. MUST create exactly one distinct worktree folder per selected task and map that folder deterministically to the selected task.
- 3. MUST ensure every worktree folder is outside the source workspace root.
- 4. MUST expose retained worktree metadata in a form the parent coordinator can persist and hand off later.
</requirements>

## Subtasks
- [x] 3.1 Define the daemon-local worktree helper boundary and metadata model.
- [x] 3.2 Implement repository and source-workspace safety preflight checks for parallel task runs.
- [x] 3.3 Add deterministic branch naming and retained worktree path derivation.
- [x] 3.4 Implement worktree create/read lifecycle behavior with tests that cover retained-state expectations.

## Implementation Details

Reference the TechSpec sections "Worktree Provisioning Layer", "Worktree Metadata", and "Technical Dependencies". Keep the helper daemon-local in V1 instead of introducing a broader reusable abstraction.

### Relevant Files
- `internal/daemon/review_watch_git.go` — narrow git boundary pattern for daemon-owned git helpers.
- `internal/daemon/task_multi.go` — current parent coordinator that will eventually depend on worktree lifecycle behavior.
- `internal/daemon/run_manager.go` — child workspace roots and daemon lifecycle integration ultimately plug in here.
- `internal/core/run/internal/worktree/snapshot.go` — existing git-state helper and env sanitization precedent.
- `internal/core/model/artifacts.go` — stable run artifact layout that can anchor retained metadata references.
- `internal/config/home.go` — daemon home paths are a natural base for retained daemon-managed assets.

### Dependent Files
- `internal/core/model/runtime_config.go` — child runs may need worktree-related metadata propagated through runtime config.
- `internal/api/contract/types.go` — future snapshot or summary contracts may expose retained worktree metadata.
- `internal/daemon/task_multi_test.go` — orchestration tests will later depend on this lifecycle layer.
- `internal/daemon/review_watch_git_test.go` — git helper tests provide the closest test pattern.
- `internal/core/run/internal/worktree/snapshot_test.go` — real git repo testing patterns are relevant to this lifecycle.

### Related ADRs
- [ADR-003: Orchestrate Parallel `--multiple` Runs as Parent and Child Runs](adrs/adr-003.md) — Parent-child orchestration depends on isolated worktree lanes.
- [ADR-005: Defer Source Workflow Mutation and Persist the Handoff Prompt as a Run Artifact](adrs/adr-005.md) — Source-workspace protection relies on correctly isolated worktree roots.

## Deliverables
- A daemon-local worktree lifecycle helper with deterministic branch/worktree metadata.
- Safety preflight that blocks unsafe parallel runs before child launch.
- Real git-repo coverage for worktree lifecycle behavior and retained-state expectations.
- Unit tests with 80%+ coverage **(REQUIRED)**
- Integration tests for worktree creation/manipulation lifecycle **(REQUIRED)**

## Tests
- Unit tests:
  - [x] Branch and worktree naming are deterministic for a given parent run and selected task.
  - [x] Safety preflight rejects unsupported repository or workspace conditions.
  - [x] Retained worktree metadata includes the expected task and parent-run linkage.
- Integration tests:
  - [x] Real git repo setup provisions exactly one retained worktree folder for each selected task.
  - [x] Each provisioned worktree folder is outside the source workspace root.
  - [x] Worktree lifecycle operations leave the source workspace untouched during provisioning.
- Test coverage target: >=80%
- All tests must pass

## Success Criteria
- All tests passing
- Test coverage >=80%
- Parallel orchestration has a tested worktree substrate it can depend on.
- Unsafe worktree prerequisites fail before any child run or source mutation occurs.
