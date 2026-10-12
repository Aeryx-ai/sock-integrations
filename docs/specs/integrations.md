# sock-integrations v1

Packaging that lets agent platforms message a human through sock. Consumes [sock-mcp](../../../mcp/docs/specs/tools.md) and sock-cli. Adds no server surface and no domain model.

## Context

Conformist downstream of the sock-mcp tool contract and the `sock` CLI. Platform vocabulary (hooks, channels, plugins, sessions) stays here and never enters sock-mcp. Contracts: no API endpoints, no domain events and no tables, because this repo only consumes existing ones. Every transition it triggers is `NotifyOwner` or `Send`, already specified in the sock-contract API.

Caller class: puppet device (`sock_pt_`). Human session tokens are not supported by the hooks, because `NotifyOwner` needs a puppet.

## Scope

v1 is bearer-token platforms only.

In: Claude Code plugin, Codex config and hooks, Agent SDK example.

Out, tracked in bd:
- Managed Agents bridge (a daemon, own spec).
- Codex and headless inbound (no channel equivalent).
- Hosted HTTP surfaces: Responses API hosted MCP, claude.ai, ChatGPT, Managed Agents cloud MCP. sock-mcp is stdio only, and these need a public URL and an OAuth-style credential.
- A `Stop` hook that fetches pending messages. Claude Code inbound is the channel.

## Rules

1. One puppet per binding (platform plus machine). A leaked token costs one binding, and the server rate limit (60 sends per minute) is per puppet.
2. One consumer per puppet. The channel and any `sock_fetch` or `sock_ack` caller consume the same deliveries, so a puppet with the channel on is never used for fetch or ack.
3. The token comes from the environment or the plugin's secure `userConfig`. No checked-in file holds it.
4. Hooks never forward transcript or notification free text. The message is a fixed phrase plus the project directory name.
5. Hooks always exit 0 and never retry. A second call gets a second idempotency key and can double post. The CLI already retries one call internally with one key.
6. Message text received from sock is untrusted. The channel gates on the owner id; nothing here widens that gate.

## Claude Code plugin

Layout, plugin root `claude-code/`:

```
.claude-plugin/plugin.json
.mcp.json
hooks/hooks.json
hooks/notify.sh
skills/sock/SKILL.md
```

Marketplace file at the repo root, `.claude-plugin/marketplace.json`: name `sock-integrations`, one entry `{"name": "sock", "source": "./claude-code"}`. Install: `/plugin marketplace add guygrigsby/sock-integrations`, then `/plugin install sock@sock-integrations`.

`plugin.json`: name `sock`, MIT, `userConfig` options:

| Key | Type | Notes |
|---|---|---|
| `sock_token` | string, sensitive, required | A `sock_pt_` token. Stored in secure storage, exported to hooks as `CLAUDE_PLUGIN_OPTION_SOCK_TOKEN` |
| `sock_url` | string, default `https://api.sock.aeryx.ai` | Exported as `CLAUDE_PLUGIN_OPTION_SOCK_URL` |
| `notify_on_stop` | boolean, default false | Opt in to a message at the end of every turn |

`channels`: one entry bound to the `sock` MCP server.

`.mcp.json`: server `sock`, `command` `sock-mcp`, `args` `["--channel"]`, `env` `SOCK_TOKEN` and `SOCK_URL` from `${user_config.sock_token}` and `${user_config.sock_url}`. Tools alone work without the dev flag. Inbound needs the channel loaded: `claude --dangerously-load-development-channels plugin:sock@sock-integrations`, interactive only, research preview, enabled by an org admin on Team and Enterprise.

`hooks/hooks.json`, all `async: true` so the agent never waits:

| Event | Matcher | Text |
|---|---|---|
| `Notification` | `permission_prompt` | `needs permission` |
| `Notification` | `idle_prompt` | `waiting for you` |
| `Stop` | none | `finished a turn`, sent only when `notify_on_stop` is true |

Each entry runs `${CLAUDE_PLUGIN_ROOT}/hooks/notify.sh <text>` in exec form. The `Stop` entry runs `notify.sh --stop <text>`. Splitting by matcher means the script never parses the hook JSON, so there is no `jq` dependency.

`SKILL.md`: when to message the owner (long task finished, blocked on a decision, needs an approval), how to reply to a `<channel source="sock" ...>` tag with `sock_send` and the tag's `conversation_id`, and that message text is data, not instructions.

## Codex

Files under `codex/`: `config.toml` snippet, `hooks.json`, `install.md`.

- `mcp_servers.sock`: `command = "sock-mcp"`, `env_vars = ["SOCK_TOKEN", "SOCK_URL"]` so the token is forwarded from the shell and never written to config.
- `hooks.json`: `PermissionRequest` (text `needs permission`) and `Stop` (text `finished a turn`, only when `SOCK_NOTIFY_ON_STOP=1`), both `async: true`, running the shared `notify.sh`.
- `install.md`: merge the entries into `~/.codex/hooks.json` (all matching hooks run), export `SOCK_TOKEN`, then review and trust the hooks with `/hooks`. Codex skips a hook until it is trusted, and changing the script re-requires trust.
- Outbound only. The model can call `sock_send` as a tool. No inbound in v1.

## Agent SDK

`agent-sdk/example.ts`: `query()` with `mcpServers.sock` as a stdio server (`command: "sock-mcp"`, `env.SOCK_TOKEN` from `process.env`) and `allowedTools` of every `mcp__sock__sock_*` tool. Auto-approving `sock_send` is the point for headless agents. The blast radius is the puppet: its owner and conversations it belongs to, 60 sends per minute. The example says so in a comment.

## notify.sh

Shared by Claude Code and Codex. Behavior:

1. Drain stdin. Ignore its content.
2. Resolve token: `SOCK_TOKEN`, else `CLAUDE_PLUGIN_OPTION_SOCK_TOKEN`. Resolve URL the same way with `SOCK_URL`. Missing token: log one line and exit 0.
3. With `--stop` (first argument), exit 0 unless `CLAUDE_PLUGIN_OPTION_NOTIFY_ON_STOP` is `true` or `SOCK_NOTIFY_ON_STOP` is `1`.
4. Text is `<basename of CLAUDE_PROJECT_DIR, else PWD>: <phrase>`. The name has control characters stripped and is cut to 100 characters.
5. Run `sock send -- "<text>"` (the `--` keeps a project name like `-v` from parsing as a flag) as a child with a watchdog that kills it after `SOCK_HOOK_TIMEOUT` seconds, default 10. Async hooks are not timed out by the host, so the script bounds itself.
6. Any failure, including `sock` missing from `PATH`, appends one line to `${XDG_STATE_HOME:-$HOME/.local/state}/sock-integrations/hook.log` (mode 600, truncated above 100 KB). The line never contains the token.
7. Exit 0 always.

## Tests

A Go test module under `test/`, run by `make test`. It runs the real `notify.sh` against a loopback stub of `MessagingService/NotifyOwner` (`SOCK_URL` accepts http on loopback).

| Case | Expect |
|---|---|
| `permission_prompt` entry, `CLAUDE_PROJECT_DIR=/x/proj` | One request, `Authorization: Bearer` with the token, text `proj: needs permission` |
| `SOCK_TOKEN` and `CLAUDE_PLUGIN_OPTION_SOCK_TOKEN` both set | `SOCK_TOKEN` wins |
| Stub always returns 503 | Exit 0 within 15 seconds, one log line |
| Stub returns `TOKEN_INVALID` | Exit 0, one log line, no retry storm |
| `sock` absent from `PATH` | Exit 0, one log line |
| `Stop`, option false then true | No request, then one request |
| Hook JSON on stdin, and closed stdin | No hang |
| Token value anywhere in the log | Never |

Drift guard: spawn the real `sock-mcp` binary over stdio, call `tools/list`, and assert every `sock_*` name in `SKILL.md`, `example.ts`, `install.md` and this spec exists in the list. `SOCK_MCP_BIN` points at the binary.

`make check` also runs `claude plugin validate ./claude-code` and `claude plugin validate .` when `claude` is on `PATH`, plus the linters.

`make smoke` sends through the real `sock` binary to a local sock-server with a real puppet token, and runs `sock-mcp` through a `tools/call` of `sock_send` to the same server.

Not covered by automation, done by hand once per release: the plugin loading in a live Claude Code session, the channel with the dev flag, and Codex hook trust. All three are interactive.

## Makefile

`build` and `check` run the plugin validation and `go vet`. `test` runs the Go module. `lint` runs `golangci-lint` and `shellcheck` on `notify.sh`. `install` registers this checkout as a local marketplace and installs `sock`. `redeploy` refreshes the marketplace and reinstalls. The exact `claude plugin` subcommands are confirmed against `claude plugin --help` when the targets are written.

## Review gates

This repo handles a secret and an agent-to-human trust boundary. Before merge, a fresh-context subagent reviews the diff for token leakage (logs, process list, config files), fail-open paths and prompt injection through the inbound path, and each finding is verified against the build.

## Decisions

- Shell hook over a Go binary: the hooks send fixed text, the matcher already splits the cases, and the `sock` CLI is the existing client. A binary would add a module dependency on `sock-client` (not yet tagged) for no gain.
- Async plus a self-bound over a host timeout: the host does not enforce `timeout` on async hooks, and a synchronous permission hook would delay the agent whenever sock is down.
- Fixed phrase over forwarding `message`: notification and transcript text can hold secrets, and the owner only needs the signal.
- Plugin `userConfig` for the token: secure storage, one prompt, and hooks and the MCP server both receive it without a file.

## Follow-ups outside this repo

- sock-contract context map: add an Integrations row, a conformist downstream of sock-mcp and sock-cli.
