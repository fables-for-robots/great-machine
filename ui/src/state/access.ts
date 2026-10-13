// What the page works out of grants: how one is written, what the
// header says of a key, which grants can be revoked, and what the form
// sends. Every function here is pure.

import { type Grant, type GrantRequest, type ID, isRole, parseTime, ROLES } from "../api/types";
import { ago, NONE, shortID } from "../format";

/** What stands for every service where one is named. */
export const EVERY = "every";

/** How many services the header names before it counts the rest. */
const NAMED = 3;

function isEvery(g: Grant): boolean {
  // An admin is of every service, whatever its grant says.
  return g.role === "admin" || g.service === undefined || g.service === "";
}

/** The service of a grant as a column has it: its name, or "every". */
export function scope(g: Grant): string {
  return isEvery(g) ? EVERY : (g.service as string);
}

/** A grant in a few words: "deployer of web", "zathras of every
 *  service", "admin". A role the page does not know is written as it
 *  came. */
export function describeGrant(g: Grant): string {
  if (g.role === "admin") {
    return "admin";
  }
  return `${g.role || "no role"} of ${isEvery(g) ? "every service" : g.service}`;
}

/** Who granted it: the beginning of the ID of an admin, or draal's
 *  command line. */
export function grantedBy(g: Grant): string {
  if (g.fixed) {
    return "draal's command line";
  }
  return shortID(g.by);
}

/** How long ago it was granted, by the clock of now (milliseconds). An
 *  admin of the command line was granted at no time. */
export function grantedWhen(g: Grant, now: number): string {
  if (g.fixed || parseTime(g.at) === undefined) {
    return NONE;
  }
  return ago(g.at, now);
}

/** What tells a grant from every other: its ID, its role and its
 *  service. */
export function grantKey(g: Grant): string {
  return `${g.id}|${g.role}|${isEvery(g) ? "" : g.service}`;
}

/** Says whether the grants are an admin's. */
export function isAdmin(grants: readonly Grant[]): boolean {
  return grants.some((g) => g.role === "admin");
}

/** What the header says of a key with the grants: "admin" for an admin,
 *  whatever else it holds; otherwise each role with its services, by
 *  name, the first three named and the rest counted; "no role" for a key
 *  that was granted nothing. */
export function rolesLine(grants: readonly Grant[]): string {
  if (grants.length === 0) {
    return "no role";
  }
  if (isAdmin(grants)) {
    return "admin";
  }
  const order = [...ROLES.filter((r) => r !== "admin"), ...new Set(grants.map((g) => g.role).filter((r) => !isRole(r)))];
  const parts: string[] = [];
  for (const role of order) {
    const of = grants.filter((g) => g.role === role);
    if (of.length === 0) {
      continue;
    }
    if (of.some(isEvery)) {
      parts.push(`${role} of every service`);
      continue;
    }
    const services = [...new Set(of.map((g) => g.service as string))].sort();
    const named = services.slice(0, NAMED).join(", ");
    parts.push(`${role} of ${named}${services.length > NAMED ? ` and ${services.length - NAMED} more` : ""}`);
  }
  return parts.join("; ");
}

/** Says whether a request can revoke the grant: not an admin of draal's
 *  command line, and not the admin grant of the key that asks, which
 *  draal lets no admin take from itself. me is the ID of that key. */
export function revocable(g: Grant, me: ID | undefined): boolean {
  if (g.fixed) {
    return false;
  }
  return !(g.role === "admin" && me !== undefined && me !== "" && g.id === me);
}

/** Why a grant has no way to be revoked, for the place of the button. */
export function whyNotRevocable(g: Grant): string {
  return g.fixed ? "Of draal's command line: no request revokes it." : "Your own: an admin does not revoke itself.";
}

/** What the form holds, as it was typed. */
export interface FormFields {
  role: string;
  id: string;
  service: string;
  note: string;
}

/** What comes of a form: the request to send, or why there is none. */
export type FormResult = { ok: true; request: GrantRequest } | { ok: false; problem: string };

/** The request a form sends. What was typed around a value is taken off,
 *  and an ID in capitals is written as draal writes one. No service is
 *  every service and is left out, as it is for an admin, whatever the
 *  field held; an empty note is left out. The page judges the ID alone:
 *  whether a service is one is draal's to say, in its own words. */
export function formRequest(fields: FormFields): FormResult {
  if (!isRole(fields.role)) {
    return { ok: false, problem: "Choose a role: admin, deployer or zathras." };
  }
  const id = fields.id.trim().toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(id)) {
    return {
      ok: false,
      problem:
        id === ""
          ? "The ID of the key is missing: 64 hexadecimal characters, as `great-machine id` prints them."
          : `That is no endpoint ID: it has ${id.length} characters, and an ID is 64 hexadecimal ones, as \`great-machine id\` prints them.`,
    };
  }
  const request: GrantRequest = { id, role: fields.role };
  const service = fields.service.trim();
  if (fields.role !== "admin" && service !== "") {
    request.service = service;
  }
  const note = fields.note.trim();
  if (note !== "") {
    request.note = note;
  }
  return { ok: true, request };
}
