import {
  type KeyboardEvent,
  type ReactNode,
  useLayoutEffect,
  useRef,
} from "react";
import { Link } from "react-router";
import { can, type ProjectContext } from "../../api/project.ts";
import type { Agent, Me, Session, Turn } from "../../api/types.ts";
import { label } from "../../lib/format.ts";
import { followed } from "../../lib/sessions.ts";
import { Icon, Use } from "../../ui/Icon.tsx";
import type { IconName } from "../../ui/icons.ts";
import { person } from "../../ui/who.ts";
import type { Barred, Compose } from "./compose.ts";
import { McpButton } from "./Mcp.tsx";
import type { McpChoice } from "./servers.ts";

// What the bar of the composer says while a turn has not ended.
const DOING: Record<string, string> = {
  queued: "Queued",
  starting: "Starting…",
  waiting: "Waiting for approval",
  stopping: "Stopping…",
};

// Composer is the box in which a message is written and sent. While a turn
// of the session has not ended it says what the turn is doing, and a caller
// who may can stop it. The MCP servers of the session are turned on and off
// from it, if the project has any.
export function Composer({
  context,
  agent,
  compose,
  mcp,
  turn,
  onSend,
  onStop,
}: {
  context: ProjectContext;
  agent: Agent;
  compose: Compose;
  mcp: McpChoice;
  // The turn of the session that has not ended, if there is one.
  turn?: Turn;
  onSend: () => void;
  // Stops the turn, for a caller who may.
  onStop?: () => void;
}) {
  const box = useRef<HTMLTextAreaElement | null>(null);
  const { text, sending, cooldown, attach } = compose;

  // The box is as tall as what is written in it, up to its limit.
  useLayoutEffect(() => {
    const node = box.current;
    if (!node) return;
    node.style.height = "auto";
    node.style.height = Math.min(node.scrollHeight, 192) + "px";
  }, [text]);

  const blocked = !text.trim() || sending || cooldown > 0 || !!turn;
  function keyed(event: KeyboardEvent<HTMLTextAreaElement>) {
    // Enter sends, except while an input method composes a character.
    const composing = event.nativeEvent.isComposing || event.keyCode === 229;
    if (event.key !== "Enter" || event.shiftKey || composing) return;
    event.preventDefault();
    if (!blocked) onSend();
  }

  const stands = turn && followed(turn);
  const sendButton = (
    <button
      className="send"
      type="button"
      title="Send"
      disabled={blocked}
      onClick={onSend}
    >
      {sending ? (
        <span className="ring" />
      ) : (
        <Use symbol="wing" className="i lg f" viewBox="0 0 36 18" />
      )}
    </button>
  );
  return (
    <div className="composer">
      <textarea
        ref={(node) => {
          box.current = node;
          attach(node);
        }}
        rows={1}
        aria-label="Message"
        placeholder={`Message ${label(agent)}…`}
        value={text}
        autoFocus
        onChange={(event) => compose.setText(event.target.value)}
        onKeyDown={keyed}
      />
      <div className="bar">
        {context.mcpServers.length > 0 && (
          <McpButton context={context} choice={mcp} />
        )}
        {cooldown > 0 ? (
          <>
            <Icon name="warn" />
            <span className="msg bad">
              The queue is full. Try again in {cooldown} s.
            </span>
          </>
        ) : stands ? (
          <>
            {stands === "waiting" ? (
              <Icon name="shield" />
            ) : (
              <span className="ring" />
            )}
            <span className="msg">{DOING[stands] ?? "Working…"}</span>
          </>
        ) : (
          <span className="msg">
            Enter to send · Shift + Enter for a new line
          </span>
        )}
        <span className="end">
          {cooldown <= 0 && stands && stands !== "stopping" && onStop && (
            <button className="btn sec sm" type="button" onClick={onStop}>
              <Icon name="stop" />
              {stands === "queued" ? "Cancel turn" : "Stop turn"}
            </button>
          )}
          {sendButton}
        </span>
      </div>
    </div>
  );
}

function Shut({
  glyph,
  children,
  action,
}: {
  glyph: IconName;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="gate">
      <Icon name={glyph} className="lg" />
      <span>
        <b>{children}</b>
      </span>
      {action}
    </div>
  );
}

// Gate stands in the place of the composer when the caller cannot send a
// message, and says why.
export function Gate({
  why,
  context,
  agent,
  me,
  session,
}: {
  why: Barred;
  context: ProjectContext;
  agent: Agent;
  me: Me;
  session?: Session;
}) {
  if (why === "role") {
    return <Shut glyph="eye">Your role cannot run sessions here.</Shut>;
  }
  if (why === "foreign") {
    const owner = person(me, context.members, session?.created_by);
    return (
      <Shut glyph="eye">
        Only {owner.known ? owner.name : "its creator"} can send messages here.
      </Shut>
    );
  }
  if (session?.end_reason === "agent_deleted") {
    return (
      <Shut glyph="lock">
        This session ended because its agent was deleted.
      </Shut>
    );
  }
  const how = session?.end_reason === "expired" ? "expired" : "ended";
  return (
    <Shut
      glyph="lock"
      action={
        can(context, "sessions.run") && (
          <Link
            className="btn sec sm"
            to={`/p/${context.project.slug}/agents/${agent.slug}`}
          >
            <Icon name="plus" />
            New session
          </Link>
        )
      }
    >
      This session has {how}.
    </Shut>
  );
}
