import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { can } from "../../api/project.ts";
import { label, plural, transportName } from "../../lib/format.ts";
import { useTitle } from "../../lib/title.ts";
import { useProject } from "../../shell/project.ts";
import { AgentTile } from "../../ui/Avatar.tsx";
import { Empty } from "../../ui/Blank.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Segmented } from "../../ui/Segmented.tsx";
import { Time } from "../../ui/Time.tsx";
import { matches } from "../../lib/mcp.ts";
import { EditMcpServer } from "./EditMcpServer.tsx";
import { Find, Head, None } from "./parts.tsx";

type Shown = "all" | "used" | "unused";

// McpServers is the screen of the MCP servers of a project: the tools that
// its agents may use. A project may have many, so the list is a table that a
// search narrows by name, slug, URL or tool, and that can show only the
// servers that no agent uses. A row leads to the server.
export function McpServers() {
  const context = useProject();
  const { project, mcpServers, agents } = context;
  const navigate = useNavigate();
  const manages = can(context, "connections.manage");
  const [adding, setAdding] = useState(false);
  const [sought, setSought] = useState("");
  const [shown, setShown] = useState<Shown>("all");
  useTitle("MCP servers", label(project));
  const base = `/p/${project.slug}/mcp-servers`;
  const usersOf = (id: string) =>
    agents.filter((agent) => (agent.mcp_servers ?? []).includes(id));
  const rows = mcpServers.filter((server) => {
    const used = usersOf(server.id).length > 0;
    if (shown === "used" && !used) return false;
    if (shown === "unused" && used) return false;
    return (
      !!matches(server, sought) ||
      server.url.toLowerCase().includes(sought.trim().toLowerCase())
    );
  });
  const add = manages && (
    <button className="btn pri" type="button" onClick={() => setAdding(true)}>
      <Icon name="plus" />
      Add MCP server
    </button>
  );
  return (
    <section className="page">
      <div className="wrap">
        <Head over={label(project)} title="MCP servers" action={add} />
        <div className="card">
          {mcpServers.length ? (
            <>
              <div className="tb-h">
                <Find
                  value={sought}
                  onChange={setSought}
                  placeholder="Find a server or a tool"
                />
                <Segmented
                  value={shown}
                  onChange={setShown}
                  choices={[
                    ["all", "All"],
                    ["used", "In use"],
                    ["unused", "Not in use"],
                  ]}
                />
                <span className="end sub">
                  {rows.length === mcpServers.length
                    ? plural(mcpServers.length, "server")
                    : `${rows.length} of ${mcpServers.length}`}
                </span>
              </div>
              <table className="tb mcps">
                <thead>
                  <tr>
                    <th>Server</th>
                    <th>URL</th>
                    <th>Transport</th>
                    <th className="num">Tools</th>
                    <th>Used by</th>
                    <th>Updated</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((server) => {
                    const users = usersOf(server.id);
                    const match = matches(server, sought);
                    return (
                      <tr
                        key={server.id}
                        onClick={() => void navigate(`${base}/${server.slug}`)}
                      >
                        <td>
                          <span className="nm">
                            <span className="ic">
                              <Icon name="plug" />
                            </span>
                            <span>
                              <Link to={`${base}/${server.slug}`}>
                                {label(server)}
                              </Link>
                              <small>
                                {match?.tool
                                  ? `Tool ${match.tool}`
                                  : server.slug}
                              </small>
                            </span>
                          </span>
                        </td>
                        <td>
                          <code className="url" title={server.url}>
                            {server.url.replace(/^https?:\/\//, "")}
                          </code>
                        </td>
                        <td>
                          <span className="tag">
                            {transportName(server.transport)}
                          </span>
                        </td>
                        <td className="num">{server.tools?.length ?? "—"}</td>
                        <td>
                          {users.length ? (
                            <span
                              className="stack"
                              title={users
                                .map((agent) => label(agent))
                                .join(", ")}
                            >
                              {users.slice(0, 4).map((agent) => (
                                <AgentTile
                                  key={agent.id}
                                  agent={agent}
                                  size="xs"
                                />
                              ))}
                              {users.length > 4 && (
                                <small>+{users.length - 4}</small>
                              )}
                            </span>
                          ) : (
                            <span className="sub">None</span>
                          )}
                        </td>
                        <td className="sub">
                          <Time value={server.updated_at} />
                        </td>
                      </tr>
                    );
                  })}
                  {!rows.length && (
                    <tr>
                      <td colSpan={6}>
                        {sought.trim() ? (
                          <None sought={sought} what="server or tool" />
                        ) : (
                          <p className="sub none-found">
                            {shown === "used"
                              ? "No agent uses a server yet."
                              : "Every server is in use."}
                          </p>
                        )}
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </>
          ) : (
            <Empty glyph="plug" title="No MCP servers yet" action={add} />
          )}
        </div>
      </div>
      {adding && (
        <EditMcpServer context={context} onClose={() => setAdding(false)} />
      )}
    </section>
  );
}
