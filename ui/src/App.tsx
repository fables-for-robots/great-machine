import { createSignal, onCleanup, onMount, Show } from "solid-js";

// The skeleton of the page: its name and, as a first sign of life, the
// number of services in the first message of /api/watch.
export default function App() {
  const [services, setServices] = createSignal<number>();
  const [failed, setFailed] = createSignal(false);

  onMount(() => {
    const scheme = location.protocol === "https:" ? "wss:" : "ws:";
    const socket = new WebSocket(`${scheme}//${location.host}/api/watch`);
    socket.onmessage = (event) => {
      if (typeof event.data !== "string") {
        return;
      }
      const fleet: unknown = JSON.parse(event.data);
      if (typeof fleet === "object" && fleet !== null && "services" in fleet) {
        const list = fleet.services;
        setServices(Array.isArray(list) ? list.length : 0);
        setFailed(false);
      }
    };
    socket.onerror = () => setFailed(true);
    socket.onclose = () => {
      if (services() === undefined) {
        setFailed(true);
      }
    };
    onCleanup(() => socket.close());
  });

  return (
    <main>
      <h1>great-machine</h1>
      <Show
        when={services() !== undefined}
        fallback={<p>{failed() ? "draal does not answer." : "Asking draal…"}</p>}
      >
        <p>services: {services()}</p>
      </Show>
    </main>
  );
}
