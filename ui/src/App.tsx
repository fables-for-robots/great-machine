import { Route, Router } from "@solidjs/router";
import { onCleanup, onMount } from "solid-js";
import Layout from "./components/Layout";
import { startFleet } from "./state/fleet";
import NotFound from "./views/NotFound";
import Service from "./views/Service";
import Services from "./views/Services";
import Zathras from "./views/Zathras";

// The page: one connection to the fleet for all of it, and a view for
// each of its three paths.
export default function App() {
  onMount(() => {
    const stop = startFleet();
    onCleanup(stop);
  });

  return (
    <Router root={Layout}>
      <Route path="/" component={Services} />
      <Route path="/s/:service" component={Service} />
      <Route path="/s/:service/z/:id" component={Zathras} />
      <Route path="*" component={NotFound} />
    </Router>
  );
}
