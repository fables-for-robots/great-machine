// What the page writes for a number, a time, an ID, a path and an exit.
// Every function here is pure and total: whatever it is given, it
// returns text a person can read, and never "NaN", "undefined" or
// "Invalid Date". What is missing is written as a dash.

import { type Exit, parseTime, type Time } from "./api/types";

/** What stands where a value is missing. */
export const NONE = "–";

const NS_PER_MS = 1e6;

function finite(n: unknown): n is number {
  return typeof n === "number" && Number.isFinite(n);
}

/** A number with as many decimals as keep three figures: 1.25, 12.5, 125. */
function three(n: number): string {
  if (n >= 100) {
    return String(Math.round(n));
  }
  if (n >= 10) {
    return String(Math.round(n * 10) / 10);
  }
  return String(Math.round(n * 100) / 100);
}

const BINARY = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] as const;

/** A number of bytes in binary units: "0 B", "1.5 KiB", "273 MiB". */
export function bytes(n: number | undefined | null): string {
  if (!finite(n) || n < 0) {
    return NONE;
  }
  let unit = 0;
  let v = n;
  while (v >= 1024 && unit < BINARY.length - 1) {
    v /= 1024;
    unit++;
  }
  // 1023.6 KiB would be written "1024 KiB": it is 1 MiB.
  if (unit > 0 && Math.round(v) >= 1024 && unit < BINARY.length - 1) {
    v /= 1024;
    unit++;
  }
  return `${unit === 0 ? Math.round(v) : three(v)} ${BINARY[unit]}`;
}

/** Bytes per second: "22 KiB/s". A rate below zero, which a counter that
 *  started again would give, is none. */
export function rate(n: number | undefined | null): string {
  const b = bytes(n);
  return b === NONE ? NONE : `${b}/s`;
}

/** Cores as a percentage of one core: 0.75 is "75%", 2.5 is "250%". */
export function cpu(cores: number | undefined | null): string {
  if (!finite(cores) || cores < 0) {
    return NONE;
  }
  const p = cores * 100;
  if (p >= 10) {
    return `${Math.round(p)}%`;
  }
  return `${Math.round(p * 10) / 10}%`;
}

/** A share, 0 to 1, as a whole percentage. */
export function percent(share: number | undefined | null): string {
  if (!finite(share) || share < 0) {
    return NONE;
  }
  return `${Math.round(share * 100)}%`;
}

/** A length of time in milliseconds, in its two largest units:
 *  "850 ms", "1.3 s", "47 s", "3 m 20 s", "2 h 5 m", "3 d 4 h". A length
 *  below zero, which two clocks that differ give, is "0 s". */
export function span(ms: number | undefined | null): string {
  if (!finite(ms)) {
    return NONE;
  }
  if (ms <= 0) {
    return "0 s";
  }
  if (ms < 1000) {
    return `${Math.max(1, Math.round(ms))} ms`;
  }
  if (ms < 10_000) {
    return `${Math.round(ms / 100) / 10} s`;
  }
  const s = Math.round(ms / 1000);
  if (s < 60) {
    return `${s} s`;
  }
  const m = Math.floor(s / 60);
  if (m < 60) {
    return s % 60 === 0 ? `${m} m` : `${m} m ${s % 60} s`;
  }
  const h = Math.floor(m / 60);
  if (h < 24) {
    return m % 60 === 0 ? `${h} h` : `${h} h ${m % 60} m`;
  }
  const d = Math.floor(h / 24);
  return h % 24 === 0 ? `${d} d` : `${d} d ${h % 24} h`;
}

/** A duration of the API, nanoseconds, written as span writes it. */
export function duration(ns: number | undefined | null): string {
  return finite(ns) ? span(ns / NS_PER_MS) : NONE;
}

/** A length of time in its largest unit alone: "47 s", "3 m", "2 h". */
function coarse(ms: number): string {
  const s = Math.floor(ms / 1000);
  if (s < 60) {
    return `${s} s`;
  }
  if (s < 3600) {
    return `${Math.floor(s / 60)} m`;
  }
  if (s < 86_400) {
    return `${Math.floor(s / 3600)} h`;
  }
  return `${Math.floor(s / 86_400)} d`;
}

/** How long ago a time was, by the clock of now (milliseconds): "now",
 *  "47 s ago", "3 m ago". A time that is none is a dash; one in the
 *  future, which a clock that is ahead gives, is "now". */
export function ago(t: Time | number | undefined | null, now: number): string {
  const at = typeof t === "number" ? t : parseTime(t);
  if (!finite(at) || !finite(now)) {
    return NONE;
  }
  const d = now - at;
  return d < 1000 ? "now" : `${coarse(d)} ago`;
}

/** How long it is until a time: "in 6 s", or "now" when it has come. */
export function until(t: Time | undefined | null, now: number): string {
  const at = parseTime(t);
  if (!finite(at) || !finite(now)) {
    return NONE;
  }
  const d = at - now;
  return d < 1000 ? "now" : `in ${coarse(d)}`;
}

/** How long there is between two times, as span writes it. */
export function between(from: Time | undefined | null, to: Time | undefined | null): string {
  const a = parseTime(from);
  const b = parseTime(to);
  return finite(a) && finite(b) ? span(b - a) : NONE;
}

function two(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

/** The time of day of a time, on the clock of the reader: "22:14:41". */
export function clock(t: Time | number | undefined | null): string {
  const at = typeof t === "number" ? t : parseTime(t);
  if (!finite(at)) {
    return NONE;
  }
  const d = new Date(at);
  return `${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}`;
}

/** The time of day with its milliseconds, for a line of a log. */
export function clockMs(t: Time | undefined | null): string {
  const at = parseTime(t);
  if (!finite(at)) {
    return NONE;
  }
  const ms = new Date(at).getMilliseconds();
  return `${clock(at)}.${ms < 10 ? "00" : ms < 100 ? "0" : ""}${ms}`;
}

/** The day and the time of a time: "2026-10-09 22:14:41". */
export function dateTime(t: Time | undefined | null): string {
  const at = parseTime(t);
  if (!finite(at)) {
    return NONE;
  }
  const d = new Date(at);
  return `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())} ${clock(at)}`;
}

/** The first ten characters of an endpoint ID. */
export function shortID(id: string | undefined | null): string {
  return typeof id === "string" && id !== "" ? id.slice(0, 10) : NONE;
}

/** A path of the Nix store without the store and the hash:
 *  "/nix/store/kzrv…-web-1.4.20/bin/web" is "web-1.4.20/bin/web". Any
 *  other path is itself. */
export function storeName(path: string | undefined | null): string {
  if (typeof path !== "string" || path === "") {
    return NONE;
  }
  const m = /^\/nix\/store\/[0-9a-z]{32}-(.+)$/.exec(path);
  return m?.[1] ?? path;
}

/** How a service ended: "exit 3", "killed by SIGKILL", "did not start". */
export function exit(e: Exit | undefined | null): string {
  if (typeof e !== "object" || e === null) {
    return NONE;
  }
  if (e.error) {
    return "did not start";
  }
  if (e.signal) {
    return `killed by ${e.signal}`;
  }
  if (!finite(e.status)) {
    return NONE;
  }
  return `exit ${e.status}`;
}

/** A whole number with its thousands apart: "1 200". The spaces do not
 *  break. */
export function count(n: number | undefined | null): string {
  if (!finite(n)) {
    return NONE;
  }
  return String(Math.round(n)).replace(/\B(?=(\d{3})+(?!\d))/g, " ");
}

/** A number with its noun: "1 zathras", "3 zathrases". */
export function plural(n: number, one: string, many: string): string {
  return `${count(n)} ${n === 1 ? one : many}`;
}

/** A generation: "20", or a dash for none (0). */
export function generation(g: number | undefined | null): string {
  return finite(g) && g > 0 ? String(g) : NONE;
}
