### Task 10: The log and the shell of a zathras

**Files:**
- Create: `ui/src/state/ansi.ts`, `ui/src/state/logbuffer.ts`, `ui/src/state/feeder.ts`
- Create: `ui/src/components/Log.tsx`, `ui/src/components/log.css`, `ui/src/components/Shell.tsx`, `ui/src/components/shell.css`
- Modify: `ui/src/views/Zathras.tsx` (the log and the shell after the figures)
- Test: `ui/src/state/ansi.test.ts`, `ui/src/state/logbuffer.test.ts`, `ui/src/state/feeder.test.ts`

**Interfaces:**
- Consumes:
  - Task 9: `ui/src/views/Zathras.tsx`, which hands its parts `params.id`, whether the fleet has the zathras, and when it connected.
  - Task 7: `socketURL`, `pause`; `connection`, `lives`; `isRefusal`, `parseTime`, `LogLine`; `clockMs`, `count`; the tokens `--term-*`.
  - Task 4: `@xterm/xterm` 6.0.0 and `@xterm/addon-fit` 0.11.0 in `ui/package.json`; no new dependency.
  - Task 2, the sockets:
    - `GET /api/zathras/{id}/logs?follow=1`: text messages, each a `LogLine` (`{"at":…,"stream":"stdout","text":…}`; an empty line has no `text`; `{"at":…,"stream":"zathras","lost":36}` says that so many lines were lost); the ring first, then every new line. The last message may be `{"error":{"code":…,"message":…}}`, after which the server closes with 1000.
    - `GET /api/zathras/{id}/shell?cols=&rows=&term=`: `cols` and `rows` 1 to 1000. The page sends binary messages (what is typed) of at most 16 KiB each (one over 64 KiB closes the socket with 1009) and text messages `{"resize":{"cols":120,"rows":40}}`; the server sends binary messages and, last, the text message `{"exit":N}` or an error message, and closes with 1000.
    - A socket that closes with another code than 1000 and a reason has a sentence fit to show as its reason; 1001 says that draal is ending; a refusal before the upgrade is a socket that fails, without a word (1006). The server drops a page that does not take a message within `WriteWait`. At most 256 sockets are open at draal at a time.
- Produces:
  - `ui/src/state/ansi.ts`: `Span`, `parse(line: string): Span[]`, `strip(line: string): string`, `colour256(n)`.
  - `ui/src/state/logbuffer.ts`: `KEEP` (400), `Entry`, `class LogBuffer` with `message(data)`, `line(l)`, `say(kind, text)`, `resume()`, `clear()`, `waiting()`, `batch()`.
  - `ui/src/state/feeder.ts`: `HELD` (1 MiB), `feeder(sink, limit?): Feeder`, `notice(bytes)`, `TYPED` (16 KiB), `pieces(data, size?)`.
  - `ui/src/components/Log.tsx` (`id`, `present`, `connected`) and `ui/src/components/Shell.tsx` (`id`, `present`), and with them the whole of the view of a zathras.

**The colours of a log.** A line is text and stays text. `parse` cuts
it into pieces at the SGR sequences (the 16 colours, the 256, the
colours by their parts, bold, dim, italic, underline, inverse) and
leaves every other escape sequence and every control character out;
the view draws each piece as a `span` whose content is a text node. A
colour of the palette is a class, any other is `rgb(r,g,b)` that the
parser made of three numbers it clamped, set through the element's
style object. Nothing a line contains reaches the document as markup or
as a style of its choosing; the tests pin both.

**Review Focus 5, the log.** What arrives is held in the buffer and
drawn once in an animation frame; the buffer keeps 400 rows and, while
no frame comes (a hidden tab), at most 800 waiting. Measured while this
plan was written, in Chrome on the machine it was written on, with the
demo's flood (1000 lines a second for 10 seconds, then 3000 at once)
and the log in view: the time between two frames stayed at 8 ms (the
display's 120 Hz) at the median and 9.3 ms at the 95th percentile, 16 ms
at the worst, with no long task, and never more than 400 rows in the
document. With the processor slowed down twentyfold by the developer
tools: 8 ms at the median, 83 ms at the 99th percentile, 476 ms at the
worst.

**Review Focus 5, the shell.** A WebSocket of a browser cannot be told
to wait, and a page that stopped reading would be dropped by draal
after `WriteWait`. So the page always reads; `feeder` hands xterm one
piece at a time (xterm's `write` says when it has taken a piece), holds
at most 1 MiB meanwhile, drops the oldest beyond that and has the
terminal say how many bytes it did not show. Of a flood the end is what
one wants. Measured the same way with `yes 'the quick brown fox jumps
over the lazy dog' | head -n 2000000` (88 MB) in the shell: it was
through in about 3.4 seconds, the time between two frames was 8 ms at
the median, 17 ms at the 95th percentile and 50 ms at the worst, and
there was no long task.

**Review Focus 2.** When the zathras goes, draal ends the socket of the
log and of the shell with an error message: the log shows it as a row,
the shell as the reason it ended. The log is asked for again only while
the fleet has the zathras, and when it is back the lines the page
already has are left out (`resume`). The test `a socket that is opened
again` pins the last; Step 17 looks at the whole.

Every socket is closed when its view is left: the log's by the cleanup
of its component, the shell's likewise and by `End the shell`. Neither
is ever open twice for one view.

- [ ] **Step 1: Write the failing test of the colours of a line**

`ui/src/state/ansi.test.ts`:

```ts
import { describe, expect, test } from "vitest";
import { colour256, parse, strip } from "./ansi";

const ESC = "\x1b";

describe("a line without colours", () => {
  test("is one piece, as it is", () => {
    expect(parse("listening on :8080")).toEqual([{ text: "listening on :8080" }]);
    expect(parse("")).toEqual([]);
  });

  test("markup in it is text and stays text", () => {
    const line = '<img src=x onerror="alert(1)"><b>bold</b>&amp;';
    expect(parse(line)).toEqual([{ text: line }]);
    expect(strip(line)).toBe(line);
  });

  test("keeps its tabs and its letters of every script", () => {
    expect(parse("a\tb – ž 日本 🙂")).toEqual([{ text: "a\tb – ž 日本 🙂" }]);
  });
});

describe("colours", () => {
  test("the eight colours, the bright ones, and the reset", () => {
    expect(parse(`${ESC}[31mERROR${ESC}[0m failed`)).toEqual([{ text: "ERROR", fg: 1 }, { text: " failed" }]);
    expect(parse(`${ESC}[92mok${ESC}[m.`)).toEqual([{ text: "ok", fg: 10 }, { text: "." }]);
    expect(parse(`${ESC}[44;97mx`)).toEqual([{ text: "x", fg: 15, bg: 4 }]);
    expect(parse(`${ESC}[103mx${ESC}[49my`)).toEqual([{ text: "x", bg: 11 }, { text: "y" }]);
  });

  test("bold, dim, italic, underline, and their ends", () => {
    expect(parse(`${ESC}[1;4mA${ESC}[22mB${ESC}[24mC`)).toEqual([
      { text: "A", bold: true, underline: true },
      { text: "B", underline: true },
      { text: "C" },
    ]);
    expect(parse(`${ESC}[2;3mA${ESC}[23mB`)).toEqual([
      { text: "A", dim: true, italic: true },
      { text: "B", dim: true },
    ]);
  });

  test("a look goes on until it is changed", () => {
    expect(parse(`${ESC}[33ma${ESC}[1mb${ESC}[39mc`)).toEqual([
      { text: "a", fg: 3 },
      { text: "b", fg: 3, bold: true },
      { text: "c", bold: true },
    ]);
  });

  test("the 256 colours and the colours by their parts", () => {
    expect(parse(`${ESC}[38;5;9mx`)).toEqual([{ text: "x", fg: 9 }]);
    expect(parse(`${ESC}[38;5;196mx`)).toEqual([{ text: "x", fg: "rgb(255,0,0)" }]);
    expect(parse(`${ESC}[48;5;244mx`)).toEqual([{ text: "x", bg: "rgb(128,128,128)" }]);
    expect(parse(`${ESC}[38;2;10;20;30;1mx`)).toEqual([{ text: "x", fg: "rgb(10,20,30)", bold: true }]);
    expect(parse(`${ESC}[38;2;999;0;30mx`)).toEqual([{ text: "x", fg: "rgb(255,0,30)" }]);
    expect(colour256(16)).toBe("rgb(0,0,0)");
    expect(colour256(231)).toBe("rgb(255,255,255)");
    expect(colour256(232)).toBe("rgb(8,8,8)");
    expect(colour256(255)).toBe("rgb(238,238,238)");
    expect(colour256(256)).toBeUndefined();
    expect(colour256(-1)).toBeUndefined();
    expect(colour256(1.5)).toBeUndefined();
  });

  test("a colour is a number of the palette or rgb() of numbers, and nothing a line could choose", () => {
    const lines = [
      `${ESC}[38;5;300mx`,
      `${ESC}[38;2;1mx`,
      `${ESC}[38;9;1mx`,
      `${ESC}[38mx`,
      `${ESC}[38;2;url(x);0;0mx`,
      `${ESC}[999999999999999999999mx`,
    ];
    for (const line of lines) {
      for (const s of parse(line)) {
        for (const c of [s.fg, s.bg]) {
          if (typeof c === "string") {
            expect(c).toMatch(/^rgb\(\d{1,3},\d{1,3},\d{1,3}\)$/);
          } else if (c !== undefined) {
            expect(c >= 0 && c <= 15).toBe(true);
          }
        }
        // Whatever was no sequence is text, and has no escape in it.
        expect(s.text).not.toContain(ESC);
      }
    }
  });

  test("inverse swaps the colours", () => {
    expect(parse(`${ESC}[7;31mx${ESC}[27my`)).toEqual([
      { text: "x", fg: "var(--term-bg)", bg: 1 },
      { text: "y", fg: 1 },
    ]);
  });
});

describe("what is taken out", () => {
  test("the sequences that move the cursor and clear", () => {
    expect(strip(`${ESC}[2K${ESC}[1Gprogress 50%${ESC}[?25l`)).toBe("progress 50%");
    expect(parse(`a${ESC}[10;20Hb`)).toEqual([{ text: "a" }, { text: "b" }]);
  });

  test("a title and a link", () => {
    expect(strip(`${ESC}]0;my title\x07text`)).toBe("text");
    expect(strip(`${ESC}]8;;https://example.com${ESC}\\link${ESC}]8;;${ESC}\\`)).toBe("link");
  });

  test("control characters, a carriage return among them", () => {
    expect(strip("a\rb\x00c\x07d\x7fe\x9bf")).toBe("abcdef");
  });

  test("a sequence that is cut off at the end of the line", () => {
    expect(strip(`text${ESC}`)).toBe("text");
    expect(strip(`text${ESC}[`)).toBe("text[");
    expect(strip(`text${ESC}[31`)).toBe("text[31");
  });

  test("a line of 16 KiB of sequences does not take long", () => {
    const line = `${ESC}[31mx${ESC}[0m`.repeat(1600);
    const start = performance.now();
    const spans = parse(line);
    expect(spans.length).toBe(1600);
    expect(performance.now() - start).toBeLessThan(200);
  });
});
```

- [ ] **Step 2: Run the test to see it fail**

Run: `nix develop .#bare --command npm --prefix ui test -- src/state/ansi.test.ts`
Expected: FAIL — `Error: Cannot find module './ansi'` (the test file fails before any test runs: `Test Files  1 failed (1)`)

- [ ] **Step 3: Write the colours of a line**

`ui/src/state/ansi.ts`:

```ts
// The colours of a line of a log. A service writes escape sequences
// into its output to colour it; the page makes of a line a list of
// pieces of text, each with its colours, and draws each as a span whose
// content is text. Nothing of a line is ever markup: every other escape
// sequence, and every control character, is left out.

/** A piece of a line with one look. */
export interface Span {
  text: string;
  /** The colour of the text: 0 to 15 for the colours of the terminal's
   *  palette, a CSS colour for any other, undefined for the default. */
  fg?: number | string;
  /** The colour behind the text, likewise. */
  bg?: number | string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
}

// The look that the sequences seen so far ask for.
interface Look {
  fg?: number | string;
  bg?: number | string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
  inverse?: boolean;
}

// The steps of the colour cube of the 256 colours.
const CUBE = [0, 95, 135, 175, 215, 255] as const;

/** One of the 256 colours: 0 to 15 are of the palette, the others are
 *  what they are everywhere. Undefined for what is none. */
export function colour256(n: number): number | string | undefined {
  if (!Number.isInteger(n) || n < 0 || n > 255) {
    return undefined;
  }
  if (n < 16) {
    return n;
  }
  if (n < 232) {
    const c = n - 16;
    return `rgb(${CUBE[Math.floor(c / 36)]},${CUBE[Math.floor(c / 6) % 6]},${CUBE[c % 6]})`;
  }
  const grey = 8 + (n - 232) * 10;
  return `rgb(${grey},${grey},${grey})`;
}

// A part of a colour given as three numbers: 0 to 255.
function part(n: number | undefined): number {
  return n !== undefined && Number.isFinite(n) ? Math.min(255, Math.max(0, Math.floor(n))) : 0;
}

// Applies the numbers of one SGR sequence to a look.
function apply(look: Look, codes: number[]): Look {
  let l = { ...look };
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i]!;
    if (c === 0) {
      l = {};
    } else if (c === 1) {
      l.bold = true;
    } else if (c === 2) {
      l.dim = true;
    } else if (c === 3) {
      l.italic = true;
    } else if (c === 4) {
      l.underline = true;
    } else if (c === 7) {
      l.inverse = true;
    } else if (c === 22) {
      l.bold = l.dim = undefined;
    } else if (c === 23) {
      l.italic = undefined;
    } else if (c === 24) {
      l.underline = undefined;
    } else if (c === 27) {
      l.inverse = undefined;
    } else if (c >= 30 && c <= 37) {
      l.fg = c - 30;
    } else if (c === 39) {
      l.fg = undefined;
    } else if (c >= 40 && c <= 47) {
      l.bg = c - 40;
    } else if (c === 49) {
      l.bg = undefined;
    } else if (c >= 90 && c <= 97) {
      l.fg = c - 90 + 8;
    } else if (c >= 100 && c <= 107) {
      l.bg = c - 100 + 8;
    } else if (c === 38 || c === 48) {
      // 38;5;N and 38;2;R;G;B: a colour by its number or its parts.
      let colour: number | string | undefined;
      if (codes[i + 1] === 5) {
        colour = colour256(codes[i + 2] ?? -1);
        i += 2;
      } else if (codes[i + 1] === 2) {
        colour = `rgb(${part(codes[i + 2])},${part(codes[i + 3])},${part(codes[i + 4])})`;
        i += 4;
      } else {
        // Not a form the page knows: the rest of the sequence is not
        // guessed at.
        break;
      }
      if (c === 38) {
        l.fg = colour;
      } else {
        l.bg = colour;
      }
    }
    // Any other number (blinking, fonts, …) changes nothing here.
  }
  return l;
}

// The span of a text with a look. Inverse swaps the two colours; the
// defaults stand in for the one that is not set.
function span(text: string, l: Look): Span {
  const s: Span = { text };
  let fg = l.fg;
  let bg = l.bg;
  if (l.inverse) {
    fg = l.bg ?? "var(--term-bg)";
    bg = l.fg ?? "var(--term-fg)";
  }
  if (fg !== undefined) {
    s.fg = fg;
  }
  if (bg !== undefined) {
    s.bg = bg;
  }
  if (l.bold) {
    s.bold = true;
  }
  if (l.dim) {
    s.dim = true;
  }
  if (l.italic) {
    s.italic = true;
  }
  if (l.underline) {
    s.underline = true;
  }
  return s;
}

// What is taken out of a line, each alternative one thing:
//   a CSI sequence (ESC [ parameters intermediates final), of which
//   only those that end in m are read;
//   an OSC sequence (ESC ] … BEL or ESC \), a title for instance;
//   any other escape sequence of two characters;
//   a control character, but for the tab.
// eslint-disable-next-line no-control-regex
const TAKEN = /\x1b\[([0-9;:?<=>]*)[ -/]*([@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[@-Z\\^_]?|[\x00-\x08\x0a-\x1f\x7f\x80-\x9f]/g;

/** The pieces of a line. The text of the pieces, put together, is the
 *  line without its escape sequences and control characters. */
export function parse(line: string): Span[] {
  const out: Span[] = [];
  let look: Look = {};
  let from = 0;
  const text = (to: number) => {
    if (to > from) {
      out.push(span(line.slice(from, to), look));
    }
  };
  TAKEN.lastIndex = 0;
  for (let m = TAKEN.exec(line); m !== null; m = TAKEN.exec(line)) {
    text(m.index);
    from = m.index + m[0].length;
    if (m[2] === "m" && m[1] !== undefined && /^[0-9;:]*$/.test(m[1])) {
      // "ESC[m" is "ESC[0m"; an empty number between two semicolons is 0.
      const codes = m[1].split(/[;:]/).map((n) => (n === "" ? 0 : Number(n)));
      look = apply(look, codes);
    }
    if (m[0].length === 0) {
      // Cannot be, by the expression; a loop that does not move must not be.
      TAKEN.lastIndex++;
    }
  }
  text(line.length);
  return out;
}

/** A line without its escape sequences and control characters. */
export function strip(line: string): string {
  return line.replace(TAKEN, "");
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command npm --prefix ui test`
Expected: PASS — `Test Files  8 passed (8)`, and `nix develop .#bare --command npm --prefix ui run check` prints no error.

- [ ] **Step 5: Commit**

```bash
git add ui/src/state/ansi.ts ui/src/state/ansi.test.ts
git commit -m "UI: the colours of a line of a log, as spans of text"
```

- [ ] **Step 6: Write the failing test of the buffer of the log (Review Focus 2 and 5)**

`ui/src/state/logbuffer.test.ts`:

```ts
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
```

- [ ] **Step 7: Run the test to see it fail**

Run: `nix develop .#bare --command npm --prefix ui test -- src/state/logbuffer.test.ts`
Expected: FAIL — `Error: Cannot find module './logbuffer'` (the test file fails before any test runs: `Test Files  1 failed (1)`)

- [ ] **Step 8: Write the buffer of the log (Review Focus 2 and 5)**

`ui/src/state/logbuffer.ts`:

```ts
// The lines of a log as the page keeps them: at most KEEP of them, the
// oldest going first, taken in batches so that a flood costs one
// drawing a frame and not one a line.

import { isRefusal, type LogLine, parseTime } from "../api/types";

/** How many lines the page keeps: as many as the ring of a zathras. */
export const KEEP = 400;

/** A row of the log as it is drawn. */
export interface Entry {
  /** A number that no other row of this buffer has: rows are told apart
   *  by it. */
  seq: number;
  /** line: a line of the service or of zathras. lost: so many lines were
   *  lost before the next. note: something the page says. error: why
   *  the log ended. */
  kind: "line" | "lost" | "note" | "error";
  /** Of a line: its time and its stream. */
  at?: string;
  stream?: string;
  /** Of a line, a note and an error. */
  text: string;
  /** Of a lost marker: how many lines. */
  lost?: number;
}

/** The log of one zathras. */
export class LogBuffer {
  /** How many rows are kept. */
  readonly keep: number;
  private entries: Entry[] = [];
  private pending: Entry[] = [];
  private seq = 0;
  // The time of the newest line taken, and the time before which the
  // lines of a socket are a repetition of what is there.
  private newest = -Infinity;
  private repeatedUntil = -Infinity;

  constructor(keep = KEEP) {
    this.keep = keep;
  }

  /** Takes a message of the log's socket, as it was received. It returns
   *  "error" when the message is the refusal that ends the socket, and
   *  "line" otherwise. */
  message(data: unknown): "line" | "error" {
    if (typeof data !== "string") {
      return "line";
    }
    let v: unknown;
    try {
      v = JSON.parse(data);
    } catch {
      return "line";
    }
    if (isRefusal(v)) {
      this.say("error", v.error.message);
      return "error";
    }
    if (typeof v === "object" && v !== null) {
      this.line(v as LogLine);
    }
    return "line";
  }

  /** Takes a line or a lost marker. */
  line(l: LogLine): void {
    const t = parseTime(l.at);
    // After resume, the ring is sent once more: what the page has is
    // not taken twice.
    if (t !== undefined && t <= this.repeatedUntil) {
      return;
    }
    if (t !== undefined && t > this.newest) {
      this.newest = t;
    }
    const lost = typeof l.lost === "number" && l.lost > 0 ? l.lost : 0;
    if (lost > 0) {
      this.add({ seq: this.seq++, kind: "lost", at: l.at, text: "", lost });
      return;
    }
    this.add({
      seq: this.seq++,
      kind: "line",
      at: l.at,
      stream: typeof l.stream === "string" ? l.stream : "",
      text: typeof l.text === "string" ? l.text : "",
    });
  }

  /** Adds a row of the page's own. One that says what the row before it
   *  said is not added. */
  say(kind: "note" | "error", text: string): void {
    const last = this.pending[this.pending.length - 1] ?? this.entries[this.entries.length - 1];
    if (last && last.kind === kind && last.text === text) {
      return;
    }
    this.add({ seq: this.seq++, kind, text });
  }

  /** Says that the socket is opened again: the lines that come first are
   *  the ring, and those the page has from before are left out. */
  resume(): void {
    this.repeatedUntil = this.newest;
  }

  /** Empties the buffer, for another zathras. */
  clear(): void {
    this.entries = [];
    this.pending = [];
    this.newest = -Infinity;
    this.repeatedUntil = -Infinity;
  }

  /** How many rows wait for the next batch. */
  waiting(): number {
    return this.pending.length;
  }

  /** Moves what was taken since the last batch into the rows, and drops
   *  the oldest rows over the bound. It returns the rows as a new list
   *  and how many were added, or undefined when nothing was taken: the
   *  caller draws only then. */
  batch(): { rows: Entry[]; added: number } | undefined {
    if (this.pending.length === 0) {
      return undefined;
    }
    const added = this.pending.length;
    const all = this.entries.concat(this.pending);
    this.pending = [];
    this.entries = all.length > this.keep ? all.slice(all.length - this.keep) : all;
    return { rows: this.entries, added };
  }

  private add(e: Entry): void {
    this.pending.push(e);
    // No frame comes while the tab is hidden: what waits is bounded too.
    if (this.pending.length > 2 * this.keep) {
      this.pending = this.pending.slice(this.pending.length - this.keep);
    }
  }
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `nix develop .#bare --command npm --prefix ui test`
Expected: PASS — `Test Files  9 passed (9)`, and `nix develop .#bare --command npm --prefix ui run check` prints no error.

- [ ] **Step 10: The log**

`ui/src/components/Log.tsx`:

```tsx
import { createEffect, createMemo, createSignal, For, Match, on, onCleanup, Show, Switch } from "solid-js";
import { pause, socketURL } from "../api/reconnect";
import { clockMs, count } from "../format";
import { parse, type Span } from "../state/ansi";
import { connection, lives } from "../state/fleet";
import { type Entry, KEEP, LogBuffer } from "../state/logbuffer";
import "./log.css";

// How near the end of the log counts as being at it, in pixels.
const NEAR = 24;

// The colour of a piece: one of the palette is a class, any other a
// colour that the parser made of numbers.
function paint(s: Span) {
  return {
    class:
      [
        typeof s.fg === "number" ? `fg${s.fg}` : "",
        typeof s.bg === "number" ? `bg${s.bg}` : "",
        s.bold ? "b" : "",
        s.dim ? "d" : "",
        s.italic ? "i" : "",
        s.underline ? "u" : "",
      ]
        .filter((c) => c !== "")
        .join(" ") || undefined,
    style:
      typeof s.fg === "string" || typeof s.bg === "string"
        ? {
            color: typeof s.fg === "string" ? s.fg : undefined,
            "background-color": typeof s.bg === "string" ? s.bg : undefined,
          }
        : undefined,
  };
}

// A row of the log. The text of a line is text: its colours are spans
// around pieces of it, and nothing of it is markup.
function Row(props: { entry: Entry }) {
  const e = props.entry;
  switch (e.kind) {
    case "lost":
      return (
        <div class="log-row log-lost" title="The log grew faster than it was read: these lines were dropped on the way.">
          … {count(e.lost)} {e.lost === 1 ? "line" : "lines"} lost …
        </div>
      );
    case "note":
      return <div class="log-row log-note">{e.text}</div>;
    case "error":
      return <div class="log-row log-error">{e.text}</div>;
    default:
      return (
        <div class="log-row" data-stream={e.stream}>
          <span class="log-at">{clockMs(e.at)}</span>
          <span class="log-stream">{e.stream}</span>
          <span class="log-text">
            <For each={parse(e.text)}>
              {(s) => {
                const p = paint(s);
                return p.class || p.style ? (
                  <span class={p.class} style={p.style}>
                    {s.text}
                  </span>
                ) : (
                  s.text
                );
              }}
            </For>
          </span>
        </div>
      );
  }
}

// The log of a zathras: its ring, and then every line as it is written.
//
// The lines that arrive are held and drawn once a frame, and at most
// KEEP rows are kept, so a service that writes thousands of lines a
// second costs one drawing a frame. The view stays at the end unless the
// reader has scrolled up.
//
// Review Focus 2: when the zathras goes, the socket's last message says
// so and is shown as a row; the page asks again only when the fleet has
// the zathras (present), and then leaves out the lines it already has.
export default function Log(props: {
  id: string;
  /** Whether the fleet has the zathras now. */
  present: boolean;
  /** When it connected: another time is another connection. */
  connected: string | undefined;
}) {
  const buffer = new LogBuffer();
  const [rows, setRows] = createSignal<Entry[]>([]);
  const [state, setState] = createSignal<"idle" | "connecting" | "following" | "ended">("idle");
  const [following, setFollowing] = createSignal(true);
  const [unseen, setUnseen] = createSignal(0);
  let box: HTMLDivElement | undefined;
  let frame = 0;

  const toEnd = () => {
    if (box) {
      box.scrollTop = box.scrollHeight;
    }
  };

  // One drawing a frame, whatever arrived in it.
  const draw = () => {
    frame = 0;
    const got = buffer.batch();
    if (!got) {
      return;
    }
    setRows(got.rows);
    if (following()) {
      toEnd();
    } else {
      setUnseen((n) => n + got.added);
    }
  };
  const schedule = () => {
    if (frame === 0) {
      frame = requestAnimationFrame(draw);
    }
  };
  onCleanup(() => cancelAnimationFrame(frame));

  const scrolled = () => {
    if (!box) {
      return;
    }
    const atEnd = box.scrollHeight - box.scrollTop - box.clientHeight < NEAR;
    setFollowing(atEnd);
    if (atEnd) {
      setUnseen(0);
    }
  };

  // The fleet changes every few seconds; the socket is opened anew only
  // when one of these does.
  const id = createMemo(() => props.id);
  const present = createMemo(() => props.present);
  const connected = createMemo(() => props.connected);

  // The socket of the log, the pause before the next one, and how many
  // were opened in vain.
  let socket: WebSocket | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let attempt = 0;

  const open = () => {
    let refused = false;
    timer = undefined;
    setState("connecting");
    // The ring comes first on every socket: what the page has of it is
    // left out.
    buffer.resume();
    const s = new WebSocket(socketURL(`/api/zathras/${id()}/logs?follow=1`));
    socket = s;
    s.onmessage = (event) => {
      if (socket !== s) {
        return;
      }
      if (buffer.message(event.data) === "error") {
        // The last message of the socket: why the log ends. It is a row.
        refused = true;
      } else {
        attempt = 0;
        setState("following");
      }
      schedule();
    };
    s.onclose = (event) => {
      if (socket !== s) {
        return;
      }
      socket = undefined;
      if (!refused && event.code !== 1000 && event.reason) {
        // draal says why it closed, in a sentence.
        buffer.say("error", event.reason);
        schedule();
      } else if (!refused) {
        // An end without a word: draal went, or dropped a page that read
        // too slowly; or the zathras went and the page was told by the
        // fleet first.
        buffer.say("error", present() ? "The connection of the log ended." : "The zathras is not connected any more.");
        schedule();
      }
      setState("ended");
      // While the fleet has the zathras the log is asked for again, with
      // the pauses of every connection of the page. When it has not,
      // nothing is asked until it is back; and while draal itself does
      // not answer, the connection of the fleet is the one that tries.
      if (present() && connection() !== "lost") {
        timer = setTimeout(open, pause(attempt++, Math.random()));
      }
    };
  };

  const shut = () => {
    clearTimeout(timer);
    timer = undefined;
    const s = socket;
    socket = undefined;
    s?.close();
  };
  onCleanup(shut);

  // Another zathras: nothing of the one before stays.
  createEffect(
    on(id, () => {
      shut();
      attempt = 0;
      buffer.clear();
      setRows([]);
      setState("idle");
      setFollowing(true);
      setUnseen(0);
    }),
  );

  // The zathras is there, is there again, or draal is back: the log is
  // asked for, unless a socket is open. The zathras is gone: nothing
  // more is asked; a socket that is open is left to end by itself, as
  // its last message says why.
  createEffect(
    on([id, present, connected, lives], ([, present]) => {
      clearTimeout(timer);
      timer = undefined;
      if (!present) {
        if (!socket) {
          setState((s) => (s === "idle" ? "idle" : "ended"));
        }
        return;
      }
      if (!socket) {
        attempt = 0;
        open();
      }
    }),
  );

  return (
    <section class="section">
      <div class="log-head">
        <h2>Log</h2>
        <span class="muted">
          <Switch>
            <Match when={state() === "following"}>followed live</Match>
            <Match when={state() === "connecting"}>asking draal…</Match>
            <Match when={state() === "ended" && props.present}>ended; asking again</Match>
            <Match when={state() === "ended"}>ended: the zathras is not connected</Match>
            <Match when={true}>not asked for: the zathras is not connected</Match>
          </Switch>
          , the last {KEEP} lines are kept
        </span>
      </div>
      <div class="log-frame">
        <div class="log" ref={box} onScroll={scrolled} tabindex="0" role="log" aria-label="The log of the zathras">
          <Show when={rows().length > 0} fallback={<div class="log-row log-note">No line yet.</div>}>
            <For each={rows()}>{(e) => <Row entry={e} />}</For>
          </Show>
        </div>
        <Show when={!following()}>
          <button
            type="button"
            class="log-end"
            onClick={() => {
              toEnd();
              setFollowing(true);
              setUnseen(0);
            }}
          >
            Go to the end
            <Show when={unseen() > 0}>
              {" "}
              ({count(unseen())} new)
            </Show>
          </button>
        </Show>
      </div>
    </section>
  );
}
```

`ui/src/components/log.css`:

```css
/* The log of a zathras: a box of rows in the colours of a terminal. */

.log-head {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 2px var(--ffr-space-4);
  margin-bottom: var(--ffr-space-3);
}

.log-head .muted {
  font-size: 13px;
}

.log-frame {
  position: relative;
}

.log {
  height: 440px;
  overflow: auto;
  overflow-anchor: auto;
  border: 1px solid var(--border);
  border-radius: var(--ffr-radius-md);
  background: var(--term-bg);
  color: var(--term-fg);
  font-family: var(--ffr-font-mono);
  font-size: 12.5px;
  line-height: 1.5;
  padding: var(--ffr-space-2) 0;
}

.log-row {
  display: grid;
  grid-template-columns: 12.5ch 7.5ch minmax(0, 1fr);
  gap: 0 var(--ffr-space-2);
  padding: 0 var(--ffr-space-3);
}

.log-row:hover {
  background: var(--surface-strong);
}

.log-at {
  color: var(--term-bright-black);
}

.log-stream {
  color: var(--term-bright-black);
}

/* What the service wrote to its standard error, and what zathras says
   itself, are told apart by the name of the stream and by a line at
   the side, not by a colour of the text: that is the service's own. */
.log-row[data-stream="stderr"] {
  box-shadow: inset 3px 0 0 var(--warn);
}

.log-row[data-stream="stderr"] .log-stream {
  color: var(--warn-text);
}

.log-row[data-stream="zathras"] {
  box-shadow: inset 3px 0 0 var(--busy);
  background: var(--busy-wash);
}

.log-row[data-stream="zathras"] .log-stream {
  color: var(--busy-text);
}

/* A line may be 16 KiB without a space: it breaks anywhere, and keeps
   the spaces it has. */
.log-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  min-width: 0;
  tab-size: 8;
}

.log-lost,
.log-note,
.log-error {
  display: block;
  text-align: center;
  font-family: var(--ffr-font-body);
  font-size: 12.5px;
  padding-top: 2px;
  padding-bottom: 2px;
}

.log-lost {
  color: var(--warn-text);
  background: var(--warn-wash);
}

.log-note {
  color: var(--fg-muted);
}

.log-error {
  color: var(--bad-text);
  background: var(--bad-wash);
  overflow-wrap: anywhere;
}

.log-end {
  position: absolute;
  right: var(--ffr-space-4);
  bottom: var(--ffr-space-3);
  font-family: var(--ffr-font-display);
  font-weight: 700;
  font-size: 12px;
  padding: 6px 12px;
  border-radius: var(--ffr-radius-sm);
  border: 1.5px solid var(--fg);
  background: var(--fg);
  color: var(--bg);
  cursor: pointer;
}

/* The sixteen colours of the palette, for the pieces of a line. */
.log .fg0 { color: var(--term-black); }
.log .fg1 { color: var(--term-red); }
.log .fg2 { color: var(--term-green); }
.log .fg3 { color: var(--term-yellow); }
.log .fg4 { color: var(--term-blue); }
.log .fg5 { color: var(--term-magenta); }
.log .fg6 { color: var(--term-cyan); }
.log .fg7 { color: var(--term-white); }
.log .fg8 { color: var(--term-bright-black); }
.log .fg9 { color: var(--term-bright-red); }
.log .fg10 { color: var(--term-bright-green); }
.log .fg11 { color: var(--term-bright-yellow); }
.log .fg12 { color: var(--term-bright-blue); }
.log .fg13 { color: var(--term-bright-magenta); }
.log .fg14 { color: var(--term-bright-cyan); }
.log .fg15 { color: var(--term-bright-white); }
.log .bg0 { background-color: var(--term-black); }
.log .bg1 { background-color: var(--term-red); }
.log .bg2 { background-color: var(--term-green); }
.log .bg3 { background-color: var(--term-yellow); }
.log .bg4 { background-color: var(--term-blue); }
.log .bg5 { background-color: var(--term-magenta); }
.log .bg6 { background-color: var(--term-cyan); }
.log .bg7 { background-color: var(--term-white); }
.log .bg8 { background-color: var(--term-bright-black); }
.log .bg9 { background-color: var(--term-bright-red); }
.log .bg10 { background-color: var(--term-bright-green); }
.log .bg11 { background-color: var(--term-bright-yellow); }
.log .bg12 { background-color: var(--term-bright-blue); }
.log .bg13 { background-color: var(--term-bright-magenta); }
.log .bg14 { background-color: var(--term-bright-cyan); }
.log .bg15 { background-color: var(--term-bright-white); }
.log .b { font-weight: 700; }
.log .d { opacity: 0.65; }
.log .i { font-style: italic; }
.log .u { text-decoration: underline; }

@media (max-width: 640px) {
  .log {
    height: 360px;
    font-size: 12px;
  }

  /* The time and the stream above the text, which gets the width. */
  .log-row {
    grid-template-columns: auto minmax(0, 1fr);
  }

  .log-text {
    grid-column: 1 / -1;
  }
}
```

In `ui/src/views/Zathras.tsx`, import the log after the figures:

```tsx
import Figures from "../components/Figures";
import Log from "../components/Log";
import Notice from "../components/Notice";
```

and put it after them:

```tsx
            <Figures id={params.id} present={live() !== undefined} connected={live()?.connected} sample={live()?.state.sample} />
            <Log id={params.id} present={live() !== undefined} connected={live()?.connected} />
          </>
```

Run: `nix develop .#bare --command npm --prefix ui run check`, `nix develop .#bare --command npm --prefix ui test` and `nix develop .#bare --command npm --prefix ui run build`
Expected: no error; `Test Files  9 passed (9)`; `✓ built`.

```bash
git add ui/src/state/logbuffer.ts ui/src/state/logbuffer.test.ts ui/src/components/Log.tsx ui/src/components/log.css ui/src/views/Zathras.tsx
git commit -m "UI: the log of a zathras, live, bounded, drawn once a frame"
```

- [ ] **Step 11: Write the failing test of what stands between the shell and the terminal (Review Focus 5)**

`ui/src/state/feeder.test.ts`:

```ts
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
```

- [ ] **Step 12: Run the test to see it fail**

Run: `nix develop .#bare --command npm --prefix ui test -- src/state/feeder.test.ts`
Expected: FAIL — `Error: Cannot find module './feeder'` (the test file fails before any test runs: `Test Files  1 failed (1)`)

- [ ] **Step 13: Write what stands between the shell and the terminal (Review Focus 5)**

`ui/src/state/feeder.ts`:

```ts
// What stands between the socket of a shell and the terminal that draws
// its output.
//
// A terminal draws slower than a shell prints: `yes`, or `cat` of a
// large file, gives megabytes a second. A WebSocket of a browser cannot
// be told to wait, and the page must not make it wait either: draal
// drops a socket that does not take a message within its WriteWait. So
// the page always reads, hands the terminal one piece at a time (the
// terminal says when it has taken a piece), and holds what arrives
// meanwhile, up to a bound. Over the bound the oldest of what is held
// is dropped, and the terminal is told how much: of a flood, the end is
// what one wants to see.

/** How many bytes are held for a terminal that is behind: 1 MiB. */
export const HELD = 1024 * 1024;

/** What a feeder gives its bytes to: xterm's write. It calls done when
 *  it has taken them. */
export type Sink = (data: Uint8Array, done: () => void) => void;

/** A feeder: see feeder. */
export interface Feeder {
  /** Takes bytes of the shell. */
  push(data: Uint8Array): void;
  /** How many bytes were dropped so far. */
  dropped(): number;
  /** How many bytes are held now. */
  held(): number;
}

const encoder = new TextEncoder();

/** What the terminal shows where bytes were dropped: on a line of its
 *  own, with the look reset before and after, as what was dropped may
 *  have ended in the middle of a colour. */
export function notice(bytes: number): Uint8Array {
  return encoder.encode(`\x1b[0m\r\n\x1b[2m[… ${bytes} bytes not shown: the shell printed faster than the page draws …]\x1b[0m\r\n`);
}

/** Makes a feeder of a sink. */
export function feeder(sink: Sink, limit = HELD): Feeder {
  let queue: Uint8Array[] = [];
  let held = 0;
  let writing = false;
  let dropped = 0;
  let unsaid = 0;

  const next = () => {
    if (queue.length === 0) {
      writing = false;
      return;
    }
    const parts = queue;
    queue = [];
    if (unsaid > 0) {
      parts.unshift(notice(unsaid));
      unsaid = 0;
    }
    let size = 0;
    for (const p of parts) {
      size += p.length;
    }
    const all = new Uint8Array(size);
    let at = 0;
    for (const p of parts) {
      all.set(p, at);
      at += p.length;
    }
    held = 0;
    writing = true;
    sink(all, next);
  };

  return {
    push(data) {
      if (data.length === 0) {
        return;
      }
      queue.push(data);
      held += data.length;
      // Over the bound the oldest pieces go, but never the newest one.
      while (held > limit && queue.length > 1) {
        const old = queue.shift()!;
        held -= old.length;
        dropped += old.length;
        unsaid += old.length;
      }
      if (!writing) {
        next();
      }
    },
    dropped: () => dropped,
    held: () => held,
  };
}

/** How large a message of what is typed may be: 16 KiB. draal closes a
 *  socket that sends a message over 64 KiB, and a paste is as large as
 *  the clipboard. */
export const TYPED = 16 * 1024;

/** The bytes of what was typed, cut into pieces of at most size, in
 *  order. The shell reads a stream of bytes, so a cut may fall between
 *  any two of them, also inside a character. */
export function pieces(data: Uint8Array<ArrayBuffer>, size = TYPED): Uint8Array<ArrayBuffer>[] {
  const step = Number.isFinite(size) && size >= 1 ? Math.floor(size) : TYPED;
  const out: Uint8Array<ArrayBuffer>[] = [];
  for (let at = 0; at < data.length; at += step) {
    out.push(data.subarray(at, Math.min(data.length, at + step)));
  }
  return out;
}
```

- [ ] **Step 14: Run the tests to see them pass**

Run: `nix develop .#bare --command npm --prefix ui test`
Expected: PASS — `Test Files  10 passed (10)`, `Tests  107 passed (107)`, and `nix develop .#bare --command npm --prefix ui run check` prints no error.

- [ ] **Step 15: The shell**

The terminal is fitted to a box without padding inside the box that has
it (the fit addon reads the width of the terminal's parent), and the
box is shown before the terminal is opened in it: a terminal cannot be
fitted to, or focused in, a box that is not displayed.

`ui/src/components/Shell.tsx`:

```tsx
import { FitAddon } from "@xterm/addon-fit";
import { type ITheme, Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { createEffect, createMemo, createSignal, Match, on, onCleanup, Show, Switch } from "solid-js";
import { socketURL } from "../api/reconnect";
import { isRefusal } from "../api/types";
import { feeder, pieces } from "../state/feeder";
import "./shell.css";

// The size of a terminal as the API takes it: 1 to 1000 each way.
function within(n: number): number {
  return Number.isFinite(n) ? Math.min(1000, Math.max(1, Math.floor(n))) : 80;
}

// The colours of the terminal: the tokens of the page, as they are now
// (in the light or in the dark).
function theme(): ITheme {
  const style = getComputedStyle(document.documentElement);
  const v = (name: string) => style.getPropertyValue(name).trim() || undefined;
  return {
    background: v("--term-bg"),
    foreground: v("--term-fg"),
    cursor: v("--term-cursor"),
    cursorAccent: v("--term-bg"),
    selectionBackground: v("--term-selection"),
    black: v("--term-black"),
    red: v("--term-red"),
    green: v("--term-green"),
    yellow: v("--term-yellow"),
    blue: v("--term-blue"),
    magenta: v("--term-magenta"),
    cyan: v("--term-cyan"),
    white: v("--term-white"),
    brightBlack: v("--term-bright-black"),
    brightRed: v("--term-bright-red"),
    brightGreen: v("--term-bright-green"),
    brightYellow: v("--term-bright-yellow"),
    brightBlue: v("--term-bright-blue"),
    brightMagenta: v("--term-bright-magenta"),
    brightCyan: v("--term-bright-cyan"),
    brightWhite: v("--term-bright-white"),
  };
}

// A shell in the container of a zathras, in xterm.js.
//
// It is opened on a button and not with the view: a shell is a process
// in the container. What is typed goes out as binary messages, UTF-8,
// of at most 16 KiB each; what arrives is written to the terminal as
// bytes, through a feeder that keeps a flood from freezing the tab; a
// change of the terminal's size goes out as the text message of the
// API. When the shell ends, or the socket does, the terminal stays to
// be read and takes no more keys. Leaving the view closes the socket,
// which ends the shell.
export default function Shell(props: {
  id: string;
  /** Whether the fleet has the zathras now. */
  present: boolean;
}) {
  const [state, setState] = createSignal<"closed" | "opening" | "open" | "ended">("closed");
  const [ending, setEnding] = createSignal("");
  let mount: HTMLDivElement | undefined;
  let close: (() => void) | undefined;

  const start = () => {
    close?.();
    if (!mount) {
      return;
    }
    // First the box is shown: a terminal can be fitted to a box, and
    // take the keys, only when the box is there.
    setState("opening");
    setEnding("");
    const term = new Terminal({
      fontFamily: getComputedStyle(document.documentElement).getPropertyValue("--ffr-font-mono").trim() || "monospace",
      fontSize: 13,
      lineHeight: 1.2,
      cursorBlink: true,
      scrollback: 5000,
      theme: theme(),
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(mount);
    fit.fit();
    term.focus();

    const query = new URLSearchParams({
      cols: String(within(term.cols)),
      rows: String(within(term.rows)),
      term: "xterm-256color",
    });
    const socket = new WebSocket(socketURL(`/api/zathras/${props.id}/shell?${query}`));
    socket.binaryType = "arraybuffer";
    const feed = feeder((data, done) => term.write(data, done));
    const encoder = new TextEncoder();
    let over = false;
    let got = false;

    // The end of the session: the terminal stays as it is and takes no
    // more keys.
    const end = (why: string) => {
      if (over) {
        return;
      }
      over = true;
      term.options.disableStdin = true;
      term.options.cursorBlink = false;
      setEnding(why);
      setState("ended");
    };

    socket.onopen = () => setState((s) => (s === "opening" ? "open" : s));
    socket.onmessage = (event) => {
      got = true;
      if (event.data instanceof ArrayBuffer) {
        feed.push(new Uint8Array(event.data));
        return;
      }
      if (typeof event.data !== "string") {
        return;
      }
      let v: unknown;
      try {
        v = JSON.parse(event.data);
      } catch {
        return;
      }
      if (isRefusal(v)) {
        end(v.error.message);
      } else if (typeof v === "object" && v !== null && typeof (v as { exit?: unknown }).exit === "number") {
        end(`The shell ended with status ${(v as { exit: number }).exit}.`);
      }
    };
    socket.onclose = (event) => {
      if (event.code !== 1000 && event.reason) {
        // draal says why it closed, in a sentence: the shell does not
        // take its input, for one.
        end(event.reason);
      } else if (event.code === 1001) {
        end("draal is ending, and the shell with it.");
      } else if (!got) {
        // A refusal before the upgrade is one the page cannot read.
        end("The shell could not be opened: draal did not take the connection.");
      } else {
        end("The connection of the shell broke off, and the shell has ended with it.");
      }
    };

    const send = (data: string | Uint8Array<ArrayBuffer>) => {
      if (!over && socket.readyState === WebSocket.OPEN) {
        socket.send(data);
      }
    };
    // What is typed, a paste included, goes out as bytes in messages of
    // at most 16 KiB, in order. When the shell does not read, draal does
    // not either, and the browser holds what was sent: nothing is done
    // about that here.
    const type = (data: Uint8Array<ArrayBuffer>) => {
      for (const piece of pieces(data)) {
        send(piece);
      }
    };
    const typed = term.onData((text) => type(encoder.encode(text)));
    // What a program asked the terminal for in bytes that are no text.
    const binary = term.onBinary((text) => type(Uint8Array.from(text, (c) => c.charCodeAt(0) & 0xff)));

    // A change of size: the terminal is fitted to its box, and the size
    // it then has is sent, once the box has been still for a moment.
    let sent = `${term.cols}x${term.rows}`;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const resized = () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        fit.fit();
        const size = `${term.cols}x${term.rows}`;
        if (size !== sent) {
          sent = size;
          send(JSON.stringify({ resize: { cols: within(term.cols), rows: within(term.rows) } }));
        }
      }, 150);
    };
    const observer = new ResizeObserver(resized);
    observer.observe(mount);

    // The colours follow the scheme of the system.
    const scheme = matchMedia("(prefers-color-scheme: dark)");
    const recolour = () => {
      term.options.theme = theme();
    };
    scheme.addEventListener("change", recolour);

    close = () => {
      close = undefined;
      over = true;
      clearTimeout(timer);
      observer.disconnect();
      scheme.removeEventListener("change", recolour);
      typed.dispose();
      binary.dispose();
      socket.onmessage = socket.onclose = socket.onopen = null;
      socket.close();
      term.dispose();
    };
  };

  const shut = () => {
    close?.();
    setState("closed");
    setEnding("");
  };

  // Another zathras, or the view is left: the session ends. (The memo
  // keeps a fleet that merely arrived from counting as another zathras.)
  const id = createMemo(() => props.id);
  createEffect(on(id, shut, { defer: true }));
  onCleanup(() => close?.());

  return (
    <section class="section">
      <div class="shell-head">
        <h2>Shell</h2>
        <span class="muted">
          <Switch>
            <Match when={state() === "open"}>
              a process in the container of this zathras; it ends when you leave this view or close the tab
            </Match>
            <Match when={state() === "opening"}>asking draal…</Match>
            <Match when={state() === "ended"}>ended</Match>
            <Match when={true}>a process in the container of this zathras, started when you ask for it</Match>
          </Switch>
        </span>
        <Show when={state() === "open" || state() === "opening"}>
          <button type="button" class="button quiet" onClick={shut}>
            End the shell
          </button>
        </Show>
      </div>
      <Show when={state() === "ended"}>
        <div class="shell-ended" role="status">
          <span>{ending()}</span>
          <button type="button" class="button" disabled={!props.present} onClick={start}>
            Open another shell
          </button>
          <button type="button" class="button quiet" onClick={shut}>
            Close the terminal
          </button>
        </div>
      </Show>
      <Show when={state() === "closed"}>
        <div class="shell-closed">
          <button type="button" class="button" disabled={!props.present} onClick={start}>
            Open a shell
          </button>
          <span class="muted">
            <Show when={props.present} fallback="The zathras is not connected: there is no container to open a shell in.">
              It sees the store, the files and the processes the service sees. Leaving this view or closing the tab ends
              it.
            </Show>
          </span>
        </div>
      </Show>
      {/* The terminal is fitted to the inner box: the outer one has the
          padding, which is no place for a column. */}
      <div class="shell-box" data-state={state()}>
        <div class="shell-term" ref={mount} />
      </div>
    </section>
  );
}
```

`ui/src/components/shell.css`:

```css
/* The shell of a zathras: xterm.js in a box of the terminal's ground. */

.shell-head {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--ffr-space-1) var(--ffr-space-4);
  margin-bottom: var(--ffr-space-3);
}

.shell-head .muted {
  font-size: 13px;
  flex: 1 1 24ch;
}

.shell-closed,
.shell-ended {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--ffr-space-2) var(--ffr-space-4);
}

.shell-closed .muted {
  font-size: 13px;
  max-width: 70ch;
}

.shell-ended {
  margin-bottom: var(--ffr-space-3);
  padding: var(--ffr-space-2) var(--ffr-space-3);
  border: 1px solid var(--border-strong);
  border-radius: var(--ffr-radius-sm);
  font-weight: 500;
}

.shell-ended span {
  flex: 1 1 30ch;
  overflow-wrap: anywhere;
}

/* The button of the design system: blocky, black on white or white on
   black, a pixel up when the pointer is on it. */
.button {
  font-family: var(--ffr-font-display);
  font-weight: 700;
  font-size: 13px;
  padding: 8px 16px;
  border-radius: var(--ffr-radius-sm);
  border: 1.5px solid var(--fg);
  background: var(--fg);
  color: var(--bg);
  cursor: pointer;
  white-space: nowrap;
  transition: transform var(--ffr-duration-fast) var(--ffr-ease-standard);
}

.button:hover:not(:disabled) {
  transform: translateY(-1px);
}

.button.quiet {
  background: transparent;
  color: var(--fg);
  padding: 5px 12px;
  font-size: 12px;
}

.button:disabled {
  background: var(--surface-strong);
  border-color: var(--border);
  color: var(--fg-subtle);
  cursor: not-allowed;
}

@media (prefers-reduced-motion: reduce) {
  .button {
    transition: none;
  }
}

.shell-box {
  height: 440px;
  border: 1px solid var(--border);
  border-radius: var(--ffr-radius-md);
  background: var(--term-bg);
  padding: var(--ffr-space-2) var(--ffr-space-3);
  overflow: hidden;
}

.shell-box[data-state="closed"] {
  display: none;
}

/* A terminal that has ended is read, not typed into. */
.shell-box[data-state="ended"] {
  opacity: 0.8;
}

.shell-term,
.shell-box .xterm {
  height: 100%;
}

@media (max-width: 640px) {
  .shell-box {
    height: 340px;
    padding: var(--ffr-space-2);
  }
}
```

In `ui/src/views/Zathras.tsx`, the shell is loaded when the view is:
xterm.js is three quarters of the page's script, and the other two
views do not need it. Add `lazy` to the import of `solid-js`:

```tsx
import { createEffect, createMemo, createSignal, For, lazy, Match, Show, Switch } from "solid-js";
```

declare it after the imports:

```tsx
import "./service.css";
import "./zathras.css";

// The shell brings xterm.js, which is most of the page's script: it is
// a file of its own that only this view asks for.
const Shell = lazy(() => import("../components/Shell"));

// Everything a zathras reports, each thing under its name.
```

and put it after the log:

```tsx
            <Log id={params.id} present={live() !== undefined} connected={live()?.connected} />
            <Shell id={params.id} present={live() !== undefined} />
          </>
```

The file is then this:

`ui/src/views/Zathras.tsx`:

```tsx
import { A, useParams } from "@solidjs/router";
import { createEffect, createMemo, createSignal, For, lazy, Match, Show, Switch } from "solid-js";
import { isID, isZeroTime, type Zathras as ZathrasInfo } from "../api/types";
import { PhaseBadge } from "../components/Badge";
import Figures from "../components/Figures";
import Log from "../components/Log";
import Notice from "../components/Notice";
import { ago, dateTime, duration, exit, generation, storeName, until } from "../format";
import { findService, findZathras, fleet, freshAt, now, received } from "../state/fleet";
import { phaseNote } from "../state/rollout";
import "./service.css";
import "./zathras.css";

// The shell brings xterm.js, which is most of the page's script: it is
// a file of its own that only this view asks for.
const Shell = lazy(() => import("../components/Shell"));

// Everything a zathras reports, each thing under its name.
function StatePanel(props: { zathras: ZathrasInfo; stable: number | undefined }) {
  const z = () => props.zathras;
  const st = () => z().state;
  return (
    <dl class="state">
      <div>
        <dt class="label">Phase</dt>
        <dd>
          <PhaseBadge phase={st().phase} /> <span class="muted">{phaseNote(st(), now())}</span>
        </dd>
      </div>
      <div>
        <dt class="label">Generation</dt>
        <dd>
          <Show when={st().generation > 0} fallback={<span class="muted">none: nothing was activated on it</span>}>
            <span class="num">{generation(st().generation)}</span>{" "}
            <Show when={props.stable !== undefined}>
              <span class="muted">
                {st().generation === props.stable
                  ? "the stable one of the service"
                  : `not the stable one of the service, which is ${generation(props.stable)}`}
              </span>
            </Show>
          </Show>
        </dd>
      </div>
      <div class="wide">
        <dt class="label">Binary</dt>
        <dd>
          <Show when={st().binary} fallback={<span class="muted">none</span>}>
            <b class="mono">{storeName(st().binary)}</b>
            <span class="mono muted path">{st().binary}</span>
          </Show>
        </dd>
      </div>
      <div class="wide">
        <dt class="label">Arguments</dt>
        <dd>
          <Show when={(st().args?.length ?? 0) > 0} fallback={<span class="muted">none</span>}>
            <span class="args">
              <For each={st().args}>{(a) => <code>{a}</code>}</For>
            </span>
          </Show>
        </dd>
      </div>
      <div>
        <dt class="label">Process ID</dt>
        <dd>
          <Show when={st().pid} fallback={<span class="muted">none: the service does not run</span>}>
            <span class="num">{st().pid}</span>
          </Show>
        </dd>
      </div>
      <div>
        <dt class="label">Running for</dt>
        <dd>
          <Show when={st().running_for !== undefined} fallback={<span class="muted">not running</span>}>
            <span class="num">{duration(st().running_for)}</span>
          </Show>
        </dd>
      </div>
      <Show when={!isZeroTime(st().restart_at)}>
        <div>
          <dt class="label">Starts again</dt>
          <dd>
            <span class="num">{dateTime(st().restart_at)}</span> <span class="muted">({until(st().restart_at, now())})</span>
          </dd>
        </div>
      </Show>
      <div>
        <dt class="label">Restarts</dt>
        <dd>
          <span class="num">{st().restarts}</span> <span class="muted">since this zathras started</span>
        </dd>
      </div>
      <div class="wide">
        <dt class="label">Last exit</dt>
        <dd>
          <Show when={st().last_exit} fallback={<span class="muted">none: the service has not ended since this zathras started</span>}>
            {(e) => (
              <>
                <span class="exit" data-tone={e().status === 0 && !e().signal && !e().error ? undefined : "bad"}>
                  {exit(e())}
                </span>
                <span class="muted">
                  {" "}
                  of generation <span class="num">{generation(e().generation)}</span>, at{" "}
                  <span class="num">{dateTime(e().at)}</span> ({ago(e().at, now())}), after it ran{" "}
                  <span class="num">{duration(e().ran)}</span>
                </span>
                <Show when={e().error}>
                  <span class="reason">{e().error}</span>
                </Show>
              </>
            )}
          </Show>
        </dd>
      </div>
      <div>
        <dt class="label">Prepared generations</dt>
        <dd>
          <Show when={(st().prepared?.length ?? 0) > 0} fallback={<span class="muted">none</span>}>
            <span class="num">{st().prepared!.join(", ")}</span>
          </Show>
        </dd>
      </div>
      <div>
        <dt class="label">Preparing</dt>
        <dd>
          <Show when={st().preparing} fallback={<span class="muted">nothing</span>}>
            generation <span class="num">{st().preparing}</span>
          </Show>
        </dd>
      </div>
      <div>
        <dt class="label">Connected to draal</dt>
        <dd>
          <span class="num">{dateTime(z().connected)}</span> <span class="muted">({ago(z().connected, now())})</span>
        </dd>
      </div>
      <div class="wide">
        <dt class="label">Endpoint ID</dt>
        <dd>
          <span class="mono whole-id">{z().id}</span>
        </dd>
      </div>
    </dl>
  );
}

// View 3: a zathras: its state in full, its figures, its log, a shell.
//
// Review Focus 2: when the zathras goes while this view is open, the
// view says so, keeps what it showed, and asks nothing more; when the
// fleet has the zathras again, it carries on.
export default function Zathras() {
  const params = useParams<{ service: string; id: string }>();
  const live = () => (isID(params.id) ? findZathras(params.service, params.id) : undefined);

  // What the zathras reported last, kept when it goes, and when that
  // was. It is kept with the path it is of: what another zathras
  // reported is never shown under this one's.
  const path = () => `${params.service}/${params.id}`;
  const [kept, setKept] = createSignal<{ path: string; zathras: ZathrasInfo; seen: number }>();
  createEffect(() => {
    const z = live();
    if (z) {
      // Reading the whole of it is what makes this run again when any
      // of it changes.
      setKept({ path: path(), zathras: JSON.parse(JSON.stringify(z)) as ZathrasInfo, seen: freshAt() });
    }
  });
  const last = () => (kept()?.path === path() ? kept()!.zathras : undefined);
  const seen = () => kept()?.seen ?? 0;

  // The service that has a zathras of this ID, when it is not the one
  // of the path.
  const elsewhere = createMemo(() =>
    isID(params.id) && !live() ? fleet.services.find((s) => s.zathrases.some((z) => z.id === params.id))?.name : undefined,
  );

  return (
    <Switch>
      <Match when={!isID(params.id)}>
        <div class="head">
          <h1>No such zathras</h1>
        </div>
        <Notice title="This is no endpoint ID">
          <p>
            A zathras is named in the path by its endpoint ID, 64 hexadecimal characters. <span class="mono wrap">{params.id}</span>{" "}
            is none.
          </p>
          <p>
            <A href={`/s/${encodeURIComponent(params.service)}`}>Go to the service {params.service}</A>
          </p>
        </Notice>
      </Match>
      <Match when={last()}>
        {(z) => (
          <>
            <div class="head z-head">
              <h1>{z().name}</h1>
              <span class="muted">
                a zathras of <A href={`/s/${encodeURIComponent(params.service)}`}>{params.service}</A>
              </span>
            </div>
            <Show when={!live() && received()}>
              <div class="gone" role="alert">
                <b>This zathras is not connected to draal any more.</b> What is below is what it reported last,{" "}
                {ago(seen(), now())}. The page asks nothing of it now, and carries on when it is back.
              </div>
            </Show>
            <div data-stale={!live() && received() ? "" : undefined} class="z-body">
              <StatePanel zathras={z()} stable={findService(params.service)?.stable} />
            </div>
            <Figures id={params.id} present={live() !== undefined} connected={live()?.connected} sample={live()?.state.sample} />
            <Log id={params.id} present={live() !== undefined} connected={live()?.connected} />
            <Shell id={params.id} present={live() !== undefined} />
          </>
        )}
      </Match>
      <Match when={!received()}>
        <div class="head">
          <h1>Zathras</h1>
        </div>
        <p class="muted">Asking draal…</p>
      </Match>
      <Match when={true}>
        <div class="head">
          <h1>No such zathras</h1>
        </div>
        <Notice title={`No zathras with this ID is connected to ${params.service}`}>
          <p>
            <span class="mono wrap">{params.id}</span>
          </p>
          <Show
            when={elsewhere()}
            fallback={
              <p>
                It may have ended, or its pod was replaced: a new pod is a new zathras with a new ID. This page shows it
                as soon as draal has it.
              </p>
            }
          >
            <p>
              A zathras with this ID is connected to another service:{" "}
              <A href={`/s/${encodeURIComponent(elsewhere()!)}/z/${params.id}`}>{elsewhere()}</A>.
            </p>
          </Show>
          <p>
            <A href={`/s/${encodeURIComponent(params.service)}`}>Go to the service {params.service}</A>
          </p>
        </Notice>
      </Match>
    </Switch>
  );
}
```

- [ ] **Step 16: Check and build**

Run: `nix develop .#bare --command npm --prefix ui run check`, `nix develop .#bare --command npm --prefix ui test` and `nix develop .#bare --command npm --prefix ui run build`
Expected: no error; `Test Files  10 passed (10)`, `Tests  107 passed (107)`; `✓ built`, with `assets/Shell-….js` (about 338 kB, 86 kB gzipped) and `assets/Shell-….css` beside `assets/index-….js` (about 99 kB, 33 kB gzipped) and `assets/index-….css`.

Run: `grep -rnE "innerHTML|outerHTML|insertAdjacentHTML|document\.write" ui/src; echo $?`
Expected: `1` and nothing else.

- [ ] **Step 17: Look at it (Review Focus 2 and 5)**

Run `nix develop .#bare --command go run ./web/demo --addr 127.0.0.1:8080 --dist web/dist` and open `http://127.0.0.1:8080/s/web`, console and
network panel open; follow the link of `web-7d9f8b6c4-mn4tc`. The
console stays empty throughout: xterm's styles pass the content
security policy.

The log:

- 400 rows, the newest at the bottom, each with its time, its stream and its text. Rows of `stderr` have an amber line at the left, rows of `zathras` a blue one and a tinted ground. `INFO`, `WARN`, `ERROR` and the status codes are coloured; a line that contains `<b>not markup</b> &amp; "quotes"` shows exactly that. Text can be selected and copied.
- One line of the ring is 16 KiB without a space: it wraps inside the box and nothing scrolls sideways. Scroll up: the view stays where it is while lines arrive, and `Go to the end` appears, with the number of new lines; a click is back at the end.
- Review Focus 5: follow the link of the second `web-7d9f8b6c4-8hjwz` (its ID starts `eafff105`). At second 14 of the demo's minute its log floods for 10 seconds: the rows race, the page scrolls and answers as before, and the box never has more than 400 rows (`document.querySelectorAll(".log-row").length` in the console).
- Review Focus 2: `web-7d9f8b6c4-q5rvd`. At second 43 a red row says `the log of zathras f1f8a967… has ended: the zathras is gone`, and the line above the box says `ended: the zathras is not connected`; the rows stay. The network panel shows no request until second 49; then one socket of the log is opened, and the box goes on with the lines that are new: none is there twice.
- Stop the demo: one red row with draal's reason, `draal is shutting down`, and the network panel shows no new socket of the log while the header says `LOST`. Start it: the log is followed again, without a line twice.

The shell (the demo's shell is a real shell on this machine: change
nothing with it):

- Before the button is pressed the network panel shows no socket of a shell. `Open a shell`: a terminal in the colours of the page with a prompt, and the keys go to it. `echo hello; printf '\033[31mred\033[0m\n'; stty size` prints `hello`, a red `red`, and the size of the terminal, which is the `cols` and `rows` of the socket's URL. Make the window narrower: `stty size` prints fewer columns. Change the system between light and dark: the terminal follows.
- Review Focus 5: `yes 'the quick brown fox jumps over the lazy dog' | head -n 2000000` scrolls for a few seconds; meanwhile the page scrolls and its links answer; at the end the prompt is back.
- Paste 100 KiB of text: the network panel shows binary messages of at most 16384 bytes.
- `exit 3`: `The shell ended with status 3.`, the terminal stays and takes no key, and `Open another shell` starts a new one.
- Open a shell on `web-7d9f8b6c4-q5rvd` and wait for second 43: the shell ends with draal's sentence that the zathras is gone, and `Open another shell` is disabled until the zathras is back.
- With a shell open, go to `/s/web`: in the network panel the sockets of the shell and of the log are closed, and only `watch` is open.
- At a width of 390 pixels the log puts the text under the time and the stream, and the terminal is as wide as the page.

- [ ] **Step 18: Commit**

```bash
git add ui/src/state/feeder.ts ui/src/state/feeder.test.ts ui/src/components/Shell.tsx ui/src/components/shell.css ui/src/views/Zathras.tsx
git commit -m "UI: a shell in xterm.js, and a flood that does not freeze the tab"
```

- [ ] **Step 19: The program with the page in it**

Run: `nix build --option allow-unsafe-native-code-during-evaluation true && ./result/bin/great-machine version && rm result`
Expected: the build succeeds (the hash of Task 7 is the one of the lock file, which this task did not change) and the version is printed.
