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
