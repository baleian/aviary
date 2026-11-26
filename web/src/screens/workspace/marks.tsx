import type { Session } from "../../api/types.ts";
import type { Standing } from "../../lib/sessions.ts";
import { working } from "../../lib/sessions.ts";
import { Icon } from "../../ui/Icon.tsx";
import type { IconName } from "../../ui/icons.ts";

const ICONS: Partial<Record<Standing, [tone: string, glyph: IconName]>> = {
  queued: ["c-dusk", "clock"],
  completed: ["c-ok", "ok"],
  failed: ["c-fail", "fail"],
  expired: ["c-grey", "hour"],
  ended: ["c-grey", "lock"],
  none: ["c-grey", "chat"],
};

// StateIcon is the small mark of how a session or a turn stands, for lists.
export function StateIcon({ stands }: { stands: Standing }) {
  if (working(stands)) return <span className="ring" />;
  const [tone, glyph] = ICONS[stands] ?? ["c-grey", "cancel"];
  return (
    <span className={tone}>
      <Icon name={glyph} className="lg" />
    </span>
  );
}

// SessionTitle is the title of a session, or that it has none.
export function SessionTitle({
  session,
  tag: Tag = "b",
}: {
  session: Session;
  tag?: "b" | "span";
}) {
  return session.title ? (
    <Tag>{session.title}</Tag>
  ) : (
    <Tag className="untitled">Untitled session</Tag>
  );
}

// What a session says of its sandbox, each with its word and its glyph.
const SANDBOXES: Record<
  NonNullable<Session["sandbox"]>,
  readonly [word: string, glyph?: IconName]
> = {
  awake: ["Awake"],
  asleep: ["Asleep", "moon"],
  lost: ["Lost", "warn"],
};

// SandboxBadge says whether the sandbox of a session is awake, asleep or
// lost. With always, it also says that there is none: not yet,
// or no longer.
export function SandboxBadge({
  session,
  always,
}: {
  session: Session;
  always?: boolean;
}) {
  if (session.ended_at) {
    return always ? <span className="sb gone">Deleted</span> : null;
  }
  if (!session.sandbox) {
    return always ? <span className="sb gone">Not started</span> : null;
  }
  // The API may say what this build does not know.
  const [word, glyph] = SANDBOXES[session.sandbox] ?? [session.sandbox];
  return (
    <span
      className={"sb " + session.sandbox}
      title={"Sandbox " + word.toLowerCase()}
    >
      {glyph && <Icon name={glyph} />}
      {word}
    </span>
  );
}

// SandboxMark is the badge of the sandbox without its word, for a list.
export function SandboxMark({ session }: { session: Session }) {
  const mark = session.sandbox && SANDBOXES[session.sandbox];
  if (session.ended_at || !mark) return null;
  const [word, glyph] = mark;
  const name = "Sandbox " + word.toLowerCase();
  return (
    <span
      className={"sbm " + session.sandbox}
      title={name}
      role="img"
      aria-label={name}
    >
      {glyph && <Icon name={glyph} />}
    </span>
  );
}
