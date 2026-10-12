#!/usr/bin/env bash
# Real-path check against a running sock server: the sock CLI, notify.sh and
# sock-mcp each deliver one message to the puppet's owner.
# Needs SOCK_URL and SOCK_TOKEN (a puppet token) in the environment.
# SOCK_BIN and SOCK_MCP_BIN override the binaries (used to test this script
# against stand-ins; leave unset for a real run).
set -euo pipefail
: "${SOCK_TOKEN:?set SOCK_TOKEN to a sock_pt_ token for a puppet on the target server}"
: "${SOCK_URL:?set SOCK_URL to the target server}"
here=$(cd "$(dirname "$0")/.." && pwd)
export PATH="$here/../cli/bin:$PATH"
sock_bin=${SOCK_BIN:-sock}
mcp_bin=${SOCK_MCP_BIN:-$here/../mcp/bin/sock-mcp}

echo "1/3 sock send"
"$sock_bin" send "smoke: cli"

echo "2/3 notify.sh"
log=${XDG_STATE_HOME:-$HOME/.local/state}/sock-integrations/hook.log
before=0
[ -f "$log" ] && before=$(wc -l <"$log")
CLAUDE_PROJECT_DIR=/x/smoke "$here/claude-code/hooks/notify.sh" "needs permission"
after=0
[ -f "$log" ] && after=$(wc -l <"$log")
if [ "$after" -gt "$before" ]; then
	echo "hook log grew ($log):" >&2
	tail -n "$((after - before))" "$log" >&2
fi

echo "3/3 sock-mcp sock_send"
rc=0
out=$(printf '%s\n' \
	'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
	'{"jsonrpc":"2.0","method":"notifications/initialized"}' \
	'{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sock_send","arguments":{"text":"smoke: mcp"}}}' |
	"$mcp_bin") || rc=$?
out=${out//"$SOCK_TOKEN"/[redacted]}
if [ "$rc" -ne 0 ]; then
	echo "sock-mcp exited $rc: $out" >&2
	exit 1
fi
resp=$(printf '%s\n' "$out" | grep '"id":2' || true)
if [ -z "$resp" ]; then
	echo "sock-mcp produced no id 2 response: $out" >&2
	exit 1
fi
case $resp in
*'"isError":true'*)
	echo "sock-mcp sock_send failed: $resp" >&2
	exit 1
	;;
esac
echo "ok: sock-mcp accepted the send. Step 2 cannot prove delivery; the owner's inbox must show three messages"
