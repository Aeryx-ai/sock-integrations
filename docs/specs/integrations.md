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
- Won't do: a `Stop` hook that fetches pending messages. The channel is the inbound path.

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

Marketplace file at the repo root, `.claude-plugin/marketplace.json`: name `sock-integrations`, a `description`, an `owner`, and one plugin entry `{"name": "sock", "source": "./claude-code", "description": ...}`. Install: `/plugin marketplace add guygrigsby/sock-integrations`, then `/plugin install sock@sock-integrations`.

`plugin.json`: name `sock`, `version`, `author`, `description`, MIT, plus whatever `claude plugin validate --strict` requires. `make build` runs the validator, so it is the authority on required manifest fields. Every `userConfig` option carries a `title` and a `description`. Options:

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
- `hooks.json`: `PermissionRequest` (text `needs permission`) and `Stop` (text `finished a turn`, only when `SOCK_NOTIFY_ON_STOP=1`), both `async: true`, running the shared `notify.sh`. Codex handlers take no `args` field, so each `command` carries its arguments inline. The file holds the placeholder `/ABSOLUTE/PATH/TO/sock-integrations`, which install substitutes with the checkout path (`install.md` gives an awk recipe that treats `#`, `&` and `\` literally). The checkout path must contain no spaces. Whether Codex parses `command` through a shell or as exec form is unconfirmed in its docs, so the inline quoting is verified by hand at release.
- `install.md`: merge the entries into `~/.codex/hooks.json` (all matching hooks run), export `SOCK_TOKEN` only in a Codex launcher or wrapper script and not a global rc file (Claude Code's Bash tool inherits the environment, so a model can read it), then review and trust the hooks with `/hooks`. Codex skips a hook until it is trusted, and changing the script re-requires trust.
- Outbound only. The model can call `sock_send` as a tool. No inbound in v1.

## Agent SDK

`agent-sdk/example.ts`: `query()` with `mcpServers.sock` as a stdio server (`command: "sock-mcp"`, `env.SOCK_TOKEN` from `process.env`) and `allowedTools` of `mcp__sock__sock_whoami`, `mcp__sock__sock_list_conversations` and `mcp__sock__sock_send`. Auto-approving `sock_send` is the point for headless agents. The blast radius is the puppet: its owner and conversations it belongs to, 60 sends per minute. `sock_fetch` and `sock_ack` are deliberately not auto-approved, because fetch returns messages from every sender in every conversation the puppet is in. The example says both in comments.

## notify.sh

Shared by Claude Code and Codex. Behavior:

1. Drain stdin, bounded to 2 seconds so a held-open stdin cannot hang the hook. Ignore its content.
2. Resolve token: `CLAUDE_PLUGIN_OPTION_SOCK_TOKEN` (set only under the plugin), else `SOCK_TOKEN`. The plugin token wins because `.mcp.json` always gives sock-mcp the plugin token, and an ambient `SOCK_TOKEN` exported for Codex must not make Claude Code hooks post as the Codex puppet. The URL comes from the same source as the token: `CLAUDE_PLUGIN_OPTION_SOCK_URL` for the plugin token, `SOCK_URL` for the env token, never the other. A URL from the matching source is passed as `sock --url`, so a config file cannot override it; with none, `SOCK_URL` is unset for the child, no `--url` is passed and the CLI default or config applies. Missing token: log one line and exit 0.
3. With `--stop` (first argument), exit 0 unless `CLAUDE_PLUGIN_OPTION_NOTIFY_ON_STOP` is `true` or `SOCK_NOTIFY_ON_STOP` is `1`.
4. Text is `<name>: <phrase>`, where `<name>` is the basename of `CLAUDE_PROJECT_DIR`, else `PWD`. Under `LC_ALL=C` the name keeps only `[A-Za-z0-9._ -]`, is cut to 100 characters, and becomes `project` when nothing remains.
5. Run `sock send -- "<text>"` (the `--` keeps a project name like `-v` from parsing as a flag) as a child with a watchdog that kills it after `SOCK_HOOK_TIMEOUT` seconds, default 10; a value that is not a positive integer falls back to 10. Async hooks are not timed out by the host, so the script bounds itself. The watchdog is reaped without a job notice on stderr.
6. Any failure, including `sock` missing from `PATH`, appends one line to `${XDG_STATE_HOME:-$HOME/.local/state}/sock-integrations/hook.log` (mode 600, truncated above 100 KB). A failed send logs `sock send exit <code>:` and the child's stderr, with the token redacted from the whole captured stderr first and the result then cut to 300 bytes, so a token straddling the cut cannot leak a prefix. Every control character is stripped from the line, so tool output cannot drive a terminal that reads the log. The line never contains the token.
7. Exit 0 always.

## Tests

A Go test module under `test/`, run by `make test`. It runs the real `notify.sh` against a loopback stub of `MessagingService/NotifyOwner` (`SOCK_URL` accepts http on loopback).

| Case | Expect |
|---|---|
| `permission_prompt` entry, `CLAUDE_PROJECT_DIR=/x/proj` | One request, `Authorization: Bearer` with the token, text `proj: needs permission` |
| `SOCK_TOKEN` and `CLAUDE_PLUGIN_OPTION_SOCK_TOKEN` both set | The plugin token wins |
| Plugin token with a different ambient `SOCK_URL` | The plugin token reaches only the plugin URL |
| Env token with a different plugin URL | The env token reaches only `SOCK_URL` |
| Plugin token, no plugin URL | Ambient `SOCK_URL` ignored, CLI config applies |
| Stub always returns 503 | Exit 0 within the watchdog timeout plus slack, one log line naming a nonzero `sock send` exit |
| Stub holds the call open | Watchdog kills `sock`, exit 0, one log line |
| Stub returns `TOKEN_INVALID` | Exit 0, one log line, no retry storm |
| `sock` absent from `PATH` | Exit 0, one log line |
| `Stop`, both gates unset, option false, then true | No request, no request, then one request |
| Hook JSON on stdin, and closed stdin | No hang |
| Token value anywhere in the log, including straddling the 300 byte cut | Never |
| Fake `sock` prints escape sequences to stderr | No byte below 0x20 in the log except the trailing newline |
| Project name with multibyte, bidi override or C1 characters, under `LANG=C` | Delivered, with only allowlisted characters, or `project` |
| Normal run | Nothing on stderr |
| Invalid `SOCK_HOOK_TIMEOUT` | Falls back to 10, request still sent |
| Unwritable state dir | Exit 0 within the timeout plus slack |

Drift guard: spawn the real `sock-mcp` binary over stdio, call `tools/list`, assert the list is exactly the pinned tool set (a new or missing tool fails), and assert every `sock_*` name in `SKILL.md`, `example.ts`, `install.md` and this spec exists in the list. `SOCK_MCP_BIN` points at the binary. One hand-rolled stdio client serves the drift guard and the send check, and kills the child after a bounded time so a silent server fails instead of hanging.

Send check (`TestMCPSend`): spawn the real `sock-mcp`, hold stdin open, call `sock_send`, and fail on a JSON-RPC error or `isError:true`. Against the stub it asserts one request with the right bearer token and text. With `SOCK_SMOKE=1` it uses `SOCK_URL` and `SOCK_TOKEN` from the environment and asserts only success. Another test proves it fails against fake servers that return each bad response.

`make check` also runs `claude plugin validate ./claude-code --strict` and `claude plugin validate . --strict` when `claude` is on `PATH`, plus the linters.

`make smoke` sends through the real `sock` binary to a local sock-server with a real puppet token, runs `notify.sh` against it (failing if the hook log grew), and runs `TestMCPSend` with `SOCK_SMOKE=1` against the same server.

Not covered by automation, done by hand once per release: the plugin loading in a live Claude Code session, the channel with the dev flag, Codex hook trust, and whether Codex runs a hook `command` through a shell or as exec form (the inline quoting in `codex/hooks.json`). All are interactive.

## Makefile

- `deps` builds the sibling `../cli` and `../mcp` binaries. `build`, `test` and `smoke` depend on it.
- `build` (default) runs `claude plugin validate --strict` on `./claude-code` and on the repo root, only when `command -v claude` finds it, and otherwise prints `claude not found, skipping plugin validation`. Then `go vet`.
- `test` runs the Go module under `-race`.
- `lint` runs `golangci-lint` and `shellcheck` on `notify.sh` and `smoke.sh`.
- `check` is `build`, `lint`, `test`.
- `install-bins` runs `make install` in `../cli` and `../mcp`. The binaries land in `$(go env GOPATH)/bin`, which must be on `PATH`, including the `PATH` of the Claude Code process. `install` depends on it, then registers this checkout as a local marketplace and installs `sock`.
- `redeploy` refreshes the marketplace and reinstalls.
- `smoke` runs `scripts/smoke.sh`.

The `claude plugin` subcommands were confirmed against `claude plugin --help`.

## Review gates

This repo handles a secret and an agent-to-human trust boundary. Before merge, a fresh-context subagent reviews the diff for token leakage (logs, process list, config files), fail-open paths and prompt injection through the inbound path, and each finding is verified against the build.

## Decisions

- Shell hook over a Go binary: the hooks send fixed text, the matcher already splits the cases, and the `sock` CLI is the existing client. A binary would add a module dependency on `sock-client` (not yet tagged) for no gain.
- Async plus a self-bound over a host timeout: the host does not enforce `timeout` on async hooks, and a synchronous permission hook would delay the agent whenever sock is down.
- Fixed phrase over forwarding `message`: notification and transcript text can hold secrets, and the owner only needs the signal.
- Plugin `userConfig` for the token: secure storage, one prompt, and hooks and the MCP server both receive it without a file.

## Follow-ups outside this repo

- sock-contract context map: add an Integrations row, a conformist downstream of sock-mcp and sock-cli.
