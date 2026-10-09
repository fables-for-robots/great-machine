import Notice from "../components/Notice";
import { fleet, received } from "../state/fleet";

// The place of the view; a later task writes it. Until then it shows
// that the fleet arrives.
export default function Zathras() {
  return (
    <Notice title="Zathras">
      <p>{received() ? `draal knows ${fleet.services.length} services.` : "Asking draal…"}</p>
    </Notice>
  );
}
