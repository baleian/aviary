import { useState } from "react";
import { Link, useMatch, useNavigate, useParams } from "react-router";
import { can, type ProjectContext } from "../../api/project.ts";
import { type Tinted, useMe } from "../../api/queries.ts";
import type { Agent, Me } from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { label } from "../../lib/format.ts";
import { prefs } from "../../lib/prefs.ts";
import { useTitle } from "../../lib/title.ts";
import { useProject } from "../../shell/project.ts";
import { Missing } from "../../shell/ProjectFrame.tsx";
import { AgentTile } from "../../ui/Avatar.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { AgentMenu } from "../agents/AgentMenu.tsx";
import { barred, useCompose } from "./compose.ts";
import { Composer, Gate } from "./Composer.tsx";
import { Conversation } from "./Conversation.tsx";
import { Details } from "./Details.tsx";
import { mcpOf } from "./servers.ts";
import { SessionList } from "./SessionList.tsx";
import { type Pane, PaneButtons, Split } from "./Split.tsx";

// Workspace is the screen of an agent: its sessions on the left, and beside
// them the conversation of the session in view, or the start of a new one.
export function Workspace() {
  const context = useProject();
  const me = useMe().data!;
  const { agent: slug } = useParams();
  const agent = context.agents.find((other) => other.slug === slug);
  useTitle(agent && label(agent), label(context.project));
  if (!agent) return <Missing />;
  return <Sessions context={context} agent={agent} me={me} />;
}

// paneKept returns the pane that the caller had beside the conversations:
// the details, unless the caller chose otherwise.
function paneKept(): Pane {
  const kept = prefs.get("pane");
  if (kept === "files") return kept;
  return kept === "none" ? null : "details";
}

function Sessions({
  context,
  agent,
  me,
}: {
  context: ProjectContext;
  agent: Tinted<Agent>;
  me: Me;
}) {
  const navigate = useNavigate();
  const sid = useMatch("/p/:project/agents/:agent/s/:sid")?.params.sid;
  // The message is the workspace's, so that the first message of a session
  // stays while the session is created and opens.
  const compose = useCompose(context, agent, sid);
  const project = `/p/${context.project.slug}`;
  const base = `${project}/agents/${agent.slug}`;
  const why = barred(context, me);
  const mcp = mcpOf({ context, agent, me, compose });
  const [pane, setPane] = useState(paneKept);
  const choose = (next: Pane) => {
    prefs.set("pane", next ?? "none");
    setPane(next);
  };
  // The sessions show unless the caller has hidden them, here or before.
  const [list, setList] = useState(() => prefs.get("list") !== "0");
  const lead = (
    <button
      className="ib lst"
      type="button"
      title={list ? "Hide the sessions" : "Show the sessions"}
      aria-pressed={list}
      onClick={() => {
        prefs.set("list", list ? "0" : "1");
        setList(!list);
      }}
    >
      <Icon name="panel" className="lg flip" />
    </button>
  );
  return (
    <section className={cls("sess", !list && "no-list")}>
      {list && (
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
      )}
      {sid ? (
        <Conversation
          key={sid}
          context={context}
          agent={agent}
          me={me}
          id={sid}
          compose={compose}
          pane={pane}
          onPane={choose}
          lead={lead}
        />
      ) : (
        <Split
          beside={pane === "details" ? "details" : undefined}
          side={
            pane === "details" && (
              <Details
                context={context}
                agent={agent}
                me={me}
                mcp={mcp}
                onClose={() => choose(null)}
              />
            )
          }
        >
          <main className="conv">
            <div className="conv-h">
              {lead}
              <div className="tx">
                <h1>
                  <span>New session</span>
                </h1>
              </div>
              <PaneButtons pane={pane} files={false} onPane={choose} />
            </div>
            <div className="welcome">
              <div className="box">
                <AgentTile agent={agent} size="xxl" />
                <h2>{label(agent)}</h2>
                {agent.description && <p>{agent.description}</p>}
                <div className="facts">
                  <span className="tag">{agent.harness}</span>
                  <span className="tag">{agent.model.name}</span>
                </div>
              </div>
            </div>
            {why ? (
              <Gate why={why} context={context} agent={agent} me={me} />
            ) : (
              <Composer
                context={context}
                agent={agent}
                compose={compose}
                mcp={mcp}
                onSend={() => void compose.send()}
              />
            )}
          </main>
        </Split>
      )}
    </section>
  );
}
