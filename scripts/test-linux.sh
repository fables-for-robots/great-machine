#!/bin/sh
# Runs the tests on Linux as root, in a container: the ones that need root
# and the kernel, which skip themselves anywhere else (what the launcher's
# thread gives up, the store that a zathras mounts), and with them every
# other. The arguments are `go test`'s; without a package among them,
# every package is tested.
#
#   scripts/test-linux.sh                              # everything
#   scripts/test-linux.sh -count=1                     # everything, anew
#   scripts/test-linux.sh -run Mount -v ./storemount/
#
# The container is not privileged. It has what the app container of a pod
# has (deploy/pod.yaml): CAP_SYS_ADMIN, which a zathras mounts its store
# with, and no AppArmor profile, because the profile Docker gives a
# container denies every mount, whatever the capabilities. That is the
# appArmorProfile "Unconfined" of the pod. Where there is no AppArmor, as
# in Docker Desktop, the option changes nothing. That the tests pass in
# such a container is part of what they show.
#
# --init, because without it `go` is process 1 of the container and waits
# for no child but its own.
#
# It needs docker, and go for the place of the module cache. The build
# cache is kept in a volume, great-machine-go-build.
set -eu
cd "$(dirname "$0")/.."
# Without a package among the arguments every package is tested: the
# arguments are then flags of `go test` alone.
packages=no
for arg in "$@"; do
	case $arg in
	. | ./*) packages=yes ;;
	esac
done
[ "$packages" = yes ] || set -- "$@" ./...
exec docker run --rm --init \
	--cap-add SYS_ADMIN --security-opt apparmor=unconfined \
	-v "$PWD:$PWD" -w "$PWD" \
	-v "$(go env GOMODCACHE):/go/pkg/mod" \
	-v great-machine-go-build:/root/.cache/go-build \
	-e GOFLAGS=-buildvcs=false \
	golang:1.27 go test "$@"
