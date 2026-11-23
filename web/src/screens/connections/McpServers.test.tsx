// @vitest-environment happy-dom
import { screen, within } from "@testing-library/react";
import { afterEach, beforeAll, expect, it } from "vitest";
import { fast, leave, open, titled } from "../../testing.tsx";

beforeAll(fast);
afterEach(leave);

// rows returns the rows of the table of servers, once it shows.
async function rows() {
  await screen.findByRole("table");
  return [...document.querySelectorAll<HTMLElement>(".mcps tbody tr")];
}

// count returns what the table says of how many servers it shows.
const count = () => document.querySelector(".tb-h .end")!.textContent;

it("lists the MCP servers, and finds them by a tool", async () => {
  const user = open("/p/storefront/mcp-servers", "alice");
  const all = await rows();
  expect(all.length).toBe(24);
  expect(count()).toBe("24 servers");
  await titled("MCP servers · Storefront · Aviary");
  expect(all[0].textContent).toContain("Issue trackerissue-tracker");
  expect(all[0].textContent).toContain("tracker.example.com/mcp");
  expect(all[0].textContent).toContain("Streamable HTTP");

  // A server that is found by one of its tools says which.
  const find = screen.getByRole("textbox", { name: "Find a server or a tool" });
  await user.type(find, "job_logs");
  const found = await rows();
  expect(found.length).toBe(1);
  expect(found[0].textContent).toContain("Code hostTool get_job_logs");
  expect(count()).toBe("1 of 24");
  await user.type(find, "s and more");
  expect((await rows())[0].textContent).toBe(
    "No server or tool matches “job_logss and more”.",
  );

  // A row leads to its server.
  await user.clear(find);
  await user.click(screen.getByRole("link", { name: "Code host" }));
  expect(
    await screen.findByRole("heading", { name: /^Code host/ }),
  ).toBeTruthy();
});

it("shows the servers that no agent uses apart from the others", async () => {
  const user = open("/p/storefront/mcp-servers", "alice");
  await rows();
  await user.click(screen.getByRole("button", { name: "Not in use" }));
  const unused = await rows();
  expect(unused.length).toBeGreaterThan(0);
  for (const row of unused) expect(row.textContent).toContain("None");
  expect(count()).toBe(`${unused.length} of 24`);

  await user.click(screen.getByRole("button", { name: "In use" }));
  const used = await rows();
  expect(used.length).toBe(24 - unused.length);
  expect(used[0].querySelector(".stack")!.getAttribute("title")).toBe(
    "Backend engineer",
  );
});

it("adds an MCP server, after a test that lists its tools", async () => {
  const user = open("/p/storefront/mcp-servers", "alice");
  await rows();
  await user.click(screen.getByRole("button", { name: /Add MCP server/ }));
  const form = within(
    screen.getByRole("dialog", { name: "Add an MCP server" }),
  );
  await user.type(
    form.getByRole("textbox", { name: /^Name/ }),
    "Release notes",
  );
  expect(
    form.getByRole<HTMLInputElement>("textbox", { name: /^Slug/ }).value,
  ).toBe("release-notes");
  const test = form.getByRole("button", { name: "Test connection" });
  // tried returns what the dialog says of a try.
  const tried = async () => {
    await user.click(test);
    return (await form.findByRole("status")).textContent;
  };
  expect(await tried()).toBe("Enter the URL of the MCP server.");
  await user.type(
    form.getByRole("textbox", { name: "URL" }),
    "https://auth.example.com/mcp",
  );
  // The server wants a token.
  expect(await tried()).toBe("The server refused the request (401).");

  // A header of an MCP server is a secret that Aviary stores, unless it is
  // said otherwise.
  await user.click(form.getByRole("button", { name: "Add header" }));
  await user.type(
    form.getByRole("combobox", { name: "Header name" }),
    "authorization",
  );
  expect(
    form.getByRole<HTMLSelectElement>("combobox", {
      name: "Where the value comes from",
    }).value,
  ).toBe("stored");
  await user.type(form.getByLabelText("Header value"), "Bearer t0ken");
  expect(await tried()).toBe("Connected in 230 ms · 1 tool");
  expect(form.getByRole("list", { name: "Tools" }).textContent).toBe("echo");

  // The other transport is not served at that URL.
  await user.click(form.getByRole("button", { name: "SSE" }));
  expect(await tried()).toBe(
    "No MCP server answers SSE at this URL (405). Try Streamable HTTP?",
  );
  await user.click(form.getByRole("button", { name: "Streamable HTTP" }));
  await user.click(form.getByRole("button", { name: "Add server" }));

  const added = (await rows()).find((row) =>
    row.textContent!.includes("Release notes"),
  )!;
  expect(added.textContent).toContain("auth.example.com/mcp");
  expect(added.textContent).toContain("None");
  expect(screen.getByRole("status").textContent).toContain(
    "Added Release notes",
  );
});

it("shows a server with its tools, who uses it and its headers", async () => {
  const user = open("/p/storefront/mcp-servers/code-host", "alice");
  expect(
    (await screen.findByRole("heading", { name: /^Code host/ })).textContent,
  ).toBe("Code hostcode-host");
  await titled("Code host · Storefront · Aviary");
  const tools = () => [...document.querySelectorAll(".tool-list li")];
  expect(tools().length).toBe(43);
  // A server of many tools can be searched for one.
  await user.type(
    screen.getByRole("textbox", { name: "Find a tool" }),
    "pull_request",
  );
  expect(
    tools().every((tool) => tool.textContent!.includes("pull_request")),
  ).toBe(true);
  expect(
    [...document.querySelectorAll(".srv-side li")].map(
      (item) => item.textContent,
    ),
  ).toEqual(["Backend engineer", "Reviewer", "authorizationPlatform secrets"]);

  await user.click(screen.getByRole("button", { name: "Refresh tools" }));
  expect(
    (await screen.findByText("Connected in 230 ms · 43 tools")).className,
  ).toBe("try-r ok");
});

it("keeps the secrets of a server while its URL stays the same", async () => {
  const user = open("/p/storefront/mcp-servers/issue-tracker", "alice");
  await user.click(await screen.findByRole("button", { name: "Edit" }));
  const form = within(
    screen.getByRole("dialog", { name: "Edit Issue tracker" }),
  );
  expect(
    form.getByRole<HTMLInputElement>("textbox", { name: "Slug" }).disabled,
  ).toBe(true);
  expect(form.getByText("Stored")).toBeTruthy();
  await user.click(form.getByRole("button", { name: "Test connection" }));
  expect((await form.findByRole("status")).textContent).toBe(
    "Connected in 230 ms · 5 tools",
  );
  const url = form.getByRole("textbox", { name: "URL" });
  await user.type(url, "/");
  expect(
    form.getByText("The URL changed: enter the stored values again."),
  ).toBeTruthy();
  await user.click(form.getByRole("button", { name: "Save server" }));
  expect(form.getByRole("alert").textContent).toBe(
    "Enter the value of authorization again.",
  );
  await user.type(form.getByLabelText("Header value"), "Bearer n3w");
  await user.click(form.getByRole("button", { name: "Save server" }));
  expect(await screen.findByText("Saved Issue tracker")).toBeTruthy();
});

it("says when a server answers, but not as an MCP server does", async () => {
  const user = open("/p/storefront/mcp-servers", "alice");
  await rows();
  await user.click(screen.getByRole("button", { name: /Add MCP server/ }));
  const form = within(
    screen.getByRole("dialog", { name: "Add an MCP server" }),
  );
  await user.type(
    form.getByRole("textbox", { name: "URL" }),
    "https://www.example.com/mcp",
  );
  await user.click(form.getByRole("button", { name: "Test connection" }));
  const said = await form.findByRole("status");
  expect(said.textContent).toBe(
    "The server does not answer as an MCP server does.",
  );
  expect(said.className).toBe("try-r bad");
  expect(form.queryByRole("list", { name: "Tools" })).toBeNull();
});

it("deletes a server, which its agents no longer use", async () => {
  const user = open("/p/storefront/mcp-servers/ci", "alice");
  await screen.findByRole("heading", { name: /^CI pipelines/ });
  expect(
    [...document.querySelectorAll(".srv-side .side-list a")].map(
      (link) => link.textContent,
    ),
  ).toEqual(["Backend engineer", "Reviewer"]);
  await user.click(screen.getByRole("button", { name: "More" }));
  await user.click(screen.getByRole("menuitem", { name: "Delete server…" }));
  const dialog = within(
    screen.getByRole("dialog", { name: "Delete CI pipelines?" }),
  );
  await user.click(dialog.getByRole("button", { name: "Delete server" }));
  expect(await screen.findByText("23 servers")).toBeTruthy();
  expect(screen.getByRole("status").textContent).toContain(
    "Deleted CI pipelines",
  );
  expect(screen.queryByRole("link", { name: "CI pipelines" })).toBeNull();

  await user.click(screen.getByRole("link", { name: /^Agents/ }));
  const reviewer = await screen.findByRole("link", { name: /Reviewer/ });
  expect(reviewer.textContent).toContain("Code host");
  expect(reviewer.textContent).not.toContain("CI pipelines");
});

it("lets only those who manage connections change MCP servers", async () => {
  open("/p/storefront/mcp-servers", "bob");
  await rows();
  expect(screen.queryByRole("button", { name: /Add MCP server/ })).toBeNull();
  await leave();

  open("/p/storefront/mcp-servers/issue-tracker", "bob");
  await screen.findByRole("heading", { name: /^Issue tracker/ });
  expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Refresh tools" })).toBeNull();
  expect(screen.queryByRole("button", { name: "More" })).toBeNull();
});
