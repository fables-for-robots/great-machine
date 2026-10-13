import { A, type RouteSectionProps, useLocation } from "@solidjs/router";
import { createEffect, createMemo, For, Match, Show, Switch } from "solid-js";
import mark from "../assets/mark-white.png";
import { isID } from "../api/types";
import { ago, shortID } from "../format";
import { isAdmin, rolesLine } from "../state/access";
import { connection, findZathras, freshAt, now, received, refusal } from "../state/fleet";
import { me, signedOut } from "../state/session";
import Notice from "./Notice";
import ShortID from "./ShortID";

// A step of the path: what it is called and where it leads.
interface Crumb {
  text: string;
  href: string;
  title?: string;
}

// A part of a path as text. A part that is no valid encoding is itself.
function decode(part: string): string {
  try {
    return decodeURIComponent(part);
  } catch {
    return part;
  }
}

// The steps of a path: the services, a service, a zathras; or the
// services and who may do what.
function crumbsOf(path: string): Crumb[] {
  const parts = path.split("/").filter((p) => p !== "");
  const crumbs: Crumb[] = [{ text: "Services", href: "/" }];
  if (parts[0] === "access" && parts.length === 1) {
    crumbs.push({ text: "Access", href: "/access" });
    return crumbs;
  }
  if (parts[0] !== "s" || parts[1] === undefined) {
    return crumbs;
  }
  const service = decode(parts[1]);
  crumbs.push({ text: service, href: `/s/${parts[1]}`, title: service });
  if (parts[2] !== "z" || parts[3] === undefined) {
    return crumbs;
  }
  const id = decode(parts[3]);
  // The name of the zathras when the fleet has it, and its ID until then.
  const name = isID(id) ? findZathras(service, id)?.name : undefined;
  crumbs.push({ text: name ?? shortID(id), href: `/s/${parts[1]}/z/${parts[3]}`, title: name ?? id });
  return crumbs;
}

const WORDS = { connecting: "Connecting", live: "Live", lost: "Lost" } as const;

// What the page says in the place of every view when its cookie is no
// longer good: the command that printed its link was started again, or
// has ended, and nothing the page asks is answered any more.
function SignedOut() {
  return (
    <div class="whole">
      <Notice title="This page is no longer signed in" kind="bad">
        <p>
          It was opened with a link that <span class="mono">great-machine web</span> printed, and that link is no longer
          good: the command was started again since, or it has ended. Open the link it printed when it was started last. If
          it no longer runs, start it, and open the link it prints:
        </p>
        <code class="cmd">great-machine web</code>
      </Notice>
    </div>
  );
}

// What the page says in the place of every view when draal has granted
// its key nothing: the whole ID, which is what an admin needs to grant
// it a role.
function NoRole(props: { id: string }) {
  return (
    <div class="whole">
      <Notice title="This key has no role">
        <p>
          draal knows the key this page asks with, and has granted it nothing: it may neither look at the fleet nor act
          on it. An admin grants it a role by its endpoint ID, which is
        </p>
        <code class="cmd">{props.id}</code>
        <p>For a key that is to deploy a service, an admin runs</p>
        <code class="cmd">great-machine access grant deployer {props.id} --service NAME</code>
        <p class="muted">The page shows the fleet as soon as the key has a role: it keeps asking.</p>
      </Notice>
    </div>
  );
}

// The frame of every view: the header with the name of the program, the
// path as breadcrumbs, the ways to who may do what and to the page of
// the store, who the key is and the state of the connection; what there
// is to say while the connection is lost; and the view.
export default function Layout(props: RouteSectionProps) {
  const location = useLocation();
  const crumbs = createMemo(() => crumbsOf(location.pathname));
  const admin = () => {
    const who = me();
    return who !== undefined && isAdmin(who.grants);
  };
  // A key that draal has answered for and has granted nothing.
  const noRole = () => {
    const who = me();
    return who !== undefined && who.grants.length === 0;
  };
  const lost = () => connection() === "lost" && !signedOut() && !noRole();

  createEffect(() => {
    const list = crumbs();
    const last = list[list.length - 1];
    document.title = list.length > 1 && last ? `${last.text} – great-machine` : "great-machine";
  });

  return (
    <div class="frame">
      <header class="top">
        <div class="top-in">
          <A href="/" class="brand" aria-label="great-machine: the services">
            <span class="mark" aria-hidden="true">
              <img src={mark} alt="" width="66" height="66" />
            </span>
            great-machine
          </A>
          <nav aria-label="Where this is">
            <ol class="crumbs">
              <For each={crumbs()}>
                {(c, i) => (
                  <li>
                    <Show
                      when={i() < crumbs().length - 1}
                      fallback={
                        <span aria-current="page" title={c.title}>
                          {c.text}
                        </span>
                      }
                    >
                      <A href={c.href} title={c.title}>
                        {c.text}
                      </A>
                    </Show>
                  </li>
                )}
              </For>
            </ol>
          </nav>
          <div class="top-side">
            {/* Both are an admin's: draal refuses them to every other
                key, so no other key is shown the way. */}
            <Show when={admin() && !signedOut()}>
              <A class="toplink" href="/access" title="Who may do what: the grants of draal">
                Access
              </A>
              {/* The page of the store is jaccard-store's own, served
                  under /store/: a page to load, and none of the views
                  of this one. rel="external" keeps the router from
                  taking the link for a path of its own. */}
              <a
                class="toplink"
                href="/store/"
                rel="external"
                title="The store: the closures that were pushed, and what sharing and compression save"
              >
                Store
              </a>
            </Show>
            <Show when={me()}>
              {(who) => (
                <span class="who" title="The key this page asks draal with, and what draal has granted it">
                  <ShortID id={who().id} />
                  <span class="who-roles" title={rolesLine(who().grants)}>
                    {rolesLine(who().grants)}
                  </span>
                </span>
              )}
            </Show>
            <span
              class="link"
              data-state={signedOut() ? "lost" : connection()}
              role="status"
              title="The connection of this page to draal"
            >
              {signedOut() ? "Signed out" : WORDS[connection()]}
            </span>
          </div>
        </div>
      </header>
      <Show when={lost()}>
        <div class="lost" role="alert">
          <p>
            <Show
              when={received()}
              fallback={`${
                refusal() ? "draal answers, and does not send the fleet." : "draal does not answer."
              } The page keeps trying, with pauses that grow to half a minute.`}
            >
              The connection to draal is lost. What is below is the fleet as it was {ago(freshAt(), now())}; the page keeps
              trying and carries on from here when draal is back.
            </Show>
            {/* Final review A1: when draal ended the socket of the fleet
                with a refusal, its own sentence says why. It is text. */}
            <Show when={refusal()}> What draal said last: “{refusal()}”.</Show>
          </p>
        </div>
      </Show>
      <main class="view" data-stale={lost() && received() ? "" : undefined}>
        <Switch fallback={props.children}>
          <Match when={signedOut()}>
            <SignedOut />
          </Match>
          <Match when={noRole()}>
            <NoRole id={me()?.id ?? ""} />
          </Match>
        </Switch>
      </main>
    </div>
  );
}
