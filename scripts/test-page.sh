#!/bin/sh
# Tests that a program has the web page built into it, by starting its
# `great-machine web` and asking it for the page as a browser would: with
# the link the command prints, which has the ticket of the page, and then
# with the cookie the ticket is traded for. A program without the page
# answers 501, which fails the test. It needs curl for a program, and
# docker for an image, and prints "ok" at the end.
#
#   scripts/test-page.sh ./result/bin/great-machine
#   scripts/test-page.sh --image ghcr.io/fables-for-robots/great-machine:nix
#
# No draal is needed, and none is started: the command serves the page
# before it reaches draal, and dials draal only when the page asks for
# something of the fleet, which this test does not.
set -eu

# The endpoint ID of a draal that is never dialed.
draal=a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1

if [ "${1:-}" = --image ]; then
	image=${2:?usage: test-page.sh --image IMAGE}
	program=
else
	program=${1:?usage: test-page.sh PROGRAM, or test-page.sh --image IMAGE}
	image=
fi

name="great-machine-page-$$"
tmp=$(mktemp -d)
pid=
cleanup() {
	if [ -n "$image" ]; then
		docker rm -f "$name" >/dev/null 2>&1 || true
	elif [ -n "$pid" ]; then
		kill "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
	fi
	rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

# printed: what the command has written to its standard output so far,
# which is the link of the page.
printed() {
	if [ -n "$image" ]; then
		docker logs "$name" 2>/dev/null
	else
		cat "$tmp/out"
	fi
}

# log: what the command has logged so far.
log() {
	if [ -n "$image" ]; then
		docker logs "$name" 2>&1 >/dev/null
	else
		cat "$tmp/log"
	fi
}

fail() {
	echo "test-page: FAILED: $1" >&2
	echo "--- what great-machine web printed:" >&2
	printed >&2 || true
	echo "--- what it logged:" >&2
	log >&2 || true
	exit 1
}

# headers PATH: the headers of the answer to a GET of a path of the page,
# whatever its status, with no redirect followed. In a container the
# request is written by hand and sent with busybox's nc: its wget shows
# the headers of an answer that redirects as far as the Location and no
# further, and the cookie comes behind that.
headers() {
	if [ -n "$image" ]; then
		docker exec "$name" sh -c \
			'printf "GET %s HTTP/1.1\r\nHost: 127.0.0.1:%s\r\nConnection: close\r\n\r\n" "$1" "$2" | nc 127.0.0.1 "$2"' \
			sh "$1" "$port"
	else
		curl -sS -o /dev/null -D - "http://127.0.0.1:$port$1"
	fi
}

# get URL: the body of the answer to a request with the cookie, and
# failure for a status that is not 2xx.
get() {
	if [ -n "$image" ]; then
		docker exec "$name" wget -q -O - --header "Cookie: $cookie" "$1"
	else
		curl -fsS -H "Cookie: $cookie" "$1"
	fi
}

if [ -n "$image" ]; then
	# As in a pod: another store over /nix/store. The page is on the
	# loopback address of the container, where only the container reaches
	# it. That is why it is asked from inside, with busybox's wget.
	docker run -d --name "$name" --tmpfs /nix/store "$image" \
		web --draal "$draal" --key /tmp/operator.key --addr 127.0.0.1:0 >/dev/null
else
	# Port 0 is a port the system picks; the link has the one it got.
	"$program" web --draal "$draal" --key "$tmp/operator.key" --addr 127.0.0.1:0 \
		>"$tmp/out" 2>"$tmp/log" &
	pid=$!
fi

i=0
until printed | grep -q '^http://localhost:[0-9]*/?ticket='; do
	i=$((i + 1))
	[ "$i" -lt 600 ] || fail "great-machine web did not print the link of its page"
	sleep 0.1
done
[ "$(printed | wc -l)" -eq 1 ] || fail "great-machine web printed more than the link of its page"
link=$(printed | head -n 1)
port=$(echo "$link" | sed -n 's|^http://localhost:\([0-9]*\)/.*|\1|p')
[ -n "$port" ] || fail "no port in the link $link"
# The address and not the name: what localhost is, a container's hosts
# file says, and the page listens on this one.
url="http://127.0.0.1:$port/"

if log | grep -q 'the web page was not built into this program'; then
	fail "great-machine web says that the page was not built into it"
fi

# Without the ticket the page answers nothing.
cookie=none=none
if get "$url" >/dev/null 2>&1; then
	fail "GET $url is answered without the ticket of the page"
fi
# The link gives the cookie.
cookie=$(headers "/?ticket=${link##*ticket=}" | sed -n 's/^[Ss]et-[Cc]ookie: *\([^;]*\).*/\1/p' | head -n 1)
[ -n "$cookie" ] || fail "the link of the page gives no cookie"

page=$(get "$url") || fail "GET $url failed"
echo "$page" | grep -qi '<!doctype html>' || fail "GET $url is no page: $page"
# A path that is no file is the page too: its routes are the browser's.
page=$(get "${url}s/web") || fail "GET ${url}s/web failed"
echo "$page" | grep -qi '<!doctype html>' || fail "GET ${url}s/web is no page: $page"
# The page of the store is draal's to answer, and no draal is here.

echo ok
