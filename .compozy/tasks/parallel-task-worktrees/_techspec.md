# TechSpec: Parallel Task Worktrees

## Executive Summary

This change adds a first-class parallel execution mode for PRD task runs while keeping sequential execution as the default. The implementation extends the existing task-run start path, carries explicit selected-task inputs across the CLI and daemon boundary, and introduces a daemon-side parent coordinator that launches one isolated child run per selected task when the effective multiple-task mode is `parallel`.

The design deliberately avoids executor-level parallelism inside a single PRD task run. Instead, it trades a more complex parent/child run model for stronger isolation, clearer per-task ownership, and a safer source-workspace story. The main technical trade-off is that task execution and workflow reconciliation become two distinct phases: child runs produce isolated outputs plus a durable handoff prompt, while source workflow task completion remains deferred to a later fan-in step.

The implementation reuses the existing `compozy tasks run --multiple` selector as the user entrypoint for selected-task execution. Sequential remains the default behavior for that flow, and parallel becomes the opt-in mode that assigns one child run and one retained worktree folder to each selected task.

## System Architecture

### Component Overview

#### 1. Task Selection Surface

The CLI `tasks run --multiple` flow remains the user entrypoint for selecting a subset of task files. It must produce an ordered list of canonical task identifiers and the effective multiple-task mode. Web UI support is not required for V1.

#### 2. Task Run API Boundary

The existing `POST /api/tasks/:slug/runs` path remains the daemon entrypoint. Its request contract is extended with first-class fields for selected tasks and the effective multiple-task mode. The daemon must no longer infer “all pending tasks” when explicit selection is present.

#### 3. Parallel Task Coordinator

A daemon-local coordinator branches from the current task-run start path. When the effective mode is `sequential`, the existing planner and executor path stays intact. When the effective mode is `parallel`, the coordinator validates inputs, prepares isolated worktrees, starts one child run per selected task, waits for terminal child states, and aggregates final outcomes.

#### 4. Worktree Provisioning Layer

A new daemon-owned worktree helper manages isolated child execution roots. Its responsibilities are:
- validate repository and source-workspace safety prerequisites,
- create one retained worktree per selected task,
- return per-task worktree paths for child runs,
- record worktree paths in parent-run artifacts.

This layer should stay daemon-local in V1. A reusable cross-package abstraction is unnecessary until another feature needs the same lifecycle.

#### 5. Child Task Runs

Each child run receives:
- the same workflow slug,
- exactly one selected task identifier,
- an isolated worktree root,
- inherited runtime defaults and per-task runtime overrides,
- a `ParentRunID` pointing back to the parent coordinator run.

Child runs continue using the existing PRD task execution machinery, but they operate against their own isolated workspace root and selected single-task scope.

#### 6. Outcome Aggregation and Handoff Writer

After child runs finish, the parent coordinator derives per-task outcomes, writes a machine-readable summary plus a copy-paste handoff prompt into run artifacts, and emits a short final summary that points to the durable artifact and echoes the prompt text for immediate reuse.

#### 7. Deferred Fan-In Boundary

V1 does not reconcile child worktrees back into the source workflow automatically. Source task markdown remains unchanged during child execution. The parent run’s handoff output becomes the contract for the later fan-in step that a user or external agent performs from the source workspace.

### Data Flow

1. User selects tasks through `tasks run --multiple`.
2. CLI sends workflow slug, selected task identifiers, and effective multiple-task mode to the daemon.
3. Daemon validates selection and chooses sequential or parallel execution.
4. In parallel mode, the coordinator creates one worktree per selected task and starts one child run per worktree.
5. Each child run executes one task in isolation and persists normal run artifacts for that child.
6. Parent waits for all child terminal states, computes a user-facing outcome summary, writes handoff artifacts, and emits final summary output.
7. A later fan-in step uses the generated prompt and preserved worktrees to prepare reviewable branches or PR work from the source workspace.

## Implementation Design

### Core Interfaces

```go
type ParallelTaskRequest struct {
	WorkflowSlug  string
	MultipleMode  MultipleMode
	SelectedTasks []string
	SourceRoot    string
	ParentRunID   string
}
```

```go
type ParallelTaskCoordinator interface {
	Prepare(ctx context.Context, req ParallelTaskRequest) (ParallelTaskPlan, error)
	StartChild(ctx context.Context, plan ParallelTaskPlan, task SelectedTask) (string, error)
	Finalize(ctx context.Context, plan ParallelTaskPlan, outcomes []ChildOutcome) (HandoffArtifacts, error)
}
```

#### Interface Notes

- `MultipleMode` is a new typed value with `sequential` and `parallel`.
- `SelectedTasks` uses canonical task identifiers, not arbitrary file paths.
- `Finalize` owns only parent-run summary and artifact generation. It does not reconcile work back into the source workflow.

### Data Models

#### 1. Config Additions

Extend `workspace.TaskRunConfig` with:

- `Multiple *string \`toml:"multiple"\``

Allowed values:
- `sequential` — default behavior
- `parallel` — parent/child worktree orchestration when selection contains more than one task

Validation rules:
- empty resolves to `sequential`
- any other value is rejected during config validation
- `parallel` has no effect unless the task run uses explicit multi-task selection

#### 2. Task Run Request Additions

Extend `contract.TaskRunRequest` with:

- `MultipleMode string \`json:"multiple_mode,omitempty"\``
- `SelectedTasks []string \`json:"selected_tasks,omitempty"\``

Rules:
- `selected_tasks` preserves user order from the selector
- duplicates are rejected before daemon dispatch
- missing tasks are rejected during daemon validation
- completed tasks are rejected unless `include_completed=true`

#### 3. RuntimeConfig Additions

Extend `model.RuntimeConfig` with:

- `MultipleMode string`
- `SelectedTasks []string`

Rules:
- sequential runs may leave `SelectedTasks` empty and retain current directory-scan behavior
- parallel child runs must carry exactly one selected task
- parent coordinator runs must carry the full ordered selection

#### 4. Canonical Task Identity

The canonical selected-task value is the task logical name that matches the filename stem, for example `task_01`. Validation resolves the selection against discovered task entries from `tasks.ReadTaskEntries`. The planner preserves caller order after validation rather than re-sorting selected tasks.

#### 5. Parent-Run Artifact Files

Persist these new files under the parent run directory:

- `parallel-handoff.md` — copy-paste-ready prompt for the user’s chosen LLM or coding agent
- `parallel-summary.json` — machine-readable parent summary
- `parallel-worktrees.json` — selected task to worktree path mapping and child run IDs

Suggested `parallel-summary.json` shape:

- `workflow_slug`
- `multiple_mode`
- `selected_tasks`
- `source_workspace_root`
- `child_outcomes[]` with:
  - `task_name`
  - `child_run_id`
  - `worktree_path`
  - `run_status`
  - `display_status`
  - `changed_workspace`
  - `head_ref`
  - `branch_name`
- `handoff_prompt_path`

`display_status` is a parent-derived value for user-facing summaries. V1 should support at least:
- `completed`
- `failed`
- `canceled`
- `unchanged`

#### 6. Worktree Metadata

Each selected task needs:
- canonical task name
- isolated worktree path outside the source workspace root
- branch name derived from parent run ID plus task name
- child run ID once launched

The exact base directory can remain daemon-managed, but it must be outside the source workspace root and recorded in `parallel-worktrees.json`.

### API Endpoints

#### POST `/api/tasks/:slug/runs`

Extends the existing daemon start-run endpoint for PRD tasks.

Request body:
- `workspace`
- `presentation_mode`
- `runtime_overrides`
- `multiple_mode`
- `selected_tasks`

Behavior:
- If `multiple_mode` is absent or `sequential`, use the existing single-run path.
- If `multiple_mode` is `parallel`, validate `selected_tasks`, create a parent coordinator run, and launch one child run per selected task.
- If `selected_tasks` is empty while `multiple_mode=parallel`, reject the request.
- If the selector exists on mainline but not on the working branch, this endpoint contract still remains the source of truth for the implementation.

Response:
- unchanged top-level run envelope
- run ID returned is the parent run ID in parallel mode

No new daemon endpoint is required for V1.

## Integration Points

### Local Git Repository and Git Worktree Commands

Purpose:
- create isolated child execution roots
- inspect dirty-source conditions
- preserve retained worktrees for later review and fan-in

Boundary rules:
- repository must have a valid `.git` directory
- parallel mode should fail fast if the repo cannot support worktrees safely
- worktrees must be created outside the source workspace root
- V1 should reject unsafe source conditions rather than guessing

Authentication and authorization:
- not applicable

Error handling:
- fail the parent run before child launch if worktree provisioning preconditions are not met
- do not retry `git worktree add` automatically
- report the failing selected task and worktree path in parent summary output

## Impact Analysis

| Component | Impact Type | Description and Risk | Required Action |
|-----------|-------------|---------------------|-----------------|
| `internal/core/workspace/config_types.go` and merge/validation paths | modified | Add `multiple` config semantics; low risk | Extend config structs, merge logic, and validation |
| CLI `tasks run --multiple` selector path | modified | Current branch does not show the selector; medium risk | Align with mainline selector or add missing CLI surface |
| `internal/api/contract/types.go` and OpenAPI schema | modified | Request contract expands with new task-selection semantics; medium risk | Add first-class request fields and schema coverage |
| `internal/daemon/run_manager.go` | modified | New coordinator branch and parent/child orchestration; high risk | Add parallel start path, validation, and aggregation |
| daemon-local worktree helper | new | New lifecycle code for isolated retained worktrees; high risk | Implement safe provisioning and retention rules |
| `internal/core/plan/*` | modified | Planner must honor explicit selected tasks; medium risk | Add selection validation and ordered planning |
| child task completion path in executor hooks | modified | Current success path mutates workflow task files directly; high risk | Bypass direct source mutation for parallel child runs |
| run artifact writer and summary rendering | modified | New durable handoff artifacts and final summary output; medium risk | Add artifact files and concise summary pointers |
| query/read-model surfaces | modified | Parent/child visibility may confuse dashboards; medium risk | Decide how parent and child runs appear in watch and list views |
| web task-start surface | deferred | No V1 web multi-selector required | Leave unchanged for V1 |

## Testing Approach

### Unit Tests

- Config parsing and merge behavior for `multiple = "sequential" | "parallel"`.
- Validation of `selected_tasks`, including duplicates, missing tasks, completed tasks, and preserved ordering.
- Parent outcome aggregation from child run terminal states plus workspace-change evidence.
- Handoff artifact writer output, including prompt path, inline summary pointer, and JSON manifest shape.
- Branch naming and worktree path derivation helpers.

Mock boundaries:
- use focused fakes for daemon child-run launching and prompt rendering
- avoid mocking git behavior where real temp repositories are practical

Critical scenarios:
- sequential default with no explicit multiple mode
- parallel request with one task
- parallel request with many tasks
- duplicate selected task IDs
- no-op child run producing `unchanged`
- failed child run mixed with successful child runs

### Integration Tests

- Parent run launches one child run per selected task and waits for terminal states.
- Worktree creation happens outside the source workspace root.
- Child runs execute against isolated workspace roots and do not mutate source workflow task files.
- Parent artifacts are written and final summary includes both prompt text and durable artifact location.
- Parallel mode rejects unsafe source conditions before any child launch.
- Sequential mode still uses the existing ordered execution path when the effective config is `sequential`.
- If the working branch lacks the selector, CLI coverage must include the new `--multiple` flow as part of the feature.

Test data and setup:
- use real temporary git repositories with committed `HEAD`
- create temp workflow task directories under `.compozy/tasks/<slug>`
- use stub or fake agent runtimes that write predictable file changes into child worktrees
- retain child worktrees until assertions finish

Environment dependencies:
- local git executable available in test environment
- daemon test harness able to observe parent and child run rows/events

## Development Sequencing

### Build Order

1. Extend config, request, and runtime data models for `multiple_mode` and `selected_tasks` — no dependencies.
2. Add selection validation and planner support for explicit selected tasks — depends on step 1.
3. Implement daemon-local worktree provisioning and safety checks — depends on step 1 and step 2.
4. Add the parallel parent coordinator path in `RunManager` with one child run per selected task — depends on step 1, step 2, and step 3.
5. Implement deferred source-mutation behavior plus parent artifact and handoff prompt writing — depends on step 4.
6. Wire final run summary output and child-run visibility behavior — depends on step 4 and step 5.
7. Add CLI selector alignment and end-to-end integration coverage for `--multiple` — depends on step 1, step 2, step 4, step 5, and step 6.

### Technical Dependencies

- The intended `compozy tasks run --multiple` selector must exist on the implementation branch or be added as part of this work.
- Parallel mode requires a valid git repository with worktree support.
- The implementation must define a worktree base path outside the source workspace root.
- Parent and child run visibility rules must remain compatible with existing run observation flows.

## Monitoring and Observability

- Track total selected tasks, launched child runs, successful child runs, failed child runs, canceled child runs, and unchanged child runs.
- Track worktree provisioning failures separately from child execution failures.
- Log structured fields: `workflow_slug`, `parent_run_id`, `child_run_id`, `task_name`, `multiple_mode`, `selected_task_count`, `worktree_path`, `source_workspace_root`.
- Include final summary fields that let operators locate `parallel-handoff.md` and `parallel-summary.json` immediately.
- No external paging is required for V1, but the parent run must fail loudly if any preflight safety check or worktree provisioning step fails.

## Technical Considerations

### Key Decisions

- **Daemon coordinator over executor concurrency:** Parallel PRD execution lives in daemon orchestration, not inside ordered executor workers.
- **First-class selection contract:** Mode and selected tasks cross the CLI, API, and runtime boundary as typed fields.
- **Deferred source reconciliation:** Child runs do not update source workflow task files during execution.
- **Durable plus inline handoff output:** The copy-paste prompt is persisted in run artifacts and echoed in final output.
- **Typed outcome aggregation:** Parent summaries derive user-facing status from child terminal status plus workspace-change evidence.

### Known Risks

- **Source-workspace ambiguity:** existing run artifacts and workflow metadata may still write under the workspace root; implementation must keep user-code and workflow-task mutation out of the source workspace during child execution.
- **Selector drift:** if `--multiple` lives only on mainline today, implementation can stall until the branch is aligned.
- **Parent and child visibility:** current dashboards and observers are job-oriented; child-run presentation may need careful filtering to avoid noise.
- **Two-phase truth model:** successful child execution is not the same as final workflow reconciliation, and naming must reflect that clearly.
- **Prompt size creep:** full multiline prompts should live in `parallel-handoff.md`; final summary output should surface only the copy-paste body plus a durable path, not an oversized event payload.

## Architecture Decision Records

- [ADR-001: Bound V1 to Configurable Parallel Worktree Mode With Agent Handoff](adrs/adr-001.md) — V1 stays opt-in, protects the source workspace, retains outputs, and avoids automatic merge behavior.
- [ADR-002: Position V1 as a Safe Review Lane for Parallel Task Worktrees](adrs/adr-002.md) — V1 is framed as a review-first throughput feature for solo maintainers rather than bare isolation or a larger release system.
- [ADR-003: Orchestrate Parallel `--multiple` Runs as Parent and Child Runs](adrs/adr-003.md) — Parallel task execution uses a parent coordinator run with one isolated child run per selected task.
- [ADR-004: Carry Parallel Mode and Explicit Task Selection as First-Class Run Inputs](adrs/adr-004.md) — The run contract adds typed fields for multiple-task mode and selected task identifiers.
- [ADR-005: Defer Source Workflow Mutation and Persist the Handoff Prompt as a Run Artifact](adrs/adr-005.md) — Child runs leave source workflow files untouched and the parent writes durable handoff artifacts plus inline prompt output.
