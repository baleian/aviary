import { Link, Navigate, Outlet, useLocation } from "react-router";
import { ApiError } from "../api/client.ts";
import { useMe, useProjects } from "../api/queries.ts";
import type { Me } from "../api/types.ts";
import { sentence } from "../lib/format.ts";
import { useTitle } from "../lib/title.ts";
import { Identity } from "../screens/Account.tsx";
import { signOut } from "../session/auth.ts";
import { Blank, Loading } from "../ui/Blank.tsx";
import { Icon } from "../ui/Icon.tsx";
import { Bar } from "./Bar.tsx";

// Wait stands for a screen whose first requests have no answer yet.
export function Wait() {
  return (
    <div className="wrap" style={{ paddingTop: "48px" }}>
      <Loading lines={4} />
    </div>
  );
}

// Unreachable is the screen of a console that gets no answer from the API.
export function Unreachable({ error }: { error: Error }) {
  return (
    <Blank
      full
      glyph="warn"
      title="The console cannot reach Aviary"
      text={sentence(error.message)}
    />
  );
}

// Shut is the screen of a caller who is signed in, but in no group that may
// use the installation.
function Shut({ me }: { me: Me }) {
  return (
    <Blank
      full
      glyph="lock"
      tone="y"
      title="You do not have access"
      text="Your account is in no group that may use this installation. Ask an administrator."
      actions={
        <button className="btn sec" type="button" onClick={signOut}>
          <Icon name="logout" />
          Sign out
        </button>
      }
    >
      <div className="card facts" style={{ padding: "18px 20px" }}>
        <Identity me={me} />
      </div>
    </Blank>
  );
}

export function NotFound() {
  useTitle();
  return (
    <Blank
      glyph="search"
      title="Not found"
      actions={
        <Link className="btn pri" to="/projects">
          Go to the projects
        </Link>
      }
    />
  );
}

// Signed is the frame of the screens that need a caller: the bar, and the
// screen below it. Without a caller it goes to the sign-in screen, which
// comes back to where the caller was going.
export function Signed() {
  const me = useMe();
  const location = useLocation();
  if (me.isPending) return <Wait />;
  if (me.isError) {
    if (me.error instanceof ApiError && me.error.status === 401) {
      const back = location.pathname + location.search;
      return <Navigate to="/signin" replace state={{ back }} />;
    }
    return <Unreachable error={me.error} />;
  }
  return <Entered me={me.data} />;
}

function Entered({ me }: { me: Me }) {
  const projects = useProjects();
  if (projects.isPending) return <Wait />;
  if (projects.isError) {
    // The API lets such a caller read nothing but who they are.
    const shut =
      projects.error instanceof ApiError && projects.error.status === 403;
    return shut ? <Shut me={me} /> : <Unreachable error={projects.error} />;
  }
  return (
    <>
      <Bar me={me} />
      <main id="view">
        <Outlet />
      </main>
    </>
  );
}
