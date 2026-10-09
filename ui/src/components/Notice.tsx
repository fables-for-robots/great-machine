import { type JSX, Show } from "solid-js";

// What a view says when it has nothing to show or could not get it: a
// heading that names what is missing, and what to do about it.
export default function Notice(props: { title?: string; kind?: "bad"; small?: boolean; children?: JSX.Element }) {
  return (
    <div class="notice" classList={{ small: props.small }} data-kind={props.kind} role={props.kind === "bad" ? "alert" : undefined}>
      <Show when={props.title}>
        <h2>{props.title}</h2>
      </Show>
      {props.children}
    </div>
  );
}
