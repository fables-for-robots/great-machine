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
