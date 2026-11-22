import { type ReactNode, useRef, useState } from "react";
import { useLocation } from "react-router";
import { ApiError } from "../../api/client.ts";
import { endSession, renameSession } from "../../api/conversation.ts";
import { type ProjectContext, sessionAccess } from "../../api/project.ts";
import type { Me, Session, Turn } from "../../api/types.ts";
import { Wrong } from "../../lib/form.ts";
import { plural } from "../../lib/format.ts";
import { followed, STATUS_NAMES } from "../../lib/sessions.ts";
import { Avatar } from "../../ui/Avatar.tsx";
import { Chip } from "../../ui/Chip.tsx";
import { copy } from "../../ui/clipboard.ts";
import { Dialog } from "../../ui/Dialog.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Menu, MenuItem } from "../../ui/Menu.tsx";
import { Time } from "../../ui/Time.tsx";
import { oops, toast } from "../../ui/toast.ts";
import { person } from "../../ui/who.ts";
import { SandboxBadge, SessionTitle } from "./marks.tsx";

// Renaming is the title of a session while it is written anew. Enter and
// leaving the field keep what was written, and Escape does not.
function Renaming({
  session,
  onDone,
}: {
  session: Session;
  onDone: () => void;
}) {
  // Enter is followed by the field losing the focus: it ends once.
  const ended = useRef(false);
  async function end(title: string | null) {
    if (ended.current) return;
    ended.current = true;
    if (title != null && title.trim() !== session.title) {
      try {
        await renameSession(session.id, title.trim());
      } catch (error) {
        oops(error);
      }
    }
    onDone();
  }
  return (
    <input
      className="inp ttl"
      defaultValue={session.title}
      placeholder="Untitled session"
      maxLength={200}
      aria-label="Title of the session"
      autoFocus
      onFocus={(event) => event.target.select()}
      onKeyDown={(event) => {
        // An input method uses Enter and Escape to compose a character.
        if (event.nativeEvent.isComposing || event.keyCode === 229) return;
        if (event.key === "Enter") void end(event.currentTarget.value);
        if (event.key === "Escape") void end(null);
      }}
      onBlur={(event) => void end(event.target.value)}
    />
  );
}

// EndSession asks whether to end a session, and does.
function EndSession({
  session,
  onClose,
}: {
  session: Session;
  onClose: () => void;
}) {
  return (
    <Dialog
      size="slim"
      danger
      title="End this session?"
      lead="The files that the agent made in it are deleted. The conversation stays."
      action="End session"
      onSubmit={async () => {
        try {
          await endSession(session.id);
        } catch (error) {
          if (error instanceof ApiError && error.code === "session_busy") {
            throw new Wrong("Stop the running turn first.");
          }
          throw error;
        }
        return true;
      }}
      onClose={(ended) => {
        onClose();
        if (ended) toast("The session has ended", "ok");
      }}
    />
  );
}

// SessionHead is the head of a conversation: the title of the session, how
// it stands and when it began, and what can be done with it. A caller who
// may renames the session by its title, and ends it from the menu. actions
// are the buttons before the menu.
export function SessionHead({
  context,
  me,
  session,
  newest,
  actions,
}: {
  context: ProjectContext;
  me: Me;
  session: Session;
  // The newest turn of the session, if it has one.
  newest?: Turn;
  actions: ReactNode;
}) {
  const { pathname } = useLocation();
  const [renaming, setRenaming] = useState(false);
  const [ending, setEnding] = useState(false);
  const { control } = sessionAccess(context, me, session);
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
          {renaming ? (
            <Renaming session={session} onDone={() => setRenaming(false)} />
          ) : control ? (
            <button
              className="ttl"
              type="button"
              title="Rename the session"
              onClick={() => setRenaming(true)}
            >
              <SessionTitle session={session} tag="span" />
              <Icon name="edit" />
            </button>
          ) : (
            <SessionTitle session={session} tag="span" />
          )}
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
      {actions}
      <Menu
        align="right"
        button={(props) => (
          <button className="ib" type="button" title="More" {...props}>
            <Icon name="dots" className="lg" />
          </button>
        )}
      >
        {control && (
          <MenuItem
            glyph="edit"
            text="Rename"
            onClick={() => setRenaming(true)}
          />
        )}
        <MenuItem
          glyph="link"
          text="Copy the link"
          onClick={() => void copy(location.origin + pathname, "link")}
        />
        <MenuItem
          glyph="copy"
          text="Copy the session ID"
          onClick={() => void copy(session.id, "session ID")}
        />
        {control && (
          <>
            <hr />
            <MenuItem
              glyph="trash"
              text="End the session…"
              danger
              disabled={!!session.ended_at}
              onClick={() => setEnding(true)}
            />
          </>
        )}
      </Menu>
      {ending && (
        <EndSession session={session} onClose={() => setEnding(false)} />
      )}
    </div>
  );
}
