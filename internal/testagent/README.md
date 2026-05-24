# testagent

`testagent` provides deterministic fake agent processes for lifecycle tests.
It models the command-line contract Spore depends on: process argv, cwd, env,
readiness, progress, exit, and JSONL event logs.

Configure the fake agent through environment variables:

- `SPORE_FAKE_AGENT_MODE`
- `SPORE_FAKE_AGENT_EVENT_LOG`
- `SPORE_FAKE_AGENT_READY_FILE`
- `SPORE_FAKE_AGENT_EXIT_FILE`
- `SPORE_FAKE_AGENT_TRANSCRIPT`
- `SPORE_FAKE_AGENT_TURN_LIMIT`

Use small unit fakes for pure logic. Use this package when a test must prove
real process behavior through PATH, tmux, hooks, inboxes, transcripts, or task
completion.

## Use this harness when

- A test must prove that `agent: claude` starts a command named `claude`.
- A test must prove that `agent: codex` starts a command named `codex`.
- A test needs real process state through `tmux`, cwd, argv, env, readiness, or
  crash timing.
- A test needs rendered runtime hooks to be read and executed.
- A test needs inbox messages, wake behavior, transcripts, evidence, commits, or
  coordinator role handling.

## Use smaller fakes when

- A test only classifies command readiness. Use fake `LookPath`.
- A test only checks CLI warnings or missing-bin errors. Use a small fake PATH.
- A test only validates parser or renderer output. Read the file directly.

Keep unit tests cheap. Reach for fake agents only when the behavior depends on
the command-line process contract.

## Plan coverage

This harness lands the mock agent plan in these commits:

- `testagent: add shared fake agent protocol`
- `testagent: build fake claude and codex binaries in tests`
- `testagent: record worker launch contract`
- `testagent: simulate idle progress and long-running states`
- `testagent: simulate crash and dead-pane scenarios`
- `testagent: execute Codex runtime hooks`
- `testagent: execute Claude runtime hooks`
- `testagent: model inbox and wake behavior`
- `testagent: write transcript fixtures`
- `testagent: support completion and evidence fixtures`
- `testagent: add coordinator-mode fake agent`
- `testagent: document fake agent usage`
