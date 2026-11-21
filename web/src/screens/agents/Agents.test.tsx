// @vitest-environment happy-dom
import { screen, within } from "@testing-library/react";
import { afterEach, beforeAll, expect, it } from "vitest";
import { fast, leave, open } from "../../testing.tsx";

beforeAll(fast);
afterEach(leave);

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
  expect(document.title).toBe("Agents · Storefront · Aviary");
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
  expect(
    within(harness)
      .getAllByRole("option")
      .map((option) => option.textContent),
  ).toEqual(["codex", "opencode"]);
  await user.selectOptions(harness, "codex");
  await user.selectOptions(
    dialog.getByRole("combobox", { name: "Model provider" }),
    "Local model server",
  );
  await user.click(
    dialog.getByRole("checkbox", { name: "Ask before it acts" }),
  );

  const create = dialog.getByRole("button", { name: "Create agent" });
  await user.click(create);
  expect(dialog.getByRole("alert").textContent).toBe("Enter the model.");
  await user.type(
    dialog.getByRole("textbox", { name: "Model" }),
    "coder-small",
  );
  await user.click(create);

  const created = await screen.findByRole("link", { name: /Notes/ });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(created.getAttribute("href")).toBe("/p/storefront/agents/notes");
  expect(created.textContent).toContain("Local model server / coder-small");
  expect(created.textContent).toContain("codex harness");
  expect(created.textContent).toContain("Writes the release notes.");
  expect(created.textContent).toContain("Asks before it acts");
  expect(screen.getByRole("status").textContent).toContain("Created Notes");
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
  const model = dialog.getByRole("textbox", { name: "Model" });
  expect((model as HTMLInputElement).value).toBe("reasoner-mini");

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

it("asks a project without a model provider to add one first", async () => {
  const user = open("/p/playground/agents", "alice");
  expect(await screen.findByText("No agents yet")).toBeTruthy();
  await user.click(screen.getAllByRole("button", { name: /New agent/ })[0]);
  const dialog = within(
    screen.getByRole("dialog", { name: "Add a model provider first" }),
  );
  expect(
    dialog.getByRole("button", { name: "Add a model provider" }),
  ).toBeTruthy();
  await user.click(dialog.getByRole("button", { name: "Cancel" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByText("No agents yet")).toBeTruthy();
});
