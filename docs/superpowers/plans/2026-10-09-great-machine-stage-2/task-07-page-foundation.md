### Task 7: The foundation of the page

**Files:**
- Modify: `ui/package.json`, `ui/package-lock.json` (three font packages and vitest; the script `test`)
- Modify: `ui/vite.config.ts` (`defineConfig` of vitest, and the block `test`)
- Modify: `flake.nix` (`npmDepsHash`)
- Modify: `.github/workflows/test.yml` (`npm test` in the step `ui`)
- Create: `ui/src/api/types.ts`, `ui/src/api/reconnect.ts`, `ui/src/api/client.ts`, `ui/src/state/fleet.ts`, `ui/src/format.ts`
- Create: `ui/src/styles/fonts.css`, `ui/src/styles/tokens.css`, `ui/src/styles/base.css`, `ui/src/assets/mark-white.png`
- Create: `ui/src/components/Layout.tsx`, `ui/src/components/Notice.tsx`, `ui/src/components/ShortID.tsx`
- Create: `ui/src/views/NotFound.tsx`, and as places for Tasks 8 and 9: `ui/src/views/Services.tsx`, `ui/src/views/Service.tsx`, `ui/src/views/Zathras.tsx`
- Modify: `ui/src/App.tsx`, `ui/src/main.tsx` (replaced)
- Test: `ui/src/api/types.test.ts`, `ui/src/format.test.ts`, `ui/src/api/reconnect.test.ts`, `ui/src/api/client.test.ts`, `ui/src/state/fleet.test.ts`

**Interfaces:**
- Consumes:
  - Task 4: the skeleton in `ui/` (`npm ci`, the scripts `dev`, `check`, `build`; `tsconfig.json` strict with `noUncheckedIndexedAccess`; `vite.config.ts` with `assetsInlineLimit: 0`; `index.html` with `#root`), `packages.<system>.ui` with `npmDepsHash` in `flake.nix`, the step `ui` of CI.
  - Tasks 1 and 2: `GET /api/watch` (WebSocket: text messages, each a `Fleet`, the first at once), the refusal format `{"error":{"code":…,"message":…}}`, the 403 of the host rule as a plain line, and the content security policy `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; …`, under which no `data:` URI and no other host is allowed.
  - Task 3: `go run ./web/demo [--addr …] [--dist DIR] [--empty]`.
  - The design system of Fables for Robots: its tokens, and its mark `Icon_White.png`.
- Produces:
  - `ui/package.json`: the script `test` (`vitest run`: once, then it exits). New dependencies, exact: `@fontsource-variable/montserrat` 5.3.0, `@fontsource-variable/inter` 5.3.0, `@fontsource-variable/jetbrains-mono` 5.3.0 (the three families of the design system as files of the build), and as a development dependency `vitest` 5.0.3 (it works with Vite 8). Unit tests are `ui/src/**/*.test.ts` and run in Node, without a document.
  - `ui/src/api/types.ts`: `ID`, `Time`, `Duration`, `Phase`, `Exit`, `Sample`, `State`, `Zathras`, `RolloutState`, `RolloutInfo`, `Service`, `Fleet`, `RolloutList`, `SampleList`, `LogLine`, `Refusal`, `ShellExit`; `underWay(s: RolloutState): boolean`; `isZeroTime(t): boolean` and `parseTime(t): number | undefined` (milliseconds; `undefined` for the zero time: the one way the page recognises it); `isID(id: string): boolean`; `isRefusal(v: unknown): v is Refusal`; `normaliseFleet(v: unknown): Fleet` (the one place where `null` lists become empty ones).
  - `ui/src/format.ts`: `NONE` (the dash for what is missing), `bytes`, `rate`, `cpu`, `percent`, `span` (milliseconds), `duration` (nanoseconds), `ago(t, now)`, `until(t, now)`, `between(from, to)`, `clock`, `clockMs`, `dateTime`, `shortID`, `storeName`, `exit`, `count`, `plural(n, one, many)`, `generation`. Each returns a string for any input, and never `NaN`, `undefined` or `Invalid Date`.
  - `ui/src/api/reconnect.ts`: `pause(attempt: number, chance: number): number`; `type Connection = "connecting" | "live" | "lost"`; `keep(options: KeepOptions): Kept` with `Kept.hurry()` and `Kept.stop()`; `socketURL(path: string): string` (`ws:` for `http:`, `wss:` for `https:`, the host of the page); `FIRST_PAUSE`, `LONGEST_PAUSE`.
  - `ui/src/api/client.ts`: `class ApiError extends Error { code: string; status: number }`; `getJSON<T>(path, signal?)`; `rollouts(service, signal?): Promise<RolloutList>`; `history(id, signal?): Promise<SampleList>`; `errorOf(status, body): ApiError`.
  - `ui/src/state/fleet.ts`: `fleet` (a store: `{ services: ServiceRow[] }`, services by name, zathrases by name and then ID), `connection()`, `received()`, `freshAt()`, `lives()` (counts how often the connection became live: a view asks again for what it asked once when it changes), `now()` (the browser's clock, every second), `findService(name)`, `findZathras(service, id)`, `startFleet(): () => void`, the types `ServiceRow` and `ZathrasRow`.
  - `ui/src/components/Notice.tsx` (`title?`, `kind?: "bad"`, `small?`, children), `ui/src/components/ShortID.tsx` (`id`), `ui/src/components/Layout.tsx` (the frame; the root of the router).
  - `ui/src/styles/tokens.css`: the custom properties every later stylesheet uses (`--bg`, `--surface`, `--surface-strong`, `--fg`, `--fg-muted`, `--fg-subtle`, `--border`, `--link`, `--ok…`, `--busy…`, `--warn…`, `--bad…`, `--idle…` each with `-text` and `-wash`, `--series-1`, `--series-2`, `--grid`, `--term-*`, and the design system's `--ffr-*`), in the light and, by the system's preference, in the dark. `ui/src/styles/base.css`: the classes `head`, `section`, `panel`, `notice`, `cmd`, `reason`, `id`, `badge` (with `data-tone="ok|busy|warn|bad|idle"`), `label`, `num`, `mono`, `muted`.
  - The routes `/`, `/s/:service`, `/s/:service/z/:id`, and a view for every other path.

The plan's constraint that every value of a zathras or of a rollout is
text to the page is kept by construction: every view is JSX, and Solid
puts an expression of JSX into the document as a text node or as the
value of an attribute, never as markup. No file of `ui/src` uses
`innerHTML`, `outerHTML` or `insertAdjacentHTML`, and Step 27 checks
that. (The script that Vite builds does contain `innerHTML`: Solid
makes the constant skeleton of each component, which the compiler wrote
from the JSX and which holds no data, that way, and xterm.js uses it on
elements of its own.)

**Review Focus 3 is this task's.** `keep` in `ui/src/api/reconnect.ts`
is what says that the connection is lost, tries again with pauses that
grow, and is live again; its test in Step 14 pins that, and Step 26
tries it with a real draal that goes and comes back. That the page is
back on the view it was on needs no code: it never leaves it.

- [ ] **Step 1: The dependencies**

```bash
nix develop .#bare --command npm --prefix ui ci
nix develop .#bare --command npm --prefix ui install --save-exact @fontsource-variable/montserrat@5.3.0 @fontsource-variable/inter@5.3.0 @fontsource-variable/jetbrains-mono@5.3.0
nix develop .#bare --command npm --prefix ui install --save-exact --save-dev vitest@5.0.3
```

Then add the script `test`, so that the file is this:

`ui/package.json`:

```json
{
  "name": "great-machine-ui",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "check": "tsc --noEmit",
    "test": "vitest run",
    "build": "vite build"
  },
  "dependencies": {
    "@fontsource-variable/inter": "5.3.0",
    "@fontsource-variable/jetbrains-mono": "5.3.0",
    "@fontsource-variable/montserrat": "5.3.0",
    "@solidjs/router": "1.0.0",
    "@xterm/addon-fit": "0.11.0",
    "@xterm/xterm": "6.0.0",
    "solid-js": "1.9.17"
  },
  "devDependencies": {
    "typescript": "7.0.2",
    "vite": "8.3.4",
    "vite-plugin-solid": "2.11.14",
    "vitest": "5.0.3"
  }
}
```

`npm install` has also written `ui/package-lock.json`, and may have
chosen other versions of what these packages depend on than this plan
was verified with. Take the lock file that is beside the plan, and
install from it:

```bash
cp docs/superpowers/plans/2026-10-09-great-machine-stage-2/assets/task-07-package-lock.json ui/package-lock.json
nix develop .#bare --command npm --prefix ui ci
```

Expected: `added … packages` and no error (`npm ci` fails when
`package.json` and the lock file do not agree: then `package.json` is
not as above).

- [ ] **Step 2: vitest in the configuration of Vite**

In `ui/vite.config.ts`, `defineConfig` comes from vitest, which knows
the block `test`. Replace the line
`import { defineConfig, type Plugin } from "vite";` with

```ts
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";
```

and put the block `test` between `build` and `server`:

```ts
    sourcemap: false,
    target: "es2022",
  },
  // The unit tests are of what has no part in the document: they run in
  // Node as it is. (vite-plugin-solid would ask for jsdom otherwise.)
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
  server: {
```

- [ ] **Step 3: The hash of the dependencies, and the tests in CI**

Run: `nix run nixpkgs#prefetch-npm-deps ui/package-lock.json`
Expected: one line, `sha256-iF9vMOHv9JOptMwCe4LclyvzZbTkk8c1x3Sz2dl0ctI=`, the hash of the lock file of Step 1.

Put it into `flake.nix`:

```nix
          nodejs = nodejs pkgs;
          npmDepsHash = "sha256-iF9vMOHv9JOptMwCe4LclyvzZbTkk8c1x3Sz2dl0ctI=";
```

In `.github/workflows/test.yml` the step `ui` becomes:

```yaml
      # The page: its dependencies as package-lock.json names them, the
      # type check, the unit tests, and the build into web/dist. A build
      # leaves nothing that git sees: web/dist is ignored but for its
      # placeholder, which the build keeps.
      - name: ui
        run: |
          cd ui
          npm ci
          npm run check
          npm test
          npm run build
          test -z "$(git status --porcelain)"
```

- [ ] **Step 4: Write the failing test of the types of the API**

`ui/src/api/types.test.ts`:

```ts
import { describe, expect, test } from "vitest";
import { isID, isRefusal, isZeroTime, normaliseFleet, parseTime, underWay } from "./types";

describe("times", () => {
  test("the zero time is none", () => {
    expect(isZeroTime("0001-01-01T00:00:00Z")).toBe(true);
    expect(isZeroTime(undefined)).toBe(true);
    expect(isZeroTime("")).toBe(true);
    expect(isZeroTime("2026-10-09T22:00:30.325511+02:00")).toBe(false);
    expect(parseTime("0001-01-01T00:00:00Z")).toBeUndefined();
  });

  test("a time is parsed with its offset and its fraction", () => {
    expect(parseTime("2026-10-09T20:00:30Z")).toBe(Date.UTC(2026, 9, 9, 20, 0, 30));
    expect(parseTime("2026-10-09T22:00:30+02:00")).toBe(Date.UTC(2026, 9, 9, 20, 0, 30));
    const a = parseTime("2026-10-09T22:00:30.325511+02:00");
    const b = parseTime("2026-10-09T22:00:30.325512001+02:00");
    expect(a).toBeDefined();
    expect(b).toBeDefined();
    expect(b! > a!).toBe(true);
    expect(Math.floor(a!)).toBe(Date.UTC(2026, 9, 9, 20, 0, 30, 325));
  });

  test("what is no time is undefined, never NaN", () => {
    expect(parseTime("yesterday")).toBeUndefined();
    expect(parseTime(null)).toBeUndefined();
  });
});

describe("shapes", () => {
  test("an endpoint ID is 64 lowercase hexadecimal characters", () => {
    expect(isID("3f08d61e04d64c081e7eff0c98be5343638e90c39d73b9088b87da7da0dd1eb1")).toBe(true);
    expect(isID("3F08D61E04D64C081E7EFF0C98BE5343638E90C39D73B9088B87DA7DA0DD1EB1")).toBe(false);
    expect(isID("abc")).toBe(false);
  });

  test("a refusal is recognised", () => {
    expect(isRefusal({ error: { code: "not-found", message: "no such zathras" } })).toBe(true);
    expect(isRefusal({ exit: 3 })).toBe(false);
    expect(isRefusal(null)).toBe(false);
    expect(isRefusal({ error: "text" })).toBe(false);
  });

  test("a rollout is under way while it prepares or activates", () => {
    expect(underWay("preparing")).toBe(true);
    expect(underWay("activating")).toBe(true);
    expect(underWay("completed")).toBe(false);
    expect(underWay("halted")).toBe(false);
    expect(underWay("superseded")).toBe(false);
  });

  test("null lists of a fleet are empty lists", () => {
    expect(normaliseFleet({ services: null })).toEqual({ services: [] });
    expect(normaliseFleet({})).toEqual({ services: [] });
    expect(normaliseFleet("text")).toEqual({ services: [] });
    const f = normaliseFleet({ services: [{ name: "web", stable: 0, zathrases: null }] });
    expect(f.services[0]?.zathrases).toEqual([]);
  });
});
```

- [ ] **Step 5: Run the test to see it fail**

Run: `nix develop .#bare --command npm --prefix ui test -- src/api/types.test.ts`
Expected: FAIL — `Error: Cannot find module './types'` (the test file fails before any test runs: `Test Files  1 failed (1)`)

- [ ] **Step 6: Write the types of the API**

`ui/src/api/types.ts`:

```ts
// The types of the HTTP API, as draal's JSON has them: one for each type
// of wire/messages.go that the API sends, with the names of its json
// tags.
//
// A time is a string in RFC 3339 with 0 to 9 fractional digits and an
// offset or Z. The zero time, "0001-01-01T00:00:00Z", stands for "none":
// isZeroTime recognises it and parseTime gives undefined for it. A
// duration is a number of nanoseconds. A field with a question mark is
// absent from the JSON when it is empty.

/** An endpoint ID: 64 lowercase hexadecimal characters. */
export type ID = string;

/** A time as the JSON has it. */
export type Time = string;

/** A duration: nanoseconds. */
export type Duration = number;

/** What a zathras is doing with its service. */
export type Phase = "idle" | "running" | "stopping" | "backoff";

/** How a service ended. */
export interface Exit {
  generation: number;
  /** The exit status; -1 when a signal ended it or it never started. */
  status: number;
  /** Such as "SIGKILL". */
  signal?: string;
  /** Why the binary could not be started. */
  error?: string;
  at: Time;
  ran: Duration;
}

/** The figures of a container at one time. */
export interface Sample {
  at: Time;
  /** Cores. */
  cpu: number;
  /** Bytes. */
  memory: number;
  /** Bytes; 0 for none. */
  memory_limit: number;
  /** Bytes per second, as the three below. */
  disk_read: number;
  disk_write: number;
  net_rx: number;
  net_tx: number;
}

/** What a zathras reports. */
export interface State {
  /** 0 for none. */
  generation: number;
  binary?: string;
  args?: string[];
  phase: Phase;
  /** While running. */
  pid?: number;
  /** While running. */
  running_for?: Duration;
  /** While in backoff; the zero time otherwise. */
  restart_at: Time;
  last_exit?: Exit;
  restarts: number;
  /** The generations that were prepared, ascending. */
  prepared?: number[];
  /** The generation being prepared, if one is. */
  preparing?: number;
  /** The latest sample, if the platform gives one. */
  sample?: Sample;
}

/** A connected zathras and what it reported last. */
export interface Zathras {
  id: ID;
  name: string;
  connected: Time;
  state: State;
}

/** How far a rollout is. */
export type RolloutState = "preparing" | "activating" | "completed" | "halted" | "superseded";

/** A rollout as draal records it. */
export interface RolloutInfo {
  id: number;
  service: string;
  generation: number;
  parallel: number;
  state: RolloutState;
  /** Why it was halted. */
  reason?: string;
  created: Time;
  created_by: ID;
  /** The zero time while under way. */
  finished: Time;
  /** Of the connected zathrases, as draal decided last. In a Fleet alone:
   *  the list of /api/services/{service}/rollouts has them as 0. */
  prepared: number;
  activated: number;
  awaited: number;
}

/** A service, its last rollout and its connected zathrases. */
export interface Service {
  name: string;
  /** The stable generation; 0 for none. */
  stable: number;
  /** The last rollout; absent before the first. */
  rollout?: RolloutInfo;
  /** By name, then by ID. draal sends a list always; the type of the
   *  sender has no guard, so normaliseFleet takes null for an empty one. */
  zathrases: Zathras[];
}

/** Every service draal knows: a message of /api/watch. */
export interface Fleet {
  services: Service[];
}

/** The answer of /api/services/{service}/rollouts: newest first. */
export interface RolloutList {
  rollouts: RolloutInfo[];
}

/** The answer of /api/zathras/{id}/history: oldest first. */
export interface SampleList {
  samples: Sample[];
}

/** A message of /api/zathras/{id}/logs. One with lost set has no text: so
 *  many lines were lost before the line that follows. An empty line has
 *  no text either. */
export interface LogLine {
  at: Time;
  stream: "stdout" | "stderr" | "zathras" | string;
  text?: string;
  lost?: number;
}

/** A refusal: the body of an HTTP answer that is one, and the last
 *  message of a socket that ends with one. */
export interface Refusal {
  error: { code: string; message: string };
}

/** The message that ends a shell's socket when the shell has ended. */
export interface ShellExit {
  exit: number;
}

/** Says whether s is under way: preparing or activating. */
export function underWay(s: RolloutState): boolean {
  return s === "preparing" || s === "activating";
}

/** Says whether t is the zero time, which stands for "none". */
export function isZeroTime(t: Time | undefined | null): boolean {
  return typeof t !== "string" || t === "" || t.startsWith("0001-01-01T00:00:00");
}

/** The time as milliseconds since 1970, with the fraction of a
 *  millisecond that the text has; undefined for the zero time and for
 *  what is no time. */
export function parseTime(t: Time | undefined | null): number | undefined {
  if (typeof t !== "string" || isZeroTime(t)) {
    return undefined;
  }
  const ms = Date.parse(t);
  if (!Number.isFinite(ms)) {
    return undefined;
  }
  // Date.parse stops at the millisecond; the digits after it order two
  // lines of a log that were written within one.
  const fraction = /\.(\d+)/.exec(t)?.[1] ?? "";
  const below = fraction.length > 3 ? Number("0." + fraction.slice(3)) : 0;
  return ms + below;
}

/** Says whether id is written as an endpoint ID is. */
export function isID(id: string): boolean {
  return /^[0-9a-f]{64}$/.test(id);
}

/** Says whether v is a refusal. */
export function isRefusal(v: unknown): v is Refusal {
  if (typeof v !== "object" || v === null || !("error" in v)) {
    return false;
  }
  const e = (v as { error: unknown }).error;
  return typeof e === "object" && e !== null && typeof (e as { message?: unknown }).message === "string";
}

/** The fleet of a message, with every list a list: services and
 *  zathrases that are null or absent are empty. This is the one place
 *  where the page distrusts the shape of what draal sends. */
export function normaliseFleet(v: unknown): Fleet {
  const raw = typeof v === "object" && v !== null ? (v as { services?: unknown }).services : undefined;
  const services = Array.isArray(raw) ? (raw as Service[]) : [];
  return {
    services: services.map((s) => ({ ...s, zathrases: Array.isArray(s.zathrases) ? s.zathrases : [] })),
  };
}
```

- [ ] **Step 7: Run the tests to see them pass**

Run: `nix develop .#bare --command npm --prefix ui test`
Expected: PASS — `Test Files  1 passed (1)`, and `nix develop .#bare --command npm --prefix ui run check` prints no error.

- [ ] **Step 8: Commit**

```bash
git add flake.nix .github/workflows/test.yml ui/package.json ui/package-lock.json ui/vite.config.ts ui/src/api/
git commit -m "UI: fonts and vitest among the dependencies, and the types of the API"
```

- [ ] **Step 9: Write the failing test of the formatters**

`ui/src/format.test.ts`:

```ts
import { describe, expect, test } from "vitest";
import {
  ago,
  between,
  bytes,
  clock,
  clockMs,
  count,
  cpu,
  dateTime,
  duration,
  exit,
  generation,
  NONE,
  percent,
  plural,
  rate,
  shortID,
  span,
  storeName,
  until,
} from "./format";

const ZERO = "0001-01-01T00:00:00Z";
const NOW = Date.UTC(2026, 9, 9, 20, 0, 0);

describe("bytes and rates", () => {
  test("binary units, three figures", () => {
    expect(bytes(0)).toBe("0 B");
    expect(bytes(512)).toBe("512 B");
    expect(bytes(1024)).toBe("1 KiB");
    expect(bytes(1536)).toBe("1.5 KiB");
    expect(bytes(286297311)).toBe("273 MiB");
    expect(bytes(536870912)).toBe("512 MiB");
    expect(bytes(1024 ** 3 * 1.25)).toBe("1.25 GiB");
    expect(bytes(1024 * 1023.7)).toBe("1 MiB");
    expect(bytes(1024 ** 6 * 5)).toBe("5120 PiB");
  });

  test("a rate is bytes a second", () => {
    expect(rate(22494.46)).toBe("22 KiB/s");
    expect(rate(0)).toBe("0 B/s");
  });

  test("what is missing or cannot be is a dash", () => {
    for (const v of [undefined, null, NaN, Infinity, -1]) {
      expect(bytes(v)).toBe(NONE);
      expect(rate(v)).toBe(NONE);
      expect(cpu(v)).toBe(NONE);
      expect(percent(v)).toBe(NONE);
    }
  });
});

describe("cpu", () => {
  test("cores as a percentage of one core", () => {
    expect(cpu(0)).toBe("0%");
    expect(cpu(0.004)).toBe("0.4%");
    expect(cpu(0.0754)).toBe("7.5%");
    expect(cpu(0.7500719696731654)).toBe("75%");
    expect(cpu(2.5)).toBe("250%");
  });

  test("a share as a percentage", () => {
    expect(percent(0.533)).toBe("53%");
    expect(percent(1)).toBe("100%");
  });
});

describe("lengths of time", () => {
  test("the two largest units", () => {
    expect(span(0)).toBe("0 s");
    expect(span(0.4)).toBe("1 ms");
    expect(span(850)).toBe("850 ms");
    expect(span(1300)).toBe("1.3 s");
    expect(span(47_000)).toBe("47 s");
    expect(span(180_000)).toBe("3 m");
    expect(span(200_000)).toBe("3 m 20 s");
    expect(span(7_500_000)).toBe("2 h 5 m");
    expect(span(3 * 86_400_000 + 4 * 3_600_000)).toBe("3 d 4 h");
  });

  test("a negative length, of clocks that differ, is 0 s", () => {
    expect(span(-5000)).toBe("0 s");
    expect(duration(-1)).toBe("0 s");
  });

  test("a duration of the API is nanoseconds", () => {
    expect(duration(1_300_000_000)).toBe("1.3 s");
    expect(duration(2_816_000_000_000)).toBe("46 m 56 s");
    expect(duration(undefined)).toBe(NONE);
    expect(duration(NaN)).toBe(NONE);
  });

  test("ago, in the largest unit", () => {
    expect(ago("2026-10-09T19:59:13Z", NOW)).toBe("47 s ago");
    expect(ago("2026-10-09T21:57:00+02:00", NOW)).toBe("3 m ago");
    expect(ago("2026-10-09T17:55:00Z", NOW)).toBe("2 h ago");
    expect(ago("2026-10-06T20:00:00Z", NOW)).toBe("3 d ago");
    expect(ago(NOW - 5000, NOW)).toBe("5 s ago");
  });

  test("ago of the zero time is a dash, and of the future is now", () => {
    expect(ago(ZERO, NOW)).toBe(NONE);
    expect(ago(undefined, NOW)).toBe(NONE);
    expect(ago("nonsense", NOW)).toBe(NONE);
    expect(ago("2026-10-09T20:00:07Z", NOW)).toBe("now");
    expect(ago("2026-10-09T20:00:00Z", NaN)).toBe(NONE);
  });

  test("until", () => {
    expect(until("2026-10-09T20:00:06Z", NOW)).toBe("in 6 s");
    expect(until("2026-10-09T19:59:00Z", NOW)).toBe("now");
    expect(until(ZERO, NOW)).toBe(NONE);
  });

  test("between two times", () => {
    expect(between("2026-10-09T20:00:00Z", "2026-10-09T20:00:34Z")).toBe("34 s");
    expect(between("2026-10-09T20:00:00Z", ZERO)).toBe(NONE);
    expect(between("2026-10-09T20:00:34Z", "2026-10-09T20:00:00Z")).toBe("0 s");
  });
});

describe("times", () => {
  // The clock of the reader is that of the machine the test runs on:
  // the expectations are made the same way.
  const at = "2026-10-09T22:14:41.284222+02:00";
  const d = new Date(Date.parse(at));
  const two = (n: number) => String(n).padStart(2, "0");
  const hms = `${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}`;

  test("the time of day", () => {
    expect(clock(at)).toBe(hms);
    expect(clockMs(at)).toBe(`${hms}.284`);
    expect(clockMs("2026-10-09T22:14:41.004+02:00")).toBe(`${hms}.004`);
    expect(clockMs("2026-10-09T22:14:41+02:00")).toBe(`${hms}.000`);
  });

  test("the day and the time", () => {
    expect(dateTime(at)).toBe(`${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())} ${hms}`);
  });

  test("the zero time and nonsense are a dash, never Invalid Date", () => {
    for (const v of [ZERO, undefined, null, "", "nonsense"]) {
      expect(clock(v)).toBe(NONE);
      expect(clockMs(v)).toBe(NONE);
      expect(dateTime(v)).toBe(NONE);
    }
    expect(clock(NaN)).toBe(NONE);
  });
});

describe("names", () => {
  test("a short ID is the first ten characters", () => {
    expect(shortID("3f08d61e04d64c081e7eff0c98be5343638e90c39d73b9088b87da7da0dd1eb1")).toBe("3f08d61e04");
    expect(shortID("")).toBe(NONE);
    expect(shortID(undefined)).toBe(NONE);
  });

  test("a store path loses the store and its hash", () => {
    expect(storeName("/nix/store/kzrvvx11ilxcm7cr7zndjxh2kav4c5ra-web-1.4.20/bin/web")).toBe("web-1.4.20/bin/web");
    expect(storeName("/usr/bin/env")).toBe("/usr/bin/env");
    expect(storeName(undefined)).toBe(NONE);
    expect(storeName("")).toBe(NONE);
  });

  test("an exit", () => {
    const base = { generation: 4, at: "2026-10-09T20:00:00Z", ran: 1_300_000_000 };
    expect(exit({ ...base, status: 3 })).toBe("exit 3");
    expect(exit({ ...base, status: 0 })).toBe("exit 0");
    expect(exit({ ...base, status: -1, signal: "SIGKILL" })).toBe("killed by SIGKILL");
    expect(exit({ ...base, status: -1, error: "exec format error" })).toBe("did not start");
    expect(exit(undefined)).toBe(NONE);
    expect(exit({ ...base, status: NaN })).toBe(NONE);
  });

  test("counts", () => {
    expect(count(1200)).toBe("1 200");
    expect(count(36)).toBe("36");
    expect(count(1234567)).toBe("1 234 567");
    expect(count(undefined)).toBe(NONE);
    expect(plural(1, "zathras", "zathrases")).toBe("1 zathras");
    expect(plural(0, "zathras", "zathrases")).toBe("0 zathrases");
  });

  test("a generation, and none", () => {
    expect(generation(20)).toBe("20");
    expect(generation(0)).toBe(NONE);
    expect(generation(undefined)).toBe(NONE);
  });
});

test("no formatter ever writes NaN, undefined or Invalid Date", () => {
  const bad: unknown[] = [undefined, null, NaN, Infinity, -Infinity, -1, "", "nonsense", ZERO, {}];
  const fns: ((v: never) => string)[] = [
    bytes,
    rate,
    cpu,
    percent,
    span,
    duration,
    clock,
    clockMs,
    dateTime,
    shortID,
    storeName,
    exit,
    count,
    generation,
    (v: never) => ago(v, NOW),
    (v: never) => ago("2026-10-09T20:00:00Z", v),
    (v: never) => until(v, NOW),
    (v: never) => between(v, v),
  ];
  for (const f of fns) {
    for (const v of bad) {
      const s = f(v as never);
      expect(typeof s).toBe("string");
      expect(s).not.toMatch(/NaN|undefined|Invalid|null|object/);
    }
  }
});
```

- [ ] **Step 10: Run the test to see it fail**

Run: `nix develop .#bare --command npm --prefix ui test -- src/format.test.ts`
Expected: FAIL — `Error: Cannot find module './format'` (the test file fails before any test runs: `Test Files  1 failed (1)`)

- [ ] **Step 11: Write the formatters**

`ui/src/format.ts`:

```ts
// What the page writes for a number, a time, an ID, a path and an exit.
// Every function here is pure and total: whatever it is given, it
// returns text a person can read, and never "NaN", "undefined" or
// "Invalid Date". What is missing is written as a dash.

import { type Exit, parseTime, type Time } from "./api/types";

/** What stands where a value is missing. */
export const NONE = "–";

const NS_PER_MS = 1e6;

function finite(n: unknown): n is number {
  return typeof n === "number" && Number.isFinite(n);
}

/** A number with as many decimals as keep three figures: 1.25, 12.5, 125. */
function three(n: number): string {
  if (n >= 100) {
    return String(Math.round(n));
  }
  if (n >= 10) {
    return String(Math.round(n * 10) / 10);
  }
  return String(Math.round(n * 100) / 100);
}

const BINARY = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] as const;

/** A number of bytes in binary units: "0 B", "1.5 KiB", "273 MiB". */
export function bytes(n: number | undefined | null): string {
  if (!finite(n) || n < 0) {
    return NONE;
  }
  let unit = 0;
  let v = n;
  while (v >= 1024 && unit < BINARY.length - 1) {
    v /= 1024;
    unit++;
  }
  // 1023.6 KiB would be written "1024 KiB": it is 1 MiB.
  if (unit > 0 && Math.round(v) >= 1024 && unit < BINARY.length - 1) {
    v /= 1024;
    unit++;
  }
  return `${unit === 0 ? Math.round(v) : three(v)} ${BINARY[unit]}`;
}

/** Bytes per second: "22 KiB/s". A rate below zero, which a counter that
 *  started again would give, is none. */
export function rate(n: number | undefined | null): string {
  const b = bytes(n);
  return b === NONE ? NONE : `${b}/s`;
}

/** Cores as a percentage of one core: 0.75 is "75%", 2.5 is "250%". */
export function cpu(cores: number | undefined | null): string {
  if (!finite(cores) || cores < 0) {
    return NONE;
  }
  const p = cores * 100;
  if (p >= 10) {
    return `${Math.round(p)}%`;
  }
  return `${Math.round(p * 10) / 10}%`;
}

/** A share, 0 to 1, as a whole percentage. */
export function percent(share: number | undefined | null): string {
  if (!finite(share) || share < 0) {
    return NONE;
  }
  return `${Math.round(share * 100)}%`;
}

/** A length of time in milliseconds, in its two largest units:
 *  "850 ms", "1.3 s", "47 s", "3 m 20 s", "2 h 5 m", "3 d 4 h". A length
 *  below zero, which two clocks that differ give, is "0 s". */
export function span(ms: number | undefined | null): string {
  if (!finite(ms)) {
    return NONE;
  }
  if (ms <= 0) {
    return "0 s";
  }
  if (ms < 1000) {
    return `${Math.max(1, Math.round(ms))} ms`;
  }
  if (ms < 10_000) {
    return `${Math.round(ms / 100) / 10} s`;
  }
  const s = Math.round(ms / 1000);
  if (s < 60) {
    return `${s} s`;
  }
  const m = Math.floor(s / 60);
  if (m < 60) {
    return s % 60 === 0 ? `${m} m` : `${m} m ${s % 60} s`;
  }
  const h = Math.floor(m / 60);
  if (h < 24) {
    return m % 60 === 0 ? `${h} h` : `${h} h ${m % 60} m`;
  }
  const d = Math.floor(h / 24);
  return h % 24 === 0 ? `${d} d` : `${d} d ${h % 24} h`;
}

/** A duration of the API, nanoseconds, written as span writes it. */
export function duration(ns: number | undefined | null): string {
  return finite(ns) ? span(ns / NS_PER_MS) : NONE;
}

/** A length of time in its largest unit alone: "47 s", "3 m", "2 h". */
function coarse(ms: number): string {
  const s = Math.floor(ms / 1000);
  if (s < 60) {
    return `${s} s`;
  }
  if (s < 3600) {
    return `${Math.floor(s / 60)} m`;
  }
  if (s < 86_400) {
    return `${Math.floor(s / 3600)} h`;
  }
  return `${Math.floor(s / 86_400)} d`;
}

/** How long ago a time was, by the clock of now (milliseconds): "now",
 *  "47 s ago", "3 m ago". A time that is none is a dash; one in the
 *  future, which a clock that is ahead gives, is "now". */
export function ago(t: Time | number | undefined | null, now: number): string {
  const at = typeof t === "number" ? t : parseTime(t);
  if (!finite(at) || !finite(now)) {
    return NONE;
  }
  const d = now - at;
  return d < 1000 ? "now" : `${coarse(d)} ago`;
}

/** How long it is until a time: "in 6 s", or "now" when it has come. */
export function until(t: Time | undefined | null, now: number): string {
  const at = parseTime(t);
  if (!finite(at) || !finite(now)) {
    return NONE;
  }
  const d = at - now;
  return d < 1000 ? "now" : `in ${coarse(d)}`;
}

/** How long there is between two times, as span writes it. */
export function between(from: Time | undefined | null, to: Time | undefined | null): string {
  const a = parseTime(from);
  const b = parseTime(to);
  return finite(a) && finite(b) ? span(b - a) : NONE;
}

function two(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

/** The time of day of a time, on the clock of the reader: "22:14:41". */
export function clock(t: Time | number | undefined | null): string {
  const at = typeof t === "number" ? t : parseTime(t);
  if (!finite(at)) {
    return NONE;
  }
  const d = new Date(at);
  return `${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}`;
}

/** The time of day with its milliseconds, for a line of a log. */
export function clockMs(t: Time | undefined | null): string {
  const at = parseTime(t);
  if (!finite(at)) {
    return NONE;
  }
  const ms = new Date(at).getMilliseconds();
  return `${clock(at)}.${ms < 10 ? "00" : ms < 100 ? "0" : ""}${ms}`;
}

/** The day and the time of a time: "2026-10-09 22:14:41". */
export function dateTime(t: Time | undefined | null): string {
  const at = parseTime(t);
  if (!finite(at)) {
    return NONE;
  }
  const d = new Date(at);
  return `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())} ${clock(at)}`;
}

/** The first ten characters of an endpoint ID. */
export function shortID(id: string | undefined | null): string {
  return typeof id === "string" && id !== "" ? id.slice(0, 10) : NONE;
}

/** A path of the Nix store without the store and the hash:
 *  "/nix/store/kzrv…-web-1.4.20/bin/web" is "web-1.4.20/bin/web". Any
 *  other path is itself. */
export function storeName(path: string | undefined | null): string {
  if (typeof path !== "string" || path === "") {
    return NONE;
  }
  const m = /^\/nix\/store\/[0-9a-z]{32}-(.+)$/.exec(path);
  return m?.[1] ?? path;
}

/** How a service ended: "exit 3", "killed by SIGKILL", "did not start". */
export function exit(e: Exit | undefined | null): string {
  if (typeof e !== "object" || e === null) {
    return NONE;
  }
  if (e.error) {
    return "did not start";
  }
  if (e.signal) {
    return `killed by ${e.signal}`;
  }
  if (!finite(e.status)) {
    return NONE;
  }
  return `exit ${e.status}`;
}

/** A whole number with its thousands apart: "1 200". The spaces do not
 *  break. */
export function count(n: number | undefined | null): string {
  if (!finite(n)) {
    return NONE;
  }
  return String(Math.round(n)).replace(/\B(?=(\d{3})+(?!\d))/g, " ");
}

/** A number with its noun: "1 zathras", "3 zathrases". */
export function plural(n: number, one: string, many: string): string {
  return `${count(n)} ${n === 1 ? one : many}`;
}

/** A generation: "20", or a dash for none (0). */
export function generation(g: number | undefined | null): string {
  return finite(g) && g > 0 ? String(g) : NONE;
}
```

- [ ] **Step 12: Run the tests to see them pass**

Run: `nix develop .#bare --command npm --prefix ui test`
Expected: PASS — `Test Files  2 passed (2)`, and `nix develop .#bare --command npm --prefix ui run check` prints no error.

- [ ] **Step 13: Commit**

```bash
git add ui/src/format.ts ui/src/format.test.ts
git commit -m "UI: what the page writes for a number, a time, an ID and an exit"
```

- [ ] **Step 14: Write the failing test of the connection that is kept (Review Focus 3)**

`ui/src/api/reconnect.test.ts`:

```ts
import { describe, expect, test } from "vitest";
import { type Connection, FIRST_PAUSE, keep, LONGEST_PAUSE, pause, type SocketLike, socketURL } from "./reconnect";

describe("the pauses", () => {
  test("1 s doubling to 30 s, when chance is 1", () => {
    expect([0, 1, 2, 3, 4, 5, 6, 20].map((n) => pause(n, 1))).toEqual([
      1000, 2000, 4000, 8000, 16_000, 30_000, 30_000, 30_000,
    ]);
    expect(pause(0, 1)).toBe(FIRST_PAUSE);
    expect(pause(1000, 1)).toBe(LONGEST_PAUSE);
  });

  test("chance takes up to a half off, and never more", () => {
    expect(pause(0, 0)).toBe(500);
    expect(pause(3, 0.5)).toBe(6000);
    expect(pause(9, 0)).toBe(15_000);
    for (let n = 0; n < 12; n++) {
      for (const c of [0, 0.25, 0.5, 0.99, 1]) {
        const p = pause(n, c);
        expect(p).toBeGreaterThanOrEqual(500);
        expect(p).toBeLessThanOrEqual(LONGEST_PAUSE);
      }
    }
  });

  test("nonsense gives a pause all the same", () => {
    expect(pause(NaN, NaN)).toBe(1000);
    expect(pause(-3, 7)).toBe(1000);
    expect(pause(2, -1)).toBe(2000);
  });
});

// A socket of the test: what the page did to it, and the means to make
// it speak and end.
class FakeSocket implements SocketLike {
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: ((event: { code?: number }) => void) | null = null;
  onerror: ((event: unknown) => void) | null = null;
  closed = false;
  close() {
    this.closed = true;
  }
  say(data: unknown) {
    this.onmessage?.({ data });
  }
  end() {
    this.onerror?.({});
    this.onclose?.({ code: 1006 });
  }
}

// The world of a test: the sockets that were opened, the timers that
// run, and what the connection reported.
function world(chance = 1) {
  const sockets: FakeSocket[] = [];
  const timers: { run: () => void; ms: number; cleared: boolean }[] = [];
  const states: Connection[] = [];
  const messages: unknown[] = [];
  const kept = keep({
    open: () => {
      const s = new FakeSocket();
      sockets.push(s);
      return s;
    },
    onMessage: (data) => messages.push(data),
    onState: (s) => states.push(s),
    setTimer: (run, ms) => {
      const t = { run, ms, cleared: false };
      timers.push(t);
      return t;
    },
    clearTimer: (t) => {
      (t as { cleared: boolean }).cleared = true;
    },
    chance: () => chance,
  });
  const last = () => sockets[sockets.length - 1]!;
  // Lets the pause that runs pass.
  const elapse = () => {
    const t = timers[timers.length - 1]!;
    expect(t.cleared).toBe(false);
    t.run();
  };
  return { sockets, timers, states, messages, kept, last, elapse };
}

describe("a connection that is kept", () => {
  test("is live with the first message", () => {
    const w = world();
    expect(w.sockets.length).toBe(1);
    expect(w.states).toEqual([]);
    w.last().say("fleet");
    expect(w.states).toEqual(["live"]);
    expect(w.messages).toEqual(["fleet"]);
  });

  // Review Focus 3: draal restarted while the page is open. The page
  // says that the connection is lost, tries again with pauses that
  // grow, and is live again with the first message.
  test("is lost when the socket ends, tries again with pauses that grow, and is live again", () => {
    const w = world();
    w.last().say("fleet");
    w.last().end();
    expect(w.states).toEqual(["live", "lost"]);
    expect(w.timers.map((t) => t.ms)).toEqual([1000]);

    // draal is not back: every attempt fails, and the pauses double.
    for (let i = 0; i < 6; i++) {
      w.elapse();
      w.last().end();
    }
    expect(w.timers.map((t) => t.ms)).toEqual([1000, 2000, 4000, 8000, 16_000, 30_000, 30_000]);
    expect(w.states).toEqual(["live", "lost"]);

    // draal is back.
    w.elapse();
    expect(w.sockets.length).toBe(8);
    w.last().say("fleet again");
    expect(w.states).toEqual(["live", "lost", "live"]);
    expect(w.messages).toEqual(["fleet", "fleet again"]);

    // The pauses start over after a message.
    w.last().end();
    expect(w.timers[w.timers.length - 1]!.ms).toBe(1000);
  });

  test("a socket that opens and ends without a message does not start the pauses over", () => {
    const w = world();
    w.last().end();
    expect(w.states).toEqual(["lost"]);
    w.elapse();
    w.last().end();
    w.elapse();
    w.last().end();
    expect(w.timers.map((t) => t.ms)).toEqual([1000, 2000, 4000]);
  });

  test("the end of a socket counts once, though it says so twice", () => {
    const w = world();
    const first = w.last();
    first.end();
    expect(w.timers.length).toBe(1);
    expect(first.closed).toBe(true);
    // What a socket that was replaced still says is not heard.
    w.elapse();
    first.onclose?.({ code: 1006 });
    first.say("late");
    expect(w.sockets.length).toBe(2);
    expect(w.timers.length).toBe(1);
    expect(w.messages).toEqual([]);
  });

  test("hurry tries at once while a pause runs, and does nothing otherwise", () => {
    const w = world();
    w.kept.hurry();
    expect(w.sockets.length).toBe(1);
    w.last().end();
    w.kept.hurry();
    expect(w.sockets.length).toBe(2);
    expect(w.timers[0]!.cleared).toBe(true);
    // An attempt is under way: another hurry opens no second socket.
    w.kept.hurry();
    expect(w.sockets.length).toBe(2);
    // The attempt that hurry made counts as one: the next pause is longer.
    w.last().end();
    expect(w.timers[w.timers.length - 1]!.ms).toBe(2000);
  });

  test("stop closes the socket, and nothing is opened after it", () => {
    const w = world();
    w.last().say("fleet");
    const s = w.last();
    w.kept.stop();
    expect(s.closed).toBe(true);
    s.end();
    expect(w.sockets.length).toBe(1);
    expect(w.timers.length).toBe(0);
    expect(w.states).toEqual(["live"]);
  });

  test("stop during a pause clears it", () => {
    const w = world();
    w.last().end();
    w.kept.stop();
    expect(w.timers[0]!.cleared).toBe(true);
    w.kept.hurry();
    expect(w.sockets.length).toBe(1);
  });

  test("a socket that cannot be made is one that ended", () => {
    const states: Connection[] = [];
    const pauses: number[] = [];
    keep({
      open: () => {
        throw new Error("no socket");
      },
      onMessage: () => {},
      onState: (s) => states.push(s),
      setTimer: (_run, ms) => pauses.push(ms),
      clearTimer: () => {},
      chance: () => 1,
    });
    expect(states).toEqual(["lost"]);
    expect(pauses).toEqual([1000]);
  });

  test("the pauses have the jitter of chance", () => {
    const w = world(0);
    w.last().end();
    w.elapse();
    w.last().end();
    expect(w.timers.map((t) => t.ms)).toEqual([500, 1000]);
  });
});

describe("the URL of a socket", () => {
  test("ws for http, wss for https, the host of the page", () => {
    expect(socketURL("/api/watch", { protocol: "http:", host: "127.0.0.1:8080" })).toBe("ws://127.0.0.1:8080/api/watch");
    expect(socketURL("/api/watch", { protocol: "https:", host: "draal.example" })).toBe("wss://draal.example/api/watch");
  });
});
```

- [ ] **Step 15: Run the test to see it fail**

Run: `nix develop .#bare --command npm --prefix ui test -- src/api/reconnect.test.ts`
Expected: FAIL — `Error: Cannot find module './reconnect'` (the test file fails before any test runs: `Test Files  1 failed (1)`)

- [ ] **Step 16: Write the connection that is kept (Review Focus 3)**

`ui/src/api/reconnect.ts`:

```ts
// A socket that is opened again whenever it ends: the schedule of the
// pauses, and the keeping of one connection. Nothing here touches the
// browser directly: what it needs of it (a socket, a timer, chance) is
// handed in, so that the whole is tested without one.

/** The first pause, in milliseconds. */
export const FIRST_PAUSE = 1000;

/** The longest pause, in milliseconds. */
export const LONGEST_PAUSE = 30_000;

/** The pause before the attempt with the number given (0 for the first
 *  one after the connection ended): 1 s doubling to 30 s, and of that a
 *  share between a half and the whole, which chance (0 to 1) picks, so
 *  that the pages of a draal that was restarted do not all come back in
 *  the same instant. */
export function pause(attempt: number, chance: number): number {
  const n = Number.isFinite(attempt) && attempt > 0 ? Math.min(Math.floor(attempt), 30) : 0;
  const full = Math.min(LONGEST_PAUSE, FIRST_PAUSE * 2 ** n);
  const c = Number.isFinite(chance) ? Math.min(1, Math.max(0, chance)) : 1;
  return Math.round(full * (0.5 + c / 2));
}

/** The state of a connection that is kept: connecting until the first
 *  message ever, live after a message, lost from the end of a socket
 *  until the next message. */
export type Connection = "connecting" | "live" | "lost";

/** What a kept connection needs of a socket. A WebSocket is one. */
export interface SocketLike {
  onmessage: ((event: { data: unknown }) => void) | null;
  onclose: ((event: { code?: number }) => void) | null;
  onerror: ((event: unknown) => void) | null;
  close(): void;
}

/** What Keep is given. */
export interface KeepOptions {
  /** Opens a socket. */
  open: () => SocketLike;
  /** A message of the socket that is open. */
  onMessage: (data: unknown) => void;
  /** The state, whenever it changes. */
  onState: (state: Connection) => void;
  /** setTimeout and clearTimeout, or what stands for them in a test. */
  setTimer?: (run: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
  /** Math.random, or what stands for it in a test. */
  chance?: () => number;
}

/** A connection that is kept: see keep. */
export interface Kept {
  /** Tries now if the connection is lost and a pause is running: for when
   *  the tab is looked at again or the network is back. */
  hurry(): void;
  /** Ends the connection for good. */
  stop(): void;
}

/** Opens a socket and keeps one open: when it ends, another is opened
 *  after a pause, and the pauses grow until a message arrives. */
export function keep(options: KeepOptions): Kept {
  const setTimer = options.setTimer ?? ((run, ms) => setTimeout(run, ms));
  const clearTimer = options.clearTimer ?? ((t) => clearTimeout(t as ReturnType<typeof setTimeout>));
  const chance = options.chance ?? Math.random;

  let socket: SocketLike | undefined;
  let timer: unknown;
  let waiting = false;
  let attempt = 0;
  let stopped = false;
  let state: Connection = "connecting";

  const set = (next: Connection) => {
    if (state !== next) {
      state = next;
      options.onState(next);
    }
  };

  const ended = (of: SocketLike) => {
    // A socket that fails says so twice (error, then close), and one
    // that was replaced may still say it: only the current one counts.
    if (stopped || socket !== of) {
      return;
    }
    socket = undefined;
    of.onmessage = of.onclose = of.onerror = null;
    try {
      of.close();
    } catch {
      // It is closed already.
    }
    set("lost");
    waiting = true;
    timer = setTimer(() => {
      waiting = false;
      open();
    }, pause(attempt, chance()));
    attempt++;
  };

  const open = () => {
    if (stopped) {
      return;
    }
    let s: SocketLike;
    try {
      s = options.open();
    } catch {
      // A socket that cannot even be made is one that ended at once.
      s = { onmessage: null, onclose: null, onerror: null, close() {} };
      socket = s;
      ended(s);
      return;
    }
    socket = s;
    s.onmessage = (event) => {
      if (stopped || socket !== s) {
        return;
      }
      // A message is what shows that draal is there: the pauses start
      // over.
      attempt = 0;
      set("live");
      options.onMessage(event.data);
    };
    s.onclose = () => ended(s);
    s.onerror = () => ended(s);
  };

  open();

  return {
    hurry() {
      if (stopped || !waiting) {
        return;
      }
      clearTimer(timer);
      waiting = false;
      open();
    },
    stop() {
      if (stopped) {
        return;
      }
      stopped = true;
      if (waiting) {
        clearTimer(timer);
        waiting = false;
      }
      const s = socket;
      socket = undefined;
      if (s) {
        s.onmessage = s.onclose = s.onerror = null;
        s.close();
      }
    },
  };
}

/** The URL of a WebSocket of the API on the host of the page: ws: for a
 *  page of http:, wss: for one of https:. */
export function socketURL(path: string, at: { protocol: string; host: string } = location): string {
  return `${at.protocol === "https:" ? "wss:" : "ws:"}//${at.host}${path}`;
}
```

- [ ] **Step 17: Run the tests to see them pass**

Run: `nix develop .#bare --command npm --prefix ui test`
Expected: PASS — `Test Files  3 passed (3)`, and `nix develop .#bare --command npm --prefix ui run check` prints no error.

- [ ] **Step 18: Commit**

```bash
git add ui/src/api/reconnect.ts ui/src/api/reconnect.test.ts
git commit -m "UI: a connection that is kept, with pauses that grow"
```

- [ ] **Step 19: Write the failing tests of the client and of the fleet**

`ui/src/api/client.test.ts`:

```ts
import { expect, test } from "vitest";
import { errorOf } from "./client";

test("a refusal becomes its code and its message", () => {
  const e = errorOf(404, '{"error":{"code":"not-found","message":"zathras abc is not connected"}}');
  expect(e.code).toBe("not-found");
  expect(e.message).toBe("zathras abc is not connected");
  expect(e.status).toBe(404);
});

test("the plain line of the host rule is shown as its text", () => {
  const line = 'great-machine: this page was asked for as "example.com", and it answers on loopback only.\n';
  const e = errorOf(403, line);
  expect(e.code).toBe("http");
  expect(e.message).toBe(line.trim());
  expect(e.status).toBe(403);
});

test("an answer without text, or with a page of HTML, names its status", () => {
  expect(errorOf(502, "").message).toBe("draal answered with status 502.");
  expect(errorOf(502, "<html><body>Bad Gateway</body></html>").message).toBe("draal answered with status 502.");
});

test("JSON that is no refusal is shown as its text", () => {
  expect(errorOf(500, '{"oops":true}').message).toBe('{"oops":true}');
});
```

`ui/src/state/fleet.test.ts`:

```ts
import { expect, test } from "vitest";
import type { Fleet, Zathras } from "../api/types";
import { fleet, findService, findZathras, received, rows, take } from "./fleet";

function zathras(id: string, name: string): Zathras {
  return {
    id,
    name,
    connected: "2026-10-09T20:00:00Z",
    state: { generation: 0, phase: "idle", restart_at: "0001-01-01T00:00:00Z", restarts: 0 },
  };
}

test("the rows are by name, and zathrases of one name by ID", () => {
  const f: Fleet = {
    services: [
      { name: "worker", stable: 3, zathrases: [] },
      { name: "web", stable: 19, zathrases: [zathras("ee", "web-b"), zathras("ff", "web-a"), zathras("aa", "web-b")] },
    ],
  };
  const r = rows(f);
  expect(r.map((s) => s.key)).toEqual(["web", "worker"]);
  expect(r[0]!.zathrases.map((z) => z.key)).toEqual(["ff", "aa", "ee"]);
});

test("a fleet is taken into the store, and a zathras that stays keeps its row", () => {
  expect(received()).toBe(false);
  take(JSON.stringify({ services: [{ name: "web", stable: 1, zathrases: [zathras("aa", "a"), zathras("bb", "b")] }] }));
  expect(received()).toBe(true);
  const before = findZathras("web", "bb");
  expect(before?.name).toBe("b");

  // The first zathras leaves: the second is the same row still.
  take(JSON.stringify({ services: [{ name: "web", stable: 2, zathrases: [zathras("bb", "b")] }] }));
  expect(fleet.services[0]!.zathrases.length).toBe(1);
  expect(findZathras("web", "bb")).toBe(before);
  expect(findService("web")?.stable).toBe(2);
  expect(findZathras("web", "aa")).toBeUndefined();
  expect(findService("nope")).toBeUndefined();
});

test("a fleet with null lists is an empty one, and nonsense is not taken", () => {
  take(JSON.stringify({ services: [{ name: "web", stable: 1, zathrases: null }] }));
  expect(fleet.services[0]!.zathrases).toEqual([]);
  take("{not json");
  take(new ArrayBuffer(4));
  expect(fleet.services.length).toBe(1);
  take(JSON.stringify({ services: null }));
  expect(fleet.services).toEqual([]);
});
```

- [ ] **Step 20: Run the tests to see them fail**

Run: `nix develop .#bare --command npm --prefix ui test`
Expected: FAIL — `Error: Cannot find module './client'` and `Error: Cannot find module './fleet'`; `Test Files  2 failed | 3 passed (5)`.

- [ ] **Step 21: Write the client and the fleet**

`ui/src/api/client.ts`:

```ts
// What the page asks once: the JSON endpoints of the API.

import { isRefusal, type RolloutList, type SampleList } from "./types";

/** Why a request was not answered with what was asked for. */
export class ApiError extends Error {
  /** The code of a refusal ("not-found", "unavailable", …); "http" for
   *  an answer that is no refusal, as the plain line of the host rule;
   *  "network" when there was no answer. */
  readonly code: string;
  /** The HTTP status; 0 when there was no answer. */
  readonly status: number;

  constructor(code: string, message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
  }
}

/** What an answer that is not the one asked for becomes: a refusal with
 *  its code and message, anything else with its text. */
export function errorOf(status: number, body: string): ApiError {
  try {
    const v: unknown = JSON.parse(body);
    if (isRefusal(v)) {
      return new ApiError(String(v.error.code || "internal"), v.error.message, status);
    }
  } catch {
    // It is no JSON: its text is what there is to say.
  }
  const text = body.trim();
  // A page of HTML is not something to show as a sentence.
  const plain = text !== "" && !text.startsWith("<") ? text.slice(0, 600) : `draal answered with status ${status}.`;
  return new ApiError("http", plain, status);
}

/** Asks for a path of the API and returns the JSON of the answer. It
 *  rejects with an ApiError and nothing else. */
export async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, { signal, headers: { Accept: "application/json" } });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") {
      throw new ApiError("aborted", "The request was given up.", 0);
    }
    throw new ApiError("network", "draal does not answer.", 0);
  }
  let body: string;
  try {
    body = await response.text();
  } catch {
    throw new ApiError("network", "The answer of draal broke off.", response.status);
  }
  if (!response.ok) {
    throw errorOf(response.status, body);
  }
  try {
    return JSON.parse(body) as T;
  } catch {
    throw new ApiError("http", "draal answered with something that is no JSON.", response.status);
  }
}

/** The last rollouts of a service, newest first, at most 20. */
export async function rollouts(service: string, signal?: AbortSignal): Promise<RolloutList> {
  const list = await getJSON<RolloutList>(`/api/services/${encodeURIComponent(service)}/rollouts`, signal);
  return { rollouts: Array.isArray(list?.rollouts) ? list.rollouts : [] };
}

/** The samples draal keeps of a zathras, oldest first. */
export async function history(id: string, signal?: AbortSignal): Promise<SampleList> {
  const list = await getJSON<SampleList>(`/api/zathras/${encodeURIComponent(id)}/history`, signal);
  return { samples: Array.isArray(list?.samples) ? list.samples : [] };
}
```

The store holds a `key` beside every service (its name) and every
zathras (its ID), because `reconcile` matches the items of all lists by
one property: a zathras that stays is then the same object, and its row
on the page the same element, whatever came or went around it.

`ui/src/state/fleet.ts`:

```ts
// The fleet as the page holds it: ONE WebSocket to /api/watch for the
// whole page, and a store that every view reads. A new fleet changes in
// the store only what changed, so a list keeps its rows, and a row that
// is the same zathras stays the same element.

import { createSignal } from "solid-js";
import { createStore, reconcile } from "solid-js/store";
import { type Connection, keep, type SocketLike, socketURL } from "../api/reconnect";
import { type Fleet, type ID, normaliseFleet, type Service, type Zathras } from "../api/types";

/** A zathras of the store: key is its ID. */
export type ZathrasRow = Zathras & { key: string };

/** A service of the store: key is its name. reconcile matches the items
 *  of every list by this one property. */
export type ServiceRow = Omit<Service, "zathrases"> & { key: string; zathrases: ZathrasRow[] };

const [store, setStore] = createStore<{ services: ServiceRow[] }>({ services: [] });
const [connection, setConnection] = createSignal<Connection>("connecting");
const [received, setReceived] = createSignal(false);
const [freshAt, setFreshAt] = createSignal(0);
const [lives, setLives] = createSignal(0);
const [now, setNow] = createSignal(Date.now());

/** The fleet: the services by name, each with its zathrases by name and
 *  then by ID. While the connection is lost it is the last one. */
export const fleet: { readonly services: ServiceRow[] } = store;

export {
  /** The state of the connection to /api/watch. */
  connection,
  /** Whether a fleet has arrived since the page was opened. Before that
   *  no view may say that something is not there. */
  received,
  /** When the last fleet arrived, by the clock of the browser. */
  freshAt,
  /** How often the connection became live. It changes when draal is
   *  back, which is when a view asks again for what it asked once. */
  lives,
  /** The time of the browser, renewed every second: what "3 m ago" is
   *  counted from. */
  now,
};

/** The rows of a fleet, in the order the page keeps: services by name,
 *  zathrases by name and then by ID, whatever order they came in. */
export function rows(f: Fleet): ServiceRow[] {
  const by = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);
  return f.services
    .map((s) => ({
      ...s,
      key: s.name,
      zathrases: s.zathrases
        .map((z) => ({ ...z, key: z.id }))
        .sort((a, b) => by(a.name, b.name) || by(a.id, b.id)),
    }))
    .sort((a, b) => by(a.name, b.name));
}

/** The service of the name, if the fleet has it. */
export function findService(name: string): ServiceRow | undefined {
  return store.services.find((s) => s.name === name);
}

/** The zathras of the ID in the service, if it is connected. */
export function findZathras(service: string, id: ID): ZathrasRow | undefined {
  return findService(service)?.zathrases.find((z) => z.id === id);
}

/** Takes a message of /api/watch into the store. */
export function take(data: unknown): void {
  if (typeof data !== "string") {
    return;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(data);
  } catch {
    return;
  }
  setStore("services", reconcile(rows(normaliseFleet(parsed)), { key: "key" }));
  setFreshAt(Date.now());
  setReceived(true);
}

/** Opens the connection and keeps it. It returns what ends it. */
export function startFleet(): () => void {
  const kept = keep({
    // A WebSocket is a SocketLike but for the types of its events, which
    // say more than keep reads.
    open: () => new WebSocket(socketURL("/api/watch")) as unknown as SocketLike,
    onMessage: take,
    onState: (state) => {
      setConnection(state);
      if (state === "live") {
        setLives((n) => n + 1);
      }
    },
  });
  // A page that is looked at again, or whose network is back, does not
  // wait for the end of a long pause.
  const hurry = () => {
    if (document.visibilityState === "visible") {
      kept.hurry();
    }
  };
  document.addEventListener("visibilitychange", hurry);
  window.addEventListener("online", hurry);
  const tick = setInterval(() => setNow(Date.now()), 1000);
  return () => {
    clearInterval(tick);
    document.removeEventListener("visibilitychange", hurry);
    window.removeEventListener("online", hurry);
    kept.stop();
  };
}
```

- [ ] **Step 22: Run the tests to see them pass, and commit**

Run: `nix develop .#bare --command npm --prefix ui test`
Expected: PASS — `Test Files  5 passed (5)`, `Tests  48 passed (48)`.

```bash
git add ui/src/api/client.ts ui/src/api/client.test.ts ui/src/state/
git commit -m "UI: the fleet in a store, and a client for what is asked once"
```

- [ ] **Step 23: The fonts, the tokens and the base styles**

The mark of the design system (its `Icon_White.png`) is a file of the
build. It is beside the plan: copy it, and see that it is the file this
plan was written with:

```bash
mkdir -p ui/src/assets
cp docs/superpowers/plans/2026-10-09-great-machine-stage-2/assets/mark-white.png ui/src/assets/mark-white.png
shasum -a 256 ui/src/assets/mark-white.png
```

Expected: `088e8020f8dc79f7f23458e3f1f20ddfcb81d5d71badc16392bbf676eafec13e  ui/src/assets/mark-white.png`

The fonts are the Latin files of the three packages, each one variable
font; Vite puts them into `web/dist/assets` and writes their names into
the stylesheet.

`ui/src/styles/fonts.css`:

```css
/* The three families of the design system, as files of the build: the
   page asks nothing of another host. Each is one variable font with the
   Latin letters; text in another script falls back to the system's
   font. */

@font-face {
  font-family: "Montserrat Variable";
  font-style: normal;
  font-display: swap;
  font-weight: 100 900;
  src: url("@fontsource-variable/montserrat/files/montserrat-latin-wght-normal.woff2") format("woff2-variations");
}

@font-face {
  font-family: "Inter Variable";
  font-style: normal;
  font-display: swap;
  font-weight: 100 900;
  src: url("@fontsource-variable/inter/files/inter-latin-wght-normal.woff2") format("woff2-variations");
}

@font-face {
  font-family: "JetBrains Mono Variable";
  font-style: normal;
  font-display: swap;
  font-weight: 100 800;
  src: url("@fontsource-variable/jetbrains-mono/files/jetbrains-mono-latin-wght-normal.woff2") format("woff2-variations");
}
```

The chart colours `--series-1` and `--series-2` were checked with the
validator of the skill `dataviz` for the light ground (`#1f5bb3`,
`#2db6be`) and for the dark one (`#2f6fd6`, `#25a8b0`): the brand's own
blue and teal are too near each other to tell two lines apart (ΔE 16
passes barely in the light and fails in the dark), so the blue is a
step darker.

`ui/src/styles/tokens.css`:

```css
/* The tokens of the Fables for Robots design system, and what this page
   makes of them. The brand is four colours: black, white, teal and
   blue. The scales below them are the design system's own. The two
   signal colours (warn, bad) are not the brand's: they are here because
   a halted rollout and a service that keeps ending must not look like
   anything else, and they are used for state alone. */

:root {
  /* The brand. */
  --ffr-black: #000000;
  --ffr-white: #ffffff;
  --ffr-teal: #2db6be;
  --ffr-blue: #358bf2;

  --ffr-ink-900: #000000;
  --ffr-ink-800: #0a0a0b;
  --ffr-ink-700: #141418;
  --ffr-ink-600: #1c1c22;
  --ffr-ink-500: #262630;
  --ffr-ink-400: #3a3a46;
  --ffr-ink-300: #5b5b6a;
  --ffr-ink-200: #8d8d9a;
  --ffr-ink-100: #c7c7ce;
  --ffr-ink-050: #eeeef1;
  --ffr-ink-025: #f7f7f9;

  --ffr-teal-700: #1a7c82;
  --ffr-teal-300: #7fd4d9;
  --ffr-teal-100: #d5f1f3;
  --ffr-blue-700: #1f5bb3;
  --ffr-blue-300: #8ab9f8;
  --ffr-blue-100: #dbe9fd;

  --ffr-font-display: "Montserrat Variable", "Inter Variable", system-ui, sans-serif;
  --ffr-font-body: "Inter Variable", system-ui, sans-serif;
  --ffr-font-mono: "JetBrains Mono Variable", ui-monospace, SFMono-Regular, Menlo, monospace;

  --ffr-space-1: 4px;
  --ffr-space-2: 8px;
  --ffr-space-3: 12px;
  --ffr-space-4: 16px;
  --ffr-space-5: 24px;
  --ffr-space-6: 32px;
  --ffr-space-7: 48px;

  --ffr-radius-sm: 4px;
  --ffr-radius-md: 8px;
  --ffr-radius-pill: 999px;

  --ffr-ease-standard: cubic-bezier(0.2, 0.7, 0.1, 1);
  --ffr-duration-fast: 120ms;
  --ffr-duration-normal: 220ms;

  /* What the page draws with, in the light. */
  color-scheme: light dark;
  --bg: var(--ffr-white);
  --surface: var(--ffr-ink-025);
  --surface-strong: var(--ffr-ink-050);
  --fg: var(--ffr-black);
  --fg-muted: var(--ffr-ink-300);
  --fg-subtle: #74748a;
  --border: #dedee4;
  --border-strong: var(--ffr-black);
  --link: var(--ffr-blue-700);
  --focus: var(--ffr-blue);

  /* State. Each is used with a word beside it, never alone. */
  --ok: var(--ffr-teal);
  --ok-text: var(--ffr-teal-700);
  --ok-wash: var(--ffr-teal-100);
  --busy: var(--ffr-blue);
  --busy-text: var(--ffr-blue-700);
  --busy-wash: var(--ffr-blue-100);
  --warn: #c98a00;
  --warn-text: #8a5a00;
  --warn-wash: #fbefd2;
  --bad: #d43a48;
  --bad-text: #b0202e;
  --bad-wash: #fde4e6;
  --idle: var(--ffr-ink-200);
  --idle-text: var(--ffr-ink-300);

  /* The two series of a chart, checked for the light surface. */
  --series-1: #1f5bb3;
  --series-2: #2db6be;
  --grid: #e6e6ec;

  /* The sixteen colours of a terminal, for the log and the shell. */
  --term-bg: var(--ffr-ink-025);
  --term-fg: var(--ffr-ink-700);
  --term-cursor: var(--ffr-blue-700);
  --term-selection: #bcd6fb;
  --term-black: #141418;
  --term-red: #b0202e;
  --term-green: #1c7c4a;
  --term-yellow: #8a5a00;
  --term-blue: #1f5bb3;
  --term-magenta: #8b3fa8;
  --term-cyan: #1a7c82;
  --term-white: #5b5b6a;
  --term-bright-black: #74748a;
  --term-bright-red: #d43a48;
  --term-bright-green: #249a5c;
  --term-bright-yellow: #a86f00;
  --term-bright-blue: #358bf2;
  --term-bright-magenta: #a855c7;
  --term-bright-cyan: #1f97a0;
  --term-bright-white: #3a3a46;
}

@media (prefers-color-scheme: dark) {
  :root {
    --bg: var(--ffr-black);
    --surface: var(--ffr-ink-700);
    --surface-strong: var(--ffr-ink-500);
    --fg: var(--ffr-white);
    --fg-muted: var(--ffr-ink-100);
    --fg-subtle: var(--ffr-ink-200);
    --border: #2c2c38;
    --border-strong: var(--ffr-white);
    --link: var(--ffr-blue-300);
    --focus: var(--ffr-blue-300);

    --ok-text: var(--ffr-teal-300);
    --ok-wash: #0f3437;
    --busy-text: var(--ffr-blue-300);
    --busy-wash: #132b4d;
    --warn: #e9a821;
    --warn-text: #f3c462;
    --warn-wash: #3a2c08;
    --bad: #f0606c;
    --bad-text: #ff8f98;
    --bad-wash: #43141a;
    --idle: var(--ffr-ink-300);
    --idle-text: var(--ffr-ink-100);

    /* The same two hues, stepped for a black surface. */
    --series-1: #2f6fd6;
    --series-2: #25a8b0;
    --grid: #23232d;

    --term-bg: var(--ffr-ink-800);
    --term-fg: #e4e4ea;
    --term-cursor: var(--ffr-teal);
    --term-selection: #1f4a80;
    --term-black: #262630;
    --term-red: #f0606c;
    --term-green: #4cc38a;
    --term-yellow: #e9a821;
    --term-blue: #5fa3f5;
    --term-magenta: #c586e0;
    --term-cyan: #2db6be;
    --term-white: #c7c7ce;
    --term-bright-black: #8d8d9a;
    --term-bright-red: #ff8f98;
    --term-bright-green: #7fdcae;
    --term-bright-yellow: #f3c462;
    --term-bright-blue: #8ab9f8;
    --term-bright-magenta: #dcaaf0;
    --term-bright-cyan: #7fd4d9;
    --term-bright-white: #ffffff;
  }
}
```

`ui/src/styles/base.css`:

```css
/* The page's own styles: the ground, the type, the frame of every view,
   and the few things every view is made of. */

*,
*::before,
*::after {
  box-sizing: border-box;
}

html {
  -webkit-text-size-adjust: 100%;
  text-size-adjust: 100%;
}

body {
  margin: 0;
  background: var(--bg);
  color: var(--fg);
  font-family: var(--ffr-font-body);
  font-size: 14px;
  line-height: 1.45;
  font-feature-settings: "tnum", "cv11";
  -webkit-font-smoothing: antialiased;
}

h1,
h2,
h3,
p,
dl,
dd,
ul,
ol {
  margin: 0;
  padding: 0;
}

ul,
ol {
  list-style: none;
}

a {
  color: var(--link);
  text-decoration: none;
}

a:hover {
  text-decoration: underline;
  text-underline-offset: 3px;
}

button {
  font: inherit;
  color: inherit;
}

:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
  border-radius: 2px;
}

::selection {
  background: var(--term-selection);
}

/* ---- The frame ------------------------------------------------- */

.frame {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

/* The header is the brand's ground in both schemes: black, with the
   glow of teal and blue at its far end, and the mark in white. */
.top {
  position: sticky;
  top: 0;
  z-index: 10;
  color: var(--ffr-white);
  background:
    radial-gradient(circle at 88% -60%, rgba(53, 139, 242, 0.6) 0%, rgba(53, 139, 242, 0) 30%),
    radial-gradient(circle at 99% 150%, rgba(45, 182, 190, 0.5) 0%, rgba(45, 182, 190, 0) 24%),
    var(--ffr-black);
  border-bottom: 1px solid var(--ffr-ink-500);
}

.top-in {
  max-width: 1280px;
  margin: 0 auto;
  padding: 0 var(--ffr-space-5);
  min-height: 52px;
  display: flex;
  align-items: center;
  gap: var(--ffr-space-4);
  flex-wrap: wrap;
}

.brand {
  display: flex;
  align-items: center;
  gap: var(--ffr-space-2);
  color: var(--ffr-white);
  font-family: var(--ffr-font-display);
  font-weight: 800;
  font-size: 16px;
  letter-spacing: -0.02em;
  white-space: nowrap;
}

.brand:hover {
  text-decoration: none;
}

/* The mark's file has a wide margin around the gear: the box shows its
   middle. */
.mark {
  width: 30px;
  height: 30px;
  overflow: hidden;
  position: relative;
  flex: none;
}

.mark img {
  position: absolute;
  width: 66px;
  height: 66px;
  left: -17px;
  top: -16px;
}

.top nav {
  min-width: 0;
  flex: 1 1 0;
}

.crumbs {
  display: flex;
  align-items: center;
  gap: var(--ffr-space-2);
  min-width: 0;
  font-size: 13px;
  font-weight: 500;
}

.crumbs li {
  display: flex;
  align-items: center;
  gap: var(--ffr-space-2);
  min-width: 0;
}

.crumbs li + li::before {
  content: "/";
  color: var(--ffr-ink-300);
}

.crumbs a,
.crumbs span {
  color: var(--ffr-ink-100);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 28ch;
}

.crumbs a:hover {
  color: var(--ffr-white);
}

.crumbs [aria-current] {
  color: var(--ffr-white);
  font-weight: 600;
}

.link {
  display: inline-flex;
  align-items: center;
  gap: var(--ffr-space-2);
  font-family: var(--ffr-font-display);
  font-weight: 700;
  font-size: 11px;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  white-space: nowrap;
  padding: 5px 10px;
  border-radius: var(--ffr-radius-pill);
  border: 1.5px solid var(--ffr-ink-400);
  background: rgba(0, 0, 0, 0.55);
}

.link::before {
  content: "";
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
}

.link[data-state="live"] {
  color: var(--ffr-teal);
  border-color: var(--ffr-teal-700);
}

.link[data-state="connecting"] {
  color: var(--ffr-ink-100);
}

.link[data-state="lost"] {
  color: #ff8f98;
  border-color: #ff8f98;
}

@media (prefers-reduced-motion: no-preference) {
  .link[data-state="connecting"]::before,
  .link[data-state="lost"]::before {
    animation: blink 1.2s steps(2, jump-none) infinite;
  }
}

@keyframes blink {
  to {
    opacity: 0.25;
  }
}

/* What is said across the page while the connection is lost. */
.lost {
  background: var(--bad-wash);
  color: var(--bad-text);
  border-bottom: 1px solid var(--bad);
  font-weight: 500;
}

.lost p {
  max-width: 1280px;
  margin: 0 auto;
  padding: var(--ffr-space-2) var(--ffr-space-5);
}

.view {
  width: 100%;
  max-width: 1280px;
  margin: 0 auto;
  padding: var(--ffr-space-5) var(--ffr-space-5) var(--ffr-space-7);
  flex: 1;
}

/* The last fleet stays while the connection is lost, and looks it. */
.view[data-stale] {
  opacity: 0.62;
}

/* ---- Type ------------------------------------------------------ */

h1 {
  font-family: var(--ffr-font-display);
  font-weight: 800;
  font-size: 28px;
  line-height: 1.1;
  letter-spacing: -0.02em;
  overflow-wrap: anywhere;
}

h2 {
  font-family: var(--ffr-font-display);
  font-weight: 700;
  font-size: 17px;
  line-height: 1.2;
  letter-spacing: -0.01em;
}

.mono {
  font-family: var(--ffr-font-mono);
  font-size: 0.93em;
  font-feature-settings: "zero";
}

.muted {
  color: var(--fg-muted);
}

.label {
  font-family: var(--ffr-font-display);
  font-weight: 700;
  font-size: 11px;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--fg-muted);
}

.num {
  font-family: var(--ffr-font-mono);
  font-size: 0.93em;
  white-space: nowrap;
}

/* ---- Pieces ---------------------------------------------------- */

.head {
  display: flex;
  align-items: baseline;
  gap: var(--ffr-space-3) var(--ffr-space-4);
  flex-wrap: wrap;
  margin-bottom: var(--ffr-space-5);
}

.head .muted {
  font-size: 14px;
}

.section {
  margin-top: var(--ffr-space-6);
}

.section > h2 {
  margin-bottom: var(--ffr-space-3);
}

.panel {
  border: 1px solid var(--border);
  border-radius: var(--ffr-radius-md);
  background: var(--bg);
}

/* What a view says when it has nothing to show, or could not get it. */
.notice {
  border: 1px solid var(--border);
  border-radius: var(--ffr-radius-md);
  background: var(--surface);
  padding: var(--ffr-space-5);
  max-width: 76ch;
}

.notice h2 {
  margin-bottom: var(--ffr-space-2);
}

.notice p + p {
  margin-top: var(--ffr-space-2);
}

.notice[data-kind="bad"] {
  border-color: var(--bad);
  background: var(--bad-wash);
}

.notice.small {
  padding: var(--ffr-space-3) var(--ffr-space-4);
}

/* A command to type: a line of its own, that can be selected whole. */
.cmd {
  display: block;
  margin-top: var(--ffr-space-3);
  padding: var(--ffr-space-2) var(--ffr-space-3);
  border-radius: var(--ffr-radius-sm);
  background: var(--ffr-black);
  color: var(--ffr-white);
  font-family: var(--ffr-font-mono);
  font-size: 13px;
  overflow-wrap: anywhere;
  user-select: all;
}

@media (prefers-color-scheme: dark) {
  .cmd {
    background: var(--ffr-ink-500);
  }
}

.cmd + p {
  margin-top: var(--ffr-space-3);
}

/* Why something failed, in the words of draal or of a zathras: in full,
   however long, and as it was written. */
.reason {
  color: var(--bad-text);
  font-family: var(--ffr-font-mono);
  font-size: 13px;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

/* An endpoint ID, short; the whole of it is its title and what a click
   copies. */
.id {
  font-family: var(--ffr-font-mono);
  font-size: 0.93em;
  color: var(--fg-muted);
  background: none;
  border: 0;
  border-bottom: 1px dashed var(--border);
  padding: 0;
  cursor: copy;
  white-space: nowrap;
}

.id:hover {
  color: var(--fg);
  border-bottom-color: var(--fg-muted);
}

.id[data-said]::after {
  content: " " attr(data-said);
  font-family: var(--ffr-font-body);
  color: var(--ok-text);
}

/* A state as a word with its colour beside it. */
.badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--ffr-font-display);
  font-weight: 700;
  font-size: 11px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  white-space: nowrap;
  padding: 3px 9px;
  border-radius: var(--ffr-radius-pill);
  border: 1.5px solid var(--tone, var(--idle));
  color: var(--tone-text, var(--idle-text));
  background: var(--tone-wash, transparent);
}

.badge::before {
  content: "";
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--tone, var(--idle));
}

[data-tone="ok"] {
  --tone: var(--ok);
  --tone-text: var(--ok-text);
  --tone-wash: var(--ok-wash);
}

[data-tone="busy"] {
  --tone: var(--busy);
  --tone-text: var(--busy-text);
  --tone-wash: var(--busy-wash);
}

[data-tone="warn"] {
  --tone: var(--warn);
  --tone-text: var(--warn-text);
  --tone-wash: var(--warn-wash);
}

[data-tone="bad"] {
  --tone: var(--bad);
  --tone-text: var(--bad-text);
  --tone-wash: var(--bad-wash);
}

[data-tone="idle"] {
  --tone: var(--idle);
  --tone-text: var(--idle-text);
  --tone-wash: transparent;
}

@media (max-width: 640px) {
  .top-in {
    padding: var(--ffr-space-2) var(--ffr-space-4);
    gap: var(--ffr-space-2) var(--ffr-space-3);
  }

  .top nav {
    order: 3;
    flex-basis: 100%;
  }

  .link {
    margin-left: auto;
  }

  .view {
    padding: var(--ffr-space-4) var(--ffr-space-4) var(--ffr-space-7);
  }

  .lost p {
    padding: var(--ffr-space-2) var(--ffr-space-4);
  }

  h1 {
    font-size: 23px;
  }
}
```

- [ ] **Step 24: The frame, the routes and the places of the views**

`ui/src/components/ShortID.tsx`:

```tsx
import { createSignal, onCleanup } from "solid-js";
import { shortID } from "../format";

// An endpoint ID as its first ten characters. The whole of it is the
// title, and a click copies it.
export default function ShortID(props: { id: string }) {
  const [said, setSaid] = createSignal<string>();
  let timer: ReturnType<typeof setTimeout> | undefined;
  onCleanup(() => clearTimeout(timer));

  const copy = async (event: MouseEvent) => {
    // An ID inside a link or a row is copied, and nothing else happens.
    event.preventDefault();
    event.stopPropagation();
    let word = "copied";
    try {
      await navigator.clipboard.writeText(props.id);
    } catch {
      word = "not copied";
    }
    setSaid(word);
    clearTimeout(timer);
    timer = setTimeout(() => setSaid(undefined), 1500);
  };

  return (
    <button type="button" class="id" title={`${props.id} (click to copy)`} data-said={said()} onClick={copy}>
      {shortID(props.id)}
    </button>
  );
}
```

`ui/src/components/Notice.tsx`:

```tsx
import { type JSX, Show } from "solid-js";

// What a view says when it has nothing to show or could not get it: a
// heading that names what is missing, and what to do about it.
export default function Notice(props: { title?: string; kind?: "bad"; small?: boolean; children?: JSX.Element }) {
  return (
    <div class="notice" classList={{ small: props.small }} data-kind={props.kind} role={props.kind === "bad" ? "alert" : undefined}>
      <Show when={props.title}>
        <h2>{props.title}</h2>
      </Show>
      {props.children}
    </div>
  );
}
```

`ui/src/components/Layout.tsx`:

```tsx
import { A, type RouteSectionProps, useLocation } from "@solidjs/router";
import { createEffect, createMemo, For, Show } from "solid-js";
import mark from "../assets/mark-white.png";
import { isID } from "../api/types";
import { ago, shortID } from "../format";
import { connection, findZathras, freshAt, now, received } from "../state/fleet";

// A step of the path: what it is called and where it leads.
interface Crumb {
  text: string;
  href: string;
  title?: string;
}

// A part of a path as text. A part that is no valid encoding is itself.
function decode(part: string): string {
  try {
    return decodeURIComponent(part);
  } catch {
    return part;
  }
}

// The steps of a path: the services, a service, a zathras.
function crumbsOf(path: string): Crumb[] {
  const parts = path.split("/").filter((p) => p !== "");
  const crumbs: Crumb[] = [{ text: "Services", href: "/" }];
  if (parts[0] !== "s" || parts[1] === undefined) {
    return crumbs;
  }
  const service = decode(parts[1]);
  crumbs.push({ text: service, href: `/s/${parts[1]}`, title: service });
  if (parts[2] !== "z" || parts[3] === undefined) {
    return crumbs;
  }
  const id = decode(parts[3]);
  // The name of the zathras when the fleet has it, and its ID until then.
  const name = isID(id) ? findZathras(service, id)?.name : undefined;
  crumbs.push({ text: name ?? shortID(id), href: `/s/${parts[1]}/z/${parts[3]}`, title: name ?? id });
  return crumbs;
}

const WORDS = { connecting: "Connecting", live: "Live", lost: "Lost" } as const;

// The frame of every view: the header with the name of the program, the
// path as breadcrumbs and the state of the connection; what there is to
// say while the connection is lost; and the view.
export default function Layout(props: RouteSectionProps) {
  const location = useLocation();
  const crumbs = createMemo(() => crumbsOf(location.pathname));
  const lost = () => connection() === "lost";

  createEffect(() => {
    const list = crumbs();
    const last = list[list.length - 1];
    document.title = list.length > 1 && last ? `${last.text} – great-machine` : "great-machine";
  });

  return (
    <div class="frame">
      <header class="top">
        <div class="top-in">
          <A href="/" class="brand" aria-label="great-machine: the services">
            <span class="mark" aria-hidden="true">
              <img src={mark} alt="" width="66" height="66" />
            </span>
            great-machine
          </A>
          <nav aria-label="Where this is">
            <ol class="crumbs">
              <For each={crumbs()}>
                {(c, i) => (
                  <li>
                    <Show
                      when={i() < crumbs().length - 1}
                      fallback={
                        <span aria-current="page" title={c.title}>
                          {c.text}
                        </span>
                      }
                    >
                      <A href={c.href} title={c.title}>
                        {c.text}
                      </A>
                    </Show>
                  </li>
                )}
              </For>
            </ol>
          </nav>
          <span class="link" data-state={connection()} role="status" title="The connection of this page to draal">
            {WORDS[connection()]}
          </span>
        </div>
      </header>
      <Show when={lost()}>
        <div class="lost" role="alert">
          <p>
            <Show
              when={received()}
              fallback="draal does not answer. The page keeps trying, with pauses that grow to half a minute."
            >
              The connection to draal is lost. What is below is the fleet as it was {ago(freshAt(), now())}; the page keeps
              trying and carries on from here when draal is back.
            </Show>
          </p>
        </div>
      </Show>
      <main class="view" data-stale={lost() && received() ? "" : undefined}>
        {props.children}
      </main>
    </div>
  );
}
```

`ui/src/views/NotFound.tsx`:

```tsx
import { A, useLocation } from "@solidjs/router";
import Notice from "../components/Notice";

// What a path that is none of the page's shows.
export default function NotFound() {
  const location = useLocation();
  return (
    <Notice title="No such page">
      <p>
        The page has nothing at <span class="mono">{location.pathname}</span>. It has the services, a service at{" "}
        <span class="mono">/s/NAME</span>, and a zathras at <span class="mono">/s/NAME/z/ID</span>.
      </p>
      <p>
        <A href="/">Go to the services</A>
      </p>
    </Notice>
  );
}
```

The three views are written by Tasks 8 and 9. Until then each is a
place that shows that the fleet arrives:

`ui/src/views/Services.tsx`:

```tsx
import Notice from "../components/Notice";
import { fleet, received } from "../state/fleet";

// The place of the view; a later task writes it. Until then it shows
// that the fleet arrives.
export default function Services() {
  return (
    <Notice title="Services">
      <p>{received() ? `draal knows ${fleet.services.length} services.` : "Asking draal…"}</p>
    </Notice>
  );
}
```

`ui/src/views/Service.tsx` and `ui/src/views/Zathras.tsx` are the same
file with `Service` and `Zathras` in place of `Services` (the name of
the function and the title).

Replace `ui/src/App.tsx` and `ui/src/main.tsx`:

`ui/src/App.tsx`:

```tsx
import { Route, Router } from "@solidjs/router";
import { onCleanup, onMount } from "solid-js";
import Layout from "./components/Layout";
import { startFleet } from "./state/fleet";
import NotFound from "./views/NotFound";
import Service from "./views/Service";
import Services from "./views/Services";
import Zathras from "./views/Zathras";

// The page: one connection to the fleet for all of it, and a view for
// each of its three paths.
export default function App() {
  onMount(() => {
    const stop = startFleet();
    onCleanup(stop);
  });

  return (
    <Router root={Layout}>
      <Route path="/" component={Services} />
      <Route path="/s/:service" component={Service} />
      <Route path="/s/:service/z/:id" component={Zathras} />
      <Route path="*" component={NotFound} />
    </Router>
  );
}
```

`ui/src/main.tsx`:

```tsx
import { render } from "solid-js/web";
import App from "./App";
import "./styles/fonts.css";
import "./styles/tokens.css";
import "./styles/base.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("index.html has no element with the id root");
}
render(() => <App />, root);
```

- [ ] **Step 25: Check and build**

Run: `nix develop .#bare --command npm --prefix ui run check`
Expected: no error.

Run: `nix develop .#bare --command npm --prefix ui run build`
Expected: `✓ built`, and among the files `assets/montserrat-latin-wght-normal-….woff2`, `assets/inter-latin-wght-normal-….woff2`, `assets/jetbrains-mono-latin-wght-normal-….woff2` and `assets/mark-white-….png`.

- [ ] **Step 26: Look at it, and take draal away (Review Focus 3)**

Run the demo with the build: `nix develop .#bare --command go run ./web/demo --addr 127.0.0.1:8080 --dist web/dist`, and open
`http://127.0.0.1:8080/s/web` with the browser's console open.

To see: a black header with the mark, `great-machine`, the breadcrumbs
`Services / web` and, at the right, `LIVE` in teal; below it a box that
says `draal knows 4 services.` The type is Montserrat in the header and
Inter below. The console has no message: no error and no violation of
the content security policy (the fonts and the mark come from
`/assets/`). `http://127.0.0.1:8080/nothing` says `No such page`. With
the system set to dark the ground is black.

Now stop the demo (Ctrl-C). Within a second the header says `LOST`, a
band under it says `The connection to draal is lost. What is below is
the fleet as it was 3 s ago; …` and counts on, and the view is dimmed
and still there. Start the demo again: within the pause that is running
(at most 30 seconds; at once if the tab is hidden and shown again) the
header says `LIVE`, the band is gone, and the page is still at
`/s/web`: it was not loaded again.

- [ ] **Step 27: Nothing is put into the page as markup**

Run: `grep -rnE "innerHTML|outerHTML|insertAdjacentHTML|document\.write" ui/src; echo $?`
Expected: `1` and nothing else: no file of the page uses any of them.

- [ ] **Step 28: Commit**

```bash
git add ui/src/
git commit -m "UI: the frame of the page: tokens, fonts, header, routes, and what is said while the connection is lost"
```
