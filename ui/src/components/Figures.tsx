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
        {/* Final review B2: the zathras went before draal had answered
            (the request was given up with it): whether it reports
            figures was not found out, and is not claimed. */}
        <Match when={!asked()}>
          <p class="muted">The figures of this zathras were not received before it went.</p>
        </Match>
        {/* Review Focus 4: a zathras without samples gets a sentence that
            says why, and no empty charts. It is said when it is known:
            draal has answered, and neither its answer nor a fleet had a
            sample. */}
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
