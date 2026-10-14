# Sock for Codex

Plugin route (recommended; see the Codex plugin section of `docs/specs/integrations.md`):

1. Put `sock` and `sock-mcp` on `PATH` (`make install-bins`). The plugin route needs sock-mcp v0.2.3 or later: v0.2.2 named a machine-key puppet after the plugin cache directory.
2. `sock login`, then `sock puppets use <puppet id>` for the puppet Codex should send as. It writes the puppet token to `~/.config/sock/config.toml` (mode 0600), where the plugin's `sock-mcp` reads it: a plugin's server never sees your shell's `SOCK_TOKEN`.
3. `codex plugin marketplace add aeryx-ai/sock-integrations`, then `codex plugin add sock@sock-integrations` (or pick sock in `/plugins`).
4. Run `/hooks` in Codex and trust both hooks. They need no `SOCK_TOKEN`: they send with `sock send --as-puppet`, which resolves the same credential the plugin's server does, so both send as one puppet. This needs sock-cli v0.2.5 or later. They ignore a `SOCK_TOKEN` in the environment Codex starts in, as the plugin's server does.

Do not also append `codex/config.toml`: two servers named `sock` would clash. The plugin's server starts in the plugin's own directory, not your project, so it sends as one puppet for every project. For a puppet per project (`<repo> on <host>`), use the manual route below with a machine key as `SOCK_TOKEN` (`sock machine-key create --label "$(hostname -s)"`): a server registered in `config.toml` starts in the session's project directory. The manual route is also for anyone who wants no token in a file.

Outbound only: hooks message you when Codex needs permission and, at the end of each turn, with the turn's trailing question or decision when it has one (`finished a turn` as well with `SOCK_NOTIFY_ON_STOP=1`), and the model can call `sock_send`. No inbound in v1. The token is forwarded from your shell and never written to config.

Hooks send only from the TUI or the Codex app, at most once an hour per project: `codex exec` and subagents never message you, and a `sock_send` reply is never held back (`docs/specs/integrations.md`, notify.sh rule 8).

1. Put `sock` and `sock-mcp` on `PATH` (`make install-bins`). The plugin route needs sock-mcp v0.2.3 or later: v0.2.2 named a machine-key puppet after the plugin cache directory.
2. Mint a puppet for this machine in the sock app, then `export SOCK_TOKEN=sock_pt_...` only in a launcher or wrapper script that starts Codex, not in a global shell rc file. Claude Code's Bash tool inherits its parent's environment, so a token exported globally is readable by a model running there. Optionally set `SOCK_URL` in the same wrapper.
3. Append `codex/config.toml` to `~/.codex/config.toml`. This registers the `sock` MCP server and forwards `SOCK_TOKEN` and `SOCK_URL` from your shell.
4. Install the hooks. `codex/hooks.json` contains the literal text `/ABSOLUTE/PATH/TO/sock-integrations`. It is a substitution token, not a working path: replace every occurrence with the absolute path of this checkout. Codex hook handlers take no separate args field, so each `command` carries its arguments inline, and the Codex hooks docs (https://developers.openai.com/codex/hooks, served at https://learn.chatgpt.com/docs/hooks) do not state whether `command` goes through a shell or how quoting is handled. Their examples quote paths inside the string, which implies shell parsing. So:
   - Path rule: the checkout path must contain no spaces and none of `$`, a backtick, `"` or `\`. The command string is likely run through a shell, which would expand or split them. If it does, clone or symlink the checkout to a plain path, or the command will break.
   - If you insist on a path with spaces, quote it inside the JSON string, with the quotes escaped: `"command": "\"/Users/me/my projects/sock-integrations/claude-code/hooks/notify.sh\" \"needs permission\""`. This relies on shell quoting that the docs do not promise; verify with `/hooks` and a real permission prompt.
   - Run every command in this step in one shell session: `$hooks` lives in that shell, so a new terminal loses it.
   - Substitute with awk, which treats `#`, `&` and `\` in the path literally (run from the repo root; `index`/`substr` do plain text matching, and the path travels in the environment so awk does not interpret backslashes):

     ```sh
     mkdir -p ~/.codex
     hooks=$(mktemp)
     SOCK_ROOT="$PWD" awk 'BEGIN { tok = "/ABSOLUTE/PATH/TO/sock-integrations"; r = ENVIRON["SOCK_ROOT"] }
       { out = ""; while ((i = index($0, tok)) > 0) { out = out substr($0, 1, i - 1) r; $0 = substr($0, i + length(tok)) } print out $0 }' \
       codex/hooks.json > "$hooks"
     ```

     The path is inserted verbatim, so a path containing a double quote or a backslash would need JSON escaping; use a plain path.
   - First install, no `~/.codex/hooks.json` yet: `cp "$hooks" ~/.codex/hooks.json`.
   - Existing `~/.codex/hooks.json`: merge by hand. Add the `PermissionRequest` and `Stop` entries from the file at `$hooks` to the arrays under `hooks` (create the array if the event is absent). All matching hooks run, so merging is additive. Remove the temp file afterwards: `rm -f "$hooks"`.
5. Run `/hooks` in Codex, then review and trust both hooks. Codex skips an untrusted hook, and editing `notify.sh` requires trusting again.
6. Optional: `export SOCK_NOTIFY_ON_STOP=1` to get a message at the end of each turn.

## Headless replies (sock-runner)

`codex/config.toml` also sets, for the sock MCP server, `enabled_tools` (only `sock_whoami`, `sock_list_conversations` and `sock_send`: `sock_fetch` and `sock_ack` consume the deliveries `sock listen` owns, so Codex never sees them) and `default_tools_approval_mode = "approve"`, so a headless run started by `runner/sock-runner` can reply. Both keys apply to every Codex session that merges this file, interactive ones included: those sessions lose `sock_fetch` and `sock_ack` and send without a prompt. Drop the approval line if you do not want that and do not use the runner.

`sock listen --exec` runs the command through `/bin/sh -c`, so the path to `runner/sock-runner` must contain no spaces or shell metacharacters. See `runner/README.md`.
