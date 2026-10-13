import { Route, Router } from "@solidjs/router";
import { createEffect, on, onCleanup, onMount } from "solid-js";
import Layout from "./components/Layout";
import { connection, lives, startFleet } from "./state/fleet";
import { askMe, signedOut } from "./state/session";
import Access from "./views/Access";
import NotFound from "./views/NotFound";
import Service from "./views/Service";
import Services from "./views/Services";
import Zathras from "./views/Zathras";

// The page: one connection to the fleet for all of it, who its key is
// to draal, and a view for each of its four paths.
export default function App() {
  let stop: (() => void) | undefined;
  onMount(() => {
    stop = startFleet();
  });
  onCleanup(() => stop?.());

  // Who the key is: asked when the page opens, and again whenever the
  // connection of the fleet is lost or back. A socket that is refused
  // for the cookie of the page only ends, so this is where the page
  // learns that it is no longer signed in; and a key that is back may
  // have been granted something else meanwhile.
  createEffect(on([connection, lives], () => void askMe()));

  // A page that is no longer signed in asks no more: its socket would
  // be refused for as long as it tried.
  createEffect(() => {
    if (signedOut()) {
      stop?.();
    }
  });

  return (
    <Router root={Layout}>
      <Route path="/" component={Services} />
      <Route path="/s/:service" component={Service} />
      <Route path="/s/:service/z/:id" component={Zathras} />
      <Route path="/access" component={Access} />
      <Route path="*" component={NotFound} />
    </Router>
  );
}
