// @vitest-environment happy-dom
import { screen, within } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";
import { afterEach, beforeAll, expect, it } from "vitest";
import { backend } from "../../api/fake.ts";
import { fast, leave, open } from "../../testing.tsx";

beforeAll(fast);
afterEach(leave);

// row returns the row of the connection with the name, once it shows.
async function row(name: string) {
  const title = await screen.findByText(name, { selector: ".clist b" });
  return title.closest("li")!;
}

// dialog opens a dialog with the button that has the name, and returns the
// dialog with the title.
async function dialog(user: UserEvent, button: RegExp, title: string) {
  await user.click((await screen.findAllByRole("button", { name: button }))[0]);
  return within(screen.getByRole("dialog", { name: title }));
}

it("lists the model providers and the secret stores", async () => {
  open("/p/storefront/connections", "alice");
  const gateway = await row("Company gateway");
  expect(gateway.textContent).toContain("https://llm.example.com/v1");
  expect(gateway.textContent).toContain(
    "Used byBackend engineerReviewerrelease-helper",
  );
  expect(gateway.textContent).toContain("Key stored");
  const local = await row("Local model server");
  expect(local.textContent).toContain("aviary/llm → api_key");
  expect(local.textContent).toContain("Used byDocs writer");
  expect(local.textContent).toContain("Key in Platform secrets");
  const store = await row("Platform secrets");
  expect(store.textContent).toContain(
    "https://secrets.example.com:8200 · secret",
  );
  expect(store.textContent).toContain("Holds the key of Local model server");
  expect(store.textContent).toContain("kv-v2Credential stored");
  expect(document.title).toBe("Connections · Storefront · Aviary");
  expect(screen.getByRole("link", { name: "Connections" }).className).toBe(
    "on",
  );
});

it("adds a provider whose key Aviary keeps", async () => {
  const user = open("/p/storefront/connections", "alice");
  const form = await dialog(user, /Add provider/, "Add a model provider");
  const add = form.getByRole("button", { name: "Add provider" });
  // Only the fields of the chosen way to give the key show.
  expect(form.getByLabelText("API key")).toBeTruthy();
  expect(form.queryByRole("textbox", { name: "Path" })).toBeNull();
  await user.click(add);
  expect(form.getByRole("alert").textContent).toBe("Enter a name.");
  await user.type(form.getByRole("textbox", { name: "Name" }), "Lab");
  await user.click(add);
  expect(form.getByRole("alert").textContent).toBe(
    "Enter the URL of the model endpoint.",
  );
  await user.type(
    form.getByRole("textbox", { name: "URL" }),
    "https://lab.example.com/v1",
  );
  await user.click(add);
  expect(form.getByRole("alert").textContent).toBe("Enter the key.");
  await user.type(form.getByLabelText("API key"), "sk-lab");
  await user.click(add);

  const added = await row("Lab");
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(added.textContent).toContain("https://lab.example.com/v1");
  expect(added.textContent).toContain("Not used yet");
  expect(added.textContent).toContain("Key stored");
  expect(screen.getByRole("status").textContent).toContain("Added Lab");
});

it("adds a provider with a key from a secret store, or without a key", async () => {
  const user = open("/p/storefront/connections", "alice");
  let form = await dialog(user, /Add provider/, "Add a model provider");
  await user.type(form.getByRole("textbox", { name: "Name" }), "Shared");
  await user.type(
    form.getByRole("textbox", { name: "URL" }),
    "https://shared.example.com/v1",
  );
  await user.click(
    form.getByRole("radio", { name: "Use a key from a secret store" }),
  );
  const add = form.getByRole("button", { name: "Add provider" });
  await user.click(add);
  expect(form.getByRole("alert").textContent).toBe(
    "Enter the path and the key of the secret.",
  );
  await user.type(form.getByRole("textbox", { name: "Path" }), "team/model");
  await user.type(form.getByRole("textbox", { name: "Key" }), "api_key");
  await user.click(add);
  const shared = await row("Shared");
  expect(shared.textContent).toContain("team/model → api_key");
  expect(shared.textContent).toContain("Key in Platform secrets");
  expect((await row("Platform secrets")).textContent).toContain(
    "Holds the key of Local model server, Shared",
  );

  form = await dialog(user, /Add provider/, "Add a model provider");
  await user.type(form.getByRole("textbox", { name: "Name" }), "Open");
  await user.type(
    form.getByRole("textbox", { name: "URL" }),
    "http://open.example.com/v1",
  );
  await user.click(form.getByRole("radio", { name: "No key" }));
  await user.click(form.getByRole("button", { name: "Add provider" }));
  expect((await row("Open")).textContent).toContain("No key");
});

it("replaces a provider, whose key is entered again", async () => {
  const user = open("/p/storefront/connections", "alice");
  await user.click(
    within(await row("Company gateway")).getByRole("button", { name: "Edit" }),
  );
  const form = within(
    screen.getByRole("dialog", { name: "Edit Company gateway" }),
  );
  const name = form.getByRole("textbox", { name: "Name" });
  expect((name as HTMLInputElement).value).toBe("Company gateway");
  const kept = form.getByRole("radio", { name: "Store the key in Aviary" });
  expect((kept as HTMLInputElement).checked).toBe(true);
  const save = form.getByRole("button", { name: "Save provider" });
  await user.click(save);
  expect(form.getByRole("alert").textContent).toBe("Enter the key again.");

  await user.clear(name);
  await user.type(name, "Gateway");
  await user.click(form.getByRole("radio", { name: "No key" }));
  await user.click(save);
  expect((await row("Gateway")).textContent).toContain("No key");
  expect(screen.queryByText("Company gateway")).toBeNull();
  expect(screen.getByRole("status").textContent).toContain("Saved Gateway");
});

it("opens a provider with its key in a store at that store", async () => {
  const user = open("/p/storefront/connections", "alice");
  await user.click(
    within(await row("Local model server")).getByRole("button", {
      name: "Edit",
    }),
  );
  const form = within(
    screen.getByRole("dialog", { name: "Edit Local model server" }),
  );
  const inStore = form.getByRole("radio", {
    name: "Use a key from a secret store",
  });
  expect((inStore as HTMLInputElement).checked).toBe(true);
  const path = form.getByRole("textbox", { name: "Path" });
  expect((path as HTMLInputElement).value).toBe("aviary/llm");
  // What the API cannot read from the store, it says.
  await user.clear(path);
  await user.type(path, "aviary/missing");
  await user.click(form.getByRole("button", { name: "Save provider" }));
  expect((await form.findByRole("alert")).textContent).toBe(
    "Cannot read api_key from the secret store: secret not found.",
  );
});

it("registers a secret store, and replaces it whole", async () => {
  const user = open("/p/data-platform/connections", "alice");
  expect(await screen.findByText("No secret stores yet")).toBeTruthy();
  // Without a store, a provider cannot take its key from one.
  let form = await dialog(user, /Add provider/, "Add a model provider");
  await user.type(form.getByRole("textbox", { name: "Name" }), "Any");
  await user.type(form.getByRole("textbox", { name: "URL" }), "http://a.test");
  await user.click(
    form.getByRole("radio", { name: "Use a key from a secret store" }),
  );
  expect(form.getByText("This project has no secret store yet.")).toBeTruthy();
  await user.click(form.getByRole("button", { name: "Add provider" }));
  expect(form.getByRole("alert").textContent).toBe(
    "Register a secret store first.",
  );
  await user.click(form.getByRole("button", { name: "Cancel" }));

  form = await dialog(user, /Register store/, "Register a secret store");
  await user.type(form.getByRole("textbox", { name: "Name" }), "Vault");
  await user.type(
    form.getByRole("textbox", { name: "Address" }),
    "https://secrets.example.com:8200",
  );
  await user.type(form.getByRole("textbox", { name: "Mount" }), "data");
  await user.type(form.getByRole("textbox", { name: "Role ID" }), "aviary");
  const register = form.getByRole("button", { name: "Register store" });
  await user.click(register);
  expect(form.getByRole("alert").textContent).toBe("Enter the credential.");
  await user.type(form.getByLabelText("Secret ID"), "s3cret");
  await user.click(register);
  const vault = await row("Vault");
  expect(vault.textContent).toContain(
    "https://secrets.example.com:8200 · data",
  );
  expect(vault.textContent).toContain("Not used yet");
  expect(screen.getByRole("status").textContent).toContain("Registered Vault");

  await user.click(within(vault).getByRole("button", { name: "Edit" }));
  form = within(screen.getByRole("dialog", { name: "Edit Vault" }));
  const mount = form.getByRole("textbox", { name: "Mount" });
  expect((mount as HTMLInputElement).value).toBe("data");
  await user.clear(mount);
  await user.type(mount, "kv");
  const save = form.getByRole("button", { name: "Save store" });
  await user.click(save);
  expect(form.getByRole("alert").textContent).toBe(
    "Enter the credential again.",
  );
  await user.type(form.getByLabelText("Secret ID"), "s3cret");
  await user.click(save);
  expect(await screen.findByText("Saved Vault")).toBeTruthy();
  expect((await row("Vault")).textContent).toContain(
    "https://secrets.example.com:8200 · kv",
  );
});

it("takes the settings of a store of another kind as JSON", async () => {
  const user = open("/p/storefront/connections", "alice");
  const form = await dialog(user, /Register store/, "Register a secret store");
  await user.type(form.getByRole("textbox", { name: "Name" }), "Other");
  await user.selectOptions(
    form.getByRole("combobox", { name: "Kind" }),
    "Another kind…",
  );
  expect(form.queryByRole("textbox", { name: "Address" })).toBeNull();
  const register = form.getByRole("button", { name: "Register store" });
  await user.click(register);
  expect(form.getByRole("alert").textContent).toBe(
    "Enter the kind of the store.",
  );
  await user.type(form.getByRole("textbox", { name: "Kind" }), "cloud-kms");
  await user.type(form.getByRole("textbox", { name: /Settings/ }), "not json");
  await user.click(register);
  expect(form.getByRole("alert").textContent).toBe(
    "The settings are not valid JSON.",
  );
  const settings = form.getByRole("textbox", { name: /Settings/ });
  await user.clear(settings);
  await user.type(settings, '{{"region": "eu"}');
  await user.type(form.getByLabelText("Credential"), "token");
  await user.click(register);
  // This installation has no such kind, and its API says which it has.
  expect((await form.findByRole("alert")).textContent).toBe(
    "Kind must be one of: kv-v2.",
  );
});

it("says which store holds the key of which provider", async () => {
  const mock = await backend();
  const projects = mock.request("GET", "/v1/projects", {
    token: "alice",
    body: null,
  }).body as { projects: { id: string; slug: string }[] };
  const storefront = projects.projects.find(
    (project) => project.slug === "storefront",
  )!;
  mock.request("POST", `/v1/projects/${storefront.id}/secret-stores`, {
    token: "alice",
    body: {
      name: "Spare",
      kind: "kv-v2",
      settings: {
        address: "https://secrets.example.com:8200",
        mount: "spare",
        role_id: "aviary",
      },
      credential: { value: "s3cret" },
    },
  });
  open("/p/storefront/connections", "alice");
  expect((await row("Spare")).textContent).toContain("Not used yet");
  expect((await row("Platform secrets")).textContent).toContain(
    "Holds the key of Local model server",
  );
});

it("lets only those who manage connections change them", async () => {
  open("/p/storefront/connections", "bob");
  await row("Company gateway");
  expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  expect(screen.queryByRole("button", { name: /Add provider/ })).toBeNull();
  expect(screen.queryByRole("button", { name: /Register store/ })).toBeNull();
  await leave();

  open("/p/internal-tools/connections", "bob");
  await row("Company gateway");
  expect(screen.getByRole("button", { name: "Edit" })).toBeTruthy();
  expect(screen.getByRole("button", { name: /Add provider/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: /Register store/ })).toBeTruthy();
});
