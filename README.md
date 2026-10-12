# sock-integrations

Integrations that let coding agents talk to a sock server through a puppet account: a Claude Code plugin (skill, hooks, MCP server), Codex config and an Agent SDK example. The plugin shells out to the `sock` CLI and the `sock-mcp` server, which live in sibling repos.

## Claude Code

```
/plugin marketplace add guygrigsby/sock-integrations
/plugin install sock@sock-integrations
```

That line works once the repo is pushed. Until then, from a local checkout run `make install`.

Set the puppet token when the plugin prompts for it. It is stored in the plugin's secure userConfig. If the prompt never appeared or you need to change the value, run `claude plugin configure sock@sock-integrations`. `sock` and `sock-mcp` must both be on the `PATH` of the Claude Code process. `make install-bins` installs them to `$(go env GOPATH)/bin` (usually `~/go/bin`), which Claude Code's `PATH` often lacks.

Inbound messages (channel) need the development channel flag:

```
claude --dangerously-load-development-channels plugin:sock@sock-integrations
```

Channels are a research preview, work in interactive sessions only, and Team and Enterprise orgs must enable them.

## Codex

See `codex/install.md`.

## Agent SDK

See `agent-sdk/example.ts`.

## Rules

- One puppet per binding. Do not share a puppet token between agents or machines.
- One consumer per puppet. Channel mode and a `sock_fetch`/`sock_ack` caller consume the same deliveries, so run only one against a given puppet.

## Troubleshooting

Hooks never fail the agent. They log one line per failure to `${XDG_STATE_HOME:-~/.local/state}/sock-integrations/hook.log`. Read it first. Common first-run causes:

- No token: the plugin option is unset, or `SOCK_TOKEN` is not exported where the hook runs.
- `sock` or `sock-mcp` is not on the `PATH` of the Claude Code process, for example installed to `~/go/bin` which that `PATH` lacks.
- The server rejected the request: a revoked or mistyped token, or a URL that does not match the token's server.

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
- Channel delivers an inbound message with the development flag.
- Codex trusts and runs the hook.
