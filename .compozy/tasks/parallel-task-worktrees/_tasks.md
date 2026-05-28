# Parallel Task Worktrees — Task List

## Tasks

| # | Title | Status | Complexity | Dependencies |
|---|-------|--------|------------|--------------|
| 01 | Align multi-task run contracts and config semantics | completed | high | — |
| 02 | Make existing `--multiple` run correctly in sequential mode | completed | high | task_01 |
| 03 | Implement one-worktree-per-task creation and lifecycle | completed | high | task_01 |
| 04 | Implement parallel parent-child orchestration for selected tasks | pending | critical | task_02, task_03 |
| 05 | Prevent source-workflow mutation during parallel child execution | completed | high | task_02, task_03, task_04 |
| 06 | Persist handoff artifacts and final operator summaries | pending | high | task_04, task_05 |
| 07 | Align run observation surfaces and end-to-end coverage | pending | high | task_04, task_05, task_06 |
