# The ledger of the execution of stage 6

Kept by the session that wrote the spec and the plan and carried the plan out itself, in the order things happened: what each task ran and found, every ruling that was made (lines with `Ruling:`), the review of the whole branch with its one pass of mends, and what was left.

Plan: docs/superpowers/plans/2026-10-13-great-machine-stage-6.md

Spec: docs/superpowers/specs/2026-10-13-great-machine-stage-6-design.md. Branch: stage-6, in the checkout. Started at 3eff91e.

## The tasks

- Ruling: work on the branch stage-6 in the main checkout; Tasks 9, 10, 11 and 12 are carried out by forks in worktrees of their own and cherry-picked, as stage 5 did — they share no file with Tasks 2 to 8 but cli/draal.go and cli/app.go, which are merged by hand — cost if wrong: a conflict to resolve.
- Ruling: the human partner said "go and implement it" to the spec; the plan was written and carried out without a second question, as in stage 5.
- Task 1: complete (commit 82d9679: wire, operator, demo; tests written first and seen to fail to build; go test ./wire/ ./operator/ ./demo/ ok)
- Pre-flight: Task 5 consumes node.Again/LoadKey/Sign of Task 4; Task 6 consumes db (2) and authz (3); Task 7 consumes authz (3); Task 8 consumes all. Tasks 9-11 consume Task 1 alone. No conflict found.
- Task 2: complete (db: grants; tests written first, failed to build, pass; sqlc diff clean)
- Task 3: complete (authz: Authority; tests first, failed to build, pass)
- Task 4: complete (node: Again, LoadKey, Sign, Verify; tests first; go test ./node/... ok)
- Task 4: Ruling: the pull of jaccard-store's client over a connection Again made is not in node's tests but in e2e's (the rollout with nothing standing in), which has a draal that answers both protocols — node's tests would need storeserver, which is built on node — cost if wrong: the third of the spec's §15 is settled two tasks later.
- Task 4: a test of mine had the listener accept once: a connection of another protocol is handed to its handler only while Accept is called. The test accepts all along now, as draal does.
- Task 5: complete (zathras statement, storedir.Config.Dial, cli --role-key; tests first; go test ./cli/ ./e2e/ ./storedir/ ./zathras/ ok)
- Task 5: settled the third of the spec's §15: TestARolloutIntoAStoreDirectory pulls with jaccard-store's client over a connection node.Again made from the pod's connection to draal.
- Task 10: carried out by a fork (3d80866), cherry-picked. Its notes for the API: /api/me answers 401 for a stale cookie and 200 with no grants for a key that has none; /access is served the page; DELETE always carries service=; the Store link is for admins alone.
- Task 7: complete (storeserver: per service, Drop, Page; tests first; go test ./storeserver/ ok). Done before Task 6: the allowlist that cli and e2e still give the store answers the new operations as it answered the old write.
- Task 6: complete (draal by grants; tests first; go test ./... ok at this commit)
- Task 6: Ruling: a revocation does not end the connection it was asked on — it is an admin's and stays one (an admin cannot revoke its own admin grant), and the admin is to read the answer; the page's fork found that a revocation of the page's own key would otherwise lose its answer — cost if wrong: one connection of an admin outlives a revocation of another grant of its key.
- Task 6: Ruling: the grants a test starts with are put into its database and not into the authority alone — a grant or a revocation reloads the authority from the database — cost if wrong: none outside tests.
- Task 6: finding: the IDs of node/nodetest are not all endpoint IDs (not every 64 hexadecimal characters are a point of the curve), so the keys the access tests grant are real ones.
- Task 6: a store's refusal (unauthorized) is retried by storedir like any other failure of a pull, for its two minutes: a zathras whose registration lapsed registers again meanwhile.
- Task 8 (first part): `great-machine access list|grant|revoke` and draal's --admin (06df519). --operator is a hidden flag of its own and not an alias: the library refuses two forms of one flag on one command line, and an upgrade may well have both. The usage of a missing argument names the whole command (commandPath), as the library's FullName does not.
- Task 9: carried out by a fork (525937a, 53f99ca), cherry-picked as 864f8f8 and cd43df2. Not verified by it: the Nix image's busybox (no Nix on macOS), and a mutation check of the host rule.
- Task 11: carried out by a fork (105ff2e), cherry-picked as 4c0cc55. In Access `g` grants, and no key jumps to the first grant.
- Task 12: carried out by a fork (b414ff4, a493e1a, 13e7734), cherry-picked as 436d6f3, 0c1cf4a, 335eb06; its list of what to check against the code is gone through after Task 8.
- Task 8: complete (commits 06df519, d6924c7, 118524f, cff01e0: access commands, --admin, the allowlist gone, e2e/access_test.go, storeserver.Admin gone; go test -count=1 ./... ok, both vets clean, sqlc diff clean)
- Task 8: Ruling: the three e2e tests of access were written after what they test, which Tasks 6 and 7 built test-first in draal and storeserver: they passed at their first run. That they can fail was shown by taking the drop out of a revocation (two of them failed, by name) — cost if wrong: an e2e test that asserts less than it reads.
- Task 8: Ruling: draal.Controller stays: the handler of the protocol is written against it and the demos implement it; only its comment named the HTTP API — cost if wrong: an interface with one production implementation.
- Tasks 9-12 reconciled: the page's requests (ui/src/api/client.ts) against web's routes — /api/me, /api/grants GET, POST and DELETE with service= always, 401 as signed out, /access answered with the page, /store/ — agree. The README against the code: the host rule with the port, 501 without a build, the keys of the terminal UI, the columns of access list, the order of the upgrade (a draal of v0.3.0 reads a register with a statement: the codec refuses no unknown field). One correction: the form of the terminal UI has no service for an admin (README).

## The end to end tests that were added after Task 8

- Access through the page (`TestHTTPAccessAndThePageOfTheStore`) and through the terminal UI (`TestTUIAccess`, `TestTUIAccessOfAKeyThatIsNoAdmin`), which the forks of Tasks 9 and 11 left for the session: both against a real draal.
- The first run of the whole suite on Linux found a test of mine that asked the zathras's end of a connection whether it was closed at the instant the revocation was answered. draal has closed its end by then, and the other learns so when that has reached it: the test waits for it.
- The Access view of the page was looked at in a browser, from the built page, over the made-up fleet of `web/demo`: a grant through the form and a revocation after its question.

## The verification before the review

- `gofmt -l .` nothing; `go build ./... && go vet ./...`; `CGO_ENABLED=0 GOOS=linux go vet ./...`; `go test -count=1 ./...` every package ok; `scripts/test-linux.sh -count=1` (Docker, root, no capability added) 33 packages ok; `sqlc diff` nothing.
- The page: `npm run check`, `npm test` (155 pass), `npm run build`; `scripts/test-page.sh` on the program built with the page: ok. What was built was removed.
- Not verified: the image of Nix (no Nix build of it on the Mac of the session); the continuous integration builds it.

## The review of the whole branch

One fresh reviewer, on the most capable model, over 3eff91e..93ace3f, with the spec, the plan, the Review Focus and this ledger. It read the authorization, the store, the statement, the guard of the page and the command lines in full, and did not read `tui/`, `ui/`, `web/shell.go`, most of `web/socket.go`, `demo/`, the manifests and the design document. No Critical finding; five Important, all taken into the one pass of mends:

- Final: fixed "the documented grant command fails": `great-machine access grant ROLE ID --service NAME` was refused as a flag behind the arguments, and a test of mine held that refusal — `TestAccessGrantWithItsFlagsBehindTheArguments` RED→GREEN (28cadbc), suite green.
- Final: fixed "a registration can race the revocation of its role key and survive it": the role key is asked about again under the lock under which a revocation ends the sessions it finds — `TestARegistrationThatARevocationOvertakesIsNotLetIn` RED→GREEN (f0404e1); the test makes the revocation at the line draal logs between the two moments, so it is the race itself and not a likeness of it.
- Final: fixed "a grant and a revocation are not asked about again under the lock they are made under": two admins that revoke each other at once were both made — `TestAGrantAndARevocationAreAnAdminsWhenTheyAreMade` RED→GREEN (f0404e1).
- Final: fixed the two gaps of the Review Focus: `TestRunDraalWithNoAdminSaysSo`, and a shell that is open when its key's grant is revoked, in `e2e` (de372b6). Both hold what was built and passed at once; the second failed first for a fault of the test, which did not read the shell to its end as `Shell.Wait` asks.
- Final: Ruling: "a deployer of one service can read another's closure through the store's deduplication" — true, and not mended in this stage: the store shares its packs between the services, by design since stage 4, and jaccard-store knows of no services, so a push that names a root the store has is given a reference to it, and the answer to a push names the nearest packs, whoever's. What was false was the README, which said a deployer or a zathras of one service is given nothing of another. It says now what holds (no reference of another, and no change of one; no closure kept secret from whoever may push), the spec says so in §6, and `TestThePacksOfTheStoreAreSharedBetweenTheServices` holds what the store does (468c9ad). Keeping the packs apart takes a store that knows of services — if wrong: an operator who took the roles for a wall between the code of two teams finds that a deployer of one reads the store paths of the other; nothing of this lets it run, change or look into the other service.
- Final: Ruling: the reviewer's smaller note with the registration race, that a zathras leaves the fleet a moment after the revocation that closed its connection was answered, and is until then let pull what it could pull a moment before — stands; the connection is closed when the revocation is answered, and the bookkeeping follows it — if wrong: one more pull of its own service's store paths by a zathras whose role key was revoked.

Minor, not taken into the pass:

- Final: minor (deferred): the cookie of the page is `SameSite=Strict`, so a link that is clicked on a web page (a terminal in a browser, a chat) may be answered 401 once; `Lax` would do, as every change and socket is checked by its origin.
- Final: minor (deferred): the cookie is sent to every server on `localhost`, and a program that is no browser could send it on with a forged Host and Origin; the README names whoever can read the link, and not this.
- Final: minor (deferred): the store's handler takes connections a moment before draal is told who is registered; it refuses meanwhile.
- Final: minor (deferred): `node.LoadKey` drops the name of the operation from the error of a missing file; the path is in the message.
- Final: minor (deferred): a key with no grant can hold the streams of its `me` requests open, up to the most a connection has; an idle connection costs as much.

What the reviewer set aside, and how each was ruled:

- Final: Ruling: signed URLs of the bucket that were handed out before a revocation stay good until they expire (an hour) — jaccard-store's — stands.
- Final: Ruling: a statement never expires, and a service that is root reads its role key — the spec's §4.2 and §4.4 — stand.
- Final: Ruling: a role key that was granted for every service lets its zathrases register as any — the spec's §4.2 — stands.
- Final: Ruling: the ticket is good for as long as the command runs, and may be in the history of a browser — the spec's §8.3 — stands.
- Final: Ruling: whether jaccard-store's own page escapes the names of references when it is shown under the page's origin was not looked into; the page's policy lets scripts of its own origin alone run — if wrong: a deployer could have a name of a reference drawn as markup on an admin's page of the store.
- Final: Ruling: no bound on how many zathrases one role key signs for — the README says so — stands.
- Final: Ruling: two protocols from one endpoint over a relay were not tried here (the spec's §15.1 says so); the run on the cluster tries them — if wrong: a pod that reaches draal over a relay alone fetches nothing.
- Final: Ruling: an operator's program of before v0.4.0 against a draal of v0.4.0 — the README says the program changes with draal — stands.
- Final: Ruling: `--addr [::1]:PORT` prints a link under `localhost`, which a resolver may not make `::1` — not tried — if wrong: the link of a page that listens on `::1` alone does not open, and `http://[::1]:PORT/` with the ticket does.

## What is left

- Task 13, on the cluster: the human partner said, when the review was under way, to open the pull request, to merge it and to roll it out to shipyard.
