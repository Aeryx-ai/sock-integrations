#!/usr/bin/env bash
# Real-path check against a running sock server: the sock CLI, notify.sh and
# sock-mcp each deliver one message to the puppet's owner.
# Needs SOCK_URL and SOCK_TOKEN (a puppet token) in the environment.
# SOCK_BIN and SOCK_MCP_BIN override the binaries (used to test this script
# against stand-ins; leave unset for a real run). Step 3 is test/mcp_test.go.
set -euo pipefail
: "${SOCK_TOKEN:?set SOCK_TOKEN to a sock_pt_ token for a puppet on the target server}"
: "${SOCK_URL:?set SOCK_URL to the target server}"
here=$(cd "$(dirname "$0")/.." && pwd)
export PATH="$here/../cli/bin:$PATH"
sock_bin=${SOCK_BIN:-sock}

echo "1/3 sock send"
"$sock_bin" send "smoke: cli"

echo "2/3 notify.sh"
# As an attended Claude Code session, with a state dir of its own so the hourly
# throttle of an earlier run cannot hold this send back.
state=$(mktemp -d)
trap 'rm -rf "$state"' EXIT
log=$state/sock-integrations/hook.log
printf '{"session_id":"smoke"}' | XDG_STATE_HOME=$state CLAUDE_CODE_SESSION_ATTENDED=1 CLAUDE_CODE_SESSION_ID=smoke CLAUDE_PROJECT_DIR=/x/smoke \
	"$here/claude-code/hooks/notify.sh" --permission
if [ -s "$log" ]; then
	echo "hook log grew ($log):" >&2
	cat "$log" >&2
	exit 1
fi

echo "3/3 sock-mcp sock_send"
(cd "$here/test" && SOCK_SMOKE=1 go test -count=1 -run '^TestMCPSend$' ./...)
echo "ok: all three sends were accepted. Step 2 cannot prove delivery; the owner's inbox must show three messages"
