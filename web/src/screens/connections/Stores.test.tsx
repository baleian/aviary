// @vitest-environment happy-dom
import { screen, within } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";
import { afterEach, beforeAll, expect, it } from "vitest";
import { backend } from "../../api/fake.ts";
import { fast, leave, open, titled } from "../../testing.tsx";

beforeAll(fast);
afterEach(leave);

// row returns the row of the store with the name, once it shows.
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

it("lists the secret stores, and whose secrets each holds", async () => {
  open("/p/storefront/secret-stores", "alice");
  const store = await row("Platform secrets");
  expect(store.textContent).toContain(
    "https://secrets.example.com:8200 · secret",
  );
  // The provider whose key is in the store comes first, then the MCP
  // servers whose headers take a secret of it.
  const used = store.querySelector(".used")!;
  expect(used.textContent).toMatch(
    /^Holds the secrets of Local model server, Code host and \d+ more$/,
  );
  expect(used.getAttribute("title")).toContain("Local model server, Code host");
  expect(store.textContent).toContain("kv-v2Credential stored");
  await titled("Secret stores · Storefront · Aviary");
  expect(screen.getByRole("link", { name: /^Secret stores/ }).className).toBe(
    "on",
  );
});

it("registers a secret store, and replaces it whole", async () => {
  const user = open("/p/data-platform/secret-stores", "alice");
  expect(await screen.findByText("No secret stores yet")).toBeTruthy();
  let form = await dialog(user, /Register store/, "Register a secret store");
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
  const user = open("/p/storefront/secret-stores", "alice");
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

it("says that a store which holds no secret is not used yet", async () => {
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
  open("/p/storefront/secret-stores", "alice");
  expect((await row("Spare")).textContent).toContain("Not used yet");
  expect((await row("Platform secrets")).textContent).toContain(
    "Holds the secrets of Local model server",
  );
});

it("lets only those who manage connections change stores", async () => {
  open("/p/storefront/secret-stores", "bob");
  await row("Platform secrets");
  expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  expect(screen.queryByRole("button", { name: /Register store/ })).toBeNull();
  await leave();

  open("/p/internal-tools/secret-stores", "bob");
  expect(await screen.findByText("No secret stores yet")).toBeTruthy();
  expect(screen.getByRole("button", { name: /Register store/ })).toBeTruthy();
});
