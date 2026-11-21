import { useState } from "react";
import { api } from "../../api/client.ts";
import { keys, type ProjectContext } from "../../api/project.ts";
import type { SecretStore } from "../../api/types.ts";
import { text, Wrong } from "../../lib/form.ts";
import { queryClient } from "../../query.ts";
import { Dialog } from "../../ui/Dialog.tsx";
import { Field, Secret, Select } from "../../ui/fields.tsx";
import { toast } from "../../ui/toast.ts";

// The kind of store whose settings the console has fields for.
const BUILTIN = "kv-v2";

// EditStore is the dialog that registers a secret store, or replaces one
// whole, so the credential is entered again. A store of another kind than
// the built-in one gives its settings as JSON.
export function EditStore({
  context,
  existing,
  onClose,
}: {
  context: ProjectContext;
  existing?: SecretStore;
  onClose: () => void;
}) {
  const [builtin, setBuiltin] = useState(
    !existing || existing.kind === BUILTIN,
  );
  const settings = existing?.settings ?? {};
  const known = !existing || existing.kind === BUILTIN;
  const project = context.project.id;
  const setting = (name: string) => String(settings[name] ?? "");

  async function save(data: FormData): Promise<SecretStore> {
    const credential = data.get("credential");
    const body: Record<string, unknown> = {
      name: text(data, "name"),
      kind: builtin ? BUILTIN : text(data, "other"),
      credential: { value: credential },
    };
    if (!body.name) throw new Wrong("Enter a name.");
    if (builtin) {
      body.settings = {
        address: text(data, "address"),
        mount: text(data, "mount"),
        role_id: text(data, "role_id"),
      };
    } else {
      if (!body.kind) throw new Wrong("Enter the kind of the store.");
      try {
        body.settings = JSON.parse(text(data, "settings") || "{}");
      } catch {
        throw new Wrong("The settings are not valid JSON.");
      }
    }
    if (!credential) {
      throw new Wrong(
        existing ? "Enter the credential again." : "Enter the credential.",
      );
    }
    const saved = existing
      ? await api.put<SecretStore>("/v1/secret-stores/" + existing.id, body)
      : await api.post<SecretStore>(
          `/v1/projects/${project}/secret-stores`,
          body,
        );
    await queryClient.invalidateQueries({ queryKey: keys.stores(project) });
    return saved;
  }

  const again = existing ? "Enter it again" : undefined;
  return (
    <Dialog
      size="wide"
      title={existing ? "Edit " + existing.name : "Register a secret store"}
      action={existing ? "Save store" : "Register store"}
      onSubmit={save}
      onClose={(saved) => {
        onClose();
        if (!saved) return;
        toast(`${existing ? "Saved" : "Registered"} ${saved.name}`, "ok");
      }}
    >
      <div className="fld-row">
        <Field label="Name">
          <input
            className="inp"
            name="name"
            defaultValue={existing?.name}
            placeholder="Platform secrets"
            autoComplete="off"
            data-autofocus
          />
        </Field>
        <Field label="Kind">
          <Select
            name="kind"
            value={builtin ? BUILTIN : ""}
            onChange={(event) => setBuiltin(event.target.value === BUILTIN)}
            options={[
              [BUILTIN, BUILTIN],
              ["", "Another kind…"],
            ]}
          />
        </Field>
      </div>
      {builtin ? (
        <div className="fld">
          <div className="fld-row">
            <Field label="Address">
              <input
                className="inp mono"
                name="address"
                defaultValue={known ? setting("address") : ""}
                placeholder="https://secrets.example.com:8200"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label="Mount">
              <input
                className="inp mono"
                name="mount"
                defaultValue={known ? setting("mount") : ""}
                placeholder="secret"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
          </div>
          <div className="fld-row">
            <Field label="Role ID">
              <input
                className="inp mono"
                name="role_id"
                defaultValue={known ? setting("role_id") : ""}
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label="Secret ID">
              <Secret name="credential" placeholder={again} />
            </Field>
          </div>
        </div>
      ) : (
        <div className="fld">
          <Field label="Kind">
            <input
              className="inp mono"
              name="other"
              defaultValue={known ? "" : existing?.kind}
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          <Field label="Settings" optional="JSON">
            <textarea
              className="inp mono"
              name="settings"
              rows={5}
              spellCheck={false}
              defaultValue={known ? "" : JSON.stringify(settings, null, 2)}
              placeholder={'{\n  "address": "https://…"\n}'}
            />
          </Field>
          <Field label="Credential">
            <Secret name="credential" placeholder={again} />
          </Field>
        </div>
      )}
    </Dialog>
  );
}
