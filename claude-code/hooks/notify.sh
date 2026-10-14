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
# The CLI prints cleaned paths, so a trailing slash on XDG_STATE_HOME or HOME
# must not leave a doubled one here.
while [ "${state_dir#*//}" != "$state_dir" ]; do state_dir=${state_dir//\/\//\/}; done
log=$state_dir/hook.log
asks=$state_dir/asks
# The token and the URL come from the same source, so a token never goes to a
# server chosen by a different source. The plugin option wins: .mcp.json gives
# sock-mcp the plugin token, and an ambient SOCK_TOKEN exported for another
# platform (Codex) must not make these hooks post as that puppet.
from_plugin=0
if [ -n "${CLAUDE_PLUGIN_OPTION_SOCK_TOKEN:-}" ]; then
	from_plugin=1
	token=$CLAUDE_PLUGIN_OPTION_SOCK_TOKEN
	url=${CLAUDE_PLUGIN_OPTION_SOCK_URL:-}
	# An ambient access gate belongs to the env-token deployment, not this URL.
	unset SOCK_ACCESS_GATE
else
	token=${SOCK_TOKEN:-}
	url=${SOCK_URL:-}
fi
# No child inherits the credential: each sock call gets the one it needs. A
# machine key in particular must reach only `sock puppets ensure`.
unset SOCK_TOKEN CLAUDE_PLUGIN_OPTION_SOCK_TOKEN

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
		# umask covers a new log; an older one may have been made wider.
		chmod 600 "$log"
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
project_dir=${CLAUDE_PROJECT_DIR:-$PWD}
project_dir=${project_dir%/}
project=$(printf '%s' "${project_dir##*/}" | LC_ALL=C tr -cd 'A-Za-z0-9._ -' | LC_ALL=C cut -c1-100)
[ -n "$project" ] || project=project

# The session the ask belongs to: the hook's session_id, else the project.
session=$(field session_id | LC_ALL=C tr -cd 'A-Za-z0-9_-' | LC_ALL=C cut -c1-64)
[ -n "$session" ] || session=$(printf '%s' "${CLAUDE_PROJECT_DIR:-$PWD}" | LC_ALL=C tr -cd 'A-Za-z0-9_-' | LC_ALL=C cut -c1-64)
[ -n "$session" ] || session=project

# utf8_cap prints stdin cut to $1 bytes, less a UTF-8 sequence the cut left
# incomplete: the CLI refuses text that is not valid UTF-8.
utf8_cap() {
	LC_ALL=C head -c "$1" | LC_ALL=C awk '
		BEGIN { for (i = 128; i < 256; i++) ord[sprintf("%c", i)] = i }
		{ s = s (NR > 1 ? "\n" : "") $0 }
		END {
			n = length(s)
			k = 0
			while (k < 3 && k < n && ord[substr(s, n - k, 1)] >= 128 && ord[substr(s, n - k, 1)] < 192) k++
			b = ord[substr(s, n - k, 1)]
			need = (b >= 240 ? 4 : b >= 224 ? 3 : b >= 192 ? 2 : 1)
			if (b >= 192 && k + 1 < need) n -= k + 1
			printf "%s", substr(s, 1, n)
		}'
}

# extract_ask prints the ask in an assistant message: fenced code blocks
# (CommonMark: 3 or more backticks or tildes, closed only by a run of the same
# character at least as long with nothing after it) are dropped, then the text
# from the last paragraph holding a question mark to the end, or, without one,
# from the paragraph before a trailing numbered list to the end. No question
# and no list: nothing. Capped at 600 bytes.
extract_ask() {
	printf '%s\n' "$1" | LC_ALL=C tr -d '\000-\010\013\014\016-\037\177' | awk '
		{
			t = $0
			sub(/^[[:space:]]*/, "", t)
			c = substr(t, 1, 1)
			run = 0
			if (c == "`" || c == "~") while (substr(t, run + 1, 1) == c) run++
			rest = substr(t, run + 1)
		}
		fence {
			if (run >= 3 && c == fc && run >= fl && rest ~ /^[[:space:]]*$/) fence = 0
			next
		}
		# A backtick run with a backtick after it is inline code, not a fence.
		run >= 3 && !(c == "`" && index(rest, "`")) { fence = 1; fc = c; fl = run; next }
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
			# A trailing list ends the message; blank lines between its items make
			# each item a paragraph, so walk back to the first.
			if (!from && lines[start[p]] ~ /^[[:space:]]*[0-9]+[.)]/) {
				k = p
				while (k > 1 && lines[start[k-1]] ~ /^[[:space:]]*[0-9]+[.)]/) k--
				from = (k > 1 ? k - 1 : k)
			}
			if (!from) exit
			for (i = start[from]; i <= n; i++) print lines[i]
		}' | awk 'NF { blank = 0 } !NF { if (blank) next; blank = 1 } { print }' | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' | utf8_cap 600
}

# redact replaces the plugin token and common secret shapes with [redacted]:
# sock tokens, AWS access key ids, GitHub and OpenAI style keys, bearer
# values, NAME=value where NAME names a key, token, secret or password, and
# the query string of any URL. It narrows what an echoed secret can leak; it
# does not close it.
redact() {
	local s=$1
	if [ -n "$token" ]; then s=${s//"$token"/[redacted]}; fi
	printf '%s' "$s" | sed -E \
		-e 's/sock_(pt|st|ot|mk|rt|ct)_[A-Za-z0-9_-]+/[redacted]/g' \
		-e 's/AKIA[0-9A-Z]{16}/[redacted]/g' \
		-e 's/(ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]+/[redacted]/g' \
		-e 's/sk-[A-Za-z0-9_-]{16,}/[redacted]/g' \
		-e 's/[Bb]earer [A-Za-z0-9._~+\/=-]+/Bearer [redacted]/g' \
		-e 's/([A-Za-z0-9_]*([Kk][Ee][Yy]|[Tt][Oo][Kk][Ee][Nn]|[Ss][Ee][Cc][Rr][Ee][Tt]|[Pp][Aa][Ss][Ss][Ww][Oo][Rr][Dd])[A-Za-z0-9_]*)=[^[:space:]]+/\1=[redacted]/g' \
		-e 's#(https?://[^[:space:]?]*)\?[^[:space:]]*#\1?[redacted]#g'
}

# hook_timeout is SOCK_HOOK_TIMEOUT seconds, 10 when unset or not a positive integer.
hook_timeout() {
	local t=${SOCK_HOOK_TIMEOUT:-10}
	case $t in
	'' | *[!0-9]*) t=10 ;;
	*) [ "$t" -gt 0 ] || t=10 ;;
	esac
	printf '%s' "$t"
}

# bounded runs a command with stdin from $1, stdout to $2 and stderr to $3,
# killed after hook_timeout seconds: async hooks are not timed out by the host.
# It returns the command's status.
bounded() {
	local in=$1 out=$2 err=$3
	shift 3
	"$@" <"$in" >"$out" 2>"$err" &
	local pid=$!
	# TERM first; a command that ignores it gets KILL two seconds later.
	(
		sleep "$(hook_timeout)"
		kill "$pid" 2>/dev/null || exit 0
		sleep 2
		kill -9 "$pid" 2>/dev/null
	) >/dev/null 2>&1 &
	local watchdog=$!
	disown "$watchdog" 2>/dev/null # no "Terminated" job notice on stderr when we reap it below
	wait "$pid"
	local rc=$?
	kill "$watchdog" 2>/dev/null
	pkill -P "$watchdog" 2>/dev/null
	return "$rc"
}

# scrubbed prints a captured stderr file fit for the log: control bytes
# stripped, then every credential in $2... redacted, then cut to 300 bytes, so
# a token split by a control byte or straddling the cut cannot leak a prefix.
scrubbed() {
	local detail
	detail=$(head -c 65536 "$1" | LC_ALL=C tr -d '[:cntrl:]')
	shift
	local s
	for s in "$@"; do
		if [ -n "$s" ]; then detail=${detail//"$s"/[redacted]}; fi
	done
	printf '%s' "$detail" | head -c 300
}

send_text() {
	# $1 is the message body; the project name is prefixed here.
	local text
	text="$project: $(redact "$1")"
	if [ -z "$token" ]; then
		log_line "no token: set SOCK_TOKEN or the plugin's sock_token option"
		return 1
	fi
	if ! command -v sock >/dev/null 2>&1; then
		log_line "sock not found on PATH"
		return 1
	fi
	# Never inherit a URL from another source. With no URL of our own, the CLI
	# default or config file applies. An explicit --url beats a config file.
	unset SOCK_URL
	local url_args=()
	if [ -n "$url" ]; then
		url_args=(--url "$url")
		# The config file's access_gate belongs to the config's own URL. When the
		# plugin names the URL, point the CLI at a config path that does not exist.
		if [ "$from_plugin" = 1 ]; then
			export XDG_CONFIG_HOME=${TMPDIR:-/tmp}/sock-hook-noconfig.$$
		fi
	fi
	local work
	work=$(mktemp -d "${TMPDIR:-/tmp}/sock-hook.XXXXXX") || {
		log_line "mktemp failed"
		return 1
	}
	local send_token=$token rc
	# A machine key (ADR 10) sends as this project's own puppet: the CLI makes or
	# reuses it and prints the path of its token file under the state dir's
	# puppets cache. Only a regular file there holding a puppet token is used.
	case $token in
	sock_mk_*)
		SOCK_TOKEN=$token bounded /dev/null "$work/ensure.out" "$work/ensure.err" \
			sock ${url_args[@]+"${url_args[@]}"} puppets ensure -- "$project_dir"
		rc=$?
		if [ "$rc" -ne 0 ]; then
			log_line "sock puppets ensure exit $rc: $(scrubbed "$work/ensure.err" "$token")"
			rm -rf "$work"
			return 1
		fi
		local tokfile=
		IFS= read -r tokfile <"$work/ensure.out" || true
		case $tokfile in
		"$state_dir/puppets/"*/../* | "$state_dir/puppets/"../*) tokfile= ;;
		"$state_dir/puppets/"?*) ;;
		*) tokfile= ;;
		esac
		send_token=
		if [ -n "$tokfile" ] && [ -f "$tokfile" ] && [ ! -L "$tokfile" ]; then
			IFS= read -r send_token <"$tokfile" || true
		fi
		case $send_token in
		sock_pt_?*) ;;
		*)
			log_line "sock puppets ensure: no puppet token in the cache, nothing sent: $(scrubbed "$work/ensure.err" "$token")"
			rm -rf "$work"
			return 1
			;;
		esac
		;;
	esac
	# The text goes on stdin, not argv, so another user's `ps` cannot read it.
	printf '%s' "$text" >"$work/text"
	SOCK_TOKEN=$send_token bounded "$work/text" /dev/null "$work/send.err" \
		sock ${url_args[@]+"${url_args[@]}"} send
	rc=$?
	if [ "$rc" -ne 0 ]; then
		log_line "sock send exit $rc: $(scrubbed "$work/send.err" "$token" "$send_token")"
	fi
	rm -rf "$work"
	[ "$rc" -eq 0 ]
}

# send_ask sends an ask unless it was already sent since this session's last
# Stop. Each Stop starts a new turn and clears the record, so the same short
# question in a later turn ("Proceed?") is sent again. With no hash tool the
# dedupe is skipped rather than suppressing everything.
send_ask() {
	local ask=$1 hash=
	[ -n "$ask" ] || return 0
	if command -v shasum >/dev/null 2>&1; then
		hash=$(printf '%s' "$ask" | shasum -a 256 | cut -c1-64)
	elif command -v sha256sum >/dev/null 2>&1; then
		hash=$(printf '%s' "$ask" | sha256sum | cut -c1-64)
	fi
	if [ -n "$hash" ] && [ -f "$asks/$session.sent" ] && [ "$(cat "$asks/$session.sent" 2>/dev/null)" = "$hash" ]; then
		return 0
	fi
	if send_text "$ask" && [ -n "$hash" ]; then
		(
			umask 077
			mkdir -p "$asks" && printf '%s\n' "$hash" >"$asks/$session.sent.$$" && mv "$asks/$session.sent.$$" "$asks/$session.sent"
		) 2>/dev/null
	fi
	return 0
}

# remember_ask records this turn's ask (or that there is none) atomically and
# starts the turn's dedupe afresh.
remember_ask() {
	(
		umask 077
		mkdir -p "$asks" || exit 0
		find "$asks" -type f -mtime +7 -delete 2>/dev/null
		rm -f "$asks/$session.sent"
		if [ -n "$1" ]; then
			printf '%s' "$1" >"$asks/$session.ask.$$" && mv "$asks/$session.ask.$$" "$asks/$session.ask"
		else
			rm -f "$asks/$session.ask"
		fi
	) 2>/dev/null
	return 0
}

case $mode in
permission)
	msg=
	if [ "$have_jq" = 1 ]; then
		msg=$(field message | LC_ALL=C tr -d '\000-\037\177' | utf8_cap 300)
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
	fi
	# Without jq the turn still starts: a stale ask from an earlier turn is
	# forgotten rather than sent later.
	remember_ask "$ask"
	if [ "$ask_now" = 1 ]; then send_ask "$ask"; fi
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
		# Only a regular file, only its last 2 MiB, read on stdin (a path that
		# starts with "-" is never an option), and only entries that carry text.
		transcript=$(field transcript_path)
		if [ -n "$transcript" ] && [ -f "$transcript" ] && [ -r "$transcript" ]; then
			last=$(tail -c 2097152 -- "$transcript" | jq -R -c 'fromjson? | select(.type == "assistant") | [.message.content[]? | select(.type == "text") | .text] | join("\n") | select(length > 0)' 2>/dev/null | tail -n 1 | jq -r '.' 2>/dev/null)
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
