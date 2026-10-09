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
