import type { ReactNode } from "react";
import { Link } from "react-router";
import type { ProjectContext } from "../../api/project.ts";
import type { Tinted } from "../../api/queries.ts";
import type { Agent } from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { label } from "../../lib/format.ts";
import { AgentTile } from "../../ui/Avatar.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Time } from "../../ui/Time.tsx";

// AgentCard is the card of an agent, which leads to its sessions. A full
// card also says what the agent is for and when it was created. badge
// stands in the corner of the card instead of its arrow, and more, a
// button, takes the corner for itself.
export function AgentCard({
  context,
  agent,
  full,
  badge,
  more,
}: {
  context: ProjectContext;
  agent: Tinted<Agent>;
  full?: boolean;
  badge?: ReactNode;
  more?: ReactNode;
}) {
  const provider = context.providers.find(
    (other) => other.id === agent.model.provider,
  );
  const servers = context.mcpServers.filter((server) =>
    (agent.mcp_servers ?? []).includes(server.id),
  );
  const card = (
    <Link
      className="card agent"
      to={`/p/${context.project.slug}/agents/${agent.slug}`}
    >
      <div className="top">
        <AgentTile agent={agent} size="xl" />
        {!more &&
          (badge || (
            <span className="go">
              <Icon name="out" />
            </span>
          ))}
      </div>
      <h3>
        {label(agent)}
        {full && agent.name && <span className="slug">{agent.slug}</span>}
      </h3>
      <p className="model">
        {provider ? provider.name : "Unknown provider"} /{" "}
        <code>{agent.model.name}</code>
      </p>
      {full && (
        <p className={cls("ins", !agent.description && "none")}>
          {agent.description || "No description"}
        </p>
      )}
      <ul>
        <li>
          <Icon name="term" />
          <span>
            <code>{agent.harness}</code> harness
          </span>
        </li>
        {servers.length > 0 && (
          <li title={servers.map((server) => label(server)).join(", ")}>
            <Icon name="plug" />
            <span>
              {servers.length > 2
                ? `${servers.length} MCP servers`
                : servers.map((server) => label(server)).join(", ")}
            </span>
          </li>
        )}
        {full && (
          <li>
            <Icon name="clock" />
            <span>
              Created <Time value={agent.created_at} />
            </span>
          </li>
        )}
      </ul>
    </Link>
  );
  return more ? (
    <div className="ag-w">
      {card}
      {more}
    </div>
  ) : (
    card
  );
}
