# sock-integrations v1

Packaging that lets agent platforms message a human through sock. Consumes [sock-mcp](../../../mcp/docs/specs/tools.md) and sock-cli. Adds no server surface and no domain model.

## Context

Conformist downstream of the sock-mcp tool contract and the `sock` CLI. Platform vocabulary (hooks, channels, plugins, sessions) stays here and never enters sock-mcp. Contracts: no API endpoints, no domain events and no tables, because this repo only consumes existing ones. Every transition it triggers is `NotifyOwner` or `Send`, already specified in the sock-contract API.

Caller class: puppet device (`sock_pt_`). A machine key (`sock_mk_`) is accepted in the same place and swapped for the project's puppet token before any call (ADR 10), so every call still runs as a puppet. Human session tokens are not supported by the hooks, because `NotifyOwner` needs a puppet.

## Scope

v1 is bearer-token platforms only.

In: Claude Code plugin, Codex config and hooks, Agent SDK example, the headless inbound runner (`runner/sock-runner`) and the Managed Agents bridge (`runner/sock-bridge`).

Out, tracked in bd:
- Managed Agents session creation, owner-brokered tool confirmations and webhook delivery: follow-up beads after the bridge below.
- Codex app-server live injection (`turn/steer`): a follow-up bead after the runner below.
- Hosted HTTP surfaces: Responses API hosted MCP, claude.ai, ChatGPT, Managed Agents cloud MCP. sock-mcp is stdio only, and these need a public URL and an OAuth-style credential.
- Won't do: a `Stop` hook that fetches pending messages. The channel is the inbound path.

## Rules

1. One puppet per binding (platform plus machine). A leaked token costs one binding, and the server rate limit (60 sends per minute) is per puppet.
2. One consumer per puppet. The channel and any `sock_fetch` or `sock_ack` caller consume the same deliveries, so a puppet with the channel on is never used for fetch or ack.
3. The token comes from the environment or the plugin's secure `userConfig`. No checked-in file holds it.
4. Hooks forward what the owner needs to act on and nothing more: the notification text for a permission prompt, and the trailing ask of the last assistant message (fenced code dropped, secret shapes redacted, 600 bytes) when a session is waiting. Nothing is sent when nothing is pending. Until 2026-10-13 this rule said hooks never forward free text; the owner reversed it ("Is something waiting on me, then tell me what I need to do in the sock message") and accepted that text the assistant echoed can leave the machine.
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

Marketplace file at the repo root, `.claude-plugin/marketplace.json`: name `sock-integrations`, a `description`, an `owner`, and one plugin entry `{"name": "sock", "source": "./claude-code", "description": ...}`. Install: `/plugin marketplace add aeryx-ai/sock-integrations`, then `/plugin install sock@sock-integrations`.

`plugin.json`: name `sock`, `version`, `author`, `description`, MIT, plus whatever `claude plugin validate --strict` requires. `make build` runs the validator, so it is the authority on required manifest fields. Every `userConfig` option carries a `title` and a `description`. Options:

| Key | Type | Notes |
|---|---|---|
| `sock_token` | string, sensitive, required | Titled "sock machine key or puppet token": a `sock_mk_` machine key (a puppet per project) or a `sock_pt_` token (one puppet). Stored in secure storage, exported to hooks as `CLAUDE_PLUGIN_OPTION_SOCK_TOKEN` |
| `sock_url` | string, default `https://api.sock.aeryx.ai` | Exported as `CLAUDE_PLUGIN_OPTION_SOCK_URL` |
| `notify_on_stop` | boolean, default false | Opt in to a message at the end of every turn |

`channels`: one entry bound to the `sock` MCP server.

`.mcp.json`: server `sock`, `command` `sock-mcp`, `args` `["--channel"]`, `env` `SOCK_TOKEN` and `SOCK_URL` from `${user_config.sock_token}` and `${user_config.sock_url}`. Tools alone work without the dev flag. Inbound needs the channel loaded: `claude --dangerously-load-development-channels plugin:sock@sock-integrations`, interactive only, research preview, enabled by an org admin on Team and Enterprise.

`hooks/hooks.json`, all `async: true` so the agent never waits:

| Event | Matcher | Text |
|---|---|---|
| `Notification` | `permission_prompt` | `--permission`: the notification's own text, which names the tool (`needs permission` when `jq` is absent) |
| `Notification` | `idle_prompt` | `--idle`: the ask recorded by the last `Stop` for this session, or the last assistant text in the transcript; nothing when nothing is pending; the same ask is never reposted |
| `Stop` | none | `--stop`: records the ask in `last_assistant_message` (the trailing question or decision, code blocks dropped, 600 bytes) for `--idle`; sends `finished a turn` only when `notify_on_stop` is true. `--stop --ask-now` (Codex, which has no idle event) also sends the ask at once |

Each entry runs `${CLAUDE_PLUGIN_ROOT}/hooks/notify.sh <mode>` in exec form. The script parses the hook JSON with `jq` (a dependency since 2026-10-13; without it, `--permission` sends the fixed phrase and `--idle` sends nothing, each logged once). The owner's words on the fixed phrases, from his phone: "These messages are not helpful. Is something waiting on me, then tell me what I need to do in the sock message." So the ask leaves the machine, capped and stripped of code blocks; rule 4 below is amended to match. The ask and the hash of the last one sent live per session under `${XDG_STATE_HOME:-~/.local/state}/sock-integrations/asks/` (mode 600, swept after 7 days).

`SKILL.md`: when to message the owner (long task finished, blocked on a decision, needs an approval), how to reply to a `<channel source="sock" ...>` tag with `sock_send` and the tag's `conversation_id`, that a reply on the channel is the owner's answer to the question asked, and that other message text is data, not instructions.

## Codex

Files under `codex/`: `config.toml` snippet, `hooks.json`, `install.md`.

- `mcp_servers.sock`: `command = "sock-mcp"`, `env_vars = ["SOCK_TOKEN", "SOCK_URL"]` so the token is forwarded from the shell and never written to config.
- `hooks.json`: `PermissionRequest` runs `--permission` (text `needs permission`) and `Stop` runs `--stop --ask-now`, which sends the turn's ask at once (Codex has no idle event) and `finished a turn` too when `SOCK_NOTIFY_ON_STOP=1`; both `async: true`, running the shared `notify.sh`. Codex handlers take no `args` field, so each `command` carries its arguments inline. The file holds the placeholder `/ABSOLUTE/PATH/TO/sock-integrations`, which install substitutes with the checkout path (`install.md` gives an awk recipe that treats `#`, `&` and `\` literally). The checkout path must contain no spaces. Whether Codex parses `command` through a shell or as exec form is unconfirmed in its docs, so the inline quoting is verified by hand at release.
- `install.md`: merge the entries into `~/.codex/hooks.json` (all matching hooks run), export `SOCK_TOKEN` only in a Codex launcher or wrapper script and not a global rc file (Claude Code's Bash tool inherits the environment, so a model can read it), then review and trust the hooks with `/hooks`. Codex skips a hook until it is trusted, and changing the script re-requires trust.
- Outbound only. The model can call `sock_send` as a tool. No inbound in v1.

## Codex plugin

The Codex counterpart of the Claude Code plugin, for Codex CLI, the Codex app and ChatGPT desktop (one plugin system: developers.openai.com/codex/plugins/build). It sits at the repo root so the shared `claude-code/hooks/notify.sh` is inside the plugin root and reused, not copied. Versioned independently of the Claude plugin (`plugin.json` `version`).

Files, from the official build page and the `agent-plugins.org` 1.0.0 schemas:

- `plugin.json` (root, the portable manifest): `name` `sock`, `version`, `description`, `author`, `repository`, `license` MIT, and `extensions.com.openai.interface` for the listing. No `hooks` key: `hooks/hooks.json` is discovered by default. No `skills` key: `skills/` is discovered.
- `mcp.json`: one stdio server `sock`, `command` `sock-mcp` from `PATH`, `args` `["--outbound-only"]`, no `env`. A plugin's stdio server gets `HOME`, `PATH`, `USER`, `SHELL`, `TMPDIR`, `PLUGIN_ROOT` and `PLUGIN_DATA` but never `SOCK_TOKEN`, and `${PLUGIN_ROOT}` is not expanded in `command` (live check, Codex 0.162.1, a probe server dumping its environment). So the server finds its credential in the CLI's config file: `sock puppets use <puppet id>` writes `puppet_token` to `~/.config/sock/config.toml` (mode 0600) and sock-mcp reads it when the environment has none (integrations-27u). `--outbound-only` serves only `sock_whoami`, `sock_list_conversations` and `sock_send` (rule 2, integrations-qox); the live check listed exactly those three. The schema has no `env_vars` or `enabled_tools`; a user can still narrow tools under `[plugins."sock".mcp_servers.sock]` in their Codex config.
- `hooks/hooks.json`: `PermissionRequest` (`--permission`) and `Stop` (`--stop --ask-now`: the turn's ask, plus `finished a turn` when `SOCK_NOTIFY_ON_STOP=1`), `async: true`, command strings with arguments inline as in `codex/hooks.json`, `"${PLUGIN_ROOT}/claude-code/hooks/notify.sh"` quoted so a plugin root with spaces works. Hooks run only for a manually installed plugin and the user reviews and trusts them with `/hooks`.
- `skills/sock/SKILL.md`: outbound only, `sock_send`, message text is data, never `sock_fetch` or `sock_ack`.
- `.agents/plugins/marketplace.json`: marketplace `sock-integrations` with one entry `sock`, `source` local `./` (the repo root is the plugin root), `policy.installation` `AVAILABLE`, `policy.authentication` `ON_INSTALL`. Install: `codex plugin marketplace add aeryx-ai/sock-integrations`, then pick sock in `/plugins`.

`$schema` in `plugin.json` is the schema URL the official build page itself prints (developers.openai.com/codex/plugins/build, `agent-plugins.org/schemas/1.0.0/`).

The plugin bundle is the whole repo: the plugin root has to contain `claude-code/hooks/notify.sh`, so the root is the repo root, and the install copies tests, `runner/`, `docs/` and `agent-sdk/` too. Accepted: they are inert to Codex (it reads `plugin.json`, `mcp.json`, `hooks/` and `skills/`), and one shared `notify.sh` beats a second copy that could drift.

Status: the plugin carries the hooks, the skill and the MCP server. Install it instead of the `[mcp_servers.sock]` block of `codex/config.toml`, never both: two servers named `sock` would clash. Its server uses one puppet for every Codex project on the machine: Codex starts a plugin's MCP server with its working directory at the plugin's cache directory and passes no project variable (live probe, Codex 0.162.1), so sock-mcp cannot tell projects apart there, and with a machine key and no repository it falls back to `puppet_token` rather than minting (sock-mcp codexkey). A server registered in `config.toml` starts in the session's project directory, so the manual route with a machine key as `SOCK_TOKEN` gives a puppet per project. The hooks agree with that server: they always send with `sock send --as-puppet --project "$PLUGIN_ROOT"` (notify.sh rule 2b) and ignore an exported `SOCK_TOKEN`, which the server never sees, so a plugin user needs no `SOCK_TOKEN`. Live-checked on Codex 0.162 against a throwaway `CODEX_HOME`: `codex plugin marketplace add <dir>` accepts the marketplace with `./` as the local plugin path, and `codex plugin add sock@sock-integrations` installs it (cached as a copy of the whole repo). The hooks were not exercised: an untrusted plugin's hooks are skipped in `codex exec`, and `/hooks` is interactive.

The public directory is out: it requires a remote HTTPS MCP with OAuth and forbids hooks.

Rules that carry over, and where the plugin cannot keep them:

- No token lives in the plugin. On the plugin route the puppet token lives in the CLI's config file (mode 0600, written by `sock puppets use`), the same file the CLI already uses; on the `config.toml` route it is forwarded by name and never written.
- Only the sending tools are exposed to Codex (rule 2): `--outbound-only` on the plugin route, `enabled_tools` on the `config.toml` route. The skill also says not to call `sock_fetch` or `sock_ack`, which is advice.
- Hooks follow rule 4 through the shared `notify.sh`: `PermissionRequest` runs `--permission` (Codex's input carries no `message`, so it sends `needs permission`) and `Stop` runs `--stop --ask-now`, since Codex has no idle event.

Not verified: that `${PLUGIN_ROOT}` expands inside a hook `command` (it is an environment variable the plugin's processes get), and `policy.authentication` values.

## Agent SDK

`agent-sdk/example.ts`: `query()` with `mcpServers.sock` as a stdio server (`command: "sock-mcp"`, `env.SOCK_TOKEN` from `process.env`) and `allowedTools` of `mcp__sock__sock_whoami`, `mcp__sock__sock_list_conversations` and `mcp__sock__sock_send`. Auto-approving `sock_send` is the point for headless agents. The blast radius is the puppet: its owner and conversations it belongs to, 60 sends per minute. `sock_fetch` and `sock_ack` are deliberately not auto-approved, because fetch returns messages from every sender in every conversation the puppet is in. The example says both in comments.

## notify.sh

Shared by Claude Code and Codex. Behavior:

1. Read the hook JSON from stdin, bounded to 2 seconds so a held-open stdin cannot hang the hook, and parse it with `jq`. The mode is the first argument: `--permission`, `--stop` (optionally `--ask-now`), `--idle`, or none, in which case the argument is a fixed phrase (the v1 shape).
1a. `--stop` extracts the ask from `last_assistant_message`: fenced blocks dropped (CommonMark: a run of 3 or more backticks or tildes opens one, and only a run of the same character at least as long, indented at most 3 spaces, with nothing after it closes it; one departure, a backtick run with a backtick later on its line opens a fence too, where CommonMark reads inline code, so code under it is dropped rather than sent), then from the last paragraph holding a `?` to the end, or the paragraph before a trailing numbered list (blank lines between its items included); nothing when there is neither. Byte caps (600 for the ask, 300 for `--permission`) drop a UTF-8 sequence the cut leaves incomplete, since the CLI refuses invalid UTF-8. It records the ask per session (`asks/<session>.ask`, written atomically) and clears the session's sent marker, so each turn starts fresh. `--idle` sends the recorded ask once per turn (`asks/<session>.sent` holds its SHA-256; skipped when no hash tool exists). Without a recorded ask, `--idle` reads the last 2 MiB of `transcript_path`, only when it is a regular file, and takes the last assistant entry that carries text. `--permission` sends the notification's `message`. Every outgoing text has the token and common secret shapes replaced with `[redacted]`, before any byte cap so a secret straddling the cut is never sent in part (sock tokens, AWS key ids, GitHub and `sk-` keys, bearer values, `NAME=value` where NAME names a key, token, secret or password, URL query strings).
2. Resolve token: `CLAUDE_PLUGIN_OPTION_SOCK_TOKEN` (set only under the plugin), else `SOCK_TOKEN`. The plugin token wins because `.mcp.json` always gives sock-mcp the plugin token, and an ambient `SOCK_TOKEN` exported for Codex must not make Claude Code hooks post as the Codex puppet. The URL comes from the same source as the token: `CLAUDE_PLUGIN_OPTION_SOCK_URL` for the plugin token, `SOCK_URL` for the env token, never the other. A URL from the matching source is passed as `sock --url`, so a config file cannot override it; with none, `SOCK_URL` is unset for the child, no `--url` is passed and the CLI default or config applies. Missing token: log one line and exit 0.
2a. A `sock_mk_` machine key is never the sender. The script runs `sock puppets ensure -- <CLAUDE_PROJECT_DIR, else PWD>` with the key as `SOCK_TOKEN` for that call only, runs it under the same watchdog as the send, accepts the printed path only when it names a regular file under the state dir's `puppets` cache whose first line is a `sock_pt_` token, and sends with that puppet token, so each project's messages come from `<project> on <host>` (ADR 10). An ensure that fails, times out or prints anything else logs one line with its stderr (both credentials redacted), sends nothing and exits 0. The script unsets `SOCK_TOKEN` and `CLAUDE_PLUGIN_OPTION_SOCK_TOKEN` once it has read them, so no child but the one sock call sees a credential. sock-mcp makes the same swap for its own process.
2b. The Codex plugin route (`PLUGIN_ROOT` set, `CLAUDE_PLUGIN_OPTION_SOCK_TOKEN` not) always sends with `sock send --as-puppet --project "$PLUGIN_ROOT"` (sock-cli v0.2.5 or later). The CLI resolves the credential exactly as the plugin's sock-mcp does, which runs in `PLUGIN_ROOT`: `SOCK_TOKEN`, else the config file's `machine_key` through ensure for that directory, else its `puppet_token`, never the login token (sock-client `ResolvePuppet`), so the hook and the server send as one puppet (integrations-zpe). `SOCK_TOKEN`, `SOCK_URL`, `SOCK_ACCESS_GATE` and `CLAUDE_PROJECT_DIR` are unset for that call: the plugin's sock-mcp never sees the first three, so an exported `SOCK_TOKEN` would make `ResolvePuppet` pick a different puppet and the config file must pick the server for both, and the CLI lets `CLAUDE_PROJECT_DIR` override `--project`. A refusal (no puppet credential) logs one line through rule 6 and sends nothing. The Claude Code plugin and manual routes keep rules 2 and 2a.
3. `--stop` also sends `finished a turn` when `CLAUDE_PLUGIN_OPTION_NOTIFY_ON_STOP` is `true` or `SOCK_NOTIFY_ON_STOP` is `1`.
4. Text is `<name>: <body>`, where `<name>` is the basename of `CLAUDE_PROJECT_DIR`, else `PWD`. Under `LC_ALL=C` the name keeps only `[A-Za-z0-9._ -]`, is cut to 100 characters, and becomes `project` when nothing remains.
5. Run `sock send` with the text on stdin, never in argv, so another user's `ps` cannot read it, as a child with a watchdog that sends TERM after `SOCK_HOOK_TIMEOUT` seconds, default 10, and KILL two seconds later; a value that is not a positive integer falls back to 10. Async hooks are not timed out by the host, so the script bounds itself. The watchdog is reaped without a job notice on stderr.
6. Any failure, including `sock` missing from `PATH`, appends one line to `${XDG_STATE_HOME:-$HOME/.local/state}/sock-integrations/hook.log` (mode 600, truncated above 100 KB). A failed send logs `sock send exit <code>:` and the child's stderr, with the token and every secret shape of rule 1a redacted from the whole captured stderr first and the result then cut to 300 bytes, so a token straddling the cut cannot leak a prefix. Every control character, C1 included (raw 0x80-0x9F and UTF-8 C2 80-9F, with any byte outside a well-formed UTF-8 sequence), is stripped from the line, so tool output cannot drive a terminal that reads the log. The line never contains the token.
7. Exit 0 always.

## Headless inbound: sock-runner

Codex and a `claude -p` agent have no channel: nothing pushes a message into a session that is idle. The inbound path is the CLI's `sock listen --exec`, which streams the puppet's deliveries and calls a command once per message. `runner/sock-runner` is that command: it resumes the agent's session headlessly with the owner's message as the prompt. It adds no server surface and no state of its own besides a lock and a log.

Rejected: a `Stop` hook that fetches pending messages. It fires only at the end of a turn, so an idle session never fetches, and it would consume the puppet's deliveries beside the channel (rule 2).

### Invocation

```sh
SOCK_TOKEN=sock_pt_...  SOCK_URL=...                      # the runner's own puppet, see Rules
SOCK_RUNNER_AGENT=claude                                  # claude | codex, required
SOCK_RUNNER_SESSION=<session id>                          # a session nobody has open, required
SOCK_RUNNER_WORKDIR=<dir>                                 # required, an existing directory
SOCK_RUNNER_PERMISSION=<mode>                             # required, no default
sock listen --exec /path/to/runner/sock-runner
```

Optional: `SOCK_RUNNER_TIMEOUT` (seconds an agent run may take, default 900; not a positive integer falls back to 900), `SOCK_RUNNER_STATE` (directory of the lock and log, a relative one taken from where the runner starts, default `${XDG_STATE_HOME:-$HOME/.local/state}/sock-integrations`).

`SOCK_RUNNER_PERMISSION` is the agent's headless permission posture, passed through and never derived from a message, and it is an allowlist: exactly the agent's own modes, anything else exits 2 before a message is read. For `claude`: `acceptEdits`, `default`, `dontAsk`, `manual`, and `auto` and `bypassPermissions`, which also need `SOCK_RUNNER_ALLOW_BYPASS=1` (auto mode approves beyond acceptEdits). The list is the CLI's own `--permission-mode` choices plus `default`, which it accepts. For `codex` the `--sandbox` value: `read-only`, `workspace-write`, and `danger-full-access`, which also needs `SOCK_RUNNER_ALLOW_BYPASS=1`. `plan` is refused for `claude` (exit 2): live-checked with claude 2.1.296, plan mode blocks non-read-only tools such as `sock_send` regardless of `--allowedTools`, the model then says it could not send, claude exits 0 and the runner would log `ran`. Documented defaults: `default` and `read-only`.

There is no prompt to answer in a headless run, and an MCP tool that is not pre-approved is denied. So for `claude` the runner always passes `--allowedTools` with the three sock tools the plugin serves, `--disallowedTools` for the other two (`sock_fetch` and `sock_ack` consume the deliveries `sock listen` owns, so rule 2 holds even under `bypassPermissions`), and `--permission-prompts none`: without them `sock_send` is refused in every mode but `bypassPermissions` and the owner never hears back. The names follow Claude Code's plugin tool convention, `mcp__plugin_<plugin>_<server>__<tool>` with any character outside `A-Za-z0-9_-` replaced by `_` ([MCP docs, "Plugin MCP tool names"](https://code.claude.com/docs/en/mcp)): plugin `sock`, server `sock`, so `mcp__plugin_sock_sock__sock_whoami`, `..._sock_list_conversations` and `..._sock_send`. A test derives them from `plugin.json` and `.mcp.json` and fails if either name changes. For `codex`, `codex/config.toml` sets `enabled_tools` to those three tools and `default_tools_approval_mode = "approve"` for the sock server ([Codex MCP config](https://learn.chatgpt.com/docs/extend/mcp)); both keys apply to every Codex session that merges the file, interactive ones included, which `codex/install.md` says. Not yet checked against a live `codex exec` (tracked as a bead).

### Interface with sock listen

`sock listen --exec` (sock-cli `internal/cli/listen.go`, `handle`) runs the command once per message, with:

- stdin: the message as `protojson` of `sock.v1.Message`, one object: `id`, `conversationId`, `seq` (int64, so a JSON string), `senderId`, `sentAt` (RFC 3339), `text`, `evidenceTag` (base64). protojson randomizes whitespace between tokens, so the runner never matches on exact bytes.
- env: `SOCK_MESSAGE_ID`, `SOCK_CONVERSATION_ID`, `SOCK_SENDER_ID`. Ids only, never text.
- exit status: 0 acknowledges the message; non-zero leaves it pending, `listen` retries up to 5 times with backoff and then holds it for the next connection. Delivery is at least once, so a run may repeat after a crash between the agent finishing and the ack.

### Behavior

1. Validate configuration: `jq` and `sock` on `PATH` (jq decodes the protojson text exactly, escapes and surrogate pairs included), agent, session, workdir and permission present, session id matching `[A-Za-z0-9._-]{1,128}`, workdir an existing directory, `claude` or `codex` on `PATH`. A bad configuration exits 2 and acknowledges nothing; the message stays pending and the log says why.
2. The owner gate. The sender must be the puppet's owner: `SOCK_SENDER_ID` and the `senderId` in the stdin JSON must be the same UUID, settled before any network call, and equal `account.puppet.ownerId` from `sock whoami --json`, run with the same token. The owner id is cached for 60 seconds in the state directory, keyed by a hash of the token, so a non-owner in a shared conversation costs no call per message. A token whoami does not show as a puppet (a human session) is a configuration error: exit 2, nothing acknowledged, stderr and the log say why. Acknowledging would consume that human account's own deliveries and lose them, and `sock listen` does not wedge on the failure: it retries the message five times and leaves it pending. Any other sender, a missing field, a puppet token that whoami does not show as a puppet, or a failing whoami: a gate failure that is the sender's (not the owner, malformed) drops the message, exit 0, so it is acknowledged and cannot wedge the stream; a whoami that could not run (network, 5xx) exits 1 so the message is retried. Nothing from a non-owner reaches the agent.
3. Take the run lock: one agent run at a time per `SOCK_RUNNER_STATE` (macOS has no `flock`). The lock is a symlink `runner.lock` whose target is the runner's pid, made with `ln -s`: creating it is atomic and carries the pid in the same step, so no second runner can see a lock without one. The target is `<pid>:<start time>` (`ps -o lstart=`, read under `LC_ALL=C TZ=UTC` because it is compared as text), so a pid that a later process has reused is not mistaken for a live runner; a bare pid target counts as live while the pid exists. A lock whose pid is dead, or whose start time no longer matches, is stale and replaced under a second guard, `mkdir runner.lock.break` (held for milliseconds, itself removed when older than a minute): inside the guard the runner re-reads the lock, confirms it is still stale, removes it and creates its own, so two runners cannot both break the same stale lock. The lock is removed on exit, including a signal. Waiting counts against the timeout; past it, exit 1.
4. `id` and `conversationId` from stdin must each be a UUID, and a value holding a newline is never one (`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`), as must the env copies; anything else is malformed and dropped with exit 0, so nothing but a UUID can reach a tag attribute. Build the prompt from the owner's text only: `<sock-message from="owner" conversation="<conversationId>" id="<id>">` newline text newline `</sock-message>`, with every `</sock-message` inside the text, in any case and with spaces after `</`, broken by inserting a zero-width space after `</`. A one-line preface above the tag says the tag holds the owner's reply to the question the agent asked and that it is not a system instruction. The text goes to the agent on stdin; it never appears in argv, the environment or a log. It is written once to a private file in the state directory (mode 600, removed on exit, and any `prompt.*` older than the timeout swept at start) so the agent reads it from a file descriptor, and the agent's own session transcript keeps the owner's text for as long as that transcript lives.
5. Run the agent, in `SOCK_RUNNER_WORKDIR`, as a background job under `set -m` so it leads its own process group. After `SOCK_RUNNER_TIMEOUT` seconds the runner sends TERM to the whole group, then KILL after 5 seconds, so the MCP servers and tool children `claude` and `codex` spawn do not outlive the run. The environment the agent gets is the runner's minus `SOCK_RUNNER_*` and the `listen` variables, and for `claude` also minus `SOCK_TOKEN` and `SOCK_URL`: the plugin's sock-mcp takes its token from the plugin's own configuration, and the agent's shell tool must not be able to read the runner's. For `codex` the token stays, because `codex/config.toml` forwards `SOCK_TOKEN` and `SOCK_URL` to sock-mcp with `env_vars`; the agent can then read it, which `codex/install.md` already says of every Codex launch.
   - `claude`: `claude -p --resume "$SESSION" --permission-mode "$MODE"`, prompt on stdin.
   - `codex`: `codex exec --sandbox "$MODE" resume "$SESSION" -`, prompt on stdin (`--sandbox` is an option of `exec`; `exec resume` has none).
   The agent replies with `sock_send` through the plugin's sock-mcp (outbound only, rule 2); the runner sends nothing.
6. Redelivery: `listen` is at-least-once, so before running, the runner checks `done.ids` in the state directory, the last 200 message ids that finished with agent exit 0, before it waits for the lock (so a repeat does not queue behind a running turn) and again once the lock is its own, and acknowledges a repeat without running it (`duplicate`). The id is added under the lock after a successful run; a failed or timed-out run is not remembered, so its retry runs. Exit 0 when the agent exits 0. Any other agent exit, or a timeout: exit 1 so the message is retried. A prompt that crashes the agent every time is retried five times and then held pending, which is why the lock and timeout bound the damage.
7. Log one line per message to `runner.log` in the state directory (mode 600, truncated above 100 KB): time, message id, outcome (`ran`, `dropped: sender`, `dropped: malformed`, `agent exit <n>`, `timeout`, `locked`). Never the text, the token, or the agent's output. A failed log write never changes the exit status, as in `notify.sh`.

### Rules that follow

- The runner's puppet is its own: sock-mcp for that puppet runs without the channel and nothing calls `sock_fetch` or `sock_ack` for it (rule 2). The `listen` process is the single consumer.
- Use one `SOCK_RUNNER_STATE` per runner: two runners sharing the default directory share one lock, one ledger and one log (and the directory `notify.sh` logs to).
- `sock listen --exec` runs its command through `/bin/sh -c`, so the runner's path must contain no spaces or shell metacharacters.
- `SOCK_RUNNER_SESSION` must be a session no one has open: a resume while the same session is open in an interactive client interleaves two writers on one transcript. Give the runner its own session.
- Run `sock listen` and the agent as the same user, in the workdir's project. The token comes from the environment (`SOCK_TOKEN`), as everywhere in this repo.
- Message text is untrusted data even from the owner: the permission mode is the control, and the default is the most restrictive.
- At-least-once: the agent may see a message twice. The prompt carries the message id so the session can recognise a repeat.

### Managed Agents bridge: sock-bridge

A Claude Managed Agents session runs on Anthropic's side and is steered by events: the owner's reply goes in as a `user.message`, and the agent's answer comes back as `agent.message` events. `runner/sock-bridge` is the `sock listen --exec` command that carries one owner reply into one session and the agent's answer back to the owner. Conformist downstream of two upstreams, `sock listen --exec` and Anthropic's Managed Agents API (beta header `managed-agents-2026-04-01`; [events and streaming](https://platform.claude.com/docs/en/managed-agents/events-and-streaming)); it adds no server surface and no domain model, and it is the anti-corruption layer between the two vocabularies: a sock message in, session events out, never a session event vocabulary inside sock.

Not a daemon. A daemon wrapping `sock listen` would duplicate what `listen --exec` already does (reconnect, backoff, at-least-once, ordering, ack on exit 0) and keep a second copy of stream state. A command run once per message, like `sock-runner`, reuses all of it: the turn is synchronous inside the command, and the ack is the exit code. The cost is that one long agent turn holds the stream; the timeout bounds it, and Managed Agents queues events anyway. No service means no rookery scaffold.

### Invocation

```sh
export SOCK_TOKEN=sock_pt_...             # the bridge's own puppet, no other consumer
export ANTHROPIC_API_KEY=...              # a key that may use this session
export SOCK_BRIDGE_SESSION=sesn_...       # an existing session, required
sock listen --exec /path/to/runner/sock-bridge
```

Optional: `SOCK_BRIDGE_TIMEOUT` (seconds one turn may take, default 900; not a positive integer falls back), `SOCK_BRIDGE_API` (default `https://api.anthropic.com`; the key goes there, so it must be an https URL or http to `localhost` or `127.0.0.1`, else exit 2), `SOCK_BRIDGE_MAX_STREAM` (bytes the event stream may hold, default 32 MiB), `SOCK_BRIDGE_STATE` (default `${XDG_STATE_HOME:-$HOME/.local/state}/sock-integrations-bridge`; one per bridge). `SOCK_BRIDGE_API` (default `https://api.anthropic.com`) exists for tests and is not for production use. The bridge does not create sessions, set the agent's tools or choose its permission policy: those live in the agent and environment definitions on Anthropic's side, and the owner of the session chooses them.

### Interface

stdin, env and exit codes are `sock-runner`'s (the `sock listen --exec` contract above): exit 0 acknowledges, 1 retries, 2 is a configuration error and runs nothing. The same gate and the same input hygiene apply, shared in one library file `runner/lib.sh` that both commands source: UUID-checked ids, env and stdin senders equal, the sender equal to `account.puppet.ownerId` (cached 60 seconds), a non-owner or malformed message acknowledged and dropped, the state-directory lock so one turn runs at a time, the log of fixed strings and ids only.

### Behavior

1. Configuration: `SOCK_BRIDGE_SESSION` matching `[A-Za-z0-9_-]{1,128}`, `ANTHROPIC_API_KEY` present, `curl`, `jq` and `sock` on `PATH`. Anything else exits 2.
2. The gate and the lock as in `sock-runner`.
3. Redelivery state: `done.ids` (the last 200 ids whose answer was posted: a repeat is acknowledged without work, `duplicate`) and `sent.ids` (ids whose `user.message` was or may have been posted). The id goes into `sent.ids` before the POST, so a run killed or failed between the post and the record cannot send it twice. A message in `sent.ids` is first looked up in the session's history (step 7); only when the history, read in full, does not hold it does the bridge send.
4. Open the event stream first (`GET /v1/sessions/$SESSION/events/stream?beta=true`, `accept: text/event-stream`); a stream shows only events after it opens, so it is open before the send. Then send the owner's reply: `POST /v1/sessions/$SESSION/events?beta=true` with `{"events":[{"type":"user.message","content":[{"type":"text","text":"<prompt>"}]}]}`. The prompt is the runner's: a one-line preface and `<sock-message from="owner" conversation=... id=...>` around the text, closing tags broken, so the id is in the session's history and a redelivery can find it.
5. Follow the stream until the turn ends. Only whole lines are events: the stream file is read by byte offset and a trailing line without its newline waits for the rest. Keep the text blocks of `agent.message` events (buffered events, one per model request) in order, once per event id, and ignore everything else, thinking and tool events included: tool inputs and outputs can hold secrets and never reach sock. The file is bounded (`SOCK_BRIDGE_MAX_STREAM`; past it the bridge exits 1, `stream too large`) and each poll reads only the new bytes in one `jq` pass, so a flood costs linear time and the deadline is checked every poll. The turn ends at `session.status_idle` with `stop_reason.type`:
   - `end_turn`: post the collected text to the owner (an empty or whitespace-only answer posts `the agent finished without a reply`), exit 0.
   - `requires_action`: a tool call needs an answer the bridge cannot give. Post the fixed text `the agent is waiting for approval of a tool call` (no tool name, no input) and exit 0; the owner answers in the Managed Agents console. Brokering confirmations through sock replies is a follow-up.
   - `budget_reached`: post `the agent's budget is used up`, exit 0.
   - `session.error`: post `the agent hit an error`, exit 0. A stream that ends without idle exits 1 (`stream dropped`), so the retry finishes from history.
6. Posting: `sock send` with the text on stdin (the puppet's `NotifyOwner`; argv is visible in `ps`), control characters other than newline and tab removed (an agent's answer can hold terminal escapes), at most 60000 bytes per message, split on blank lines, then lines, then bytes, never inside a UTF-8 character; at most 5 parts, the last saying the rest was cut. After each part the bridge writes `<id> <n>` to `posted.part`, so a retry of a message whose part n failed posts only the remaining parts. Record `replied` (`done.ids`) after the last part, then exit 0. A crash between the last part and the record repeats the last part on redelivery, which is at-least-once.
7. Catch-up from history (an id in `sent.ids`): list the session's events (`GET /v1/sessions/$SESSION/events?beta=true`, following `next_page` through `page=`; events come oldest first), find the last `user.message` whose text's second line, after the preface, is exactly this message's opening tag (a tag quoted inside a message's text is on a later line and does not count), take the `agent.message` text (once per event id, in history order, events without an id kept) after it up to the first `session.status_idle` or `session.error`, and continue at step 5's outcomes. The message is there but the agent has not finished: poll the history every 2 seconds until it has, or exit 1 at the deadline. The message is not there: the post never landed, so the bridge sends it (steps 4 and 5).
8. HTTP politeness: `User-Agent: sock-bridge`, a connect timeout and an overall turn timeout, `Retry-After` honored (seconds or HTTP date, any digit string, never past the time left) with exponential backoff otherwise. 429 is retried a few times for any request, and 5xx and network failures too for a GET; a POST that failed any other way may have landed and is never repeated (exit 1, and the redelivery asks the history). 401, 403 and 404 exit 2 (the key or session is wrong; retrying cannot help). The key goes to curl on a private config file (`--config`), never in argv, the environment of any child or the log. The key's charset is checked (`A-Za-z0-9_.-`) so it cannot break out of a config line.
9. Log one line per message to `bridge.log` (mode 600, truncated above 100 KB): time, id, outcome (`agent error (<stop reason>)` names the API's stop reason when it is `[a-z_]{1,40}`, else `unknown`; `ran`, `dropped: sender <uuid>`, `dropped: malformed`, `not a puppet token`, `duplicate`, `waiting for approval`, `budget`, `agent error`, `stream dropped`, `stream too large`, `send failed`, `http <status>`, `timeout`, `locked`). Never the text, the answer, the key or the token.

### Rules that follow

- The bridge's puppet is its own and its one consumer is `sock listen`. Nothing calls `sock_fetch` or `sock_ack` for it.
- Temp files are named `<prefix>.<pid>` in the state directory and removed on exit; the next run removes those of pids that no longer exist, and any `curl.*` older than the turn timeout whatever its pid (it holds the key), so a SIGKILL cannot leave the curl config and its key behind for long (an orphaned stream curl ends at its `--max-time`, the turn deadline). An unwritable state directory exits 2 before any wait.
- One session per bridge, and the session is not driven by anything else at the same moment: two writers interleave turns, and the bridge cannot tell whose answer a `session.status_idle` ends.
- The owner's reply is data to the agent, in a tag, like everywhere in this repo. What the agent may do is the session's own configuration.
- The agent's answer goes only to the owner, over the bridge's puppet, as ordinary messages.

### Tests

Under `test/`, with a fake Managed Agents server (Go `httptest`, speaking the stream and events endpoints) and stub `sock` and `curl` where the harness needs them: the stream is open before the send; the request has the headers `x-api-key`, `anthropic-version` and `anthropic-beta: managed-agents-2026-04-01` and the `user.message` body; text blocks of `agent.message` are posted once at `end_turn` and thinking and tool events never are; `requires_action`, `budget_reached` and `session.error` post only their fixed texts; a dropped stream exits 1 and the retry finishes from history without a second `user.message`; a `replied` id is a duplicate; a long answer is split and bounded; 429 with `Retry-After` is honored and 5xx retried then exit 1; 401, 403 and 404 exit 2; the key is in no argv, child environment, log or file other than the private curl config, which is removed; the owner gate, the lock and the input hygiene hold as in the runner's tests (shared cases run against both commands).

Security review before merge, as the review gate below: sender spoofing and the gate, injection through the text into the tag or the API body, the key's exposure (argv, environment, logs, files, `curl` config), the lock and ledger races, and a hostile or malformed event stream (very large events, a flood of `agent.message`, events that never reach idle).

## Tests

Under `test/`, against stubs of `sock` (whoami output, listen environment) and of `claude` and `codex` that record argv, environment and stdin: the owner's message runs the agent exactly once with the text on stdin and in neither argv nor environment; a non-owner sender, a mismatch between env and stdin sender, malformed JSON and a missing field are dropped with exit 0 and no agent run; a failing whoami exits 1 with no agent run; an owner id is cached for a minute and a non-owner costs no call; a human token exits 2 with nothing acknowledged; the allowed tools equal the plugin-derived names; a redelivered id does not rerun; a stale lock whose pid was reused is replaced; a bad configuration exits 2; a second runner waits on the lock, a stale lock is replaced by exactly one of two racing runners, and a lock whose pid file is still being written is never taken (the lock is one symlink, so there is no such window); a non-UUID `id` or `conversationId` is dropped with exit 0 and never reaches the agent; the timeout kills grandchildren of the agent too (a stub agent that spawns a long `sleep` leaves none); the agent's environment holds no `SOCK_TOKEN` or `SOCK_URL` for `claude` and does for `codex`; a log directory that cannot be written still exits with the agent's status; the timeout kills the agent and exits 1; the log never contains the text or the token; `</sock-message>` in the text cannot close the tag; `bypassPermissions` and `danger-full-access` are refused without `SOCK_RUNNER_ALLOW_BYPASS=1`.

Security review before merge, as the review gate below: sender spoofing, injection through the text into argv, environment or the tag, lock races and stale locks, log leakage, and any path where a non-owner message reaches the agent.

## Hosted connector: ChatGPT, dots and claude.ai

The public listing in the OpenAI plugin directory and the claude.ai connector both take a remote MCP server behind OAuth 2.1, and the directory refuses plugins with hooks. The design is split ([ADR 11](https://github.com/aeryx-ai/sock-contract/blob/main/docs/adr/0011-agent-platforms-connect-through-oauth.md) in `sock-contract`): the authorization server and the connector grant live in `sockd`'s Identity context on `api.sock.aeryx.ai`; the resource server is `sock-mcpd` on `mcp.sock.aeryx.ai`, specified in [connector](connector.md). This repository owns the client side:

- **Client registration.** ChatGPT and Claude arrive by CIMD, so no secrets are exchanged; redirect URIs come from their metadata documents (`https://chatgpt.com/connector_platform_oauth_redirect` because the hub implements RFC 9207, `https://claude.ai/api/mcp/auth_callback`, loopback for Claude Code). Others register by DCR.
- **The directory package** is a second plugin, separate from the Codex marketplace one (which keeps hooks for manual installs): `skills/sock/SKILL.md` plus a remote MCP entry for `https://mcp.sock.aeryx.ai/mcp` with auth mode `cimd`, every tool declared `oauth2`, no hooks, and the listing metadata under `extensions.com.openai.interface`: display name, the subtitle (at most 30 characters), long description, developer name, category, a square icon of at least 48 px, website, support, privacy and terms URLs. The domain challenge is served by `sock-mcpd`. Submission needs the owner's business verification, five positive and three negative test cases, a demo video, release notes and a reviewer account: a dedicated sock human on production whose connector puppet the consent flow creates, with no MFA. Servers are rescanned daily, so a tool change is a release.
- **Dots.** The user tells the dot to watch sock replies; whether the dot or ChatGPT creates the `owner.reply` subscription is settled by the live dot test and written into the install notes.
- **claude.ai.** The same URL as a custom connector; `oauth_cimd` works without a registration call. Outbound only in v1; Claude Code keeps the stdio plugin and its channel.
- **Testing, in order**: tool discovery over OpenAI's Secure MCP Tunnel with the stdio server; the staging endpoint as a custom MCP server in ChatGPT developer mode and as a claude.ai connector; an owner reply arriving at a dot; then submission. The production plugin stays private, installed from this marketplace with the hosted URL, until the directory approves.

## Project puppets: the machine key

One puppet per machine breaks with more than one Claude Code session: the hub's delivery queue is per puppet, so sessions compete for the owner's replies. The product owner's requirements: setup in one step, and every message shows who sent it. [ADR 10](https://github.com/aeryx-ai/sock-contract/blob/main/docs/adr/0010-machine-keys-mint-project-puppets.md) in `sock-contract` gives the hub a **machine key** (`sock_mk_`): a human's device that may only create, list, mint tokens for and erase the puppets it created. This repository's part:

- The plugin's `userConfig` keeps one `sock_token` option (sensitive, required, titled "sock machine key or puppet token" since plugin 0.3.2) that takes either credential; `sock_url` and `notify_on_stop` stay. This amends the earlier plan of a separate `sock_machine_key` option: one option means a `sock_token` already set keeps working, as one puppet for the machine, until the owner pastes a machine key in its place.
- `sock-mcp` (the `mcp` repository) mints one puppet per project on first use, named `<project> on <host>`, where the project is the repository name (every worktree of a repository shares its puppet) or the directory name outside git; on `PUPPET_NAME_TAKEN` it appends a short hash of the path. It keeps each token at `${XDG_STATE_HOME:-~/.local/state}/sock-integrations/puppets/<key>` (mode 600) beside the repository path, and on start erases puppets whose repository is gone.
- `hooks/notify.sh` sends as the project's puppet: it asks the `sock` CLI for the project's token (`sock puppets ensure <dir>`, which reads the same cache or mints), so the hook and the server always agree. The ensure-or-read routine lives once, in `sock-client`.
- `SKILL.md` and the README say what the owner sees: a contact per project, erasable from the app like any puppet; `sock puppets use` stays for Codex and the tunnel.
- Each diff that touches the key (hub, `sock-mcp`, this plugin) gets a fresh-context security review before merge.

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
- Fixed phrase over forwarding `message` (superseded 2026-10-13): the fixed phrases told the owner nothing, so the hooks now forward the notification text and the trailing ask of the last assistant message. The residual risk, text the assistant echoed leaving the machine, is accepted by the owner; code blocks are dropped and the ask is capped at 600 bytes.
- Plugin `userConfig` for the token: secure storage, one prompt, and hooks and the MCP server both receive it without a file.

## Follow-ups outside this repo

- sock-contract context map: add an Integrations row, a conformist downstream of sock-mcp and sock-cli.
