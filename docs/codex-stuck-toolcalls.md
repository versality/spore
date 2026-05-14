# codex stuck tool calls

## Problem

Codex workers can leave a turn with a `tool_use` envelope opened but no
matching `tool_result` before Stop fires. The next turn starts with the
pending call still in the rollout transcript, which either blocks new
tool dispatch entirely or produces duplicate / conflicting effects when
codex re-invokes around it.

Manifests as wedged sessions. Operator visibility into the wedge has
been low because the transcript parser only cared about tokens.

## Design

Three gates, one detector, one ledger. The detector lives at the
transcript layer; everything else is a thin consumer.

### Detector

`internal/transcript/codex_toolcalls.go`. `LastUnfinalizedToolCalls`
walks the codex rollout JSONL and pairs `response_item.payload` events
by `call_id` using a suffix rule: `*_call` opens, `*_call_output`
closes. Whatever remains at EOF is returned, oldest-first. The suffix
rule generalises beyond `function_call` / `custom_tool_call` to new
envelope kinds future codex versions may introduce (`mcp_call`,
`local_shell_call`, etc) without code changes.

The function is stateless and order-tolerant. Interleaved tools within
a turn pair up by `call_id` regardless of how they're laid out in the
transcript.

### Gate 1: Stop hook

`internal/hooks/codex/stop_stuck.go` is invoked from the codex Stop
adapter after the context monitor and before the worker inbox drain.
On any stuck call:

1. Appends one row per stuck call to
   `$SPORE_COORDINATOR_STATE_DIR/codex-stuck-toolcalls.jsonl`.
2. Exits 2 with `CODEX STUCK TOOLCALL: N unfinalized tool call(s): ...`
   so codex gets one more shot at emitting the missing outputs before
   the session actually wraps.

Same coordinator-session gate as the context monitor (`inboxUnderRoot`,
`Driver == "codex"`). Worker / ad-hoc sessions skip until telemetry
warrants widening.

### Gate 2: PreToolUse hook

`spore hooks codex pre-tool-use` wired in `.codex/hooks.json` re-parses
the transcript on every PreToolUse fire. If any prior call is still
unfinalized, exits 2 with `codex-stuck-toolcall-prior: refusing <tool>:
N prior tool call(s) unfinalized: ...`. This is the mechanical block:
even if the Stop reminder missed (e.g. codex resumed past the wrap), no
new dispatch goes through while prior state is unresolved.

The PreToolUse gate does not consult the ledger. Live transcript
re-parse is the source of truth; the ledger is observability only.

### Gate 3: SessionStart additionalContext (S5, pending)

On session resume, additionalContext gets a "carry-over tool call(s)
from prior turn" block describing each stuck call. This relies on the
agent reading + acknowledging; the PreToolUse gate is the
belt-and-braces backstop.

Synthetic `function_call_output` injection into the rollout JSONL was
considered and rejected: codex owns the rollout file, injecting risks
replay confusion if codex caches offsets or checksums, demands exact
output schema knowledge per tool kind, and races codex itself during
session boot.

## Ledger

Path: `$SPORE_COORDINATOR_STATE_DIR/codex-stuck-toolcalls.jsonl`.

One JSON row per Stop-detected stuck call:

```
{"ts":"2026-05-14T09:00:00Z","event":"stuck-toolcall","session_id":"...",
 "call_id":"call_X","tool_name":"exec_command","kind":"function_call",
 "transcript_path":"/.../rollout-....jsonl","line_num":12}
```

Append-only; no consumed-marker, no TTL. Rotation is a future concern.
The ledger is for humans + boot probes to track frequency; control flow
re-reads the transcript.

## Fixtures

`internal/transcript/testdata/codex-stuck-*.jsonl` are synthesised from
real codex rollout shapes captured on the development host (codex
0.128.0). The four envelope kinds observed there:

- `function_call` / `function_call_output`
- `custom_tool_call` / `custom_tool_call_output`

When real captures of stuck states become available (after codex driver
unpause), swap the fixtures in place; the parser logic should not need
to change because of the suffix-rule generalisation.

## Failure modes considered

- Driver-universal? No, codex-only. Claude Code Stop already pairs
  tool_use / tool_result via its native protocol; we don't replicate.
- Survives just switch? Yes. Detector is pure; hooks are Go
  subcommands; no daemon state.
- Stale-vs-idle? Does not apply. Live decisions re-parse the
  transcript EOF state on each call. The ledger is append-only history.
- Simplest layer? Transcript layer. Hooks are thin consumers.
- Prior art? `codexContextMonitor` (gate + ledger + exit-2 shape).
  `inboxwatcher_state.go` pattern considered and rejected: the
  transcript itself is the source of truth, no persistent markers
  needed.
