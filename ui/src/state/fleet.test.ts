import { expect, test } from "vitest";
import type { Fleet, Zathras } from "../api/types";
import { fleet, findService, findZathras, received, rows, take } from "./fleet";

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
