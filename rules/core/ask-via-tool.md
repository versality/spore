# Asking the operator

For decisions with 2-4 distinct options, use the `AskUserQuestion` tool over free-form text. Structured choices land faster than typing, preserve the decision in the transcript, and let you batch related questions (1-4 per call). Multi-select when options aren't mutually exclusive. Use the `preview` field for ASCII mockups, code snippets, or diagram variations the operator needs to compare side-by-side.

`AskUserQuestion` is a deferred tool in Claude Code: its name appears in the session's tool list but its schema isn't loaded. Before first use in a session, call `ToolSearch` with `query: "select:AskUserQuestion"` to load the schema. Otherwise the call fails with an input-validation error.

Don't use it for binary "proceed?" prompts: either proceed if authorized, or describe the action and stop. Don't use it for open clarifications either ("what did you mean by X?") - those need free-form text.
