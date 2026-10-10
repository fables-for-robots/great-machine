import { createSignal, onCleanup } from "solid-js";
import { shortID } from "../format";

// An endpoint ID as its first ten characters. The whole of it is the
// title, and a click copies it.
export default function ShortID(props: { id: string }) {
  const [said, setSaid] = createSignal<string>();
  let timer: ReturnType<typeof setTimeout> | undefined;
  onCleanup(() => clearTimeout(timer));

  const copy = async (event: MouseEvent) => {
    // An ID inside a link or a row is copied, and nothing else happens.
    event.preventDefault();
    event.stopPropagation();
    let word = "copied";
    try {
      await navigator.clipboard.writeText(props.id);
    } catch {
      word = "not copied";
    }
    setSaid(word);
    clearTimeout(timer);
    timer = setTimeout(() => setSaid(undefined), 1500);
  };

  return (
    <button type="button" class="id" title={`${props.id} (click to copy)`} data-said={said()} onClick={copy}>
      {shortID(props.id)}
    </button>
  );
}
