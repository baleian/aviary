// @vitest-environment happy-dom
import { screen, within } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";
import { afterEach, beforeAll, expect, it } from "vitest";
import { backend } from "../../api/fake.ts";
import { fast, leave, open, titled } from "../../testing.tsx";

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
  // A provider is listed by its name and its URL: where its key is in the
  // store is not in the list.
  const local = await row("Local model server");
  expect(local.querySelector(".tx")!.textContent).toBe(
    "Local model serverhttp://10.0.0.5:8000/v1",
  );
  expect(local.textContent).toContain("Used byDocs writer");
  expect(local.textContent).toContain("Key in Platform secrets");
  const store = await row("Platform secrets");
  expect(store.textContent).toContain(
    "https://secrets.example.com:8200 · secret",
  );
  expect(store.textContent).toContain("Holds the key of Local model server");
  expect(store.textContent).toContain("kv-v2Credential stored");
  await titled("Connections · Storefront · Aviary");
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
  expect(within(shared).getByTitle("team/model → api_key").textContent).toBe(
    "Key in Platform secrets",
  );
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

it("tries a provider with what its dialog holds, and adds nothing", async () => {
  const user = open("/p/storefront/connections", "alice");
  await row("Company gateway");
  const listed = document.querySelectorAll(".clist li").length;
  const form = await dialog(user, /Add provider/, "Add a model provider");
  const test = form.getByRole("button", { name: "Test connection" });
  // tried returns what the dialog says of the last try.
  const tried = async () => {
    await user.click(test);
    return await form.findByRole("status");
  };
  // The endpoint is tried without a name, but not without its URL and key.
  let said = await tried();
  expect(said.textContent).toBe("Enter the URL of the model endpoint.");
  expect(said.className).toBe("try-r bad");
  const url = form.getByRole("textbox", { name: "URL" });
  await user.type(url, "http://localhost:11434/v1");
  expect((await tried()).textContent).toBe("Enter the key.");
  const key = form.getByLabelText("API key");
  await user.type(key, "sk-lab");
  said = await tried();
  expect(said.textContent).toBe("Connected in 120 ms.");
  expect(said.className).toBe("try-r ok");

  await user.clear(key);
  await user.type(key, "wrong-key");
  said = await tried();
  expect(said.textContent).toBe("The endpoint refused the key (401).");
  expect(said.className).toBe("try-r bad");

  await user.click(form.getByRole("radio", { name: "No key" }));
  await user.clear(url);
  await user.type(url, "https://lab.example.com/none");
  said = await tried();
  expect(said.textContent).toBe(
    "Reached. The endpoint lists no models at this URL (404).",
  );
  expect(said.className).toBe("try-r");

  await user.clear(url);
  await user.type(url, "https://lab.invalid/v1");
  said = await tried();
  expect(said.textContent).toBe(
    "Not reached: upstream connect error or disconnect/reset before headers. reset reason: connection timeout.",
  );
  expect(said.className).toBe("try-r bad");

  // What the API refuses of the URL shows the same way.
  await user.clear(url);
  await user.type(url, "lab.example.com");
  expect((await tried()).textContent).toBe(
    "Url must be a URL that begins with http:// or https://.",
  );

  expect(screen.getByRole("dialog")).toBeTruthy();
  await user.click(form.getByRole("button", { name: "Cancel" }));
  expect(document.querySelectorAll(".clist li").length).toBe(listed);
});

it("shows that a provider is being tried, without what the last try said", async () => {
  const user = open("/p/storefront/connections", "alice");
  const form = await dialog(user, /Add provider/, "Add a model provider");
  const test = form.getByRole("button", { name: "Test connection" });
  await user.type(
    form.getByRole("textbox", { name: "URL" }),
    "https://lab.invalid/v1",
  );
  await user.click(form.getByRole("radio", { name: "No key" }));
  await user.click(test);
  expect((await form.findByRole("status")).textContent).toContain(
    "Not reached",
  );

  // The backend takes a moment to answer, as an API does.
  const { mock } = await backend();
  mock.speed = 1;
  try {
    await user.click(test);
    expect(form.queryByRole("status")).toBeNull();
    expect((test as HTMLButtonElement).disabled).toBe(true);
    expect(test.querySelector(".ring")).toBeTruthy();
    expect((await form.findByRole("status")).textContent).toContain(
      "Not reached",
    );
    expect((test as HTMLButtonElement).disabled).toBe(false);
    expect(test.querySelector(".ring")).toBeNull();
  } finally {
    mock.speed = 0;
  }
});

it("tries a provider that is edited with the key entered again", async () => {
  const user = open("/p/storefront/connections", "alice");
  await user.click(
    within(await row("Company gateway")).getByRole("button", { name: "Edit" }),
  );
  const form = within(
    screen.getByRole("dialog", { name: "Edit Company gateway" }),
  );
  const test = form.getByRole("button", { name: "Test connection" });
  await user.click(test);
  expect((await form.findByRole("status")).textContent).toBe(
    "Enter the key again.",
  );
  await user.type(form.getByLabelText("API key"), "sk-gateway");
  await user.click(test);
  expect((await form.findByText("Connected in 120 ms.")).className).toBe(
    "try-r ok",
  );
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

it("gives a provider the headers that its endpoint needs", async () => {
  const user = open("/p/storefront/connections", "alice");
  const form = await dialog(user, /Add provider/, "Add a model provider");
  await user.type(form.getByRole("textbox", { name: "Name" }), "Routed");
  await user.type(
    form.getByRole("textbox", { name: "URL" }),
    "https://routed.example.com/v1",
  );
  await user.click(form.getByRole("radio", { name: "No key" }));
  // tried returns what the dialog says of a try.
  const tried = async () => {
    await user.click(form.getByRole("button", { name: "Test connection" }));
    return (await form.findByRole("status")).textContent;
  };
  // The endpoint routes its requests by a header, and refuses one without.
  expect(await tried()).toBe("The endpoint answered 400.");

  // The headers are among the settings that few providers need.
  expect(form.queryByRole("textbox", { name: "Header name" })).toBeNull();
  await user.click(form.getByText("Advanced"));
  const add = form.getByRole("button", { name: "Add header" });
  await user.click(add);
  const name = form.getByRole("textbox", { name: "Header name" });
  expect(document.activeElement).toBe(name);
  await user.type(name, "X-Gateway-Route");
  expect(await tried()).toBe("Enter the name and the value of each header.");
  await user.type(
    form.getByRole("textbox", { name: "Header value" }),
    "team-a",
  );
  // A try carries the headers that the dialog holds.
  expect(await tried()).toBe("Connected in 120 ms.");

  // A row that is left empty is no header, and neither is one that is
  // removed.
  await user.click(add);
  await user.click(add);
  await user.type(
    form.getAllByRole("textbox", { name: "Header name" })[2],
    "x-other",
  );
  await user.type(
    form.getAllByRole("textbox", { name: "Header value" })[2],
    "gone",
  );
  await user.click(form.getAllByRole("button", { name: "Remove header" })[2]);
  expect(form.getAllByRole("textbox", { name: "Header name" })).toHaveLength(2);
  await user.click(form.getByRole("button", { name: "Add provider" }));
  // The list of providers does not show their headers.
  const routed = await row("Routed");
  expect(routed.textContent).not.toContain("x-gateway-route");

  // A provider that is edited has its headers in the dialog, which shows
  // them.
  await user.click(within(routed).getByRole("button", { name: "Edit" }));
  const edit = within(screen.getByRole("dialog", { name: "Edit Routed" }));
  expect(document.querySelector<HTMLDetailsElement>("dialog .adv")!.open).toBe(
    true,
  );
  const kept = edit.getByRole<HTMLInputElement>("textbox", {
    name: "Header name",
  });
  expect(kept.value).toBe("x-gateway-route");
  expect(
    edit.getByRole<HTMLInputElement>("textbox", { name: "Header value" }).value,
  ).toBe("team-a");
  // What the API refuses of a header shows in the dialog.
  const save = edit.getByRole("button", { name: "Save provider" });
  await user.clear(kept);
  await user.type(kept, "Authorization");
  await user.click(save);
  expect((await edit.findByRole("alert")).textContent).toBe(
    "Headers[0].name must not be authorization; set the endpoint's key in api_key.",
  );
  // Saved without its header, the provider has none.
  await user.click(edit.getByRole("button", { name: "Remove header" }));
  await user.click(save);
  expect(await screen.findByText(/Saved Routed/)).toBeTruthy();
  await user.click(
    within(await row("Routed")).getByRole("button", { name: "Edit" }),
  );
  expect(
    within(screen.getByRole("dialog", { name: "Edit Routed" })).queryByRole(
      "textbox",
      { name: "Header name" },
    ),
  ).toBeNull();
  expect(document.querySelector<HTMLDetailsElement>("dialog .adv")!.open).toBe(
    false,
  );
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
