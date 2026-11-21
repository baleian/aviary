import { Link } from "react-router";
import { useMe, useProjects } from "../api/queries.ts";
import { label } from "../lib/format.ts";
import { useTitle } from "../lib/title.ts";
import { ProjectTile } from "../ui/Avatar.tsx";
import { Empty } from "../ui/Blank.tsx";
import { Icon } from "../ui/Icon.tsx";
import { Time } from "../ui/Time.tsx";

// Projects is the list of the caller's projects.
export function Projects() {
  const projects = useProjects().data ?? [];
  const admin = useMe().data?.admin;
  useTitle("Projects");
  return (
    <section className="page">
      <div className="wrap">
        <div className="pg-h">
          <h1 className="h1">Projects</h1>
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
          </div>
        ) : (
          <div className="card">
            <Empty
              glyph="folder"
              title="No projects yet"
              text={!admin && "Ask an administrator to add you to one."}
            />
          </div>
        )}
      </div>
    </section>
  );
}
