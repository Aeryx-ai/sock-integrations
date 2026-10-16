# Set up a sock agent on a Mac

There are two ways for an agent to hear you. Use the runner for chat: it answers from a small, fresh or resumed session per project, keeps running after a reboot or logout, and never touches a long-lived working session. An interactive session hears replies sent to its own puppet while it runs.

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

3. Create a machine key and keep it in the sock config file. Leave the plugin's `sock_token` empty: the runner, the agent's tools and the hooks then resolve the same project puppet from that file.

   ```sh
   sock machine-key create | sock machine-key use
   ```

   Each project gets its own puppet, named `<project> on <host>`. An old `sock_token` in the plugin overrides the key and makes every session one puppet: clear it with `claude plugin configure sock@sock-integrations`.

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

## Interactive sessions

Nothing to set up: with the plugin and a machine key, a reply in a session's thread appears in that session ([ADR 4](adr/0004-claude-replies-enter-through-the-session-inbox.md)). The session's server logs `listening for the owner's replies` once the session has messaged you:

```sh
grep -h 'listening for the owner' ~/Library/Caches/claude-cli-nodejs/*/mcp-logs-plugin-sock-sock/*.jsonl | tail -3
```

## Rules

- The runner listens as the project's puppet and each interactive session as its own, so they never take each other's messages.
- Run chat through the runner, not through a long-lived session. A session carrying a large context answers slowly. The runner's session holds only this project's chat.
- The hub limits a puppet's posts to its owner (ADR 20 in sock-contract): about 3 replies at once, then one every 10 minutes per puppet, and 20 an hour across all your puppets. A busy chat hits this.
