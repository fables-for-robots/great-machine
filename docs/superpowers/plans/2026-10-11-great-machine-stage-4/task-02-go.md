### Task 2: Go 1.27

The mount of a zathras (Task 10) is jaccard-nfs-nix-store's, whose NFS server is Buildbarn's, and those packages need Go 1.27. The module and the flake move to it here, by themselves, so that whatever the newer Go changes is seen on the tree as it is and not among the changes of a later task. **Nothing else changes in this task: no code, no dependency.**

**Files:**
- Modify: `go.mod` (the `go` line), `flake.nix` (the Go of the build and of the two shells)

**Interfaces:**
- Consumes: nothing of the other tasks. nixpkgs as `flake.lock` pins it has `go_1_27` (1.27.1).
- Produces: `nix develop .#bare --command go version` prints `go1.27.1`; every later task runs its Go commands so.

- [ ] **Step 1: See what the tree builds with now**

```bash
nix develop .#bare --command go version
```

Expected: `go version go1.26.` and a patch number: the Go of the pinned nixpkgs.

- [ ] **Step 2: The flake**

Change `flake.nix`:

```diff
--- a/flake.nix
+++ b/flake.nix
@@ -76,7 +76,15 @@
             nixpkgs.legacyPackages.${builtins.currentSystem}
           else
             null;
-          goEnv = gonixgo.lib.mkGoEnv { inherit pkgs evalPkgs; };
+          # Buildbarn's packages, of which the mount of a zathras is
+          # made, need Go 1.27, which is not yet the Go of this nixpkgs.
+          # The Go that resolves the packages while Nix evaluates has to
+          # be the same one.
+          goEnv = gonixgo.lib.mkGoEnv {
+            inherit pkgs evalPkgs;
+            go = pkgs.buildPackages.go_1_27;
+            evalGo = if evalPkgs != null then evalPkgs.go_1_27 else null;
+          };
 
           # The source gonixgo builds: the repository with the build of
           # the page at web/dist, where the package web embeds it.
@@ -164,7 +172,7 @@
           hardeningDisable = [ "all" ];
 
           packages = with pkgs; [
-            go
+            go_1_27
             sqlc
             (nodejs pkgs)
             self.packages.${system}.default
@@ -178,7 +186,7 @@
         bare = pkgs.mkShell {
           hardeningDisable = [ "all" ];
 
-          packages = with pkgs; [ go sqlc (nodejs pkgs) ];
+          packages = with pkgs; [ go_1_27 sqlc (nodejs pkgs) ];
         };
       });
     };
```

`evalGo` is the Go that gonixgo asks, while Nix evaluates, which files a package is made of. gonixgo takes `evalPkgs.go` for it when another machine than the one that builds evaluates (the Linux image asked for on a Mac), which would be 1.26 again, and gonixgo refuses two versions.

- [ ] **Step 3: The module**

```bash
nix develop .#bare --command go version
nix develop .#bare --command go mod edit -go=1.27.1
nix develop .#bare --command go mod tidy
git diff --stat
```

Expected: `go version go1.27.1`, and two files changed: `flake.nix`, and `go.mod` by one line (`go 1.27.1` in the place of `go 1.26.3`). `go.sum` does not change.

- [ ] **Step 4: Everything as before, on the new Go**

```bash
nix develop .#bare --command bash -c 'test -z "$(gofmt -l .)" && go vet ./... && go test ./...'
```

Expected: `ok` for the 30 packages that have tests, as before this task. While this plan was written that took 40 seconds on an arm64 Mac.

On Linux, in a container (the script of Task 8 is not there yet):

```bash
docker run --init --rm -v "$PWD":/src:ro -w /src -e GOFLAGS=-buildvcs=false -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomod golang:1.27 go test ./...
```

Expected: `ok` for the same packages.

- [ ] **Step 5: The program as Nix builds it**

```bash
nix build --option allow-unsafe-native-code-during-evaluation true
./result/bin/great-machine version
rm result
```

Expected: the commit, with `-dirty`. The first build with the new Go builds the standard library and every package again: a minute on the machine this plan was written on.

- [ ] **Step 6: Commit**

```bash
git add go.mod flake.nix
git commit -m "Build: Go 1.27, which the packages of a zathras's store need"
```
