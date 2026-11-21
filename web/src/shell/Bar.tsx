import { useState } from "react";
import { Link, NavLink, useMatch } from "react-router";
import { useProjects } from "../api/queries.ts";
import type { Me } from "../api/types.ts";
import { label } from "../lib/format.ts";
import { NewProject } from "../screens/NewProject.tsx";
import { signOut } from "../session/auth.ts";
import { setTheme, type Theme, useTheme } from "../theme.ts";
import { AdminBadge, Avatar, ProjectTile } from "../ui/Avatar.tsx";
import { Icon, Use } from "../ui/Icon.tsx";
import type { IconName } from "../ui/icons.ts";
import { Menu, MenuItem } from "../ui/Menu.tsx";
import { Segmented } from "../ui/Segmented.tsx";
import { self } from "../ui/who.ts";

const THEMES: [Theme, string, IconName][] = [
  ["system", "Auto", "monitor"],
  ["light", "Light", "sun"],
  ["dark", "Dark", "moon"],
];

function ThemeRow() {
  const { theme } = useTheme();
  return (
    <div className="row">
      Theme
      <Segmented choices={THEMES} value={theme} onChange={setTheme} />
    </div>
  );
}

// Account is the button of the caller, and the menu that it opens.
function Account({ me }: { me: Me }) {
  // Without an identity provider nobody signs in, and the caller has no ID.
  if (!me.id) {
    return (
      <Menu
        align="right"
        className="acct"
        button={(props) => (
          <button className="me open" type="button" {...props}>
            <Icon name="user" />
            No sign-in
            <Icon name="down" />
          </button>
        )}
      >
        <div className="who">
          <div>
            <b>No sign-in</b>
            <small>Everyone is an administrator here.</small>
          </div>
        </div>
        <hr />
        <ThemeRow />
      </Menu>
    );
  }
  const who = self(me);
  return (
    <Menu
      align="right"
      className="acct"
      button={(props) => (
        <button className="me" type="button" {...props}>
          <Avatar who={who} size="sm" />
          <span className="nm">{who.name}</span>
          {me.admin && <AdminBadge />}
          <Icon name="down" />
        </button>
      )}
    >
      <div className="who">
        <Avatar who={who} size="lg" />
        <div>
          <b>{who.name}</b>
          <small>{me.username || me.subject}</small>
        </div>
      </div>
      <hr />
      <MenuItem glyph="user" text="Your account" to="/me" />
      <ThemeRow />
      <hr />
      <MenuItem glyph="logout" text="Sign out" onClick={signOut} />
    </Menu>
  );
}

// Bar is the bar at the top of every signed-in screen: the project in view
// and the way to another, the screens of the project, and the caller.
export function Bar({ me }: { me: Me }) {
  const { dark } = useTheme();
  const projects = useProjects().data ?? [];
  const slug = useMatch("/p/:project/*")?.params.project;
  const project = projects.find((other) => other.slug === slug);
  const [creating, setCreating] = useState(false);
  return (
    <header className="nav">
      <Link
        className="logo"
        to={project ? `/p/${project.slug}` : "/projects"}
        title="Aviary"
      >
        <Use symbol="logo" />
        Aviary
      </Link>
      <span className="slash" />
      <Menu
        className="sw"
        button={(props) => (
          <button
            className={project ? "sw-b" : "sw-b none"}
            type="button"
            {...props}
          >
            {project ? (
              <>
                <ProjectTile project={project} />
                <span>{label(project)}</span>
              </>
            ) : (
              <span>Choose a project</span>
            )}
            <Icon name="updown" />
          </button>
        )}
      >
        <small className="h">Projects</small>
        <div className="list">
          {projects.map((other) => (
            <MenuItem
              key={other.id}
              lead={<ProjectTile project={other} />}
              text={label(other)}
              on={other.id === project?.id}
              to={`/p/${other.slug}`}
            />
          ))}
        </div>
        {!projects.length && (
          <div className="row">You are in no project yet.</div>
        )}
        <hr />
        <MenuItem glyph="grid" text="All projects" to="/projects" />
        {me.admin && (
          <MenuItem
            className="new"
            lead={
              <span className="box">
                <Icon name="plus" />
              </span>
            }
            text="New project"
            onClick={() => setCreating(true)}
          />
        )}
      </Menu>
      {project && (
        <nav className="navt" aria-label="Project">
          <NavLink
            to={`/p/${project.slug}`}
            end
            className={({ isActive }) => (isActive ? "on" : undefined)}
          >
            Overview
          </NavLink>
          <NavLink
            to={`/p/${project.slug}/agents`}
            className={({ isActive }) => (isActive ? "on" : undefined)}
          >
            Agents
          </NavLink>
          <NavLink
            to={`/p/${project.slug}/connections`}
            className={({ isActive }) => (isActive ? "on" : undefined)}
          >
            Connections
          </NavLink>
        </nav>
      )}
      <div className="nav-r">
        <button
          className="ib"
          type="button"
          title={
            dark ? "Switch to the light theme" : "Switch to the dark theme"
          }
          onClick={() => setTheme(dark ? "light" : "dark")}
        >
          <Icon name={dark ? "sun" : "moon"} className="lg" />
        </button>
        <Account me={me} />
      </div>
      {creating && <NewProject onClose={() => setCreating(false)} />}
    </header>
  );
}
