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
