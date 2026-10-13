// Who the page is to draal: the key that `great-machine web` asks with,
// and what it was granted. And whether the page is still signed in: its
// cookie is good for as long as the command runs that printed its link.

import { batch, createSignal } from "solid-js";
import { ApiError, me as askWho, onSignedOut } from "../api/client";
import type { Identity } from "../api/types";

const [me, setMe] = createSignal<Identity>();
const [meFailure, setMeFailure] = createSignal<string>();
const [signedOut, setSignedOut] = createSignal(false);

export {
  /** Who the key is; undefined until draal has said so once. While draal
   *  cannot be reached it is who the key was. */
  me,
  /** Why draal could not be asked who the key is, the last time it was
   *  asked; undefined when it answered. */
  meFailure,
  /** Whether a request was answered with a 401: the cookie of the page
   *  is no longer good, and nothing it asks is answered any more. It
   *  stays so: a page is signed in by being opened with a link. */
  signedOut,
};

// Whatever the page asks, a 401 is the same news.
onSignedOut(() => setSignedOut(true));

let asking: AbortController | undefined;

/** Asks draal who the key is. It is asked when the page opens, and again
 *  whenever the connection of the fleet is lost or back: a WebSocket
 *  that is refused for its cookie only ends, and this is where the page
 *  learns why; and a key that is back may have been granted something
 *  else meanwhile. Of two questions the later one counts. The promise is
 *  done when the answer was taken, and never rejects. */
export function askMe(): Promise<void> {
  asking?.abort();
  const abort = new AbortController();
  asking = abort;
  return askWho(abort.signal).then(
    (who) => {
      if (abort.signal.aborted) {
        return;
      }
      batch(() => {
        setMe(who);
        setMeFailure(undefined);
      });
    },
    (err: unknown) => {
      if (abort.signal.aborted || (err instanceof ApiError && err.code === "aborted")) {
        return;
      }
      setMeFailure(err instanceof Error ? err.message : String(err));
    },
  );
}

/** Forgets everything, as a page that was just opened has it. It is for
 *  the tests. */
export function forget(): void {
  asking?.abort();
  asking = undefined;
  batch(() => {
    setMe(undefined);
    setMeFailure(undefined);
    setSignedOut(false);
  });
  // A test may have taken the listener away.
  onSignedOut(() => setSignedOut(true));
}
