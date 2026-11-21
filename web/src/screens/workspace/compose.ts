import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { ApiError } from "../../api/client.ts";
import {
  rereadSession,
  sendMessage,
  startSession,
} from "../../api/conversation.ts";
import {
  can,
  keys,
  type ProjectContext,
  sessionAccess,
} from "../../api/project.ts";
import type { Agent, Me, Session } from "../../api/types.ts";
import { label, sentence } from "../../lib/format.ts";
import { suggest } from "../../lib/sessions.ts";
import { queryClient } from "../../query.ts";
import { oops, toast } from "../../ui/toast.ts";

// The message that is being written in a workspace, and how to send it.
export interface Compose {
  text: string;
  setText: (text: string) => void;
  // fill puts a text in the box of the message, and the caret in the box.
  fill: (text: string) => void;
  // attach tells which box the message is written in.
  attach: (box: HTMLTextAreaElement | null) => void;
  // Whether a message is on its way.
  sending: boolean;
  // The seconds until a message can be sent again, after the API said that
  // its queue is full.
  cooldown: number;
  // The turn that found the sandbox of its session asleep, and waits for
  // it to wake.
  woken?: string;
  // send sends the message. The composer says when there is one to send.
  send: () => Promise<void>;
}

// Why a caller cannot send a message: the caller's role does not run
// sessions, the session has ended, or the session is someone else's.
export type Barred = "role" | "ended" | "foreign";

// barred says why the caller cannot send a message to the session, or to a
// new session if there is none. It returns undefined if the caller can.
export function barred(
  context: ProjectContext,
  me: Me,
  session?: Session,
): Barred | undefined {
  if (!session) return can(context, "sessions.run") ? undefined : "role";
  if (session.ended_at) return "ended";
  const access = sessionAccess(context, me, session);
  if (access.send) return undefined;
  return access.own ? "role" : "foreign";
}

// How long to wait after the API said that its queue is full without
// saying for how long, in seconds.
const COOLDOWN = 5;

// useCompose keeps the message that is being written to the session with
// the ID, or to a new session of the agent if there is none. A message that
// was begun in one session is not carried to another.
export function useCompose(
  context: ProjectContext,
  agent: Agent,
  sid: string | undefined,
): Compose {
  const navigate = useNavigate();
  const [draft, setDraft] = useState<{ sid?: string; text: string }>({
    text: "",
  });
  const [sending, setSending] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const [woken, setWoken] = useState<string>();
  const box = useRef<HTMLTextAreaElement | null>(null);
  const attach = useCallback((node: HTMLTextAreaElement | null) => {
    box.current = node;
  }, []);
  const text = draft.sid === sid ? draft.text : "";
  const setText = useCallback(
    (next: string) => setDraft({ sid, text: next }),
    [sid],
  );
  const fill = useCallback(
    (next: string) => {
      setText(next);
      box.current?.focus();
    },
    [setText],
  );

  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = setTimeout(() => setCooldown(cooldown - 1), 1000);
    return () => clearTimeout(timer);
  }, [cooldown]);

  async function send() {
    const message = text.trim();
    setSending(true);
    const project = `/p/${context.project.slug}`;
    let session = sid;
    try {
      if (!session) {
        // The session is created with its first message, and named after
        // it. The message stays in the box until it has been accepted.
        session = (await startSession(agent.id, suggest(message))).id;
        setDraft({ sid: session, text: message });
        await navigate(`${project}/agents/${agent.slug}/s/${session}`, {
          replace: true,
        });
      }
      const read = queryClient.getQueryData<Session>(["session", session]);
      const turn = await sendMessage(session, message);
      if (read?.sandbox === "asleep") setWoken(turn.id);
      // What was begun in another session meanwhile stays.
      const sent = session;
      setDraft((now) => (now.sid === sent ? { sid: sent, text: "" } : now));
    } catch (error) {
      const code = error instanceof ApiError ? error.code : "";
      if (error instanceof ApiError && code === "queue_full") {
        setCooldown(error.retryAfter || COOLDOWN);
      } else if (code === "session_busy" || code === "session_ended") {
        // The session is not as the console has it.
        oops(error);
        if (session) await rereadSession(session);
      } else if (code === "agent_deleted") {
        toast(sentence(`${label(agent)} was deleted`), "bad");
        await queryClient.invalidateQueries({
          queryKey: keys.agents(context.project.id),
        });
        await navigate(`${project}/agents`);
      } else {
        oops(error);
      }
    } finally {
      setSending(false);
    }
  }

  return { text, setText, fill, attach, sending, cooldown, woken, send };
}
