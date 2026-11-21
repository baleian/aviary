import { useEffect } from "react";
import { Link, Outlet, useParams } from "react-router";
import { ApiError } from "../api/client.ts";
import { useFeed } from "../api/feed.ts";
import { useProjectContext } from "../api/project.ts";
import { sentence } from "../lib/format.ts";
import { prefs } from "../lib/prefs.ts";
import { Blank } from "../ui/Blank.tsx";
import { Wait } from "./Signed.tsx";

// Missing is the screen of what the caller cannot see: it does not exist,
// or the caller has no access to it.
export function Missing() {
  return (
    <Blank
      glyph="search"
      title="Not found"
      text="It does not exist, or you do not have access to it."
      actions={
        <Link className="btn pri" to="/projects">
          Go to your projects
        </Link>
      }
    />
  );
}

// Failed is the screen of a request that the API refused.
export function Failed({ error }: { error: Error }) {
  const refused = error instanceof ApiError && error.status === 403;
  return (
    <Blank
      glyph="warn"
      title={refused ? "You may not do this" : "Something went wrong"}
      text={sentence(error.message)}
      actions={
        <Link className="btn sec" to="/projects">
          Go to your projects
        </Link>
      }
    />
  );
}

// ProjectFrame is the frame of the screens of a project: it reads the
// project of the path, and gives it to the screen.
export function ProjectFrame() {
  const { project: slug = "" } = useParams();
  const found = useProjectContext(slug);
  const ready = found.state === "ready";
  useFeed(ready ? found.context.project.id : undefined);
  // The console comes back to the project that was last in view.
  useEffect(() => {
    if (ready) prefs.set("project", slug);
  }, [ready, slug]);
  if (found.state === "pending") return <Wait />;
  if (found.state === "missing") return <Missing />;
  if (found.state === "failed") return <Failed error={found.error} />;
  return <Outlet context={found.context} />;
}
