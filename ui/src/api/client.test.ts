import { afterEach, describe, expect, test, vi } from "vitest";
import { errorOf, getJSON, grant, grants, me, onSignedOut, revoke } from "./client";

test("a refusal becomes its code and its message", () => {
  const e = errorOf(404, '{"error":{"code":"not-found","message":"zathras abc is not connected"}}');
  expect(e.code).toBe("not-found");
  expect(e.message).toBe("zathras abc is not connected");
  expect(e.status).toBe(404);
});

test("a plain line that is no refusal is shown as its text", () => {
  const line = "the proxy in front of draal lets nobody in without a session.\n";
  const e = errorOf(403, line);
  expect(e.code).toBe("http");
  expect(e.message).toBe(line.trim());
  expect(e.status).toBe(403);
});

test("an answer without text, or with a page of HTML, names its status", () => {
  expect(errorOf(502, "").message).toBe("draal answered with status 502.");
  expect(errorOf(502, "<html><body>Bad Gateway</body></html>").message).toBe("draal answered with status 502.");
});

test("JSON that is no refusal is shown as its text", () => {
  expect(errorOf(500, '{"oops":true}').message).toBe('{"oops":true}');
});

// ---- What changes something, and who the key is ---------------------

const ID = "b".repeat(64);

interface Asked {
  url: string;
  method: string;
  headers: Record<string, string>;
  body: unknown;
}

// answers stands in for fetch: it answers every request with the status
// and the body given, and notes what was asked.
function answers(status: number, body: string): Asked[] {
  const asked: Asked[] = [];
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    asked.push({
      url,
      method: init?.method ?? "GET",
      headers: Object.fromEntries(new Headers(init?.headers).entries()),
      body: typeof init?.body === "string" ? JSON.parse(init.body) : init?.body,
    });
    // A 204 has no body, and Response refuses one that is given.
    return new Response(status === 204 ? null : body, { status });
  });
  return asked;
}

afterEach(() => {
  vi.unstubAllGlobals();
  onSignedOut(undefined);
});

describe("who the key is", () => {
  test("the ID and the grants of the answer", async () => {
    answers(200, JSON.stringify({ id: ID, grants: [{ id: ID, role: "deployer", service: "web", at: "2026-10-13T09:00:00Z" }] }));
    const who = await me();
    expect(who.id).toBe(ID);
    expect(who.grants).toEqual([{ id: ID, role: "deployer", service: "web", at: "2026-10-13T09:00:00Z" }]);
  });

  test("a key with no grant has an empty list, whatever stands for it", async () => {
    answers(200, JSON.stringify({ id: ID, grants: null }));
    expect((await me()).grants).toEqual([]);
    answers(200, JSON.stringify({ id: ID }));
    expect((await me()).grants).toEqual([]);
  });
});

describe("the grants", () => {
  test("are asked for with GET, and are a list always", async () => {
    const asked = answers(200, JSON.stringify({ grants: null }));
    expect((await grants()).grants).toEqual([]);
    expect(asked[0]!.url).toBe("/api/grants");
    expect(asked[0]!.method).toBe("GET");
  });

  test("a grant is sent as JSON with POST, and 204 is its answer", async () => {
    const asked = answers(204, "");
    await grant({ id: ID, role: "deployer", service: "web", note: "ci" });
    expect(asked).toHaveLength(1);
    expect(asked[0]!.url).toBe("/api/grants");
    expect(asked[0]!.method).toBe("POST");
    expect(asked[0]!.headers["content-type"]).toBe("application/json");
    expect(asked[0]!.body).toEqual({ id: ID, role: "deployer", service: "web", note: "ci" });
  });

  test("a revocation is a DELETE that names the grant in its query", async () => {
    const asked = answers(204, "");
    await revoke({ id: ID, role: "deployer", service: "web" });
    await revoke({ id: ID, role: "zathras" });
    expect(asked.map((a) => `${a.method} ${a.url}`)).toEqual([
      `DELETE /api/grants?id=${ID}&role=deployer&service=web`,
      `DELETE /api/grants?id=${ID}&role=zathras&service=`,
    ]);
    expect(asked[0]!.body).toBeUndefined();
  });

  test("what draal refuses is its code and its sentence", async () => {
    answers(403, '{"error":{"code":"unauthorized","message":"bbbb is no admin and may not grant"}}');
    await expect(grant({ id: ID, role: "admin" })).rejects.toMatchObject({
      code: "unauthorized",
      message: "bbbb is no admin and may not grant",
      status: 403,
    });
    answers(404, '{"error":{"code":"not-found","message":"bbbb holds no such grant"}}');
    await expect(revoke({ id: ID, role: "admin" })).rejects.toMatchObject({ code: "not-found", status: 404 });
  });
});

describe("a cookie that is no longer good", () => {
  test("a 401 says so to whoever listens, and is an error of its own", async () => {
    const asks = [() => me(), () => grants(), () => grant({ id: ID, role: "admin" }), () => getJSON("/api/zathras/x/history")];
    for (const ask of asks) {
      let said = 0;
      onSignedOut(() => said++);
      answers(401, '{"error":{"code":"unauthorized","message":"open the link that great-machine web printed"}}');
      await expect(ask()).rejects.toMatchObject({ code: "signed-out", status: 401 });
      expect(said).toBe(1);
    }
  });

  test("a refusal of draal is no such thing", async () => {
    let said = 0;
    onSignedOut(() => said++);
    answers(403, '{"error":{"code":"unauthorized","message":"no admin"}}');
    await expect(grants()).rejects.toMatchObject({ code: "unauthorized", status: 403 });
    answers(503, '{"error":{"code":"unavailable","message":"draal cannot be reached"}}');
    await expect(me()).rejects.toMatchObject({ code: "unavailable", status: 503 });
    expect(said).toBe(0);
  });
});
