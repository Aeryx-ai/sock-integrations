#!/usr/bin/env bash
# Sends one fixed-phrase message to the puppet's owner through the sock CLI.
# Usage: notify.sh [--stop] <phrase>
# Always exits 0: a hook must never block or fail the agent.
set -u

stop_gated=0
if [ "${1:-}" = "--stop" ]; then
	stop_gated=1
	shift
fi
phrase=${1:-}

state_dir=${XDG_STATE_HOME:-${HOME:-/nonexistent}/.local/state}/sock-integrations
log=$state_dir/hook.log
# The token and the URL come from the same source, so a token never goes to a
# server chosen by a different source. The plugin option wins: .mcp.json gives
# sock-mcp the plugin token, and an ambient SOCK_TOKEN exported for another
# platform (Codex) must not make these hooks post as that puppet.
if [ -n "${CLAUDE_PLUGIN_OPTION_SOCK_TOKEN:-}" ]; then
	token=$CLAUDE_PLUGIN_OPTION_SOCK_TOKEN
	url=${CLAUDE_PLUGIN_OPTION_SOCK_URL:-}
else
	token=${SOCK_TOKEN:-}
	url=${SOCK_URL:-}
fi

log_line() {
	# Strip every control character (escape sequences, CR, tab, newline) so
	# tool output cannot drive the terminal of whoever reads the log.
	local msg
	msg=$(printf '%s' "$1" | LC_ALL=C tr -d '[:cntrl:]')
	if [ -n "$token" ]; then
		msg=${msg//"$token"/[redacted]}
	fi
	(
		umask 077
		mkdir -p "$state_dir"
		if [ -f "$log" ] && [ "$(wc -c <"$log")" -gt 102400 ]; then
			: >"$log"
		fi
		printf '%s %s\n' "$(date -u +%FT%TZ)" "$msg" >>"$log"
	) 2>/dev/null
	return 0
}

# Drain the hook JSON without parsing it. Bounded so a held-open stdin cannot hang us.
IFS= read -r -t 2 -d '' _ || true

if [ "$stop_gated" = 1 ]; then
	case "${CLAUDE_PLUGIN_OPTION_NOTIFY_ON_STOP:-}:${SOCK_NOTIFY_ON_STOP:-}" in
	true:* | *:1) ;;
	*) exit 0 ;;
	esac
fi

[ -n "$phrase" ] || exit 0
if [ -z "$token" ]; then
	log_line "no token: set SOCK_TOKEN or the plugin's sock_token option"
	exit 0
fi
if ! command -v sock >/dev/null 2>&1; then
	log_line "sock not found on PATH"
	exit 0
fi

# Allowlist, in the C locale so multibyte input is dropped bytewise instead of cut mid-character.
project=$(basename "${CLAUDE_PROJECT_DIR:-$PWD}" | LC_ALL=C tr -cd 'A-Za-z0-9._ -' | LC_ALL=C cut -c1-100)
[ -n "$project" ] || project=project
text="$project: $phrase"

export SOCK_TOKEN=$token
# Never inherit a URL from another source. With no URL of our own, the CLI
# default or config file applies. An explicit --url beats a config file.
unset SOCK_URL
url_args=()
if [ -n "$url" ]; then
	url_args=(--url "$url")
fi

errf=$(mktemp "${TMPDIR:-/tmp}/sock-hook.XXXXXX") || {
	log_line "mktemp failed"
	exit 0
}
trap 'rm -f "$errf"' EXIT

timeout=${SOCK_HOOK_TIMEOUT:-10}
case $timeout in
'' | *[!0-9]*) timeout=10 ;;
*) [ "$timeout" -gt 0 ] || timeout=10 ;;
esac

sock ${url_args[@]+"${url_args[@]}"} send -- "$text" >/dev/null 2>"$errf" </dev/null &
pid=$!
(
	sleep "$timeout"
	kill "$pid" 2>/dev/null
) >/dev/null 2>&1 &
watchdog=$!
disown "$watchdog" 2>/dev/null # no "Terminated" job notice on stderr when we reap it below

wait "$pid"
rc=$?
kill "$watchdog" 2>/dev/null
pkill -P "$watchdog" 2>/dev/null

if [ "$rc" -ne 0 ]; then
	# Redact the whole captured stderr before cutting, so a token straddling the cut cannot leak a prefix.
	detail=$(head -c 65536 "$errf")
	if [ -n "$token" ]; then
		detail=${detail//"$token"/[redacted]}
	fi
	detail=$(printf '%s' "$detail" | head -c 300)
	log_line "sock send exit $rc: $detail"
fi
exit 0
