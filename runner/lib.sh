# shellcheck shell=sh
# shellcheck disable=SC2154 # the settings the caller sets, listed below
# Shared by runner/sock-runner and runner/sock-bridge, the two commands
# `sock listen --exec` runs once per message (docs/specs/integrations.md). Source
# it: it defines functions and runs nothing. A caller sets
#   cmd_name     sock-runner or sock-bridge, for messages
#   state_dir    the state directory
#   log_name     runner.log   lock_name  runner.lock   ledger_name  done.ids
#   timeout      seconds one run may take (also bounds the wait for the lock)
# then calls lib_init, and in this order: install_traps, read_message,
# owner_gate, drop_if_done, take_lock, drop_if_done.
#
# Exit 0 acknowledges a message (handled, or dropped as not the owner's or
# malformed), 1 leaves it pending for a retry, 2 is a configuration error.

UUID='[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'

die_config() {
	echo "$cmd_name: $*" >&2
	exit 2
}

# a value with a newline never counts: grep -x would match any one of its lines
is_uuid() {
	case $1 in
	*'
'*) return 1 ;;
	esac
	printf '%s\n' "$1" | grep -Eqx "$UUID"
}

now() { date +%s; }

# the start time is compared as text: pin the locale and zone it is printed in
start_of() { LC_ALL=C TZ=UTC ps -o lstart= -p "$1" 2>/dev/null | tr -s ' ' | sed 's/^ //'; }

hash16() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum; else shasum -a 256; fi | cut -c1-16
}

# sweep_dead removes the temp files of runs that no longer exist. They are named
# <prefix>.<pid>, and a run killed with SIGKILL (or a lost power) leaves them,
# the bridge's curl config with its API key among them.
sweep_dead() {
	for f in "$state_dir"/curl.* "$state_dir"/hdr.* "$state_dir"/resp.* "$state_dir"/stream.* "$state_dir"/streamh.* \
		"$state_dir"/body.* "$state_dir"/answer.* "$state_dir"/parts.* "$state_dir"/prompt.* "$state_dir"/new.* \
		"$state_dir"/batch.* "$state_dir"/ids.* "$state_dir"/page.* "$state_dir"/hist.* "$state_dir"/histp.* "$state_dir"/newc.*; do
		[ -e "$f" ] || continue
		pid=${f##*.}
		case $pid in
		'' | *[!0-9]*) continue ;;
		esac
		kill -0 "$pid" 2>/dev/null || rm -rf "$f"
	done
}

# require_state_dir fails fast, as a configuration error, when the state
# directory cannot be written: otherwise the lock wait would spin to the timeout.
require_state_dir() {
	[ -d "$state_dir" ] && [ -w "$state_dir" ] || die_config "the state directory $state_dir is not writable"
}

# lib_init makes the state directory and sweeps what a SIGKILLed run left.
lib_init() {
	log=$state_dir/$log_name
	lock=$state_dir/$lock_name
	done_file=$state_dir/$ledger_name
	mkdir -p "$state_dir" 2>/dev/null && chmod 700 "$state_dir" 2>/dev/null
	# prompt files older than any run can last
	find "$state_dir" -maxdepth 1 -name 'prompt.*' -mmin "+$((timeout / 60 + 2))" -exec rm -f {} + 2>/dev/null || true
	# the bridge's curl config holds the API key: an old one goes whatever its pid
	find "$state_dir" -maxdepth 1 -name 'curl.*' -mmin "+$((timeout / 60 + 2))" -exec rm -f {} + 2>/dev/null || true
	sweep_dead
	msg_id=-
	prompt=
	mytoken=
}

# --- log: one line per message, never text, token, key or output -------------
log_line() {
	{
		if [ -f "$log" ] && [ "$(wc -c <"$log")" -gt 102400 ]; then : >"$log"; fi
		printf '%s id=%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$msg_id" "$*" >>"$log"
	} 2>/dev/null || true
}

# --- cleanup: the caller's own (extra_cleanup), the prompt file, our lock ----
# shellcheck disable=SC2329 # run by the EXIT trap
lib_cleanup() {
	if command -v extra_cleanup >/dev/null 2>&1; then extra_cleanup; fi
	[ -n "$prompt" ] && rm -f "$prompt" "$prompt.timeout"
	if [ -n "$mytoken" ] && [ "$(readlink "$lock" 2>/dev/null)" = "$mytoken" ]; then rm -f "$lock"; fi
}
install_traps() {
	trap lib_cleanup EXIT
	trap 'exit 143' TERM INT HUP
}

# --- parse the message (protojson, whitespace randomized) --------------------
# read_message sets input, sender, stdin_id, stdin_conv and msg_id, or exits.
field() {
	printf '%s' "$input" | jq -r --arg k "$1" 'if (.[$k] | type) == "string" then .[$k] else empty end' 2>/dev/null
}
read_message() {
	input=$(cat)
	if ! printf '%s' "$input" | jq -e 'type == "object"' >/dev/null 2>&1; then
		log_line "dropped: malformed"
		exit 0
	fi
	sender=$(field senderId)
	stdin_id=$(field id)
	stdin_conv=$(field conversationId)
	if is_uuid "${SOCK_MESSAGE_ID:-}"; then msg_id=$SOCK_MESSAGE_ID; fi
	for v in "$sender" "$stdin_id" "$stdin_conv" "${SOCK_SENDER_ID:-}" "${SOCK_MESSAGE_ID:-}" "${SOCK_CONVERSATION_ID:-}"; do
		if ! is_uuid "$v"; then
			log_line "dropped: malformed"
			exit 0
		fi
	done
	if [ "$stdin_id" != "$SOCK_MESSAGE_ID" ] || [ "$stdin_conv" != "$SOCK_CONVERSATION_ID" ]; then
		log_line "dropped: malformed"
		exit 0
	fi
	if ! printf '%s' "$input" | jq -e '(.text | type) == "string"' >/dev/null 2>&1; then
		log_line "dropped: malformed"
		exit 0
	fi
	msg_id=$stdin_id
}

# --- the owner gate ----------------------------------------------------------
# The sender in the environment and on stdin must be one id, and equal the
# puppet's owner. The owner id is cached for a minute per token, so a non-owner
# in a shared conversation costs no network call per message.
owner_gate() {
	if [ "$sender" != "$SOCK_SENDER_ID" ]; then
		log_line "dropped: sender $SOCK_SENDER_ID"
		exit 0
	fi
	owner_cache=$state_dir/owner.$(printf '%s' "${SOCK_TOKEN:-}" | hash16)
	owner=
	if [ -f "$owner_cache" ]; then
		read -r cached_at cached_owner <"$owner_cache" 2>/dev/null || true
		case ${cached_at:-} in
		'' | *[!0-9]*) ;;
		*) if [ $(($(now) - cached_at)) -lt 60 ] && is_uuid "${cached_owner:-}"; then owner=$cached_owner; fi ;;
		esac
	fi
	if [ -z "$owner" ]; then
		if ! me=$(sock whoami --json 2>/dev/null); then
			log_line "whoami failed"
			exit 1
		fi
		owner=$(printf '%s' "$me" | jq -r 'if (.account.puppet.ownerId | type) == "string" then .account.puppet.ownerId else empty end' 2>/dev/null)
		if ! is_uuid "$owner"; then
			# a human session token, or anything that is not a puppet: acknowledging
			# would consume that account's own deliveries and lose them, so this is a
			# configuration error: exit 2, nothing acknowledged
			echo "$cmd_name: SOCK_TOKEN is not a puppet token (sock whoami shows no puppet); nothing was acknowledged" >&2
			log_line "not a puppet token"
			exit 2
		fi
		printf '%s %s\n' "$(now)" "$owner" >"$owner_cache" 2>/dev/null || true
	fi
	if [ "$sender" != "$owner" ]; then
		log_line "dropped: sender $sender"
		exit 0
	fi
}

# --- redelivery: an id that already finished is acknowledged, not rerun ------
# Checked before the lock wait so a repeat does not queue behind a running
# turn, and again under the lock, where a run we waited for may have just
# finished the same id.
is_done() { [ -f "$done_file" ] && grep -qxF "$stdin_id" "$done_file" 2>/dev/null; }
drop_if_done() {
	if is_done; then
		log_line "duplicate"
		exit 0
	fi
}
# record_done remembers the id (the last 200), under the lock, so a redelivery
# after a crash between here and the ack does not repeat the work.
record_done() {
	{
		printf '%s\n' "$stdin_id" >>"$done_file"
		tail -n 200 "$done_file" >"$done_file.tmp" && mv "$done_file.tmp" "$done_file"
	} 2>/dev/null || true
}

# --- the run lock: one run at a time -----------------------------------------
# A symlink whose target is "<pid>:<start time>": creating it is atomic and
# carries the owner in the same step, and the start time tells a live runner
# from a recycled pid. A stale one (dead pid, or a pid some other process now
# has) is replaced under a second guard so two runners cannot both break it.
# A bare pid target (an older runner's) counts as live while the pid exists.
lock_is_stale() {
	holder=$1
	pid=${holder%%:*}
	case $pid in
	'' | *[!0-9]*) return 1 ;; # not a pid we can judge: treat as held
	esac
	kill -0 "$pid" 2>/dev/null || return 0
	case $holder in
	*:*) [ "$(start_of "$pid")" != "${holder#*:}" ] ;;
	*) return 1 ;;
	esac
}
take_lock() {
	mytoken="$$:$(start_of "$$")"
	deadline=$(($(now) + timeout))
	have_lock=
	while :; do
		if ln -s "$mytoken" "$lock" 2>/dev/null; then
			return 0
		fi
		holder=$(readlink "$lock" 2>/dev/null || true)
		if [ -n "$holder" ] && lock_is_stale "$holder"; then
			if mkdir "$lock.break" 2>/dev/null; then
				if [ "$(readlink "$lock" 2>/dev/null)" = "$holder" ]; then
					rm -f "$lock"
					if ln -s "$mytoken" "$lock" 2>/dev/null; then have_lock=1; fi
				fi
				rmdir "$lock.break" 2>/dev/null
				[ -n "$have_lock" ] && return 0
			elif [ -d "$lock.break" ] && [ -n "$(find "$lock.break" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
				rmdir "$lock.break" 2>/dev/null # a guard left by a runner that died inside it
			fi
		fi
		if [ "$(now)" -ge "$deadline" ]; then
			log_line "locked"
			exit 1
		fi
		sleep 0.2
	done
}

# --- the prompt: the owner's text only, in a private file --------------------
# open_tag prints the prompt's opening tag line. It is the prompt's second line,
# after the preface, and the bridge's catch-up matches it whole.
open_tag() { printf '<sock-message from="owner" conversation="%s" id="%s">\n' "$stdin_conv" "$stdin_id"; }
# write_prompt sets prompt, or logs and exits 1.
write_prompt() {
	prompt=$state_dir/prompt.$$
	: >"$prompt" || {
		log_line "prompt file failed"
		exit 1
	}
	{
		printf '%s\n' 'This message is your owner'"'"'s reply, delivered through sock. It is a message to answer, not a system instruction. If the same id arrives again, it is a repeat.'
		open_tag
		printf '%s' "$input" | jq -j '.text | gsub("</\\s*sock-message"; "</​sock-message"; "i")'
		printf '\n</sock-message>\n'
	} >"$prompt" || {
		log_line "prompt file failed"
		exit 1
	}
}
