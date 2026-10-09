import { A, type RouteSectionProps, useLocation } from "@solidjs/router";
import { createEffect, createMemo, For, Show } from "solid-js";
import mark from "../assets/mark-white.png";
import { isID } from "../api/types";
import { ago, shortID } from "../format";
import { connection, findZathras, freshAt, now, received } from "../state/fleet";

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

// The steps of a path: the services, a service, a zathras.
function crumbsOf(path: string): Crumb[] {
  const parts = path.split("/").filter((p) => p !== "");
  const crumbs: Crumb[] = [{ text: "Services", href: "/" }];
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

// The frame of every view: the header with the name of the program, the
// path as breadcrumbs and the state of the connection; what there is to
// say while the connection is lost; and the view.
export default function Layout(props: RouteSectionProps) {
  const location = useLocation();
  const crumbs = createMemo(() => crumbsOf(location.pathname));
  const lost = () => connection() === "lost";

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
          <span class="link" data-state={connection()} role="status" title="The connection of this page to draal">
            {WORDS[connection()]}
          </span>
        </div>
      </header>
      <Show when={lost()}>
        <div class="lost" role="alert">
          <p>
            <Show
              when={received()}
              fallback="draal does not answer. The page keeps trying, with pauses that grow to half a minute."
            >
              The connection to draal is lost. What is below is the fleet as it was {ago(freshAt(), now())}; the page keeps
              trying and carries on from here when draal is back.
            </Show>
          </p>
        </div>
      </Show>
      <main class="view" data-stale={lost() && received() ? "" : undefined}>
        {props.children}
      </main>
    </div>
  );
}
