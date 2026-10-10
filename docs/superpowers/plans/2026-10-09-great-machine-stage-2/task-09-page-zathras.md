### Task 9: A zathras: its state and its charts

**Files:**
- Create: `ui/src/state/series.ts`
- Create: `ui/src/components/Chart.tsx`, `ui/src/components/Figures.tsx`, `ui/src/components/chart.css`
- Modify: `ui/src/views/Zathras.tsx` (the place of Task 7, replaced)
- Create: `ui/src/views/zathras.css`
- Test: `ui/src/state/series.test.ts`

**Interfaces:**
- Consumes:
  - Task 7: `fleet`, `received`, `now`, `freshAt`, `lives`, `findService`, `findZathras`; the types, `isID`, `isZeroTime`, `parseTime`; `history(id, signal?)` and `ApiError`; the formatters; `Notice`; the tokens `--series-1`, `--series-2`, `--grid`.
  - Task 8: `PhaseBadge`, `phaseNote`, and the classes `scroll`, `table`, `exit` of `ui/src/views/service.css`.
  - Task 1: `GET /api/zathras/{id}/history` → `{"samples":[…]}`, oldest first, always a list; a zathras that is not connected is refused with `not-found`.
- Produces:
  - `ui/src/state/series.ts`: `WINDOW_MS` (15 minutes), `GAP_MS` (5 seconds), `Point`, `merge(points, samples): Point[]`, `figure(v): number | undefined`, `runs(points, pick): Dot[][]`, `largest`, `nearest(points, t)`, `Axis`, `byteAxis(max, least?)`, `coreAxis(max, least?)`, `minuteTicks(from, to)`.
  - `ui/src/components/Chart.tsx` (`Series`, `ChartProps`) and `ui/src/components/Figures.tsx` (`id`, `present`, `connected`, `sample`).
  - `ui/src/views/Zathras.tsx`: the view at `/s/:service/z/:id`, with the place where Task 10 puts the log and the shell: after `<Figures … />`. It hands its parts `params.id`, `live() !== undefined` (whether the fleet has the zathras) and `live()?.connected`.

**The charts are drawn by the page itself, as SVG.** A series is at
most 450 points, there are seven of them, and they change every two
seconds: a path of 450 segments is nothing a browser notices. A library
(uPlot is the small one) would add a dependency, a stylesheet and a
canvas whose colours would have to be copied out of the tokens in both
schemes, to save about 150 lines. Drawn as SVG the charts take their
colours from the tokens by CSS, are sharp at every density, and work
under the content security policy without a thought.

What the charts keep to, from the skill `dataviz`: one value axis for
each chart, and two measures of different scale are two charts; the
four charts have one time axis (the last 15 minutes before the newest
sample); a single series has no legend box, two have a legend that is
also where the values are read; text is in the colours of text, a mark
of the series' colour beside it; the grid and the axes are quiet;
the limit of the memory is a bound and no measurement, so it is a thin
dashed line in the colour of text; the pointer shows the sample under
it in all four charts at once, the arrow keys do the same without a
pointer, and the samples are there as a table.

**Review Focus 4**: a zathras without samples gets a sentence that
says why, and no empty chart (the last branch of `Figures`). **Review
Focus 2**, for the state and the charts: when the zathras goes, the
view says so, keeps what it showed, and asks nothing; when the fleet
has it again the history is asked for once more, and the time it was
away is a gap in every line. Step 8 looks at both; the tests of Step 1
pin that a gap is a gap and that no figure is below zero.

- [ ] **Step 1: Write the failing test of the samples as lines**

`ui/src/state/series.test.ts`:

```ts
import { describe, expect, test } from "vitest";
import type { Sample } from "../api/types";
import { byteAxis, coreAxis, figure, GAP_MS, largest, merge, minuteTicks, nearest, type Point, runs, WINDOW_MS } from "./series";

const T0 = Date.UTC(2026, 9, 9, 20, 0, 0);

// A sample s seconds after T0.
function sample(s: number, over: Partial<Sample> = {}): Sample {
  return {
    at: new Date(T0 + s * 1000).toISOString(),
    cpu: 0.5,
    memory: 100,
    memory_limit: 0,
    disk_read: 0,
    disk_write: 10,
    net_rx: 20,
    net_tx: 30,
    ...over,
  };
}

function points(...seconds: number[]): Point[] {
  return merge([], seconds.map((s) => sample(s)));
}

describe("the samples that are kept", () => {
  test("are in the order of time, whatever order they came in", () => {
    const p = merge([], [sample(4), sample(0), sample(2)]);
    expect(p.map((x) => x.t - T0)).toEqual([0, 2000, 4000]);
  });

  test("are one for each time: the sample of the fleet that the history had too is not twice there", () => {
    const first = merge([], [sample(0), sample(2)]);
    const again = merge(first, [sample(2, { cpu: 0.9 }), sample(4)]);
    expect(again.map((x) => x.t - T0)).toEqual([0, 2000, 4000]);
    expect(again[1]!.sample.cpu).toBe(0.9);
  });

  test("are of the last 15 minutes before the newest", () => {
    const p = merge(points(0, 2), [sample(900), sample(902)]);
    expect(p.map((x) => (x.t - T0) / 1000)).toEqual([2, 900, 902]);
    expect(WINDOW_MS).toBe(900_000);
  });

  test("a sample without a time is left out, and nothing is nothing", () => {
    expect(merge([], [])).toEqual([]);
    expect(merge([], [sample(0, { at: "0001-01-01T00:00:00Z" }), sample(0, { at: "nonsense" })])).toEqual([]);
    expect(merge([], [undefined as unknown as Sample])).toEqual([]);
  });
});

describe("a figure", () => {
  test("is a finite number that is not below zero", () => {
    expect(figure(0)).toBe(0);
    expect(figure(12.5)).toBe(12.5);
    for (const v of [-1, -0.0001, NaN, Infinity, undefined, null, "3"]) {
      expect(figure(v)).toBeUndefined();
    }
  });
});

describe("the runs of a line", () => {
  test("one run while the samples follow each other", () => {
    const r = runs(points(0, 2, 4, 6), (s) => s.cpu);
    expect(r.length).toBe(1);
    expect(r[0]!.map((d) => (d.t - T0) / 1000)).toEqual([0, 2, 4, 6]);
  });

  test("one sample late is no gap; more than two missing is one", () => {
    expect(runs(points(0, 2, 6, 8), (s) => s.cpu).length).toBe(1);
    // The zathras was away from second 4 to 12: a gap is a gap, not a
    // line through it.
    const r = runs(points(0, 2, 4, 12, 14), (s) => s.cpu);
    expect(r.map((run) => run.map((d) => (d.t - T0) / 1000))).toEqual([
      [0, 2, 4],
      [12, 14],
    ]);
    expect(GAP_MS).toBe(5000);
  });

  test("a counter that started again gives no negative rate: the line has a gap there", () => {
    const p = merge([], [sample(0), sample(2), sample(4, { net_rx: -48_000 }), sample(6), sample(8)]);
    const r = runs(p, (s) => s.net_rx);
    expect(r.map((run) => run.map((d) => (d.t - T0) / 1000))).toEqual([
      [0, 2],
      [6, 8],
    ]);
    for (const run of r) {
      for (const d of run) {
        expect(d.v).toBeGreaterThanOrEqual(0);
      }
    }
    // The other figures of that sample are drawn.
    expect(runs(p, (s) => s.cpu).length).toBe(1);
  });

  test("a figure that is no number cuts the line, and a lone sample is a run of one", () => {
    const p = merge([], [sample(0), sample(2, { cpu: NaN }), sample(4)]);
    expect(runs(p, (s) => s.cpu).map((run) => run.length)).toEqual([1, 1]);
    expect(runs([], (s) => s.cpu)).toEqual([]);
  });

  test("the largest figure ignores what is no figure", () => {
    const p = merge([], [sample(0, { cpu: 0.2 }), sample(2, { cpu: -5 }), sample(4, { cpu: NaN }), sample(6, { cpu: 0.7 })]);
    expect(largest(p, (s) => s.cpu)).toBe(0.7);
    expect(largest([], (s) => s.cpu)).toBe(0);
  });
});

describe("the sample under the pointer", () => {
  test("is the nearest, if it is within reach", () => {
    const p = points(0, 2, 4, 20);
    expect(nearest(p, T0 + 2900)!.t).toBe(T0 + 2000);
    expect(nearest(p, T0 + 3100)!.t).toBe(T0 + 4000);
    // In the middle of a gap there is none.
    expect(nearest(p, T0 + 12_000)).toBeUndefined();
    expect(nearest([], T0)).toBeUndefined();
  });
});

describe("the value axis", () => {
  test("of bytes ends at a power of two, in whole binary units", () => {
    expect(byteAxis(286_297_311)).toEqual({ top: 536_870_912, ticks: [0, 268_435_456, 536_870_912] });
    expect(byteAxis(536_870_912).top).toBe(536_870_912);
    expect(byteAxis(22_494).top).toBe(32_768);
  });

  test("of bytes is never flat: nothing, and nonsense, give the least", () => {
    expect(byteAxis(0)).toEqual({ top: 1024, ticks: [0, 512, 1024] });
    expect(byteAxis(NaN).top).toBe(1024);
    expect(byteAxis(-3).top).toBe(1024);
  });

  test("of cores ends at twice a step of 1, 2 or 5", () => {
    expect(coreAxis(0.75)).toEqual({ top: 1, ticks: [0, 0.5, 1] });
    expect(coreAxis(0.3)).toEqual({ top: 0.4, ticks: [0, 0.2, 0.4] });
    expect(coreAxis(2.08)).toEqual({ top: 4, ticks: [0, 2, 4] });
    expect(coreAxis(1)).toEqual({ top: 1, ticks: [0, 0.5, 1] });
    expect(coreAxis(0)).toEqual({ top: 0.1, ticks: [0, 0.05, 0.1] });
    expect(coreAxis(NaN).top).toBe(0.1);
  });
});

describe("the time axis", () => {
  test("has a label every five minutes of the clock", () => {
    const from = Date.UTC(2026, 9, 9, 20, 1, 30);
    const ticks = minuteTicks(from, from + WINDOW_MS);
    expect(ticks.map((t) => new Date(t).getUTCMinutes())).toEqual([5, 10, 15]);
    expect(ticks.every((t) => new Date(t).getUTCSeconds() === 0)).toBe(true);
  });

  test("has none when there is no span", () => {
    expect(minuteTicks(5, 5)).toEqual([]);
    expect(minuteTicks(NaN, 5)).toEqual([]);
  });
});
```

- [ ] **Step 2: Run the test to see it fail**

Run: `nix develop .#bare --command npm --prefix ui test -- src/state/series.test.ts`
Expected: FAIL — `Error: Cannot find module './series'` (the test file fails before any test runs: `Test Files  1 failed (1)`)

- [ ] **Step 3: Write the samples as lines**

The samples of the API carry rates that zathras computed, so a counter
that started again shows as a rate that is no figure (below zero, or no
number). `figure` is the one place that decides what may be drawn.

`ui/src/state/series.ts`:

```ts
// The samples of a zathras as the charts draw them: kept in order and
// within the last 15 minutes, cut into runs wherever a sample is
// missing, and never below zero.

import { parseTime, type Sample } from "../api/types";

/** How much of the past the charts show, in milliseconds. */
export const WINDOW_MS = 15 * 60 * 1000;

/** Two samples that are further apart than this have a gap between
 *  them: zathras samples every 2 seconds, so this is more than two
 *  samples missing. No line is drawn across a gap. */
export const GAP_MS = 5000;

/** A sample with its time as a number. */
export interface Point {
  /** Milliseconds since 1970. */
  t: number;
  sample: Sample;
}

/** The points with the samples added: in the order of time, one for
 *  each time (a later one of the same time replaces the earlier), and
 *  none older than the window before the newest. A sample without a
 *  time is left out. */
export function merge(points: readonly Point[], samples: readonly Sample[], window = WINDOW_MS): Point[] {
  const at = new Map<number, Point>();
  for (const p of points) {
    at.set(p.t, p);
  }
  for (const sample of samples) {
    const t = parseTime(sample?.at);
    if (t !== undefined) {
      at.set(t, { t, sample });
    }
  }
  const all = [...at.values()].sort((a, b) => a.t - b.t);
  const newest = all[all.length - 1];
  if (!newest) {
    return [];
  }
  const from = newest.t - window;
  return all.filter((p) => p.t >= from);
}

/** A figure as a chart may draw it: a finite number that is not below
 *  zero. A rate below zero is what a counter that started again gives
 *  (the service was restarted): it is no figure, and the line has a gap
 *  there. */
export function figure(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : undefined;
}

/** A place of a line: a time and a value. */
export interface Dot {
  t: number;
  v: number;
}

/** The runs of a series: the points whose figure there is, cut wherever
 *  two neighbours are further apart than gap and wherever a figure is
 *  missing. A line is drawn through each run and through nothing else. */
export function runs(points: readonly Point[], pick: (s: Sample) => unknown, gap = GAP_MS): Dot[][] {
  const out: Dot[][] = [];
  let run: Dot[] = [];
  let last: number | undefined;
  const cut = () => {
    if (run.length > 0) {
      out.push(run);
      run = [];
    }
  };
  for (const p of points) {
    const v = figure(pick(p.sample));
    if (last !== undefined && p.t - last > gap) {
      cut();
    }
    last = p.t;
    if (v === undefined) {
      cut();
      continue;
    }
    run.push({ t: p.t, v });
  }
  cut();
  return out;
}

/** The largest figure of a series; 0 when it has none. */
export function largest(points: readonly Point[], pick: (s: Sample) => unknown): number {
  let max = 0;
  for (const p of points) {
    const v = figure(pick(p.sample));
    if (v !== undefined && v > max) {
      max = v;
    }
  }
  return max;
}

/** The point nearest to a time, if one is within reach of it. */
export function nearest(points: readonly Point[], t: number, reach = GAP_MS): Point | undefined {
  let best: Point | undefined;
  let distance = Infinity;
  for (const p of points) {
    const d = Math.abs(p.t - t);
    if (d < distance) {
      best = p;
      distance = d;
    }
  }
  return best !== undefined && distance <= reach ? best : undefined;
}

/** The value axis of a chart: its top, and the values that get a line
 *  and a label (0, the middle, the top). */
export interface Axis {
  top: number;
  ticks: number[];
}

/** An axis for bytes or bytes a second: its top is the power of two at
 *  or above the largest value, so that the labels are whole binary
 *  units ("256 MiB", "512 MiB"), and at least least. */
export function byteAxis(max: number, least = 1024): Axis {
  const m = Math.max(figure(max) ?? 0, least);
  const top = 2 ** Math.ceil(Math.log2(m));
  return { top, ticks: [0, top / 2, top] };
}

/** An axis for a number of cores, labelled as a percentage: its top is
 *  twice a step of 1, 2 or 5 times a power of ten, and at least least
 *  (0.1 cores, which is 10%). */
export function coreAxis(max: number, least = 0.1): Axis {
  const m = Math.max(figure(max) ?? 0, least);
  const half = m / 2;
  const power = 10 ** Math.floor(Math.log10(half));
  const step = [1, 2, 5, 10].map((k) => k * power).find((s) => s >= half - 1e-12) ?? 10 * power;
  // 0.1 + 0.2 is not 0.3: the ticks are rounded to what they stand for.
  const round = (v: number) => Math.round(v * 1e9) / 1e9;
  return { top: round(step * 2), ticks: [0, round(step), round(step * 2)] };
}

/** The times that get a label on the time axis: every five minutes of
 *  the clock within from and to. */
export function minuteTicks(from: number, to: number, every = 5 * 60 * 1000): number[] {
  if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from) {
    return [];
  }
  // By the clock of the reader: an offset of a quarter or half an hour
  // still puts the labels on its own five minutes.
  const offset = new Date(to).getTimezoneOffset() * 60_000;
  const out: number[] = [];
  for (let t = Math.ceil((from - offset) / every) * every + offset; t <= to; t += every) {
    out.push(t);
  }
  return out;
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command npm --prefix ui test`
Expected: PASS — `Test Files  7 passed (7)`, and `nix develop .#bare --command npm --prefix ui run check` prints no error.

- [ ] **Step 5: Commit**

```bash
git add ui/src/state/series.ts ui/src/state/series.test.ts
git commit -m "UI: the samples of a zathras as lines: gaps, and no figure below zero"
```

- [ ] **Step 6: The chart and the figures**

`ui/src/components/Chart.tsx`:

```tsx
import { type Accessor, createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import type { Sample } from "../api/types";
import { clock } from "../format";
import { type Axis, largest, minuteTicks, nearest, type Point, runs, WINDOW_MS } from "../state/series";

/** A line of a chart. */
export interface Series {
  /** What it is called in the legend. */
  label: string;
  /** Its figure in a sample. */
  pick: (s: Sample) => unknown;
  /** 1 or 2: which of the two colours of a chart it has. A line that is
   *  a bound and no measurement (the limit of the memory) is "bound":
   *  drawn thin and dashed, in the colour of the text. */
  slot: 1 | 2 | "bound";
}

/** What Chart is given. */
export interface ChartProps {
  /** What the chart is of: "CPU". */
  title: string;
  /** What its values are: "of one core". */
  unit: string;
  series: Series[];
  /** The samples, in the order of time. */
  points: readonly Point[];
  /** The value axis for the largest value of the series. */
  axis: (max: number) => Axis;
  /** A value as text, for the axis and for the legend. */
  format: (v: number | undefined) => string;
  /** The time under the pointer, which all charts of a view share. */
  hover: Accessor<number | undefined>;
  setHover: (t: number | undefined) => void;
}

// The space around the plot: for the labels of the value axis on the
// left and of the time axis below.
const LEFT = 74;
const RIGHT = 20;
const TOP = 8;
const BOTTOM = 22;
const HEIGHT = 168;

// A chart of one or two lines over the last 15 minutes, drawn as SVG.
//
// The time axis ends at the newest sample and is the same in every
// chart that is given the same points. The legend above the plot is
// also where the values are read: those of the newest sample, or those
// of the sample under the pointer, which a line across the plot marks
// in every chart of the view.
export default function Chart(props: ChartProps) {
  let box: HTMLDivElement | undefined;
  const [width, setWidth] = createSignal(480);

  onMount(() => {
    if (!box) {
      return;
    }
    const observer = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width;
      if (w !== undefined && w > 0) {
        setWidth(Math.floor(w));
      }
    });
    observer.observe(box);
    onCleanup(() => observer.disconnect());
  });

  const newest = () => props.points[props.points.length - 1];
  const to = () => newest()?.t ?? 0;
  const from = () => to() - WINDOW_MS;
  const plotW = () => Math.max(1, width() - LEFT - RIGHT);
  const plotH = HEIGHT - TOP - BOTTOM;

  const axis = createMemo(() => props.axis(Math.max(0, ...props.series.map((s) => largest(props.points, s.pick)))));
  const x = (t: number) => LEFT + ((t - from()) / WINDOW_MS) * plotW();
  const y = (v: number) => TOP + plotH - (Math.min(v, axis().top) / axis().top) * plotH;

  // The lines: for each series its runs, each a path; a run of a single
  // sample is a dot, as a path of one point draws nothing.
  const lines = createMemo(() =>
    props.series.map((s) => {
      const all = runs(props.points, s.pick);
      return {
        series: s,
        paths: all
          .filter((run) => run.length > 1)
          .map((run) => run.map((d, i) => `${i === 0 ? "M" : "L"}${x(d.t).toFixed(1)} ${y(d.v).toFixed(1)}`).join("")),
        dots: all.filter((run) => run.length === 1).map((run) => run[0]!),
      };
    }),
  );

  // The sample the legend reads: the one under the pointer, or the
  // newest.
  const read = createMemo(() => {
    const t = props.hover();
    return (t === undefined ? undefined : nearest(props.points, t)) ?? newest();
  });
  const hovered = createMemo(() => {
    const t = props.hover();
    return t === undefined ? undefined : nearest(props.points, t);
  });

  const point = (clientX: number, target: Element) => {
    const rect = target.getBoundingClientRect();
    const t = from() + ((clientX - rect.left - LEFT) / plotW()) * WINDOW_MS;
    props.setHover(nearest(props.points, t, WINDOW_MS)?.t);
  };

  // The arrows move the pointer of the keyboard a sample at a time.
  const key = (event: KeyboardEvent) => {
    const step = event.key === "ArrowLeft" ? -1 : event.key === "ArrowRight" ? 1 : 0;
    if (event.key === "Escape") {
      props.setHover(undefined);
      return;
    }
    if (step === 0 || props.points.length === 0) {
      return;
    }
    event.preventDefault();
    const at = hovered();
    const i = at ? props.points.indexOf(at) : props.points.length;
    const next = props.points[Math.min(props.points.length - 1, Math.max(0, i + step))];
    props.setHover(next?.t);
  };

  const summary = () =>
    `${props.title}, ${props.unit}, the last 15 minutes. ` +
    props.series.map((s) => `${s.label}: ${props.format(numberOf(s.pick, newest()))}`).join(", ") +
    " now. The arrow keys read one sample after another.";

  return (
    <figure class="chart">
      <figcaption>
        <span class="chart-title">
          <b>{props.title}</b> <span class="muted">{props.unit}</span>
        </span>
        <span class="chart-read">
          <For each={props.series}>
            {(s) => (
              <span class="chart-key">
                <i data-slot={s.slot} aria-hidden="true" />
                <span class="muted">{s.label}</span> <b class="num">{props.format(numberOf(s.pick, read()))}</b>
              </span>
            )}
          </For>
          <span class="chart-at num muted">{hovered() ? clock(hovered()!.t) : "now"}</span>
        </span>
      </figcaption>
      <div class="chart-box" ref={box}>
        <svg
          width={width()}
          height={HEIGHT}
          role="img"
          aria-label={summary()}
          tabindex="0"
          onPointerMove={(e) => point(e.clientX, e.currentTarget)}
          onPointerDown={(e) => point(e.clientX, e.currentTarget)}
          onPointerLeave={() => props.setHover(undefined)}
          onKeyDown={key}
          onBlur={() => props.setHover(undefined)}
        >
          <For each={axis().ticks}>
            {(v) => (
              <>
                <line class="chart-grid" x1={LEFT} x2={LEFT + plotW()} y1={y(v)} y2={y(v)} />
                <text class="chart-tick" x={LEFT - 8} y={y(v)} dy="0.32em" text-anchor="end">
                  {props.format(v)}
                </text>
              </>
            )}
          </For>
          {/* A time whose label would run into the labels of the value
              axis gets none. */}
          <For each={minuteTicks(from(), to()).filter((t) => x(t) > LEFT + 20)}>
            {(t) => (
              <>
                <line class="chart-grid" x1={x(t)} x2={x(t)} y1={TOP + plotH} y2={TOP + plotH + 4} />
                <text class="chart-tick" x={x(t)} y={HEIGHT - 5} text-anchor="middle">
                  {clock(t).slice(0, 5)}
                </text>
              </>
            )}
          </For>
          <For each={lines()}>
            {(line) => (
              <g class="chart-line" data-slot={line.series.slot}>
                <For each={line.paths}>{(d) => <path d={d} />}</For>
                <For each={line.dots}>{(d) => <circle cx={x(d.t)} cy={y(d.v)} r="2" />}</For>
              </g>
            )}
          </For>
          <Show when={hovered()}>
            {(p) => (
              <g class="chart-cross">
                <line x1={x(p().t)} x2={x(p().t)} y1={TOP} y2={TOP + plotH} />
                <For each={props.series.filter((s) => s.slot !== "bound")}>
                  {(s) => (
                    <Show when={numberOf(s.pick, p()) !== undefined}>
                      <circle data-slot={s.slot} cx={x(p().t)} cy={y(numberOf(s.pick, p())!)} r="4" />
                    </Show>
                  )}
                </For>
              </g>
            )}
          </Show>
        </svg>
      </div>
    </figure>
  );
}

// The figure of a series in a point, if it is one that may be drawn.
function numberOf(pick: (s: Sample) => unknown, p: Point | undefined): number | undefined {
  if (!p) {
    return undefined;
  }
  const v = pick(p.sample);
  return typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : undefined;
}
```

`ui/src/components/Figures.tsx`:

```tsx
import { createEffect, createMemo, createSignal, For, Match, on, onCleanup, Show, Switch } from "solid-js";
import { ApiError, history } from "../api/client";
import type { Sample } from "../api/types";
import { bytes, clock, cpu, rate } from "../format";
import { lives } from "../state/fleet";
import { byteAxis, coreAxis, merge, type Point } from "../state/series";
import Chart from "./Chart";
import Notice from "./Notice";
import "./chart.css";

// The figures of a zathras over the last 15 minutes: what draal keeps
// of them, asked for once, and then the sample of every fleet that
// arrives.
//
// While the zathras is not connected nothing is asked: the charts stay
// as they were. When it is back (present again, or connected at another
// time) the history is asked for again, and the time it was away is a
// gap in every line.
export default function Figures(props: {
  id: string;
  /** Whether the fleet has the zathras now. */
  present: boolean;
  /** When it connected: another time is another connection. */
  connected: string | undefined;
  /** The sample of its last report, if it reports any. */
  sample: Sample | undefined;
}) {
  const [points, setPoints] = createSignal<Point[]>([]);
  const [asked, setAsked] = createSignal(false);
  const [failure, setFailure] = createSignal<string>();
  const [hover, setHover] = createSignal<number>();
  const [table, setTable] = createSignal(false);

  // The fleet changes every few seconds; the history is asked for only
  // when one of these does.
  const id = createMemo(() => props.id);
  const present = createMemo(() => props.present);
  const connected = createMemo(() => props.connected);
  const sampled = createMemo(() => props.sample?.at);

  // Another zathras: nothing of the one before stays.
  createEffect(
    on(id, () => {
      setPoints([]);
      setAsked(false);
      setFailure(undefined);
      setHover(undefined);
    }),
  );

  createEffect(
    on([id, present, connected, lives], ([id, present]) => {
      if (!present) {
        return;
      }
      const abort = new AbortController();
      onCleanup(() => abort.abort());
      history(id, abort.signal).then(
        (answer) => {
          setPoints((p) => merge(p, answer.samples));
          setAsked(true);
          setFailure(undefined);
        },
        (err: unknown) => {
          if (err instanceof ApiError && err.code === "aborted") {
            return;
          }
          setAsked(true);
          setFailure(err instanceof Error ? err.message : String(err));
        },
      );
    }),
  );

  createEffect(
    on(sampled, () => {
      const s = props.sample;
      if (s) {
        setPoints((p) => merge(p, [{ ...s }]));
      }
    }),
  );

  const limited = () => points().some((p) => p.sample.memory_limit > 0);

  return (
    <section class="section">
      <h2>Figures of the container</h2>
      <Show when={failure()}>
        <Notice kind="bad" small>
          <p>The figures draal keeps could not be asked for: {failure()}</p>
        </Notice>
      </Show>
      <Switch>
        <Match when={points().length > 0}>
          <div class="charts">
            <Chart
              title="CPU"
              unit="of one core"
              series={[{ label: "used", pick: (s) => s.cpu, slot: 1 }]}
              points={points()}
              axis={coreAxis}
              format={cpu}
              hover={hover}
              setHover={setHover}
            />
            <Chart
              title="Memory"
              unit={limited() ? "and its limit" : "no limit is set"}
              series={[
                { label: "used", pick: (s) => s.memory, slot: 1 },
                // A limit of 0 is none: it is no figure of the line.
                ...(limited() ? [{ label: "limit", pick: (s: Sample) => s.memory_limit || undefined, slot: "bound" as const }] : []),
              ]}
              points={points()}
              axis={(max) => byteAxis(max, 1024 * 1024)}
              format={bytes}
              hover={hover}
              setHover={setHover}
            />
            <Chart
              title="Disk"
              unit="all devices"
              series={[
                { label: "read", pick: (s) => s.disk_read, slot: 1 },
                { label: "written", pick: (s) => s.disk_write, slot: 2 },
              ]}
              points={points()}
              axis={byteAxis}
              format={rate}
              hover={hover}
              setHover={setHover}
            />
            <Chart
              title="Network"
              unit="without the loopback"
              series={[
                { label: "received", pick: (s) => s.net_rx, slot: 1 },
                { label: "sent", pick: (s) => s.net_tx, slot: 2 },
              ]}
              points={points()}
              axis={byteAxis}
              format={rate}
              hover={hover}
              setHover={setHover}
            />
          </div>
          <p class="muted chart-note">
            The container is the service, zathras and the shells. zathras samples every 2 seconds and draal keeps 15
            minutes; a line stops where samples are missing.{" "}
            <button type="button" class="text-button" aria-expanded={table()} onClick={() => setTable((v) => !v)}>
              {table() ? "Hide the table" : "Show the samples as a table"}
            </button>
          </p>
          <Show when={table()}>
            <div class="scroll samples">
              <table class="table">
                <thead>
                  <tr>
                    <th>Time</th>
                    <th class="r">CPU</th>
                    <th class="r">Memory</th>
                    <th class="r">Limit</th>
                    <th class="r">Disk read</th>
                    <th class="r">Disk written</th>
                    <th class="r">Received</th>
                    <th class="r">Sent</th>
                  </tr>
                </thead>
                <tbody>
                  <For each={[...points()].reverse()}>
                    {(p) => (
                      <tr>
                        <td data-label="Time" class="num">
                          {clock(p.t)}
                        </td>
                        <td data-label="CPU" class="r num">
                          {cpu(p.sample.cpu)}
                        </td>
                        <td data-label="Memory" class="r num">
                          {bytes(p.sample.memory)}
                        </td>
                        <td data-label="Limit" class="r num">
                          {p.sample.memory_limit > 0 ? bytes(p.sample.memory_limit) : "none"}
                        </td>
                        <td data-label="Disk read" class="r num">
                          {rate(p.sample.disk_read)}
                        </td>
                        <td data-label="Disk written" class="r num">
                          {rate(p.sample.disk_write)}
                        </td>
                        <td data-label="Received" class="r num">
                          {rate(p.sample.net_rx)}
                        </td>
                        <td data-label="Sent" class="r num">
                          {rate(p.sample.net_tx)}
                        </td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
          </Show>
        </Match>
        <Match when={!asked() && props.present}>
          <p class="muted">Asking draal…</p>
        </Match>
        {/* Review Focus 4: a zathras without samples gets a sentence that
            says why, and no empty charts. */}
        <Match when={!failure()}>
          <Notice title="This zathras reports no figures" small>
            <p>
              zathras reads the figures of its container from cgroup v2 on Linux (<span class="mono">/sys/fs/cgroup</span>{" "}
              and <span class="mono">/proc/net/dev</span>). Where those files are missing, as with cgroup v1 or on another
              system, it sends none, and there is nothing to draw. Its state, its log and its shell do not depend on them.
            </p>
          </Notice>
        </Match>
      </Switch>
    </section>
  );
}
```

`ui/src/components/chart.css`:

```css
/* The charts of a zathras: four, side by side in pairs, one time axis. */

.charts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--ffr-space-3);
}

.chart {
  margin: 0;
  border: 1px solid var(--border);
  border-radius: var(--ffr-radius-md);
  padding: var(--ffr-space-3) var(--ffr-space-3) var(--ffr-space-2);
  min-width: 0;
}

.chart figcaption {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 2px var(--ffr-space-4);
  margin-bottom: var(--ffr-space-2);
  padding-left: var(--ffr-space-1);
}

.chart-title b {
  font-family: var(--ffr-font-display);
  font-weight: 700;
  font-size: 14px;
}

.chart-title .muted {
  font-size: 12.5px;
}

.chart-read {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 2px var(--ffr-space-3);
  font-size: 12.5px;
}

.chart-key {
  white-space: nowrap;
}

.chart-key b {
  font-weight: 600;
  display: inline-block;
  min-width: 7.5ch;
}

/* The mark of a series in the legend: a piece of its line. */
.chart-key i {
  display: inline-block;
  width: 12px;
  height: 0;
  margin-right: 5px;
  vertical-align: middle;
  border-top: 3px solid var(--series-1);
  border-radius: 2px;
}

.chart-key i[data-slot="2"] {
  border-top-color: var(--series-2);
}

.chart-key i[data-slot="bound"] {
  border-top: 2px dashed var(--fg-subtle);
  border-radius: 0;
}

.chart-at {
  min-width: 8ch;
  text-align: right;
}

.chart-box {
  width: 100%;
  overflow: hidden;
}

.chart svg {
  display: block;
  cursor: crosshair;
  touch-action: pan-y;
  border-radius: var(--ffr-radius-sm);
}

.chart-grid {
  stroke: var(--grid);
  stroke-width: 1;
  shape-rendering: crispEdges;
}

.chart-tick {
  fill: var(--fg-muted);
  font-family: var(--ffr-font-mono);
  font-size: 11px;
}

.chart-line path {
  fill: none;
  stroke: var(--series-1);
  stroke-width: 2;
  stroke-linejoin: round;
  stroke-linecap: round;
}

.chart-line circle {
  fill: var(--series-1);
}

.chart-line[data-slot="2"] path {
  stroke: var(--series-2);
}

.chart-line[data-slot="2"] circle {
  fill: var(--series-2);
}

.chart-line[data-slot="bound"] path {
  stroke: var(--fg-subtle);
  stroke-width: 1.5;
  stroke-dasharray: 5 4;
  stroke-linecap: butt;
}

.chart-line[data-slot="bound"] circle {
  fill: var(--fg-subtle);
}

.chart-cross line {
  stroke: var(--fg);
  stroke-width: 1;
  shape-rendering: crispEdges;
}

/* The mark on a line under the pointer, with a ring of the ground so
   that it stands off the line. */
.chart-cross circle {
  fill: var(--series-1);
  stroke: var(--bg);
  stroke-width: 2;
}

.chart-cross circle[data-slot="2"] {
  fill: var(--series-2);
}

.chart-note {
  margin-top: var(--ffr-space-2);
  font-size: 13px;
}

.text-button {
  background: none;
  border: 0;
  padding: 0;
  color: var(--link);
  cursor: pointer;
}

.text-button:hover {
  text-decoration: underline;
  text-underline-offset: 3px;
}

.samples {
  margin-top: var(--ffr-space-3);
  max-height: 320px;
  overflow-y: auto;
}

.samples thead th {
  position: sticky;
  top: 0;
}

@media (max-width: 980px) {
  .charts {
    grid-template-columns: minmax(0, 1fr);
  }
}
```

- [ ] **Step 7: The view of a zathras**

What the zathras reported last is kept in a signal, with the path it
is of, so that it stays when the zathras goes and is never shown under
another zathras' path.

Replace `ui/src/views/Zathras.tsx`:

`ui/src/views/Zathras.tsx`:

```tsx
import { A, useParams } from "@solidjs/router";
import { createEffect, createMemo, createSignal, For, Match, Show, Switch } from "solid-js";
import { isID, isZeroTime, type Zathras as ZathrasInfo } from "../api/types";
import { PhaseBadge } from "../components/Badge";
import Figures from "../components/Figures";
import Notice from "../components/Notice";
import { ago, dateTime, duration, exit, generation, storeName, until } from "../format";
import { findService, findZathras, fleet, freshAt, now, received } from "../state/fleet";
import { phaseNote } from "../state/rollout";
import "./service.css";
import "./zathras.css";

// Everything a zathras reports, each thing under its name.
function StatePanel(props: { zathras: ZathrasInfo; stable: number | undefined }) {
  const z = () => props.zathras;
  const st = () => z().state;
  return (
    <dl class="state">
      <div>
        <dt class="label">Phase</dt>
        <dd>
          <PhaseBadge phase={st().phase} /> <span class="muted">{phaseNote(st(), now())}</span>
        </dd>
      </div>
      <div>
        <dt class="label">Generation</dt>
        <dd>
          <Show when={st().generation > 0} fallback={<span class="muted">none: nothing was activated on it</span>}>
            <span class="num">{generation(st().generation)}</span>{" "}
            <Show when={props.stable !== undefined}>
              <span class="muted">
                {st().generation === props.stable
                  ? "the stable one of the service"
                  : `not the stable one of the service, which is ${generation(props.stable)}`}
              </span>
            </Show>
          </Show>
        </dd>
      </div>
      <div class="wide">
        <dt class="label">Binary</dt>
        <dd>
          <Show when={st().binary} fallback={<span class="muted">none</span>}>
            <b class="mono">{storeName(st().binary)}</b>
            <span class="mono muted path">{st().binary}</span>
          </Show>
        </dd>
      </div>
      <div class="wide">
        <dt class="label">Arguments</dt>
        <dd>
          <Show when={(st().args?.length ?? 0) > 0} fallback={<span class="muted">none</span>}>
            <span class="args">
              <For each={st().args}>{(a) => <code>{a}</code>}</For>
            </span>
          </Show>
        </dd>
      </div>
      <div>
        <dt class="label">Process ID</dt>
        <dd>
          <Show when={st().pid} fallback={<span class="muted">none: the service does not run</span>}>
            <span class="num">{st().pid}</span>
          </Show>
        </dd>
      </div>
      <div>
        <dt class="label">Running for</dt>
        <dd>
          <Show when={st().running_for !== undefined} fallback={<span class="muted">not running</span>}>
            <span class="num">{duration(st().running_for)}</span>
          </Show>
        </dd>
      </div>
      <Show when={!isZeroTime(st().restart_at)}>
        <div>
          <dt class="label">Starts again</dt>
          <dd>
            <span class="num">{dateTime(st().restart_at)}</span> <span class="muted">({until(st().restart_at, now())})</span>
          </dd>
        </div>
      </Show>
      <div>
        <dt class="label">Restarts</dt>
        <dd>
          <span class="num">{st().restarts}</span> <span class="muted">since this zathras started</span>
        </dd>
      </div>
      <div class="wide">
        <dt class="label">Last exit</dt>
        <dd>
          <Show when={st().last_exit} fallback={<span class="muted">none: the service has not ended since this zathras started</span>}>
            {(e) => (
              <>
                <span class="exit" data-tone={e().status === 0 && !e().signal && !e().error ? undefined : "bad"}>
                  {exit(e())}
                </span>
                <span class="muted">
                  {" "}
                  of generation <span class="num">{generation(e().generation)}</span>, at{" "}
                  <span class="num">{dateTime(e().at)}</span> ({ago(e().at, now())}), after it ran{" "}
                  <span class="num">{duration(e().ran)}</span>
                </span>
                <Show when={e().error}>
                  <span class="reason">{e().error}</span>
                </Show>
              </>
            )}
          </Show>
        </dd>
      </div>
      <div>
        <dt class="label">Prepared generations</dt>
        <dd>
          <Show when={(st().prepared?.length ?? 0) > 0} fallback={<span class="muted">none</span>}>
            <span class="num">{st().prepared!.join(", ")}</span>
          </Show>
        </dd>
      </div>
      <div>
        <dt class="label">Preparing</dt>
        <dd>
          <Show when={st().preparing} fallback={<span class="muted">nothing</span>}>
            generation <span class="num">{st().preparing}</span>
          </Show>
        </dd>
      </div>
      <div>
        <dt class="label">Connected to draal</dt>
        <dd>
          <span class="num">{dateTime(z().connected)}</span> <span class="muted">({ago(z().connected, now())})</span>
        </dd>
      </div>
      <div class="wide">
        <dt class="label">Endpoint ID</dt>
        <dd>
          <span class="mono whole-id">{z().id}</span>
        </dd>
      </div>
    </dl>
  );
}

// View 3: a zathras: its state in full, its figures, its log, a shell.
//
// Review Focus 2: when the zathras goes while this view is open, the
// view says so, keeps what it showed, and asks nothing more; when the
// fleet has the zathras again, it carries on.
export default function Zathras() {
  const params = useParams<{ service: string; id: string }>();
  const live = () => (isID(params.id) ? findZathras(params.service, params.id) : undefined);

  // What the zathras reported last, kept when it goes, and when that
  // was. It is kept with the path it is of: what another zathras
  // reported is never shown under this one's.
  const path = () => `${params.service}/${params.id}`;
  const [kept, setKept] = createSignal<{ path: string; zathras: ZathrasInfo; seen: number }>();
  createEffect(() => {
    const z = live();
    if (z) {
      // Reading the whole of it is what makes this run again when any
      // of it changes.
      setKept({ path: path(), zathras: JSON.parse(JSON.stringify(z)) as ZathrasInfo, seen: freshAt() });
    }
  });
  const last = () => (kept()?.path === path() ? kept()!.zathras : undefined);
  const seen = () => kept()?.seen ?? 0;

  // The service that has a zathras of this ID, when it is not the one
  // of the path.
  const elsewhere = createMemo(() =>
    isID(params.id) && !live() ? fleet.services.find((s) => s.zathrases.some((z) => z.id === params.id))?.name : undefined,
  );

  return (
    <Switch>
      <Match when={!isID(params.id)}>
        <div class="head">
          <h1>No such zathras</h1>
        </div>
        <Notice title="This is no endpoint ID">
          <p>
            A zathras is named in the path by its endpoint ID, 64 hexadecimal characters. <span class="mono wrap">{params.id}</span>{" "}
            is none.
          </p>
          <p>
            <A href={`/s/${encodeURIComponent(params.service)}`}>Go to the service {params.service}</A>
          </p>
        </Notice>
      </Match>
      <Match when={last()}>
        {(z) => (
          <>
            <div class="head z-head">
              <h1>{z().name}</h1>
              <span class="muted">
                a zathras of <A href={`/s/${encodeURIComponent(params.service)}`}>{params.service}</A>
              </span>
            </div>
            <Show when={!live() && received()}>
              <div class="gone" role="alert">
                <b>This zathras is not connected to draal any more.</b> What is below is what it reported last,{" "}
                {ago(seen(), now())}. The page asks nothing of it now, and carries on when it is back.
              </div>
            </Show>
            <div data-stale={!live() && received() ? "" : undefined} class="z-body">
              <StatePanel zathras={z()} stable={findService(params.service)?.stable} />
            </div>
            <Figures id={params.id} present={live() !== undefined} connected={live()?.connected} sample={live()?.state.sample} />
          </>
        )}
      </Match>
      <Match when={!received()}>
        <div class="head">
          <h1>Zathras</h1>
        </div>
        <p class="muted">Asking draal…</p>
      </Match>
      <Match when={true}>
        <div class="head">
          <h1>No such zathras</h1>
        </div>
        <Notice title={`No zathras with this ID is connected to ${params.service}`}>
          <p>
            <span class="mono wrap">{params.id}</span>
          </p>
          <Show
            when={elsewhere()}
            fallback={
              <p>
                It may have ended, or its pod was replaced: a new pod is a new zathras with a new ID. This page shows it
                as soon as draal has it.
              </p>
            }
          >
            <p>
              A zathras with this ID is connected to another service:{" "}
              <A href={`/s/${encodeURIComponent(elsewhere()!)}/z/${params.id}`}>{elsewhere()}</A>.
            </p>
          </Show>
          <p>
            <A href={`/s/${encodeURIComponent(params.service)}`}>Go to the service {params.service}</A>
          </p>
        </Notice>
      </Match>
    </Switch>
  );
}
```

`ui/src/views/zathras.css`:

```css
/* View 3: a zathras. */

.z-head h1 {
  font-size: 24px;
}

/* What is said while the zathras is gone. */
.gone {
  border: 1px solid var(--bad);
  background: var(--bad-wash);
  color: var(--bad-text);
  border-radius: var(--ffr-radius-md);
  padding: var(--ffr-space-3) var(--ffr-space-4);
  margin-bottom: var(--ffr-space-4);
  max-width: 96ch;
}

.z-body[data-stale] {
  opacity: 0.62;
}

/* The state: a name above each value, as many beside each other as fit. */
.state {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--ffr-space-4) var(--ffr-space-5);
  border: 1px solid var(--border);
  border-radius: var(--ffr-radius-md);
  padding: var(--ffr-space-4) var(--ffr-space-5);
}

.state > div {
  display: grid;
  gap: 3px;
  align-content: start;
  min-width: 0;
}

.state > .wide {
  grid-column: span 2;
}

.state dd {
  overflow-wrap: anywhere;
  min-width: 0;
}

.state .path {
  display: block;
  font-size: 12px;
}

.state .reason {
  display: block;
  margin-top: 2px;
}

/* The arguments, each in a box of its own: one with a space in it, or
   an empty one, is seen for what it is. */
.args {
  display: flex;
  flex-wrap: wrap;
  gap: var(--ffr-space-1);
}

.args code {
  font-family: var(--ffr-font-mono);
  font-size: 12.5px;
  background: var(--surface-strong);
  border-radius: 3px;
  padding: 1px 6px;
  min-width: 1.5ch;
  min-height: 1.5em;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.whole-id {
  font-size: 12.5px;
  user-select: all;
}

.wrap {
  overflow-wrap: anywhere;
}

@media (max-width: 980px) {
  .state {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .state {
    grid-template-columns: minmax(0, 1fr);
    padding: var(--ffr-space-4);
  }

  .state > .wide {
    grid-column: auto;
  }

  .z-head h1 {
    font-size: 20px;
  }
}
```

Run: `nix develop .#bare --command npm --prefix ui run check`, `nix develop .#bare --command npm --prefix ui test` and `nix develop .#bare --command npm --prefix ui run build`
Expected: no error; `Test Files  7 passed (7)`, `Tests  72 passed (72)`; `✓ built`.

- [ ] **Step 8: Look at it (Review Focus 2 and 4)**

Run `nix develop .#bare --command go run ./web/demo --addr 127.0.0.1:8080 --dist web/dist` and open `http://127.0.0.1:8080/s/web`, console open;
follow the link of a zathras. The console stays empty throughout.

- The state: every field under its name: phase with since when, generation and whether it is the stable one, the binary without the hash of its store path and in full, each argument in a box, process ID, running for, restarts, last exit in full, prepared generations, preparing, connected since, the whole endpoint ID.
- Four charts in two columns: CPU, Memory with a dashed line at its limit, Disk, Network. All four end at the same time at the right; the labels of the value axis are `0%`, `100%`, `200%` and `0 B`, `256 MiB`, `512 MiB` and the like. Move the pointer over one: a line marks the same time in all four, and each legend shows the values of that sample and its time instead of `now`. Focus a chart with Tab and press the left arrow: the same. `Show the samples as a table` shows them, newest first.
- Review Focus 4: `/s/fresh` → `fresh-1`, and `/s/worker` → `worker-2`: in place of the charts, `This zathras reports no figures`, with the reason. No empty chart.
- Review Focus 2: `/s/web` → `web-7d9f8b6c4-q5rvd`. At second 43 of the demo's minute a red box says `This zathras is not connected to draal any more. What is below is what it reported last, 2 s ago. …`; the state is dimmed and the charts stay. In the network panel nothing is asked while it is gone. At second 49 the box goes, `/api/zathras/…/history` is asked for once, and every line of the charts has a gap where the zathras was away.
- `/s/web/z/abc`: `This is no endpoint ID`. `/s/web/z/` followed by 64 zeros: `No zathras with this ID is connected to web`. The ID of a zathras of `web` under `/s/worker/z/…`: the same, with a link to it under `web`.
- At a width of 390 pixels: the state is one column, the charts are one column, nothing scrolls sideways. In the dark the lines are the dark steps of the two colours.

- [ ] **Step 9: Commit**

```bash
git add ui/src/components/ ui/src/views/
git commit -m "UI: a zathras: its state in full and its figures as charts"
```
