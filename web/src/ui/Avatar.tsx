import type { Tinted } from "../api/queries.ts";
import type { Project } from "../api/types.ts";
import { cls } from "../lib/cls.ts";
import { initials, label, pick } from "../lib/format.ts";
import { Icon } from "./Icon.tsx";
import type { Who } from "./who.ts";

export function Avatar({
  who,
  size,
}: {
  who: Who;
  size?: "xs" | "sm" | "lg" | "xl";
}) {
  if (!who.known) {
    return (
      <span className={cls("av anon", size)} title={who.title || who.name}>
        <Icon name="user" />
      </span>
    );
  }
  const colour = ["", "a2", "a3", "a4"][pick(who.key || who.name, 4)];
  return (
    <span className={cls("av", colour, size)} title={who.name}>
      {initials(who.name)}
    </span>
  );
}

export function ProjectTile({
  project,
  size,
}: {
  project: Tinted<Project>;
  size?: "lg";
}) {
  const colour = ["", "p2", "p3", "p4"][project.tint % 4];
  return (
    <span className={cls("pt", colour, size)} aria-hidden="true">
      {label(project)[0]}
    </span>
  );
}

export function AdminBadge({ text = "Admin" }: { text?: string }) {
  return (
    <span className="adm">
      <Icon name="shield" />
      {text}
    </span>
  );
}
