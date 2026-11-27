import { Link } from "react-router";
import type { ProjectContext } from "../../api/project.ts";
import type { Tinted } from "../../api/queries.ts";
import type { Agent, Me, Session, Turn } from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { label, num } from "../../lib/format.ts";
import { AgentTile, Avatar } from "../../ui/Avatar.tsx";
import { CopyButton } from "../../ui/CopyButton.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Time } from "../../ui/Time.tsx";
import { person } from "../../ui/who.ts";
import { SandboxBadge } from "./marks.tsx";
import { McpSummary } from "./Mcp.tsx";
import type { McpChoice } from "./servers.ts";

// DetailsButton shows and hides the details beside a conversation.
export function DetailsButton({
  shown,
  onToggle,
}: {
  shown: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      className={cls("btn sec sm dtl", shown && "tog")}
      type="button"
      aria-pressed={shown}
      onClick={onToggle}
    >
      <Icon name="panel" />
      Details
    </button>
  );
}

// Facts is what the details say of a session: whose it is, how long it has
// been, and what its turns took. turns are those that were read, which may
// not be all.
function Facts({
  context,
  me,
  session,
  turns,
}: {
  context: ProjectContext;
  me: Me;
  session: Session;
  turns: Turn[];
}) {
  const owner = person(me, context.members, session.created_by);
  const newest = turns.at(-1);
  const partial = !!newest && turns.length < newest.seq;
  const counts = new Map<string, number>();
  for (const turn of turns) {
    counts.set(turn.state, (counts.get(turn.state) ?? 0) + 1);
  }
  const states = [...counts]
    .map(([state, count]) => `${count} ${state}`)
    .join(", ");
  const sum = (field: "input_tokens" | "output_tokens") =>
    turns.reduce((total, turn) => total + (turn.usage?.[field] ?? 0), 0);
  return (
    <>
      <div className="r-s">
        <div className="idb" title="Session ID">
          <span>{session.id}</span>
          <CopyButton text={session.id} what="session ID" />
        </div>
        <dl className="kv" style={{ marginTop: "12px" }}>
          {/* Without an identity provider nobody owns a session. */}
          {me.id && (
            <>
              <dt>Owner</dt>
              <dd>
                <Avatar who={owner} size="xs" />
                <span title={owner.title}>{owner.name}</span>
              </dd>
            </>
          )}
          <dt>Started</dt>
          <dd>
            <Time value={session.created_at} />
          </dd>
          {session.ended_at && (
            <>
              <dt>{session.end_reason === "expired" ? "Expired" : "Ended"}</dt>
              <dd>
                <Time value={session.ended_at} />
              </dd>
            </>
          )}
          <dt>Turns</dt>
          <dd>
            {newest?.seq ?? 0}
            {states && (
              <small>{partial ? `of those shown: ${states}` : states}</small>
            )}
          </dd>
          <dt>Sandbox</dt>
          <dd>
            <SandboxBadge session={session} always />
          </dd>
        </dl>
      </div>
      <div className="r-s">
        <h4>
          Tokens
          {partial && (
            <small className="sub" style={{ fontWeight: 500 }}>
              of the turns shown
            </small>
          )}
        </h4>
        <div className="tok">
          <div>
            <b>{num(sum("input_tokens"))}</b>
            <small>in</small>
          </div>
          <div>
            <b>{num(sum("output_tokens"))}</b>
            <small>out</small>
          </div>
        </div>
      </div>
    </>
  );
}

// Details is the pane beside a conversation: what there is to know of the
// session in view, and of its agent. Without a session it tells of the
// agent, and while a session is read it is empty. mcp are the MCP servers of
// the session, or of the one that is about to begin.
export function Details({
  context,
  agent,
  me,
  session,
  turns,
  mcp,
  empty,
  onClose,
}: {
  context: ProjectContext;
  agent: Tinted<Agent>;
  me: Me;
  session?: Session;
  turns?: Turn[];
  mcp?: McpChoice;
  empty?: boolean;
  onClose: () => void;
}) {
  if (empty) return <aside className="pane details" />;
  const provider = context.providers.find(
    (other) => other.id === agent.model.provider,
  );
  const servers = mcp && context.mcpServers.length > 0 && (
    <McpSummary context={context} choice={mcp} />
  );
  return (
    <aside className="pane details">
      <div className="r-top">
        <h2 className="h3">
          {session ? "Session details" : "About this agent"}
        </h2>
        <button className="ib sm" type="button" title="Close" onClick={onClose}>
          <Icon name="chev" />
        </button>
      </div>
      <div className="pane-s">
        {session && (
          <Facts
            context={context}
            me={me}
            session={session}
            turns={turns ?? []}
          />
        )}
        {session && servers}
        <div className="r-s">
          <h4>
            <AgentTile agent={agent} size="xs" />
            <Link to={`/p/${context.project.slug}/agents`}>{label(agent)}</Link>
          </h4>
          <dl className="kv">
            <dt>Slug</dt>
            <dd>
              <span className="tag">{agent.slug}</span>
            </dd>
            <dt>Harness</dt>
            <dd>
              <span className="tag">{agent.harness}</span>
            </dd>
            <dt>Model</dt>
            <dd>
              <span className="tag">{agent.model.name}</span>
            </dd>
            <dt>Provider</dt>
            <dd>{provider ? provider.name : "Unknown"}</dd>
          </dl>
          {agent.description && <p className="about">{agent.description}</p>}
        </div>
        {!session && servers}
      </div>
    </aside>
  );
}
