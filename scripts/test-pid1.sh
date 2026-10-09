#!/bin/sh
# Tests the package reaper where it does something: as process 1 of a
# container. The program in reaper/testdata/pid1 is built for Linux and
# run as the first process of a busybox container, and the script checks
# that it runs itself again as a child, that an orphan leaves no zombie,
# that SIGTERM reaches the child, and that the child's exit status becomes
# the container's. It needs go and docker, and prints "ok" at the end.
#
#   scripts/test-pid1.sh
set -eu
cd "$(dirname "$0")/.."

image=busybox:1.36
name="great-machine-pid1-$$"
tmp=$(mktemp -d)
cleanup() {
	docker rm -f "$name" >/dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

fail() {
	echo "test-pid1: FAILED: $1" >&2
	echo "--- what the container wrote:" >&2
	docker logs "$name" >&2 2>&1 || true
	exit 1
}

# wait_for_log TEXT: waits until the container has written TEXT.
wait_for_log() {
	i=0
	until docker logs "$name" 2>&1 | grep -q "$1"; do
		i=$((i + 1))
		[ "$i" -lt 100 ] || return 1
		sleep 0.1
	done
}

# states: a line for every process of the container, as /proc has it:
# pid (name) state ...
states() {
	docker exec "$name" sh -c 'for f in /proc/[0-9]*/stat; do cat "$f" 2>/dev/null || true; done'
}

# The container runs on the architecture of the Docker server, which on a
# Mac is a virtual machine.
arch=$(docker version --format '{{.Server.Arch}}') || {
	echo "test-pid1: FAILED: docker does not answer" >&2
	exit 1
}
case "$arch" in
x86_64) arch=amd64 ;;
aarch64) arch=arm64 ;;
esac
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -o "$tmp/pid1" ./reaper/testdata/pid1
chmod 755 "$tmp" "$tmp/pid1"

docker run -d --name "$name" -v "$tmp:/test:ro" "$image" /test/pid1 serve >/dev/null

# (a) Process 1 has started the program again, as a child.
wait_for_log '^serving$' || fail "(a) the program did not get to serve"
docker logs "$name" 2>&1 | grep -q '^child: pid [0-9]*, parent 1$' ||
	fail "(a) no line of a child whose parent is process 1"
if docker logs "$name" 2>&1 | grep -q '^child: pid 1,'; then
	fail "(a) the program runs as process 1 itself"
fi

# (b) The orphan, a sleep whose parent has ended, leaves no zombie. First
# it has to be there, as a child of process 1 (in busybox it is a shell,
# which has sleep built in); then it has to go. The orphan sleeps 5 seconds,
# so the bound of the loop below, 300 steps of 0.1 second, which is 30
# seconds, is far above that and leaves room for a slow runner.
orphan=$(docker logs "$name" 2>&1 | sed -n 's/^orphan: pid \([0-9]*\)$/\1/p')
[ -n "$orphan" ] || fail "(b) no line of the orphan"
states | grep -q "^$orphan ([^)]*) [^Z] 1 " ||
	fail "(b) the orphan $orphan is no process whose parent is process 1"
i=0
while states | grep -q "^$orphan ([^)]*) [^Z] "; do
	i=$((i + 1))
	[ "$i" -lt 300 ] || fail "(b) the orphan did not end"
	sleep 0.1
done
sleep 0.5
if states | grep ') Z '; then
	fail "(b) a zombie is left"
fi

# (c) SIGTERM to process 1 reaches the child, which exits with 0, long
# before docker stop would kill the container.
start=$(date +%s)
docker stop -t 20 "$name" >/dev/null
took=$(($(date +%s) - start))
docker logs "$name" 2>&1 | grep -q '^child: got SIGTERM$' || fail "(c) SIGTERM did not reach the child"
status=$(docker inspect --format '{{.State.ExitCode}}' "$name")
[ "$status" = 0 ] || fail "(c) the container exited with $status, want 0"
[ "$took" -lt 10 ] || fail "(c) the container took $took seconds to stop"
docker rm -f "$name" >/dev/null

# (d) The exit status of the child is the exit status of the container.
status=0
docker run --name "$name" -v "$tmp:/test:ro" "$image" /test/pid1 exit 3 >/dev/null 2>&1 || status=$?
[ "$status" = 3 ] || fail "(d) the container exited with $status, want 3"

echo ok
