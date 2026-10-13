import { describe, expect, test } from "vitest";
import {
  isID,
  isRefusal,
  isRole,
  isZeroTime,
  normaliseFleet,
  normaliseGrants,
  normaliseIdentity,
  parseTime,
  underWay,
} from "./types";

describe("grants", () => {
  test("the three roles are roles, and nothing else is", () => {
    expect(["admin", "deployer", "zathras"].every(isRole)).toBe(true);
    expect(["", "operator", "Admin", "root"].some(isRole)).toBe(false);
  });

  test("a list of grants is a list, whatever stands for an empty one", () => {
    expect(normaliseGrants(null)).toEqual([]);
    expect(normaliseGrants(undefined)).toEqual([]);
    expect(normaliseGrants("grants")).toEqual([]);
    const g = { id: "a".repeat(64), role: "deployer", service: "web", at: "2026-10-13T09:00:00Z" };
    expect(normaliseGrants([g])).toEqual([g]);
  });

  test("what is no grant is left out of a list", () => {
    const g = { id: "a".repeat(64), role: "admin", at: "0001-01-01T00:00:00Z", fixed: true };
    expect(normaliseGrants([null, 7, {}, { id: 1, role: "admin" }, { id: "x" }, g])).toEqual([g]);
  });

  test("who a key is has an ID and a list", () => {
    expect(normaliseIdentity({ id: "ab", grants: null })).toEqual({ id: "ab", grants: [] });
    expect(normaliseIdentity(null)).toEqual({ id: "", grants: [] });
    expect(normaliseIdentity({ id: 7 })).toEqual({ id: "", grants: [] });
  });
});

describe("times", () => {
  test("the zero time is none", () => {
    expect(isZeroTime("0001-01-01T00:00:00Z")).toBe(true);
    expect(isZeroTime(undefined)).toBe(true);
    expect(isZeroTime("")).toBe(true);
    expect(isZeroTime("2026-10-09T22:00:30.325511+02:00")).toBe(false);
    expect(parseTime("0001-01-01T00:00:00Z")).toBeUndefined();
  });

  test("a time is parsed with its offset and its fraction", () => {
    expect(parseTime("2026-10-09T20:00:30Z")).toBe(Date.UTC(2026, 9, 9, 20, 0, 30));
    expect(parseTime("2026-10-09T22:00:30+02:00")).toBe(Date.UTC(2026, 9, 9, 20, 0, 30));
    const a = parseTime("2026-10-09T22:00:30.325511+02:00");
    const b = parseTime("2026-10-09T22:00:30.325512001+02:00");
    expect(a).toBeDefined();
    expect(b).toBeDefined();
    expect(b! > a!).toBe(true);
    expect(Math.floor(a!)).toBe(Date.UTC(2026, 9, 9, 20, 0, 30, 325));
  });

  test("what is no time is undefined, never NaN", () => {
    expect(parseTime("yesterday")).toBeUndefined();
    expect(parseTime(null)).toBeUndefined();
  });
});

describe("shapes", () => {
  test("an endpoint ID is 64 lowercase hexadecimal characters", () => {
    expect(isID("3f08d61e04d64c081e7eff0c98be5343638e90c39d73b9088b87da7da0dd1eb1")).toBe(true);
    expect(isID("3F08D61E04D64C081E7EFF0C98BE5343638E90C39D73B9088B87DA7DA0DD1EB1")).toBe(false);
    expect(isID("abc")).toBe(false);
  });

  test("a refusal is recognised", () => {
    expect(isRefusal({ error: { code: "not-found", message: "no such zathras" } })).toBe(true);
    expect(isRefusal({ exit: 3 })).toBe(false);
    expect(isRefusal(null)).toBe(false);
    expect(isRefusal({ error: "text" })).toBe(false);
  });

  test("a rollout is under way while it prepares or activates", () => {
    expect(underWay("preparing")).toBe(true);
    expect(underWay("activating")).toBe(true);
    expect(underWay("completed")).toBe(false);
    expect(underWay("halted")).toBe(false);
    expect(underWay("superseded")).toBe(false);
  });

  test("null lists of a fleet are empty lists", () => {
    expect(normaliseFleet({ services: null })).toEqual({ services: [] });
    expect(normaliseFleet({})).toEqual({ services: [] });
    expect(normaliseFleet("text")).toEqual({ services: [] });
    const f = normaliseFleet({ services: [{ name: "web", stable: 0, zathrases: null }] });
    expect(f.services[0]?.zathrases).toEqual([]);
  });
});
