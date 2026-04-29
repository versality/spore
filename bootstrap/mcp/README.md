# MCP server templates

Drop-in MCP (Model Context Protocol) server config for the four
servers spore expects downstream agents to have available: Reddit,
Kagi, Hacker News, GitHub. Each server is invoked through `npx`; no
binaries are bundled.

`config.json.template` carries `${ENV_VAR}` placeholders only. Wire
your own credentials before installing.

## Install path

The exact target depends on which agent runtime the operator runs:

- Claude Code (CLI): merge entries into `~/.claude.json` under
  `mcpServers`, or register each server with
  `claude mcp add --transport stdio <name> -- <command> [args...]`.
- mcp-hub (or any MCP aggregator): write the rendered file to
  `~/.config/mcp-hub/config.json`.
- Other runtimes: consult that runtime's MCP config docs; the
  template format is the standard `mcpServers` shape.

## Environment variables per server

| Server     | Required envvars                                           | Optional envvars                                                                                                                            | Auth posture                                                              |
| ---------- | ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| reddit     | none                                                       | `REDDIT_AUTH_MODE` (default `auto`), `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET`, `REDDIT_USERNAME`, `REDDIT_PASSWORD`, `REDDIT_USER_AGENT`  | Anonymous read works zero-setup. Higher rate limits + writes need a Reddit "script" app at https://www.reddit.com/prefs/apps. |
| kagi       | `KAGI_SESSION_TOKEN`                                       | none                                                                                                                                        | Session token from a logged-in Kagi browser; see czottmann/kagi-ken-mcp.  |
| hackernews | none                                                       | none                                                                                                                                        | Public Firebase API. No account, no token.                                |
| github     | `GITHUB_PERSONAL_ACCESS_TOKEN`                             | none                                                                                                                                        | Fine-grained PAT recommended. Scope to the repos and operations you need. |

## Rendering the template

The template uses literal `${VAR}` placeholders for every secret
slot. Three common ways to substitute:

```
envsubst < bootstrap/mcp/config.json.template > ~/.config/mcp-hub/config.json
```

```
sed -e "s|\${REDDIT_CLIENT_ID}|$REDDIT_CLIENT_ID|g" \
    -e "s|\${KAGI_SESSION_TOKEN}|$KAGI_SESSION_TOKEN|g" \
    bootstrap/mcp/config.json.template > rendered.json
```

```
jq --arg gh "$GITHUB_PERSONAL_ACCESS_TOKEN" \
   '.mcpServers.github.env.GITHUB_PERSONAL_ACCESS_TOKEN = $gh' \
   bootstrap/mcp/config.json.template > rendered.json
```

Validate the rendered file with `jq . rendered.json` before
installing; an empty placeholder will still parse but the server
will refuse to start.

## Bootstrap stage hook

The `creds-wired` stage of `spore bootstrap` checks that any secret
surface it finds is documented in `CLAUDE.md`; it does not auto-wire
MCP servers. The manual checklist above is the contract: render
placeholders to real values, copy to the runtime's expected path,
restart the agent.

## Out of scope

- No real keys, tokens, or session bodies live in this directory.
- Server upstreams own their own rate-limit and auth policies; spore
  does not patch or wrap them.
