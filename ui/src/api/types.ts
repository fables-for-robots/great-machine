// The types of the HTTP API, as draal's JSON has them: one for each type
// of wire/messages.go that the API sends, with the names of its json
// tags.
//
// A time is a string in RFC 3339 with 0 to 9 fractional digits and an
// offset or Z. The zero time, "0001-01-01T00:00:00Z", stands for "none":
// isZeroTime recognises it and parseTime gives undefined for it. A
// duration is a number of nanoseconds. A field with a question mark is
// absent from the JSON when it is empty.

/** An endpoint ID: 64 lowercase hexadecimal characters. */
export type ID = string;

/** A time as the JSON has it. */
export type Time = string;

/** A duration: nanoseconds. */
export type Duration = number;

/** What a zathras is doing with its service. */
export type Phase = "idle" | "running" | "stopping" | "backoff";

/** How a service ended. */
export interface Exit {
  generation: number;
  /** The exit status; -1 when a signal ended it or it never started. */
  status: number;
  /** Such as "SIGKILL". */
  signal?: string;
  /** Why the binary could not be started. */
  error?: string;
  at: Time;
  ran: Duration;
}

/** The figures of a container at one time. */
export interface Sample {
  at: Time;
  /** Cores. */
  cpu: number;
  /** Bytes. */
  memory: number;
  /** Bytes; 0 for none. */
  memory_limit: number;
  /** Bytes per second, as the three below. */
  disk_read: number;
  disk_write: number;
  net_rx: number;
  net_tx: number;
}

/** What a zathras reports. */
export interface State {
  /** 0 for none. */
  generation: number;
  binary?: string;
  args?: string[];
  phase: Phase;
  /** While running. */
  pid?: number;
  /** While running. */
  running_for?: Duration;
  /** While in backoff; the zero time otherwise. */
  restart_at: Time;
  last_exit?: Exit;
  restarts: number;
  /** The generations that were prepared, ascending. */
  prepared?: number[];
  /** The generation being prepared, if one is. */
  preparing?: number;
  /** The latest sample, if the platform gives one. */
  sample?: Sample;
}

/** A connected zathras and what it reported last. */
export interface Zathras {
  id: ID;
  name: string;
  connected: Time;
  state: State;
}

/** How far a rollout is. */
export type RolloutState = "preparing" | "activating" | "completed" | "halted" | "superseded";

/** A rollout as draal records it. */
export interface RolloutInfo {
  id: number;
  service: string;
  generation: number;
  parallel: number;
  state: RolloutState;
  /** Why it was halted. */
  reason?: string;
  created: Time;
  created_by: ID;
  /** The zero time while under way. */
  finished: Time;
  /** Of the connected zathrases, as draal decided last. In a Fleet alone:
   *  the list of /api/services/{service}/rollouts has them as 0. */
  prepared: number;
  activated: number;
  awaited: number;
}

/** A service, its last rollout and its connected zathrases. */
export interface Service {
  name: string;
  /** The stable generation; 0 for none. */
  stable: number;
  /** The last rollout; absent before the first. */
  rollout?: RolloutInfo;
  /** By name, then by ID. draal sends a list always; the type of the
   *  sender has no guard, so normaliseFleet takes null for an empty one. */
  zathrases: Zathras[];
}

/** Every service draal knows: a message of /api/watch. */
export interface Fleet {
  services: Service[];
}

/** The answer of /api/services/{service}/rollouts: newest first. */
export interface RolloutList {
  rollouts: RolloutInfo[];
}

/** The answer of /api/zathras/{id}/history: oldest first. */
export interface SampleList {
  samples: Sample[];
}

/** A message of /api/zathras/{id}/logs. One with lost set has no text: so
 *  many lines were lost before the line that follows. An empty line has
 *  no text either. */
export interface LogLine {
  at: Time;
  stream: "stdout" | "stderr" | "zathras" | string;
  text?: string;
  lost?: number;
}

/** What a key was granted. An admin does everything; a deployer pushes
 *  and rolls out, and looks at what it rolled out; the grant of a zathras
 *  is held by a role key, which signs for the pods of a service. */
export type Role = "admin" | "deployer" | "zathras";

/** A role that a key was given, for a service or for every service. A
 *  key has as many grants as it was given. */
export interface Grant {
  id: ID;
  /** One of the three roles. draal sends no other; the type of the
   *  sender has no guard, so the page writes one it does not know as it
   *  came. */
  role: Role | string;
  /** The service the role is of; absent for every service, which an
   *  admin always is of. */
  service?: string;
  /** What a person wrote beside the grant. */
  note?: string;
  /** The admin who granted it; absent for an admin of draal's command
   *  line. */
  by?: ID;
  /** When it was granted; the zero time for an admin of the command
   *  line. */
  at: Time;
  /** An admin of draal's command line, which no request revokes. */
  fixed?: boolean;
}

/** The answer of /api/grants: the admins of the command line first, then
 *  the grants by role, service and ID. */
export interface GrantList {
  grants: Grant[];
}

/** The answer of /api/me: the endpoint ID of the key the page asks draal
 *  with, and what it was granted. */
export interface Identity {
  id: ID;
  grants: Grant[];
}

/** What is sent to /api/grants to grant a role. No service is every
 *  service. */
export interface GrantRequest {
  id: ID;
  role: Role;
  service?: string;
  note?: string;
}

/** The roles, in the order the page names them. */
export const ROLES: readonly Role[] = ["admin", "deployer", "zathras"];

/** Says whether r is one of the three roles. */
export function isRole(r: unknown): r is Role {
  return r === "admin" || r === "deployer" || r === "zathras";
}

/** The grants of an answer as a list: null and what is no list are an
 *  empty one, and what is no grant (it has no ID or no role) is left
 *  out. */
export function normaliseGrants(v: unknown): Grant[] {
  if (!Array.isArray(v)) {
    return [];
  }
  return v.filter((g): g is Grant => {
    if (typeof g !== "object" || g === null) {
      return false;
    }
    const { id, role } = g as { id?: unknown; role?: unknown };
    return typeof id === "string" && typeof role === "string";
  });
}

/** Who a key is, of an answer of /api/me, with its grants a list. */
export function normaliseIdentity(v: unknown): Identity {
  const raw = typeof v === "object" && v !== null ? (v as { id?: unknown; grants?: unknown }) : {};
  return { id: typeof raw.id === "string" ? raw.id : "", grants: normaliseGrants(raw.grants) };
}

/** A refusal: the body of an HTTP answer that is one, and the last
 *  message of a socket that ends with one. */
export interface Refusal {
  error: { code: string; message: string };
}

/** The message that ends a shell's socket when the shell has ended. */
export interface ShellExit {
  exit: number;
}

/** Says whether s is under way: preparing or activating. */
export function underWay(s: RolloutState): boolean {
  return s === "preparing" || s === "activating";
}

/** Says whether t is the zero time, which stands for "none". */
export function isZeroTime(t: Time | undefined | null): boolean {
  return typeof t !== "string" || t === "" || t.startsWith("0001-01-01T00:00:00");
}

/** The time as milliseconds since 1970, with the fraction of a
 *  millisecond that the text has; undefined for the zero time and for
 *  what is no time. */
export function parseTime(t: Time | undefined | null): number | undefined {
  if (typeof t !== "string" || isZeroTime(t)) {
    return undefined;
  }
  const ms = Date.parse(t);
  if (!Number.isFinite(ms)) {
    return undefined;
  }
  // Date.parse stops at the millisecond; the digits after it order two
  // lines of a log that were written within one.
  const fraction = /\.(\d+)/.exec(t)?.[1] ?? "";
  const below = fraction.length > 3 ? Number("0." + fraction.slice(3)) : 0;
  return ms + below;
}

/** Says whether id is written as an endpoint ID is. */
export function isID(id: string): boolean {
  return /^[0-9a-f]{64}$/.test(id);
}

/** Says whether v is a refusal. */
export function isRefusal(v: unknown): v is Refusal {
  if (typeof v !== "object" || v === null || !("error" in v)) {
    return false;
  }
  const e = (v as { error: unknown }).error;
  return typeof e === "object" && e !== null && typeof (e as { message?: unknown }).message === "string";
}

/** Says whether v is a fleet as /api/watch sends one: an object that is
 *  no refusal and has services, whatever they are (normaliseFleet makes
 *  a list of them). The last message of a socket that draal ends for a
 *  failure is a refusal, and is none. */
export function isFleet(v: unknown): v is { services: unknown } {
  return typeof v === "object" && v !== null && !Array.isArray(v) && !isRefusal(v) && "services" in v;
}

/** The fleet of a message, with every list a list: services and
 *  zathrases that are null or absent are empty. This is the one place
 *  where the page distrusts the shape of what draal sends. */
export function normaliseFleet(v: unknown): Fleet {
  const raw = typeof v === "object" && v !== null ? (v as { services?: unknown }).services : undefined;
  const services = Array.isArray(raw) ? (raw as Service[]) : [];
  return {
    services: services.map((s) => ({ ...s, zathrases: Array.isArray(s.zathrases) ? s.zathrases : [] })),
  };
}
