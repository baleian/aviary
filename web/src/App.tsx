import { Navigate, Route, Routes } from "react-router";
import { useProjects } from "./api/queries.ts";
import { prefs } from "./lib/prefs.ts";
import { Account } from "./screens/Account.tsx";
import { Agents } from "./screens/agents/Agents.tsx";
import { Connections } from "./screens/connections/Connections.tsx";
import { Overview } from "./screens/Overview.tsx";
import { Projects } from "./screens/Projects.tsx";
import { SignIn } from "./screens/SignIn.tsx";
import { ProjectFrame } from "./shell/ProjectFrame.tsx";
import { NotFound, Signed } from "./shell/Signed.tsx";
import { Toasts } from "./ui/Toasts.tsx";

// Home goes to where a caller begins: the project that was last in view, or
// the caller's only project, or else the list of projects.
function Home() {
  const projects = useProjects().data ?? [];
  const last = prefs.get("project");
  const known =
    projects.find((project) => project.slug === last) ??
    (projects.length === 1 ? projects[0] : undefined);
  return <Navigate to={known ? `/p/${known.slug}` : "/projects"} replace />;
}

// App is the console: its screens, by their paths, and the messages that
// show above them.
export function App() {
  return (
    <>
      <Routes>
        <Route path="/signin" element={<SignIn />} />
        <Route element={<Signed />}>
          <Route index element={<Home />} />
          <Route path="/projects" element={<Projects />} />
          <Route path="/p/:project" element={<ProjectFrame />}>
            <Route index element={<Overview />} />
            <Route path="agents" element={<Agents />} />
            <Route path="connections" element={<Connections />} />
            <Route path="*" element={<NotFound />} />
          </Route>
          <Route path="/me" element={<Account />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
      <Toasts />
    </>
  );
}
