# sock-integrations

Integrations that let coding agents talk to a sock server through a puppet account: a Claude Code plugin (skill, hooks, MCP server), Codex config and an Agent SDK example. The plugin shells out to the `sock` CLI and the `sock-mcp` server, which live in sibling repos.

## Claude Code

```
/plugin marketplace add aeryx-ai/sock-integrations
/plugin install sock@sock-integrations
```

Leave the plugin's `sock_token` empty. The plugin then uses the machine key in the sock config file.

- A machine key, the default: run `sock login` once, then `sock machine-key create | sock machine-key use`. Every session then gets its own puppet the first time it messages you, named `<project> <first 8 of the session id> on <host>` until you `/rename` the session, then for its title. Reply in that puppet's thread and the reply appears in that session, idle or busy, with no flag ([ADR 4](docs/adr/0004-claude-replies-enter-through-the-session-inbox.md)). A session's puppet is archived when the session ends and comes back when it is resumed. Tokens are cached in `${XDG_STATE_HOME:-~/.local/state}/sock-integrations/puppets` (mode 600). The key itself can only make and erase the puppets it made: it cannot read or send your messages. Erase a project's puppet from the app like any other puppet.
- A puppet token (`sock_pt_`) set as the plugin's `sock_token`: every session on the machine shares that one puppet, and replies race between sessions. If the prompt never appeared or you need to change the value, run `claude plugin configure sock@sock-integrations`. `sock` and `sock-mcp` must both be on the `PATH` of the Claude Code process. `make install-bins` installs them to `$(go env GOPATH)/bin` (usually `~/go/bin`), which Claude Code's `PATH` often lacks.

Step-by-step setup on a Mac and the runner service (chat with a headless session per project) are in `docs/setup-mac.md`.

## Codex

See `codex/install.md`.

## Agent SDK

See `agent-sdk/example.ts`.

## Rules

- One puppet per binding. With a machine key that is one per project per machine, made for you; do not share a puppet token between agents or machines.
- One consumer per puppet. A session's server is its puppet's only consumer; the runner service listens as the project's puppet, a different one.

## Troubleshooting

Hooks never fail the agent. They log one line per failure to `${XDG_STATE_HOME:-~/.local/state}/sock-integrations/hook.log`. Read it first. Common first-run causes:

- `quiet: background`, `quiet: unknown session` or `quiet: throttled`: by design. Hooks message you only from an interactive session, at most once an hour per project; `-p`, SDK, background and subagent sessions never do, and replies through `sock_send` are never held back (`docs/specs/integrations.md`, notify.sh rule 8).
- No token: the plugin option is unset, or `SOCK_TOKEN` is not exported where the hook runs.
- `sock` or `sock-mcp` is not on the `PATH` of the Claude Code process, for example installed to `~/go/bin` which that `PATH` lacks.
- The server rejected the request: a revoked or mistyped token, or a URL that does not match the token's server.
- A reply never reaches the session: the session has a puppet only once it messaged you, and its server logs `listening for the owner's replies` (in `~/Library/Caches/claude-cli-nodejs/*/mcp-logs-plugin-sock-sock/`). `no session inbox` means the Claude Code release changed the inbox (ADR 4); the reply waits at the hub.
- With a puppet token instead of a machine key, every session on the machine shares one puppet and replies race between them.

## Make targets

- `make deps`: build the sibling CLI and MCP binaries. The tests also need `../contract` (the generated API module) next to `../cli` and `../mcp`.
- `make install-bins`: install `sock` and `sock-mcp` to `$(go env GOPATH)/bin`, which must be on `PATH`, including the `PATH` of the Claude Code process.
- `make build` (default): build `../cli` and `../mcp`, validate the plugin and marketplace manifests with `claude plugin validate --strict` (skipped with a message when `claude` is absent), `go vet` the tests.
- `make test`: run the Go tests with `-race`.
- `make lint`: `shellcheck` and `golangci-lint`.
- `make check`: build, lint and test.
- `make install`: run `install-bins`, add this directory as a local marketplace and install the plugin.
- `make redeploy`: update the marketplace and the installed plugin.
- `make smoke`: send one message each through the CLI, `notify.sh` and `sock-mcp` (the last is `TestMCPSend` with `SOCK_SMOKE=1`). Needs a running server, `SOCK_URL` and a puppet `SOCK_TOKEN`, and someone to look at the owner's inbox for three messages. Not part of `make check`.

## Manual checks per release

- Plugin loads in Claude Code and the `sock` skill and hooks appear.
- A reply from the app reaches an idle interactive session without any flag.
- Codex trusts and runs the hook.
