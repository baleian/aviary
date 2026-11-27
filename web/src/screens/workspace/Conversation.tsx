import {
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { Link } from "react-router";
import { ApiError } from "../../api/client.ts";
import {
  answerRequest,
  followTurn,
  joinTurn,
  noteTurn,
  olderTurns,
  stopTurn,
  suggestTitle,
  useSession,
  useTurns,
} from "../../api/conversation.ts";
import {
  controls,
  type ProjectContext,
  sessionAccess,
} from "../../api/project.ts";
import type { Tinted } from "../../api/queries.ts";
import type { Agent, Me, Session, Turn } from "../../api/types.ts";
import { sentence } from "../../lib/format.ts";
import { Blank, Empty, Loading } from "../../ui/Blank.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { oops, toast } from "../../ui/toast.ts";
import { person } from "../../ui/who.ts";
import { barred, type Compose } from "./compose.ts";
import { Composer, Gate } from "./Composer.tsx";
import { Details } from "./Details.tsx";
import { FilesPane } from "./files/Pane.tsx";
import { useShown } from "./files/state.ts";
import { mcpOf } from "./servers.ts";
import { SessionHead } from "./SessionHead.tsx";
import { type Beside, type Pane, PaneButtons, Split } from "./Split.tsx";
import { TurnView } from "./TurnView.tsx";

// How near its end the conversation has to be scrolled towards it to stay
// there as it grows, in pixels.
const NEAR = 96;

// How far from its end the conversation still counts as at it, in pixels.
const AT = 2;

const NONE: Turn[] = [];

// Conversation is a session: its head, its turns from the oldest that was
// read to the newest, and the box for the next message, with the files of
// the session or its details beside them if they are shown. It opens at the
// newest turn, and follows the turn that has not ended.
export function Conversation({
  context,
  agent,
  me,
  id,
  compose,
  pane,
  onPane,
  lead,
}: {
  context: ProjectContext;
  agent: Tinted<Agent>;
  me: Me;
  id: string;
  compose: Compose;
  // The pane that shows beside the conversation, and what shows another or
  // none.
  pane: Pane;
  onPane: (pane: Pane) => void;
  // What the head of the conversation begins with.
  lead?: ReactNode;
}) {
  const session = useSession(id);
  const turns = useTurns(id);
  const files = useShown(id);
  const scroller = useRef<HTMLDivElement>(null);
  // How tall the turns were before older ones were added above them.
  const before = useRef<number | null>(null);
  // Whether the view stays at the end of the conversation as it grows.
  const stick = useRef(true);
  // Where the view was when it was last looked at.
  const top = useRef(0);
  const [away, setAway] = useState(false);
  const [loading, setLoading] = useState(false);
  const list = turns.data ?? NONE;
  const newest = list.at(-1);
  const who = useCallback(
    (user?: string) => person(me, context.members, user),
    [me, context.members],
  );

  const unfinished = newest && !newest.ended_at ? newest.id : undefined;
  // A caller who may rename the session takes the title that its harness
  // suggests.
  const renames =
    !!session.data && sessionAccess(context, me, session.data).control;
  useEffect(() => {
    if (!unfinished) return;
    const retitle = (title: unknown) =>
      void suggestTitle(id, title).catch(() => {});
    return followTurn(id, unfinished, oops, renames ? retitle : undefined);
  }, [id, unfinished, renames]);

  // What the session says of its newest turn shows, and a turn that began
  // elsewhere joins the turns that are shown.
  const last = session.data?.last_turn;
  useEffect(() => {
    if (last) noteTurn(id, last).catch(oops);
  }, [id, last]);
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
    } else {
      // A view that was taken back since it was last looked at has left the
      // end, though its scroll event has not come yet.
      const left = node.scrollHeight - node.scrollTop - node.clientHeight;
      if (node.scrollTop < top.current && left >= AT) stick.current = false;
      if (stick.current) node.scrollTop = node.scrollHeight;
    }
    top.current = node.scrollTop;
  }, [list]);

  const error = session.error ?? turns.error;
  const base = `/p/${context.project.slug}/agents/${agent.slug}`;
  const foreign = session.data && session.data.agent_id !== agent.id;
  const mcp = (of?: Session) =>
    of &&
    mcpOf({ context, agent, me, compose, session: of, running: !!unfinished });
  // What the agent has done in its newest turn, by which the files of the
  // session may have changed.
  const done = (newest?.items ?? []).filter(
    (item) =>
      item.type === "tool_call" &&
      (item.status === "completed" || item.status === "failed"),
  ).length;
  const pulse = newest ? `${newest.id}:${done}:${newest.ended_at ?? ""}` : "";
  // The files take more room once one of them is open.
  const beside: Beside | undefined =
    pane === "files"
      ? files.tabs.length
        ? "files"
        : "tree"
      : (pane ?? undefined);
  const close = () => onPane(null);
  const side = (of?: Session) =>
    pane === "details" ? (
      <Details
        context={context}
        agent={agent}
        me={me}
        session={of}
        turns={list}
        mcp={mcp(of)}
        empty={!of}
        onClose={close}
      />
    ) : pane === "files" && of ? (
      <FilesPane
        context={context}
        me={me}
        session={of}
        pulse={pulse}
        onClose={close}
      />
    ) : (
      pane && <aside className="pane files" />
    );
  if (error || foreign) {
    const missing =
      foreign || (error instanceof ApiError && error.status === 404);
    return (
      <Split beside={beside} side={side()}>
        <main className="conv">
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
        </main>
      </Split>
    );
  }
  if (!session.data || !turns.data) {
    return (
      <Split beside={beside} side={side()}>
        <main className="conv">
          <div className="welcome">
            <div style={{ width: "420px" }}>
              <Loading lines={4} />
            </div>
          </div>
        </main>
      </Split>
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

  // Going back leaves the end, however little, so that what is written
  // meanwhile does not pull the view to the end again. Going towards the
  // end stays there once it is near.
  function scrolled() {
    const node = scroller.current!;
    const left = node.scrollHeight - node.scrollTop - node.clientHeight;
    if (left < AT) stick.current = true;
    else if (node.scrollTop < top.current) stick.current = false;
    else if (node.scrollTop > top.current && left < NEAR) stick.current = true;
    top.current = node.scrollTop;
    setAway(!stick.current);
  }

  function toEnd() {
    const node = scroller.current!;
    stick.current = true;
    node.scrollTop = node.scrollHeight;
    setAway(false);
  }

  const live = unfinished ? newest : undefined;
  // What stops the turn that has not ended and answers its requests, for
  // a caller who may.
  const may = live && controls(context, me, live);
  const stop = may ? () => void stopTurn(id, live.id).catch(oops) : undefined;
  const answer = may
    ? async (request: string, option: string) => {
        try {
          if (!(await answerRequest(live.id, request, option))) {
            toast("Someone has already answered this request.");
          }
          return true;
        } catch (failure) {
          oops(failure);
          return false;
        }
      }
    : undefined;
  const why = barred(context, me, session.data);

  return (
    <Split beside={beside} side={side(session.data)}>
      <main className="conv">
        <SessionHead
          context={context}
          me={me}
          session={session.data}
          newest={newest}
          lead={lead}
          actions={<PaneButtons pane={pane} files onPane={onPane} />}
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
                servers={context.mcpServers}
                person={who}
                kept={!session.data.ended_at}
                awaits={turn.id === compose.woken ? "wake" : undefined}
                onStop={turn.id === unfinished ? stop : undefined}
                onAnswer={turn.id === unfinished ? answer : undefined}
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
            context={context}
            agent={agent}
            compose={compose}
            mcp={mcp(session.data)!}
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
      </main>
    </Split>
  );
}
