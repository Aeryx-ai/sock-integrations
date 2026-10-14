# Sock for Codex

Plugin route (preview; see the Codex plugin section of `docs/specs/integrations.md`): `codex plugin marketplace add aeryx-ai/sock-integrations`, then `codex plugin add sock@sock-integrations` (or pick sock in `/plugins`), and review the hooks with `/hooks`. It installs the hooks and the skill with no path substitution, replacing steps 4 and 5 below. It ships no MCP server: a plugin's server gets no `SOCK_TOKEN`, so steps 1 to 3 stay and `config.toml` registers `sock-mcp`.

Outbound only: hooks message you when Codex needs permission or finishes a turn, and the model can call `sock_send`. No inbound in v1. The token is forwarded from your shell and never written to config.

1. Put `sock` and `sock-mcp` on `PATH` (`make install-bins`).
2. Mint a puppet for this machine in the sock app, then `export SOCK_TOKEN=sock_pt_...` only in a launcher or wrapper script that starts Codex, not in a global shell rc file. Claude Code's Bash tool inherits its parent's environment, so a token exported globally is readable by a model running there. Optionally set `SOCK_URL` in the same wrapper.
3. Append `codex/config.toml` to `~/.codex/config.toml`. This registers the `sock` MCP server and forwards `SOCK_TOKEN` and `SOCK_URL` from your shell.
4. Install the hooks. `codex/hooks.json` contains the literal text `/ABSOLUTE/PATH/TO/sock-integrations`. It is a substitution token, not a working path: replace every occurrence with the absolute path of this checkout. Codex hook handlers take no separate args field, so each `command` carries its arguments inline, and the Codex hooks docs (https://developers.openai.com/codex/hooks, served at https://learn.chatgpt.com/docs/hooks) do not state whether `command` goes through a shell or how quoting is handled. Their examples quote paths inside the string, which implies shell parsing. So:
   - Path rule: the checkout path must contain no spaces. If it does, clone or symlink the checkout to a path without spaces, or the command will break.
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
