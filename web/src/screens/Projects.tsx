import { useState } from "react";
import { Link } from "react-router";
import { useMe, useProjects } from "../api/queries.ts";
import { label } from "../lib/format.ts";
import { useTitle } from "../lib/title.ts";
import { ProjectTile } from "../ui/Avatar.tsx";
import { Empty } from "../ui/Blank.tsx";
import { Icon } from "../ui/Icon.tsx";
import { Time } from "../ui/Time.tsx";
import { NewProject } from "./NewProject.tsx";

// Projects is the list of the caller's projects. An administrator creates
// projects here.
export function Projects() {
  const projects = useProjects().data ?? [];
  const admin = useMe().data?.admin;
  const [creating, setCreating] = useState(false);
  useTitle("Projects");
  const create = admin && (
    <button className="btn pri" type="button" onClick={() => setCreating(true)}>
      <Icon name="plus" />
      New project
    </button>
  );
  return (
    <section className="page">
      <div className="wrap">
        <div className="pg-h">
          <h1 className="h1">Projects</h1>
          <div className="act">{create}</div>
        </div>
        {projects.length ? (
          <div className="projects">
            {projects.map((project) => (
              <Link
                key={project.id}
                className="card proj"
                to={`/p/${project.slug}`}
              >
                <ProjectTile project={project} size="lg" />
                <div className="tx">
                  <h3>{label(project)}</h3>
                  {project.name && <code>{project.slug}</code>}
                  <small>
                    Created <Time value={project.created_at} />
                  </small>
                </div>
                <span className="go">
                  <Icon name="arrow" />
                </span>
              </Link>
            ))}
            {admin && (
              <button
                className="card proj new"
                type="button"
                onClick={() => setCreating(true)}
              >
                <span className="box">
                  <Icon name="plus" className="lg" />
                </span>
                New project
              </button>
            )}
          </div>
        ) : (
          <div className="card">
            <Empty
              glyph="folder"
              title="No projects yet"
              text={!admin && "Ask an administrator to add you to one."}
              action={create}
            />
          </div>
        )}
      </div>
      {creating && <NewProject onClose={() => setCreating(false)} />}
    </section>
  );
}
