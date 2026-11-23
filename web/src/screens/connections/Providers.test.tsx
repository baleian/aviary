// @vitest-environment happy-dom
import { screen, within } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";
import { afterEach, beforeAll, expect, it } from "vitest";
import { backend } from "../../api/fake.ts";
import { fast, leave, open, titled } from "../../testing.tsx";

beforeAll(fast);
afterEach(leave);

// row returns the row of the provider with the name, once it shows.
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

// edit opens the dialog of the provider with the name.
async function edit(user: UserEvent, name: string) {
  await user.click(
    within(await row(name)).getByRole("button", { name: "Edit" }),
  );
  return within(screen.getByRole("dialog", { name: "Edit " + name }));
}

it("lists the model providers, and where their keys are", async () => {
  open("/p/storefront/providers", "alice");
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
  await titled("Model providers · Storefront · Aviary");
  expect(screen.getByRole("link", { name: /^Model providers/ }).className).toBe(
    "on",
  );
});

it("adds a provider whose key Aviary keeps", async () => {
  const user = open("/p/storefront/providers", "alice");
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
  const user = open("/p/storefront/providers", "alice");
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

it("cannot take a key from a secret store in a project without one", async () => {
  const user = open("/p/data-platform/providers", "alice");
  const form = await dialog(user, /Add provider/, "Add a model provider");
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
});

it("tries a provider with what its dialog holds, and adds nothing", async () => {
  const user = open("/p/storefront/providers", "alice");
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
  const user = open("/p/storefront/providers", "alice");
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

it("keeps the key of a provider while its URL stays the same", async () => {
  const user = open("/p/storefront/providers", "alice");
  let form = await edit(user, "Company gateway");
  const kept = form.getByRole("radio", { name: "Store the key in Aviary" });
  expect((kept as HTMLInputElement).checked).toBe(true);
  expect(form.queryByLabelText("API key")).toBeNull();
  // A try uses the key that Aviary stores.
  await user.click(form.getByRole("button", { name: "Test connection" }));
  expect((await form.findByRole("status")).textContent).toBe(
    "Connected in 120 ms.",
  );
  const name = form.getByRole("textbox", { name: "Name" });
  await user.clear(name);
  await user.type(name, "Gateway");
  await user.click(form.getByRole("button", { name: "Save provider" }));
  expect((await row("Gateway")).textContent).toContain("Key stored");
  expect(screen.getByRole("status").textContent).toContain("Saved Gateway");

  // A key is not sent to a URL that it was not stored for.
  form = await edit(user, "Gateway");
  const url = form.getByRole("textbox", { name: "URL" });
  await user.clear(url);
  await user.type(url, "https://llm2.example.com/v1");
  expect(form.getByText("The URL changed: enter the key again.")).toBeTruthy();
  const save = form.getByRole("button", { name: "Save provider" });
  await user.click(save);
  expect(form.getByRole("alert").textContent).toBe("Enter the key again.");
  await user.type(form.getByLabelText("API key"), "sk-gateway");
  await user.click(save);
  expect((await row("Gateway")).textContent).toContain(
    "https://llm2.example.com/v1",
  );
});

it("replaces the key of a provider that Aviary stores, or drops it", async () => {
  const user = open("/p/storefront/providers", "alice");
  let form = await edit(user, "Company gateway");
  await user.click(form.getByRole("button", { name: "Replace" }));
  const key = form.getByLabelText("API key");
  expect(document.activeElement).toBe(key);
  await user.type(key, "wrong-key");
  await user.click(form.getByRole("button", { name: "Test connection" }));
  expect((await form.findByRole("status")).textContent).toBe(
    "The endpoint refused the key (401).",
  );
  await user.click(form.getByRole("radio", { name: "No key" }));
  await user.click(form.getByRole("button", { name: "Save provider" }));
  expect((await row("Company gateway")).textContent).toContain("No key");

  form = await edit(user, "Company gateway");
  expect(
    form.getByRole<HTMLInputElement>("radio", { name: "No key" }).checked,
  ).toBe(true);
});

it("gives a provider the headers that its endpoint needs", async () => {
  const user = open("/p/storefront/providers", "alice");
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
  expect(await tried()).toBe("Enter the value of X-Gateway-Route.");
  // A header of a provider is plain text unless it is said otherwise.
  expect(
    form.getByRole<HTMLSelectElement>("combobox", {
      name: "Where the value comes from",
    }).value,
  ).toBe("value");
  expect(form.getByText(/can see plain text values/)).toBeTruthy();
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
  const edited = await edit(user, "Routed");
  expect(document.querySelector<HTMLDetailsElement>("dialog .adv")!.open).toBe(
    true,
  );
  const kept = edited.getByRole<HTMLInputElement>("textbox", {
    name: "Header name",
  });
  expect(kept.value).toBe("x-gateway-route");
  expect(
    edited.getByRole<HTMLInputElement>("textbox", { name: "Header value" })
      .value,
  ).toBe("team-a");
  // What the API refuses of a header shows in the dialog.
  const save = edited.getByRole("button", { name: "Save provider" });
  await user.clear(kept);
  await user.type(kept, "Authorization");
  await user.click(save);
  expect((await edited.findByRole("alert")).textContent).toBe(
    "Headers[0].name must not be authorization; set the endpoint's key in api_key.",
  );
  // Saved without its header, the provider has none.
  await user.click(edited.getByRole("button", { name: "Remove header" }));
  await user.click(save);
  expect(await screen.findByText(/Saved Routed/)).toBeTruthy();
  const again = await edit(user, "Routed");
  expect(again.queryByRole("textbox", { name: "Header name" })).toBeNull();
  expect(document.querySelector<HTMLDetailsElement>("dialog .adv")!.open).toBe(
    false,
  );
});

it("keeps a header value that Aviary stores, and takes one from a store", async () => {
  const user = open("/p/storefront/providers", "alice");
  const form = await dialog(user, /Add provider/, "Add a model provider");
  await user.type(form.getByRole("textbox", { name: "Name" }), "Routed");
  await user.type(
    form.getByRole("textbox", { name: "URL" }),
    "https://routed.example.com/v1",
  );
  await user.click(form.getByRole("radio", { name: "No key" }));
  await user.click(form.getByText("Advanced"));
  const add = form.getByRole("button", { name: "Add header" });
  await user.click(add);
  await user.type(
    form.getByRole("textbox", { name: "Header name" }),
    "x-gateway-route",
  );
  const sources = () =>
    form.getAllByRole("combobox", { name: "Where the value comes from" });
  await user.selectOptions(sources()[0], "Stored in Aviary");
  // A value that Aviary stores is entered as a secret, and not shown.
  await user.type(form.getByLabelText("Header value"), "team-a");
  expect(form.queryByText(/can see plain text values/)).toBeNull();
  await user.click(add);
  await user.type(
    form.getAllByRole("textbox", { name: "Header name" })[1],
    "x-team-token",
  );
  await user.selectOptions(sources()[1], "In a secret store");
  await user.click(form.getByRole("button", { name: "Add provider" }));
  expect(form.getByRole("alert").textContent).toBe(
    "Enter the path and the key of the secret of x-team-token.",
  );
  await user.type(
    form.getByRole("textbox", { name: "Path of the secret" }),
    "team/model",
  );
  await user.type(
    form.getByRole("textbox", { name: "Key of the secret" }),
    "token",
  );
  await user.click(form.getByRole("button", { name: "Add provider" }));
  await row("Routed");

  // The stored value stays while the URL does.
  const edited = await edit(user, "Routed");
  expect(edited.getByText("Stored")).toBeTruthy();
  expect(edited.queryByLabelText("Header value")).toBeNull();
  expect(
    edited.getByRole<HTMLInputElement>("textbox", {
      name: "Path of the secret",
    }).value,
  ).toBe("team/model");
  await user.click(edited.getByRole("button", { name: "Test connection" }));
  expect((await edited.findByRole("status")).textContent).toBe(
    "Connected in 120 ms.",
  );
  const url = edited.getByRole("textbox", { name: "URL" });
  await user.type(url, "/");
  expect(
    edited.getByText("The URL changed: enter the stored values again."),
  ).toBeTruthy();
  await user.click(edited.getByRole("button", { name: "Save provider" }));
  expect(edited.getByRole("alert").textContent).toBe(
    "Enter the value of x-gateway-route again.",
  );
});

it("opens a provider with its key in a store at that store", async () => {
  const user = open("/p/storefront/providers", "alice");
  const form = await edit(user, "Local model server");
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

it("lets only those who manage connections change providers", async () => {
  open("/p/storefront/providers", "bob");
  await row("Company gateway");
  expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  expect(screen.queryByRole("button", { name: /Add provider/ })).toBeNull();
  await leave();

  open("/p/internal-tools/providers", "bob");
  await row("Company gateway");
  expect(screen.getByRole("button", { name: "Edit" })).toBeTruthy();
  expect(screen.getByRole("button", { name: /Add provider/ })).toBeTruthy();
});
