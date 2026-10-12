#!/usr/bin/env bash
# Real-path check against a running sock server: the sock CLI, notify.sh and
# sock-mcp each deliver one message to the puppet's owner.
# Needs SOCK_URL and SOCK_TOKEN (a puppet token) in the environment.
set -eu
: "${SOCK_TOKEN:?set SOCK_TOKEN to a sock_pt_ token for a puppet on the target server}"
: "${SOCK_URL:?set SOCK_URL to the target server}"
here=$(cd "$(dirname "$0")/.." && pwd)
export PATH="$here/../cli/bin:$PATH"

echo "1/3 sock send"
sock send "smoke: cli"

echo "2/3 notify.sh"
CLAUDE_PROJECT_DIR=/x/smoke "$here/claude-code/hooks/notify.sh" "needs permission"
log=${XDG_STATE_HOME:-$HOME/.local/state}/sock-integrations/hook.log
if [ -f "$log" ] && [ "$(wc -l <"$log")" -gt 0 ]; then
	echo "hook log has entries: $log" >&2
	tail -n 3 "$log" >&2
fi

echo "3/3 sock-mcp sock_send"
printf '%s\n' \
	'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
	'{"jsonrpc":"2.0","method":"notifications/initialized"}' \
	'{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sock_send","arguments":{"text":"smoke: mcp"}}}' |
	"$here/../mcp/bin/sock-mcp" | grep -q '"id":2' || { echo "sock-mcp sock_send got no response" >&2; exit 1; }
echo "ok: check your owner's sock for three messages"
