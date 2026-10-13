import { describe, expect, test } from "vitest";
import { revokeQuery } from "../api/client";
import type { Grant } from "../api/types";
import { describeGrant, formRequest, grantKey, grantedBy, grantedWhen, isAdmin, revocable, rolesLine, scope } from "./access";

const ZERO = "0001-01-01T00:00:00Z";
const ME = "a".repeat(64);
const OTHER = "b".repeat(64);
const ADMIN = "c".repeat(64);

function grant(over: Partial<Grant>): Grant {
  return { id: OTHER, role: "deployer", at: "2026-10-13T09:00:00Z", by: ADMIN, ...over };
}

describe("how a grant is written", () => {
  test("its service: the one it names, or every one", () => {
    expect(scope(grant({ service: "web" }))).toBe("web");
    expect(scope(grant({}))).toBe("every");
    expect(scope(grant({ service: "" }))).toBe("every");
    // An admin is of every service, whatever the grant says.
    expect(scope(grant({ role: "admin", service: "web" }))).toBe("every");
  });

  test("in a sentence: the role, and of what", () => {
    expect(describeGrant(grant({ service: "web" }))).toBe("deployer of web");
    expect(describeGrant(grant({}))).toBe("deployer of every service");
    expect(describeGrant(grant({ role: "zathras", service: "worker" }))).toBe("zathras of worker");
    expect(describeGrant(grant({ role: "admin" }))).toBe("admin");
    // A role the page does not know is written as it came.
    expect(describeGrant(grant({ role: "auditor", service: "web" }))).toBe("auditor of web");
    expect(describeGrant(grant({ role: "" }))).toBe("no role of every service");
  });

  test("who granted it: the start of an ID, or draal's command line", () => {
    expect(grantedBy(grant({}))).toBe("cccccccccc");
    expect(grantedBy(grant({ fixed: true, by: undefined }))).toBe("draal's command line");
    expect(grantedBy(grant({ by: undefined }))).toBe("–");
    expect(grantedBy(grant({ by: "" }))).toBe("–");
  });

  test("its age: how long ago, and none for one of the command line", () => {
    const now = Date.parse("2026-10-13T09:03:20Z");
    expect(grantedWhen(grant({}), now)).toBe("3 m ago");
    expect(grantedWhen(grant({ at: "2026-10-10T09:00:00Z" }), now)).toBe("3 d ago");
    expect(grantedWhen(grant({ at: ZERO, fixed: true }), now)).toBe("–");
    expect(grantedWhen(grant({ at: ZERO }), now)).toBe("–");
    expect(grantedWhen(grant({ at: "soon" }), now)).toBe("–");
  });

  test("its key tells apart the grants of one ID", () => {
    const keys = [
      grantKey(grant({ service: "web" })),
      grantKey(grant({ service: "worker" })),
      grantKey(grant({})),
      grantKey(grant({ role: "zathras", service: "web" })),
      grantKey(grant({ id: ME, service: "web" })),
    ];
    expect(new Set(keys).size).toBe(keys.length);
    expect(grantKey(grant({ service: "web", note: "a" }))).toBe(grantKey(grant({ service: "web", note: "b" })));
  });
});

describe("what the header says of a key", () => {
  test("an admin is an admin, whatever else it holds", () => {
    const grants = [grant({ id: ME, role: "admin", fixed: true }), grant({ id: ME, service: "web" })];
    expect(isAdmin(grants)).toBe(true);
    expect(rolesLine(grants)).toBe("admin");
  });

  test("a deployer, of which services", () => {
    expect(rolesLine([grant({ id: ME, service: "web" })])).toBe("deployer of web");
    expect(rolesLine([grant({ id: ME, service: "worker" }), grant({ id: ME, service: "web" })])).toBe("deployer of web, worker");
    expect(isAdmin([grant({ id: ME, service: "web" })])).toBe(false);
  });

  test("a deployer of every service is that, and its other grants say no more", () => {
    expect(rolesLine([grant({ id: ME, service: "web" }), grant({ id: ME })])).toBe("deployer of every service");
  });

  test("many services are counted after the first three", () => {
    const grants = ["e", "d", "c", "b", "a"].map((s) => grant({ id: ME, service: s }));
    expect(rolesLine(grants)).toBe("deployer of a, b, c and 2 more");
  });

  test("a key that holds a role key's grant is said to", () => {
    expect(rolesLine([grant({ id: ME, role: "zathras", service: "web" })])).toBe("zathras of web");
    expect(rolesLine([grant({ id: ME, service: "web" }), grant({ id: ME, role: "zathras", service: "api" })])).toBe(
      "deployer of web; zathras of api",
    );
  });

  test("a key with no grant has no role", () => {
    expect(rolesLine([])).toBe("no role");
    expect(isAdmin([])).toBe(false);
  });
});

describe("which grants can be revoked", () => {
  test("not one of draal's command line", () => {
    expect(revocable(grant({ role: "admin", fixed: true, by: undefined }), ME)).toBe(false);
  });

  test("not the admin's own admin grant", () => {
    expect(revocable(grant({ id: ME, role: "admin" }), ME)).toBe(false);
    // Its other grants, and the admin grant of another, are.
    expect(revocable(grant({ id: ME, service: "web" }), ME)).toBe(true);
    expect(revocable(grant({ id: OTHER, role: "admin" }), ME)).toBe(true);
  });

  test("every other one", () => {
    expect(revocable(grant({ service: "web" }), ME)).toBe(true);
    expect(revocable(grant({ role: "zathras" }), undefined)).toBe(true);
  });

  test("what is asked to revoke one names its three parts", () => {
    expect(revokeQuery(grant({ service: "web" }))).toBe(`id=${OTHER}&role=deployer&service=web`);
    // Every service is an empty service, and is written.
    expect(revokeQuery(grant({ role: "zathras" }))).toBe(`id=${OTHER}&role=zathras&service=`);
    expect(revokeQuery(grant({ role: "admin", service: "web" }))).toBe(`id=${OTHER}&role=admin&service=`);
  });
});

describe("what the form sends", () => {
  test("a deployer of a service, with a note", () => {
    expect(formRequest({ role: "deployer", id: OTHER, service: "web", note: "ci" })).toEqual({
      ok: true,
      request: { id: OTHER, role: "deployer", service: "web", note: "ci" },
    });
  });

  test("what was typed around it is taken off, and an ID in capitals is one", () => {
    expect(formRequest({ role: "zathras", id: `  ${OTHER.toUpperCase()}\n`, service: " web ", note: "  the pods of web " })).toEqual({
      ok: true,
      request: { id: OTHER, role: "zathras", service: "web", note: "the pods of web" },
    });
  });

  test("no service is every service, and no note is none", () => {
    expect(formRequest({ role: "deployer", id: OTHER, service: "", note: " " })).toEqual({
      ok: true,
      request: { id: OTHER, role: "deployer" },
    });
  });

  test("an admin is granted no service, whatever the field held", () => {
    expect(formRequest({ role: "admin", id: OTHER, service: "web", note: "" })).toEqual({
      ok: true,
      request: { id: OTHER, role: "admin" },
    });
  });

  test("what is no endpoint ID is not sent, and the form says why", () => {
    for (const id of ["", "abc", "g".repeat(64), OTHER + "0", OTHER.slice(1)]) {
      const r = formRequest({ role: "deployer", id, service: "web", note: "" });
      expect(r.ok).toBe(false);
      if (!r.ok) {
        expect(r.problem).toMatch(/64/);
      }
    }
    const none = formRequest({ role: "deployer", id: "  ", service: "web", note: "" });
    expect(none.ok).toBe(false);
  });

  test("a role that is none is not sent", () => {
    const r = formRequest({ role: "root", id: OTHER, service: "", note: "" });
    expect(r.ok).toBe(false);
  });

  test("the name of a service is draal's to judge", () => {
    // The page sends it as it was typed: draal says what is wrong with
    // it, in its own words.
    expect(formRequest({ role: "deployer", id: OTHER, service: "Not A Label", note: "" })).toEqual({
      ok: true,
      request: { id: OTHER, role: "deployer", service: "Not A Label" },
    });
  });
});
