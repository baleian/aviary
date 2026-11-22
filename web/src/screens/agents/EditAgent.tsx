import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { api, ApiError } from "../../api/client.ts";
import { can, keys, type ProjectContext } from "../../api/project.ts";
import type { Agent, Harness, ProviderModels } from "../../api/types.ts";
import { named, text, Wrong } from "../../lib/form.ts";
import { label, sentence } from "../../lib/format.ts";
import { queryClient } from "../../query.ts";
import { Dialog } from "../../ui/Dialog.tsx";
import { Field, NameAndSlug, Select, Toggle } from "../../ui/fields.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { toast } from "../../ui/toast.ts";

// ModelField is where the model of an agent is given: chosen among the
// models that the provider lists, or entered if the provider lists none. A
// provider that is not reached has no field. Why it is not shows in its
// place, and an agent keeps the model that it has at that provider.
function ModelField({ provider, kept }: { provider: string; kept?: string }) {
  const listed = useQuery({
    queryKey: keys.models(provider),
    queryFn: () => api.get<ProviderModels>(`/v1/providers/${provider}/models`),
  });
  if (listed.isPending) {
    return (
      <Field label="Model">
        <Select name="model" disabled options={[["", "Loading…"]]} />
      </Field>
    );
  }
  if (listed.isError || !listed.data.reached) {
    const why = listed.isError
      ? listed.error.message
      : "Not reached: " + listed.data.reason;
    return (
      <div className="fld">
        <span className="lab">Model</span>
        <div className="alert bad" role="alert">
          <Icon name="warn" />
          <span>{sentence(why)}</span>
        </div>
        <input
          type="hidden"
          name={kept ? "model" : "unreached"}
          value={kept ?? "1"}
        />
      </div>
    );
  }
  const models = listed.data.models ?? [];
  if (!models.length) {
    return (
      <Field label="Model">
        <input
          className="inp mono"
          name="model"
          defaultValue={kept}
          placeholder="coder-large"
          autoComplete="off"
          spellCheck={false}
        />
      </Field>
    );
  }
  // The model of the agent stays a choice if the provider no longer lists it.
  const choices = kept && !models.includes(kept) ? [kept, ...models] : models;
  return (
    <Field label="Model">
      <Select
        // A box of its own, which begins at the model of the agent.
        key="listed"
        name="model"
        defaultValue={kept ?? models[0]}
        options={choices.map((each) => [each, each])}
      />
    </Field>
  );
}

// EditAgent is the dialog that creates an agent, or replaces one: all of it
// but its slug and its harness, which stay as they are. A project without a
// model provider is sent to add one first.
export function EditAgent({
  context,
  existing,
  onClose,
}: {
  context: ProjectContext;
  existing?: Agent;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const harnesses = useQuery({
    queryKey: ["harnesses"],
    queryFn: async () =>
      (await api.get<{ harnesses: Harness[] }>("/v1/harnesses")).harnesses,
    staleTime: Infinity,
    enabled: !existing,
  });
  const [provider, setProvider] = useState(
    existing?.model.provider ?? context.providers[0]?.id ?? "",
  );

  if (!context.providers.length) {
    const manages = can(context, "connections.manage");
    return (
      <Dialog
        size="slim"
        title="Add a model provider first"
        lead={
          manages
            ? "An agent uses a model of one of the project’s providers."
            : "Ask someone who manages the connections of this project."
        }
        action={manages ? "Add a model provider" : "See connections"}
        onSubmit={() => true}
        onClose={(go) => {
          onClose();
          if (go) void navigate(`/p/${context.project.slug}/connections`);
        }}
      />
    );
  }

  async function save(data: FormData): Promise<Agent> {
    const model = text(data, "model");
    const names = existing ? { name: text(data, "name") } : named(data);
    if (!model) {
      throw new Wrong(
        data.has("unreached")
          ? "The model provider is not reached."
          : "Enter the model.",
      );
    }
    const body = {
      ...names,
      description: text(data, "description"),
      model: { provider: text(data, "provider"), name: model },
      instructions: text(data, "instructions"),
      approval: data.get("approval") ? "ask" : "",
    };
    const saved = existing
      ? await api.put<Agent>("/v1/agents/" + existing.id, body)
      : await api.post<Agent>(`/v1/projects/${context.project.id}/agents`, {
          ...body,
          harness: text(data, "harness"),
        });
    await queryClient.invalidateQueries({
      queryKey: keys.agents(context.project.id),
    });
    return saved;
  }

  const provided = (
    <Field label="Model provider">
      <Select
        name="provider"
        value={provider}
        onChange={(event) => setProvider(event.target.value)}
        options={context.providers.map((each) => [each.id, each.name])}
      />
    </Field>
  );
  const model = (
    <ModelField
      key={provider}
      provider={provider}
      kept={
        existing?.model.provider === provider ? existing.model.name : undefined
      }
    />
  );
  return (
    <Dialog
      size="wide"
      title={existing ? "Edit " + label(existing) : "New agent"}
      action={existing ? "Save agent" : "Create agent"}
      onSubmit={save}
      onClose={(saved) => {
        onClose();
        if (!saved) return;
        toast(`${existing ? "Saved" : "Created"} ${label(saved)}`, "ok");
        // A new agent is there to be talked to.
        if (!existing) {
          void navigate(`/p/${context.project.slug}/agents/${saved.slug}`);
        }
      }}
    >
      {existing ? (
        <Field label="Name" optional>
          <input
            className="inp"
            name="name"
            defaultValue={existing.name}
            placeholder="Backend engineer"
            autoComplete="off"
            maxLength={120}
            data-autofocus
          />
        </Field>
      ) : (
        <NameAndSlug example="Backend engineer" />
      )}
      <Field label="Description" optional>
        <input
          className="inp"
          name="description"
          defaultValue={existing?.description}
          placeholder="Features, fixes and tests for the storefront backend."
          autoComplete="off"
          maxLength={200}
        />
      </Field>
      {existing ? (
        <div className="fld-row">
          {provided}
          {model}
        </div>
      ) : (
        <>
          <div className="fld-row">
            <Field label="Harness">
              <Select
                name="harness"
                options={(harnesses.data ?? []).map((each) => [
                  each.name,
                  each.name,
                ])}
              />
            </Field>
            {provided}
          </div>
          {model}
        </>
      )}
      <Field label="Instructions" optional>
        <textarea
          className="inp"
          name="instructions"
          rows={4}
          defaultValue={existing?.instructions}
          placeholder="You work on the storefront backend. Keep changes small, and say which tests you ran."
        />
      </Field>
      <div className="fld">
        <Toggle name="approval" defaultChecked={existing?.approval === "ask"}>
          Ask before it acts
        </Toggle>
      </div>
    </Dialog>
  );
}

// DeleteAgent asks whether to delete an agent, and does.
export function DeleteAgent({
  context,
  agent,
  onClose,
}: {
  context: ProjectContext;
  agent: Agent;
  // onClose is told whether the agent was deleted.
  onClose: (deleted: boolean) => void;
}) {
  return (
    <Dialog
      size="slim"
      danger
      title={`Delete ${label(agent)}?`}
      lead="The sessions of every member end, and the files that the agent made in them are deleted."
      action="Delete agent"
      onSubmit={async () => {
        try {
          await api.delete("/v1/agents/" + agent.id);
        } catch (error) {
          if (error instanceof ApiError && error.code === "session_busy") {
            throw new Wrong("Stop its running turns first.");
          }
          throw error;
        }
        await queryClient.invalidateQueries({
          queryKey: keys.agents(context.project.id),
        });
        return true;
      }}
      onClose={(deleted) => {
        onClose(!!deleted);
        if (deleted) toast(`Deleted ${label(agent)}`, "ok");
      }}
    />
  );
}
