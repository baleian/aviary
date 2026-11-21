import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { Link } from "react-router";
import { ApiError } from "../../api/client.ts";
import {
  followTurn,
  joinTurn,
  olderTurns,
  stopTurn,
  useSession,
  useTurns,
} from "../../api/conversation.ts";
import { controls, type ProjectContext } from "../../api/project.ts";
import type { Tinted } from "../../api/queries.ts";
import type { Agent, Me, Session, Turn } from "../../api/types.ts";
import { plural, sentence } from "../../lib/format.ts";
import { followed, STATUS_NAMES } from "../../lib/sessions.ts";
import { Avatar } from "../../ui/Avatar.tsx";
import { Blank, Empty, Loading } from "../../ui/Blank.tsx";
import { Chip } from "../../ui/Chip.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Time } from "../../ui/Time.tsx";
import { oops } from "../../ui/toast.ts";
import { person } from "../../ui/who.ts";
import { barred, type Compose } from "./compose.ts";
import { Composer, Gate } from "./Composer.tsx";
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
  const stands = newest && followed(newest);
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
          ) : stands ? (
            <Chip tone={stands}>
              {stands === "waiting"
                ? "Waiting for approval"
                : STATUS_NAMES[stands]}
            </Chip>
          ) : (
            <Chip tone="idle">No messages yet</Chip>
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

// How near its end the conversation has to be scrolled to stay there as it
// grows, in pixels.
const NEAR = 96;

const NONE: Turn[] = [];

// Conversation is a session: its head, its turns from the oldest that was
// read to the newest, and the box for the next message. It opens at the
// newest turn, and follows the turn that has not ended.
export function Conversation({
  context,
  agent,
  me,
  id,
  compose,
}: {
  context: ProjectContext;
  agent: Tinted<Agent>;
  me: Me;
  id: string;
  compose: Compose;
}) {
  const session = useSession(id);
  const turns = useTurns(id);
  const scroller = useRef<HTMLDivElement>(null);
  // How tall the turns were before older ones were added above them.
  const before = useRef<number | null>(null);
  // Whether the view stays at the end of the conversation as it grows.
  const stick = useRef(true);
  const [away, setAway] = useState(false);
  const [loading, setLoading] = useState(false);
  const list = turns.data ?? NONE;
  const newest = list.at(-1);
  const who = useCallback(
    (user?: string) => person(me, context.members, user),
    [me, context.members],
  );

  const unfinished = newest && !newest.ended_at ? newest.id : undefined;
  useEffect(() => {
    if (unfinished) return followTurn(id, unfinished, oops);
  }, [id, unfinished]);

  // A turn that began elsewhere joins the turns that are shown.
  const last = session.data?.last_turn;
  const joins =
    turns.data && last && last.seq > (newest?.seq ?? 0) ? last.id : undefined;
  useEffect(() => {
    if (joins) joinTurn(id, joins).catch(oops);
  }, [id, joins]);

  // Older turns that are added above leave what is in view where it is.
  // Otherwise a conversation that is at its end stays there.
  useLayoutEffect(() => {
    const node = scroller.current;
    if (!node) return;
    if (before.current != null) {
      node.scrollTop += node.scrollHeight - before.current;
      before.current = null;
    } else if (stick.current) {
      node.scrollTop = node.scrollHeight;
    }
  }, [list]);

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

  function scrolled() {
    const node = scroller.current!;
    const left = node.scrollHeight - node.scrollTop - node.clientHeight;
    stick.current = left < NEAR;
    setAway(!stick.current);
  }

  function toEnd() {
    const node = scroller.current!;
    stick.current = true;
    node.scrollTop = node.scrollHeight;
    setAway(false);
  }

  const live = unfinished ? newest : undefined;
  // What stops the turn that has not ended, for a caller who may.
  const stop =
    live && controls(context, me, live)
      ? () => void stopTurn(id, live.id).catch(oops)
      : undefined;
  const why = barred(context, me, session.data);

  return (
    <>
      <Header
        context={context}
        me={me}
        session={session.data}
        newest={newest}
      />
      <div className="conv-s" ref={scroller} onScroll={scrolled}>
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
              agent={agent}
              person={who}
              waking={turn.id === compose.woken}
              onStop={turn.id === unfinished ? stop : undefined}
              onAgain={why ? undefined : compose.fill}
            />
          ))}
          {!list.length && (
            <div style={{ paddingTop: "48px" }}>
              <Empty glyph="chat" title="No messages yet" />
            </div>
          )}
        </div>
      </div>
      {away && unfinished && (
        <button className="btn sec sm jump" type="button" onClick={toEnd}>
          <Icon name="dn" />
          Latest
        </button>
      )}
      {why ? (
        <Gate
          why={why}
          context={context}
          agent={agent}
          me={me}
          session={session.data}
        />
      ) : (
        <Composer
          agent={agent}
          compose={compose}
          turn={live}
          // A message that is sent is seen: the conversation goes to its
          // end.
          onSend={() => {
            toEnd();
            void compose.send();
          }}
          onStop={stop}
        />
      )}
    </>
  );
}
