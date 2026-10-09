// What the page works out from the fleet about a rollout and about the
// phases of a service's zathrases. It reads what a zathras reports and
// nothing else: the fleet has no record of a rollout for each zathras,
// so where a zathras is in a rollout is what its own state says about
// the rollout's generation.

import type { Phase, RolloutInfo, State, Zathras } from "../api/types";
import { duration, until } from "../format";

/** Where a zathras is on the way to the generation of a rollout. */
export type Stage =
  /** It has not prepared the generation and is not preparing it. */
  | "waiting"
  /** It is making the closure of the generation present. */
  | "preparing"
  /** It has the closure and runs something else, or nothing. */
  | "prepared"
  /** It has taken the generation as its own and runs it, or is stopping
   *  it. Whether draal counts it as activated the page cannot tell:
   *  that is a matter of how long it has run, by a time draal is
   *  configured with. */
  | "on"
  /** It has taken the generation as its own, and the service has ended
   *  and waits to be started again. */
  | "failed";

/** The stages in the order of a rollout. */
export const STAGES: readonly Stage[] = ["waiting", "preparing", "prepared", "on", "failed"];

/** Where a zathras that reports state is on the way to the generation. */
export function stageOf(state: State, target: number): Stage {
  if (state.generation === target) {
    return state.phase === "backoff" ? "failed" : "on";
  }
  if (state.preparing === target) {
    return "preparing";
  }
  if (state.prepared?.includes(target)) {
    return "prepared";
  }
  return "waiting";
}

/** The zathrases of each stage, in the order they were given. */
export function byStage<Z extends Zathras>(zathrases: readonly Z[], target: number): Record<Stage, Z[]> {
  const out: Record<Stage, Z[]> = { waiting: [], preparing: [], prepared: [], on: [], failed: [] };
  for (const z of zathrases) {
    out[stageOf(z.state, target)].push(z);
  }
  return out;
}

/** The phases in the order the page lists them. */
export const PHASES: readonly Phase[] = ["running", "stopping", "backoff", "idle"];

/** How many zathrases are in each phase. A phase the page does not know
 *  is counted as idle: it runs nothing the page could name. */
export function byPhase(zathrases: readonly Zathras[]): Record<Phase, number> {
  const out: Record<Phase, number> = { running: 0, stopping: 0, backoff: 0, idle: 0 };
  for (const z of zathrases) {
    out[knownPhase(z.state.phase)]++;
  }
  return out;
}

/** The phase, or idle for one the page does not know. */
export function knownPhase(phase: string): Phase {
  return phase === "running" || phase === "stopping" || phase === "backoff" ? phase : "idle";
}

/** The tone a phase is drawn in. */
export function phaseTone(phase: string): "ok" | "warn" | "bad" | "idle" {
  switch (knownPhase(phase)) {
    case "running":
      return "ok";
    case "stopping":
      return "warn";
    case "backoff":
      return "bad";
    default:
      return "idle";
  }
}

/** The tone a state of a rollout is drawn in. */
export function rolloutTone(state: string): "ok" | "busy" | "bad" | "idle" {
  switch (state) {
    case "preparing":
    case "activating":
      return "busy";
    case "completed":
      return "ok";
    case "halted":
      return "bad";
    default:
      return "idle";
  }
}

/** What there is to say about a phase besides its name: since when, or
 *  until when. Empty when the state says nothing of it. */
export function phaseNote(state: State, now: number): string {
  switch (knownPhase(state.phase)) {
    case "running":
      return state.running_for === undefined ? "" : `for ${duration(state.running_for)}`;
    case "backoff": {
      const u = until(state.restart_at, now);
      return u === "–" ? "" : u === "now" ? "starts again now" : `starts again ${u}`;
    }
    case "stopping":
      return "told to end";
    default:
      return state.generation === 0 ? "has no generation" : "";
  }
}

/** Says whether a rollout is one to show with every zathras' part in
 *  it: under way, or halted. */
export function open(r: RolloutInfo | undefined): r is RolloutInfo {
  return r !== undefined && (r.state === "preparing" || r.state === "activating" || r.state === "halted");
}
