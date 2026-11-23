import { useState } from "react";
import { can } from "../../api/project.ts";
import type { Header, SecretStore } from "../../api/types.ts";
import { label, plural } from "../../lib/format.ts";
import { useTitle } from "../../lib/title.ts";
import { useProject } from "../../shell/project.ts";
import { Empty } from "../../ui/Blank.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Time } from "../../ui/Time.tsx";
import { EditStore } from "./EditStore.tsx";
import { Find, Head, None } from "./parts.tsx";

// inStore says whether the headers take a secret of the store.
const inStore = (headers: Header[] | undefined, store: string) =>
  (headers ?? []).some(
    (header) =>
      header.secret &&
      "store" in header.secret &&
      header.secret.store === store,
  );

// How many there are before the list offers to search them.
const SEARCHED = 4;

// Stores is the screen of the secret stores of a project: where the keys of
// its model providers and the secrets of its MCP servers may be kept.
export function Stores() {
  const context = useProject();
  const { project, providers, mcpServers, stores } = context;
  const manages = can(context, "connections.manage");
  const [editing, setEditing] = useState<SecretStore | "new" | null>(null);
  const [sought, setSought] = useState("");
  useTitle("Secret stores", label(project));
  const words = sought.trim().toLowerCase();
  const shown = stores.filter((store) =>
    `${store.name} ${String(store.settings?.address ?? "")}`
      .toLowerCase()
      .includes(words),
  );
  return (
    <section className="page">
      <div className="wrap">
        <Head
          over={label(project)}
          title="Secret stores"
          action={
            manages && (
              <button
                className="btn pri"
                type="button"
                onClick={() => setEditing("new")}
              >
                <Icon name="plus" />
                Register store
              </button>
            )
          }
        />
        <div className="card">
          {stores.length > SEARCHED && (
            <div className="tb-h">
              <Find
                value={sought}
                onChange={setSought}
                placeholder="Find a store"
              />
              <span className="end sub">{plural(stores.length, "store")}</span>
            </div>
          )}
          {stores.length ? (
            <ul className="clist">
              {shown.map((store) => {
                const set = store.settings ?? {};
                const users = [
                  ...providers
                    .filter(
                      (provider) =>
                        (provider.api_key &&
                          "store" in provider.api_key &&
                          provider.api_key.store === store.id) ||
                        inStore(provider.headers, store.id),
                    )
                    .map((provider) => provider.name),
                  ...mcpServers
                    .filter((server) => inStore(server.headers, store.id))
                    .map((server) => label(server)),
                ];
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
                    <div
                      className="used"
                      title={users.length ? users.join(", ") : undefined}
                    >
                      {users.length
                        ? `Holds the secrets of ${users.slice(0, 2).join(", ")}` +
                          (users.length > 2
                            ? ` and ${users.length - 2} more`
                            : "")
                        : "Not used yet"}
                    </div>
                    <div className="meta">
                      <span className="pills">
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
                        onClick={() => setEditing(store)}
                      >
                        Edit
                      </button>
                    )}
                  </li>
                );
              })}
              {!shown.length && <None sought={sought} what="store" />}
            </ul>
          ) : (
            <Empty glyph="db" title="No secret stores yet" />
          )}
        </div>
      </div>
      {editing && (
        <EditStore
          context={context}
          existing={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
    </section>
  );
}
