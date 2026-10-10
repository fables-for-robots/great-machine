import { describe, expect, test } from "vitest";
import { feeder, HELD, notice, pieces, TYPED } from "./feeder";

const decoder = new TextDecoder();

// A terminal of the test: what it was given, and the means to say that
// it has taken it.
function terminal() {
  const written: string[] = [];
  const waiting: (() => void)[] = [];
  return {
    written,
    sink: (data: Uint8Array, done: () => void) => {
      written.push(decoder.decode(data));
      waiting.push(done);
    },
    // The terminal has taken the piece it was given first.
    taken: () => waiting.shift()!(),
    busy: () => waiting.length,
  };
}

const bytes = (s: string) => new TextEncoder().encode(s);

describe("a terminal that keeps up", () => {
  test("is given every piece, one at a time", () => {
    const t = terminal();
    const f = feeder(t.sink);
    f.push(bytes("a"));
    expect(t.written).toEqual(["a"]);
    t.taken();
    f.push(bytes("b"));
    t.taken();
    expect(t.written).toEqual(["a", "b"]);
    expect(f.dropped()).toBe(0);
    expect(f.held()).toBe(0);
  });

  test("an empty piece is nothing", () => {
    const t = terminal();
    const f = feeder(t.sink);
    f.push(new Uint8Array(0));
    expect(t.written).toEqual([]);
  });
});

describe("a terminal that is behind", () => {
  test("is never given a second piece before it has taken the first", () => {
    const t = terminal();
    const f = feeder(t.sink);
    f.push(bytes("a"));
    f.push(bytes("b"));
    f.push(bytes("c"));
    expect(t.written).toEqual(["a"]);
    expect(t.busy()).toBe(1);
    expect(f.held()).toBe(2);
    // What was held meanwhile comes as one piece.
    t.taken();
    expect(t.written).toEqual(["a", "bc"]);
    expect(t.busy()).toBe(1);
    t.taken();
    expect(t.busy()).toBe(0);
    expect(f.held()).toBe(0);
  });

  // Review Focus 5: a shell that prints megabytes. What is held is
  // bounded, the oldest of it goes, and the terminal says how much.
  test("holds no more than its bound, drops the oldest, and says how much", () => {
    const t = terminal();
    const f = feeder(t.sink, 10);
    f.push(bytes("first"));
    for (const s of ["aaaa", "bbbb", "cccc", "dddd"]) {
      f.push(bytes(s));
      expect(f.held()).toBeLessThanOrEqual(10);
    }
    expect(f.dropped()).toBe(8);
    t.taken();
    expect(t.written.length).toBe(2);
    expect(t.written[1]).toBe(decoder.decode(notice(8)) + "ccccdddd");
    expect(t.written[1]).toContain("8 bytes not shown");
    // It is said once: what comes next is whole again.
    f.push(bytes("e"));
    t.taken();
    expect(t.written[2]).toBe("e");
  });

  test("a flood of megabytes costs the bound and no more", () => {
    const t = terminal();
    const f = feeder(t.sink);
    const piece = new Uint8Array(32 * 1024).fill(0x79);
    f.push(piece);
    for (let i = 0; i < 2000; i++) {
      f.push(piece);
      expect(f.held()).toBeLessThanOrEqual(HELD);
    }
    expect(f.dropped()).toBe((2000 - 32) * piece.length);
    expect(t.busy()).toBe(1);
  });

  test("a single piece over the bound is kept: the newest is never dropped", () => {
    const t = terminal();
    const f = feeder(t.sink, 4);
    f.push(bytes("a"));
    f.push(bytes("0123456789"));
    expect(f.held()).toBe(10);
    expect(f.dropped()).toBe(0);
    t.taken();
    expect(t.written[1]).toBe("0123456789");
  });
});

// Final review B4: the terminal is disposed when the session is closed,
// and may still say afterwards that it has taken a piece.
describe("a feeder that was stopped", () => {
  test("gives the sink nothing more: not what it held, and not what is pushed after", () => {
    const t = terminal();
    const f = feeder(t.sink);
    f.push(bytes("a"));
    f.push(bytes("b"));
    expect(t.written).toEqual(["a"]);
    expect(f.held()).toBe(1);
    f.stop();
    expect(f.held()).toBe(0);
    // The terminal says, after it was disposed, that it has taken "a".
    t.taken();
    expect(t.written).toEqual(["a"]);
    expect(t.busy()).toBe(0);
    f.push(bytes("c"));
    expect(t.written).toEqual(["a"]);
    expect(f.held()).toBe(0);
    expect(f.dropped()).toBe(0);
  });

  test("a notice of what was dropped is not written either, and stopping twice is stopping", () => {
    const t = terminal();
    const f = feeder(t.sink, 2);
    f.push(bytes("first"));
    f.push(bytes("aa"));
    f.push(bytes("bb"));
    expect(f.dropped()).toBe(2);
    f.stop();
    f.stop();
    t.taken();
    expect(t.written).toEqual(["first"]);
  });

  test("before it is stopped, what arrives after anything else is still written", () => {
    const t = terminal();
    const f = feeder(t.sink);
    f.push(bytes("a"));
    t.taken();
    f.push(bytes("late"));
    expect(t.written).toEqual(["a", "late"]);
  });
});

describe("what is typed", () => {
  test("a paste of 100 KiB with letters of several bytes goes out in pieces of at most 16 KiB", () => {
    const unit = "žluťoučký kůň 日本語 🙂 plain ";
    const text = unit.repeat(Math.ceil((100 * 1024) / new TextEncoder().encode(unit).length));
    const all = new TextEncoder().encode(text);
    expect(all.length).toBeGreaterThanOrEqual(100 * 1024);
    const parts = pieces(all);
    expect(TYPED).toBe(16 * 1024);
    expect(parts.length).toBe(Math.ceil(all.length / TYPED));
    let size = 0;
    for (const p of parts) {
      expect(p.length).toBeGreaterThan(0);
      expect(p.length).toBeLessThanOrEqual(TYPED);
      size += p.length;
    }
    // The bytes of the pieces, joined in order, are the UTF-8 of the text.
    const joined = new Uint8Array(size);
    let at = 0;
    for (const p of parts) {
      joined.set(p, at);
      at += p.length;
    }
    expect(joined).toEqual(all);
    expect(new TextDecoder().decode(joined)).toBe(text);
  });

  test("a key is one piece, nothing is none, and a size that is none is the usual one", () => {
    expect(pieces(new TextEncoder().encode("a"))).toEqual([new Uint8Array([0x61])]);
    expect(pieces(new Uint8Array(0))).toEqual([]);
    expect(pieces(new Uint8Array(5), 2).map((p) => p.length)).toEqual([2, 2, 1]);
    expect(pieces(new Uint8Array(5), 0).length).toBe(1);
    expect(pieces(new Uint8Array(5), NaN).length).toBe(1);
  });
});
