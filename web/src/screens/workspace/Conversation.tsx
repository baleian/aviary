import { useLayoutEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { ApiError } from "../../api/client.ts";
import { olderTurns, useSession, useTurns } from "../../api/conversation.ts";
import type { ProjectContext } from "../../api/project.ts";
import type { Tinted } from "../../api/queries.ts";
import type { Agent, Me, Session, Turn } from "../../api/types.ts";
import { plural, sentence } from "../../lib/format.ts";
import { standing, STATUS_NAMES } from "../../lib/sessions.ts";
import { Avatar } from "../../ui/Avatar.tsx";
import { Blank, Empty, Loading } from "../../ui/Blank.tsx";
import { Chip } from "../../ui/Chip.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Time } from "../../ui/Time.tsx";
import { oops } from "../../ui/toast.ts";
import { person } from "../../ui/who.ts";
import { SandboxBadge, SessionTitle } from "./marks.tsx";
import { TurnView } from "./TurnView.tsx";

// Header is the head of a conversation: its title, how it stands, and when
// it began.
function Header({
  context,
  me,
  session,
  newest,
}: {
  context: ProjectContext;
  me: Me;
  session: Session;
  newest?: Turn;
}) {
  const stands = standing(session);
  const owner =
    me.id && session.created_by !== me.id
      ? person(me, context.members, session.created_by)
      : null;
  const ended = session.end_reason === "expired" ? "Expired" : "Ended";
  return (
    <div className="conv-h">
      <div className="tx">
        <h1>
          <SessionTitle session={session} tag="span" />
        </h1>
        <p>
          {session.ended_at ? (
            <Chip tone="ended">{ended}</Chip>
          ) : stands === "none" ? (
            <Chip tone="idle">No messages yet</Chip>
          ) : (
            <Chip tone={stands}>
              {stands === "waiting"
                ? "Waiting for approval"
                : STATUS_NAMES[stands]}
            </Chip>
          )}
          <SandboxBadge session={session} />
          {owner && (
            <span
              style={{
                display: "inline-flex",
                gap: "6px",
                alignItems: "center",
              }}
            >
              <Avatar who={owner} size="xs" />
              {owner.name}
            </span>
          )}
          <span>
            Started <Time value={session.created_at} />
            {newest && ` · ${plural(newest.seq, "turn")}`}
            {session.ended_at && (
              <>
                {` · ${ended.toLowerCase()} `}
                <Time value={session.ended_at} />
              </>
            )}
          </span>
        </p>
      </div>
    </div>
  );
}

// Conversation is a session: its head, and its turns from the oldest that
// was read to the newest. It opens at the newest turn.
export function Conversation({
  context,
  agent,
  me,
  id,
}: {
  context: ProjectContext;
  agent: Tinted<Agent>;
  me: Me;
  id: string;
}) {
  const session = useSession(id);
  const turns = useTurns(id);
  const scroller = useRef<HTMLDivElement>(null);
  // How tall the turns were before older ones were added above them.
  const before = useRef<number | null>(null);
  const [loading, setLoading] = useState(false);
  const list = turns.data ?? [];
  const opened = turns.isSuccess && session.isSuccess;

  // The conversation opens at its end. Older turns that are added above
  // leave what is in view where it is.
  useLayoutEffect(() => {
    const node = scroller.current;
    if (!node || !opened) return;
    if (before.current == null) node.scrollTop = node.scrollHeight;
    else node.scrollTop += node.scrollHeight - before.current;
    before.current = null;
  }, [opened, id, list.length]);

  const error = session.error ?? turns.error;
  const base = `/p/${context.project.slug}/agents/${agent.slug}`;
  const foreign = session.data && session.data.agent_id !== agent.id;
  if (error || foreign) {
    const missing =
      foreign || (error instanceof ApiError && error.status === 404);
    return (
      <Blank
        glyph={missing ? "search" : "warn"}
        title={missing ? "No such session" : "Could not open the session"}
        text={
          missing
            ? "It does not exist, or you do not have access to it."
            : sentence(error?.message ?? "")
        }
        actions={
          <Link className="btn sec" to={base}>
            Start a new session
          </Link>
        }
      />
    );
  }
  if (!session.data || !turns.data) {
    return (
      <div className="welcome">
        <div style={{ width: "420px" }}>
          <Loading lines={4} />
        </div>
      </div>
    );
  }

  const more = list.length > 0 && list[0].seq > 1;
  async function older() {
    setLoading(true);
    before.current = scroller.current?.scrollHeight ?? null;
    try {
      await olderTurns(id);
    } catch (failure) {
      before.current = null;
      oops(failure);
    }
    setLoading(false);
  }

  return (
    <>
      <Header
        context={context}
        me={me}
        session={session.data}
        newest={list[list.length - 1]}
      />
      <div className="conv-s" ref={scroller}>
        <div className="thread">
          <div className="more-turns">
            {more && (
              <button
                className="btn sec sm"
                type="button"
                disabled={loading}
                onClick={() => void older()}
              >
                <Icon name="up" />
                Earlier turns
              </button>
            )}
          </div>
          {list.map((turn) => (
            <TurnView
              key={turn.id}
              turn={turn}
              items={turn.items ?? []}
              agent={agent}
              person={(user) => person(me, context.members, user)}
            />
          ))}
          {!list.length && (
            <div style={{ paddingTop: "48px" }}>
              <Empty glyph="chat" title="No messages yet" />
            </div>
          )}
        </div>
      </div>
    </>
  );
}
