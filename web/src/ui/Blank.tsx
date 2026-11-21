import type { ReactNode } from "react";
import { cls } from "../lib/cls.ts";
import { Icon } from "./Icon.tsx";
import type { IconName } from "./icons.ts";

// Blank is a page that says one thing. A full one has no bar above it.
export function Blank({
  glyph,
  tone,
  title,
  text,
  actions,
  full,
  children,
}: {
  glyph: IconName;
  tone?: "y";
  title: string;
  text?: ReactNode;
  actions?: ReactNode;
  full?: boolean;
  children?: ReactNode;
}) {
  return (
    <section className={cls("blank", full && "full")}>
      <div className="box">
        <span className={cls("ic", tone)}>
          <Icon name={glyph} className="xl" />
        </span>
        <h1>{title}</h1>
        {text && <p>{text}</p>}
        {children}
        {actions && <div className="acts">{actions}</div>}
      </div>
    </section>
  );
}

// Empty stands where a list has nothing to show.
export function Empty({
  glyph,
  title,
  text,
  action,
}: {
  glyph: IconName;
  title: string;
  text?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="empty">
      <span className="ic">
        <Icon name={glyph} className="xl" />
      </span>
      <b>{title}</b>
      {text && <p>{text}</p>}
      {action}
    </div>
  );
}

// Loading stands where something is being read.
export function Loading({ lines = 3 }: { lines?: number }) {
  return (
    <div
      style={{ display: "grid", gap: "12px", padding: "8px 0" }}
      aria-busy="true"
    >
      {Array.from({ length: lines }, (_, index) => (
        <span
          key={index}
          className="skel"
          style={{ height: "18px", width: `${86 - index * 17}%` }}
        />
      ))}
    </div>
  );
}
