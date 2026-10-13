import { A, useLocation } from "@solidjs/router";
import Notice from "../components/Notice";

// What a path that is none of the page's shows.
export default function NotFound() {
  const location = useLocation();
  return (
    <Notice title="No such page">
      <p>
        The page has nothing at <span class="mono">{location.pathname}</span>. It has the services, a service at{" "}
        <span class="mono">/s/NAME</span>, a zathras at <span class="mono">/s/NAME/z/ID</span>, and, for an admin, who may
        do what at <span class="mono">/access</span>.
      </p>
      <p>
        <A href="/">Go to the services</A>
      </p>
    </Notice>
  );
}
