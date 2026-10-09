### Task 11: Delivery: the image, the manifest and the README

**Files:**
- Modify: `.github/workflows/test.yml` (the step `image` asks the draal of the image for the page)
- Modify: `deploy/draal.yaml` (where the page listens, how it is reached, and that the port is not to be exposed)
- Modify: `README.md` (the section "The web page", the flag in draal's table, the security of the page, how the page is developed)

**Interfaces:**
- Consumes:
  - `scripts/test-page.sh --image IMAGE` (Task 5): starts the image's `draal` with another store over `/nix/store`, and fails unless it answers `GET /` and `GET /s/web` on the loopback address of the container with a page.
  - `packages.<linux>.image` (stage 1), whose program is `packages.<system>.default`, which since Task 4 is built from the repository with the page at `web/dist`. Nothing in the flake changes here.
  - `--web-addr` / `GREAT_MACHINE_WEB_ADDR` with its default `127.0.0.1:8080`, `--web-host` / `GREAT_MACHINE_WEB_HOST`, the log line `web page` with its `hosts=`, and the two warnings (Task 5).
  - Of `web` (Tasks 1 and 2), as the contract has them: the table "The host name" and its refusal, 403 with a plain line that names the host, says why and says what to use; 403 as a refusal with the code `unauthorized` for a WebSocket of another origin; 501 with a plain line when the page is not built. The README describes these lines and does not quote them.
  - The routes `/`, `/s/:service`, `/s/:service/z/:id` (the tasks of the page); `go run ./web/demo` (Task 3); the scripts `dev`, `check`, `build` of `ui/package.json`, `packages.<system>.ui` and the procedure of `npmDepsHash` (Task 4).
- Produces: nothing a later task uses. A person finds out whether a program has the page from what its draal logs at its start (the warning `the web page was not built into this program`) and from what it answers (501); `great-machine version` prints what it printed.

`.github/workflows/release.yml` needs no change: it builds `.#image`, the
same derivation the step `image` of `test.yml` builds and now asks for the
page, on every push before a tag can be made of it.

- [ ] **Step 1: CI asks the draal of the image for the page**

In `.github/workflows/test.yml`, the step `image` becomes:

```yaml
      # The image as a release builds it, and what a pod asks of it: that
      # it runs with another store mounted over /nix/store.
      - name: image
        run: |
          nix build .#image --option allow-unsafe-native-code-during-evaluation true
          docker load < result
          docker run --rm --tmpfs /nix/store ghcr.io/fables-for-robots/great-machine:nix version
          docker run --rm --tmpfs /nix/store --entrypoint sh ghcr.io/fables-for-robots/great-machine:nix \
            -c 'test -d /tmp && test -s /etc/ssl/certs/ca-certificates.crt'
          # The draal of the image has the page.
          scripts/test-page.sh --image ghcr.io/fables-for-robots/great-machine:nix
```

- [ ] **Step 2: Build the image, start its draal and ask it for the page**

On Linux:

```bash
GREAT_MACHINE_VERSION=v0.0.0-test nix build .#image --impure \
  --option allow-unsafe-native-code-during-evaluation true
docker load < result
rm result
```

On a Mac with Docker, the same build in a container of the Mac's own architecture, as stage 1's delivery did it (the page is built inside the container too, by the Linux Node):

```bash
out=$(mktemp -d)
docker run --rm -v "$PWD":/src:ro -v "$out":/out nixos/nix:2.26.1 sh -c '
  set -e
  cp -r /src /work && cd /work && rm -f result
  git config --global --add safe.directory /work
  GREAT_MACHINE_VERSION=v0.0.0-test nix --extra-experimental-features "nix-command flakes" \
    build .#image --impure --option allow-unsafe-native-code-during-evaluation true
  cp -L result /out/image.tar.gz'
docker load < "$out/image.tar.gz"
rm -r "$out"
```

Either way, then:

```bash
IMAGE=ghcr.io/fables-for-robots/great-machine:nix
docker run --rm --tmpfs /nix/store "$IMAGE" version
scripts/test-page.sh --image "$IMAGE"
docker ps -a --format '{{.Names}}' | grep great-machine-page || echo "no container left"
docker image rm "$IMAGE"
```

Expected: `Loaded image: ghcr.io/fables-for-robots/great-machine:nix` (the build in the container takes a few minutes the first time; among its lines are `building '/nix/store/…-great-machine-ui-0.drv'...` and `building '/nix/store/…-great-machine-src.drv'...`), then `v0.0.0-test`, `ok`, `no container left`, and the lines of the image being removed.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/test.yml
git commit -m "CI: the draal of the image is asked for the page"
```

- [ ] **Step 4: The manifest of draal**

In `deploy/draal.yaml`:

Replace

````yaml
# draal listens on no TCP port, and needs no Service: zathrases and
# operators reach it over iroh by its endpoint ID.
````

with

````yaml
# draal needs no Service: zathrases and operators reach it over iroh by
# its endpoint ID.
#
# The web page of draal listens on the loopback address of the pod, port
# 8080, where nothing outside the pod reaches it. A forward brings it to
# the laptop:
#
#   kubectl port-forward deployment/draal 8080:8080
#
# and then http://localhost:8080/. Do not expose the port with a Service
# or an Ingress: the page has no authentication, and whoever reaches it
# has a shell in every container.
````

Replace

````yaml
            # Put before the name of a store path as it is, to make its
            # reference. The same value the sidecars have.
            - name: JACCARD_PREFIX
              value: /great-machine/nix/store/
````

with

````yaml
            # Put before the name of a store path as it is, to make its
            # reference. The same value the sidecars have.
            - name: JACCARD_PREFIX
              value: /great-machine/nix/store/
            # Where the web page listens. This is the default, written
            # out: loopback, reached with `kubectl port-forward` (see the
            # top of this file). An empty value is no web page. Any
            # address that is not loopback opens the page, and with it a
            # shell in every container, to whatever can reach the pod.
            - name: GREAT_MACHINE_WEB_ADDR
              value: "127.0.0.1:8080"
````

Nothing else in the manifest changes: no `containerPort`, and no Service. `kubectl port-forward` reaches a port that is bound to the loopback address of a pod, and the browser then asks `localhost:8080`, a name the page answers to.

Run: `ruby -ryaml -e 'YAML.load_stream(File.read("deploy/draal.yaml")) { |d| puts d["kind"] }'` (or `kubectl apply --dry-run=client -f deploy/draal.yaml` where there is a kubectl)
Expected: `PersistentVolumeClaim` and `Deployment` (or `persistentvolumeclaim/draal-data created (dry run)` and `deployment.apps/draal created (dry run)`).

- [ ] **Step 5: Commit**

```bash
git add deploy/draal.yaml
git commit -m "Deploy: the web page of draal listens on loopback, and is reached by a forward"
```

- [ ] **Step 6: The README**

In `README.md`, 11 places, from the top of the file to its end. Each is given as what is there and what it becomes.

Replace

````markdown
A web page and a terminal UI that show the same are planned, and are not
there yet.
````

with

````markdown
draal also serves [a web page](#the-web-page) that shows the fleet, the
figures and the log of every zathras, and gives a shell in its container.
A terminal UI that shows the same is planned, and is not there yet.
````

Replace

````markdown
```sh
great-machine draal --data /var/lib/draal --operator OPERATOR_ID \
  --jaccard-server SERVER_ID --jaccard-prefix /great-machine/nix/store/
```
````

with

````markdown
```sh
great-machine draal --data /var/lib/draal --operator OPERATOR_ID \
  --jaccard-server SERVER_ID --jaccard-prefix /great-machine/nix/store/
```

Its [web page](#the-web-page) is at `http://127.0.0.1:8080/` on the
machine draal runs on. In the cluster that is the pod, and a forward
brings it to the laptop:

```sh
kubectl port-forward deployment/draal 8080:8080    # then http://localhost:8080/
```
````

Replace

````markdown
```sh
great-machine draal --data DIR --jaccard-server ID --jaccard-prefix P [--operator ID]...
```
````

with

````markdown
```sh
great-machine draal --data DIR --jaccard-server ID --jaccard-prefix P [--operator ID]... [--web-addr ADDR] [--web-host NAME]...
```
````

Replace

````markdown
| flag | environment | default | meaning |
| --- | --- | --- | --- |
| `--data DIR` | `GREAT_MACHINE_DATA` | required | holds `draal.key` (created on first run) and `draal.sqlite` |
| `--operator ID` | `GREAT_MACHINE_OPERATORS` | none | an endpoint ID that may act as an operator; may be repeated, and the variable takes several with commas between them |
| `--jaccard-server ID` | `JACCARD_SERVER` | required | the jaccard-store server that closures are pushed to |
| `--jaccard-prefix P` | `JACCARD_PREFIX` | required | what is put before the name of a store path to make its reference; the same value the sidecars are given |
| `--bind IP:PORT` | `GREAT_MACHINE_BIND` | every address, a port the system picks | the UDP address of the iroh endpoint |
````

with

````markdown
| flag | environment | default | meaning |
| --- | --- | --- | --- |
| `--data DIR` | `GREAT_MACHINE_DATA` | required | holds `draal.key` (created on first run) and `draal.sqlite` |
| `--operator ID` | `GREAT_MACHINE_OPERATORS` | none | an endpoint ID that may act as an operator; may be repeated, and the variable takes several with commas between them |
| `--jaccard-server ID` | `JACCARD_SERVER` | required | the jaccard-store server that closures are pushed to |
| `--jaccard-prefix P` | `JACCARD_PREFIX` | required | what is put before the name of a store path to make its reference; the same value the sidecars are given |
| `--web-addr ADDR` | `GREAT_MACHINE_WEB_ADDR` | `127.0.0.1:8080` | where [the web page](#the-web-page) listens, as `HOST:PORT`; empty for no web page |
| `--web-host NAME` | `GREAT_MACHINE_WEB_HOST` | none | a host name under which the web page is answered besides IP addresses and `localhost`, for a draal behind a proxy that authenticates; may be repeated, and the variable takes several with commas between them |
| `--bind IP:PORT` | `GREAT_MACHINE_BIND` | every address, a port the system picks | the UDP address of the iroh endpoint |
````

Replace

````markdown
At start draal logs its endpoint ID. Without `--operator` it warns that
nobody can act as an operator.
````

with

````markdown
At start draal logs its endpoint ID, and the URL of its web page with
the host names of `--web-host`. Without `--operator` it warns that nobody
can act as an operator. An address for the web page that is taken ends
the command with 1.
````

Replace

````markdown
```sh
echo uptime | great-machine shell web
```
````

with

````markdown
```sh
echo uptime | great-machine shell web
```

## The web page

draal serves a web page, on `http://127.0.0.1:8080/` unless `--web-addr`
says otherwise:

| view | address | shows |
| --- | --- | --- |
| services | `/` | every service: its stable generation, its zathrases by phase, the last rollout and how far it is |
| a service | `/s/SERVICE` | its zathrases (name, phase, generation, time running, restarts, CPU and memory), the rollout under way with every zathras' part in it, and the last 20 rollouts |
| a zathras | `/s/SERVICE/z/ID` | its state in full; CPU, memory, disk and network over the last 15 minutes; the last 400 lines of its log, followed as they come; a shell in its container |

The page is what `status`, `logs` and `shell` are on the command line.
**It can roll nothing out and nothing back**: a rollout needs the Nix
store of the laptop, and the page has no authentication.

It draws from one connection to draal, says so when that connection is
lost, and connects again. It asks nothing of any other host: its scripts,
styles and fonts are in the program.

**How it is reached.** On the machine draal runs on, by its URL, which
draal logs at its start. In the cluster the page listens on the loopback
address of draal's pod, where nothing outside the pod reaches it, and a
forward brings it to the laptop:

```sh
kubectl port-forward deployment/draal 8080:8080
```

then `http://localhost:8080/`.

**The page has no authentication, and whoever can reach its port has a
shell in every container.** What stands in the way is where it listens,
and the name it is asked under:

- **Loopback.** The default address is `127.0.0.1:8080`. An address that
  is not loopback, such as `:8080`, is allowed, and draal warns about it
  at its start. Do not put a Service or an Ingress in front of the port.
- **The host name.** draal answers a request by the name it was asked
  under, the `Host` of the request without its port:

  | asked as | answered |
  | --- | --- |
  | a loopback IP address, or `localhost` | always |
  | another IP address | when draal does not listen on loopback |
  | a name given with `--web-host` | always |
  | any other name | never, wherever draal listens |

  Capitals and a dot at the end of a name make no difference. Everything
  else is refused with `403` and a line that names the host that was
  asked for, says why, and says what to use: the URL on loopback, or an
  IP address or `--web-host`. draal logs the first such refusal as a
  warning.

  The reason for the last row: a name that draal was not told is a name
  anyone can point at draal's address. A web site does that with a name
  of its own while its page is open in the operator's browser (DNS
  rebinding), and its page is then of the same origin as draal's and may
  open a shell. An IP address cannot be pointed anywhere.
- **The origin.** A WebSocket, which is how the page gets the fleet, the
  logs and the shell, is refused with `403` when the page that opens it
  was not loaded from the host it is opened on. That keeps another web
  site that is open in the same browser from opening a shell.

**Behind a proxy.** `--web-host NAME` is for a draal behind a proxy that
authenticates whoever it lets through, and passes requests on under the
name it is reached by:

```sh
great-machine draal … --web-addr 127.0.0.1:8080 --web-host draal.example.com
```

The name is a host name without a port. The proxy is then all the
authentication there is. **A proxy that does not authenticate, or one
that rewrites `Host` to `127.0.0.1`, puts a shell in every container on
the network it listens on**: the first lets anyone in under the name, and
the second makes every request look like one from the machine itself.

`--web-addr ""` (or an empty `GREAT_MACHINE_WEB_ADDR`) turns the page off:
nothing listens.

A program that was built with `go build` alone has no page in it, since
the page is built by Node (see [Development](#development)). Its draal
warns about that at its start, and answers `501` with a line that says
how the page is built. The program that Nix builds, and the image, have
it.
````

Replace

````markdown
- **Operators are an allowlist.** draal does what an operator command
  asks only for the endpoint IDs it was given with `--operator`. The
  operator's key file is the credential: whoever has it can roll out, and
  has a shell in every container.
- **Registration is open.** Any peer that knows the endpoint ID of draal
  can register as a zathras of any service. It is then listed with the
  others, is sent the path and the arguments of what the service runs,
  and can halt a rollout by failing it. Treat the endpoint ID of draal as
  something the pods know and the world does not, and put no secret in
  the arguments of a service.
- **What draal takes from a registrant is bounded.** A registration is
  refused unless the name is 1 to 253 bytes of letters, digits, `.`, `_`
  and `-` and the version at most 64 bytes of printable ASCII. What a
  zathras reports is cleaned of control characters and cut before draal
  keeps or shows it (the binary at 4 KiB, the arguments at 128 of 1 KiB,
  the reason of a failure at 4 KiB), a report that is not of the protocol
  ends the connection, and draal keeps at most 900 samples of a zathras,
  twice what 15 minutes bring. What draal does not bound is how many register:
  a peer can register any number of zathrases, every one is sent to
  every watcher, and a fleet that comes to more than a message holds
  (4 MiB) ends `status` and the following of a rollout, which draal
  logs.
- **The jaccard-store server** takes a push from anyone who can reach it,
  and the sidecar's preload endpoint has no authentication, which is why
  it listens on the loopback address of the pod.
- A shell runs as the user of the app container, with everything the
  service can see.
````

with

````markdown
- **Operators are an allowlist.** draal does what an operator command
  asks only for the endpoint IDs it was given with `--operator`. The
  operator's key file is the credential: whoever has it can roll out, and
  has a shell in every container.
- **The web page has no authentication.** Whoever can reach its port can
  read every log and has a shell in every container, though no rollout.
  It listens on loopback unless told otherwise, answers to no host name
  but `localhost` and those it was given with `--web-host`, and is reached
  through `kubectl port-forward`: see [The web page](#the-web-page). The
  port must not be exposed by a Service, and a proxy in front of it has
  to authenticate.
- **Registration is open.** Any peer that knows the endpoint ID of draal
  can register as a zathras of any service. It is then listed with the
  others, is sent the path and the arguments of what the service runs,
  and can halt a rollout by failing it. Treat the endpoint ID of draal as
  something the pods know and the world does not, and put no secret in
  the arguments of a service.
- **What draal takes from a registrant is bounded.** A registration is
  refused unless the name is 1 to 253 bytes of letters, digits, `.`, `_`
  and `-` and the version at most 64 bytes of printable ASCII. What a
  zathras reports is cleaned of control characters and cut before draal
  keeps or shows it (the binary at 4 KiB, the arguments at 128 of 1 KiB,
  the reason of a failure at 4 KiB), a report that is not of the protocol
  ends the connection, and draal keeps at most 900 samples of a zathras,
  twice what 15 minutes bring. What draal does not bound is how many register:
  a peer can register any number of zathrases, every one is sent to
  every watcher, and a fleet that comes to more than a message holds
  (4 MiB) ends `status` and the following of a rollout, which draal
  logs.
- **The jaccard-store server** takes a push from anyone who can reach it,
  and the sidecar's preload endpoint has no authentication, which is why
  it listens on the loopback address of the pod.
- A shell runs as the user of the app container, with everything the
  service can see.
````

Replace

````markdown
In the dev shell are `go`, `sqlc`, and `great-machine` as built from the
tree. With direnv, `.envrc` passes the option and reloads the shell,
rebuilding the program, when the sources change. Nix sees only the files
git tracks, so `git add` new ones.
````

with

````markdown
In the dev shell are `go`, `sqlc`, `node` with `npm`, and `great-machine`
as built from the tree, with the web page in it. With direnv, `.envrc`
passes the option and reloads the shell, rebuilding the program, when the
sources of the program or of the page change. Nix sees only the files git
tracks, so `git add` new ones.
````

Replace

````markdown
The default shell cannot be entered while the Go module does not resolve,
because building the program is part of it: after a `go get` that left
`go.sum` behind, for example. `nix develop .#bare` has `go` and `sqlc`
without the program and needs no option; run `go mod tidy` there.
````

with

````markdown
The default shell cannot be entered while the Go module does not resolve,
because building the program is part of it: after a `go get` that left
`go.sum` behind, for example. `nix develop .#bare` has `go`, `sqlc` and
`node` without the program and needs no option; run `go mod tidy` there.
````

Replace

````markdown
The tests need no cluster and no network beyond the loopback interface.
Those in `e2e` run draal and several zathrases in one process, with real
child processes for the services. `scripts/test-pid1.sh` runs zathras as
the first process of a container, and takes Docker.
````

with

````markdown
The tests need no cluster and no network beyond the loopback interface.
Those in `e2e` run draal and several zathrases in one process, with real
child processes for the services. `scripts/test-pid1.sh` runs zathras as
the first process of a container, and takes Docker.

### The page

The sources of the web page are in `ui/`: SolidJS and TypeScript, built
by Vite. `ui/` has a `go.mod` of its own, which holds no Go and keeps the
Go tools out of the directory. The package `web` serves the page from
`web/dist`, which it embeds, and has the HTTP API the page talks to.

```sh
cd ui
npm ci           # the dependencies, as package-lock.json names them
npm run check    # the type check
npm run build    # into ../web/dist; `go build` after it has the page
```

`web/dist` is not committed, but for a placeholder that keeps the
directory there. **The Go module builds and its tests pass without the
build**; a program built so answers `501` in place of the page. Nix needs
no `npm` by hand: `nix build` builds the page in a derivation of its own
(`.#ui`) and the program from the tree with that build at `web/dist`.
Evaluating the flake therefore builds the page, the first time and after
every change in `ui/`.

To work on the page, run the API over a made-up fleet in one terminal and
Vite in another:

```sh
go run ./web/demo           # the real API on 127.0.0.1:8080, no draal and no cluster
cd ui && npm run dev        # the page on http://localhost:5173/, reloaded as it is edited
```

Vite passes `/api`, WebSockets included, on to `127.0.0.1:8080`, which
can be a real draal as well.

After a change of the dependencies (`npm install …`, which rewrites
`ui/package-lock.json`), the flake has to be told their new hash:

```sh
nix run nixpkgs#prefetch-npm-deps ui/package-lock.json    # prints npmDepsHash for flake.nix
```

With a stale hash `nix build .#ui` fails and names the right one in its
`got:` line. The shells and the derivation have the same Node, 24.
````

Replace

````markdown
`.github/workflows/test.yml` runs on every push and pull request: `gofmt`,
`go vet`, the tests, `sqlc diff` (the generated queries are what the SQL
says), zathras as the first process of a container, the Nix build and the
image.
````

with

````markdown
`.github/workflows/test.yml` runs on every push and pull request: `gofmt`,
`go vet`, the tests, `sqlc diff` (the generated queries are what the SQL
says), the type check and the build of the page and the tests of what
serves it once more with it, zathras as the first process of a container,
the Nix build and the image. `scripts/test-page.sh` asks the program that
Nix built, and the draal of the image, for the page.
````

- [ ] **Step 7: Read it against the program**

```bash
grep -n "not there yet\|no TCP port" README.md deploy/draal.yaml
grep -c "(#the-web-page)" README.md
grep -n "^## The web page\|^### The page\|web-addr" README.md
```

Expected: one line from the first command, the sentence about the terminal UI (`A terminal UI that shows the same is planned, and is not there yet.`); `4` from the second, the lines that link to the section; and from the third the heading `## The web page` between `### Operator commands` and `## Rollout`, the heading `### The page` under `## Development`, and `--web-addr` in the usage line of draal, in its table and in the section of the page. `grep -n web-host README.md` finds `--web-host` in the usage line, in the table, in the table of host names and under **Behind a proxy**, and in the section on security.

Then do what the README says, as a reader would. Build the page and the program, start draal and open the page:

```bash
nix build --option allow-unsafe-native-code-during-evaluation true
./result/bin/great-machine draal --data "$(mktemp -d)" \
  --jaccard-server 0000000000000000000000000000000000000000000000000000000000000000 \
  --jaccard-prefix nix/
```

Expected: among what it logs, `msg="web page" addr=127.0.0.1:8080 url=http://127.0.0.1:8080/` and no warning about the page. Open the URL in a browser: the view of the services, which says that there is no service yet. Then, in another terminal, the refusals the README describes:

```bash
curl -si -H 'Host: draal.example.com' http://127.0.0.1:8080/ | head -n 12
curl -si -H 'Origin: http://evil.example.com' -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
  -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
  http://127.0.0.1:8080/api/watch | head -n 1
```

Expected: `HTTP/1.1 403 Forbidden` with a plain line that names `draal.example.com`, says why it is refused and what to use (the URL on loopback, or `--web-host`); and `HTTP/1.1 403 Forbidden`, whose body is the refusal `{"error":{"code":"unauthorized",…}}`. The first also left a warning in what draal logs. Stop draal with Ctrl-C (it logs `stopping` and ends with status 0).

Then the name a proxy would pass on. Start the same draal with `--web-host Draal.Example.COM` added, and:

```bash
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: draal.example.com' http://127.0.0.1:8080/
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: other.example.com' http://127.0.0.1:8080/
```

Expected: the line of the log `msg="web page" addr=127.0.0.1:8080 url=http://127.0.0.1:8080/ hosts=draal.example.com`, then `200` and `403`. Stop draal, and `rm result`.

If what `web` answers reads differently from what the section "The web page" says of it, the README is what gets corrected, in this step.

- [ ] **Step 8: Commit**

```bash
git add README.md
git commit -m "README: the web page"
```
