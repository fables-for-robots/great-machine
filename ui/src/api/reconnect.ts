// A socket that is opened again whenever it ends: the schedule of the
// pauses, and the keeping of one connection. Nothing here touches the
// browser directly: what it needs of it (a socket, a timer, chance) is
// handed in, so that the whole is tested without one.

/** The first pause, in milliseconds. */
export const FIRST_PAUSE = 1000;

/** The longest pause, in milliseconds. */
export const LONGEST_PAUSE = 30_000;

/** The pause before the attempt with the number given (0 for the first
 *  one after the connection ended): 1 s doubling to 30 s, and of that a
 *  share between a half and the whole, which chance (0 to 1) picks, so
 *  that the pages of a draal that was restarted do not all come back in
 *  the same instant. */
export function pause(attempt: number, chance: number): number {
  const n = Number.isFinite(attempt) && attempt > 0 ? Math.min(Math.floor(attempt), 30) : 0;
  const full = Math.min(LONGEST_PAUSE, FIRST_PAUSE * 2 ** n);
  const c = Number.isFinite(chance) ? Math.min(1, Math.max(0, chance)) : 1;
  return Math.round(full * (0.5 + c / 2));
}

/** The state of a connection that is kept: connecting until the first
 *  message ever, live after a message, lost from the end of a socket
 *  until the next message. */
export type Connection = "connecting" | "live" | "lost";

/** What a kept connection needs of a socket. A WebSocket is one. */
export interface SocketLike {
  onmessage: ((event: { data: unknown }) => void) | null;
  onclose: ((event: { code?: number }) => void) | null;
  onerror: ((event: unknown) => void) | null;
  close(): void;
}

/** What Keep is given. */
export interface KeepOptions {
  /** Opens a socket. */
  open: () => SocketLike;
  /** A message of the socket that is open. */
  onMessage: (data: unknown) => void;
  /** The state, whenever it changes. */
  onState: (state: Connection) => void;
  /** setTimeout and clearTimeout, or what stands for them in a test. */
  setTimer?: (run: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
  /** Math.random, or what stands for it in a test. */
  chance?: () => number;
}

/** A connection that is kept: see keep. */
export interface Kept {
  /** Tries now if the connection is lost and a pause is running: for when
   *  the tab is looked at again or the network is back. */
  hurry(): void;
  /** Ends the connection for good. */
  stop(): void;
}

/** Opens a socket and keeps one open: when it ends, another is opened
 *  after a pause, and the pauses grow until a message arrives. */
export function keep(options: KeepOptions): Kept {
  const setTimer = options.setTimer ?? ((run, ms) => setTimeout(run, ms));
  const clearTimer = options.clearTimer ?? ((t) => clearTimeout(t as ReturnType<typeof setTimeout>));
  const chance = options.chance ?? Math.random;

  let socket: SocketLike | undefined;
  let timer: unknown;
  let waiting = false;
  let attempt = 0;
  let stopped = false;
  let state: Connection = "connecting";

  const set = (next: Connection) => {
    if (state !== next) {
      state = next;
      options.onState(next);
    }
  };

  const ended = (of: SocketLike) => {
    // A socket that fails says so twice (error, then close), and one
    // that was replaced may still say it: only the current one counts.
    if (stopped || socket !== of) {
      return;
    }
    socket = undefined;
    of.onmessage = of.onclose = of.onerror = null;
    try {
      of.close();
    } catch {
      // It is closed already.
    }
    set("lost");
    waiting = true;
    timer = setTimer(() => {
      waiting = false;
      open();
    }, pause(attempt, chance()));
    attempt++;
  };

  const open = () => {
    if (stopped) {
      return;
    }
    let s: SocketLike;
    try {
      s = options.open();
    } catch {
      // A socket that cannot even be made is one that ended at once.
      s = { onmessage: null, onclose: null, onerror: null, close() {} };
      socket = s;
      ended(s);
      return;
    }
    socket = s;
    s.onmessage = (event) => {
      if (stopped || socket !== s) {
        return;
      }
      // A message is what shows that draal is there: the pauses start
      // over.
      attempt = 0;
      set("live");
      options.onMessage(event.data);
    };
    s.onclose = () => ended(s);
    s.onerror = () => ended(s);
  };

  open();

  return {
    hurry() {
      if (stopped || !waiting) {
        return;
      }
      clearTimer(timer);
      waiting = false;
      open();
    },
    stop() {
      if (stopped) {
        return;
      }
      stopped = true;
      if (waiting) {
        clearTimer(timer);
        waiting = false;
      }
      const s = socket;
      socket = undefined;
      if (s) {
        s.onmessage = s.onclose = s.onerror = null;
        s.close();
      }
    },
  };
}

/** The URL of a WebSocket of the API on the host of the page: ws: for a
 *  page of http:, wss: for one of https:. */
export function socketURL(path: string, at: { protocol: string; host: string } = location): string {
  return `${at.protocol === "https:" ? "wss:" : "ws:"}//${at.host}${path}`;
}
