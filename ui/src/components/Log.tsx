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
