// @vitest-environment happy-dom
import { screen, within } from "@testing-library/react";
import { afterEach, beforeAll, expect, it } from "vitest";
import { backend } from "../../api/fake.ts";
import { fast, leave, open, titled } from "../../testing.tsx";

beforeAll(fast);
afterEach(leave);

// asked sends the backend a request of Alice, an administrator, and returns
// the body of its answer.
async function asked<T>(method: string, path: string, body: unknown = null) {
  const mock = await backend();
  return mock.request(method, path, { token: "alice", body }).body as T;
}

// storefront returns the project Storefront and its model providers by name.
async function storefront() {
  const { projects } = await asked<{
    projects: { id: string; slug: string }[];
  }>("GET", "/v1/projects");
  const project = projects.find((each) => each.slug === "storefront")!;
  const { providers } = await asked<{
    providers: { id: string; name: string }[];
  }>("GET", `/v1/projects/${project.id}/providers`);
  const ids = new Map(providers.map((each) => [each.name, each.id]));
  return { project: project.id, providers: ids };
}

// options returns the choices of a select box.
function options(select: HTMLElement) {
  return within(select)
    .getAllByRole("option")
    .map((option) => option.textContent);
}

// card returns the card of the agent with the name, once it shows.
async function card(name: string) {
  const link = await screen.findByRole("link", { name: new RegExp(name) });
  return within(link.parentElement!);
}

it("lists the agents of a project", async () => {
  open("/p/storefront/agents", "alice");
  const backend = await screen.findByRole("link", {
    name: /Backend engineer/,
  });
  expect(backend.getAttribute("href")).toBe(
    "/p/storefront/agents/backend-engineer",
  );
  expect(backend.textContent).toContain("Company gateway / coder-large");
  expect(backend.textContent).toContain("opencode harness");
  expect(backend.textContent).toContain("Asks before it acts");
  const reviewer = screen.getByRole("link", { name: /Reviewer/ });
  expect(reviewer.textContent).toContain("Acts without asking");
  expect(reviewer.textContent).toContain("Reviews a branch");
  // An agent without a name shows its slug, and one without a description
  // says so.
  const unnamed = screen.getByRole("link", { name: /release-helper/ });
  expect(unnamed.textContent).toContain("No description");
  await titled("Agents · Storefront · Aviary");
  // The bar marks the screen that is in view, and no other.
  expect(screen.getByRole("link", { name: "Agents" }).className).toBe("on");
  expect(screen.getByRole("link", { name: "Overview" }).className).toBe("");
});

it("creates an agent", async () => {
  const user = open("/p/storefront/agents", "alice");
  await user.click(
    (await screen.findAllByRole("button", { name: /New agent/ }))[0],
  );
  const dialog = within(screen.getByRole("dialog", { name: "New agent" }));
  await user.type(dialog.getByRole("textbox", { name: /Name/ }), "Notes");
  await user.type(
    dialog.getByRole("textbox", { name: /Description/ }),
    "Writes the release notes.",
  );
  const harness = dialog.getByRole("combobox", { name: "Harness" });
  expect(options(harness)).toEqual(["codex", "opencode"]);
  await user.selectOptions(harness, "codex");
  await user.selectOptions(
    dialog.getByRole("combobox", { name: "Model provider" }),
    "Local model server",
  );
  await user.click(
    dialog.getByRole("checkbox", { name: "Ask before it acts" }),
  );

  // The provider lists no models, so the model is entered.
  const model = await dialog.findByRole("textbox", { name: "Model" });
  const create = dialog.getByRole("button", { name: "Create agent" });
  await user.click(create);
  expect(dialog.getByRole("alert").textContent).toBe("Enter the model.");
  await user.type(model, "coder-small");
  await user.click(create);

  // The agent is there to be talked to: its workspace opens.
  expect(
    await screen.findByRole("heading", { name: "New session" }),
  ).toBeTruthy();
  expect(screen.queryByRole("dialog")).toBeNull();
  await titled("Notes · Storefront · Aviary");
  expect(document.querySelector(".welcome")!.textContent).toBe(
    "NotesWrites the release notes.codexcoder-smallasks before it acts",
  );
  expect(screen.getByRole("status").textContent).toContain("Created Notes");

  // And it is among the agents of the project.
  await user.click(document.querySelector(".l-ag")!);
  const created = await screen.findByRole("link", { name: /Notes/ });
  expect(created.getAttribute("href")).toBe("/p/storefront/agents/notes");
  expect(created.textContent).toContain("Local model server / coder-small");
  expect(created.textContent).toContain("codex harness");
  expect(created.textContent).toContain("Writes the release notes.");
  expect(created.textContent).toContain("Asks before it acts");
});

it("changes an agent, but for its slug and its harness", async () => {
  const user = open("/p/storefront/agents", "alice");
  await user.click(
    (await card("Reviewer")).getByRole("button", { name: "More" }),
  );
  await user.click(screen.getByRole("menuitem", { name: "Edit agent" }));
  const dialog = within(screen.getByRole("dialog", { name: "Edit Reviewer" }));
  expect(dialog.queryByRole("textbox", { name: /Slug/ })).toBeNull();
  expect(dialog.queryByRole("combobox", { name: "Harness" })).toBeNull();
  const model = await dialog.findByRole("combobox", { name: "Model" });
  expect((model as HTMLSelectElement).value).toBe("reasoner-mini");

  const description = dialog.getByRole("textbox", { name: /Description/ });
  await user.clear(description);
  await user.type(description, "Reads every diff.");
  await user.click(
    dialog.getByRole("checkbox", { name: "Ask before it acts" }),
  );
  await user.click(dialog.getByRole("button", { name: "Save agent" }));

  expect(await screen.findByText("Reads every diff.")).toBeTruthy();
  const changed = screen.getByRole("link", { name: /Reviewer/ });
  expect(changed.textContent).toContain("Asks before it acts");
  expect(changed.textContent).toContain("codex harness");
  expect(changed.textContent).toContain("Company gateway / reasoner-mini");
  expect(screen.getByRole("status").textContent).toContain("Saved Reviewer");
});

it("chooses the MCP servers that an agent begins its sessions with", async () => {
  const user = open("/p/storefront/agents", "alice");
  const reviewer = await card("Reviewer");
  expect(reviewer.getByText("Code host, CI pipelines")).toBeTruthy();
  await user.click(reviewer.getByRole("button", { name: "More" }));
  await user.click(screen.getByRole("menuitem", { name: "Edit agent" }));
  const dialog = within(screen.getByRole("dialog", { name: "Edit Reviewer" }));
  await dialog.findByRole("combobox", { name: "Model" });
  const field = dialog
    .getByRole("textbox", { name: "Find an MCP server" })
    .closest<HTMLElement>(".fld")!;
  expect(field.querySelector(".lab")!.textContent).toBe("MCP servers2 chosen");
  // The servers that the agent has come first.
  const boxes = () => within(field).getAllByRole<HTMLInputElement>("checkbox");
  expect(
    boxes()
      .slice(0, 3)
      .map((box) => box.checked),
  ).toEqual([true, true, false]);
  const find = dialog.getByRole("textbox", { name: "Find an MCP server" });
  await user.type(find, "refund");
  const shown = () =>
    [...field.querySelectorAll<HTMLElement>(".pick-r")].filter(
      (row) => !row.hidden,
    );
  expect(shown().map((row) => row.textContent)).toEqual([
    "Payments sandboxTool list_refunds",
  ]);
  await user.click(within(field).getByRole("checkbox", { name: /Payments/ }));
  expect(field.querySelector(".lab")!.textContent).toBe("MCP servers3 chosen");
  await user.clear(find);
  await user.type(find, "nothing");
  expect(
    within(field).getByText("No server or tool matches “nothing”."),
  ).toBeTruthy();
  // What a search hides stays chosen.
  await user.click(dialog.getByRole("button", { name: "Save agent" }));
  expect(
    (await screen.findByRole("link", { name: /Reviewer/ })).textContent,
  ).toContain("3 MCP servers");
});

it("chooses the model of an agent among those that its provider lists", async () => {
  const user = open("/p/storefront/agents", "alice");
  await user.click(
    (await screen.findAllByRole("button", { name: /New agent/ }))[0],
  );
  const dialog = within(screen.getByRole("dialog", { name: "New agent" }));
  await user.type(dialog.getByRole("textbox", { name: /Name/ }), "Notes");
  const model = await dialog.findByRole("combobox", { name: "Model" });
  expect(options(model)).toEqual([
    "coder-large",
    "coder-small",
    "reasoner-mini",
  ]);
  expect((model as HTMLSelectElement).value).toBe("coder-large");
  expect(dialog.queryByRole("textbox", { name: "Model" })).toBeNull();

  // Another provider has other models, or lists none: the choice starts
  // again.
  const provider = dialog.getByRole("combobox", { name: "Model provider" });
  await user.selectOptions(model, "reasoner-mini");
  await user.selectOptions(provider, "Local model server");
  expect(
    (
      (await dialog.findByRole("textbox", {
        name: "Model",
      })) as HTMLInputElement
    ).value,
  ).toBe("");
  expect(dialog.queryByRole("combobox", { name: "Model" })).toBeNull();
  await user.selectOptions(provider, "Company gateway");
  const again = await dialog.findByRole("combobox", { name: "Model" });
  expect((again as HTMLSelectElement).value).toBe("coder-large");
  await user.selectOptions(again, "coder-small");
  await user.click(dialog.getByRole("button", { name: "Create agent" }));

  expect(
    await screen.findByRole("heading", { name: "New session" }),
  ).toBeTruthy();
  await user.click(document.querySelector(".l-ag")!);
  const created = await screen.findByRole("link", { name: /Notes/ });
  expect(created.textContent).toContain("Company gateway / coder-small");
});

it("keeps the model of an agent a choice if its provider no longer lists it", async () => {
  const { project, providers } = await storefront();
  await asked("POST", `/v1/projects/${project}/agents`, {
    slug: "veteran",
    name: "Veteran",
    harness: "opencode",
    model: { provider: providers.get("Company gateway"), name: "coder-2023" },
  });
  const user = open("/p/storefront/agents", "alice");
  await user.click(
    (await card("Veteran")).getByRole("button", { name: "More" }),
  );
  await user.click(screen.getByRole("menuitem", { name: "Edit agent" }));
  const dialog = within(screen.getByRole("dialog", { name: "Edit Veteran" }));
  const model = await dialog.findByRole("combobox", { name: "Model" });
  expect(options(model)).toEqual([
    "coder-2023",
    "coder-large",
    "coder-small",
    "reasoner-mini",
  ]);
  expect((model as HTMLSelectElement).value).toBe("coder-2023");
});

it("has the model of an agent again when its provider is chosen again", async () => {
  const { project } = await storefront();
  await asked("POST", `/v1/projects/${project}/providers`, {
    name: "Second",
    url: "https://second.example.com/v1",
  });
  const user = open("/p/storefront/agents", "alice");
  await user.click(
    (await card("Reviewer")).getByRole("button", { name: "More" }),
  );
  await user.click(screen.getByRole("menuitem", { name: "Edit agent" }));
  const dialog = within(screen.getByRole("dialog", { name: "Edit Reviewer" }));
  // chosen returns the box of the models once it has the model.
  const chosen = async (model: string) =>
    (await dialog.findByRole("option", { name: model }))
      .parentElement as HTMLSelectElement;
  expect((await chosen("reasoner-mini")).value).toBe("reasoner-mini");

  // At another provider the choice is among its models.
  const provider = dialog.getByRole("combobox", { name: "Model provider" });
  await user.selectOptions(provider, "Second");
  const other = await chosen("model-a");
  expect(options(other)).toEqual(["model-a", "model-b"]);
  expect(other.value).toBe("model-a");
  await user.selectOptions(other, "model-b");
  await user.selectOptions(provider, "Company gateway");
  expect((await chosen("reasoner-mini")).value).toBe("reasoner-mini");
});

it("has the model entered at a provider that is not reached, and says why", async () => {
  const { project, providers } = await storefront();
  await asked("POST", `/v1/projects/${project}/providers`, {
    name: "Gone",
    url: "https://gone.invalid/v1",
  });
  let user = open("/p/storefront/agents", "alice");
  await user.click(
    (await screen.findAllByRole("button", { name: /New agent/ }))[0],
  );
  let dialog = within(screen.getByRole("dialog", { name: "New agent" }));
  await user.type(dialog.getByRole("textbox", { name: /Name/ }), "Notes");
  await dialog.findByRole("combobox", { name: "Model" });
  await user.selectOptions(
    dialog.getByRole("combobox", { name: "Model provider" }),
    "Gone",
  );
  const why =
    "Not reached: upstream connect error or disconnect/reset before headers. reset reason: connection timeout.";
  expect((await dialog.findByRole("status")).textContent).toBe(why);
  expect(dialog.queryByRole("combobox", { name: "Model" })).toBeNull();
  // The model is entered, as at a provider that lists none.
  const model = dialog.getByRole("textbox", { name: /^Model/ });
  const create = dialog.getByRole("button", { name: "Create agent" });
  await user.click(create);
  expect(dialog.getByRole("alert").textContent).toBe("Enter the model.");
  await user.type(model, "coder-xl");
  await user.click(create);
  await screen.findByRole("heading", { name: "New session" });
  await user.click(document.querySelector(".l-ag")!);
  expect(
    (await screen.findByRole("link", { name: /Notes/ })).textContent,
  ).toContain("Gone / coder-xl");
  await leave();

  // An agent that has a model there has it in the field, and keeps it.
  await asked("PUT", "/v1/providers/" + providers.get("Company gateway"), {
    name: "Company gateway",
    url: "https://llm.invalid/v1",
    api_key: { value: "sk-gateway" },
  });
  user = open("/p/storefront/agents", "alice");
  await user.click(
    (await card("Reviewer")).getByRole("button", { name: "More" }),
  );
  await user.click(screen.getByRole("menuitem", { name: "Edit agent" }));
  dialog = within(screen.getByRole("dialog", { name: "Edit Reviewer" }));
  expect((await dialog.findByRole("status")).textContent).toBe(why);
  expect(
    dialog.getByRole<HTMLInputElement>("textbox", { name: /^Model/ }).value,
  ).toBe("reasoner-mini");
  const description = dialog.getByRole("textbox", { name: /Description/ });
  await user.clear(description);
  await user.type(description, "Reads every diff.");
  await user.click(dialog.getByRole("button", { name: "Save agent" }));
  expect(await screen.findByText("Reads every diff.")).toBeTruthy();
  expect(screen.getByRole("link", { name: /Reviewer/ }).textContent).toContain(
    "Company gateway / reasoner-mini",
  );
});

it("waits for the models of a provider before the model is chosen", async () => {
  const user = open("/p/storefront/agents", "alice");
  const create = (
    await screen.findAllByRole("button", { name: /New agent/ })
  )[0];
  // The backend takes a moment to answer, as an API does.
  const { mock } = await backend();
  mock.speed = 1;
  try {
    await user.click(create);
    const dialog = within(screen.getByRole("dialog", { name: "New agent" }));
    const waiting = dialog.getByRole("combobox", { name: "Model" });
    expect((waiting as HTMLSelectElement).disabled).toBe(true);
    expect(options(waiting)).toEqual(["Loading…"]);
    const model = await dialog.findByRole("option", { name: "coder-large" });
    expect((model.parentElement as HTMLSelectElement).disabled).toBe(false);
  } finally {
    mock.speed = 0;
  }
});

it("deletes an agent, unless a turn of it has not ended", async () => {
  const user = open("/p/storefront/agents", "alice");
  await user.click(
    (await card("Backend engineer")).getByRole("button", { name: "More" }),
  );
  await user.click(screen.getByRole("menuitem", { name: "Delete agent…" }));
  const busy = within(
    screen.getByRole("dialog", { name: "Delete Backend engineer?" }),
  );
  await user.click(busy.getByRole("button", { name: "Delete agent" }));
  expect((await busy.findByRole("alert")).textContent).toBe(
    "Stop its running turns first.",
  );
  await user.click(busy.getByRole("button", { name: "Cancel" }));
  expect(screen.getByRole("link", { name: /Backend engineer/ })).toBeTruthy();

  await user.click(
    (await card("Docs writer")).getByRole("button", { name: "More" }),
  );
  await user.click(screen.getByRole("menuitem", { name: "Delete agent…" }));
  const idle = within(
    screen.getByRole("dialog", { name: "Delete Docs writer?" }),
  );
  await user.click(idle.getByRole("button", { name: "Delete agent" }));
  expect(await screen.findByText("Deleted Docs writer")).toBeTruthy();
  expect(screen.queryByRole("link", { name: /Docs writer/ })).toBeNull();
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("shows a caller only what their role allows", async () => {
  // A user of the project sees its agents, and changes none.
  let user = open("/p/storefront/agents", "bob");
  await screen.findByRole("link", { name: /Reviewer/ });
  expect(screen.queryByRole("button", { name: /New agent/ })).toBeNull();
  expect(screen.queryByRole("button", { name: "More" })).toBeNull();
  await leave();

  // An editor manages the agents, but does not end the sessions of others,
  // which deleting an agent does.
  user = open("/p/internal-tools/agents", "bob");
  await user.click(
    (await card("Helpdesk")).getByRole("button", { name: "More" }),
  );
  expect(screen.getByRole("menuitem", { name: "Edit agent" })).toBeTruthy();
  expect(screen.queryByRole("menuitem", { name: "Delete agent…" })).toBeNull();
  expect(screen.getAllByRole("button", { name: /New agent/ }).length).toBe(2);
  await leave();

  // An owner does both.
  user = open("/p/storefront/agents", "carol");
  await user.click(
    (await card("Reviewer")).getByRole("button", { name: "More" }),
  );
  expect(screen.getByRole("menuitem", { name: "Edit agent" })).toBeTruthy();
  expect(screen.getByRole("menuitem", { name: "Delete agent…" })).toBeTruthy();
});

it("sends a project without a model provider to add one first", async () => {
  const user = open("/p/playground/agents", "alice");
  expect(await screen.findByText("No agents yet")).toBeTruthy();
  await user.click(screen.getAllByRole("button", { name: /New agent/ })[0]);
  let dialog = within(
    screen.getByRole("dialog", { name: "Add a model provider first" }),
  );
  await user.click(dialog.getByRole("button", { name: "Cancel" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByText("No agents yet")).toBeTruthy();

  await user.click(screen.getAllByRole("button", { name: /New agent/ })[0]);
  dialog = within(
    screen.getByRole("dialog", { name: "Add a model provider first" }),
  );
  await user.click(
    dialog.getByRole("button", { name: "Add a model provider" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Model providers" }),
  ).toBeTruthy();
  expect(screen.getByText("No model providers yet")).toBeTruthy();
});
