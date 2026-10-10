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
