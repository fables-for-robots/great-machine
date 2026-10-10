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
