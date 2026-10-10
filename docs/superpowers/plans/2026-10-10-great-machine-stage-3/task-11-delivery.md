### Task 11: Delivery

The README says what the terminal UI is and how it is used, the program's help and the README agree, and everything is run once more.

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: `great-machine tui` (Task 9), `go run ./tui/demo` (Task 7), the keys and the views (Tasks 5 and 6), the shell (Task 8), the tests of the whole (Task 10).
- Produces: the section "The terminal UI" of the README. Nothing in the image, the manifests or the workflows changes: the terminal UI is part of the one program, it opens no port, and its tests run in the `test` step of `.github/workflows/test.yml` (`go test ./...`), on Linux, in pseudo-terminals of their own.

- [ ] **Step 1: The README: the section**

In `README.md`, put a new section between the end of "The web page" and `## Rollout`. It is the file `README.md` from the line `## The terminal UI` to the line before `## Rollout`; here it is, in a fence of four backticks, since it has a fenced block of its own:

````markdown
## The terminal UI

```sh
great-machine tui --draal ID
```

shows in a terminal what the web page shows, and follows it. It talks to
draal as an operator, as `status`, `logs` and `shell` do: it takes
`--draal` and `--key`, draal lets it in by its allowlist, and it needs
neither the web page nor a forwarded port.

| view | shows |
| --- | --- |
| services | every service: its stable generation, its zathrases by phase, its last rollout and how far it is |
| a service | its zathrases as a table (name, phase and since when, generation and whether it is the stable one, restarts, last exit, CPU and memory), the rollout under way with where every zathras stands in it, and its last rollouts |
| a zathras | its state in full; CPU, memory, disk and network over the last 15 minutes as sparklines, with the value now; the last 400 lines of its log, followed as they come |

One view at a time fills the terminal, under a line that says where one
is, the beginning of draal's endpoint ID and the state of the connection,
and above a line of the keys that work there.

| key | does |
| --- | --- |
| `↑` `↓`, `k` `j` | move in a list; scroll a log |
| `Enter`, `→`, `l` | open what is chosen |
| `Esc`, `←`, `h`, `Backspace` | back |
| `PgUp` `PgDn` | a page up, a page down |
| `g` `G` | the start, the end; in a log `G` also follows it again |
| `s` | on a zathras: open a shell |
| `?` | the keys, and what the signs mean |
| `q`, `Ctrl-C` | quit |

**It can roll nothing out and nothing back.** `rollout` and `rollback`
are commands of their own.

**The shell.** `s` on the view of a zathras that is connected opens a
shell in its container, the one `great-machine shell` opens. First draal
is asked for it, and a line says so; the terminal UI keeps the terminal
meanwhile, `Esc` gives the shell up, and nothing else that is typed then
is acted on or passed on to the shell. When draal has opened the shell
the terminal UI steps aside for it: the shell is on the terminal's own
screen, under a line that says whose shell it is, and what it wrote is
still there, in the terminal's scrollback, when the terminal UI has
ended. Everything that is typed goes to the shell, `Ctrl-C` and `q` too,
until the shell ends (`exit`) or `Ctrl-]` is typed. `Ctrl-]` hangs the
shell up, as closing its terminal would: what runs in it ends with it.
Back in the terminal UI, a line says how the shell ended: its exit
status, that it was left, or why it failed; a shell that draal refuses
is said the same way, without the terminal UI leaving its screen. While
a shell is open the terminal UI draws nothing; it shows the fleet as it
is then when it is back. What is typed in the shell never reaches the
terminal UI, and what is typed before `s` never reaches the shell. Within
a few milliseconds of the terminal changing hands, in either direction,
a key may be dropped; and a key typed in the instant before the shell
takes the terminal may be the shell's or, rarely, be acted on by the
terminal UI when the shell has ended.

**When the connection to draal is lost** (draal restarted, the network
gone), the first line says so and for how long, what was shown stays and
is marked as what draal sent last, and draal is dialed again: after one
second, then after pauses that double up to 30 seconds. Any key asks at
once. When draal answers, the view that was open is alive again. What
draal refuses at the very start, an operator that is not on its
allowlist, ends the command with that refusal and the status 1.

**The terminal.** The terminal UI needs a terminal as its standard input
and its standard output, and ends with the status 2 and one line
otherwise: `status` and `logs` print the same without one. It draws on
the terminal's alternate screen and leaves the terminal as it found it,
however it ends. It wants 80 columns and 24 rows, uses more, draws down
to 50 by 12, and says so in one line below that. The keyboard alone
drives it: it does not ask for the mouse, so the terminal's own selection
works. Its colours are the terminal's own sixteen, so it is light or dark
as the terminal is, and it has no theme. With `NO_COLOR` set it draws
without colour; every state is also said by a sign and a word. It needs
UTF-8 and a font with the box-drawing block elements (`▁▂▃▄▅▆▇█`) for
the sparklines. Nothing that draal or a zathras sent, a name, a reason or
a line of a log, reaches the terminal as it came, but through a shell:
control characters and escape sequences are taken out, and a log line
keeps its colours by being read and drawn again.
````

- [ ] **Step 2: The README: what else names the terminal UI**

Six more changes of `README.md`.

1. In the introduction, the sentence `A terminal UI that shows the same is planned, and is not there yet.` goes, and the paragraph becomes:

```markdown
draal also serves [a web page](#the-web-page) that shows the fleet, the
figures and the log of every zathras, and gives a shell in its container.
[`great-machine tui`](#the-terminal-ui) shows the same in a terminal.
```

2. In the table of "Commands", behind the row of `great-machine shell`:

```markdown
| `great-machine tui` | the fleet in the terminal: services, zathrases, figures, logs, and a shell |
```

3. In "Operator commands", the sentence that names the commands with both flags becomes:

```markdown
`id` takes `--key` alone. `rollout`, `rollback`, `status`, `logs`, `shell`
and `tui` take both of these:
```

4. At the end of "Operator commands", behind the example `echo uptime | great-machine shell web`:

```markdown
`tui` takes no argument and no flag of its own: see
[The terminal UI](#the-terminal-ui).
```

5. In "Development", the sentence on the tests of `e2e` becomes:

```markdown
Those in `e2e` run draal and several zathrases in one process, with real
child processes for the services, and the terminal UI against them in a
pseudo-terminal.
```

6. In "The page" of "Development", behind the paragraph `Vite passes /api, WebSockets included, …`, add:

````markdown
The terminal UI has the same made-up fleet behind it with

```sh
go run ./tui/demo           # the terminal UI, no draal and no cluster; --empty for a fleet without a service
```

**Its shell is a real one too**: `s` on a zathras opens a shell on the
machine that runs the demo, as the user who runs it.
````

- [ ] **Step 3: Hold the README to the program**

Run: `nix develop .#bare --command go run ./cmd/great-machine --help`
Expected: ten commands; `tui` is between `shell` and `version`, and the table of "Commands" in the README has the same ten.

Run: `nix develop .#bare --command go run ./cmd/great-machine tui --help`
Expected: the help of the command:

```
NAME:
   great-machine tui - the fleet in this terminal: services, zathrases, figures, logs, and a shell

USAGE:
   great-machine tui [command options]

DESCRIPTION:
   Shows what the web page shows, and follows it: every service, the
   zathrases of one and how far a rollout is, and of a zathras its state, its
   figures and its log. It rolls nothing out. The key ? says the keys; s on a
   zathras opens a shell in its container, which Ctrl-] leaves; q quits.
   While a shell is being opened Esc gives it up, and nothing else that is
   typed then is acted on or passed on to the shell.

   It needs a terminal as its standard input and its standard output. When
   the connection to draal is lost it says so, keeps what it showed, and
   asks again by itself.

OPTIONS:
   --draal ID  endpoint ID of draal, as draal logs it at its start (required) [$GREAT_MACHINE_DRAAL]
   --key FILE  FILE of the operator's key, created on first use (default: great-machine/operator.key in the user's configuration directory) [$GREAT_MACHINE_KEY]
   --help, -h  show help
```

Compare with the README and correct the README where they differ:

- the two flags and their variables are those of the table in "Operator commands", and the command has no other;
- what the README says of the shell is what Task 8 built: draal is asked first, `Esc` gives that up, and nothing else typed then is acted on or reaches the shell;
- the keys of the README's table are those the terminal UI says itself: run `nix develop .#bare --command go run ./tui/demo`, type `?`, and compare row by row (`↑ ↓ k j`, `Enter → l`, `Esc ← h Backspace`, `PgUp PgDn`, `g G`, `s`, `?`, `q Ctrl-C`);
- the sizes are those of `tui/view.go` (`leastWidth` 50, `leastHeight` 12), and the pauses those of `tui.Config` (1 second, 30 seconds);
- `grep -n 'planned' README.md` prints nothing about a terminal UI.

- [ ] **Step 4: Run everything once more**

```bash
nix develop .#bare --command sh -c 'test -z "$(gofmt -l .)" && go vet ./... && go test ./...'
git add -A
nix build --option allow-unsafe-native-code-during-evaluation true
./result/bin/great-machine --help | grep -c '^   tui '
./result/bin/great-machine tui --draal "$(printf 'a1%.0s' $(seq 32))" < /dev/null; echo "status $?"
rm result
```

Expected: `ok` for every package (`e2e` takes about a minute); the Nix build succeeds; `1`; then

```
great-machine: tui needs a terminal as its standard input and its standard output; status and logs print the same without one
status 2
```

and no `result` link is left.

Then once in a real terminal, against a real draal if one is at hand (`great-machine tui --draal ID`), and against the demo otherwise: what Task 8's Step 10 lists holds for the program as it does for the demo.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "README: the terminal UI: what it shows, its keys, its shell, a connection that is lost, and what it needs of a terminal"
```
