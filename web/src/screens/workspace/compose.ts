import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { ApiError } from "../../api/client.ts";
import {
  rereadSession,
  sendMessage,
  startSession,
  uploadAttachment,
  useLimits,
} from "../../api/conversation.ts";
import {
  can,
  keys,
  type ProjectContext,
  sessionAccess,
} from "../../api/project.ts";
import type { Agent, Me, Session } from "../../api/types.ts";
import { label, plural, sentence, size } from "../../lib/format.ts";
import { suggest } from "../../lib/sessions.ts";
import { queryClient } from "../../query.ts";
import { oops, toast } from "../../ui/toast.ts";

// A file that the message being written carries. It is uploaded when the
// message is sent.
export interface Pending {
  // Tells the files of the message apart.
  key: number;
  file: File;
  // The bytes of the file that have gone, while it is uploaded.
  sent?: number;
  // The ID of its attachment, once the file is uploaded: a message that is
  // sent again does not upload it again.
  id?: string;
}

// The message that is being written in a workspace, and how to send it.
export interface Compose {
  text: string;
  setText: (text: string) => void;
  // fill puts a text in the box of the message, and the caret in the box.
  fill: (text: string) => void;
  // attach tells which box the message is written in.
  attach: (box: HTMLTextAreaElement | null) => void;
  // The files that the message carries, in its order.
  files: Pending[];
  // How many files a message may carry: 0 if the installation takes none,
  // and until it has said how many.
  carries: number;
  // add adds files to the message, as far as a message may carry them, and
  // says which it leaves out.
  add: (files: File[]) => void;
  // remove takes a file from the message.
  remove: (key: number) => void;
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
  // The MCP servers that a new session is to begin with, if they are not
  // those of its agent.
  servers?: string[];
  setServers: (servers: string[] | undefined) => void;
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

const NONE: Pending[] = [];

// The files that were added so far, which numbers them.
let added = 0;

// A message that is being written, and the session that it is written to.
interface Draft {
  sid?: string;
  text: string;
  files: Pending[];
}

// useCompose keeps the message that is being written to the session with
// the ID, or to a new session of the agent if there is none. A message that
// was begun in one session is not carried to another.
export function useCompose(
  context: ProjectContext,
  agent: Agent,
  sid: string | undefined,
): Compose {
  const navigate = useNavigate();
  const limits = useLimits().data;
  const [draft, setDraft] = useState<Draft>({ text: "", files: NONE });
  const [sending, setSending] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const [woken, setWoken] = useState<string>();
  // The choice is the agent's that it was made for.
  const [chosen, setChosen] = useState<{ agent: string; servers: string[] }>();
  const servers = chosen?.agent === agent.id ? chosen.servers : undefined;
  const setServers = useCallback(
    (list: string[] | undefined) =>
      setChosen(list && { agent: agent.id, servers: list }),
    [agent.id],
  );
  const box = useRef<HTMLTextAreaElement | null>(null);
  const attach = useCallback((node: HTMLTextAreaElement | null) => {
    box.current = node;
  }, []);
  const text = draft.sid === sid ? draft.text : "";
  const files = draft.sid === sid ? draft.files : NONE;
  // change changes the message of the session, which is a new one if what
  // was being written is another session's.
  const change = useCallback(
    (session: string | undefined, next: (now: Draft) => Partial<Draft>) =>
      setDraft((now) => {
        const mine =
          now.sid === session ? now : { sid: session, text: "", files: NONE };
        return { ...mine, ...next(mine) };
      }),
    [],
  );
  const setText = useCallback(
    (next: string) => change(sid, () => ({ text: next })),
    [change, sid],
  );
  const fill = useCallback(
    (next: string) => {
      setText(next);
      box.current?.focus();
    },
    [setText],
  );

  const carries = limits?.max_attachments ?? 0;
  const largest = limits?.max_attachment_bytes ?? 0;
  const add = useCallback(
    (list: File[]) => {
      if (!carries || !list.length) return;
      const large = list.filter((file) => file.size > largest);
      const fit = list.filter((file) => file.size <= largest);
      const taken = fit.slice(0, Math.max(0, carries - files.length));
      if (large.length) {
        const which =
          large.length === 1 ? large[0].name : plural(large.length, "file");
        const verb = large.length === 1 ? "is" : "are";
        toast(`${which} ${verb} larger than ${size(largest)}.`, "bad", 5200);
      }
      if (taken.length < fit.length) {
        toast(
          `A message carries up to ${plural(carries, "file")}.`,
          "bad",
          5200,
        );
      }
      if (!taken.length) return;
      const more = taken.map((file) => ({ key: ++added, file }));
      change(sid, (now) => ({
        files: [...now.files, ...more].slice(0, carries),
      }));
    },
    [carries, change, files.length, largest, sid],
  );
  const remove = useCallback(
    (key: number) =>
      change(sid, (now) => ({
        files: now.files.filter((file) => file.key !== key),
      })),
    [change, sid],
  );

  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = setTimeout(() => setCooldown(cooldown - 1), 1000);
    return () => clearTimeout(timer);
  }, [cooldown]);

  async function send() {
    const message = text.trim();
    const carried = files;
    setSending(true);
    const project = `/p/${context.project.slug}`;
    let session = sid;
    // told changes what the message says of one of its files, unless another
    // session is written to by now.
    const told = (key: number, more: Partial<Pending>) =>
      setDraft((now) =>
        now.sid !== session
          ? now
          : {
              ...now,
              files: now.files.map((file) =>
                file.key === key ? { ...file, ...more } : file,
              ),
            },
      );
    try {
      if (!session) {
        // The session is created with its first message, and named after
        // it, or after its first file if it has no text. The message stays
        // in the box until it has been accepted.
        const title = suggest(message || carried[0].file.name);
        session = (await startSession(agent.id, title, servers)).id;
        setServers(undefined);
        setDraft({ sid: session, text: message, files: carried });
        await navigate(`${project}/agents/${agent.slug}/s/${session}`, {
          replace: true,
        });
      }
      const read = queryClient.getQueryData<Session>(["session", session]);
      // The files go first, one after another. One that has gone keeps the
      // ID of its attachment, and does not go again with a message that is
      // sent again.
      const attachments: string[] = [];
      for (const { key, file, id } of carried) {
        if (id) {
          attachments.push(id);
          continue;
        }
        told(key, { sent: 0 });
        try {
          const uploaded = await uploadAttachment(session, file, (sent) =>
            told(key, { sent }),
          );
          told(key, { id: uploaded.id });
          attachments.push(uploaded.id);
        } finally {
          told(key, { sent: undefined });
        }
      }
      const turn = await sendMessage(session, message, attachments);
      if (read?.sandbox === "asleep") setWoken(turn.id);
      // What was begun in another session meanwhile stays.
      const sent = session;
      setDraft((now) =>
        now.sid === sent ? { sid: sent, text: "", files: NONE } : now,
      );
    } catch (error) {
      const code = error instanceof ApiError ? error.code : "";
      if (error instanceof ApiError && code === "queue_full") {
        setCooldown(error.retryAfter || COOLDOWN);
      } else if (code === "unknown_attachment") {
        // The API has the files that had gone no longer: they go again.
        oops(error);
        for (const { key } of carried) told(key, { id: undefined });
      } else if (
        code === "attachments_disabled" ||
        code === "attachment_too_large" ||
        code === "too_many_attachments"
      ) {
        // The installation does not take files as the console has it.
        oops(error);
        await queryClient.invalidateQueries({ queryKey: ["limits"] });
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

  return {
    text,
    setText,
    fill,
    attach,
    files,
    carries,
    add,
    remove,
    sending,
    cooldown,
    woken,
    send,
    servers,
    setServers,
  };
}
