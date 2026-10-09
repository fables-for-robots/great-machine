import { describe, expect, test } from "vitest";
import {
  ago,
  between,
  bytes,
  clock,
  clockMs,
  count,
  cpu,
  dateTime,
  duration,
  exit,
  generation,
  NONE,
  percent,
  plural,
  rate,
  shortID,
  span,
  storeName,
  until,
} from "./format";

const ZERO = "0001-01-01T00:00:00Z";
const NOW = Date.UTC(2026, 9, 9, 20, 0, 0);

describe("bytes and rates", () => {
  test("binary units, three figures", () => {
    expect(bytes(0)).toBe("0 B");
    expect(bytes(512)).toBe("512 B");
    expect(bytes(1024)).toBe("1 KiB");
    expect(bytes(1536)).toBe("1.5 KiB");
    expect(bytes(286297311)).toBe("273 MiB");
    expect(bytes(536870912)).toBe("512 MiB");
    expect(bytes(1024 ** 3 * 1.25)).toBe("1.25 GiB");
    expect(bytes(1024 * 1023.7)).toBe("1 MiB");
    expect(bytes(1024 ** 6 * 5)).toBe("5120 PiB");
  });

  test("a rate is bytes a second", () => {
    expect(rate(22494.46)).toBe("22 KiB/s");
    expect(rate(0)).toBe("0 B/s");
  });

  test("what is missing or cannot be is a dash", () => {
    for (const v of [undefined, null, NaN, Infinity, -1]) {
      expect(bytes(v)).toBe(NONE);
      expect(rate(v)).toBe(NONE);
      expect(cpu(v)).toBe(NONE);
      expect(percent(v)).toBe(NONE);
    }
  });
});

describe("cpu", () => {
  test("cores as a percentage of one core", () => {
    expect(cpu(0)).toBe("0%");
    expect(cpu(0.004)).toBe("0.4%");
    expect(cpu(0.0754)).toBe("7.5%");
    expect(cpu(0.7500719696731654)).toBe("75%");
    expect(cpu(2.5)).toBe("250%");
  });

  test("a share as a percentage", () => {
    expect(percent(0.533)).toBe("53%");
    expect(percent(1)).toBe("100%");
  });
});

describe("lengths of time", () => {
  test("the two largest units", () => {
    expect(span(0)).toBe("0 s");
    expect(span(0.4)).toBe("1 ms");
    expect(span(850)).toBe("850 ms");
    expect(span(1300)).toBe("1.3 s");
    expect(span(47_000)).toBe("47 s");
    expect(span(180_000)).toBe("3 m");
    expect(span(200_000)).toBe("3 m 20 s");
    expect(span(7_500_000)).toBe("2 h 5 m");
    expect(span(3 * 86_400_000 + 4 * 3_600_000)).toBe("3 d 4 h");
  });

  test("a negative length, of clocks that differ, is 0 s", () => {
    expect(span(-5000)).toBe("0 s");
    expect(duration(-1)).toBe("0 s");
  });

  test("a duration of the API is nanoseconds", () => {
    expect(duration(1_300_000_000)).toBe("1.3 s");
    expect(duration(2_816_000_000_000)).toBe("46 m 56 s");
    expect(duration(undefined)).toBe(NONE);
    expect(duration(NaN)).toBe(NONE);
  });

  test("ago, in the largest unit", () => {
    expect(ago("2026-10-09T19:59:13Z", NOW)).toBe("47 s ago");
    expect(ago("2026-10-09T21:57:00+02:00", NOW)).toBe("3 m ago");
    expect(ago("2026-10-09T17:55:00Z", NOW)).toBe("2 h ago");
    expect(ago("2026-10-06T20:00:00Z", NOW)).toBe("3 d ago");
    expect(ago(NOW - 5000, NOW)).toBe("5 s ago");
  });

  test("ago of the zero time is a dash, and of the future is now", () => {
    expect(ago(ZERO, NOW)).toBe(NONE);
    expect(ago(undefined, NOW)).toBe(NONE);
    expect(ago("nonsense", NOW)).toBe(NONE);
    expect(ago("2026-10-09T20:00:07Z", NOW)).toBe("now");
    expect(ago("2026-10-09T20:00:00Z", NaN)).toBe(NONE);
  });

  test("until", () => {
    expect(until("2026-10-09T20:00:06Z", NOW)).toBe("in 6 s");
    expect(until("2026-10-09T19:59:00Z", NOW)).toBe("now");
    expect(until(ZERO, NOW)).toBe(NONE);
  });

  test("between two times", () => {
    expect(between("2026-10-09T20:00:00Z", "2026-10-09T20:00:34Z")).toBe("34 s");
    expect(between("2026-10-09T20:00:00Z", ZERO)).toBe(NONE);
    expect(between("2026-10-09T20:00:34Z", "2026-10-09T20:00:00Z")).toBe("0 s");
  });
});

describe("times", () => {
  // The clock of the reader is that of the machine the test runs on:
  // the expectations are made the same way.
  const at = "2026-10-09T22:14:41.284222+02:00";
  const d = new Date(Date.parse(at));
  const two = (n: number) => String(n).padStart(2, "0");
  const hms = `${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}`;

  test("the time of day", () => {
    expect(clock(at)).toBe(hms);
    expect(clockMs(at)).toBe(`${hms}.284`);
    expect(clockMs("2026-10-09T22:14:41.004+02:00")).toBe(`${hms}.004`);
    expect(clockMs("2026-10-09T22:14:41+02:00")).toBe(`${hms}.000`);
  });

  test("the day and the time", () => {
    expect(dateTime(at)).toBe(`${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())} ${hms}`);
  });

  test("the zero time and nonsense are a dash, never Invalid Date", () => {
    for (const v of [ZERO, undefined, null, "", "nonsense"]) {
      expect(clock(v)).toBe(NONE);
      expect(clockMs(v)).toBe(NONE);
      expect(dateTime(v)).toBe(NONE);
    }
    expect(clock(NaN)).toBe(NONE);
  });
});

describe("names", () => {
  test("a short ID is the first ten characters", () => {
    expect(shortID("3f08d61e04d64c081e7eff0c98be5343638e90c39d73b9088b87da7da0dd1eb1")).toBe("3f08d61e04");
    expect(shortID("")).toBe(NONE);
    expect(shortID(undefined)).toBe(NONE);
  });

  test("a store path loses the store and its hash", () => {
    expect(storeName("/nix/store/kzrvvx11ilxcm7cr7zndjxh2kav4c5ra-web-1.4.20/bin/web")).toBe("web-1.4.20/bin/web");
    expect(storeName("/usr/bin/env")).toBe("/usr/bin/env");
    expect(storeName(undefined)).toBe(NONE);
    expect(storeName("")).toBe(NONE);
  });

  test("an exit", () => {
    const base = { generation: 4, at: "2026-10-09T20:00:00Z", ran: 1_300_000_000 };
    expect(exit({ ...base, status: 3 })).toBe("exit 3");
    expect(exit({ ...base, status: 0 })).toBe("exit 0");
    expect(exit({ ...base, status: -1, signal: "SIGKILL" })).toBe("killed by SIGKILL");
    expect(exit({ ...base, status: -1, error: "exec format error" })).toBe("did not start");
    expect(exit(undefined)).toBe(NONE);
    expect(exit({ ...base, status: NaN })).toBe(NONE);
  });

  test("counts", () => {
    expect(count(1200)).toBe("1 200");
    expect(count(36)).toBe("36");
    expect(count(1234567)).toBe("1 234 567");
    expect(count(undefined)).toBe(NONE);
    expect(plural(1, "zathras", "zathrases")).toBe("1 zathras");
    expect(plural(0, "zathras", "zathrases")).toBe("0 zathrases");
  });

  test("a generation, and none", () => {
    expect(generation(20)).toBe("20");
    expect(generation(0)).toBe(NONE);
    expect(generation(undefined)).toBe(NONE);
  });
});

test("no formatter ever writes NaN, undefined or Invalid Date", () => {
  const bad: unknown[] = [undefined, null, NaN, Infinity, -Infinity, -1, "", "nonsense", ZERO, {}];
  const fns: ((v: never) => string)[] = [
    bytes,
    rate,
    cpu,
    percent,
    span,
    duration,
    clock,
    clockMs,
    dateTime,
    shortID,
    storeName,
    exit,
    count,
    generation,
    (v: never) => ago(v, NOW),
    (v: never) => ago("2026-10-09T20:00:00Z", v),
    (v: never) => until(v, NOW),
    (v: never) => between(v, v),
  ];
  for (const f of fns) {
    for (const v of bad) {
      const s = f(v as never);
      expect(typeof s).toBe("string");
      expect(s).not.toMatch(/NaN|undefined|Invalid|null|object/);
    }
  }
});
