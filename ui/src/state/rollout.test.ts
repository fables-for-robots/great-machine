import { describe, expect, test } from "vitest";
import type { State, Zathras } from "../api/types";
import { byPhase, byStage, knownPhase, open, phaseNote, phaseTone, rolloutTone, stageOf } from "./rollout";

const ZERO = "0001-01-01T00:00:00Z";

function state(over: Partial<State>): State {
  return { generation: 19, phase: "running", restart_at: ZERO, restarts: 0, ...over };
}

function zathras(id: string, over: Partial<State>): Zathras {
  return { id, name: `z-${id}`, connected: "2026-10-09T20:00:00Z", state: state(over) };
}

describe("where a zathras is in a rollout", () => {
  test("by what it reports of the rollout's generation", () => {
    expect(stageOf(state({ prepared: [19] }), 20)).toBe("waiting");
    expect(stageOf(state({ prepared: [19], preparing: 20 }), 20)).toBe("preparing");
    expect(stageOf(state({ prepared: [19, 20] }), 20)).toBe("prepared");
    expect(stageOf(state({ generation: 20, prepared: [19, 20] }), 20)).toBe("on");
    expect(stageOf(state({ generation: 20, phase: "stopping" }), 20)).toBe("on");
    expect(stageOf(state({ generation: 20, phase: "backoff" }), 20)).toBe("failed");
  });

  test("a zathras without a generation and without a list is waiting", () => {
    expect(stageOf({ generation: 0, phase: "idle", restart_at: ZERO, restarts: 0 }, 1)).toBe("waiting");
  });

  test("the zathrases of each stage keep their order", () => {
    const zs = [
      zathras("a", { generation: 20 }),
      zathras("b", { prepared: [20] }),
      zathras("c", { generation: 20, phase: "backoff" }),
      zathras("d", { prepared: [20] }),
      zathras("e", {}),
    ];
    const s = byStage(zs, 20);
    expect(s.on.map((z) => z.id)).toEqual(["a"]);
    expect(s.prepared.map((z) => z.id)).toEqual(["b", "d"]);
    expect(s.failed.map((z) => z.id)).toEqual(["c"]);
    expect(s.waiting.map((z) => z.id)).toEqual(["e"]);
    expect(s.preparing).toEqual([]);
  });
});

describe("phases", () => {
  test("are counted, and one the page does not know is idle", () => {
    const zs = [
      zathras("a", {}),
      zathras("b", {}),
      zathras("c", { phase: "backoff" }),
      zathras("d", { phase: "stopping" }),
      zathras("e", { phase: "idle" }),
      zathras("f", { phase: "hibernating" as never }),
    ];
    expect(byPhase(zs)).toEqual({ running: 2, stopping: 1, backoff: 1, idle: 2 });
    expect(byPhase([])).toEqual({ running: 0, stopping: 0, backoff: 0, idle: 0 });
    expect(knownPhase("hibernating")).toBe("idle");
  });

  test("have a tone", () => {
    expect(phaseTone("running")).toBe("ok");
    expect(phaseTone("stopping")).toBe("warn");
    expect(phaseTone("backoff")).toBe("bad");
    expect(phaseTone("idle")).toBe("idle");
    expect(phaseTone("")).toBe("idle");
  });

  test("have a note of since when or until when", () => {
    const now = Date.UTC(2026, 9, 9, 20, 0, 0);
    expect(phaseNote(state({ running_for: 2_739_385_042 }), now)).toBe("for 2.7 s");
    expect(phaseNote(state({}), now)).toBe("");
    expect(phaseNote(state({ phase: "backoff", restart_at: "2026-10-09T20:00:06Z" }), now)).toBe("starts again in 6 s");
    expect(phaseNote(state({ phase: "backoff", restart_at: "2026-10-09T19:59:00Z" }), now)).toBe("starts again now");
    expect(phaseNote(state({ phase: "backoff" }), now)).toBe("");
    expect(phaseNote(state({ phase: "stopping" }), now)).toBe("told to end");
    expect(phaseNote(state({ phase: "idle", generation: 0 }), now)).toBe("has no generation");
    expect(phaseNote(state({ phase: "idle", generation: 3 }), now)).toBe("");
  });
});

describe("rollouts", () => {
  test("have a tone", () => {
    expect(rolloutTone("preparing")).toBe("busy");
    expect(rolloutTone("activating")).toBe("busy");
    expect(rolloutTone("completed")).toBe("ok");
    expect(rolloutTone("halted")).toBe("bad");
    expect(rolloutTone("superseded")).toBe("idle");
    expect(rolloutTone("")).toBe("idle");
  });

  test("are open while under way or halted", () => {
    const r = {
      id: 1,
      service: "web",
      generation: 2,
      parallel: 1,
      created: "2026-10-09T20:00:00Z",
      created_by: "aa",
      finished: ZERO,
      prepared: 0,
      activated: 0,
      awaited: 0,
    };
    expect(open({ ...r, state: "preparing" })).toBe(true);
    expect(open({ ...r, state: "activating" })).toBe(true);
    expect(open({ ...r, state: "halted" })).toBe(true);
    expect(open({ ...r, state: "completed" })).toBe(false);
    expect(open({ ...r, state: "superseded" })).toBe(false);
    expect(open(undefined)).toBe(false);
  });
});
