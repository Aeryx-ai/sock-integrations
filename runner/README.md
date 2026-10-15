# sock-runner

The command `sock listen --exec` runs once per message. It resumes an agent's session headlessly with the owner's message as the prompt. The spec is `docs/specs/integrations.md`, "Headless inbound: sock-runner"; this is how to run it.

Needs `sock`, `jq` and the agent (`claude` or `codex`) on `PATH`.

```sh
export SOCK_TOKEN=sock_pt_...        # the runner's own puppet, no other consumer
export SOCK_RUNNER_AGENT=claude      # or codex
# SOCK_RUNNER_SESSION: optional for claude (the runner makes and keeps its own session), required for codex
export SOCK_RUNNER_WORKDIR=/path/to/project
export SOCK_RUNNER_PERMISSION=default # claude: --permission-mode; codex: --sandbox (read-only, workspace-write)
export SOCK_RUNNER_MODEL=haiku       # optional: a small fast model answers a chat quickly
sock listen --exec /path/to/runner/sock-runner
```

- The owner sees the puppet working from the moment a message passes the owner gate (`sock activity`, refreshed every 10 s, idle at exit) until it replies.
- Only the owner's messages run the agent; anything else is acknowledged and dropped. `bypassPermissions` (claude) and `danger-full-access` (codex) also need `SOCK_RUNNER_ALLOW_BYPASS=1`.
- Exit 0 acknowledges, 1 retries (`sock listen` tries five times), 2 is a configuration error and runs nothing. With sock-mcp v0.3.4 or later the runner checks the reply actually posted (`sock-mcp replied`): a run that replied is done even if the agent then failed, and an agent that exits 0 without replying is retried once, then given up. The log is `runner.log` in `SOCK_RUNNER_STATE` (default `~/.local/state/sock-integrations`), one line per message, never the text.
- The agent replies with `sock_send` through sock-mcp. Run sock-mcp for this puppet without the channel, and call neither `sock_fetch` nor `sock_ack`: `sock listen` is its one consumer.
- Under `codex` the agent's environment keeps `SOCK_TOKEN` and `SOCK_URL` (its sock-mcp needs them); under `claude` it does not.

- `sock listen --exec` runs the command through `/bin/sh -c`: the runner's path must have no spaces or shell metacharacters.
- One `SOCK_RUNNER_STATE` per runner. Runners sharing a directory share one lock, one ledger of finished message ids and one log.
- The agent's own session transcript keeps the owner's text for as long as the transcript lives; the runner's prompt file is private and removed on exit.
- `claude` runs with `--allowedTools` for the three sending sock tools, `--disallowedTools` for `sock_fetch` and `sock_ack`, and `--permission-prompts none`, so `sock_send` works under `default`, `dontAsk` and `acceptEdits` (checked live with claude 2.1.296; `plan` is refused: it blocks `sock_send` even when allowed, and claude still exits 0); `codex` relies on `enabled_tools` and `default_tools_approval_mode = "approve"` in `codex/config.toml`, which also apply to interactive Codex sessions that merge it. Neither is confirmed against a live model yet.
- `auto` and `bypassPermissions` (claude) and `danger-full-access` (codex) need `SOCK_RUNNER_ALLOW_BYPASS=1`. A `SOCK_TOKEN` that is not a puppet's exits 2 and acknowledges nothing.

## Codex notes

- A fresh `CODEX_HOME` ignores `OPENAI_API_KEY`: run `codex login --with-api-key` once for the account the runner uses.
- `codex exec` refuses a workdir that is neither a git repository nor a trusted project (`Not inside a trusted directory and --skip-git-repo-check was not specified`). Point `SOCK_RUNNER_WORKDIR` at a git repository or trust the directory in `~/.codex/config.toml`. A failed run logs only `agent exit <n>` in `runner.log`, never the agent's output: to see the message, run the same `codex exec` in the workdir by hand.

# sock-bridge

The `sock listen --exec` command that carries one owner reply into a Claude Managed Agents session and the agent's answer back. The spec is `docs/specs/integrations.md`, "Managed Agents bridge: sock-bridge". Needs `sock`, `curl` and `jq` on `PATH`.

```sh
export SOCK_TOKEN=sock_pt_...             # the bridge's own puppet, no other consumer
export ANTHROPIC_API_KEY=...
export SOCK_BRIDGE_SESSION=sesn_...       # one existing session for every conversation
sock listen --exec /path/to/runner/sock-bridge
```

Or a session per conversation, created on first use:

```sh
export SOCK_BRIDGE_AGENT=agent_...        # instead of SOCK_BRIDGE_SESSION; both together exit 2
export SOCK_BRIDGE_ENVIRONMENT=env_...    # required with the agent
export SOCK_BRIDGE_AGENT_VERSION=3        # optional: pin a version, else the latest
export SOCK_BRIDGE_BUDGET=500             # optional: a cap per session in US cents, only settable at creation
```

- The map is `sessions` in `SOCK_BRIDGE_STATE` (default `~/.local/state/sock-integrations-bridge`), one `<conversation> <session>` line each. Losing it loses nothing: the bridge finds its sessions again by their metadata (`bridge_conversation`, and `bridge_puppet`, the puppet's account id, so two bridges on one agent never share a session).
- A session that is deleted, terminated or archived is replaced, and the owner is told `the agent's earlier session ended; this reply started a new one`.
- A session made for another agent, environment or pinned version is replaced too, and so is one whose budget is above `SOCK_BRIDGE_BUDGET`, or has none while it is set; the owner is told `the bridge configuration changed; this reply started a new session`. A budget within `SOCK_BRIDGE_BUDGET` fits, so raising the setting replaces nothing.
- A create the bridge cannot use (an id it refuses, or a session that does not fit the settings it was made from, at create or at a later check) writes `create.halt` in the state directory. From then on it creates and adopts nothing, exits 2 and tells the owner once: `the bridge cannot start a session that fits its configuration; check SOCK_BRIDGE_AGENT, SOCK_BRIDGE_ENVIRONMENT and SOCK_BRIDGE_BUDGET`. Changing any of those settings clears it, and so does deleting the file.
- A session at its budget pauses idle and refuses new replies (each exits 1). Raise its budget in the console, within `SOCK_BRIDGE_BUDGET`, to resume it; to go higher, raise `SOCK_BRIDGE_BUDGET` first.
- Every session is billed on its own. The bridge never archives or deletes one: archive them in the console.
- A tool call the agent's permission policy sends for confirmation (`always_ask`, or `auto` undecided) reaches the owner as `the agent wants to run <tool>: reply "approve <code>" or "deny <code> <optional reason>"`. Only that whole reply from the owner, in the same conversation, answers it; the code is never sent to the agent or logged, and the tool input is never shown. While a call waits, other replies are held and the owner is reminded of the codes. Custom tools, and calls the bridge did not see, are answered in the console.
- Exit 0 acknowledges, 1 retries, 2 is a configuration error. The log is `bridge.log` in the state directory, one line per message and per session step, never the text, the answer, the key or the token.
