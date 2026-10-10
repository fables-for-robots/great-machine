#!/bin/sh
# Tests that a program has the web page built into it, by starting its
# draal and asking it for the page as a browser would. A program without
# the page answers 501, which fails the test. It needs curl for a program,
# and docker for an image, and prints "ok" at the end.
#
#   scripts/test-page.sh ./result/bin/great-machine
#   scripts/test-page.sh --image ghcr.io/fables-for-robots/great-machine:nix
#
# The draal it starts has an endpoint ID of its own and no operator, and
# dials iroh's relays as every draal does.
set -eu

# A jaccard-store server that is never dialed: draal only hands it on.
server=0000000000000000000000000000000000000000000000000000000000000000

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

# log: what draal has written so far.
log() {
	if [ -n "$image" ]; then
		docker logs "$name" 2>&1
	else
		cat "$tmp/log"
	fi
}

fail() {
	echo "test-page: FAILED: $1" >&2
	echo "--- what draal wrote:" >&2
	log >&2 || true
	exit 1
}

# get URL: the body of the answer, and failure for a status that is not 2xx.
get() {
	if [ -n "$image" ]; then
		docker exec "$name" wget -q -O - "$1"
	else
		curl -fsS "$1"
	fi
}

if [ -n "$image" ]; then
	# As in a pod: another store over /nix/store, and the page on the
	# loopback address of the container, where only the container reaches
	# it. That is why it is asked from inside, with busybox's wget.
	docker run -d --name "$name" --tmpfs /nix/store "$image" \
		draal --data /tmp/draal --jaccard-server "$server" --jaccard-prefix nix/ >/dev/null
else
	# Port 0 is a port the system picks; draal logs the one it got.
	"$program" draal --data "$tmp/data" --jaccard-server "$server" --jaccard-prefix nix/ \
		--web-addr 127.0.0.1:0 2>"$tmp/log" &
	pid=$!
fi

# draal binds its iroh endpoint first, which waits for a relay, 15 seconds
# at most; 600 steps of 0.1 second leave room above that.
i=0
until log | grep -q 'msg="web page"'; do
	i=$((i + 1))
	[ "$i" -lt 600 ] || fail "draal did not log its web page"
	sleep 0.1
done
url=$(log | sed -n 's/.*msg="web page".* url=\([^ ]*\).*/\1/p' | head -n 1)
[ -n "$url" ] || fail "no url in the line of the web page"

if log | grep -q 'the web page was not built into this program'; then
	fail "draal says that the page was not built into it"
fi
page=$(get "$url") || fail "GET $url failed"
echo "$page" | grep -qi '<!doctype html>' || fail "GET $url is no page: $page"
# A path that is no file is the page too: its routes are the browser's.
page=$(get "${url}s/web") || fail "GET ${url}s/web failed"
echo "$page" | grep -qi '<!doctype html>' || fail "GET ${url}s/web is no page: $page"

echo ok
