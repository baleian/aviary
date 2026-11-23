import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { api, ApiError } from "../../api/client.ts";
import { can, keys, type ProjectContext } from "../../api/project.ts";
import type { Agent, Harness, ProviderModels } from "../../api/types.ts";
import { named, text, Wrong } from "../../lib/form.ts";
import { label, plural, sentence, transportName } from "../../lib/format.ts";
import { queryClient } from "../../query.ts";
import { Dialog } from "../../ui/Dialog.tsx";
import { Check, Field, NameAndSlug, Select, Toggle } from "../../ui/fields.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { toast } from "../../ui/toast.ts";
import { matches } from "../../lib/mcp.ts";

// ModelField is where the model of an agent is given: chosen among the
// models that the provider lists, or entered if the provider lists none or
// is not reached. Why it is not reached shows below the field.
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
  const entered = (unreached?: string) => (
    <Field label="Model" warning={unreached && sentence(unreached)}>
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
  if (listed.isError) return entered(listed.error.message);
  if (!listed.data.reached) {
    return entered("Not reached: " + listed.data.reason);
  }
  const models = listed.data.models ?? [];
  if (!models.length) return entered();
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

// How many MCP servers a project has before the field offers to search them.
const SEARCHED = 6;

// McpField is where the MCP servers of the project are chosen that the
// sessions of an agent begin with. Those that are chosen come first, in the
// order that they had when the field appeared, and a search narrows the rest
// by name, slug or tool. A row that the search hides keeps its box in the
// form.
function McpField({
  context,
  chosen,
}: {
  context: ProjectContext;
  chosen: string[];
}) {
  const [on, setOn] = useState(() => new Set(chosen));
  const [sought, setSought] = useState("");
  const [order] = useState(() => [
    ...context.mcpServers.filter((server) => chosen.includes(server.id)),
    ...context.mcpServers.filter((server) => !chosen.includes(server.id)),
  ]);
  const servers = context.mcpServers;
  const count = servers.filter((server) => on.has(server.id)).length;
  const shown = order.map((server) => ({
    server,
    match: matches(server, sought),
  }));
  return (
    <div className="fld">
      <span className="lab">
        MCP servers
        <em>{count ? `${count} chosen` : "optional"}</em>
      </span>
      {servers.length ? (
        <div className="picks">
          {servers.length > SEARCHED && (
            <label className="inp-i picks-find">
              <Icon name="search" />
              <input
                className="inp"
                placeholder="Find a server or a tool"
                aria-label="Find an MCP server"
                value={sought}
                onChange={(event) => setSought(event.target.value)}
                // Enter finds; it does not send the form.
                onKeyDown={(event) => {
                  if (event.key === "Enter") event.preventDefault();
                }}
              />
            </label>
          )}
          <div className="picks-list">
            {shown.map(({ server, match }) => (
              <span key={server.id} hidden={!match} className="pick-r">
                <Check
                  name="mcp"
                  value={server.id}
                  checked={on.has(server.id)}
                  onChange={(event) => {
                    const next = new Set(on);
                    if (event.target.checked) next.add(server.id);
                    else next.delete(server.id);
                    setOn(next);
                  }}
                >
                  <span className="pick">
                    <Icon name="plug" />
                    <b>{label(server)}</b>
                    <small>
                      {match?.tool
                        ? `Tool ${match.tool}`
                        : server.tools
                          ? plural(server.tools.length, "tool")
                          : transportName(server.transport)}
                    </small>
                  </span>
                </Check>
              </span>
            ))}
            {!shown.some(({ match }) => match) && (
              <p className="sub none-found">
                No server or tool matches “{sought.trim()}”.
              </p>
            )}
          </div>
        </div>
      ) : (
        <p className="sub sm">
          This project has no MCP servers yet.
          {can(context, "connections.manage") && (
            <>
              {" "}
              <Link to={`/p/${context.project.slug}/mcp-servers`}>Add one</Link>
            </>
          )}
        </p>
      )}
    </div>
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
        action={manages ? "Add a model provider" : "See model providers"}
        onSubmit={() => true}
        onClose={(go) => {
          onClose();
          if (go) void navigate(`/p/${context.project.slug}/providers`);
        }}
      />
    );
  }

  async function save(data: FormData): Promise<Agent> {
    const model = text(data, "model");
    const names = existing ? { name: text(data, "name") } : named(data);
    if (!model) throw new Wrong("Enter the model.");
    const body = {
      ...names,
      description: text(data, "description"),
      model: { provider: text(data, "provider"), name: model },
      instructions: text(data, "instructions"),
      approval: data.get("approval") ? "ask" : "",
      mcp_servers: data.getAll("mcp").map(String),
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
      <h3 className="dlg-sec">Tools</h3>
      <McpField context={context} chosen={existing?.mcp_servers ?? []} />
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
