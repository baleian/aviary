import { useState } from "react";
import { Link } from "react-router";
import { olderSessions, useSessionList } from "../../api/conversation.ts";
import { can, type ProjectContext } from "../../api/project.ts";
import type { Tinted } from "../../api/queries.ts";
import type { Agent, Me, Session } from "../../api/types.ts";
import { standing, standingText } from "../../lib/sessions.ts";
import { Avatar } from "../../ui/Avatar.tsx";
import { Loading } from "../../ui/Blank.tsx";
import { Segmented } from "../../ui/Segmented.tsx";
import { Time } from "../../ui/Time.tsx";
import { oops } from "../../ui/toast.ts";
import { person } from "../../ui/who.ts";
import { SandboxMark, SessionTitle, StateIcon } from "./marks.tsx";

// SessionList is the list of the sessions of an agent, newest first, with
// the one in view marked. A caller who reads the sessions of others can
// leave them out.
export function SessionList({
  context,
  agent,
  me,
  current,
}: {
  context: ProjectContext;
  agent: Tinted<Agent>;
  me: Me;
  // The ID of the session in view.
  current?: string;
}) {
  const list = useSessionList(agent.id);
  const [filter, setFilter] = useState<"all" | "mine">("all");
  const [loading, setLoading] = useState(false);
  const readsAll = !!me.id && (me.admin || can(context, "sessions.view_all"));
  const sessions = list.data?.sessions ?? [];
  const shown = sessions.filter(
    (session) => filter === "all" || session.created_by === me.id,
  );
  const base = `/p/${context.project.slug}/agents/${agent.slug}`;

  async function older() {
    setLoading(true);
    try {
      await olderSessions(agent.id);
    } catch (error) {
      oops(error);
    }
    setLoading(false);
  }

  const row = (session: Session) => {
    const stands = standing(session);
    const owner =
      me.id && session.created_by !== me.id
        ? person(me, context.members, session.created_by)
        : null;
    const failure = stands === "failed" && session.last_turn?.failure;
    const on = session.id === current;
    return (
      <Link
        key={session.id}
        className={on ? "on" : undefined}
        aria-current={on ? "page" : undefined}
        to={`${base}/s/${session.id}`}
      >
        <span className="sic">
          <StateIcon stands={stands} />
        </span>
        <span className="tx">
          <SessionTitle session={session} />
          <small>
            {owner && (
              <span className="own">
                <Avatar who={owner} size="xs" />
                {owner.name}
              </span>
            )}
            <span>
              <Time value={session.created_at} /> ·{" "}
              {stands === "waiting" ? (
                <span className="c-wait">Needs approval</span>
              ) : (
                standingText(session)
              )}
            </span>
            {failure && (
              <span className="err">{failure.message || failure.reason}</span>
            )}
          </small>
        </span>
        <SandboxMark session={session} />
      </Link>
    );
  };

  return (
    <div className="pane-s">
      <div className="l-t">
        Sessions
        {readsAll && (
          <Segmented
            choices={[
              ["all", "All"],
              ["mine", "Mine"],
            ]}
            value={filter}
            onChange={setFilter}
          />
        )}
      </div>
      <nav className="sl" aria-label="Sessions">
        {list.isPending ? (
          <div style={{ padding: "6px 10px" }}>
            <Loading lines={4} />
          </div>
        ) : shown.length ? (
          shown.map(row)
        ) : (
          <p className="sub sm" style={{ padding: "8px 10px 12px" }}>
            {sessions.length ? "None of these are yours" : "No sessions yet"}
          </p>
        )}
      </nav>
      <div className="sl-more">
        {list.data?.more && (
          <button
            className="btn quiet sm"
            type="button"
            disabled={loading}
            onClick={() => void older()}
          >
            Load older
          </button>
        )}
      </div>
    </div>
  );
}
