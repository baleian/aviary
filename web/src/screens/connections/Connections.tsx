import { useState } from "react";
import { can } from "../../api/project.ts";
import type { Provider, SecretStore } from "../../api/types.ts";
import { label } from "../../lib/format.ts";
import { useTitle } from "../../lib/title.ts";
import { useProject } from "../../shell/project.ts";
import { AgentTile } from "../../ui/Avatar.tsx";
import { Empty } from "../../ui/Blank.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Time } from "../../ui/Time.tsx";
import { KeyPill } from "../Overview.tsx";
import { EditProvider } from "./EditProvider.tsx";
import { EditStore } from "./EditStore.tsx";

// What a dialog of the screen is open for: a new connection of a kind, or
// one to edit.
type Editing =
  { provider: Provider | "new" } | { store: SecretStore | "new" } | null;

// Connections is the screen of what a project connects to: the model
// providers that its agents use, and the secret stores that may hold their
// keys.
export function Connections() {
  const context = useProject();
  const { project, providers, stores, agents } = context;
  const manages = can(context, "connections.manage");
  const [editing, setEditing] = useState<Editing>(null);
  useTitle("Connections", label(project));

  return (
    <section className="page">
      <div className="wrap">
        <div className="pg-h">
          <div>
            <p className="over">{label(project)}</p>
            <h1 className="h1">Connections</h1>
          </div>
        </div>

        <div className="card">
          <div className="tb-h">
            <h2 className="h2">
              Model providers<span className="cnt">{providers.length}</span>
            </h2>
            <div className="end">
              {manages && (
                <button
                  className="btn pri"
                  type="button"
                  onClick={() => setEditing({ provider: "new" })}
                >
                  <Icon name="plus" />
                  Add provider
                </button>
              )}
            </div>
          </div>
          {providers.length ? (
            <ul className="clist">
              {providers.map((provider) => {
                const key = provider.api_key;
                const users = agents.filter(
                  (agent) => agent.model.provider === provider.id,
                );
                return (
                  <li key={provider.id}>
                    <span className="ic">
                      <Icon name="spark" className="lg" />
                    </span>
                    <div className="tx">
                      <b>{provider.name}</b>
                      <code>{provider.url}</code>
                      {provider.headers?.map((header) => (
                        <code key={header.name}>
                          {header.name}: {header.value}
                        </code>
                      ))}
                      {key && "store" in key && (
                        <code>
                          {key.path} → {key.key}
                        </code>
                      )}
                    </div>
                    <div className="used">
                      {users.length ? (
                        <>
                          Used by
                          {users.slice(0, 3).map((agent) => (
                            <span key={agent.id} className="usr">
                              <AgentTile agent={agent} size="xs" />
                              {label(agent)}
                            </span>
                          ))}
                          {users.length > 3 && `and ${users.length - 3} more`}
                        </>
                      ) : (
                        "Not used yet"
                      )}
                    </div>
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
                        onClick={() => setEditing({ provider })}
                      >
                        Edit
                      </button>
                    )}
                  </li>
                );
              })}
            </ul>
          ) : (
            <Empty glyph="spark" title="No model providers yet" />
          )}
        </div>

        <div className="card sec">
          <div className="tb-h">
            <h2 className="h2">
              Secret stores<span className="cnt">{stores.length}</span>
            </h2>
            <div className="end">
              {manages && (
                <button
                  className="btn sec"
                  type="button"
                  onClick={() => setEditing({ store: "new" })}
                >
                  <Icon name="plus" />
                  Register store
                </button>
              )}
            </div>
          </div>
          {stores.length ? (
            <ul className="clist">
              {stores.map((store) => {
                const set = store.settings ?? {};
                const users = providers.filter(
                  (provider) =>
                    provider.api_key &&
                    "store" in provider.api_key &&
                    provider.api_key.store === store.id,
                );
                return (
                  <li key={store.id}>
                    <span className="ic">
                      <Icon name="db" className="lg" />
                    </span>
                    <div className="tx">
                      <b>{store.name}</b>
                      <code>
                        {store.kind === "kv-v2"
                          ? `${String(set.address)} · ${String(set.mount)}`
                          : JSON.stringify(set)}
                      </code>
                    </div>
                    <div className="used">
                      {users.length
                        ? "Holds the key of " +
                          users.map((provider) => provider.name).join(", ")
                        : "Not used yet"}
                    </div>
                    <div className="meta">
                      <span style={{ display: "flex", gap: "6px" }}>
                        <span className="tag">{store.kind}</span>
                        <span className="pill g">
                          <Icon name="lock" />
                          Credential stored
                        </span>
                      </span>
                      <small>
                        Updated <Time value={store.updated_at} />
                      </small>
                    </div>
                    {manages && (
                      <button
                        className="btn sec sm"
                        type="button"
                        onClick={() => setEditing({ store })}
                      >
                        Edit
                      </button>
                    )}
                  </li>
                );
              })}
            </ul>
          ) : (
            <Empty glyph="db" title="No secret stores yet" />
          )}
        </div>
      </div>

      {editing && "provider" in editing && (
        <EditProvider
          context={context}
          existing={editing.provider === "new" ? undefined : editing.provider}
          onClose={() => setEditing(null)}
        />
      )}
      {editing && "store" in editing && (
        <EditStore
          context={context}
          existing={editing.store === "new" ? undefined : editing.store}
          onClose={() => setEditing(null)}
        />
      )}
    </section>
  );
}
