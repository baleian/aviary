import { useState } from "react";
import { api } from "../../api/client.ts";
import { keys, type ProjectContext } from "../../api/project.ts";
import type { Provider, ProviderModels } from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { text, Wrong } from "../../lib/form.ts";
import { sentence } from "../../lib/format.ts";
import { queryClient } from "../../query.ts";
import { Dialog } from "../../ui/Dialog.tsx";
import { Field, Options, Secret, Select } from "../../ui/fields.tsx";
import { toast } from "../../ui/toast.ts";

// Where a provider gets its key: entered here and kept by Aviary, read from
// a secret store, or none.
type How = "value" | "store" | "none";

// said puts what came of asking an endpoint for its models in words, as a
// test of the connection to it.
function said(asked: ProviderModels): {
  tone: "ok" | "bad" | "";
  text: string;
} {
  if (!asked.reached) {
    return { tone: "bad", text: sentence("Not reached: " + asked.reason) };
  }
  const status = asked.status ?? 0;
  if (status >= 200 && status < 300) {
    return { tone: "ok", text: `Connected in ${asked.duration_ms} ms.` };
  }
  if (status === 401 || status === 403) {
    return { tone: "bad", text: `The endpoint refused the key (${status}).` };
  }
  if (status === 404) {
    return {
      tone: "",
      text: "Reached. The endpoint lists no models at this URL (404).",
    };
  }
  return { tone: "bad", text: `The endpoint answered ${status}.` };
}

// EditProvider is the dialog that adds a model provider, or replaces one:
// its name, URL and key together, so the key is entered again. The
// connection can be tested before they are saved, by asking the endpoint for
// its models.
export function EditProvider({
  context,
  existing,
  onClose,
}: {
  context: ProjectContext;
  existing?: Provider;
  onClose: () => void;
}) {
  const kept = existing?.api_key;
  const at = kept && "store" in kept ? kept : undefined;
  const [how, setHow] = useState<How>(
    !existing || (kept && !at) ? "value" : at ? "store" : "none",
  );
  const project = context.project.id;
  const [trying, setTrying] = useState(false);
  const [tried, setTried] = useState<ReturnType<typeof said>>();

  // endpoint reads the URL and the key that the form holds.
  function endpoint(data: FormData): Record<string, unknown> {
    const body: Record<string, unknown> = { url: text(data, "url") };
    if (!body.url) throw new Wrong("Enter the URL of the model endpoint.");
    if (how === "value") {
      const value = data.get("value");
      if (!value) {
        throw new Wrong(existing ? "Enter the key again." : "Enter the key.");
      }
      body.api_key = { value };
    } else if (how === "store") {
      if (!context.stores.length) {
        throw new Wrong("Register a secret store first.");
      }
      const path = text(data, "path");
      const key = text(data, "key");
      if (!path || !key) {
        throw new Wrong("Enter the path and the key of the secret.");
      }
      body.api_key = { store: text(data, "store"), path, key };
    }
    return body;
  }

  async function save(data: FormData): Promise<Provider> {
    const name = text(data, "name");
    if (!name) throw new Wrong("Enter a name.");
    const body = { name, ...endpoint(data) };
    const saved = existing
      ? await api.put<Provider>("/v1/providers/" + existing.id, body)
      : await api.post<Provider>(`/v1/projects/${project}/providers`, body);
    await queryClient.invalidateQueries({ queryKey: keys.providers(project) });
    return saved;
  }

  async function tryIt(form: HTMLFormElement) {
    setTried(undefined);
    setTrying(true);
    try {
      const path = `/v1/projects/${project}/providers/models`;
      const asked = endpoint(new FormData(form));
      setTried(said(await api.post<ProviderModels>(path, asked)));
    } catch (failure) {
      const why = failure instanceof Error ? failure.message : "";
      setTried({ tone: "bad", text: sentence(why) });
    } finally {
      setTrying(false);
    }
  }

  return (
    <Dialog
      size="wide"
      title={existing ? "Edit " + existing.name : "Add a model provider"}
      action={existing ? "Save provider" : "Add provider"}
      onSubmit={save}
      onClose={(saved) => {
        onClose();
        if (saved) toast(`${existing ? "Saved" : "Added"} ${saved.name}`, "ok");
      }}
    >
      <div className="fld-row">
        <Field label="Name">
          <input
            className="inp"
            name="name"
            defaultValue={existing?.name}
            placeholder="Company gateway"
            autoComplete="off"
            data-autofocus
          />
        </Field>
        <Field label="URL">
          <input
            className="inp mono"
            name="url"
            defaultValue={existing?.url}
            placeholder="https://api.example.com/v1"
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
      </div>
      <div className="fld">
        <span className="lab">API key</span>
        <Options
          name="how"
          value={how}
          onChange={setHow}
          choices={[
            {
              value: "value",
              title: "Store the key in Aviary",
              body: (
                <Secret
                  name="value"
                  aria-label="API key"
                  placeholder={kept && !at ? "Enter the key again" : "API key"}
                />
              ),
            },
            {
              value: "store",
              title: "Use a key from a secret store",
              body: context.stores.length ? (
                <>
                  <div className="fld-row">
                    <Field label="Secret store">
                      <Select
                        name="store"
                        defaultValue={at?.store}
                        options={context.stores.map((store) => [
                          store.id,
                          store.name,
                        ])}
                      />
                    </Field>
                    <Field label="Path">
                      <input
                        className="inp mono"
                        name="path"
                        defaultValue={at?.path}
                        placeholder="team/model"
                        autoComplete="off"
                        spellCheck={false}
                      />
                    </Field>
                  </div>
                  <Field label="Key">
                    <input
                      className="inp mono"
                      name="key"
                      defaultValue={at?.key}
                      placeholder="api_key"
                      autoComplete="off"
                      spellCheck={false}
                    />
                  </Field>
                </>
              ) : (
                <p className="sub sm">This project has no secret store yet.</p>
              ),
            },
            { value: "none", title: "No key" },
          ]}
        />
      </div>
      <div className="try">
        <button
          className="btn sec sm"
          type="button"
          disabled={trying}
          onClick={(event) => void tryIt(event.currentTarget.form!)}
        >
          {trying && <span className="ring" />}
          Test connection
        </button>
        {tried && (
          <span className={cls("try-r", tried.tone)} role="status">
            {tried.text}
          </span>
        )}
      </div>
    </Dialog>
  );
}
