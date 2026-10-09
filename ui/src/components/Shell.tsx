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
