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
