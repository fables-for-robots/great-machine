import { describe, expect, test } from "vitest";
import { KEEP, LogBuffer } from "./logbuffer";

const T0 = Date.UTC(2026, 9, 9, 20, 0, 0);

// The message of a line i milliseconds after T0.
function line(i: number, text = `line ${i}`): string {
  return JSON.stringify({ at: new Date(T0 + i).toISOString(), stream: "stdout", text });
}

describe("the bound", () => {
  test("is the 400 lines of the ring", () => {
    expect(KEEP).toBe(400);
    expect(new LogBuffer().keep).toBe(400);
  });

  test("the oldest lines go", () => {
    const b = new LogBuffer();
    for (let i = 0; i < 1000; i++) {
      b.message(line(i));
      if (i % 7 === 0) {
        b.batch();
      }
    }
    const rows = b.batch()!.rows;
    expect(rows.length).toBe(400);
    expect(rows[0]!.text).toBe("line 600");
    expect(rows[399]!.text).toBe("line 999");
  });

  test("a flood between two frames is bounded while it waits", () => {
    const b = new LogBuffer();
    for (let i = 0; i < 100_000; i++) {
      b.message(line(i));
      expect(b.waiting()).toBeLessThanOrEqual(800);
    }
    const got = b.batch()!;
    expect(got.rows.length).toBe(400);
    expect(got.rows[399]!.text).toBe("line 99999");
    // The rows that stay are the newest, in their order.
    expect(got.rows.map((r) => r.text)).toEqual(Array.from({ length: 400 }, (_, i) => `line ${99_600 + i}`));
  });
});

describe("batches", () => {
  test("nothing taken, nothing to draw", () => {
    const b = new LogBuffer();
    expect(b.batch()).toBeUndefined();
    b.message(line(1));
    expect(b.batch()!.added).toBe(1);
    expect(b.batch()).toBeUndefined();
  });

  test("a batch is a new list, and a row that stays is the same row", () => {
    const b = new LogBuffer(3);
    b.message(line(1));
    b.message(line(2));
    const first = b.batch()!.rows;
    b.message(line(3));
    b.message(line(4));
    const second = b.batch()!;
    expect(second.added).toBe(2);
    expect(second.rows).not.toBe(first);
    expect(second.rows.map((r) => r.text)).toEqual(["line 2", "line 3", "line 4"]);
    expect(second.rows[0]).toBe(first[1]);
    expect(new Set(second.rows.map((r) => r.seq)).size).toBe(3);
  });
});

describe("what a message is", () => {
  test("a line with its time, its stream and its text", () => {
    const b = new LogBuffer();
    expect(b.message('{"at":"2026-10-09T22:00:00.5+02:00","stream":"stderr","text":"oops"}')).toBe("line");
    expect(b.batch()!.rows).toEqual([
      { seq: 0, kind: "line", at: "2026-10-09T22:00:00.5+02:00", stream: "stderr", text: "oops" },
    ]);
  });

  test("an empty line has no text key and is an empty line", () => {
    const b = new LogBuffer();
    b.message('{"at":"2026-10-09T22:00:00Z","stream":"stdout"}');
    const rows = b.batch()!.rows;
    expect(rows[0]!.kind).toBe("line");
    expect(rows[0]!.text).toBe("");
  });

  test("a lost marker is a row of its own", () => {
    const b = new LogBuffer();
    b.message(line(1));
    b.message('{"at":"2026-10-09T22:00:01Z","stream":"zathras","lost":1200}');
    b.message(line(2000));
    const rows = b.batch()!.rows;
    expect(rows.map((r) => r.kind)).toEqual(["line", "lost", "line"]);
    expect(rows[1]!.lost).toBe(1200);
  });

  test("the refusal that ends the socket is an error row", () => {
    const b = new LogBuffer();
    expect(b.message('{"error":{"code":"not-found","message":"zathras f1f8 is not connected"}}')).toBe("error");
    expect(b.batch()!.rows).toEqual([{ seq: 0, kind: "error", text: "zathras f1f8 is not connected" }]);
  });

  test("what is no JSON, and what is no text, is not taken", () => {
    const b = new LogBuffer();
    expect(b.message("{nonsense")).toBe("line");
    expect(b.message(new ArrayBuffer(2))).toBe("line");
    expect(b.message("null")).toBe("line");
    expect(b.batch()).toBeUndefined();
  });
});

describe("what the page says itself", () => {
  test("is not said twice in a row", () => {
    const b = new LogBuffer();
    b.say("error", "the zathras is gone");
    b.say("error", "the zathras is gone");
    expect(b.batch()!.rows.length).toBe(1);
    b.say("error", "the zathras is gone");
    expect(b.batch()).toBeUndefined();
    b.say("note", "followed again");
    b.say("error", "the zathras is gone");
    expect(b.batch()!.rows.map((r) => r.kind)).toEqual(["error", "note", "error"]);
  });
});

describe("a socket that is opened again", () => {
  // Review Focus 2: the zathras went and is back: the ring is sent once
  // more, and the lines the page has are not there twice.
  test("the lines of the ring that the page has are left out", () => {
    const b = new LogBuffer();
    b.message(line(1));
    b.message(line(2));
    b.message(line(3));
    b.batch();
    b.say("error", "the zathras is gone");
    b.resume();
    // The ring again, and what was written meanwhile.
    for (const i of [1, 2, 3, 4, 5]) {
      b.message(line(i));
    }
    const rows = b.batch()!.rows;
    expect(rows.map((r) => r.text)).toEqual(["line 1", "line 2", "line 3", "the zathras is gone", "line 4", "line 5"]);
  });

  test("without resume nothing is left out, and clear forgets everything", () => {
    const b = new LogBuffer();
    b.message(line(5));
    b.message(line(5));
    expect(b.batch()!.rows.length).toBe(2);
    b.resume();
    b.clear();
    b.message(line(1));
    expect(b.batch()!.rows.map((r) => r.text)).toEqual(["line 1"]);
  });
});
