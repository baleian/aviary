import { Link, useMatch, useNavigate, useParams } from "react-router";
import { can } from "../../api/project.ts";
import { useMe } from "../../api/queries.ts";
import { label } from "../../lib/format.ts";
import { useTitle } from "../../lib/title.ts";
import { useProject } from "../../shell/project.ts";
import { Missing } from "../../shell/ProjectFrame.tsx";
import { AgentTile } from "../../ui/Avatar.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { AgentMenu } from "../agents/AgentMenu.tsx";
import { Conversation } from "./Conversation.tsx";
import { SessionList } from "./SessionList.tsx";

// Workspace is the screen of an agent: its sessions on the left, and beside
// them the conversation of the session in view, or the start of a new one.
export function Workspace() {
  const context = useProject();
  const me = useMe().data!;
  const navigate = useNavigate();
  const { agent: slug } = useParams();
  const sid = useMatch("/p/:project/agents/:agent/s/:sid")?.params.sid;
  const agent = context.agents.find((other) => other.slug === slug);
  useTitle(agent && label(agent), label(context.project));
  if (!agent) return <Missing />;

  const project = `/p/${context.project.slug}`;
  const base = `${project}/agents/${agent.slug}`;
  return (
    <section className="sess no-details">
      <aside className="pane list">
        <div className="l-h">
          <div className="l-top">
            <Link
              className="l-ag"
              to={`${project}/agents`}
              title={"All agents of " + label(context.project)}
            >
              <AgentTile agent={agent} />
              <div>
                <b>{label(agent)}</b>
                <small>
                  {agent.harness} · {agent.model.name}
                </small>
              </div>
            </Link>
            <AgentMenu
              context={context}
              agent={agent}
              size="sm"
              onDeleted={() => void navigate(`${project}/agents`)}
            />
          </div>
          {can(context, "sessions.run") && (
            <Link className="btn pri wide" to={base}>
              <Icon name="plus" />
              New session
            </Link>
          )}
        </div>
        <SessionList
          key={agent.id}
          context={context}
          agent={agent}
          me={me}
          current={sid}
        />
      </aside>
      <main className="conv">
        {sid ? (
          <Conversation
            key={sid}
            context={context}
            agent={agent}
            me={me}
            id={sid}
          />
        ) : (
          <>
            <div className="conv-h">
              <div className="tx">
                <h1>
                  <span>New session</span>
                </h1>
              </div>
            </div>
            <div className="welcome">
              <div className="box">
                <AgentTile agent={agent} size="xxl" />
                <h2>{label(agent)}</h2>
                {agent.description && <p>{agent.description}</p>}
                <div className="facts">
                  <span className="tag">{agent.harness}</span>
                  <span className="tag">{agent.model.name}</span>
                  {agent.approval === "ask" && (
                    <span className="tag y">asks before it acts</span>
                  )}
                </div>
              </div>
            </div>
          </>
        )}
      </main>
    </section>
  );
}
