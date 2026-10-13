# The ledger of the execution of stage 5

Kept by the session that wrote the spec and the plan and carried the plan out itself, in the order things happened: what each task ran and found, every ruling that was made (lines with `Ruling:`), the review of the whole branch with its one pass of mends, and every finding that was deferred (lines with `minor (deferred)`). The commits it names are on the branch `stage-5`.

Plan: docs/superpowers/plans/2026-10-13-great-machine-stage-5.md

Spec: docs/superpowers/specs/2026-10-13-great-machine-stage-5-design.md. Branch: stage-5, in the checkout. Started at 7700355.

## Setup

- Ruling: the work is done on the branch `stage-5` in the checkout, not in a worktree — stages 1 to 4 were carried out so and the plan names the branch — if wrong: nothing; the branch is local.
- Ruling: Tasks 3 (the launcher) and 7 (the documents) were carried out by two forks of the session, each in a worktree of its own, while the session carried out Tasks 1 and 2; their commits were cherry-picked, and the worktrees and their branches removed — the tasks share no file with Tasks 1 and 2, and the time the stage took mattered more than what it cost — if wrong: a conflict to resolve in `cli/zathras.go`, of which there was none.
- Pre-flight: Task 4 consumes `storedir.Config`, `Open` and `Close` of Task 2 and the launcher of Task 3, which has no `Close`; Task 5 consumes Tasks 2 and 4. No conflict.

## The tasks

**Task 1, a reference written out** (f52aeb6). The tests were written first and failed to build; with `write.go` they pass, on macOS and, as root, in the container of `scripts/test-linux.sh`.

- Settled, the first of the spec's §11: a tree with nested directories, files with and without an execute bit, an empty file, one of 3 MiB and symbolic links to a file, to nowhere and out of the tree is imported as a push imports it and written out as it was, with the modes of the spec's §3.3 and the times it was imported with.

**Task 2, the store of a zathras** (57e41ce, f0c9fc9). The tests were written first and failed to build; with the store they pass, with `-race -count=3` on macOS and with `-race` on Linux as root.

- Settled, the second of the spec's §11, by `TestAPackTheCacheSaves`: a store path that was pushed after one it leans on is pulled with 2 packs into an empty cache and with 1 into a cache that holds the other. The pack cache stays.
- Ruling: there is no partial directory; a store path is written beside its name — `rename(2)` of a directory nobody may write to into another directory needs write permission on it, for its `..`, which a zathras that is not root has not, and neither has root without `CAP_DAC_OVERRIDE`; within one directory it does not — if wrong: nothing in behaviour. The spec's §3.1, §3.2, §3.5 and §10 and the plan were reworded (f0c9fc9).
- Ruling: `own()` sets the mode of the store directory and of the cache to exactly `0755`, where `storemount`'s only took the write bits of the group and of the others away — a service of `--user` has to read `/nix/store`, and the spec says `0755` — if wrong: a mode an operator had narrowed on the cache is widened.
- Ruling: `Config` has an unexported `wrapDial` for the tests, which count, hold up and fail what the store asks of its connections, in the place of `storemount`'s package variable — if wrong: nothing outside the tests.

**Task 3, the launcher** (6be5b21, by a fork). `go test ./...` on macOS; `scripts/test-linux.sh` for `launcher`, `supervisor`, `ptyshell`, `cmd` and `cli` in a container with no capability added.

- Its decisions, which the plan did not spell out: `Start` no longer returns `cmd.Err` itself, since `cmd.Start` does; `New` warns and goes on when the bounding set cannot be read; `cli/main_test.go` and `e2e/main_test.go` are deleted, as they held the `TestMain` of `exec-in-place` alone; the `NoNewPrivs` assertions of `cli/zathras_service_test.go` are gone, since the container sets it now and zathras does not.

**Task 4, zathras opens a storedir** (88c900f). `TestRunZathrasOpensTheStoreItReallyHas` failed first, with `storemount`'s "on Linux alone", and passes. `scripts/test-linux.sh -count=1`: 33 packages, in a container with no capability added.

- Ruling: `zathrasConfig.startStore` is `openStore`, and the fake store of the tests of `cli` opens and closes — the store is opened and closed now, not started and stopped — if wrong: names in tests.
- Ruling: `e2e/mount_linux_test.go` is deleted in this task's commit and not in Task 5's — `go mod tidy` cannot drop jaccard-nfs-nix-store while a test imports `storemount`, and every commit is to build — if wrong: nothing.
- `go.mod` and `go.sum` name neither jaccard-nfs-nix-store nor a package of Buildbarn.

**Task 5, the rollout with nothing standing in** (a8d176a). `TestARolloutIntoAStoreDirectory` passed at its first run: it is the mount's test carried over to parts that existed. It asserts that the store directory is empty before the rollout and holds the one store path after it, and runs on macOS without root and on Linux as root.

**Task 6, the pod, the script and the build** (e96fc13). Both manifests parse. `nix build --option allow-unsafe-native-code-during-evaluation true` builds the program on macOS, and `great-machine version` and `great-machine zathras --help` run from it. The image was not built: it is built on Linux alone, and the workflow builds it.

**Task 7, the documents** (6a79366, bdb53ae, by a fork). Read against the code: the pod of the README is `deploy/pod.yaml`'s, `-run User` names tests that are there, and the name of what is being written is the code's.

## The review of the branch

A reviewer that had seen nothing of the execution read the branch at bdb53ae against the spec and the plan, on the most capable model. It found nothing critical and said: ready to merge, with fixes. It read `storedir`, the launcher's code, `cli/zathras.go`, the manifests, the scripts and the README in full; of `e2e/storedir_test.go`, the tests of the launcher and the commit of the design it says it read little, and those were read by the session.

One pass of mends, in bbe70f0, each with a test that failed first:

- Final: fixed, a store that cannot be dialed was dialed once for every fetch, one after another (Important) — twenty store paths were given up after about ten minutes where the spec says two, and fetches nobody waited for went on dialing in turn and held their places. A dial is one for all who wait for it — `TestADialThatFailsIsEverybodysWhoWaitedForIt`, `TestFetchesNobodyWaitsForDoNotWaitForADial` RED→GREEN.
- Final: fixed, no test failed a pull on a connection that was there (Important) — `TestAPullThatFailsCostsItsConnection`, which passed at once and was shown to fail when the drop of the connection is taken out; `TestPreloadOfAStorePathThatHoldsAFIFO`, which passed at once and covers what was there.
- Final: fixed, an attempt that took longer than it may kept its connection (Minor, graded up: the next attempt hung on the connection that had hung) — `TestAnAttemptThatTakesTooLongCostsItsConnection` RED→GREEN.
- Final: fixed, a store path whose name is longer than 232 bytes could not be written, as what is being written had its name behind a prefix of 23 (Minor, graded up: a closure with such a name could not be rolled out at all). What is being written is named by a number — `TestAStorePathWithTheLongestNameThereIs` RED→GREEN.
- Final: Ruling: after a sync of the store directory that failed, the store path was removed under its name; the reviewer proposed to move it back first. It is not removed at all — it is whole and was on disk before it was moved, so a name that outlasts a loss of power names a whole store path, and one that does not is fetched again; the fetch fails with what the disk said — if wrong: a store path stays that a failing disk may have damaged, which the next read finds — `TestAMoveThatCouldNotBeSyncedLeavesTheStorePath` RED→GREEN.
- Final: fixed, the session's own finding: the connection to the store was closed whenever no pull was under way, so store paths that were fetched one after another were each dialed for. It is closed when no fetch is under way, as the spec says — `TestTheConnectionLastsAsLongAsFetchesAreUnderWay` RED→GREEN.

After the pass: `go test -count=1 ./...` on macOS, 33 packages; `scripts/test-linux.sh -count=1`, 33 packages; `go test -race -count=3 ./storedir/`; gofmt and vet clean for both systems.

Deferred:

- Final: minor (deferred): one `syncfs` for every store path, up to twenty at a time, syncs the whole file system the volume is on, the node's for an `emptyDir`: a closure of some hundred store paths is some hundred of them. The spec asks for it and the mount did the same; one sync for a `prepare`, before the moves, would do.
- Final: minor (deferred): a pod left with the `securityContext` of v0.2.0 whose service has `--user` is not warned. v0.2.0 gave such a service `no_new_privs` and a bounding set without `CAP_SYS_ADMIN`; v0.3.0 in that pod gives neither, and warns of a service that is root alone, as the spec's §4.3 says.
- Final: minor (deferred): the branch of `own()` that changes the owner of a directory has no test.
- Final: minor (deferred): any failure of a pull that is no answer of the store costs the connection, also one that cannot be the connection's, a full disk under the pack cache among them. It costs a dial.

What the reviewer set aside, and how each was ruled:

- Final: Ruling: a pod's disk holds every store path twice and nothing is removed — the spec's §12 — stands.
- Final: Ruling: fetching is open and the store's connection has a key of its own — stage 6, the spec's §13 — stands.
- Final: Ruling: off Linux the directories of a store path are not synced — no pod runs there — stands.
- Final: Ruling: what a pack cache is after a loss of power was not looked into; Core's packstore syncs what it writes and recovers by itself — if wrong: a zathras whose cache was damaged ends at its start until the pod is replaced.
- Final: Ruling: whatever is under a name in the store directory is taken for that store path, a link that points nowhere among it; a service that is root can write the store and the cache — the spec's §3.1 and §4.1 — stand.
- Final: Ruling: a `prepare` can wait an hour on a connection that hangs without breaking — the time is the spec's, and draal ends a `prepare` after 30 minutes — stands.

## What is left

- Task 8, on a cluster by hand, after the pull request is merged and v0.3.0 is released: both wait for the human partner's word, and so does the push of this branch.
