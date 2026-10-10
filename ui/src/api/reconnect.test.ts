import { describe, expect, test } from "vitest";
import { type Connection, FIRST_PAUSE, keep, LONGEST_PAUSE, pause, type SocketLike, socketURL } from "./reconnect";

describe("the pauses", () => {
  test("1 s doubling to 30 s, when chance is 1", () => {
    expect([0, 1, 2, 3, 4, 5, 6, 20].map((n) => pause(n, 1))).toEqual([
      1000, 2000, 4000, 8000, 16_000, 30_000, 30_000, 30_000,
    ]);
    expect(pause(0, 1)).toBe(FIRST_PAUSE);
    expect(pause(1000, 1)).toBe(LONGEST_PAUSE);
  });

  test("chance takes up to a half off, and never more", () => {
    expect(pause(0, 0)).toBe(500);
    expect(pause(3, 0.5)).toBe(6000);
    expect(pause(9, 0)).toBe(15_000);
    for (let n = 0; n < 12; n++) {
      for (const c of [0, 0.25, 0.5, 0.99, 1]) {
        const p = pause(n, c);
        expect(p).toBeGreaterThanOrEqual(500);
        expect(p).toBeLessThanOrEqual(LONGEST_PAUSE);
      }
    }
  });

  test("nonsense gives a pause all the same", () => {
    expect(pause(NaN, NaN)).toBe(1000);
    expect(pause(-3, 7)).toBe(1000);
    expect(pause(2, -1)).toBe(2000);
  });
});

// A socket of the test: what the page did to it, and the means to make
// it speak and end.
class FakeSocket implements SocketLike {
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: ((event: { code?: number }) => void) | null = null;
  onerror: ((event: unknown) => void) | null = null;
  closed = false;
  close() {
    this.closed = true;
  }
  say(data: unknown) {
    this.onmessage?.({ data });
  }
  end() {
    this.onerror?.({});
    this.onclose?.({ code: 1006 });
  }
}

// The world of a test: the sockets that were opened, the timers that
// run, and what the connection reported.
function world(chance = 1) {
  const sockets: FakeSocket[] = [];
  const timers: { run: () => void; ms: number; cleared: boolean }[] = [];
  const states: Connection[] = [];
  const messages: unknown[] = [];
  const kept = keep({
    open: () => {
      const s = new FakeSocket();
      sockets.push(s);
      return s;
    },
    // What the test calls a refusal is a message that is not taken.
    onMessage: (data) => {
      messages.push(data);
      return data !== "refusal";
    },
    onState: (s) => states.push(s),
    setTimer: (run, ms) => {
      const t = { run, ms, cleared: false };
      timers.push(t);
      return t;
    },
    clearTimer: (t) => {
      (t as { cleared: boolean }).cleared = true;
    },
    chance: () => chance,
  });
  const last = () => sockets[sockets.length - 1]!;
  // Lets the pause that runs pass.
  const elapse = () => {
    const t = timers[timers.length - 1]!;
    expect(t.cleared).toBe(false);
    t.run();
  };
  return { sockets, timers, states, messages, kept, last, elapse };
}

describe("a connection that is kept", () => {
  test("is live with the first message", () => {
    const w = world();
    expect(w.sockets.length).toBe(1);
    expect(w.states).toEqual([]);
    w.last().say("fleet");
    expect(w.states).toEqual(["live"]);
    expect(w.messages).toEqual(["fleet"]);
  });

  // Review Focus 3: draal restarted while the page is open. The page
  // says that the connection is lost, tries again with pauses that
  // grow, and is live again with the first message.
  test("is lost when the socket ends, tries again with pauses that grow, and is live again", () => {
    const w = world();
    w.last().say("fleet");
    w.last().end();
    expect(w.states).toEqual(["live", "lost"]);
    expect(w.timers.map((t) => t.ms)).toEqual([1000]);

    // draal is not back: every attempt fails, and the pauses double.
    for (let i = 0; i < 6; i++) {
      w.elapse();
      w.last().end();
    }
    expect(w.timers.map((t) => t.ms)).toEqual([1000, 2000, 4000, 8000, 16_000, 30_000, 30_000]);
    expect(w.states).toEqual(["live", "lost"]);

    // draal is back.
    w.elapse();
    expect(w.sockets.length).toBe(8);
    w.last().say("fleet again");
    expect(w.states).toEqual(["live", "lost", "live"]);
    expect(w.messages).toEqual(["fleet", "fleet again"]);

    // The pauses start over after a message.
    w.last().end();
    expect(w.timers[w.timers.length - 1]!.ms).toBe(1000);
  });

  test("a socket that opens and ends without a message does not start the pauses over", () => {
    const w = world();
    w.last().end();
    expect(w.states).toEqual(["lost"]);
    w.elapse();
    w.last().end();
    w.elapse();
    w.last().end();
    expect(w.timers.map((t) => t.ms)).toEqual([1000, 2000, 4000]);
  });

  // Final review A1: draal's last message on a socket that fails is a
  // refusal. It is no fleet, and no sign that draal is there.
  test("a socket that gave only a message that was not taken is one that gave nothing", () => {
    const w = world();
    w.last().say("fleet");
    w.last().end();
    expect(w.timers.map((t) => t.ms)).toEqual([1000]);

    // draal is there and fails: every socket says so and ends.
    for (let i = 0; i < 3; i++) {
      w.elapse();
      w.last().say("refusal");
      expect(w.states).toEqual(["live", "lost"]);
      w.last().end();
    }
    // The pause after such a socket is longer than the one before it.
    expect(w.timers.map((t) => t.ms)).toEqual([1000, 2000, 4000, 8000]);
    expect(w.messages).toEqual(["fleet", "refusal", "refusal", "refusal"]);

    // A fleet after that is taken, and the pauses start over.
    w.elapse();
    w.last().say("fleet again");
    expect(w.states).toEqual(["live", "lost", "live"]);
    w.last().end();
    expect(w.timers[w.timers.length - 1]!.ms).toBe(1000);
  });

  test("a first socket that gave only a message that was not taken leaves the page connecting, then lost", () => {
    const w = world();
    w.last().say("refusal");
    expect(w.states).toEqual([]);
    w.last().end();
    expect(w.states).toEqual(["lost"]);
  });

  // Final review A2: who hears that the connection is live finds what
  // the message brought already taken.
  test("the message is handed on before the state is live", () => {
    const order: string[] = [];
    const sockets: FakeSocket[] = [];
    keep({
      open: () => {
        const s = new FakeSocket();
        sockets.push(s);
        return s;
      },
      onMessage: () => {
        order.push("message");
        return true;
      },
      onState: (s) => order.push(s),
      setTimer: () => 0,
      clearTimer: () => {},
    });
    sockets[0]!.say("fleet");
    expect(order).toEqual(["message", "live"]);
  });

  test("the end of a socket counts once, though it says so twice", () => {
    const w = world();
    const first = w.last();
    first.end();
    expect(w.timers.length).toBe(1);
    expect(first.closed).toBe(true);
    // What a socket that was replaced still says is not heard.
    w.elapse();
    first.onclose?.({ code: 1006 });
    first.say("late");
    expect(w.sockets.length).toBe(2);
    expect(w.timers.length).toBe(1);
    expect(w.messages).toEqual([]);
  });

  test("hurry tries at once while a pause runs, and does nothing otherwise", () => {
    const w = world();
    w.kept.hurry();
    expect(w.sockets.length).toBe(1);
    w.last().end();
    w.kept.hurry();
    expect(w.sockets.length).toBe(2);
    expect(w.timers[0]!.cleared).toBe(true);
    // An attempt is under way: another hurry opens no second socket.
    w.kept.hurry();
    expect(w.sockets.length).toBe(2);
    // The attempt that hurry made counts as one: the next pause is longer.
    w.last().end();
    expect(w.timers[w.timers.length - 1]!.ms).toBe(2000);
  });

  test("stop closes the socket, and nothing is opened after it", () => {
    const w = world();
    w.last().say("fleet");
    const s = w.last();
    w.kept.stop();
    expect(s.closed).toBe(true);
    s.end();
    expect(w.sockets.length).toBe(1);
    expect(w.timers.length).toBe(0);
    expect(w.states).toEqual(["live"]);
  });

  test("stop during a pause clears it", () => {
    const w = world();
    w.last().end();
    w.kept.stop();
    expect(w.timers[0]!.cleared).toBe(true);
    w.kept.hurry();
    expect(w.sockets.length).toBe(1);
  });

  test("a socket that cannot be made is one that ended", () => {
    const states: Connection[] = [];
    const pauses: number[] = [];
    keep({
      open: () => {
        throw new Error("no socket");
      },
      onMessage: () => true,
      onState: (s) => states.push(s),
      setTimer: (_run, ms) => pauses.push(ms),
      clearTimer: () => {},
      chance: () => 1,
    });
    expect(states).toEqual(["lost"]);
    expect(pauses).toEqual([1000]);
  });

  test("the pauses have the jitter of chance", () => {
    const w = world(0);
    w.last().end();
    w.elapse();
    w.last().end();
    expect(w.timers.map((t) => t.ms)).toEqual([500, 1000]);
  });
});

describe("the URL of a socket", () => {
  test("ws for http, wss for https, the host of the page", () => {
    expect(socketURL("/api/watch", { protocol: "http:", host: "127.0.0.1:8080" })).toBe("ws://127.0.0.1:8080/api/watch");
    expect(socketURL("/api/watch", { protocol: "https:", host: "draal.example" })).toBe("wss://draal.example/api/watch");
  });
});
