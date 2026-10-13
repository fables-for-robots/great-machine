import { afterEach, expect, test, vi } from "vitest";
import { askMe, forget, me, meFailure, signedOut } from "./session";

const ID = "d".repeat(64);

function answers(status: number, body: string): void {
  vi.stubGlobal("fetch", async () => new Response(body, { status }));
}

afterEach(() => {
  vi.unstubAllGlobals();
  forget();
});

test("who the key is is not known before draal was asked", () => {
  expect(me()).toBeUndefined();
  expect(signedOut()).toBe(false);
});

test("what draal answers is who the key is", async () => {
  answers(200, JSON.stringify({ id: ID, grants: [{ id: ID, role: "admin", at: "0001-01-01T00:00:00Z", fixed: true }] }));
  await askMe();
  expect(me()?.id).toBe(ID);
  expect(me()?.grants).toHaveLength(1);
  expect(meFailure()).toBeUndefined();
  expect(signedOut()).toBe(false);
});

test("a draal that cannot be reached leaves who the key was, and why it was not asked anew is kept", async () => {
  answers(200, JSON.stringify({ id: ID, grants: [] }));
  await askMe();
  answers(503, '{"error":{"code":"unavailable","message":"draal cannot be reached"}}');
  await askMe();
  expect(me()?.id).toBe(ID);
  expect(meFailure()).toBe("draal cannot be reached");
  expect(signedOut()).toBe(false);
  // And it is forgotten when draal answers again.
  answers(200, JSON.stringify({ id: ID, grants: [] }));
  await askMe();
  expect(meFailure()).toBeUndefined();
});

test("a cookie that is no longer good signs the page out, and it stays so", async () => {
  answers(401, "open the link that great-machine web printed\n");
  await askMe();
  expect(signedOut()).toBe(true);
  // What answers afterwards is another command's: this page was opened
  // with a link that is no longer good.
  answers(200, JSON.stringify({ id: ID, grants: [] }));
  await askMe();
  expect(signedOut()).toBe(true);
});

test("of two questions the later one counts", async () => {
  let release: (r: Response) => void = () => {};
  vi.stubGlobal(
    "fetch",
    (_url: string, init?: RequestInit) =>
      new Promise<Response>((resolve, reject) => {
        release = resolve;
        init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
      }),
  );
  const first = askMe();
  answers(200, JSON.stringify({ id: ID, grants: [] }));
  await askMe();
  release(new Response(JSON.stringify({ id: "e".repeat(64), grants: [] }), { status: 200 }));
  await first;
  expect(me()?.id).toBe(ID);
});
