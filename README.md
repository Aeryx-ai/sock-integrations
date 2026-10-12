# sock-integrations

Integrations that let coding agents talk to a sock server through a puppet account: a Claude Code plugin (skill, hooks, MCP server), Codex config and an Agent SDK example. The plugin shells out to the `sock` CLI and the `sock-mcp` server, which live in sibling repos.

## Claude Code

```
/plugin marketplace add guygrigsby/sock-integrations
/plugin install sock@sock-integrations
```

Set the puppet token when the plugin prompts for it. It is stored in the plugin's secure userConfig. `sock` and `sock-mcp` must both be on `PATH`. A missing `sock-mcp` fails the MCP server silently.

Inbound messages (channel) need the development channel flag:

```
claude --dangerously-load-development-channels plugin:sock@sock-integrations
```

Channels are a research preview, work in interactive sessions only (the flag is ignored under `-p`), and Team and Enterprise orgs must enable them.

## Codex

See `codex/install.md`.

## Agent SDK

See `agent-sdk/example.ts`.

## Rules

- One puppet per binding. Do not share a puppet token between agents or machines.
- One consumer per puppet. Channel mode and a fetch/ack hook consume the same deliveries, so run only one against a given puppet.

## Make targets

- `make deps`: build the sibling CLI and MCP binaries.
- `make build` (default): build `../cli` and `../mcp`, validate the plugin and marketplace manifests, `go vet` the tests.
- `make test`: run the Go tests with `-race`.
- `make lint`: `shellcheck` and `golangci-lint`.
- `make check`: build, lint and test.
- `make install`: add this directory as a local marketplace and install the plugin.
- `make redeploy`: update the marketplace and the installed plugin.
- `make smoke`: send one message each through the CLI, `notify.sh` and `sock-mcp`. Needs a running server, `SOCK_URL` and a puppet `SOCK_TOKEN`, and someone to look at the owner's inbox for three messages. Not part of `make check`.

## Manual checks per release

- Plugin loads in Claude Code and the `sock` skill and hooks appear.
- Channel delivers an inbound message with the development flag.
- Codex trusts and runs the hook.
