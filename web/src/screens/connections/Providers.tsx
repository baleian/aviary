import { useState } from "react";
import { can } from "../../api/project.ts";
import type { Provider } from "../../api/types.ts";
import { label, plural } from "../../lib/format.ts";
import { useTitle } from "../../lib/title.ts";
import { useProject } from "../../shell/project.ts";
import { Empty } from "../../ui/Blank.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Time } from "../../ui/Time.tsx";
import { KeyPill } from "../Overview.tsx";
import { EditProvider } from "./EditProvider.tsx";
import { Find, Head, None, Users } from "./parts.tsx";

// How many there are before the list offers to search them.
const SEARCHED = 4;

// Providers is the screen of the model providers of a project: the endpoints
// whose models its agents use.
export function Providers() {
  const context = useProject();
  const { project, providers, agents } = context;
  const manages = can(context, "connections.manage");
  const [editing, setEditing] = useState<Provider | "new" | null>(null);
  const [sought, setSought] = useState("");
  useTitle("Model providers", label(project));
  const words = sought.trim().toLowerCase();
  const shown = providers.filter((provider) =>
    `${provider.name} ${provider.url}`.toLowerCase().includes(words),
  );
  return (
    <section className="page">
      <div className="wrap">
        <Head
          over={label(project)}
          title="Model providers"
          action={
            manages && (
              <button
                className="btn pri"
                type="button"
                onClick={() => setEditing("new")}
              >
                <Icon name="plus" />
                Add provider
              </button>
            )
          }
        />
        <div className="card">
          {providers.length > SEARCHED && (
            <div className="tb-h">
              <Find
                value={sought}
                onChange={setSought}
                placeholder="Find a provider"
              />
              <span className="end sub">
                {plural(providers.length, "provider")}
              </span>
            </div>
          )}
          {providers.length ? (
            <ul className="clist">
              {shown.map((provider) => (
                <li key={provider.id}>
                  <span className="ic">
                    <Icon name="spark" className="lg" />
                  </span>
                  <div className="tx">
                    <b>{provider.name}</b>
                    <code>{provider.url}</code>
                  </div>
                  <Users
                    agents={agents.filter(
                      (agent) => agent.model.provider === provider.id,
                    )}
                  />
                  <div className="meta">
                    <KeyPill context={context} provider={provider} />
                    <small>
                      Updated <Time value={provider.updated_at} />
                    </small>
                  </div>
                  {manages && (
                    <button
                      className="btn sec sm"
                      type="button"
                      onClick={() => setEditing(provider)}
                    >
                      Edit
                    </button>
                  )}
                </li>
              ))}
              {!shown.length && <None sought={sought} what="provider" />}
            </ul>
          ) : (
            <Empty glyph="spark" title="No model providers yet" />
          )}
        </div>
      </div>
      {editing && (
        <EditProvider
          context={context}
          existing={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
    </section>
  );
}
