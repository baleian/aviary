import { resetMcpServers, setMcpServer } from "../../api/conversation.ts";
import { can, type ProjectContext, sessionAccess } from "../../api/project.ts";
import type { Agent, Me, Session } from "../../api/types.ts";
import { oops } from "../../ui/toast.ts";
import type { Compose } from "./compose.ts";

// The MCP servers of a session, or of the session that is about to begin:
// which are on, which the agent begins its sessions with, and how to change
// them, for a caller who may. A turn that runs keeps the servers that it
// began with.
export interface McpChoice {
  on: string[];
  defaults: string[];
  onToggle?: (server: string, on: boolean) => void;
  // Goes back to the servers of the agent.
  onReset?: () => void;
  running: boolean;
}

// mcpOf works out the MCP servers of a session, or of a new one of the agent
// if there is none.
export function mcpOf({
  context,
  agent,
  me,
  compose,
  session,
  running = false,
}: {
  context: ProjectContext;
  agent: Agent;
  me: Me;
  compose: Compose;
  session?: Session;
  running?: boolean;
}): McpChoice {
  // The servers of the agent that the project still has.
  const defaults = (agent.mcp_servers ?? []).filter((id) =>
    context.mcpServers.some((server) => server.id === id),
  );
  if (!session) {
    const on = compose.servers ?? defaults;
    const may = can(context, "sessions.run");
    return {
      on,
      defaults,
      running: false,
      onToggle: may
        ? (server, enabled) =>
            compose.setServers(
              enabled
                ? [...on, server]
                : on.filter((other) => other !== server),
            )
        : undefined,
      onReset: may ? () => compose.setServers(undefined) : undefined,
    };
  }
  const may = !session.ended_at && sessionAccess(context, me, session).send;
  return {
    on: session.mcp_servers ?? [],
    defaults,
    running,
    onToggle: may
      ? (server, enabled) =>
          void setMcpServer(session, server, enabled).catch(oops)
      : undefined,
    onReset: may ? () => void resetMcpServers(session).catch(oops) : undefined,
  };
}

// differs reports whether a choice is not that of the agent.
export function differs(choice: McpChoice): boolean {
  return (
    choice.on.length !== choice.defaults.length ||
    choice.on.some((id) => !choice.defaults.includes(id))
  );
}
