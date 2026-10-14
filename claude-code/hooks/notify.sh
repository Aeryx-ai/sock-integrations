#!/usr/bin/env bash
# Messages the puppet's owner through the sock CLI, saying what the session
# needs. Modes:
#   --permission   a Notification permission_prompt: forwards the notification
#                  text, which names the tool ("needs permission" without jq)
#   --stop         a Stop: records the ask in the last assistant message (the
#                  trailing question or decision, code blocks dropped) for
#                  --idle; sends nothing unless notify_on_stop is on
#   --stop --ask-now  the same, and sends the ask at once (hosts with no idle
#                  event, such as Codex)
#   --idle         a Notification idle_prompt: sends the recorded ask once; a
#                  session with nothing pending sends nothing, and the same ask
#                  is never reposted
#   <phrase>       sends "<project>: <phrase>" (the v1 shape, kept for callers)
# The owner asked for the ask itself in these messages (2026-10-13), which is
# why assistant text now leaves the machine; it is capped and stripped of code.
# Always exits 0: a hook must never block or fail the agent.
set -u

mode=
ask_now=0
phrase=
while [ $# -gt 0 ]; do
	case $1 in
	--permission | --idle | --stop) mode=${1#--} ;;
	--ask-now) ask_now=1 ;;
	*) phrase=$1 ;;
	esac
	shift
done

state_dir=${XDG_STATE_HOME:-${HOME:-/nonexistent}/.local/state}/sock-integrations
log=$state_dir/hook.log
asks=$state_dir/asks
# The token and the URL come from the same source, so a token never goes to a
# server chosen by a different source. The plugin option wins: .mcp.json gives
# sock-mcp the plugin token, and an ambient SOCK_TOKEN exported for another
# platform (Codex) must not make these hooks post as that puppet.
if [ -n "${CLAUDE_PLUGIN_OPTION_SOCK_TOKEN:-}" ]; then
	token=$CLAUDE_PLUGIN_OPTION_SOCK_TOKEN
	url=${CLAUDE_PLUGIN_OPTION_SOCK_URL:-}
	# An ambient access gate belongs to the env-token deployment, not this URL.
	unset SOCK_ACCESS_GATE
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

# Read the hook JSON, bounded so a held-open stdin cannot hang us.
input=
IFS= read -r -t 2 -d '' input || true

have_jq=0
if command -v jq >/dev/null 2>&1; then have_jq=1; fi

# field prints a string field of the hook JSON, or nothing.
field() {
	[ "$have_jq" = 1 ] || return 0
	printf '%s' "$input" | jq -r --arg k "$1" 'if (.[$k] | type) == "string" then .[$k] else empty end' 2>/dev/null
}

# Allowlist, in the C locale so multibyte input is dropped bytewise instead of cut mid-character.
project=$(basename "${CLAUDE_PROJECT_DIR:-$PWD}" | LC_ALL=C tr -cd 'A-Za-z0-9._ -' | LC_ALL=C cut -c1-100)
[ -n "$project" ] || project=project

# The session the ask belongs to: the hook's session_id, else the project.
session=$(field session_id | LC_ALL=C tr -cd 'A-Za-z0-9_-' | LC_ALL=C cut -c1-64)
[ -n "$session" ] || session=$(printf '%s' "${CLAUDE_PROJECT_DIR:-$PWD}" | LC_ALL=C tr -cd 'A-Za-z0-9_-' | LC_ALL=C cut -c1-64)
[ -n "$session" ] || session=project

# extract_ask prints the ask in an assistant message: fenced code blocks are
# dropped, then the text from the last paragraph holding a question mark to
# the end, or, without one, from the paragraph before a trailing numbered list
# to the end. No question and no list: nothing. Capped at 600 bytes.
extract_ask() {
	printf '%s\n' "$1" | LC_ALL=C tr -d '\000-\010\013\014\016-\037\177' | awk '
		/^[[:space:]]*```/ { fence = !fence; next }
		fence { next }
		{ lines[++n] = $0 }
		END {
			# paragraph starts
			p = 0
			for (i = 1; i <= n; i++) {
				blank = (lines[i] ~ /^[[:space:]]*$/)
				if (!blank && (i == 1 || lines[i-1] ~ /^[[:space:]]*$/)) { start[++p] = i }
			}
			if (p == 0) exit
			for (k = 1; k <= p; k++) { end[k] = (k < p ? start[k+1] - 1 : n) }
			from = 0
			for (k = p; k >= 1; k--) {
				for (i = start[k]; i <= end[k]; i++) if (index(lines[i], "?")) { from = k; break }
				if (from) break
			}
			if (!from) {
				for (k = p; k >= 1; k--) {
					if (lines[start[k]] ~ /^[[:space:]]*[0-9]+[.)]/) { from = (k > 1 ? k - 1 : k); break }
					if (k < p) break
				}
			}
			if (!from) exit
			for (i = start[from]; i <= n; i++) print lines[i]
		}' | awk 'NF { blank = 0 } !NF { if (blank) next; blank = 1 } { print }' | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' | LC_ALL=C head -c 600
}

send_text() {
	# $1 is the message body; the project name is prefixed here.
	local text="$project: $1"
	if [ -z "$token" ]; then
		log_line "no token: set SOCK_TOKEN or the plugin's sock_token option"
		return 1
	fi
	if ! command -v sock >/dev/null 2>&1; then
		log_line "sock not found on PATH"
		return 1
	fi
	export SOCK_TOKEN=$token
	# Never inherit a URL from another source. With no URL of our own, the CLI
	# default or config file applies. An explicit --url beats a config file.
	unset SOCK_URL
	local url_args=()
	if [ -n "$url" ]; then
		url_args=(--url "$url")
		# The config file's access_gate belongs to the config's own URL. When the
		# plugin names the URL, point the CLI at a config path that does not exist.
		if [ -n "${CLAUDE_PLUGIN_OPTION_SOCK_TOKEN:-}" ]; then
			export XDG_CONFIG_HOME=${TMPDIR:-/tmp}/sock-hook-noconfig.$$
		fi
	fi
	local errf
	errf=$(mktemp "${TMPDIR:-/tmp}/sock-hook.XXXXXX") || {
		log_line "mktemp failed"
		return 1
	}
	local timeout=${SOCK_HOOK_TIMEOUT:-10}
	case $timeout in
	'' | *[!0-9]*) timeout=10 ;;
	*) [ "$timeout" -gt 0 ] || timeout=10 ;;
	esac
	sock ${url_args[@]+"${url_args[@]}"} send -- "$text" >/dev/null 2>"$errf" </dev/null &
	local pid=$!
	(
		sleep "$timeout"
		kill "$pid" 2>/dev/null
	) >/dev/null 2>&1 &
	local watchdog=$!
	disown "$watchdog" 2>/dev/null # no "Terminated" job notice on stderr when we reap it below
	wait "$pid"
	local rc=$?
	kill "$watchdog" 2>/dev/null
	pkill -P "$watchdog" 2>/dev/null
	if [ "$rc" -ne 0 ]; then
		# Strip control bytes, then redact the whole captured stderr, then cut, so a
		# token split by a control byte or straddling the cut cannot leak a prefix.
		local detail
		detail=$(head -c 65536 "$errf" | LC_ALL=C tr -d '[:cntrl:]')
		if [ -n "$token" ]; then
			detail=${detail//"$token"/[redacted]}
		fi
		detail=$(printf '%s' "$detail" | head -c 300)
		log_line "sock send exit $rc: $detail"
	fi
	rm -f "$errf"
	[ "$rc" -eq 0 ]
}

# send_ask sends an ask unless this session already sent the same one.
send_ask() {
	local ask=$1 hash
	[ -n "$ask" ] || return 0
	hash=$(printf '%s' "$ask" | shasum -a 256 2>/dev/null | cut -c1-64)
	[ -n "$hash" ] || hash=$(printf '%s' "$ask" | sha256sum 2>/dev/null | cut -c1-64)
	if [ -f "$asks/$session.sent" ] && [ "$(cat "$asks/$session.sent" 2>/dev/null)" = "$hash" ]; then
		return 0
	fi
	if send_text "$ask"; then
		(
			umask 077
			mkdir -p "$asks" && printf '%s\n' "$hash" >"$asks/$session.sent"
		) 2>/dev/null
	fi
	return 0
}

remember_ask() {
	(
		umask 077
		mkdir -p "$asks" || exit 0
		find "$asks" -type f -mtime +7 -delete 2>/dev/null
		if [ -n "$1" ]; then printf '%s' "$1" >"$asks/$session.ask"; else rm -f "$asks/$session.ask"; fi
	) 2>/dev/null
	return 0
}

case $mode in
permission)
	msg=
	if [ "$have_jq" = 1 ]; then
		msg=$(field message | LC_ALL=C tr -d '\000-\037\177' | LC_ALL=C head -c 300)
	else
		log_line "jq not found: sending the fixed phrase"
	fi
	[ -n "$msg" ] || msg="needs permission"
	send_text "$msg"
	exit 0
	;;
stop)
	ask=
	if [ "$have_jq" = 1 ]; then
		ask=$(extract_ask "$(field last_assistant_message)")
		remember_ask "$ask"
		if [ "$ask_now" = 1 ]; then send_ask "$ask"; fi
	fi
	case "${CLAUDE_PLUGIN_OPTION_NOTIFY_ON_STOP:-}:${SOCK_NOTIFY_ON_STOP:-}" in
	true:* | *:1) send_text "finished a turn" ;;
	esac
	exit 0
	;;
idle)
	if [ "$have_jq" != 1 ]; then
		log_line "jq not found: idle messages need jq, nothing sent"
		exit 0
	fi
	ask=
	if [ -f "$asks/$session.ask" ]; then
		ask=$(cat "$asks/$session.ask" 2>/dev/null)
	else
		# Stop never ran for this session: the transcript's last assistant text.
		transcript=$(field transcript_path)
		if [ -n "$transcript" ] && [ -r "$transcript" ]; then
			last=$(jq -R -c 'fromjson? | select(.type == "assistant") | [.message.content[]? | select(.type == "text") | .text] | join("\n")' "$transcript" 2>/dev/null | tail -n 1 | jq -r '.' 2>/dev/null)
			ask=$(extract_ask "$last")
		fi
	fi
	send_ask "$ask"
	exit 0
	;;
esac

# No mode: the v1 shape, a fixed phrase.
[ -n "$phrase" ] || exit 0
send_text "$phrase"
exit 0
