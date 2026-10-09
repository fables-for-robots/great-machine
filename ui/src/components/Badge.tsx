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
