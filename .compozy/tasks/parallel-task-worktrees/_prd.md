# PRD: Parallel Task Worktrees

## Overview

Parallel Task Worktrees lets a solo maintainer run selected PRD tasks in parallel without putting the main workspace at risk. The feature turns parallel execution into a review-first workflow: users keep their source workspace untouched, get isolated outputs for each task, and end with a copy-paste-ready prompt they can hand to their preferred LLM or coding agent to turn results into reviewable branches or pull-request work.

This feature matters because AI-assisted development has made it cheap to generate more work, but not easier to trust it. Users want more throughput only when they can still inspect, compare, and reconcile outputs with confidence. Compozy can win by making safe parallel task execution feel controlled, reviewable, and easy to hand off into the next review step.

## Goals

- Increase throughput for independent task batches without changing the product’s safe default behavior.
- Protect the user’s source workspace during every supported parallel run.
- Make the end of a parallel run feel actionable, not ambiguous, by giving users clear review-oriented next steps.
- Reuse the existing multi-task execution mental model so users do not need to learn a second workflow.
- Position V1 as a trust-building product capability for solo maintainers, with room to expand later if adoption is strong.

## User Stories

### Primary Persona: Solo Maintainer

- As a solo maintainer, I want to run several independent tasks at once so that I can reduce elapsed time on a larger initiative.
- As a solo maintainer, I want each task’s output kept separate so that I can review results without wondering which agent changed what.
- As a solo maintainer, I want my main workspace protected so that I can try parallel execution without risking the work already in front of me.
- As a solo maintainer, I want a clear end-of-run package so that I can turn finished work into reviewable branches or PR preparation without inventing the process myself.

### Secondary Persona: Cautious AI Adopter

- As a cautious AI user, I want parallel mode to stay opt-in so that I can choose it only when the task set is a good fit.
- As a cautious AI user, I want clear task outcomes and preserved outputs so that I can trust the workflow even when some tasks fail or need review.

### Secondary Persona: Small-Team Lead

- As a small-team lead, I want a predictable review-first parallel workflow so that I can reuse it later across more than one contributor without changing the product promise.

## Core Features

### 1. Opt-In Parallel Task Mode

A user can choose to execute selected tasks in parallel instead of sequentially within the existing `compozy tasks run --multiple` flow. Sequential execution remains the default behavior for that same flow. This keeps the safe baseline intact while unlocking faster execution when tasks are independent.

### 2. Isolated Task Lanes

In parallel mode, each selected task executes in its own isolated working lane, retained worktree, and separate folder so the user can inspect results independently. Users should be able to understand which output belongs to which task at a glance. This is the foundation for trust, traceability, and clean follow-up review.

### 3. Source Workspace Protection

The user’s main workspace remains untouched while parallel child tasks run. This is a non-negotiable requirement for V1 and the strongest trust signal in the feature.

### 4. Clear Per-Task Outcome Visibility

At the end of a run, the user can see the final outcome of each selected task in plain language. The product should make it obvious which tasks succeeded, which need attention, and which should not be integrated yet.

### 5. Copy-Paste Agent Handoff Prompt

A successful parallel run ends with a generated prompt that the user can copy and paste into their preferred LLM or coding agent. The prompt should summarize task outcomes, identify where each preserved output lives, and instruct the agent on the next review-oriented step, such as preparing reviewable branches or pull-request work. This makes the feature feel actionable at the moment of handoff instead of leaving the user to invent the workflow alone.

### 6. Preserved Outputs Until Intentional Cleanup

Outputs from the run remain available for inspection after execution completes. Users should never feel rushed to reconcile or clean up before they understand what each task produced.

### 7. Existing Workflow Reuse

The feature builds on the existing `compozy tasks run --multiple` entrypoint rather than introducing a second path for choosing tasks. This reduces product sprawl, keeps the experience familiar, and makes parallel mode an opt-in behavior of an existing workflow.

## User Experience

The ideal V1 experience is simple and calm.

A solo maintainer selects several tasks they believe can move independently. They choose parallel mode only when they want it. Before the run starts, the product makes the safety model obvious: the source workspace stays protected, each task gets its own lane, and outputs will be preserved for review.

During execution, the user can understand progress at the task level without needing to infer hidden state. The product should favor direct language over dense system jargon. A user should be able to tell which tasks are progressing, which are blocked, and which are finished without opening every output manually.

At the end of the run, the user receives a review-first summary plus a ready-to-copy prompt for their chosen LLM or coding agent. The experience should answer five things immediately: what finished, what failed, where each output lives, what the next review step is, and what exact prompt to paste into the next tool. The feature should feel like it hands the user a clean tray of work to inspect and a clear next action to take.

Accessibility and usability expectations:
- Status language should be understandable without specialized internal terminology.
- Color should not be the only signal for run or task state.
- Summaries should be easy to copy, scan, and reuse in follow-up review workflows.
- First-use guidance should explain when parallel mode is appropriate and when sequential mode is the better fit.

## High-Level Technical Constraints

- The feature must fit into the existing multi-task execution experience rather than create a separate task-selection product.
- The feature must reuse the existing `compozy tasks run --multiple` flow for task selection and mode switching in V1.
- The source workspace must remain unchanged during supported parallel runs.
- The end-of-run handoff must stay provider-neutral so users can continue with their preferred review workflow.
- The product must preserve a human review step rather than imply unattended integration or release.

## Non-Goals (Out of Scope)

- Automatic dependency inference between selected tasks.
- Automatic conflict resolution between task outputs.
- Unattended merge back into the source workspace.
- Built-in provider-specific PR or MR creation in V1.
- Automatic cleanup of preserved outputs in V1.
- A general-purpose scheduling or agent fleet management product.
- Reframing the feature around team-wide coordination before solo-maintainer value is proven.

## Phased Rollout Plan

### MVP (Phase 1)
- Opt-in parallel execution for the existing multi-task flow.
- Isolated task lanes for each selected task.
- Protected source workspace throughout execution.
- Clear per-task outcome visibility.
- Preserved outputs after execution.
- Review-oriented end-of-run package aimed at reviewable branches or PR preparation.

**Success criteria to proceed to Phase 2**
- The workflow consistently protects the source workspace in supported scenarios.
- Users can understand and act on the end-of-run package without needing ad hoc explanation.
- Parallel mode shows meaningful throughput gains on independent task batches.

### Phase 2
- Stronger guidance on when tasks are good candidates for parallel execution.
- Richer review summaries and clearer next-step packaging.
- Optional cleanup assistance after review is complete.
- Better support for repeated use by cautious but frequent solo maintainers.

**Success criteria to proceed to Phase 3**
- Users adopt parallel mode regularly for appropriate task sets.
- Review completion remains smooth enough that added throughput does not create downstream drag.
- Cleanup burden does not outweigh the perceived value of preserved outputs.

### Phase 3
- More advanced orchestration for broader multi-task workflows.
- Deeper review and downstream integration support where it materially improves user outcomes.
- Expansion beyond the solo-maintainer core use case if demand is proven.

**Long-term success criteria**
- Parallel execution becomes a trusted option for substantial task batches, not a niche experiment.
- Users associate Compozy with safe, structured AI parallelism rather than generic concurrency.

## Success Metrics

- **Source workspace protection:** 100% of supported parallel runs leave the source workspace unchanged.
- **Throughput improvement:** Median wall-clock time to complete three independent selected tasks improves by at least 35% compared with sequential execution.
- **Review readiness:** 100% of supported parallel runs produce a per-task terminal summary plus actionable review-oriented handoff information.
- **Output retention clarity:** 100% of created task outputs remain inspectable until the user intentionally cleans them up.
- **Early adoption:** At least 25% of eligible multi-task runs opt into parallel mode within the first 30 days of release.
- **User confidence:** At least 70% of pilot users report that the end-of-run package is clear enough to start review without extra coaching.

## Risks and Mitigations

- **Adoption risk:** Users may avoid the feature if parallel mode feels unsafe or too advanced.  
  **Mitigation:** Keep it opt-in, position it as trust-first, and make the safety model explicit from the start.

- **Review confusion risk:** Users may get faster output but still feel lost at the end of the run.  
  **Mitigation:** Treat the review package as a core product outcome, not an afterthought.

- **Cleanup burden risk:** Preserved outputs may feel heavy if users do not know what to keep or discard.  
  **Mitigation:** Make ownership and next steps obvious, and plan optional cleanup assistance for Phase 2.

- **Competitive parity risk:** Isolation alone may feel like a checkbox if similar tools already offer it.  
  **Mitigation:** Tie the feature to Compozy’s structured task workflow and provider-neutral review handoff.

- **Misuse risk:** Users may try parallel mode on overlapping or poorly scoped tasks and blame the feature for messy review.  
  **Mitigation:** Provide clear guidance on when parallel mode is appropriate and keep the solo-maintainer, independent-task use case at the center of launch messaging.

## Architecture Decision Records

- [ADR-001: Bound V1 to Configurable Parallel Worktree Mode With Agent Handoff](adrs/adr-001.md) — V1 stays opt-in, protects the source workspace, retains outputs, and avoids automatic merge behavior.
- [ADR-002: Position V1 as a Safe Review Lane for Parallel Task Worktrees](adrs/adr-002.md) — V1 is framed as a review-first throughput feature for solo maintainers rather than bare isolation or a larger release system.

## Open Questions

- What exact threshold should qualify as a successful throughput uplift for general availability if pilot usage differs from the current 35% target?
- Should launch messaging target only solo maintainers at first, or include small teams as an explicit secondary audience?
- What usage signal should trigger stronger in-product guidance about when sequential mode is the better choice?
