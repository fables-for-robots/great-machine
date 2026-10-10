// What the page asks once: the JSON endpoints of the API.

import { isRefusal, type RolloutList, type SampleList } from "./types";

/** Why a request was not answered with what was asked for. */
export class ApiError extends Error {
  /** The code of a refusal ("not-found", "unavailable", …); "http" for
   *  an answer that is no refusal, as a line of a proxy in front of draal;
   *  "network" when there was no answer. */
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

/** Asks for a path of the API and returns the JSON of the answer. It
 *  rejects with an ApiError and nothing else. */
export async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, { signal, headers: { Accept: "application/json" } });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") {
      throw new ApiError("aborted", "The request was given up.", 0);
    }
    throw new ApiError("network", "draal does not answer.", 0);
  }
  let body: string;
  try {
    body = await response.text();
  } catch {
    throw new ApiError("network", "The answer of draal broke off.", response.status);
  }
  if (!response.ok) {
    throw errorOf(response.status, body);
  }
  try {
    return JSON.parse(body) as T;
  } catch {
    throw new ApiError("http", "draal answered with something that is no JSON.", response.status);
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
