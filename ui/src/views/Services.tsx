import Notice from "../components/Notice";
import { fleet, received } from "../state/fleet";

// The place of the view; a later task writes it. Until then it shows
// that the fleet arrives.
export default function Services() {
  return (
    <Notice title="Services">
      <p>{received() ? `draal knows ${fleet.services.length} services.` : "Asking draal…"}</p>
    </Notice>
  );
}
