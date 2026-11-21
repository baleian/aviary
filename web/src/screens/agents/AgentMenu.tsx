import { useState } from "react";
import { can, type ProjectContext } from "../../api/project.ts";
import type { Agent } from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { Icon } from "../../ui/Icon.tsx";
import { Menu, MenuItem } from "../../ui/Menu.tsx";
import { DeleteAgent, EditAgent } from "./EditAgent.tsx";

// AgentMenu is the button that opens the menu of an agent, for a caller who
// manages the agents. onDeleted is called after the agent was deleted.
export function AgentMenu({
  context,
  agent,
  size,
  onDeleted,
}: {
  context: ProjectContext;
  agent: Agent;
  size?: "sm";
  onDeleted?: () => void;
}) {
  const [doing, setDoing] = useState<"edit" | "delete" | null>(null);
  if (!can(context, "agents.manage")) return null;
  return (
    <>
      <Menu
        align="right"
        button={(props) => (
          <button
            className={cls("ib", size)}
            type="button"
            title="More"
            {...props}
          >
            <Icon name="dots" />
          </button>
        )}
      >
        <MenuItem
          glyph="edit"
          text="Edit agent"
          onClick={() => setDoing("edit")}
        />
        {/* Deleting an agent ends the sessions of every member. */}
        {can(context, "sessions.control_all") && (
          <>
            <hr />
            <MenuItem
              glyph="trash"
              text="Delete agent…"
              danger
              onClick={() => setDoing("delete")}
            />
          </>
        )}
      </Menu>
      {doing === "edit" && (
        <EditAgent
          context={context}
          existing={agent}
          onClose={() => setDoing(null)}
        />
      )}
      {doing === "delete" && (
        <DeleteAgent
          context={context}
          agent={agent}
          onClose={(deleted) => {
            setDoing(null);
            if (deleted) onDeleted?.();
          }}
        />
      )}
    </>
  );
}
