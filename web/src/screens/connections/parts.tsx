import type { ReactNode } from "react";
import type { Tinted } from "../../api/queries.ts";
import type { Agent } from "../../api/types.ts";
import { label } from "../../lib/format.ts";
import { AgentTile } from "../../ui/Avatar.tsx";
import { Icon } from "../../ui/Icon.tsx";

// Users says which agents use a connection.
export function Users({ agents }: { agents: Tinted<Agent>[] }) {
  if (!agents.length) return <div className="used">Not used yet</div>;
  return (
    <div className="used">
      Used by
      {agents.slice(0, 3).map((agent) => (
        <span key={agent.id} className="usr">
          <AgentTile agent={agent} size="xs" />
          {label(agent)}
        </span>
      ))}
      {agents.length > 3 && `and ${agents.length - 3} more`}
    </div>
  );
}

// Find is the box that narrows a list to what is sought.
export function Find({
  value,
  onChange,
  placeholder,
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
}) {
  return (
    <label className="inp-i find">
      <Icon name="search" />
      <input
        className="inp"
        placeholder={placeholder}
        aria-label={placeholder}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  );
}

// Head is the head of a page of a project: the project, the title, and what
// can be done there.
export function Head({
  over,
  title,
  action,
}: {
  over: string;
  title: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="pg-h">
      <div>
        <p className="over">{over}</p>
        <h1 className="h1">{title}</h1>
      </div>
      {action && <div className="act">{action}</div>}
    </div>
  );
}

// None is the row of a list that nothing that was sought matches.
export function None({ sought, what }: { sought: string; what: string }) {
  return (
    <p className="sub none-found">
      No {what} matches “{sought.trim()}”.
    </p>
  );
}
