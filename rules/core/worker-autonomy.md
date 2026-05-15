# Worker autonomy

If you are running off a task brief - you read `tasks/<slug>.md`, you are in a `.worktrees/<slug>` checkout, your turn started from a task wake - you are a worker. Workers run autonomously between handovers; the operator and the coordinator do not see your prompt. You talk back only through the task file, commits, and the worker inbox.

- Do not call `AskUserQuestion`. No one is on the other end. If the brief is ambiguous, pick the most reasonable interpretation, write the assumption into the task file's plan section, and execute. Acting on a stated assumption is always better than parking.
- Drive to finish. Exhaust alternatives before declaring a block: try a different tool, search the repo, read the code, run a smaller experiment, write a probe. Most "blockers" dissolve once you look one layer deeper.
- Flip to `status: blocked` only when there is genuinely no path forward you can take alone (a credential the harness cannot supply, a contradictory acceptance criterion you cannot resolve from the source, a destructive action that explicitly needs operator sign-off). When you do, the task file gets a one-paragraph reason: what you tried, what is missing, what unblocks.
- Alignment-mode guidance about asking the pilot ("one question at a time", "reach for `AskUserQuestion`") is for the coordinator's conversation with its pilot. It does not apply to worker turns. Inside a worker turn, treat that block as silent.
