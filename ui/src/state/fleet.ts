// The fleet as the page holds it: ONE WebSocket to /api/watch for the
// whole page, and a store that every view reads. A new fleet changes in
// the store only what changed, so a list keeps its rows, and a row that
// is the same zathras stays the same element.

import { batch, createSignal } from "solid-js";
import { createStore, reconcile } from "solid-js/store";
import { type Connection, keep, type SocketLike, socketURL } from "../api/reconnect";
import { type Fleet, type ID, isFleet, isRefusal, normaliseFleet, type Service, type Zathras } from "../api/types";

/** A zathras of the store: key is its ID. */
export type ZathrasRow = Zathras & { key: string };

/** A service of the store: key is its name. reconcile matches the items
 *  of every list by this one property. */
export type ServiceRow = Omit<Service, "zathrases"> & { key: string; zathrases: ZathrasRow[] };

const [store, setStore] = createStore<{ services: ServiceRow[] }>({ services: [] });
const [connection, setConnection] = createSignal<Connection>("connecting");
const [received, setReceived] = createSignal(false);
const [freshAt, setFreshAt] = createSignal(0);
const [lives, setLives] = createSignal(0);
const [refusal, setRefusal] = createSignal<string>();
const [now, setNow] = createSignal(Date.now());

/** The fleet: the services by name, each with its zathrases by name and
 *  then by ID. While the connection is lost it is the last one. */
export const fleet: { readonly services: ServiceRow[] } = store;

export {
  /** The state of the connection to /api/watch. */
  connection,
  /** Whether a fleet has arrived since the page was opened. Before that
   *  no view may say that something is not there. */
  received,
  /** When the last fleet arrived, by the clock of the browser. */
  freshAt,
  /** How often the connection became live. It changes when draal is
   *  back, which is when a view asks again for what it asked once: the
   *  first fleet of the new connection is in the store by then. */
  lives,
  /** What draal said when it ended the socket of the fleet with a
   *  refusal, in its own sentence; undefined once a fleet has arrived
   *  after it. */
  refusal,
  /** The time of the browser, renewed every second: what "3 m ago" is
   *  counted from. */
  now,
};

/** The rows of a fleet, in the order the page keeps: services by name,
 *  zathrases by name and then by ID, whatever order they came in. */
export function rows(f: Fleet): ServiceRow[] {
  const by = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);
  return f.services
    .map((s) => ({
      ...s,
      key: s.name,
      zathrases: s.zathrases
        .map((z) => ({ ...z, key: z.id }))
        .sort((a, b) => by(a.name, b.name) || by(a.id, b.id)),
    }))
    .sort((a, b) => by(a.name, b.name));
}

/** The service of the name, if the fleet has it. */
export function findService(name: string): ServiceRow | undefined {
  return store.services.find((s) => s.name === name);
}

/** The zathras of the ID in the service, if it is connected. */
export function findZathras(service: string, id: ID): ZathrasRow | undefined {
  return findService(service)?.zathrases.find((z) => z.id === id);
}

/** Takes a message of /api/watch into the store, if it is a fleet, and
 *  says whether it was one. Anything else leaves the fleet as it is and
 *  as old as it is: the last message of a socket that draal ends for a
 *  failure is a refusal, of which the sentence is kept. */
export function take(data: unknown): boolean {
  if (typeof data !== "string") {
    return false;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(data);
  } catch {
    return false;
  }
  if (!isFleet(parsed)) {
    if (isRefusal(parsed)) {
      setRefusal(parsed.error.message);
    }
    return false;
  }
  batch(() => {
    setStore("services", reconcile(rows(normaliseFleet(parsed)), { key: "key" }));
    setFreshAt(Date.now());
    setReceived(true);
    setRefusal(undefined);
  });
  return true;
}

/** A message of the socket of the fleet: it is taken, and the first
 *  fleet of a connection is what makes the connection live. All of that
 *  changes in one, so whoever reacts to lives, to ask again for what it
 *  asked once, finds the new fleet in the store and not the one from
 *  before the loss. It says whether the message was a fleet. */
export function arrived(data: unknown): boolean {
  return batch(() => {
    if (!take(data)) {
      return false;
    }
    if (connection() !== "live") {
      setConnection("live");
      setLives((n) => n + 1);
    }
    return true;
  });
}

/** Opens the connection and keeps it. It returns what ends it. */
export function startFleet(): () => void {
  const kept = keep({
    // A WebSocket is a SocketLike but for the types of its events, which
    // say more than keep reads.
    open: () => new WebSocket(socketURL("/api/watch")) as unknown as SocketLike,
    onMessage: arrived,
    // arrived has said "live" already, together with the fleet that
    // made it so; what is left to say here is "lost".
    onState: setConnection,
  });
  // A page that is looked at again, or whose network is back, does not
  // wait for the end of a long pause.
  const hurry = () => {
    if (document.visibilityState === "visible") {
      kept.hurry();
    }
  };
  document.addEventListener("visibilitychange", hurry);
  window.addEventListener("online", hurry);
  const tick = setInterval(() => setNow(Date.now()), 1000);
  return () => {
    clearInterval(tick);
    document.removeEventListener("visibilitychange", hurry);
    window.removeEventListener("online", hurry);
    kept.stop();
  };
}
