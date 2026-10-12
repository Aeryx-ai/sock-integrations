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
token=${SOCK_TOKEN:-${CLAUDE_PLUGIN_OPTION_SOCK_TOKEN:-}}

log_line() {
	local msg=${1//$'\n'/ }
	if [ -n "$token" ]; then
		msg=${msg//"$token"/[redacted]}
	fi
	(
		umask 077
		mkdir -p "$state_dir" || exit 0
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

project=$(basename "${CLAUDE_PROJECT_DIR:-$PWD}" | LC_ALL=C tr -d '[:cntrl:]' | cut -c1-100)
text="$project: $phrase"

export SOCK_TOKEN=$token
url=${SOCK_URL:-${CLAUDE_PLUGIN_OPTION_SOCK_URL:-}}
if [ -n "$url" ]; then
	export SOCK_URL=$url
fi

errf=$(mktemp "${TMPDIR:-/tmp}/sock-hook.XXXXXX") || {
	log_line "mktemp failed"
	exit 0
}
trap 'rm -f "$errf"' EXIT

sock send -- "$text" >/dev/null 2>"$errf" </dev/null &
pid=$!
(
	sleep "${SOCK_HOOK_TIMEOUT:-10}"
	kill "$pid" 2>/dev/null
) >/dev/null 2>&1 &
watchdog=$!

wait "$pid"
rc=$?
pkill -P "$watchdog" 2>/dev/null
kill "$watchdog" 2>/dev/null

if [ "$rc" -ne 0 ]; then
	log_line "sock send exit $rc: $(head -c 300 "$errf")"
fi
exit 0
