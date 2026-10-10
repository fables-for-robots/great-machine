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
  /** Stops it, for a terminal that is disposed: what is held is let go,
   *  what is pushed afterwards is dropped unsaid, and the sink is given
   *  nothing more, also when it says only then that it has taken its
   *  last piece. */
  stop(): void;
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
  let stopped = false;

  const next = () => {
    if (stopped || queue.length === 0) {
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
      if (stopped || data.length === 0) {
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
    stop() {
      stopped = true;
      queue = [];
      held = 0;
      unsaid = 0;
    },
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
