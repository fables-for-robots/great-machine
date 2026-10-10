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
