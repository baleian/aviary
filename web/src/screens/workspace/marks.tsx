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

// SandboxBadge says whether the sandbox of a session is awake, asleep or
// lost. With always, it also says that there is none: not yet, or no longer.
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
  if (session.sandbox === "awake") {
    return (
      <span className="sb awake" title="Sandbox awake">
        Awake
      </span>
    );
  }
  if (session.sandbox === "asleep") {
    return (
      <span className="sb asleep" title="Sandbox asleep">
        <Icon name="moon" />
        Asleep
      </span>
    );
  }
  if (session.sandbox === "lost") {
    return (
      <span className="sb lost" title="Sandbox lost">
        <Icon name="warn" />
        Lost
      </span>
    );
  }
  return always ? <span className="sb gone">Not started</span> : null;
}

const MARKS = {
  awake: ["Sandbox awake", undefined],
  asleep: ["Sandbox asleep", "moon"],
  lost: ["Sandbox lost", "warn"],
} as const;

// SandboxMark is the badge of the sandbox without its word, for a list.
export function SandboxMark({ session }: { session: Session }) {
  if (session.ended_at || !session.sandbox) return null;
  const [name, glyph] = MARKS[session.sandbox];
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
