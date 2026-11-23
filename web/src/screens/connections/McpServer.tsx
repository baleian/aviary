import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api } from "../../api/client.ts";
import { can, keys } from "../../api/project.ts";
import type { McpServerTools } from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { label, sentence, transportName } from "../../lib/format.ts";
import { said } from "../../lib/mcp.ts";
import { useTitle } from "../../lib/title.ts";
import { queryClient } from "../../query.ts";
import { useProject } from "../../shell/project.ts";
import { Missing } from "../../shell/ProjectFrame.tsx";
import { AgentTile } from "../../ui/Avatar.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Menu, MenuItem } from "../../ui/Menu.tsx";
import { Time } from "../../ui/Time.tsx";
import { DeleteMcpServer, EditMcpServer } from "./EditMcpServer.tsx";
import { Find, None } from "./parts.tsx";

// How many tools a server lists before they can be searched.
const SEARCHED = 8;

// McpServerPage is the screen of one MCP server: its tools, which agents use
// it, and the headers that its requests carry. Its tools are listed again on
// request, with what the server stores.
export function McpServerPage() {
  const context = useProject();
  const { server: slug } = useParams();
  const server = context.mcpServers.find((other) => other.slug === slug);
  const navigate = useNavigate();
  const [doing, setDoing] = useState<"edit" | "delete" | null>(null);
  const [sought, setSought] = useState("");
  const [asking, setAsking] = useState(false);
  const [asked, setAsked] = useState<{ tone: string; text: string }>();
  useTitle(server && label(server), label(context.project));
  if (!server) return <Missing />;
  const manages = can(context, "connections.manage");
  const base = `/p/${context.project.slug}`;
  const tools = server.tools ?? [];
  const words = sought.trim().toLowerCase();
  const found = tools.filter((tool) =>
    `${tool.name} ${tool.description ?? ""}`.toLowerCase().includes(words),
  );
  const users = context.agents.filter((agent) =>
    (agent.mcp_servers ?? []).includes(server.id),
  );
  const stores = context.stores;

  async function refresh() {
    setAsked(undefined);
    setAsking(true);
    try {
      const listed = await api.post<McpServerTools>(
        `/v1/mcp-servers/${server!.id}/tools`,
      );
      await queryClient.invalidateQueries({
        queryKey: keys.mcpServers(context.project.id),
      });
      setAsked(said(listed, server!.transport));
    } catch (failure) {
      setAsked({
        tone: "bad",
        text: sentence(failure instanceof Error ? failure.message : ""),
      });
    } finally {
      setAsking(false);
    }
  }

  return (
    <section className="page">
      <div className="wrap">
        <p className="back">
          <Link to={`${base}/mcp-servers`}>
            <Icon name="chev" />
            MCP servers
          </Link>
        </p>
        <div className="pg-h srv-h">
          <span className="ic">
            <Icon name="plug" className="lg" />
          </span>
          <div>
            <h1 className="h1">
              {label(server)}
              {server.name && <code>{server.slug}</code>}
            </h1>
            <p className="sub">
              <code>{server.url}</code> · {transportName(server.transport)} ·
              Updated <Time value={server.updated_at} />
            </p>
          </div>
          {manages && (
            <div className="act">
              <button
                className="btn sec"
                type="button"
                disabled={asking}
                onClick={() => void refresh()}
              >
                {asking ? <span className="ring" /> : <Icon name="refresh" />}
                Refresh tools
              </button>
              <button
                className="btn pri"
                type="button"
                onClick={() => setDoing("edit")}
              >
                <Icon name="edit" />
                Edit
              </button>
              <Menu
                align="right"
                button={(props) => (
                  <button className="ib" type="button" title="More" {...props}>
                    <Icon name="dots" />
                  </button>
                )}
              >
                <MenuItem
                  glyph="trash"
                  text="Delete server…"
                  danger
                  onClick={() => setDoing("delete")}
                />
              </Menu>
            </div>
          )}
        </div>
        {asked && (
          <p className={cls("try-r", asked.tone)} role="status">
            {asked.text}
          </p>
        )}

        <div className="srv">
          <div className="card">
            <div className="tb-h">
              <h2 className="h2">
                Tools<span className="cnt">{tools.length}</span>
              </h2>
              {tools.length > SEARCHED && (
                <Find
                  value={sought}
                  onChange={setSought}
                  placeholder="Find a tool"
                />
              )}
              {server.tools_listed_at && (
                <span className="end sub">
                  Listed <Time value={server.tools_listed_at} />
                </span>
              )}
            </div>
            {tools.length ? (
              <ul className="tool-list">
                {found.map((tool) => (
                  <li key={tool.name}>
                    <code>{tool.name}</code>
                    {tool.description && <span>{tool.description}</span>}
                  </li>
                ))}
                {!found.length && <None sought={sought} what="tool" />}
              </ul>
            ) : (
              <p className="sub none-found">
                The server has not listed its tools yet.
              </p>
            )}
          </div>

          <div className="srv-side">
            <div className="card">
              <div className="tb-h">
                <h2 className="h2">
                  Used by<span className="cnt">{users.length}</span>
                </h2>
              </div>
              {users.length ? (
                <ul className="side-list">
                  {users.map((agent) => (
                    <li key={agent.id}>
                      <AgentTile agent={agent} size="xs" />
                      <Link to={`${base}/agents/${agent.slug}`}>
                        {label(agent)}
                      </Link>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="sub none-found">
                  No agent begins its sessions with it. A session can still turn
                  it on.
                </p>
              )}
            </div>
            <div className="card">
              <div className="tb-h">
                <h2 className="h2">
                  Headers
                  <span className="cnt">{server.headers?.length ?? 0}</span>
                </h2>
              </div>
              {server.headers?.length ? (
                <ul className="side-list hdr-list">
                  {server.headers.map((header) => {
                    const secret = header.secret;
                    const store =
                      secret &&
                      "store" in secret &&
                      stores.find((each) => each.id === secret.store);
                    return (
                      <li key={header.name}>
                        <code>{header.name}</code>
                        {!secret ? (
                          <span className="val">{header.value}</span>
                        ) : "store" in secret ? (
                          <span
                            className="pill"
                            title={`${secret.path} → ${secret.key}`}
                          >
                            <Icon name="key" />
                            {store ? store.name : "A secret store"}
                          </span>
                        ) : (
                          <span className="pill g">
                            <Icon name="lock" />
                            Stored
                          </span>
                        )}
                      </li>
                    );
                  })}
                </ul>
              ) : (
                <p className="sub none-found">None.</p>
              )}
            </div>
          </div>
        </div>
      </div>
      {doing === "edit" && (
        <EditMcpServer
          context={context}
          existing={server}
          onClose={() => setDoing(null)}
        />
      )}
      {doing === "delete" && (
        <DeleteMcpServer
          context={context}
          server={server}
          onClose={(deleted) => {
            setDoing(null);
            if (deleted) void navigate(`${base}/mcp-servers`);
          }}
        />
      )}
    </section>
  );
}
