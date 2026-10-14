#!/usr/bin/env bash
# Messages the puppet's owner through the sock CLI, saying what the session
# needs. Modes:
#   --permission   a Notification permission_prompt: forwards the notification
#                  text, which names the tool ("needs permission" when it has none)
#   --stop         a Stop: records the ask in the last assistant message (the
#                  trailing question or decision, code blocks dropped) for
#                  --idle; sends nothing
#   --stop --ask-now  the same, and sends the ask at once (hosts with no idle
#                  event, such as Codex)
#   --idle         a Notification idle_prompt: sends the recorded ask once; a
#                  session with nothing pending sends nothing, and the same ask
#                  is never reposted
# Anything else sends nothing: only the ask and permission prompts reach the
# owner.
# The owner asked for the ask itself in these messages (2026-10-13), which is
# why assistant text now leaves the machine; it is capped and stripped of code.
# Only a session the hook can show is interactive sends, and at most once an
# hour per project (2026-10-14: dozens of messages overnight from agents).
# Always exits 0: a hook must never block or fail the agent.
set -u

mode=
ask_now=0
while [ $# -gt 0 ]; do
	case $1 in
	--permission | --idle | --stop) mode=${1#--} ;;
	--ask-now) ask_now=1 ;;
	esac
	shift
done
[ -n "$mode" ] || exit 0

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

# strip_c1 drops C1 controls from stdin, raw (0x80-0x9F) and UTF-8 encoded
# (C2 80-9F), and every byte not part of a well-formed UTF-8 sequence, keeping
# other characters whole: an 8-bit CSI drives a terminal as ESC [ does.
strip_c1() {
	LC_ALL=C awk '
		BEGIN { for (i = 1; i < 256; i++) ord[sprintf("%c", i)] = i }
		{
			n = length($0); o = ""; i = 1
			while (i <= n) {
				b = ord[substr($0, i, 1)]
				if (b < 128) { o = o substr($0, i, 1); i++; continue }
				need = (b >= 240 && b <= 244 ? 4 : b >= 224 && b < 240 ? 3 : b >= 194 && b < 224 ? 2 : 0)
				ok = (need > 0 && i + need - 1 <= n)
				for (j = 1; ok && j < need; j++) { c = ord[substr($0, i + j, 1)]; if (c < 128 || c >= 192) ok = 0 }
				nx = ord[substr($0, i + 1, 1)]
				if (b == 194 && nx >= 128 && nx < 160) { i += 2; continue }
				if (ok) { o = o substr($0, i, need); i += need } else i++
			}
			printf "%s", o
		}'
}

log_line() {
	# Strip every control character (escape sequences, CR, tab, newline, C1)
	# so tool output cannot drive the terminal of whoever reads the log.
	local msg
	msg=$(printf '%s' "$1" | LC_ALL=C tr -d '[:cntrl:]' | strip_c1)
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

# The Codex plugin route: PLUGIN_ROOT set and a turn_id in the input, which
# only Codex sends. CLAUDE_PLUGIN_ROOT cannot tell the routes apart, since
# Codex sets it for plugin hooks too. The plugin's sock-mcp runs in
# PLUGIN_ROOT, never sees SOCK_TOKEN and resolves its credential from the
# config file, so the CLI resolves the same one (`sock send --as-puppet`,
# sock-cli v0.2.5), never the login token. An exported SOCK_TOKEN or plugin
# option is ignored here; $token keeps it only for redaction.
as_puppet=0
if [ -n "${PLUGIN_ROOT:-}" ] && [ -n "$(field turn_id)" ]; then
	as_puppet=1
fi

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
			ind = substr($0, 1, length($0) - length(t))
			c = substr(t, 1, 1)
			run = 0
			if (c == "`" || c == "~") while (substr(t, run + 1, 1) == c) run++
			rest = substr(t, run + 1)
		}
		fence {
			# A closer takes at most 3 spaces of indent.
			if (run >= 3 && c == fc && run >= fl && rest ~ /^[[:space:]]*$/ && length(ind) <= 3 && ind !~ /\t/) fence = 0
			next
		}
		# CommonMark makes a backtick run with a backtick after it inline code;
		# it opens a fence here anyway, so code under it never leaves the machine.
		run >= 3 { fence = 1; fc = c; fl = run; next }
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
		}' | awk 'NF { blank = 0 } !NF { if (blank) next; blank = 1 } { print }' | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' | redact_stream | utf8_cap 600
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

# redact_stream is redact on stdin. Every byte cap runs after it, so a secret
# straddling the cut is redacted whole rather than sent in part.
redact_stream() {
	local s
	s=$(cat)
	redact "$s"
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
# stripped, then every credential in $2... and every secret shape redact knows
# (a credential the script never saw, such as the config file's) redacted, then
# cut to 300 bytes, so a token split by a control byte or straddling the cut
# cannot leak a prefix.
scrubbed() {
	local detail
	detail=$(head -c 65536 "$1" | LC_ALL=C tr -d '[:cntrl:]')
	shift
	local s
	for s in "$@"; do
		if [ -n "$s" ]; then detail=${detail//"$s"/[redacted]}; fi
	done
	detail=$(redact "$detail")
	printf '%s' "$detail" | head -c 300
}

# send_as_puppet sends $1 as the puppet the plugin's sock-mcp resolves. The
# config file picks the server and gate as it does for sock-mcp, which never
# sees the shell's SOCK_TOKEN, SOCK_URL or SOCK_ACCESS_GATE, and CLAUDE_PROJECT_DIR would
# override --project.
send_as_puppet() {
	if ! command -v sock >/dev/null 2>&1; then
		log_line "sock not found on PATH"
		return 1
	fi
	local work rc
	work=$(mktemp -d "${TMPDIR:-/tmp}/sock-hook.XXXXXX") || {
		log_line "mktemp failed"
		return 1
	}
	printf '%s' "$1" >"$work/text"
	bounded "$work/text" /dev/null "$work/send.err" \
		env -u SOCK_TOKEN -u SOCK_URL -u SOCK_ACCESS_GATE -u CLAUDE_PROJECT_DIR sock send --as-puppet --project "$PLUGIN_ROOT"
	rc=$?
	if [ "$rc" -ne 0 ]; then
		log_line "sock send exit $rc: $(scrubbed "$work/send.err")"
	fi
	rm -rf "$work"
	[ "$rc" -eq 0 ]
}

# session_kind prints interactive, background or unknown. Only a positive
# signal is interactive:
#   Claude Code  CLAUDE_CODE_SESSION_ATTENDED=1 with CLAUDE_CODE_SESSION_ID equal
#                to the input's session_id (0 is -p, the SDK, --bg and teammates)
#   Codex        the transcript's session_meta for this session_id has source
#                "cli" (the TUI) or "vscode" (the app); "exec" and a subagent
#                object are background
# agent_id in the input (a subagent) is background, except a permission prompt
# in an attended Claude Code session. SOCK_HEADLESS (set by sock-runner) is
# background; anything else is unknown.
session_kind() {
	if [ -n "${SOCK_HEADLESS:-}" ]; then
		echo background
		return
	fi
	local sid
	sid=$(field session_id)
	if [ -z "$sid" ]; then
		echo unknown
		return
	fi
	local agent
	agent=$(field agent_id)
	if [ "${CLAUDE_CODE_SESSION_ID:-}" = "$sid" ]; then
		case ${CLAUDE_CODE_SESSION_ATTENDED:-} in
		# A subagent's permission prompt holds up the attended session; its
		# other sends stay quiet.
		1) if [ -z "$agent" ] || [ "$mode" = permission ]; then echo interactive; else echo background; fi ;;
		0) echo background ;;
		*) echo unknown ;;
		esac
		return
	fi
	if [ -n "$agent" ]; then
		echo background
		return
	fi
	local transcript source=
	transcript=$(field transcript_path)
	if [ -n "$transcript" ] && [ -f "$transcript" ] && [ -r "$transcript" ]; then
		source=$(head -c 4194304 <"$transcript" | head -n 1 | jq -r --arg s "$sid" 'select(.type == "session_meta" and .payload.id == $s) | .payload.source | if type == "string" then . elif type == "object" and has("subagent") then "subagent" else empty end' 2>/dev/null)
	fi
	case $source in
	cli | vscode) echo interactive ;;
	exec | subagent) echo background ;;
	*) echo unknown ;;
	esac
}

# The last send for this project, as epoch seconds, one file per project name.
throttle_dir=$state_dir/throttle
throttle_file=$throttle_dir/$project.at
prev_send=

# reserve_hour takes this project's hour: it returns 0 and records now as the
# last send when none is recorded in the past hour, else 1. The check and the
# record run under a mkdir lock, so two hooks firing together cannot both send.
reserve_hour() {
	local lock=$throttle_file.lock now last='' rc=1
	(
		umask 077
		mkdir -p "$throttle_dir"
	) 2>/dev/null
	if ! mkdir "$lock" 2>/dev/null; then
		# A lock a killed hook left behind goes after a minute.
		[ -z "$(find "$lock" -maxdepth 0 -mmin +1 2>/dev/null)" ] || rmdir "$lock" 2>/dev/null
		mkdir "$lock" 2>/dev/null || return 1
	fi
	now=$(date +%s)
	[ -f "$throttle_file" ] && IFS= read -r last <"$throttle_file"
	case $last in '' | *[!0-9]*) last=0 ;; esac
	if [ $((now - last)) -lt 0 ] || [ $((now - last)) -ge 3600 ]; then
		prev_send=$last
		(
			umask 077
			printf '%s\n' "$now" >"$throttle_file.$$" && mv -f "$throttle_file.$$" "$throttle_file"
		) 2>/dev/null && rc=0
	fi
	rmdir "$lock" 2>/dev/null
	return "$rc"
}

# release_hour gives back the hour a failed send reserved.
release_hour() {
	(
		umask 077
		if [ "$prev_send" = 0 ]; then
			rm -f "$throttle_file"
		else
			printf '%s\n' "$prev_send" >"$throttle_file.$$" && mv -f "$throttle_file.$$" "$throttle_file"
		fi
	) 2>/dev/null
	return 0
}

# send_owner sends $1 when this session may message the owner now, logging a
# fixed outcome, never the text, when it may not. $2, when set, is the ask's
# hash, recorded before the send: a send the watchdog killed may have landed,
# and the same ask is never reposted.
send_owner() {
	case $(session_kind) in
	interactive) ;;
	background)
		log_line "quiet: background"
		return 1
		;;
	*)
		log_line "quiet: unknown session"
		return 1
		;;
	esac
	if ! reserve_hour; then
		log_line "quiet: throttled"
		return 1
	fi
	if [ -n "${2:-}" ]; then
		(
			umask 077
			mkdir -p "$asks" && printf '%s\n' "$2" >"$asks/$session.sent.$$" && mv "$asks/$session.sent.$$" "$asks/$session.sent"
		) 2>/dev/null
	fi
	send_text "$1" && return 0
	release_hour
	return 1
}

send_text() {
	# $1 is the message body; the project name is prefixed here.
	local text
	text="$project: $(redact "$1")"
	if [ "$as_puppet" = 1 ]; then
		send_as_puppet "$text"
		return
	fi
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
		# A symlinked directory on the way can still lead outside the cache, so
		# the physical path must sit under the physical cache too.
		if [ -n "$tokfile" ]; then
			local real_dir real_cache
			real_dir=$(cd -P -- "${tokfile%/*}" 2>/dev/null && pwd -P) || real_dir=
			real_cache=$(cd -P -- "$state_dir/puppets" 2>/dev/null && pwd -P) || real_cache=
			case $real_dir/ in
			"$real_cache"/*) [ -n "$real_cache" ] || tokfile= ;;
			*) tokfile= ;;
			esac
			[ -z "$tokfile" ] || tokfile=$real_dir/${tokfile##*/}
		fi
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
	send_owner "$ask" "$hash"
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
		# an empty file is "nothing pending", so --idle does not read the transcript
		printf '%s' "$1" >"$asks/$session.ask.$$" && mv "$asks/$session.ask.$$" "$asks/$session.ask"
	) 2>/dev/null
	return 0
}

case $mode in
permission)
	# Without jq the session cannot be read, so send_owner stays quiet.
	msg=$(field message | LC_ALL=C tr -d '\000-\037\177' | redact_stream | utf8_cap 300)
	[ -n "$msg" ] || msg="needs permission"
	send_owner "$msg"
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
		# Stop never ran for this session (it leaves a file, empty when nothing
		# is pending): the transcript's last assistant text.
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
