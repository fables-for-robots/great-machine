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
