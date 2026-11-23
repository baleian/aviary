import { useState } from "react";
import { can, type ProjectContext } from "../../api/project.ts";
import type { McpServer } from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { label, plural } from "../../lib/format.ts";
import { Icon } from "../../ui/Icon.tsx";
import { Menu, MenuItem } from "../../ui/Menu.tsx";
import { matches } from "../../lib/mcp.ts";
import { differs, type McpChoice } from "./servers.ts";

// How many servers a project has before the picker offers to search them,
// and how many that are on the details list before the rest is folded.
const SEARCHED = 6;
const LISTED = 6;

// toolsOf says in a few words what a server gives, as far as is known.
const toolsOf = (server: McpServer) =>
  server.tools ? plural(server.tools.length, "tool") : "";

// McpPicker is the MCP servers of the project, to turn on and off for a
// session: those that are on first, then the others. Rows keep the places
// that they had when the picker opened, so that a row that is switched stays
// where it is. A search narrows them by name, slug or tool.
function McpPicker({
  context,
  choice,
}: {
  context: ProjectContext;
  choice: McpChoice;
}) {
  const [sought, setSought] = useState("");
  const [first] = useState(() => new Set(choice.on));
  const servers = context.mcpServers;
  const found = servers.flatMap((server) => {
    const match = matches(server, sought);
    return match ? [{ server, tool: match.tool }] : [];
  });
  const groups: [string, typeof found][] = [
    ["On", found.filter((row) => first.has(row.server.id))],
    [
      first.size ? "Off" : "In this project",
      found.filter((row) => !first.has(row.server.id)),
    ],
  ];
  const count = servers.filter((server) =>
    choice.on.includes(server.id),
  ).length;
  const reset = choice.onReset && differs(choice);
  const manages = can(context, "connections.manage");
  return (
    <div className="mcp">
      <div className="mcp-top">
        <b>MCP servers</b>
        <small>
          {count} of {servers.length} on
        </small>
      </div>
      {servers.length > SEARCHED && (
        <label className="inp-i mcp-find">
          <Icon name="search" />
          <input
            className="inp"
            placeholder="Find a server or a tool"
            aria-label="Find an MCP server"
            value={sought}
            autoFocus
            onChange={(event) => setSought(event.target.value)}
          />
        </label>
      )}
      <div className="mcp-list">
        {groups.map(
          ([title, rows]) =>
            rows.length > 0 && (
              <div className="mcp-g" key={title}>
                <small className="h">
                  {title}
                  <span>{rows.length}</span>
                </small>
                {rows.map(({ server, tool }) => {
                  const on = choice.on.includes(server.id);
                  return (
                    <label
                      key={server.id}
                      className={cls("swt mcp-r", !on && "off")}
                    >
                      <span className="ic">
                        <Icon name="plug" />
                      </span>
                      <span className="tx">
                        <b>
                          {label(server)}
                          {choice.defaults.includes(server.id) && (
                            <em title="The agent begins its sessions with it">
                              default
                            </em>
                          )}
                        </b>
                        <small>{tool ? `Tool ${tool}` : toolsOf(server)}</small>
                      </span>
                      <input
                        type="checkbox"
                        checked={on}
                        disabled={!choice.onToggle}
                        aria-label={label(server)}
                        onChange={(event) =>
                          choice.onToggle?.(server.id, event.target.checked)
                        }
                      />
                      <i />
                    </label>
                  );
                })}
              </div>
            ),
        )}
        {!found.length && (
          <p className="mcp-none">No server or tool matches “{sought}”.</p>
        )}
      </div>
      {choice.running && choice.onToggle && (
        <p className="mcp-n">
          <Icon name="info" />
          Changes apply from the next message.
        </p>
      )}
      {(reset || manages) && <hr />}
      {reset && (
        <MenuItem
          glyph="refresh"
          text="Use the agent's servers"
          onClick={choice.onReset}
        />
      )}
      {manages && (
        <MenuItem
          glyph="set"
          text="Manage MCP servers"
          to={`/p/${context.project.slug}/mcp-servers`}
        />
      )}
    </div>
  );
}

// McpButton opens the MCP servers of the session from its composer, and
// shows how many are on.
export function McpButton({
  context,
  choice,
}: {
  context: ProjectContext;
  choice: McpChoice;
}) {
  const on = context.mcpServers.filter((server) =>
    choice.on.includes(server.id),
  );
  return (
    <Menu
      className="mcp-pop"
      button={(props) => (
        <button
          className={cls("mcp-b", !on.length && "off")}
          type="button"
          aria-label="MCP servers"
          title={
            on.length
              ? "MCP servers: " + on.map((server) => label(server)).join(", ")
              : "MCP servers"
          }
          {...props}
        >
          <Icon name="plug" />
          {on.length > 0 && <span>{on.length}</span>}
        </button>
      )}
    >
      <McpPicker context={context} choice={choice} />
    </Menu>
  );
}

// McpSummary is the MCP servers that a session has on, in its details: each
// can be turned off there, and the picker turns on others. Servers that the
// session turned on itself say so, and the agent's that it turned off are
// counted.
export function McpSummary({
  context,
  choice,
}: {
  context: ProjectContext;
  choice: McpChoice;
}) {
  const [all, setAll] = useState(false);
  const on = context.mcpServers.filter((server) =>
    choice.on.includes(server.id),
  );
  const off = choice.defaults.filter((id) => !choice.on.includes(id)).length;
  const shown = all ? on : on.slice(0, LISTED);
  return (
    <div className="r-s">
      <h4>
        MCP servers
        <small className="sub">{on.length} on</small>
        {choice.onToggle && (
          <Menu
            className="mcp-pop"
            align="right"
            button={(props) => (
              <button className="btn quiet sm" type="button" {...props}>
                Change
              </button>
            )}
          >
            <McpPicker context={context} choice={choice} />
          </Menu>
        )}
      </h4>
      {on.length ? (
        <ul className="mcp-on">
          {shown.map((server) => (
            <li key={server.id}>
              <span className="ic">
                <Icon name="plug" />
              </span>
              <span className="tx">
                <b>{label(server)}</b>
                <small>{toolsOf(server)}</small>
              </span>
              {!choice.defaults.includes(server.id) && (
                <span className="tag b">added</span>
              )}
              {choice.onToggle && (
                <button
                  className="ib sm"
                  type="button"
                  title={"Turn off " + label(server)}
                  onClick={() => choice.onToggle?.(server.id, false)}
                >
                  <Icon name="x" />
                </button>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p className="sub sm">None is on.</p>
      )}
      {on.length > LISTED && (
        <button
          className="btn quiet sm more"
          type="button"
          onClick={() => setAll(!all)}
        >
          {all ? "Show fewer" : `Show all ${on.length}`}
        </button>
      )}
      {off > 0 && (
        <p className="mcp-n">
          {off === 1
            ? "1 server of the agent is off."
            : `${off} servers of the agent are off.`}
        </p>
      )}
      {choice.running && choice.onToggle && (
        <p className="mcp-n">
          <Icon name="info" />
          Changes apply from the next message.
        </p>
      )}
    </div>
  );
}
