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
