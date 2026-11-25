import {
  type ClipboardEvent,
  type DragEvent,
  type KeyboardEvent,
  type ReactNode,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Link } from "react-router";
import { can, type ProjectContext } from "../../api/project.ts";
import type { Agent, Me, Session, Turn } from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { label, size } from "../../lib/format.ts";
import { followed } from "../../lib/sessions.ts";
import { Icon, Use } from "../../ui/Icon.tsx";
import type { IconName } from "../../ui/icons.ts";
import { person } from "../../ui/who.ts";
import type { Barred, Compose, Pending } from "./compose.ts";
import { FileCard } from "./Files.tsx";
import { McpButton } from "./Mcp.tsx";
import type { McpChoice } from "./servers.ts";
import { chosenFile, type Shown } from "./shown.ts";

// What the bar of the composer says while a turn has not ended.
const DOING: Record<string, string> = {
  queued: "Queued",
  starting: "Starting…",
  waiting: "Waiting for approval",
  stopping: "Stopping…",
};

// holds reports whether what is dragged is files.
const holds = (event: DragEvent) => event.dataTransfer.types.includes("Files");

// Chosen shows a file that the message carries, and how far it has gone
// while the message is sent. It is taken from the message with its button.
function Chosen({
  pending,
  shown,
  sending,
  onRemove,
}: {
  pending: Pending;
  shown: Shown;
  sending: boolean;
  onRemove: () => void;
}) {
  const { file, sent, id } = pending;
  const going = sent !== undefined;
  const gone = file.size ? Math.floor(((sent ?? 0) / file.size) * 100) : 100;
  return (
    <FileCard
      file={shown}
      gone={going ? gone : undefined}
      note={
        going ? (
          gone + "%"
        ) : id ? (
          <>
            <Icon name="check" />
            {size(file.size)}
          </>
        ) : undefined
      }
    >
      <button
        className="ib sm"
        type="button"
        title="Remove"
        aria-label={"Remove " + file.name}
        disabled={sending}
        onClick={onRemove}
      >
        <Icon name="x" />
      </button>
    </FileCard>
  );
}

// Composer is the box in which a message is written and sent, with the files
// that it carries: those that are chosen, dropped on it or pasted into it.
// While a turn of the session has not ended it says what the turn is doing,
// and a caller who may can stop it. The MCP servers of the session are
// turned on and off from it, if the project has any.
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
  const picker = useRef<HTMLInputElement | null>(null);
  // Whether files are dragged over the composer, and over how many of its
  // elements.
  const [over, setOver] = useState(false);
  const inside = useRef(0);
  const { text, files, sending, cooldown, attach } = compose;
  // Whether a message may carry files.
  const takes = compose.carries > 0;
  // The files as they are shown.
  const shown = useMemo(
    () => files.map(({ key, file }) => chosenFile(key, file)),
    [files],
  );

  // The box is as tall as what is written in it, up to its limit.
  useLayoutEffect(() => {
    const node = box.current;
    if (!node) return;
    node.style.height = "auto";
    node.style.height = Math.min(node.scrollHeight, 192) + "px";
  }, [text]);

  const empty = !text.trim() && !files.length;
  const blocked = empty || sending || cooldown > 0 || !!turn;
  function keyed(event: KeyboardEvent<HTMLTextAreaElement>) {
    // Enter sends, except while an input method composes a character.
    const composing = event.nativeEvent.isComposing || event.keyCode === 229;
    if (event.key !== "Enter" || event.shiftKey || composing) return;
    event.preventDefault();
    if (!blocked) onSend();
  }

  // A drag enters and leaves each element of the composer that it goes
  // over, so the composer counts how far in it is.
  function entered(event: DragEvent<HTMLDivElement>) {
    if (!takes || !holds(event)) return;
    inside.current++;
    setOver(true);
  }
  function left() {
    if (inside.current > 0 && --inside.current === 0) setOver(false);
  }
  function dragged(event: DragEvent<HTMLDivElement>) {
    // Taking the drag is what lets the files be dropped: the browser would
    // open them otherwise.
    if (takes && holds(event)) event.preventDefault();
  }
  function dropped(event: DragEvent<HTMLDivElement>) {
    if (!takes || !holds(event)) return;
    event.preventDefault();
    inside.current = 0;
    setOver(false);
    if (!sending) compose.add([...event.dataTransfer.files]);
  }
  function pasted(event: ClipboardEvent<HTMLTextAreaElement>) {
    const copied = [...event.clipboardData.files];
    if (!takes || !copied.length) return;
    // A file is pasted as a file, and not as its name.
    event.preventDefault();
    if (!sending) compose.add(copied);
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
    <div
      className={cls("composer", over && "over")}
      onDragEnter={entered}
      onDragLeave={left}
      onDragOver={dragged}
      onDrop={dropped}
    >
      {files.length > 0 && (
        <ul className="files" aria-label="Files">
          {files.map((pending, at) => (
            <li key={pending.key}>
              <Chosen
                pending={pending}
                shown={shown[at]}
                sending={sending}
                onRemove={() => compose.remove(pending.key)}
              />
            </li>
          ))}
        </ul>
      )}
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
        // What is on its way is not written on.
        readOnly={sending}
        onChange={(event) => compose.setText(event.target.value)}
        onKeyDown={keyed}
        onPaste={pasted}
      />
      <div className="bar">
        {takes && (
          <>
            <button
              className="ib sm"
              type="button"
              title="Attach files"
              aria-label="Attach files"
              disabled={sending}
              onClick={() => picker.current?.click()}
            >
              <Icon name="clip" />
            </button>
            <input
              ref={picker}
              type="file"
              multiple
              hidden
              onChange={(event) => {
                compose.add([...(event.target.files ?? [])]);
                // The same file can be chosen again.
                event.target.value = "";
                box.current?.focus();
              }}
            />
          </>
        )}
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
