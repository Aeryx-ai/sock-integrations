# sock-runner

The command `sock listen --exec` runs once per message. It resumes an agent's session headlessly with the owner's message as the prompt. The spec is `docs/specs/integrations.md`, "Headless inbound: sock-runner"; this is how to run it.

Needs `sock`, `jq` and the agent (`claude` or `codex`) on `PATH`.

```sh
export SOCK_TOKEN=sock_pt_...        # the runner's own puppet, no other consumer
export SOCK_RUNNER_AGENT=claude      # or codex
export SOCK_RUNNER_SESSION=<a session id nobody has open>
export SOCK_RUNNER_WORKDIR=/path/to/project
export SOCK_RUNNER_PERMISSION=default # claude: --permission-mode; codex: --sandbox (read-only, workspace-write)
sock listen --exec /path/to/runner/sock-runner
```

- Only the owner's messages run the agent; anything else is acknowledged and dropped. `bypassPermissions` (claude) and `danger-full-access` (codex) also need `SOCK_RUNNER_ALLOW_BYPASS=1`.
- Exit 0 acknowledges, 1 retries (`sock listen` tries five times), 2 is a configuration error and runs nothing. The log is `runner.log` in `SOCK_RUNNER_STATE` (default `~/.local/state/sock-integrations`), one line per message, never the text.
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
