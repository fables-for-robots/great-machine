import { A } from "@solidjs/router";
import { createEffect, createSignal, For, Match, on, onCleanup, Show, Switch } from "solid-js";
import { ApiError, grant as askGrant, grants as askGrants, revoke as askRevoke } from "../api/client";
import { type Grant, type Role, ROLES } from "../api/types";
import Notice from "../components/Notice";
import ShortID from "../components/ShortID";
import { dateTime, NONE, plural, shortID } from "../format";
import {
  describeGrant,
  EVERY,
  formRequest,
  grantedBy,
  grantedWhen,
  grantKey,
  isAdmin,
  revocable,
  rolesLine,
  scope,
  whyNotRevocable,
} from "../state/access";
import { lives, now } from "../state/fleet";
import { askMe, me, meFailure } from "../state/session";
import "./service.css";
import "./access.css";

// What each role is, in the words of the form.
const ROLE_HINT: Record<Role, string> = {
  admin: "Does everything, on every service, and grants.",
  deployer: "Pushes and rolls out, reads the logs and opens shells, of the service.",
  zathras: "For a role key: the pods of the service register with what it signs.",
};

// What an error is as a sentence: draal's own words when it refused.
function sentence(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// The form that grants: the role as a choice of three, the ID of the
// key, the service, which an admin has none of, and a note. What draal
// refuses is said under it, in draal's words, and the form stays as it
// was filled.
function GrantForm(props: { onGranted: () => void }) {
  const [role, setRole] = createSignal<Role>("deployer");
  const [id, setID] = createSignal("");
  const [service, setService] = createSignal("");
  const [note, setNote] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [problem, setProblem] = createSignal<string>();
  const [done, setDone] = createSignal<string>();

  const submit = (event: SubmitEvent) => {
    event.preventDefault();
    if (busy()) {
      return;
    }
    setDone(undefined);
    const result = formRequest({ role: role(), id: id(), service: service(), note: note() });
    if (!result.ok) {
      setProblem(result.problem);
      return;
    }
    const request = result.request;
    setProblem(undefined);
    setBusy(true);
    askGrant(request).then(
      () => {
        setBusy(false);
        setDone(`Granted: ${describeGrant({ ...request, at: "" })} to ${shortID(request.id)}.`);
        // The key and the note are of this grant; the role and the
        // service are likely those of the next.
        setID("");
        setNote("");
        props.onGranted();
      },
      (err: unknown) => {
        setBusy(false);
        setProblem(sentence(err));
      },
    );
  };

  return (
    <form class="panel grant-form" onSubmit={submit} noValidate>
      <fieldset class="roles">
        <legend class="label">Role</legend>
        <For each={ROLES}>
          {(r) => (
            <label class="role-choice" data-chosen={role() === r ? "" : undefined}>
              <input type="radio" name="role" value={r} checked={role() === r} onChange={() => setRole(r)} />
              <span class="role-name">{r}</span>
              <span class="muted">{ROLE_HINT[r]}</span>
            </label>
          )}
        </For>
      </fieldset>
      <div class="fields">
        <label class="field wide">
          <span class="label">ID of the key</span>
          <input
            class="input mono"
            name="id"
            value={id()}
            onInput={(e) => setID(e.currentTarget.value)}
            placeholder="64 hexadecimal characters, as `great-machine id` prints them"
            autocomplete="off"
            autocapitalize="off"
            spellcheck={false}
          />
        </label>
        <Show when={role() !== "admin"}>
          <label class="field">
            <span class="label">Service</span>
            <input
              class="input"
              name="service"
              value={service()}
              onInput={(e) => setService(e.currentTarget.value)}
              placeholder="every service"
              autocomplete="off"
              autocapitalize="off"
              spellcheck={false}
            />
          </label>
        </Show>
        <label class="field">
          <span class="label">Note</span>
          <input
            class="input"
            name="note"
            value={note()}
            onInput={(e) => setNote(e.currentTarget.value)}
            placeholder="whose key it is"
            maxLength={200}
            autocomplete="off"
          />
        </label>
      </div>
      <div class="grant-act">
        <button type="submit" class="button" disabled={busy()}>
          {busy() ? "Granting…" : "Grant"}
        </button>
        <Show when={role() !== "admin" && service().trim() === ""}>
          <span class="muted">Without a service the grant is for every service.</span>
        </Show>
      </div>
      <Show when={problem()}>
        <p class="reason" role="alert">
          {problem()}
        </p>
      </Show>
      <Show when={done()}>
        <p class="granted" role="status">
          {done()}
        </p>
      </Show>
    </form>
  );
}

// A grant as a row of the table, with the way to revoke it if it has
// one: a button that asks first. What the row is asked, and what draal
// said of it, is the table's to keep: the table is read again after
// every change, and its rows are made anew.
function Row(props: {
  grant: Grant;
  /** The ID of the key the page asks draal with. */
  me: string;
  /** This row is asked whether its grant is to be revoked. */
  asking: boolean;
  /** A revocation is under way, of this grant or of another. */
  busy: boolean;
  /** What draal said when it refused to revoke this grant. */
  problem: string | undefined;
  onAsk: () => void;
  onKeep: () => void;
  onRevoke: () => void;
}) {
  const g = () => props.grant;
  return (
    <tr data-fixed={g().fixed ? "" : undefined}>
      <td data-label="Role">
        <span class="role" data-role={g().role}>
          {g().role || "none"}
        </span>
      </td>
      <td data-label="Service">
        <Show when={scope(g()) !== EVERY} fallback={<span class="muted">{EVERY}</span>}>
          {g().service}
        </Show>
      </td>
      <td data-label="Key" class="nw">
        <ShortID id={g().id} />
        <Show when={g().id === props.me}>
          {" "}
          <span class="tag">this key</span>
        </Show>
      </td>
      <td data-label="Note" class="why">
        <Show when={g().note} fallback={<span class="muted">{NONE}</span>}>
          {g().note}
        </Show>
      </td>
      <td data-label="Granted by" class="nw">
        <Show when={!g().fixed && g().by} fallback={grantedBy(g())}>
          {(by) => <ShortID id={by()} />}
        </Show>
      </td>
      <td data-label="When" class="nw" title={g().fixed ? undefined : dateTime(g().at)}>
        {grantedWhen(g(), now())}
      </td>
      <td data-label="Revoke" class="act">
        <Switch>
          <Match when={!revocable(g(), props.me)}>
            <span class="muted" title={whyNotRevocable(g())}>
              {g().fixed ? "command line" : "your own"}
            </span>
          </Match>
          <Match when={props.asking}>
            <span class="confirm" role="group" aria-label={`Revoke ${describeGrant(g())}?`}>
              <span>Revoke {describeGrant(g())}?</span>
              <button type="button" class="button quiet danger" disabled={props.busy} onClick={props.onRevoke}>
                {props.busy ? "Revoking…" : "Revoke"}
              </button>
              <button type="button" class="button quiet" disabled={props.busy} onClick={props.onKeep}>
                Keep
              </button>
            </span>
          </Match>
          <Match when={true}>
            <button type="button" class="button quiet" disabled={props.busy} onClick={props.onAsk}>
              Revoke
            </button>
          </Match>
        </Switch>
        <Show when={props.problem}>
          <p class="reason" role="alert">
            {props.problem}
          </p>
        </Show>
      </td>
    </tr>
  );
}

// The view of an admin: the form, and every grant draal has.
function Grants(props: { me: string }) {
  const [list, setList] = createSignal<Grant[]>();
  const [failure, setFailure] = createSignal<string>();
  // Counted up when the table is to be read again: after a grant and
  // after a revocation.
  const [turn, setTurn] = createSignal(0);
  // The grant that is asked about, the one that is being revoked, and
  // what draal said of the one it would not revoke: each by grantKey.
  const [asking, setAsking] = createSignal<string>();
  const [busy, setBusy] = createSignal<string>();
  const [problem, setProblem] = createSignal<{ key: string; text: string }>();

  const changed = () => {
    setTurn((n) => n + 1);
    // The grant may have been one of this page's own key.
    void askMe();
  };

  const revoke = (g: Grant) => {
    if (busy() !== undefined) {
      return;
    }
    const key = grantKey(g);
    setBusy(key);
    setProblem(undefined);
    askRevoke(g).then(
      () => {
        setBusy(undefined);
        setAsking(undefined);
        changed();
      },
      (err: unknown) => {
        setBusy(undefined);
        setAsking(undefined);
        setProblem({ key, text: sentence(err) });
        // draal ends the connections of the key whose grant it revokes
        // before it answers: a revocation of a grant of this page's own
        // key may have been done and not been answered. The table says
        // which it was: a grant that is gone has no row to say it under.
        changed();
      },
    );
  };

  createEffect(
    on([lives, turn], () => {
      const abort = new AbortController();
      onCleanup(() => abort.abort());
      askGrants(abort.signal).then(
        (answer) => {
          if (abort.signal.aborted) {
            return;
          }
          setList(answer.grants);
          setFailure(undefined);
        },
        (err: unknown) => {
          if (abort.signal.aborted || (err instanceof ApiError && err.code === "aborted")) {
            return;
          }
          setFailure(sentence(err));
        },
      );
    }),
  );

  const keys = () => new Set((list() ?? []).map((g) => g.id)).size;

  return (
    <>
      <div class="head">
        <h1>Access</h1>
        <Show when={list()}>
          {(l) => (
            <span class="muted">
              {plural(l().length, "grant", "grants")} of {plural(keys(), "key", "keys")}
            </span>
          )}
        </Show>
      </div>

      <section>
        <h2 class="access-h">Grant a role</h2>
        <GrantForm onGranted={changed} />
      </section>

      <section class="section">
        <h2>Grants</h2>
        <Show when={failure()}>
          <Notice kind="bad" small>
            <p>The grants could not be asked for: {failure()}</p>
          </Notice>
        </Show>
        <Switch>
          <Match when={list() === undefined}>
            <Show when={!failure()}>
              <p class="muted">Asking draal…</p>
            </Show>
          </Match>
          <Match when={list()!.length === 0}>
            <p class="muted">draal has granted nothing.</p>
          </Match>
          <Match when={true}>
            <div class="scroll">
              <table class="table grants">
                <thead>
                  <tr>
                    <th>Role</th>
                    <th>Service</th>
                    <th>Key</th>
                    <th>Note</th>
                    <th>Granted by</th>
                    <th>When</th>
                    <th>
                      <span class="sr">Revoke</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <For each={list()}>
                    {(g) => {
                      const key = grantKey(g);
                      return (
                        <Row
                          grant={g}
                          me={props.me}
                          asking={asking() === key}
                          busy={busy() !== undefined}
                          problem={problem()?.key === key ? problem()?.text : undefined}
                          onAsk={() => {
                            setProblem(undefined);
                            setAsking(key);
                          }}
                          onKeep={() => setAsking(undefined)}
                          onRevoke={() => revoke(g)}
                        />
                      );
                    }}
                  </For>
                </tbody>
              </table>
            </div>
            <p class="muted access-key">
              A revocation ends what the grant was used for: draal closes the connections of the key, and of every zathras
              a role key signed for. An admin of draal's command line is no grant of the table, and an admin does not revoke
              its own.
            </p>
          </Match>
        </Switch>
      </section>
    </>
  );
}

// View 4: who may do what. It is an admin's: every other key is told so,
// with who it is.
export default function Access() {
  return (
    <Switch>
      <Match when={me() === undefined}>
        <div class="head">
          <h1>Access</h1>
        </div>
        <Show when={meFailure()} fallback={<p class="muted">Asking draal who this key is…</p>}>
          <Notice kind="bad" small>
            <p>draal could not be asked who this key is: {meFailure()}</p>
          </Notice>
        </Show>
      </Match>
      <Match when={!isAdmin(me()!.grants)}>
        <div class="head">
          <h1>Access</h1>
        </div>
        <Notice title="This view is an admin's">
          <p>
            Who was granted what is an admin's to see and to change. The key this page asks draal with is{" "}
            <ShortID id={me()!.id} />, and what it was granted is: {rolesLine(me()!.grants)}.
          </p>
          <p>
            <A href="/">Go to the services</A>
          </p>
        </Notice>
      </Match>
      <Match when={true}>
        <Grants me={me()!.id} />
      </Match>
    </Switch>
  );
}
