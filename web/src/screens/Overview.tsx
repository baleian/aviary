import { useQueries } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import { Link } from "react-router";
import { readSessions, useFeedReady } from "../api/feed.ts";
import { can, type ProjectContext } from "../api/project.ts";
import { type Tinted, useMe } from "../api/queries.ts";
import type {
  Agent,
  ApprovalRequest,
  Me,
  Member,
  Provider,
  Session,
} from "../api/types.ts";
import { initials, label, plural } from "../lib/format.ts";
import {
  type Standing,
  standing,
  standingText,
  working,
} from "../lib/sessions.ts";
import { useTitle } from "../lib/title.ts";
import { useProject } from "../shell/project.ts";
import { AgentTile, Avatar, ProjectTile, RoleBadge } from "../ui/Avatar.tsx";
import { Empty, Loading } from "../ui/Blank.tsx";
import { Chip } from "../ui/Chip.tsx";
import { Icon, Use } from "../ui/Icon.tsx";
import type { IconName } from "../ui/icons.ts";
import { Time } from "../ui/Time.tsx";
import { person } from "../ui/who.ts";
import { AgentCard } from "./agents/AgentCard.tsx";
import { EditAgent } from "./agents/EditAgent.tsx";

// How many of the caller's sessions the overview shows, and how many agents.
const RECENT = 6;
const AGENTS = 8;

const ROLES = ["owner", "editor", "user", "viewer", "auditor", "custom"];

function greeting(me: Me): string {
  const hour = new Date().getHours();
  const part =
    hour < 5
      ? "Good evening"
      : hour < 12
        ? "Good morning"
        : hour < 18
          ? "Good afternoon"
          : "Good evening";
  const name = (me.display_name || "").split(" ")[0];
  return name ? `${part}, ${name}` : part;
}

// KeyPill says where the key of a model provider is kept.
export function KeyPill({
  context,
  provider,
}: {
  context: ProjectContext;
  provider: Provider;
}) {
  const key = provider.api_key;
  if (!key) return <span className="pill">No key</span>;
  if ("stored" in key) {
    return (
      <span className="pill g">
        <Icon name="lock" />
        Key stored
      </span>
    );
  }
  const store = context.stores.find((other) => other.id === key.store);
  return (
    <span className="pill" title={`${key.path} → ${key.key}`}>
      <Icon name="key" />
      Key in {store ? store.name : "a secret store"}
    </span>
  );
}

// wants says in a few words what an approval request asks about.
function wants(request: ApprovalRequest): ReactNode {
  const call = request.tool_call ?? {};
  const input = call.input as { command?: unknown } | undefined;
  if (typeof input?.command === "string" && input.command) {
    return (
      <>
        Wants to run <code>{input.command}</code>
      </>
    );
  }
  return <>Wants to: {call.title || call.kind || "act"}</>;
}

const MARKS: Partial<Record<Standing, IconName>> = {
  queued: "clock",
  completed: "check",
  failed: "x",
  cancelled: "cancel",
  expired: "hour",
  ended: "lock",
  none: "chat",
};

// A session of the caller with its agent, and how it stands.
interface Row {
  session: Session;
  agent: Tinted<Agent>;
  stands: Standing;
}

function SessionRow({ context, row }: { context: ProjectContext; row: Row }) {
  const { session, agent, stands } = row;
  const tone = stands === "none" ? "idle" : stands;
  const failure = stands === "failed" && session.last_turn?.failure;
  return (
    <Link
      to={`/p/${context.project.slug}/agents/${agent.slug}/s/${session.id}`}
    >
      <span className={`sti st-${tone}`}>
        {working(stands) ? (
          <span className="ring" />
        ) : (
          <Icon name={MARKS[stands] ?? "cancel"} className="lg" />
        )}
      </span>
      <span className="tx">
        {session.title ? (
          <b>{session.title}</b>
        ) : (
          <b className="untitled">Untitled session</b>
        )}
        <small>
          <AgentTile agent={agent} size="xs" />
          {label(agent)} · <Time value={session.created_at} />
          {failure && (
            <>
              {" · "}
              <span className="err">{failure.message || failure.reason}</span>
            </>
          )}
        </small>
      </span>
      <Chip tone={tone}>
        {stands === "waiting" ? "Waiting for approval" : standingText(session)}
      </Chip>
    </Link>
  );
}

// useRecent reads the caller's newest sessions in the project. The API lists
// sessions by agent, so the console asks every agent and merges.
function useRecent(context: ProjectContext, me: Me) {
  const reads =
    can(context, "sessions.run") || can(context, "sessions.view_all");
  // The sessions are read once the feed can keep them up to date.
  const ready = useFeedReady();
  const lists = useQueries({
    queries: context.agents.map((agent) => ({
      queryKey: ["sessions", agent.id, { limit: RECENT }],
      queryFn: () => readSessions(agent.id, `limit=${RECENT}`),
      enabled: reads && ready,
    })),
  });
  const rows: Row[] = [];
  for (const [index, list] of lists.entries()) {
    for (const session of list.data ?? []) {
      // Without an identity provider every session is the caller's.
      if (me.id && session.created_by !== me.id) continue;
      const agent = context.agents[index];
      rows.push({ session, agent, stands: standing(session) });
    }
  }
  rows.sort((a, b) => (a.session.id < b.session.id ? 1 : -1));
  return {
    rows: rows.slice(0, RECENT),
    pending: reads && (!ready || lists.some((list) => list.isPending)),
    error: lists.find((list) => list.error)?.error,
  };
}

// Start is the first steps of a project without agents.
function Start({
  context,
  onNewAgent,
}: {
  context: ProjectContext;
  onNewAgent: () => void;
}) {
  const connected = context.providers.length > 0;
  const step = (
    n: number,
    state: "done" | "now" | "later",
    heading: string,
    text: string,
    action?: ReactNode,
  ) => (
    <div className={state}>
      <span className="n">{state === "done" ? <Icon name="check" /> : n}</span>
      <b>{heading}</b>
      <p>{text}</p>
      {state === "now" && action}
    </div>
  );
  return (
    <div className="card start">
      {step(
        1,
        connected ? "done" : "now",
        "Add a model provider",
        "The endpoint of a model, and its key.",
        can(context, "connections.manage") ? (
          <Link
            className="btn pri sm"
            to={`/p/${context.project.slug}/connections`}
          >
            Add a provider
          </Link>
        ) : (
          <p className="sub sm">Ask someone who manages connections.</p>
        ),
      )}
      {step(
        2,
        connected ? "now" : "later",
        "Create an agent",
        "A harness, a model and instructions.",
        can(context, "agents.manage") ? (
          <button className="btn pri sm" type="button" onClick={onNewAgent}>
            New agent
          </button>
        ) : (
          <p className="sub sm">Ask someone who manages agents.</p>
        ),
      )}
      {step(3, "later", "Start a session", "Send the agent a message.")}
    </div>
  );
}

function Face({
  me,
  members,
  member,
}: {
  me: Me;
  members: Member[];
  member: Member;
}) {
  if (member.group) {
    return (
      <span className="av grp" title={"Group " + member.group}>
        <Icon name="users" />
      </span>
    );
  }
  const user = member.user!;
  if (!user.id) {
    return (
      <span
        className="av wait"
        title={user.username + " has not signed in yet"}
      >
        {initials(user.username)[0].toLowerCase()}
      </span>
    );
  }
  return <Avatar who={person(me, members, user.id)} />;
}

// Overview is where a person lands in a project: they pick an agent, or go
// back to what they were doing.
export function Overview() {
  const context = useProject();
  const me = useMe().data!;
  const { project, agents, providers, stores, members } = context;
  const base = `/p/${project.slug}`;
  const recent = useRecent(context, me);
  const [creating, setCreating] = useState(false);
  useTitle("Overview", label(project));

  const waiting = recent.rows.find((row) => row.stands === "waiting");
  const badge = (agent: Agent) => {
    const mine = recent.rows.filter((row) => row.agent.id === agent.id);
    if (mine.some((row) => row.stands === "waiting")) {
      return <Chip tone="waiting">Needs you</Chip>;
    }
    const busy = mine.some(
      (row) => row.stands === "queued" || working(row.stands),
    );
    return busy ? <Chip tone="running">Working</Chip> : undefined;
  };

  const people = members.filter((member) => member.user?.id).length;
  const awaited = members.filter(
    (member) => member.user && !member.user.id,
  ).length;
  const groups = members.filter((member) => member.group).length;
  const kinds = [
    people && plural(people, "person", "people"),
    groups && plural(groups, "group"),
    awaited && `${awaited} awaited`,
  ]
    .filter(Boolean)
    .join(", ");
  const fact = (count: number, one: string) => (
    <span>
      <b>{count}</b>
      {count === 1 ? one : one + "s"}
    </span>
  );

  return (
    <section className="page">
      <div className="wrap">
        <div className="hero">
          <Use symbol="dome" className="ribs" viewBox="0 0 600 400" />
          <div>
            <p className="over">{greeting(me)}</p>
            <h1>
              <ProjectTile project={project} size="lg" />
              {label(project)}
              {project.name && <code>{project.slug}</code>}
            </h1>
            <p className="facts">
              {fact(agents.length, "agent")}
              {fact(providers.length, "model provider")}
              {fact(stores.length, "secret store")}
              {fact(members.length, "member")}
            </p>
          </div>
          {waiting && (
            <Link
              className="nudge"
              to={`${base}/agents/${waiting.agent.slug}/s/${waiting.session.id}`}
            >
              <span>
                <Icon name="shield" className="lg" />
              </span>
              <div className="tx">
                <b>{label(waiting.agent)} is waiting for you</b>
                <small>{wants(waiting.session.last_turn!.approvals![0])}</small>
              </div>
              <span className="btn pri sm">Review</span>
            </Link>
          )}
        </div>

        {agents.length ? (
          <>
            <div className="sec-h">
              <h2 className="h2">Agents</h2>
              <span className="cnt">{agents.length}</span>
              {agents.length > AGENTS && (
                <Link className="more" to={`${base}/agents`}>
                  All agents
                </Link>
              )}
              {can(context, "agents.manage") && (
                <button
                  className="btn pri"
                  type="button"
                  onClick={() => setCreating(true)}
                >
                  <Icon name="plus" />
                  New agent
                </button>
              )}
            </div>
            <div className="agents">
              {agents.slice(0, AGENTS).map((agent) => (
                <AgentCard
                  key={agent.id}
                  context={context}
                  agent={agent}
                  badge={badge(agent)}
                />
              ))}
            </div>
          </>
        ) : (
          <Start context={context} onNewAgent={() => setCreating(true)} />
        )}

        <div className="cols">
          <div className="card">
            <div className="sec-h">
              <h2 className="h2">Your recent sessions</h2>
            </div>
            <div className="rows">
              {recent.error ? (
                <p className="sub" style={{ padding: "12px 20px" }}>
                  Could not load the sessions: {recent.error.message}
                </p>
              ) : recent.pending ? (
                <div
                  style={{
                    padding: "12px 20px 16px",
                    borderTop: "1px solid var(--line)",
                  }}
                >
                  <Loading lines={3} />
                </div>
              ) : recent.rows.length ? (
                recent.rows.map((row) => (
                  <SessionRow
                    key={row.session.id}
                    context={context}
                    row={row}
                  />
                ))
              ) : (
                <div style={{ borderTop: "1px solid var(--line)" }}>
                  <Empty
                    glyph="chat"
                    title="No sessions yet"
                    text={
                      can(context, "sessions.run") &&
                      "Open an agent to start one."
                    }
                  />
                </div>
              )}
            </div>
          </div>
          <div className="card">
            <div className="sec-h">
              <h2 className="h2">Connections</h2>
              <Link className="more" to={`${base}/connections`}>
                {can(context, "connections.manage") ? "Manage" : "See all"}
              </Link>
            </div>
            <div className="conn">
              <h4>Model providers · {providers.length}</h4>
              {providers.length ? (
                <ul>
                  {providers.map((provider) => (
                    <li key={provider.id}>
                      <span className="ic">
                        <Icon name="spark" className="lg" />
                      </span>
                      <span className="tx">
                        <b>{provider.name}</b>
                        <code>{provider.url}</code>
                      </span>
                      <KeyPill context={context} provider={provider} />
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="none">None yet</p>
              )}
              <h4>Secret stores · {stores.length}</h4>
              {stores.length ? (
                <ul>
                  {stores.map((store) => (
                    <li key={store.id}>
                      <span className="ic">
                        <Icon name="db" className="lg" />
                      </span>
                      <span className="tx">
                        <b>{store.name}</b>
                        <code>
                          {String(store.settings?.address ?? "")} · mount{" "}
                          {String(store.settings?.mount ?? "")}
                        </code>
                      </span>
                      <span className="tag">{store.kind}</span>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="none">None yet</p>
              )}
            </div>
          </div>
        </div>

        <div className="card mstrip">
          <h2 className="h2">Members</h2>
          {members.length > 0 && (
            <div className="stack">
              {members.slice(0, 7).map((member) => (
                <Face
                  key={member.id}
                  me={me}
                  members={members}
                  member={member}
                />
              ))}
            </div>
          )}
          {members.length ? (
            <p>
              <b>{plural(members.length, "member")}</b>
              {kinds && " · " + kinds}
            </p>
          ) : (
            <p>
              {me.id
                ? "No members yet"
                : "No sign-in: everyone is an administrator"}
            </p>
          )}
          <div className="by">
            {ROLES.filter((role) =>
              members.some((member) => member.role === role),
            ).map((role) => (
              <RoleBadge key={role} role={role} />
            ))}
          </div>
          <Link className="btn sec sm" to={`${base}/access`}>
            {me.admin ? "Manage access" : "See access"}
          </Link>
        </div>
      </div>
      {creating && (
        <EditAgent context={context} onClose={() => setCreating(false)} />
      )}
    </section>
  );
}
