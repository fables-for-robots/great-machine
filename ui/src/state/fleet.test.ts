import { createEffect, createRoot, on } from "solid-js";
import { expect, test } from "vitest";
import type { Fleet, Zathras } from "../api/types";
import { arrived, connection, fleet, findService, findZathras, freshAt, lives, received, refusal, rows, take } from "./fleet";

function zathras(id: string, name: string): Zathras {
  return {
    id,
    name,
    connected: "2026-10-09T20:00:00Z",
    state: { generation: 0, phase: "idle", restart_at: "0001-01-01T00:00:00Z", restarts: 0 },
  };
}

test("the rows are by name, and zathrases of one name by ID", () => {
  const f: Fleet = {
    services: [
      { name: "worker", stable: 3, zathrases: [] },
      { name: "web", stable: 19, zathrases: [zathras("ee", "web-b"), zathras("ff", "web-a"), zathras("aa", "web-b")] },
    ],
  };
  const r = rows(f);
  expect(r.map((s) => s.key)).toEqual(["web", "worker"]);
  expect(r[0]!.zathrases.map((z) => z.key)).toEqual(["ff", "aa", "ee"]);
});

test("a fleet is taken into the store, and a zathras that stays keeps its row", () => {
  expect(received()).toBe(false);
  take(JSON.stringify({ services: [{ name: "web", stable: 1, zathrases: [zathras("aa", "a"), zathras("bb", "b")] }] }));
  expect(received()).toBe(true);
  const before = findZathras("web", "bb");
  expect(before?.name).toBe("b");

  // The first zathras leaves: the second is the same row still.
  take(JSON.stringify({ services: [{ name: "web", stable: 2, zathrases: [zathras("bb", "b")] }] }));
  expect(fleet.services[0]!.zathrases.length).toBe(1);
  expect(findZathras("web", "bb")).toBe(before);
  expect(findService("web")?.stable).toBe(2);
  expect(findZathras("web", "aa")).toBeUndefined();
  expect(findService("nope")).toBeUndefined();
});

test("a fleet with null lists is an empty one, and nonsense is not taken", () => {
  take(JSON.stringify({ services: [{ name: "web", stable: 1, zathrases: null }] }));
  expect(fleet.services[0]!.zathrases).toEqual([]);
  take("{not json");
  take(new ArrayBuffer(4));
  expect(fleet.services.length).toBe(1);
  take(JSON.stringify({ services: null }));
  expect(fleet.services).toEqual([]);
});

// Final review A1: the last message of /api/watch when draal fails is a
// refusal. It is no fleet: the page keeps the one it has.
test("a refusal, an object without services and what is no object are not taken", () => {
  expect(take(JSON.stringify({ services: [{ name: "web", stable: 4, zathrases: [zathras("aa", "a")] }] }))).toBe(true);
  const row = findZathras("web", "aa");
  const at = freshAt();
  expect(at).toBeGreaterThan(0);

  const refused = JSON.stringify({ error: { code: "internal", message: "internal error" } });
  for (const data of [
    refused,
    "{}",
    "[]",
    "null",
    "7",
    JSON.stringify({ zathrases: [] }),
    JSON.stringify({ error: { code: "internal", message: "internal error" }, services: [] }),
  ]) {
    expect(take(data), data).toBe(false);
  }
  expect(fleet.services.length).toBe(1);
  expect(findZathras("web", "aa")).toBe(row);
  expect(findService("web")?.stable).toBe(4);
  expect(freshAt()).toBe(at);
  expect(received()).toBe(true);

  // What draal said is kept for the page to say, until a fleet arrives.
  expect(refusal()).toBe("internal error");
  expect(take("{}")).toBe(false);
  expect(refusal()).toBe("internal error");
  expect(take(JSON.stringify({ services: [] }))).toBe(true);
  expect(refusal()).toBeUndefined();
  expect(fleet.services).toEqual([]);
});

test("a refusal before any fleet leaves the page not knowing the fleet", async () => {
  // The state of the module is that of a page just opened.
  const { vi } = await import("vitest");
  vi.resetModules();
  const fresh = await import("./fleet");
  expect(fresh.received()).toBe(false);
  expect(fresh.arrived(JSON.stringify({ error: { code: "internal", message: "internal error" } }))).toBe(false);
  expect(fresh.received()).toBe(false);
  expect(fresh.freshAt()).toBe(0);
  expect(fresh.connection()).toBe("connecting");
  expect(fresh.lives()).toBe(0);
  expect(fresh.refusal()).toBe("internal error");
});

// Final review A1 and A2: the first fleet of a connection makes it live,
// and what reacts to that finds the fleet in the store already.
test("lives goes up with the first fleet of a connection, and who reacts to it sees that fleet", () => {
  const seen: { lives: number; services: string[]; connection: string }[] = [];
  const dispose = createRoot((dispose) => {
    createEffect(
      on(
        lives,
        (n) => {
          seen.push({ lives: n, services: fleet.services.map((s) => s.key), connection: connection() });
        },
        { defer: true },
      ),
    );
    return dispose;
  });
  const before = lives();

  // A refusal is no fleet: nothing is live by it.
  expect(arrived(JSON.stringify({ error: { code: "unavailable", message: "draal has stopped sending the fleet" } }))).toBe(false);
  expect(lives()).toBe(before);
  expect(seen).toEqual([]);

  expect(arrived(JSON.stringify({ services: [{ name: "after", stable: 1, zathrases: [] }] }))).toBe(true);
  expect(lives()).toBe(before + 1);
  expect(connection()).toBe("live");
  expect(seen).toEqual([{ lives: before + 1, services: ["after"], connection: "live" }]);

  // The next fleet of the same connection is no new life.
  expect(arrived(JSON.stringify({ services: [{ name: "later", stable: 1, zathrases: [] }] }))).toBe(true);
  expect(lives()).toBe(before + 1);
  expect(seen.length).toBe(1);
  dispose();
});
