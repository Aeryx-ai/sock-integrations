<p align="center">
  <img src="assets/sock-emblem.svg" width="120" alt="Sock">
</p>

# sock-integrations

Sock is messaging between people and the agents, or puppets, that act for them. The hosted hub is at `api.joinsock.com`; see [joinsock.com](https://joinsock.com).

This repo connects coding agents to Sock through a puppet account you own, so an agent can tell you when it needs you and hear your reply in the session that asked. Every integration here is a thin layer over two binaries from sibling repos: [`sock`](https://github.com/aeryx-ai/sock-cli), the CLI, and [`sock-mcp`](https://github.com/aeryx-ai/sock-mcp), the MCP server.

| Integration | What it is | Where |
|---|---|---|
| Claude Code plugin | Skill, hooks and MCP server; replies reach the session | `claude-code/` |
| Codex plugin | Skill, hooks and MCP server for the Codex TUI and app | `codex/`, `skills/`, `hooks/`, `mcp.json` |
| Agent SDK | A headless Claude Agent SDK agent with the send tools | `agent-sdk/` |
| sock-runner | Headless chat: a Claude Code or Codex session per project answers your messages | `runner/sock-runner`, `runner/sock-runner-service` |
| sock-bridge | Carries your replies into a Claude Managed Agents session and the answer back | `runner/sock-bridge` |
| sock-http | Forwards your messages to any agent with an inbound HTTP API, such as OpenHands | `runner/sock-http` |
| Hosted connector | Remote MCP over OAuth for claude.ai and ChatGPT | `hosted/` |

## Prerequisites

Install `sock` and `sock-mcp` and put them on `PATH`, including the `PATH` of the agent process (Claude Code's often lacks `~/go/bin`):

```sh
brew install aeryx-ai/tap/sock aeryx-ai/tap/sock-mcp
```

or

```sh
curl -fsSL https://joinsock.com/install.sh | sh
```

or, from the sibling checkouts `../cli` and `../mcp`:

```sh
make install-bins
```

Then sign in once and keep a machine key in the sock config file. Every project then gets its own puppet, named `<project> on <host>`, with no token to paste anywhere:

```sh
sock login
sock machine-key create | sock machine-key use
```

The key can only make and erase the puppets it made: it cannot read or send your messages. Erase a project's puppet from the app like any other puppet.

## Claude Code

```
/plugin marketplace add aeryx-ai/sock-integrations
/plugin install sock@sock-integrations
```

Or from a checkout:

```sh
make install
```

Leave the plugin's `sock_token` empty. With a machine key every session gets its own puppet the first time it messages you, named `<project> <first 8 of the session id> on <host>` until you `/rename` the session, then for its title. Reply in that puppet's thread and the reply appears in that session, idle or busy ([ADR 4](docs/adr/0004-claude-replies-enter-through-the-session-inbox.md)). A session's puppet is archived when the session ends and comes back when it is resumed. Tokens are cached in `${XDG_STATE_HOME:-~/.local/state}/sock-integrations/puppets` (mode 600).

A puppet token (`sock_pt_`) set as `sock_token` instead makes every session on the machine share one puppet, and replies race between sessions. Change the value with `claude plugin configure sock@sock-integrations`.

The hooks message you when a permission prompt is waiting and, at the end of a turn, with the turn's question when it has one. They send only from an interactive session, at most once an hour per project; `-p`, SDK, background and subagent sessions never do.

Mac setup step by step, including the runner service, is in `docs/setup-mac.md`.

## Codex

```sh
codex plugin marketplace add aeryx-ai/sock-integrations
codex plugin add sock@sock-integrations
```

Then run `/hooks` in Codex and trust both hooks. They send with `sock send --as-puppet`, so hooks and the model send as the same puppet, with no token in the environment. Needs sock-cli v0.2.5 and sock-mcp v0.2.3 or later.

With a machine key the plugin sends as each project's own puppet and your replies come back into the session that asked as a turn ([ADR 1](docs/adr/0001-codex-replies-enter-through-the-app-server.md), `docs/specs/codex-inbound.md`). With `sock puppets use <puppet id>` instead, every project sends as that one puppet.

The manual route, for anyone who wants no plugin: append `codex/config.toml` to `~/.codex/config.toml`, export `SOCK_TOKEN` in the launcher that starts Codex and install `codex/hooks.json` with the checkout path substituted. Every step is in `codex/install.md`.

## Agent SDK

A headless agent that can message its owner, with the inbound tools never exposed. Needs `@anthropic-ai/claude-agent-sdk` and `tsx` installed:

```sh
npm install @anthropic-ai/claude-agent-sdk tsx
SOCK_TOKEN=sock_pt_... npx tsx agent-sdk/example.ts "tell me when you are done"
```

`agent-sdk/example.ts` is the whole integration: it hands the token and `SOCK_MCP_MESSAGE_OWNER=1` to `sock-mcp --outbound-only` and allows the three send tools.

## sock-runner: headless chat per project

`sock listen --exec` runs `runner/sock-runner` once per message. It resumes a headless `claude` or `codex` session for the project with your message as the prompt, shows the puppet working while it runs, and the agent replies with `sock_send`. Needs `sock`, `jq` and the agent on `PATH`.

Install it as a per-project service that starts at login and restarts on exit (launchd on macOS, systemd `--user` on Linux):

```sh
runner/sock-runner-service install ~/projects/<project>
runner/sock-runner-service uninstall ~/projects/<project>
```

Set `SOCK_RUNNER_AGENT=codex`, `SOCK_RUNNER_PERMISSION`, `SOCK_RUNNER_MODEL` or `SOCK_RUNNER_SESSION` in the environment before `install` to change the defaults. To run it by hand instead:

```sh
export SOCK_TOKEN=sock_pt_...        # the runner's own puppet, no other consumer
export SOCK_RUNNER_AGENT=claude      # or codex
export SOCK_RUNNER_WORKDIR=/path/to/project
sock listen --exec /path/to/runner/sock-runner
```

Then open `<project> on <host>` in the app and send `reply with the word pong`. Logs are in `~/.local/state/sock-integrations/runner/*/runner.log`, one line per message, never the text. Every setting and exit code is in `runner/README.md`.

## sock-bridge: Claude Managed Agents

`runner/sock-bridge` carries one reply into a Managed Agents session and the answer back, with tool confirmations brokered to you in the same conversation. Needs `sock`, `curl` and `jq`.

```sh
export SOCK_TOKEN=sock_pt_...             # the bridge's own puppet, no other consumer
export ANTHROPIC_API_KEY=...
export SOCK_BRIDGE_SESSION=sesn_...       # one session for every conversation
sock listen --exec /path/to/runner/sock-bridge
```

Or a session per conversation, created on first use:

```sh
export SOCK_BRIDGE_AGENT=agent_... SOCK_BRIDGE_ENVIRONMENT=env_...
export SOCK_BRIDGE_BUDGET=500             # optional cap per session, US cents
sock listen --exec /path/to/runner/sock-bridge
```

Sessions, budgets and confirmations are in `runner/README.md`.

## sock-http: any agent with an inbound API

`runner/sock-http` forwards each of your messages to an HTTP endpoint from a JSON template you write. The agent replies itself through `sock-mcp --outbound-only` as the same puppet. Needs `sock`, `curl` and `jq`.

```sh
export SOCK_TOKEN=sock_pt_...
export SOCK_HTTP_URL=https://agent.example/api/inbox
export SOCK_HTTP_HEADERS='Authorization: Bearer ...'   # optional, one "Name: value" per line
export SOCK_HTTP_BODY=/path/to/body.json              # template; {{prompt}} carries the message
sock listen --as-puppet --exec /path/to/runner/sock-http
```

OpenHands is the worked example in `runner/README.md`: the endpoint is the conversation's events URL and the body is `{"role": "user", "content": [{"type": "text", "text": "{{prompt}}"}], "run": true}`.

## Hosted connector: claude.ai and ChatGPT

No install here. The remote MCP server is `sock-mcpd` on `mcp.joinsock.com` behind OAuth; the client side (listing, skills, review package) is in `hosted/`, and the design is in `docs/specs/connector.md`.

- claude.ai: add `https://mcp.joinsock.com/mcp` as a custom connector and sign in to Sock when asked.
- ChatGPT: the directory listing is staged in `hosted/sock/` and submitted separately; see `hosted/README.md` for its gates.

Outbound only in v1: the hosted connection sends, and receiving needs a host that subscribes to `owner.reply` (ChatGPT dots), which the setup skill asks for.

## Rules

- One puppet per binding. With a machine key that is one per project per machine, made for you; never share a puppet token between agents or machines.
- One consumer per puppet. A session's server is its puppet's only consumer; the runner, bridge and forwarder each listen as their own puppet. Never call `sock_fetch` or `sock_ack` from an agent whose deliveries `sock listen` owns.
- The hub limits a puppet's posts to its owner: about 3 at once, then one every 10 minutes per puppet, and 20 an hour across all your puppets.

## Troubleshooting

Hooks never fail the agent. They log one line per failure to `${XDG_STATE_HOME:-~/.local/state}/sock-integrations/hook.log`. Read it first.

- `quiet: background`, `quiet: unknown session` or `quiet: throttled`: by design. Hooks message you only from an interactive session, at most once an hour per project (`docs/specs/integrations.md`, notify.sh rule 8).
- No token: the plugin option is unset and no machine key is in the config file, or `SOCK_TOKEN` is not exported where the hook runs.
- `sock` or `sock-mcp` is not on the `PATH` of the agent process, for example installed to `~/go/bin` which that `PATH` lacks.
- The server rejected the request: a revoked or mistyped token, or a URL that does not match the token's server.
- A reply never reaches a Claude Code session: the session has a puppet only once it messaged you, and its server logs `listening for the owner's replies` (in `~/Library/Caches/claude-cli-nodejs/*/mcp-logs-plugin-sock-sock/`). `no session inbox` means the Claude Code release changed the inbox (ADR 4); the reply waits at the hub.
- With a puppet token instead of a machine key, every session on the machine shares one puppet, so no session hears replies; use a machine key.
- Runner: `agent exit <n>`, `timeout` or `whoami failed` in `runner.log` name what went wrong. Under `codex`, a workdir that is not a git repository or trusted project is refused; see `runner/README.md`.

## Development

The tests are a Go module in `test/` that drives the real `sock` and `sock-mcp` binaries from the sibling checkouts `../cli` and `../mcp` against a stub hub. `make deps` builds them.

- `make deps`: build the sibling CLI and MCP binaries.
- `make install-bins`: install `sock` and `sock-mcp` to `$(go env GOPATH)/bin`.
- `make build` (default): build the siblings, validate the plugin and marketplace manifests with `claude plugin validate --strict` (skipped with a message when `claude` is absent), `go vet` the tests.
- `make test`: run the Go tests with `-race`.
- `make lint`: `shellcheck` and `golangci-lint`.
- `make check`: build, lint and test. CI runs the same target.
- `make install`: run `install-bins`, add this directory as a local marketplace and install the plugin.
- `make redeploy`: update the marketplace and the installed plugin.
- `make smoke`: send one message each through the CLI, `notify.sh` and `sock-mcp`. Needs a running server, `SOCK_URL` and a puppet `SOCK_TOKEN`. Not part of `make check`.
- `make e2e-dev`: zero-config puppets end to end against a local dev hub, with nobody signing in (`scripts/e2e-dev.sh`). Not part of `make check`.

Manual checks per release: the plugin loads in Claude Code and the `sock` skill and hooks appear; a reply from the app reaches an idle interactive session; Codex trusts and runs the hooks.

Design records are in `docs/adr/` and the specs in `docs/specs/`. See [CONTRIBUTING.md](CONTRIBUTING.md) for how to make a change and [SECURITY.md](SECURITY.md) for reporting a vulnerability.

## License

MIT, copyright Aeryx Industries LLC ([LICENSE](LICENSE)).
