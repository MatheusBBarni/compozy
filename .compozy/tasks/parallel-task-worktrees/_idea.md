# Parallel Task Worktrees

## Overview

Compozy should extend the existing `tasks run --multiple` workflow with a configurable execution mode in `config.toml`.

`--multiple` already handles task selection. V1 should add a `multiple = "sequential"` default mode and a `multiple = "parallel"` mode. When parallel mode is enabled, Compozy runs the selected tasks concurrently in isolated git worktrees.

The feature solves the trust problem in parallel AI-assisted implementation: users want several agents working at once, but they do not want those agents to overwrite the main working directory or race on shared state. V1 should protect the source worktree, keep all generated worktrees, and hand the user's preferred agent a cleanup/fan-in prompt so the user can create a GitHub PR, GitLab MR, or another provider-specific integration path.

### Summary / Differentiator

Claude Code, OpenAI Codex, Cursor, and Jules all expose worktree-based or parallel agent execution. Compozy can differentiate by tying parallel execution to structured PRD task files and a provider-neutral handoff prompt. The product promise is not "more agents at once"; it is "run selected PRD tasks in parallel without clobbering your workspace, then hand off reconciliation to the agent/tool you already use."

## Problem

A solo maintainer using Compozy can select multiple generated task files, but the safe default should remain queued/sequential execution. That preserves predictability, but it wastes time when tasks are independent.

Manual worktree orchestration is error-prone. Users must create worktrees, launch agents, track outputs, preserve failed workspaces, and decide how to turn results into a PR or MR. This is especially risky because AI-generated work may be "almost right" and still require careful review.

The core user anxiety is not only speed. The user selected "Isolate agents" and "No conflicts" as the main problem and success signal. V1 should therefore optimize for trust first: keep `sequential` as the default, make `parallel` opt-in, isolate each selected task, retain all worktrees, and generate a clear agent prompt for cleanup/reconciliation.

### Market Data

- Stack Overflow 2025 reports that **84%** of developers use or plan to use AI tools, and **51%** of professional developers use AI tools daily.
- Stack Overflow 2025 reports trust friction: **46%** of respondents distrust AI output, and **66%** cite "almost right, but not quite" as the biggest frustration.
- Microsoft's 2025 Work Trend Index reports that **43%** of leaders already use multi-agent systems and **82%** expect to use AI agents to expand capacity in the next 12-18 months.
- Competitors already validate the worktree pattern: Claude Code, OpenAI Codex, Cursor, and Jules all support isolated or parallel agent sessions.

## Core Features

| # | Feature | Priority | Description |
|---|---|---|---|
| F1 | Configurable multiple-task execution mode | Critical | Add `multiple = "sequential"` as the default and `multiple = "parallel"` as the opt-in mode in `config.toml`. |
| F2 | Existing multiple task selection | Critical | Reuse the current `tasks run --multiple` task-selection flow. V1 should not invent a second selector. |
| F3 | Isolated worktree execution | Critical | In parallel mode, create a separate git worktree per selected task and run each agent inside its assigned worktree. |
| F4 | Source worktree protection | Critical | Keep the original repository worktree untouched during child task execution. |
| F5 | Child run status aggregation | High | Report each task's final state: completed, failed, blocked, cancelled, or needs review. |
| F6 | Retained worktrees | High | Keep all created worktrees after execution so the user or agent can inspect, reconcile, and clean them up intentionally. |
| F7 | Agent cleanup/fan-in prompt | High | Generate a provider-neutral prompt for the user's chosen agent to inspect retained worktrees and prepare the next step, such as a GitHub PR or GitLab MR. |
| F8 | Successful child completion marking | High | Mark a task finished when its child run finishes successfully in its isolated worktree. |

### Integration with Existing Features

| Integration Point | How |
|---|---|
| `config.toml` | Adds the `multiple` execution mode setting with `sequential` default and `parallel` opt-in. |
| `compozy tasks run --multiple` | Reuses the existing multiple task selection flow. |
| Daemon-backed execution | Reuses run supervision, attach/detach behavior, and status reporting where compatible. |
| `.compozy/tasks/<slug>` artifacts | Uses existing task files as the selection source while avoiding unsafe concurrent writes in V1. |
| Executor concurrency patterns | Reuses existing `WaitGroup`, semaphore, and shutdown patterns where they fit the new isolation model. |
| Workspace snapshotting | Reuses git state detection concepts to verify worktree changes and identify no-op results. |

## KPIs

| KPI | Target | How to Measure |
|---|---:|---|
| Source worktree protection | 100% untouched during child execution | Compare original worktree status before, during, and after parallel runs. |
| Cross-task clobber incidents | 0 incidents in supported V1 scenarios | Run parallel task fixtures and verify each task only modifies its own worktree. |
| Parallel mode opt-in clarity | 100% documented config examples use `multiple = "sequential"` or `multiple = "parallel"` | Audit docs, help text, and generated config examples. |
| Status visibility | 100% of child runs report terminal state | Verify every selected task produces a final status event or summary. |
| Worktree retention | 100% of created worktrees retained after execution | Compare created worktree count with retained worktree count after a completed parallel run. |
| Completion marking accuracy | 100% of successful child runs mark their task finished | Verify task state changes only after successful child-run completion. |

## Feature Assessment

| Criteria | Question | Score |
|---|---|---|
| **Impact** | How much more valuable does this make the product? | Strong |
| **Reach** | What % of users would this affect? | Strong |
| **Frequency** | How often would users encounter this value? | Maybe |
| **Differentiation** | Does this set us apart or just match competitors? | Strong |
| **Defensibility** | Is this easy to copy or does it compound over time? | Maybe |
| **Feasibility** | Can we actually build this? | Strong |

Leverage type: Strategic Bet

## Council Insights

- **Recommended approach:** Build an opt-in parallel mode for the existing `--multiple` workflow. Keep sequential execution as the default.
- **Key trade-offs:**
  - Isolation-only would ship faster but may feel like plumbing.
  - Full orchestration would require dependency inference, merge policy, cleanup rules, and shared-state reconciliation.
  - Retained worktrees plus an agent cleanup/fan-in prompt gives users a product-shaped next step without forcing provider-specific PR/MR automation into V1.
- **Risks identified:**
  - Shared `.compozy/tasks/<slug>` state can race if child runs write concurrently.
  - Automatic merge behavior could damage trust if it hides conflicts.
  - Keeping all worktrees can grow disk usage unless users get a clear follow-up prompt.
- **Stretch goal (V2+):** Add dependency-aware batching or a parallel task workbench after V1 proves opt-in parallel execution.

## Out of Scope (V1)

- **Automatic dependency inference** — V1 should only run tasks the user explicitly selects through `--multiple`.
- **Automatic conflict resolution** — Conflict handling must stay visible and user-controlled.
- **Unattended merge to the main worktree** — Results should remain in retained worktrees until the user or chosen agent reconciles them.
- **Built-in GitHub/GitLab PR creation** — V1 should generate an agent prompt, not implement provider-specific PR/MR automation.
- **Automatic worktree cleanup** — V1 keeps all worktrees so users can inspect and reconcile them safely.
- **General-purpose scheduler** — V1 should not become a queueing platform for arbitrary commands.

## Architecture Decision Records

- [ADR-001: Bound V1 to Configurable Parallel Worktree Mode With Agent Handoff](adrs/adr-001.md) — V1 adds opt-in parallel mode for existing `--multiple` task selection, retains all worktrees, and uses an agent prompt for cleanup/fan-in.

## Open Questions

None at this stage. Current resolved decisions:

- Use `multiple = "sequential"` as the default config value.
- Use `multiple = "parallel"` as the opt-in parallel worktree mode.
- Reuse the existing `tasks run --multiple` selection flow.
- Keep all created worktrees after execution.
- Do not require a built-in fan-in artifact; generate an agent cleanup/fan-in prompt instead.
- Mark a task finished when its child run finishes successfully.

## Cost Estimate

| Type | Volume | Estimated Cost |
|---|---|---|
| Local git worktrees | One per selected task | No direct infrastructure cost |
| AI agent usage | N concurrent agents | Cost scales with the user's configured IDE/model provider |
| Disk usage | One checkout per selected task | Local disk cost only; retained worktrees require user-managed cleanup |
