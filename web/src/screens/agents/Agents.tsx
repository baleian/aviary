import { useState } from "react";
import { can } from "../../api/project.ts";
import { label } from "../../lib/format.ts";
import { useTitle } from "../../lib/title.ts";
import { useProject } from "../../shell/project.ts";
import { Empty } from "../../ui/Blank.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { AgentCard } from "./AgentCard.tsx";
import { AgentMenu } from "./AgentMenu.tsx";
import { EditAgent } from "./EditAgent.tsx";

// Agents is the screen of the agents of a project.
export function Agents() {
  const context = useProject();
  const manages = can(context, "agents.manage");
  const [creating, setCreating] = useState(false);
  useTitle("Agents", label(context.project));
  const create = manages && (
    <button className="btn pri" type="button" onClick={() => setCreating(true)}>
      <Icon name="plus" />
      New agent
    </button>
  );
  return (
    <section className="page">
      <div className="wrap">
        <div className="pg-h">
          <div>
            <p className="over">{label(context.project)}</p>
            <h1 className="h1">Agents</h1>
          </div>
          <div className="act">{create}</div>
        </div>
        {context.agents.length ? (
          <div className="ag-grid">
            {context.agents.map((agent) => (
              <AgentCard
                key={agent.id}
                context={context}
                agent={agent}
                full
                more={manages && <AgentMenu context={context} agent={agent} />}
              />
            ))}
            {manages && (
              <button
                className="card agent new"
                type="button"
                onClick={() => setCreating(true)}
              >
                <span className="box">
                  <Icon name="plus" className="lg" />
                </span>
                New agent
              </button>
            )}
          </div>
        ) : (
          <div className="card">
            <Empty glyph="spark" title="No agents yet" action={create} />
          </div>
        )}
      </div>
      {creating && (
        <EditAgent context={context} onClose={() => setCreating(false)} />
      )}
    </section>
  );
}
