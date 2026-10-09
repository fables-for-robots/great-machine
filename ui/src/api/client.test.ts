import { expect, test } from "vitest";
import { errorOf } from "./client";

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
