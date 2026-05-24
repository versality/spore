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
