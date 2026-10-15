#!/bin/sh
# e2e-dev.sh: zero-config puppets end to end against the local dev hub, with
# the real sock and sock-mcp and nobody signing in. A fresh HOME gets the
# seeded owner's session (~/.config/sock-dev, from the sock server repo's dev
# hub) as its login, a machine key, and two git projects. sock-mcp starts in
# each with an empty SOCK_TOKEN, as the plugin starts it with sock_token
# unset, and must answer sock_whoami as "<project> on <host>". Then the owner
# replies in each puppet's thread and each puppet must receive only its own
# reply. Everything it made on the hub is erased at exit.
#
# Needs: a dev hub up (SOCK_DEV dir with url and owner.device1.token), sock,
# sock-mcp and jq on PATH. Exit 0 pass, 1 fail, 2 not runnable.
set -u
umask 077

dev=${SOCK_DEV:-$HOME/.config/sock-dev}
fail() {
	echo "FAIL: $*" >&2
	exit 1
}
skip() {
	echo "cannot run: $*" >&2
	exit 2
}
for b in sock sock-mcp jq git; do command -v "$b" >/dev/null 2>&1 || skip "$b not on PATH"; done
[ -r "$dev/url" ] && [ -r "$dev/owner.device1.token" ] || skip "no dev hub credentials in $dev"
url=$(cat "$dev/url")
owner_token=$(cat "$dev/owner.device1.token")
curl -fsS "$url/healthz" >/dev/null 2>&1 || skip "dev hub at $url is not up"

work=$(mktemp -d "${TMPDIR:-/tmp}/sock-e2e.XXXXXX") || skip "mktemp"
export HOME="$work/home" XDG_CONFIG_HOME="$work/home/.config" XDG_STATE_HOME="$work/home/.local/state"
unset SOCK_TOKEN SOCK_URL SOCK_ACCESS_GATE CLAUDE_PROJECT_DIR
mkdir -p "$XDG_CONFIG_HOME/sock" "$XDG_STATE_HOME"

# --- cleanup: erase the puppets and revoke the machine key we made ----------
puppets=
key_device=
cleanup() {
	for p in $puppets; do
		sock puppets erase --yes "$p" >/dev/null 2>&1 || echo "warn: puppet $p not erased" >&2
	done
	if [ -n "$key_device" ]; then
		sock devices revoke "$key_device" >/dev/null 2>&1 || echo "warn: machine key device $key_device not revoked" >&2
	fi
	rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

# --- sign in: the seeded owner's session stands in for `sock login` ---------
printf 'url = "%s"\ntoken = "%s"\n' "$url" "$owner_token" >"$XDG_CONFIG_HOME/sock/config.toml"
sock whoami >/dev/null 2>&1 || fail "the seeded owner token does not sign in"

# --- machine key ---------------------------------------------------------------
sock machine-key create --label "e2e-$$" 2>"$work/mk.err" | sock machine-key use >/dev/null 2>"$work/use.err" ||
	fail "machine key: $(cat "$work/mk.err" "$work/use.err")"
key_device=$(grep -Eo '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' "$work/mk.err" | head -n 1)

# whoami_in DIR prints the account JSON sock-mcp's sock_whoami returns when it
# starts in DIR the way the plugin starts it with no sock_token
whoami_in() {
	(
		cd "$1" || exit 1
		{
			printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}'
			printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
			printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sock_whoami","arguments":{}}}'
			sleep 5
		} | SOCK_TOKEN='' SOCK_URL=https://api.sock.aeryx.ai sock-mcp --outbound-only 2>"$work/mcp.err" |
			jq -c 'select(.id == 2) | .result.structuredContent // (.result.content[0].text | fromjson? // .)' | head -n 1
	)
}

host=$(hostname -s)
# unique per run: the hub suffixes a name the owner already has
names="alpha$$ beta$$"
for name in $names; do
	d=$work/$name
	mkdir -p "$d" || fail "mkdir $name"
	git -C "$d" init -q || fail "git init $name"
	out=$(whoami_in "$d")
	[ -n "$out" ] || fail "sock-mcp in $name answered nothing: $(cat "$work/mcp.err")"
	got=$(printf '%s' "$out" | jq -r '.puppet_name // empty')
	id=$(printf '%s' "$out" | jq -r 'select(.kind == "puppet") | .id // empty')
	[ -n "$id" ] || fail "$name: not a puppet: $out"
	puppets="$puppets $id"
	[ "$got" = "$name on $host" ] || fail "$name: sock_whoami name '$got', want '$name on $host': $out"
	eval "id_$name=\$id"
	echo "ok: $name registered as '$got' ($id)"
done
# shellcheck disable=SC2154 # set by the eval above
# shellcheck disable=SC2086 # $names is two words
set -- $names
eval "a=\$id_$1 b=\$id_$2"
# shellcheck disable=SC2154 # set by the eval
[ "$a" != "$b" ] || fail "both projects resolved one puppet"

# --- routing: each puppet hears only its own thread -------------------------
# as_puppet DIR CMD... runs a sock command as DIR's puppet, the way sock-mcp
# resolves it there
as_puppet() {
	d=$1
	shift
	(cd "$d" && sock "$@" --as-puppet --project "$d")
}
for name in $names; do
	as_puppet "$work/$name" send "e2e: $name ready" >/dev/null 2>"$work/send.err" ||
		fail "$name could not reach its owner: $(cat "$work/send.err")"
done
conv_of() {
	sock conversations --json 2>/dev/null | jq -r --arg p "$1" 'select((.members // .memberIds // []) | tostring | contains($p)) | .id' | head -n 1
}
for name in $names; do
	eval "pid=\$id_$name"
	# shellcheck disable=SC2154 # pid is set by the eval
	c=$(conv_of "$pid")
	[ -n "$c" ] || fail "no owner conversation with $name"
	sock send --conversation "$c" "for-$name" >/dev/null 2>"$work/send.err" || fail "owner reply to $name: $(cat "$work/send.err")"
done
for name in $names; do
	# fetch has no --as-puppet: take the puppet token the machine key's ensure
	# caches for the project, as sock-mcp does
	mk=$(sed -n 's/^machine_key *= *"\(.*\)"$/\1/p' "$XDG_CONFIG_HOME/sock/config.toml")
	tokfile=$(SOCK_TOKEN=$mk sock puppets ensure "$work/$name" 2>"$work/ensure.err") || fail "ensure $name: $(cat "$work/ensure.err")"
	got=$(SOCK_TOKEN=$(head -n 1 "$tokfile") sock fetch --json 2>"$work/fetch.err" | jq -r '.text // .message.text // empty')
	case $got in
	"for-$name") echo "ok: $name received only its own reply" ;;
	*) fail "$name received '$got', want only 'for-$name': $(cat "$work/fetch.err")" ;;
	esac
done
echo "PASS"
