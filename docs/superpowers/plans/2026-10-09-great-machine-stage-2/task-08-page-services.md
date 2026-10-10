### Task 8: The services, and a service

**Files:**
- Create: `ui/src/state/rollout.ts`
- Create: `ui/src/components/Badge.tsx`, `ui/src/components/Strip.tsx`, `ui/src/components/strip.css`
- Modify: `ui/src/views/Services.tsx`, `ui/src/views/Service.tsx` (the places of Task 7, replaced)
- Create: `ui/src/views/services.css`, `ui/src/views/service.css`
- Test: `ui/src/state/rollout.test.ts`

**Interfaces:**
- Consumes:
  - Task 7: `fleet`, `received`, `now`, `lives`, `findService`, `ServiceRow`, `ZathrasRow` of `ui/src/state/fleet.ts`; the types and `underWay`, `isZeroTime` of `ui/src/api/types.ts`; `rollouts(service, signal?)` and `ApiError` of `ui/src/api/client.ts`; the formatters of `ui/src/format.ts`; `Notice`, `ShortID`; the classes of `base.css` and the tokens.
  - Task 1: `GET /api/services/{service}/rollouts` → `{"rollouts":[…]}`, newest first, at most 20, always a list; an unknown service gets an empty list; the counts `prepared`, `activated`, `awaited` are 0 there.
  - What a `Fleet` has of a rollout: the `RolloutInfo` of each service's last rollout with the counts of draal's last decision, and the `State` of every connected zathras. There is no record of a rollout for each zathras.
- Produces:
  - `ui/src/state/rollout.ts`: `type Stage = "waiting" | "preparing" | "prepared" | "on" | "failed"`, `STAGES`, `stageOf(state: State, target: number): Stage`, `byStage(zathrases, target): Record<Stage, Z[]>`, `PHASES`, `byPhase(zathrases): Record<Phase, number>`, `knownPhase(phase: string): Phase`, `phaseTone(phase)`, `rolloutTone(state)`, `phaseNote(state: State, now: number): string`, `open(r: RolloutInfo | undefined): r is RolloutInfo` (under way or halted).
  - `ui/src/components/Badge.tsx`: `PhaseBadge` (`phase`), `RolloutBadge` (`state`). `ui/src/components/Strip.tsx` (`zathrases`).
  - `ui/src/views/service.css`: the classes `scroll`, `table` (with `r`, `nw`, `why` for cells, and `data-label` on every cell for the narrow layout), `exit`, `tag`, which Task 9 uses too.
  - The views at `/` and `/s/:service`.

**What the page can say about a rollout.** The specification asks for
"the rollout under way with every zathras' part in it". The fleet
carries, of a rollout, its record and three counts; of a zathras, its
state. So a zathras' part is what its own state says about the
rollout's generation, and `stageOf` is that reading: it runs the
generation (`on`), it runs it and the service has ended (`failed`), it
is preparing it, it has prepared it, or none of these. Whether draal
counts a zathras as activated (it has run the generation for
`StableAfter`) the page cannot tell and does not claim: it shows
draal's three counts beside its own reading, under their own name. A
zathras that left is not in the fleet and so in no stage; the counts of
draal may still include it, which is why they are not drawn as a
progress bar over the zathrases listed. The list of
`/api/services/…/rollouts` has the counts as 0, so the table of the
last rollouts has no column for them.

**Review Focus 4 is partly this task's**: no service yet, a service
without a zathras, a service without a rollout. Each is a branch of a
view that says what is missing and what to do; Step 9 looks at each.

- [ ] **Step 1: Write the failing test of the reading of a rollout**

`ui/src/state/rollout.test.ts`:

```ts
import { describe, expect, test } from "vitest";
import type { State, Zathras } from "../api/types";
import { byPhase, byStage, knownPhase, open, phaseNote, phaseTone, rolloutTone, stageOf } from "./rollout";

const ZERO = "0001-01-01T00:00:00Z";

function state(over: Partial<State>): State {
  return { generation: 19, phase: "running", restart_at: ZERO, restarts: 0, ...over };
}

function zathras(id: string, over: Partial<State>): Zathras {
  return { id, name: `z-${id}`, connected: "2026-10-09T20:00:00Z", state: state(over) };
}

describe("where a zathras is in a rollout", () => {
  test("by what it reports of the rollout's generation", () => {
    expect(stageOf(state({ prepared: [19] }), 20)).toBe("waiting");
    expect(stageOf(state({ prepared: [19], preparing: 20 }), 20)).toBe("preparing");
    expect(stageOf(state({ prepared: [19, 20] }), 20)).toBe("prepared");
    expect(stageOf(state({ generation: 20, prepared: [19, 20] }), 20)).toBe("on");
    expect(stageOf(state({ generation: 20, phase: "stopping" }), 20)).toBe("on");
    expect(stageOf(state({ generation: 20, phase: "backoff" }), 20)).toBe("failed");
  });

  test("a zathras without a generation and without a list is waiting", () => {
    expect(stageOf({ generation: 0, phase: "idle", restart_at: ZERO, restarts: 0 }, 1)).toBe("waiting");
  });

  test("the zathrases of each stage keep their order", () => {
    const zs = [
      zathras("a", { generation: 20 }),
      zathras("b", { prepared: [20] }),
      zathras("c", { generation: 20, phase: "backoff" }),
      zathras("d", { prepared: [20] }),
      zathras("e", {}),
    ];
    const s = byStage(zs, 20);
    expect(s.on.map((z) => z.id)).toEqual(["a"]);
    expect(s.prepared.map((z) => z.id)).toEqual(["b", "d"]);
    expect(s.failed.map((z) => z.id)).toEqual(["c"]);
    expect(s.waiting.map((z) => z.id)).toEqual(["e"]);
    expect(s.preparing).toEqual([]);
  });
});

describe("phases", () => {
  test("are counted, and one the page does not know is idle", () => {
    const zs = [
      zathras("a", {}),
      zathras("b", {}),
      zathras("c", { phase: "backoff" }),
      zathras("d", { phase: "stopping" }),
      zathras("e", { phase: "idle" }),
      zathras("f", { phase: "hibernating" as never }),
    ];
    expect(byPhase(zs)).toEqual({ running: 2, stopping: 1, backoff: 1, idle: 2 });
    expect(byPhase([])).toEqual({ running: 0, stopping: 0, backoff: 0, idle: 0 });
    expect(knownPhase("hibernating")).toBe("idle");
  });

  test("have a tone", () => {
    expect(phaseTone("running")).toBe("ok");
    expect(phaseTone("stopping")).toBe("warn");
    expect(phaseTone("backoff")).toBe("bad");
    expect(phaseTone("idle")).toBe("idle");
    expect(phaseTone("")).toBe("idle");
  });

  test("have a note of since when or until when", () => {
    const now = Date.UTC(2026, 9, 9, 20, 0, 0);
    expect(phaseNote(state({ running_for: 2_739_385_042 }), now)).toBe("for 2.7 s");
    expect(phaseNote(state({}), now)).toBe("");
    expect(phaseNote(state({ phase: "backoff", restart_at: "2026-10-09T20:00:06Z" }), now)).toBe("starts again in 6 s");
    expect(phaseNote(state({ phase: "backoff", restart_at: "2026-10-09T19:59:00Z" }), now)).toBe("starts again now");
    expect(phaseNote(state({ phase: "backoff" }), now)).toBe("");
    expect(phaseNote(state({ phase: "stopping" }), now)).toBe("told to end");
    expect(phaseNote(state({ phase: "idle", generation: 0 }), now)).toBe("has no generation");
    expect(phaseNote(state({ phase: "idle", generation: 3 }), now)).toBe("");
  });
});

describe("rollouts", () => {
  test("have a tone", () => {
    expect(rolloutTone("preparing")).toBe("busy");
    expect(rolloutTone("activating")).toBe("busy");
    expect(rolloutTone("completed")).toBe("ok");
    expect(rolloutTone("halted")).toBe("bad");
    expect(rolloutTone("superseded")).toBe("idle");
    expect(rolloutTone("")).toBe("idle");
  });

  test("are open while under way or halted", () => {
    const r = {
      id: 1,
      service: "web",
      generation: 2,
      parallel: 1,
      created: "2026-10-09T20:00:00Z",
      created_by: "aa",
      finished: ZERO,
      prepared: 0,
      activated: 0,
      awaited: 0,
    };
    expect(open({ ...r, state: "preparing" })).toBe(true);
    expect(open({ ...r, state: "activating" })).toBe(true);
    expect(open({ ...r, state: "halted" })).toBe(true);
    expect(open({ ...r, state: "completed" })).toBe(false);
    expect(open({ ...r, state: "superseded" })).toBe(false);
    expect(open(undefined)).toBe(false);
  });
});
```

- [ ] **Step 2: Run the test to see it fail**

Run: `nix develop .#bare --command npm --prefix ui test -- src/state/rollout.test.ts`
Expected: FAIL — `Error: Cannot find module './rollout'` (the test file fails before any test runs: `Test Files  1 failed (1)`)

- [ ] **Step 3: Write the reading of a rollout**

`ui/src/state/rollout.ts`:

```ts
// What the page works out from the fleet about a rollout and about the
// phases of a service's zathrases. It reads what a zathras reports and
// nothing else: the fleet has no record of a rollout for each zathras,
// so where a zathras is in a rollout is what its own state says about
// the rollout's generation.

import type { Phase, RolloutInfo, State, Zathras } from "../api/types";
import { duration, until } from "../format";

/** Where a zathras is on the way to the generation of a rollout. */
export type Stage =
  /** It has not prepared the generation and is not preparing it. */
  | "waiting"
  /** It is making the closure of the generation present. */
  | "preparing"
  /** It has the closure and runs something else, or nothing. */
  | "prepared"
  /** It has taken the generation as its own and runs it, or is stopping
   *  it. Whether draal counts it as activated the page cannot tell:
   *  that is a matter of how long it has run, by a time draal is
   *  configured with. */
  | "on"
  /** It has taken the generation as its own, and the service has ended
   *  and waits to be started again. */
  | "failed";

/** The stages in the order of a rollout. */
export const STAGES: readonly Stage[] = ["waiting", "preparing", "prepared", "on", "failed"];

/** Where a zathras that reports state is on the way to the generation. */
export function stageOf(state: State, target: number): Stage {
  if (state.generation === target) {
    return state.phase === "backoff" ? "failed" : "on";
  }
  if (state.preparing === target) {
    return "preparing";
  }
  if (state.prepared?.includes(target)) {
    return "prepared";
  }
  return "waiting";
}

/** The zathrases of each stage, in the order they were given. */
export function byStage<Z extends Zathras>(zathrases: readonly Z[], target: number): Record<Stage, Z[]> {
  const out: Record<Stage, Z[]> = { waiting: [], preparing: [], prepared: [], on: [], failed: [] };
  for (const z of zathrases) {
    out[stageOf(z.state, target)].push(z);
  }
  return out;
}

/** The phases in the order the page lists them. */
export const PHASES: readonly Phase[] = ["running", "stopping", "backoff", "idle"];

/** How many zathrases are in each phase. A phase the page does not know
 *  is counted as idle: it runs nothing the page could name. */
export function byPhase(zathrases: readonly Zathras[]): Record<Phase, number> {
  const out: Record<Phase, number> = { running: 0, stopping: 0, backoff: 0, idle: 0 };
  for (const z of zathrases) {
    out[knownPhase(z.state.phase)]++;
  }
  return out;
}

/** The phase, or idle for one the page does not know. */
export function knownPhase(phase: string): Phase {
  return phase === "running" || phase === "stopping" || phase === "backoff" ? phase : "idle";
}

/** The tone a phase is drawn in. */
export function phaseTone(phase: string): "ok" | "warn" | "bad" | "idle" {
  switch (knownPhase(phase)) {
    case "running":
      return "ok";
    case "stopping":
      return "warn";
    case "backoff":
      return "bad";
    default:
      return "idle";
  }
}

/** The tone a state of a rollout is drawn in. */
export function rolloutTone(state: string): "ok" | "busy" | "bad" | "idle" {
  switch (state) {
    case "preparing":
    case "activating":
      return "busy";
    case "completed":
      return "ok";
    case "halted":
      return "bad";
    default:
      return "idle";
  }
}

/** What there is to say about a phase besides its name: since when, or
 *  until when. Empty when the state says nothing of it. */
export function phaseNote(state: State, now: number): string {
  switch (knownPhase(state.phase)) {
    case "running":
      return state.running_for === undefined ? "" : `for ${duration(state.running_for)}`;
    case "backoff": {
      const u = until(state.restart_at, now);
      return u === "–" ? "" : u === "now" ? "starts again now" : `starts again ${u}`;
    }
    case "stopping":
      return "told to end";
    default:
      return state.generation === 0 ? "has no generation" : "";
  }
}

/** Says whether a rollout is one to show with every zathras' part in
 *  it: under way, or halted. */
export function open(r: RolloutInfo | undefined): r is RolloutInfo {
  return r !== undefined && (r.state === "preparing" || r.state === "activating" || r.state === "halted");
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command npm --prefix ui test`
Expected: PASS — `Test Files  6 passed (6)`, and `nix develop .#bare --command npm --prefix ui run check` prints no error.

- [ ] **Step 5: Commit**

```bash
git add ui/src/state/rollout.ts ui/src/state/rollout.test.ts
git commit -m "UI: where a zathras is in a rollout, by what it reports"
```

- [ ] **Step 6: The services**

`ui/src/components/Badge.tsx`:

```tsx
import { knownPhase, phaseTone, rolloutTone } from "../state/rollout";

// The phase of a zathras as a word with its colour.
export function PhaseBadge(props: { phase: string }) {
  return (
    <span class="badge" data-tone={phaseTone(props.phase)}>
      {knownPhase(props.phase) === props.phase ? props.phase : props.phase || "unknown"}
    </span>
  );
}

// The state of a rollout as a word with its colour.
export function RolloutBadge(props: { state: string }) {
  return (
    <span class="badge" data-tone={rolloutTone(props.state)}>
      {props.state || "unknown"}
    </span>
  );
}
```

`ui/src/components/Strip.tsx`:

```tsx
import { For, Show } from "solid-js";
import type { ZathrasRow } from "../state/fleet";
import { byPhase, knownPhase, PHASES } from "../state/rollout";
import "./strip.css";

// The zathrases of a service as a row of blocks, one for each, in the
// order of the list, drawn by its phase; and beside it the number of
// each phase in words. The blocks are the picture and the words are what
// is read: no phase is told by its colour alone.
export default function Strip(props: { zathrases: readonly ZathrasRow[] }) {
  const counts = () => byPhase(props.zathrases);
  return (
    <div class="strip">
      <Show when={props.zathrases.length > 0}>
        <span class="strip-blocks" aria-hidden="true">
          <For each={props.zathrases}>
            {(z) => <i data-phase={knownPhase(z.state.phase)} title={`${z.name}: ${z.state.phase}`} />}
          </For>
        </span>
      </Show>
      <span class="strip-words">
        <Show when={props.zathrases.length > 0} fallback={<span class="muted">no zathras connected</span>}>
          <For each={PHASES.filter((p) => counts()[p] > 0)}>
            {(p) => (
              <span class="strip-word" data-phase={p}>
                <b class="num">{counts()[p]}</b> {p}
              </span>
            )}
          </For>
        </Show>
      </span>
    </div>
  );
}
```

`ui/src/components/strip.css`:

```css
/* The zathrases of a service, a block each. Running is a full block of
   the brand's teal; stopping is hollow; backoff is full, in the colour
   of what is wrong; idle is a low block. */

.strip {
  display: flex;
  align-items: center;
  gap: var(--ffr-space-2) var(--ffr-space-3);
  flex-wrap: wrap;
  min-width: 0;
}

.strip-blocks {
  display: flex;
  flex-wrap: wrap;
  gap: 3px;
  align-items: flex-end;
}

.strip-blocks i {
  display: block;
  width: 9px;
  height: 20px;
  border-radius: 2px;
  background: var(--ok);
}

.strip-blocks i[data-phase="stopping"] {
  background: transparent;
  border: 2px solid var(--warn);
}

.strip-blocks i[data-phase="backoff"] {
  background: var(--bad);
}

.strip-blocks i[data-phase="idle"] {
  height: 7px;
  background: var(--idle);
}

.strip-words {
  display: flex;
  flex-wrap: wrap;
  gap: 2px var(--ffr-space-3);
  font-size: 13px;
  color: var(--fg-muted);
}

.strip-word {
  white-space: nowrap;
}

.strip-word b {
  font-weight: 600;
  color: var(--fg);
}

.strip-word[data-phase="backoff"],
.strip-word[data-phase="backoff"] b {
  color: var(--bad-text);
}

.strip-word[data-phase="stopping"],
.strip-word[data-phase="stopping"] b {
  color: var(--warn-text);
}
```

Replace `ui/src/views/Services.tsx`:

`ui/src/views/Services.tsx`:

```tsx
import { A } from "@solidjs/router";
import { For, Match, Show, Switch } from "solid-js";
import { isZeroTime, underWay } from "../api/types";
import { RolloutBadge } from "../components/Badge";
import Notice from "../components/Notice";
import Strip from "../components/Strip";
import { ago, generation, plural } from "../format";
import { fleet, now, received, type ServiceRow } from "../state/fleet";
import "./services.css";

// The last rollout of a service in a few words: what it is, how far, and
// since when. A halted one has its reason in full.
function Rollout(props: { service: ServiceRow }) {
  const r = () => props.service.rollout;
  return (
    <Show when={r()} fallback={<p class="muted">Nothing was rolled out yet.</p>}>
      {(r) => (
        <>
          <p class="svc-roll-line">
            <RolloutBadge state={r().state} />
            <span>
              Rollout <span class="num">{r().id}</span> to generation <span class="num">{generation(r().generation)}</span>
            </span>
            <span class="muted">
              <Switch>
                <Match when={underWay(r().state)}>started {ago(r().created, now())}</Match>
                <Match when={!isZeroTime(r().finished)}>
                  {r().state === "halted" ? "halted" : "ended"} {ago(r().finished, now())}
                </Match>
              </Switch>
            </span>
          </p>
          <Show when={underWay(r().state)}>
            <p class="muted svc-counts">
              <span class="num">{r().prepared}</span> prepared, <span class="num">{r().activated}</span> activated,{" "}
              <span class="num">{r().awaited}</span> awaited
            </p>
          </Show>
          <Show when={r().reason}>
            <p class="reason">{r().reason}</p>
          </Show>
        </>
      )}
    </Show>
  );
}

// View 1: every service with its stable generation, its zathrases by
// phase, and its last rollout.
export default function Services() {
  const zathrases = () => fleet.services.reduce((n, s) => n + s.zathrases.length, 0);
  return (
    <Switch>
      <Match when={!received()}>
        <div class="head">
          <h1>Services</h1>
        </div>
        <p class="muted">Asking draal…</p>
      </Match>
      {/* Review Focus 4: no service yet, as a draal that was started for
          the first time has it. */}
      <Match when={fleet.services.length === 0}>
        <div class="head">
          <h1>Services</h1>
        </div>
        <Notice title="draal knows no service yet">
          <p>
            A service is there as soon as a zathras of it is connected or something was rolled out to it. Start a zathras
            and name its service:
          </p>
          <code class="cmd">great-machine zathras --draal ID --service NAME</code>
          <p class="muted">ID is the endpoint ID that draal logged when it started. This page shows the zathras as soon as it has registered.</p>
        </Notice>
      </Match>
      <Match when={true}>
        <div class="head">
          <h1>Services</h1>
          <span class="muted">
            {plural(fleet.services.length, "service", "services")}, {plural(zathrases(), "zathras", "zathrases")} connected
          </span>
        </div>
        <ul class="svcs">
          <For each={fleet.services}>
            {(s) => (
              <li class="svc panel" data-state={s.rollout?.state}>
                <div class="svc-name">
                  <A href={`/s/${encodeURIComponent(s.name)}`}>{s.name}</A>
                  <span class="muted">
                    <Show when={s.stable > 0} fallback="no stable generation">
                      stable generation <span class="num">{s.stable}</span>
                    </Show>
                  </span>
                </div>
                <div class="svc-fleet">
                  <Strip zathrases={s.zathrases} />
                </div>
                <div class="svc-roll">
                  <Rollout service={s} />
                </div>
              </li>
            )}
          </For>
        </ul>
      </Match>
    </Switch>
  );
}
```

`ui/src/views/services.css`:

```css
/* View 1: a row for each service: its name, its zathrases, its rollout. */

.svcs {
  display: grid;
  gap: var(--ffr-space-3);
}

.svc {
  display: grid;
  grid-template-columns: minmax(0, 5fr) minmax(0, 6fr) minmax(0, 8fr);
  gap: var(--ffr-space-3) var(--ffr-space-5);
  align-items: start;
  padding: var(--ffr-space-4) var(--ffr-space-5);
}

/* A service whose rollout is halted is the one to look at. */
.svc[data-state="halted"] {
  border-color: var(--bad);
}

.svc-name {
  display: grid;
  gap: 2px;
  min-width: 0;
}

.svc-name a {
  font-family: var(--ffr-font-display);
  font-weight: 800;
  font-size: 20px;
  line-height: 1.2;
  letter-spacing: -0.02em;
  color: var(--fg);
  overflow-wrap: anywhere;
}

.svc-name a:hover {
  color: var(--link);
}

.svc-fleet {
  padding-top: 3px;
  min-width: 0;
}

.svc-roll {
  display: grid;
  gap: var(--ffr-space-1);
  min-width: 0;
}

.svc-roll-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--ffr-space-1) var(--ffr-space-3);
}

@media (max-width: 860px) {
  .svc {
    grid-template-columns: minmax(0, 1fr);
    padding: var(--ffr-space-4);
  }
}
```

Run: `nix develop .#bare --command npm --prefix ui run check` and `nix develop .#bare --command npm --prefix ui run build`
Expected: no error, and `✓ built`.

```bash
git add ui/src/components/ ui/src/views/
git commit -m "UI: the services"
```

- [ ] **Step 7: A service**

The list of the last rollouts is asked for when the view opens, when
the last rollout of the fleet is another one or in another state, and
when draal is back (`lives`). The memos are what keeps a fleet that
merely arrived from asking again: a property read in an effect runs
the effect whenever the store is written, a memo only when its value
is another.

Replace `ui/src/views/Service.tsx`:

`ui/src/views/Service.tsx`:

```tsx
import { A, useParams } from "@solidjs/router";
import { createEffect, createMemo, createSignal, For, Match, on, onCleanup, Show, Switch } from "solid-js";
import { ApiError, rollouts as askRollouts } from "../api/client";
import { isZeroTime, type RolloutInfo, underWay } from "../api/types";
import { PhaseBadge, RolloutBadge } from "../components/Badge";
import Notice from "../components/Notice";
import ShortID from "../components/ShortID";
import { ago, between, bytes, cpu, dateTime, duration, exit, generation, percent, plural } from "../format";
import { findService, lives, now, received, type ServiceRow, type ZathrasRow } from "../state/fleet";
import { byStage, open, phaseNote, type Stage, STAGES } from "../state/rollout";
import "./service.css";

// What each stage of a rollout is called, with the generation.
function stageTitle(stage: Stage, target: number): string {
  switch (stage) {
    case "waiting":
      return "Not prepared";
    case "preparing":
      return "Preparing";
    case "prepared":
      return "Prepared";
    case "on":
      return `On generation ${target}`;
    case "failed":
      return "Ended on it";
  }
}

const STAGE_TONE: Record<Stage, string> = { waiting: "idle", preparing: "busy", prepared: "busy", on: "ok", failed: "bad" };

// The last rollout of the service: what it is, and, while it is under
// way or halted, where each connected zathras is in it.
function RolloutPanel(props: { service: ServiceRow; rollout: RolloutInfo }) {
  const r = () => props.rollout;
  const stages = () => byStage(props.service.zathrases, r().generation);
  return (
    <div class="panel roll" data-state={r().state}>
      <div class="roll-head">
        <RolloutBadge state={r().state} />
        <h2>
          Rollout <span class="num">{r().id}</span> to generation <span class="num">{generation(r().generation)}</span>
        </h2>
      </div>
      <dl class="facts">
        <div>
          <dt class="label">Started</dt>
          <dd>
            <span class="num">{dateTime(r().created)}</span> <span class="muted">({ago(r().created, now())})</span>
          </dd>
        </div>
        <Show when={!isZeroTime(r().finished)}>
          <div>
            <dt class="label">{r().state === "halted" ? "Halted" : "Ended"}</dt>
            <dd>
              <span class="num">{dateTime(r().finished)}</span>{" "}
              <span class="muted">
                ({ago(r().finished, now())}, after {between(r().created, r().finished)})
              </span>
            </dd>
          </div>
        </Show>
        <div>
          <dt class="label">At a time</dt>
          <dd>{r().parallel > 0 ? plural(r().parallel, "zathras", "zathrases") : "all of them"}</dd>
        </div>
        <div>
          <dt class="label">By the operator</dt>
          <dd>
            <ShortID id={r().created_by} />
          </dd>
        </div>
        <Show when={underWay(r().state)}>
          <div>
            <dt class="label">draal counts</dt>
            <dd>
              <span class="num">{r().prepared}</span> prepared, <span class="num">{r().activated}</span> activated,{" "}
              <span class="num">{r().awaited}</span> awaited
            </dd>
          </div>
        </Show>
      </dl>
      <Show when={r().reason}>
        <div class="roll-reason">
          <span class="label">Why it was halted</span>
          <p class="reason">{r().reason}</p>
        </div>
      </Show>
      <Show when={open(r())}>
        <Show
          when={props.service.zathrases.length > 0}
          fallback={<p class="muted roll-none">No zathras of the service is connected, so none has a part in this rollout now.</p>}
        >
          <ol class="stages">
            <For each={STAGES}>
              {(stage) => (
                <li class="stage" data-tone={STAGE_TONE[stage]} data-empty={stages()[stage].length === 0 ? "" : undefined}>
                  <div class="stage-head">
                    <span class="label">{stageTitle(stage, r().generation)}</span>
                    <b class="num">{stages()[stage].length}</b>
                  </div>
                  <ul>
                    <For each={stages()[stage]}>
                      {(z) => (
                        <li>
                          <A href={`/s/${encodeURIComponent(props.service.name)}/z/${z.id}`} title={`${z.name} (${z.id})`}>
                            {z.name}
                          </A>
                          {/* Two zathrases may have one name: the start of
                              the ID tells them apart. */}
                          <span class="mono muted">{z.id.slice(0, 6)}</span>
                        </li>
                      )}
                    </For>
                  </ul>
                </li>
              )}
            </For>
          </ol>
          <p class="muted roll-key">
            Each connected zathras stands where its own report puts it. draal counts one as activated when it has run the
            generation for a while without ending; until then it is awaited.
          </p>
        </Show>
      </Show>
    </div>
  );
}

// A zathras as a row of the table.
function Row(props: { service: ServiceRow; zathras: ZathrasRow }) {
  const z = () => props.zathras;
  const st = () => z().state;
  const target = () => (open(props.service.rollout) ? props.service.rollout.generation : 0);
  const share = () => {
    const s = st().sample;
    return s && s.memory_limit > 0 ? ` of ${bytes(s.memory_limit)} (${percent(s.memory / s.memory_limit)})` : "";
  };
  return (
    <tr>
      <td data-label="Zathras" class="z-name">
        <A href={`/s/${encodeURIComponent(props.service.name)}/z/${z().id}`}>{z().name}</A>
        <ShortID id={z().id} />
      </td>
      <td data-label="Phase" class="nw">
        <PhaseBadge phase={st().phase} /> <span class="muted z-note">{phaseNote(st(), now())}</span>
      </td>
      <td data-label="Generation">
        <span class="num">{generation(st().generation)}</span>{" "}
        <Switch>
          <Match when={st().generation === 0}>{null}</Match>
          <Match when={st().generation === props.service.stable}>
            <span class="tag" data-tone="ok">
              stable
            </span>
          </Match>
          <Match when={st().generation === target()}>
            <span class="tag" data-tone="busy">
              of the rollout
            </span>
          </Match>
          <Match when={true}>
            <span class="tag" data-tone="warn">
              not the stable one
            </span>
          </Match>
        </Switch>
      </td>
      <td data-label="Restarts" class="r">
        <span class="num">{st().restarts}</span>
      </td>
      <td data-label="Last exit">
        <Show when={st().last_exit} fallback={<span class="muted">none</span>}>
          {(e) => (
            <>
              <span data-tone={e().status === 0 && !e().signal && !e().error ? undefined : "bad"} class="exit">
                {exit(e())}
              </span>
              <span class="muted">
                , {ago(e().at, now())}, generation {generation(e().generation)}, ran {duration(e().ran)}
              </span>
            </>
          )}
        </Show>
      </td>
      <td data-label="CPU" class="r nw">
        <Show when={st().sample} fallback={<span class="muted" title="This zathras reports no figures.">no figures</span>}>
          {(s) => <span class="num">{cpu(s().cpu)}</span>}
        </Show>
      </td>
      <td data-label="Memory" class="r nw">
        <Show when={st().sample}>
          {(s) => (
            <>
              <span class="num">{bytes(s().memory)}</span>
              <span class="muted z-note">{share()}</span>
            </>
          )}
        </Show>
      </td>
    </tr>
  );
}

// The last rollouts of a service, as draal records them. They are asked
// for when the view opens, whenever the last rollout of the fleet is
// another one or in another state, and when draal is back.
function History(props: { service: string; last: RolloutInfo | undefined }) {
  const [list, setList] = createSignal<RolloutInfo[]>();
  const [failure, setFailure] = createSignal<string>();

  // The fleet changes every few seconds; the list is asked for only when
  // one of these does.
  const service = createMemo(() => props.service);
  const id = createMemo(() => props.last?.id);
  const state = createMemo(() => props.last?.state);

  createEffect(
    on([service, id, state, lives], ([service]) => {
      const abort = new AbortController();
      onCleanup(() => abort.abort());
      askRollouts(service, abort.signal).then(
        (answer) => {
          setList(answer.rollouts);
          setFailure(undefined);
        },
        (err: unknown) => {
          if (err instanceof ApiError && err.code === "aborted") {
            return;
          }
          setFailure(err instanceof Error ? err.message : String(err));
        },
      );
    }),
  );

  return (
    <section class="section">
      <h2>Last rollouts</h2>
      <Show when={failure()}>
        <Notice kind="bad" small>
          <p>The rollouts could not be asked for: {failure()}</p>
        </Notice>
      </Show>
      <Switch>
        <Match when={list() === undefined}>
          <Show when={!failure()}>
            <p class="muted">Asking draal…</p>
          </Show>
        </Match>
        <Match when={list()!.length === 0}>
          <p class="muted">draal has no rollout of this service on record.</p>
        </Match>
        <Match when={true}>
          <div class="scroll">
            <table class="table">
              <thead>
                <tr>
                  <th class="r">Rollout</th>
                  <th class="r">Generation</th>
                  <th>State</th>
                  <th>Started</th>
                  <th>Took</th>
                  <th class="r">At a time</th>
                  <th>Operator</th>
                  <th>Why it was halted</th>
                </tr>
              </thead>
              <tbody>
                <For each={list()}>
                  {(r) => (
                    <tr>
                      <td data-label="Rollout" class="r">
                        <span class="num">{r.id}</span>
                      </td>
                      <td data-label="Generation" class="r">
                        <span class="num">{generation(r.generation)}</span>
                      </td>
                      <td data-label="State">
                        <RolloutBadge state={r.state} />
                      </td>
                      <td data-label="Started" class="nw">
                        <span class="num">{dateTime(r.created)}</span> <span class="muted">({ago(r.created, now())})</span>
                      </td>
                      <td data-label="Took" class="nw">
                        <Show when={!isZeroTime(r.finished)} fallback={<span class="muted">under way</span>}>
                          <span class="num">{between(r.created, r.finished)}</span>
                        </Show>
                      </td>
                      <td data-label="At a time" class="r">
                        <span class="num">{r.parallel > 0 ? r.parallel : "all"}</span>
                      </td>
                      <td data-label="Operator">
                        <ShortID id={r.created_by} />
                      </td>
                      <td data-label="Why it was halted" class="why">
                        <Show when={r.reason}>
                          <span class="reason">{r.reason}</span>
                        </Show>
                      </td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </div>
        </Match>
      </Switch>
    </section>
  );
}

// View 2: a service: its rollout, its zathrases and its last rollouts.
export default function Service() {
  const params = useParams<{ service: string }>();
  const service = () => findService(params.service);

  return (
    <Switch>
      <Match when={!received()}>
        <div class="head">
          <h1>{params.service}</h1>
        </div>
        <p class="muted">Asking draal…</p>
      </Match>
      <Match when={service() === undefined}>
        <div class="head">
          <h1>{params.service}</h1>
        </div>
        <Notice title="draal knows no service of this name">
          <p>
            A service is there as soon as a zathras of it is connected or something was rolled out to it. If the name is
            right, start a zathras of it:
          </p>
          <code class="cmd">great-machine zathras --draal ID --service {params.service}</code>
          <p>
            <A href="/">Go to the services</A>
          </p>
        </Notice>
      </Match>
      <Match when={service()}>
        {(s) => (
          <>
            <div class="head">
              <h1>{s().name}</h1>
              <span class="muted">
                <Show when={s().stable > 0} fallback="no stable generation">
                  stable generation <span class="num">{s().stable}</span>
                </Show>
                , {plural(s().zathrases.length, "zathras", "zathrases")} connected
              </span>
            </div>

            {/* Review Focus 4: a service without a rollout says so, and
                how one is made. */}
            <Show
              when={s().rollout}
              fallback={
                <Notice title="Nothing was rolled out to this service yet">
                  <p>
                    Its zathrases are connected and run nothing. A rollout gives them a generation: on the machine that
                    has the binary in its Nix store, as an operator of this draal,
                  </p>
                  <code class="cmd">great-machine rollout {s().name} /nix/store/…/bin/…</code>
                </Notice>
              }
            >
              {(r) => <RolloutPanel service={s()} rollout={r()} />}
            </Show>

            <section class="section">
              <h2>Zathrases</h2>
              {/* Review Focus 4: a service without a zathras. */}
              <Show
                when={s().zathrases.length > 0}
                fallback={
                  <Notice small>
                    <p>
                      No zathras of this service is connected. draal keeps the service because something was rolled out to
                      it; a zathras that is started with <span class="mono">--service {s().name}</span> gets its stable
                      generation
                      <Show when={s().stable > 0}>
                        , <span class="num">{s().stable}</span>
                      </Show>
                      .
                    </p>
                  </Notice>
                }
              >
                <div class="scroll">
                  <table class="table zathrases">
                    <thead>
                      <tr>
                        <th>Zathras</th>
                        <th>Phase</th>
                        <th>Generation</th>
                        <th class="r">Restarts</th>
                        <th>Last exit</th>
                        <th class="r">CPU</th>
                        <th class="r">Memory</th>
                      </tr>
                    </thead>
                    <tbody>
                      <For each={s().zathrases}>{(z) => <Row service={s()} zathras={z} />}</For>
                    </tbody>
                  </table>
                </div>
              </Show>
            </section>

            <History service={s().name} last={s().rollout} />
          </>
        )}
      </Match>
    </Switch>
  );
}
```

`ui/src/views/service.css`:

```css
/* View 2: the rollout of a service, its zathrases, its last rollouts. */

/* ---- A table, here and in the view of a zathras ----------------- */

.scroll {
  overflow-x: auto;
  border: 1px solid var(--border);
  border-radius: var(--ffr-radius-md);
}

.table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13.5px;
}

.table th {
  font-family: var(--ffr-font-display);
  font-weight: 700;
  font-size: 11px;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--fg-muted);
  text-align: left;
  white-space: nowrap;
  background: var(--surface);
  padding: var(--ffr-space-2) var(--ffr-space-3);
  border-bottom: 1px solid var(--border);
}

.table td {
  padding: var(--ffr-space-2) var(--ffr-space-3);
  border-bottom: 1px solid var(--border);
  vertical-align: top;
}

.table tr:last-child td {
  border-bottom: 0;
}

.table .r {
  text-align: right;
}

.table .nw {
  white-space: nowrap;
}

.table .why {
  min-width: 22ch;
}

.table tbody tr:hover {
  background: var(--surface);
}

/* The name of a zathras is as long as a DNS name may be, and two may
   have one name: the name wraps where it has to, and the ID is always
   under it. */
.z-name {
  max-width: 34ch;
  min-width: 18ch;
}

.z-name a {
  display: block;
  font-weight: 600;
  overflow-wrap: anywhere;
}

.z-note {
  white-space: nowrap;
}

.tag {
  font-size: 12px;
  color: var(--tone-text);
  white-space: nowrap;
}

.exit {
  font-family: var(--ffr-font-mono);
  font-size: 0.93em;
  white-space: nowrap;
}

.exit[data-tone="bad"] {
  color: var(--bad-text);
}

/* ---- The rollout ------------------------------------------------ */

.roll {
  padding: var(--ffr-space-4) var(--ffr-space-5) var(--ffr-space-5);
}

.roll[data-state="halted"] {
  border-color: var(--bad);
}

.roll[data-state="preparing"],
.roll[data-state="activating"] {
  border-color: var(--busy);
}

.roll-head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--ffr-space-2) var(--ffr-space-3);
}

.facts {
  display: flex;
  flex-wrap: wrap;
  gap: var(--ffr-space-3) var(--ffr-space-6);
  margin-top: var(--ffr-space-4);
}

.facts > div {
  display: grid;
  gap: 2px;
  min-width: 0;
}

.facts dd {
  overflow-wrap: anywhere;
}

.roll-reason {
  margin-top: var(--ffr-space-4);
  padding: var(--ffr-space-3) var(--ffr-space-4);
  border-radius: var(--ffr-radius-sm);
  background: var(--bad-wash);
  display: grid;
  gap: var(--ffr-space-1);
}

.roll-reason .label {
  color: var(--bad-text);
}

.roll-none,
.roll-key {
  margin-top: var(--ffr-space-4);
  max-width: 86ch;
  font-size: 13px;
}

/* The stages of a rollout, left to right as a zathras passes them. */
.stages {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: var(--ffr-space-2);
  margin-top: var(--ffr-space-4);
}

.stage {
  border-top: 3px solid var(--tone);
  background: var(--surface);
  border-radius: 0 0 var(--ffr-radius-sm) var(--ffr-radius-sm);
  padding: var(--ffr-space-2) var(--ffr-space-3) var(--ffr-space-3);
  min-width: 0;
}

.stage[data-empty] {
  border-top-color: var(--border);
}

.stage[data-empty] .stage-head b {
  color: var(--fg-subtle);
}

.stage-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--ffr-space-2);
}

.stage-head b {
  font-size: 18px;
  font-weight: 700;
}

.stage ul {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 2px;
  margin-top: var(--ffr-space-1);
  font-size: 13px;
}

.stage li {
  display: flex;
  align-items: baseline;
  gap: var(--ffr-space-2);
  min-width: 0;
}

.stage li a {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.stage li span {
  font-size: 11.5px;
  flex: none;
}

@media (max-width: 860px) {
  .stages {
    grid-template-columns: minmax(0, 1fr);
  }

  .roll {
    padding: var(--ffr-space-4);
  }

  /* A table becomes a list: each row a block, each cell a line with
     the name of its column before it. */
  .table thead {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }

  .table,
  .table tbody,
  .table tr,
  .table td {
    display: block;
  }

  .table tr {
    padding: var(--ffr-space-3);
    border-bottom: 1px solid var(--border);
  }

  .table tr:last-child {
    border-bottom: 0;
  }

  .table td {
    border: 0;
    padding: 2px 0;
    text-align: left;
    max-width: none;
    min-width: 0;
  }

  .table .r {
    text-align: left;
  }

  .table .nw {
    white-space: normal;
  }

  .table td:empty {
    display: none;
  }

  .table td:not(.z-name)::before {
    content: attr(data-label);
    display: inline-block;
    min-width: 11ch;
    margin-right: var(--ffr-space-2);
    font-family: var(--ffr-font-display);
    font-weight: 700;
    font-size: 10.5px;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--fg-muted);
  }

  .z-note {
    white-space: normal;
  }
}
```

- [ ] **Step 8: Check and build**

Run: `nix develop .#bare --command npm --prefix ui run check`, `nix develop .#bare --command npm --prefix ui test` and `nix develop .#bare --command npm --prefix ui run build`
Expected: no error; `Test Files  6 passed (6)`, `Tests  56 passed (56)`; `✓ built`.

- [ ] **Step 9: Look at it (Review Focus 4)**

Run `nix develop .#bare --command go run ./web/demo --addr 127.0.0.1:8080 --dist web/dist` and open `http://127.0.0.1:8080/`, console open. On
every view below the console stays empty, and nothing on the page
reads `NaN`, `undefined` or `Invalid Date`.

- `/`: four rows. `empty`: `stable generation 2`, `no zathras connected`, its completed rollout. `fresh`: `no stable generation`, two low grey blocks and `2 idle`, `Nothing was rolled out yet.` `web`: five blocks, and during the first half of every minute `PREPARING` or `ACTIVATING` in blue with `started … ago` and draal's counts. `worker`: a red border, `HALTED`, and the reason in full, `worker-1: exited with status 2 after 1.3s`; its blocks show running, backoff (red), stopping (hollow) and idle (low) as the demo has them.
- `/s/web` while a rollout is under way: the panel of the rollout with five stages; the zathrases move from `Prepared` to `On generation N`, two at a time. Two zathrases are called `web-7d9f8b6c4-8hjwz`: the stages and the table show the start of their IDs. From second 43 to 49 of the demo's minute `web-7d9f8b6c4-q5rvd` is gone: its row leaves the table and comes back in its place, and the rows around it are the same elements (select text in a neighbouring row first: the selection stays).
- `/s/worker`: the halted rollout with `Why it was halted`; `worker-1` under `Ended on it` while it is in backoff. One zathras has a name of 253 characters: it wraps inside its cell, is cut with an ellipsis in the stage, and the page does not scroll sideways. `worker-2` and `worker-3` say `no figures`.
- `/s/fresh` (a service without a rollout): `Nothing was rolled out to this service yet`, with `great-machine rollout fresh /nix/store/…/bin/…`; `Last rollouts` says that draal has none on record.
- `/s/empty` (a service without a zathras): `No zathras of this service is connected. … gets its stable generation, 2.`
- `/s/nope`: `draal knows no service of this name`.
- Stop the demo and start it with `--empty` added (no service yet): `/` says `draal knows no service yet` and gives `great-machine zathras --draal ID --service NAME`.
- At a width of 390 pixels, in the light and in the dark: each service is a block, each table a list of blocks with the name of the column before each value; nothing is cut and nothing scrolls sideways.

- [ ] **Step 10: Commit**

```bash
git add ui/src/views/
git commit -m "UI: a service: its rollout, its zathrases, its last rollouts"
```
