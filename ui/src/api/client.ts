// What the page asks once: the JSON endpoints of the API.

import {
  type Grant,
  type GrantList,
  type GrantRequest,
  type Identity,
  isRefusal,
  normaliseGrants,
  normaliseIdentity,
  type RolloutList,
  type SampleList,
} from "./types";

/** Why a request was not answered with what was asked for. */
export class ApiError extends Error {
  /** The code of a refusal ("not-found", "unavailable", …); "signed-out"
   *  for a 401, which is the cookie of the page being no longer good;
   *  "http" for an answer that is no refusal, as a line of a proxy in
   *  front of the page; "network" when there was no answer. */
  readonly code: string;
  /** The HTTP status; 0 when there was no answer. */
  readonly status: number;

  constructor(code: string, message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
  }
}

/** The status of an answer to a page whose cookie is no longer good:
 *  `great-machine web` was started again, and has another. It is no
 *  refusal of draal, which is a 403. */
export const SIGNED_OUT = 401;

/** What an answer that is not the one asked for becomes: a refusal with
 *  its code and message, anything else with its text. */
export function errorOf(status: number, body: string): ApiError {
  try {
    const v: unknown = JSON.parse(body);
    if (isRefusal(v)) {
      return new ApiError(String(v.error.code || "internal"), v.error.message, status);
    }
  } catch {
    // It is no JSON: its text is what there is to say.
  }
  const text = body.trim();
  // A page of HTML is not something to show as a sentence.
  const plain = text !== "" && !text.startsWith("<") ? text.slice(0, 600) : `draal answered with status ${status}.`;
  return new ApiError("http", plain, status);
}

let signedOut: (() => void) | undefined;

/** Has listener told whenever a request is answered with a 401: the
 *  cookie of the page is no longer good. There is one listener, and
 *  undefined is none. */
export function onSignedOut(listener: (() => void) | undefined): void {
  signedOut = listener;
}

/** Asks for a path of the API with the method, and with body as JSON if
 *  there is one, and returns the text of the answer, which is empty for
 *  a 204. It rejects with an ApiError and nothing else. */
async function ask(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<string> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  let response: Response;
  try {
    response = await fetch(path, { method, signal, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") {
      throw new ApiError("aborted", "The request was given up.", 0);
    }
    throw new ApiError("network", "draal does not answer.", 0);
  }
  let text: string;
  try {
    text = await response.text();
  } catch {
    throw new ApiError("network", "The answer of draal broke off.", response.status);
  }
  if (response.status === SIGNED_OUT) {
    signedOut?.();
    throw new ApiError("signed-out", "This page is no longer signed in.", response.status);
  }
  if (!response.ok) {
    throw errorOf(response.status, text);
  }
  return text;
}

/** Asks for a path of the API and returns the JSON of the answer. It
 *  rejects with an ApiError and nothing else. */
export async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const body = await ask("GET", path, undefined, signal);
  try {
    return JSON.parse(body) as T;
  } catch {
    throw new ApiError("http", "draal answered with something that is no JSON.", 200);
  }
}

/** The last rollouts of a service, newest first, at most 20. */
export async function rollouts(service: string, signal?: AbortSignal): Promise<RolloutList> {
  const list = await getJSON<RolloutList>(`/api/services/${encodeURIComponent(service)}/rollouts`, signal);
  return { rollouts: Array.isArray(list?.rollouts) ? list.rollouts : [] };
}

/** The samples draal keeps of a zathras, oldest first. */
export async function history(id: string, signal?: AbortSignal): Promise<SampleList> {
  const list = await getJSON<SampleList>(`/api/zathras/${encodeURIComponent(id)}/history`, signal);
  return { samples: Array.isArray(list?.samples) ? list.samples : [] };
}

/** Who the key is that the page asks draal with: its endpoint ID and its
 *  grants, of which a key may have none. */
export async function me(signal?: AbortSignal): Promise<Identity> {
  return normaliseIdentity(await getJSON<unknown>("/api/me", signal));
}

/** Every grant draal has. It is an admin's to ask: any other key is
 *  refused. */
export async function grants(signal?: AbortSignal): Promise<GrantList> {
  const list = await getJSON<{ grants?: unknown }>("/api/grants", signal);
  return { grants: normaliseGrants(list?.grants) };
}

/** Grants a role. A grant that is there already is there with the new
 *  note. */
export async function grant(request: GrantRequest, signal?: AbortSignal): Promise<void> {
  await ask("POST", "/api/grants", request, signal);
}

/** The query that names a grant to revoke: its ID, its role and its
 *  service, which is empty for every service and for an admin. */
export function revokeQuery(g: Pick<Grant, "id" | "role" | "service">): string {
  const service = g.role === "admin" ? "" : (g.service ?? "");
  return `id=${encodeURIComponent(g.id)}&role=${encodeURIComponent(g.role)}&service=${encodeURIComponent(service)}`;
}

/** Revokes a grant. draal has ended what the grant was used for when it
 *  answers. */
export async function revoke(g: Pick<Grant, "id" | "role" | "service">, signal?: AbortSignal): Promise<void> {
  await ask("DELETE", `/api/grants?${revokeQuery(g)}`, undefined, signal);
}
