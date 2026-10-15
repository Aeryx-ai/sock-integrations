# Set up a sock agent on a Mac

There are two ways for an agent to hear you. Use the runner for chat: it answers from a small, fresh or resumed session per project, keeps running after a reboot or logout, and never touches a long-lived working session. Use the channel only while you are sitting at an interactive session.

## Runner (default)

1. Install the binaries and the plugin. In this repository:

   ```sh
   make install
   ```

   `sock`, `sock-mcp`, `jq` and `claude` must be on `PATH`. The service copies the `PATH` of the shell that installs it.

2. Sign in once:

   ```sh
   sock login
   ```

3. Create a machine key. Keep it in the sock config file and set it as the plugin's key, so the runner and the agent's tools resolve the same project puppet:

   ```sh
   key=$(sock machine-key create)
   printf '%s' "$key" | sock machine-key use
   printf '%s\n' "$key"   # paste this into the next command's prompt
   claude plugin configure sock@sock-integrations
   unset key
   ```

   Each project gets its own puppet, named `<project> on <host>`.

4. Start the runner for a project. Run this once per project:

   ```sh
   runner/sock-runner-service install ~/projects/<project>
   ```

   This installs a launchd agent that restarts itself and starts at login. The first message starts a `claude -p` session for the project and later messages resume it. The defaults are `SOCK_RUNNER_PERMISSION=default` and `SOCK_RUNNER_AGENT=claude`; set either before `install` to change them. Requires a `sock` with `listen --as-puppet` (integrations-795). The installer refuses an older one.

5. Check it. In the sock app, open `<project> on <host>` and send `reply with the word pong`. A reply should arrive. If none does:

   ```sh
   tail ~/.local/state/sock-integrations/runner/*/runner.log ~/.local/state/sock-integrations/runner/*/listen.log
   ```

   `ran` means the agent finished. `agent exit <n>`, `timeout` or `whoami failed` name what went wrong.

Remove it with `runner/sock-runner-service uninstall ~/projects/<project>`.

## Channel (interactive, while you are at the session)

Start Claude Code in the project with the development channel and accept the development channels dialog it shows:

```sh
claude --dangerously-load-development-channels plugin:sock@sock-integrations
```

This path is read from the Claude Code 2.1.300 binary and has not been confirmed live yet. Claude Code registers a development channel only when you accept that dialog. A headless session (`-p`) ignores the flag. A dialog that is dismissed leaves the session without the channel, and its log says `not in --channels list`. Confirm with a fresh interactive session. After accepting, this should print `Channel notifications registered`:

```sh
grep -h 'Channel notif' "$(ls -t ~/Library/Caches/claude-cli-nodejs/*/mcp-logs-plugin-sock-sock/*.jsonl | head -1)"
```

A Team or Enterprise org must also set `channelsEnabled: true` in managed settings. Otherwise the log says `channels not enabled by org policy`.

## Rules

- One consumer per project puppet: the runner service or an interactive session that accepted the channel, never both. Both would take the same messages. An interactive session without the channel is fine beside the runner.
- Run chat through the runner, not through a long-lived session. A session carrying a large context answers slowly. The runner's session holds only this project's chat.
- The hub limits a puppet's posts to its owner (ADR 20 in sock-contract): about 3 replies at once, then one every 10 minutes per puppet, and 20 an hour across all your puppets. A busy chat hits this.
