# Sock for Codex

Outbound only: hooks message you when Codex needs permission or finishes a turn, and the model can call `sock_send`. No inbound in v1. The token is forwarded from your shell and never written to config.

1. Put `sock` and `sock-mcp` on `PATH` (`make install-bins`).
2. Mint a puppet for this machine in the sock app, then `export SOCK_TOKEN=sock_pt_...` in the shell that starts Codex. Optionally set `SOCK_URL` too.
3. Append `codex/config.toml` to `~/.codex/config.toml`. This registers the `sock` MCP server and forwards `SOCK_TOKEN` and `SOCK_URL` from your shell.
4. Merge `codex/hooks.json` into `~/.codex/hooks.json`. The file contains the literal text `/ABSOLUTE/PATH/TO/sock-integrations`. It is a substitution token, not a working path: replace every occurrence with the absolute path of this checkout (for example `sed -i.bak "s#/ABSOLUTE/PATH/TO/sock-integrations#$PWD#g"` run from the repo root, on a copy). Codex hook handlers take no separate args field, so each `command` carries its arguments inline. All matching hooks run, so merging is additive.
5. Run `/hooks` in Codex, then review and trust both hooks. Codex skips an untrusted hook, and editing `notify.sh` requires trusting again.
6. Optional: `export SOCK_NOTIFY_ON_STOP=1` to get a message at the end of each turn.
