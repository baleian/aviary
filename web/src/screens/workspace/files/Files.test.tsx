// @vitest-environment happy-dom
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, beforeEach, expect, it, vi } from "vitest";
import { api, ApiError } from "../../../api/client.ts";
import { backend } from "../../../api/fake.ts";
import { fast, leave, open, sized } from "../../../testing.tsx";

beforeAll(fast);
// The tree draws the rows that are in view, and happy-dom lays nothing out:
// the tree is as tall as a test says.
let tall = 840;
beforeEach(() => {
  tall = 840;
  Object.defineProperty(HTMLElement.prototype, "clientHeight", {
    configurable: true,
    get: () => tall,
  });
});
afterEach(async () => {
  Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
  vi.restoreAllMocks();
  await leave();
});

const BACKEND = "/p/storefront/agents/backend-engineer";
// A session of the example data whose agent has worked, and whose sandbox
// sleeps.
const TRIAGE = "Triage last night's checkout errors";

// as returns what answers a request of the account to the backend.
async function as(account: string) {
  const mock = await backend();
  return <T,>(method: string, path: string, body: unknown = null) => {
    const reply = mock.request(method, path, { token: account, body });
    return reply.body as T;
  };
}

// agentOf returns the ID of an agent of Storefront: of the backend engineer,
// unless another is named.
async function agentOf(slug = "backend-engineer") {
  const request = await as("alice");
  const { projects } = request<{ projects: { id: string; slug: string }[] }>(
    "GET",
    "/v1/projects",
  );
  const storefront = projects.find((each) => each.slug === "storefront")!;
  const { agents } = request<{ agents: { id: string; slug: string }[] }>(
    "GET",
    `/v1/projects/${storefront.id}/agents`,
  );
  return agents.find((each) => each.slug === slug)!.id;
}

// sessionOf returns the ID of the session of the backend engineer with the
// title.
async function sessionOf(title: string) {
  const request = await as("alice");
  const { sessions } = request<{ sessions: { id: string; title: string }[] }>(
    "GET",
    `/v1/agents/${await agentOf()}/sessions?limit=100`,
  );
  return sessions.find((each) => each.title === title)!.id;
}

const pathOf = (id: string) => `${BACKEND}/s/${id}`;

// files shows the files of the session to the account, and returns the
// person who uses the web UI.
async function files(id: string, account = "alice") {
  const user = open(pathOf(id), account);
  const button = await screen.findByRole("button", { name: "Files" });
  // The pane shows already if it showed when the web UI was last open.
  if (button.getAttribute("aria-pressed") !== "true") await user.click(button);
  return user;
}

// names returns the names of the rows of the tree, once it has some.
async function names() {
  await screen.findAllByRole("treeitem");
  return rows().map((row) => row.querySelector(".nm")!.textContent);
}

const rows = () => screen.queryAllByRole("treeitem");

// row returns the row of the tree with the name.
const row = (name: string) =>
  rows().find((each) => each.querySelector(".nm")!.textContent === name)!;

// act chooses what the menu of a row offers.
async function act(
  user: ReturnType<typeof open>,
  name: string,
  what: string | RegExp,
) {
  await user.click(row(name).querySelector('button[title="More"]')!);
  await user.click(screen.getByRole("menuitem", { name: what }));
}

// saved returns the names under which the browser was told to save files.
function saved() {
  const told: string[] = [];
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    told.push(this.download);
  });
  return told;
}

// picker returns the input with which files are picked.
const picker = () =>
  document.querySelector<HTMLInputElement>('.ft input[type="file"][multiple]')!;

// pick picks the files, as a person does in the dialog of the browser.
function pick(...picked: File[]) {
  fireEvent.change(picker(), { target: { files: picked } });
}

it("lists the workspace of a session beside its conversation, the directories first", async () => {
  const id = await sessionOf(TRIAGE);
  const user = await files(id);
  const listed = await names();
  expect(listed.slice(0, 5)).toEqual([
    ".git",
    ".github",
    "coverage",
    "data",
    "docs",
  ]);
  expect(listed.slice(13, 16)).toEqual([
    ".env.example",
    ".gitignore",
    "Dockerfile",
  ]);
  expect(row("src").getAttribute("aria-expanded")).toBe("false");
  expect(row("README.md").getAttribute("aria-expanded")).toBeNull();
  // The files take the place of the details.
  expect(
    screen.getByRole("button", { name: "Files" }).getAttribute("aria-pressed"),
  ).toBe("true");
  expect(document.querySelector(".pane.details")).toBeNull();
  // Reading them has woken the sandbox.
  const request = await as("alice");
  expect(
    request<{ sandbox: string }>("GET", "/v1/sessions/" + id).sandbox,
  ).toBe("awake");

  // A directory opens in place, and closes again.
  await user.click(row("src"));
  await waitFor(() => expect(row("checkout")).toBeTruthy());
  expect(row("src").getAttribute("aria-expanded")).toBe("true");
  expect(row("checkout").getAttribute("aria-level")).toBe("2");
  await user.click(row("src"));
  expect(row("checkout")).toBeUndefined();

  // The pane closes, and the choice is kept.
  await user.click(screen.getByRole("button", { name: "Files" }));
  expect(screen.queryByRole("tree")).toBeNull();
  cleanup();
  open(pathOf(id), "alice");
  await screen.findByRole("button", { name: "Files" });
  expect(document.querySelector(".pane")).not.toBeNull();
  expect(screen.queryByRole("tree")).toBeNull();
  expect(document.querySelector(".pane.details")).toBeNull();
});

it("reads a long directory a part at a time", async () => {
  tall = 28 * 400;
  const user = await files(await sessionOf(TRIAGE));
  await names();
  await user.click(row("logs"));
  await waitFor(() => expect(screen.getByText("Show more")).toBeTruthy());
  const logs = () =>
    rows().filter((each) => each.getAttribute("aria-level") === "2").length;
  expect(logs()).toBe(200);
  await user.click(screen.getByText("Show more"));
  await waitFor(() => expect(logs()).toBe(320));
  expect(screen.queryByText("Show more")).toBeNull();
  // An empty directory says so.
  await user.click(row("logs"));
  await user.click(row("coverage"));
  await waitFor(() => expect(screen.getByText("Empty")).toBeTruthy());
});

it("moves through the tree with the keys", async () => {
  const user = await files(await sessionOf(TRIAGE));
  await names();
  const tree = screen.getByRole("tree");
  tree.focus();
  const current = () =>
    document.querySelector(".ft-row.cur .nm")?.textContent ?? null;
  await user.keyboard("{ArrowDown}");
  expect(current()).toBe(".git");
  await user.keyboard("{ArrowDown}{ArrowDown}{ArrowDown}");
  expect(current()).toBe("data");
  // Right opens a directory and then enters it, and left leaves it and
  // then closes it.
  await user.keyboard("{ArrowRight}");
  await waitFor(() => expect(row("events.parquet")).toBeTruthy());
  await user.keyboard("{ArrowRight}");
  expect(row(current()!).getAttribute("aria-level")).toBe("2");
  await user.keyboard("{ArrowLeft}");
  expect(current()).toBe("data");
  await user.keyboard("{ArrowLeft}");
  expect(row("events.parquet")).toBeUndefined();
  // Enter opens a file.
  await user.keyboard("{End}");
  expect(current()).toBe("tsconfig.json");
  await user.keyboard("{Enter}");
  expect(
    (await screen.findByRole("tab", { name: /tsconfig\.json/ })).getAttribute(
      "aria-selected",
    ),
  ).toBe("true");
});

it("opens files in tabs, each as what it makes or as it is written", async () => {
  const user = await files(await sessionOf(TRIAGE));
  await names();
  await user.click(row("README.md"));
  // A Markdown file shows as the document that it makes.
  const view = () => document.querySelector<HTMLElement>(".fv")!;
  await waitFor(() =>
    expect(view().querySelector(".fv-doc h1")?.textContent).toBe("storefront"),
  );
  expect(row("README.md").getAttribute("aria-selected")).toBe("true");
  // And as it is written, on request.
  await user.click(screen.getByRole("button", { name: "Source" }));
  await waitFor(() =>
    expect(view().querySelector(".cm-content")?.textContent).toContain(
      "# storefront",
    ),
  );
  expect(view().querySelector(".fv-doc")).toBeNull();

  // Another file gets a tab of its own, and the first keeps how it showed.
  await user.click(row("package.json"));
  await waitFor(() =>
    expect(view().querySelector(".cm-content")?.textContent).toContain(
      '"name": "storefront"',
    ),
  );
  const tabs = () =>
    screen
      .getAllByRole("tab")
      .map((tab) => tab.querySelector(".nm")!.textContent);
  expect(tabs()).toEqual(["README.md", "package.json"]);
  expect(screen.queryByRole("button", { name: "Source" })).toBeNull();
  await user.click(screen.getByRole("tab", { name: /README\.md/ }));
  await waitFor(() =>
    expect(view().querySelector(".cm-content")?.textContent).toContain(
      "# storefront",
    ),
  );

  // A tab that closes leaves the next in view, and the last leaves the
  // tree alone.
  await user.click(screen.getByRole("button", { name: "Close README.md" }));
  expect(tabs()).toEqual(["package.json"]);
  expect(
    screen
      .getByRole("tab", { name: /package\.json/ })
      .getAttribute("aria-selected"),
  ).toBe("true");
  await user.click(screen.getByRole("button", { name: "Close package.json" }));
  expect(document.querySelector(".fv")).toBeNull();
  expect(screen.getByRole("tree")).toBeTruthy();
});

it("shows the beginning of a long text, and saves what it cannot show", async () => {
  const told = saved();
  const user = await files(await sessionOf(TRIAGE));
  await names();
  await user.click(row("data"));
  await waitFor(() => expect(row("checkout-errors.log")).toBeTruthy());
  await user.click(row("checkout-errors.log"));
  await waitFor(() =>
    expect(document.querySelector(".fv-cut")?.textContent).toBe(
      "The first 1 MB of 1.8 MB.",
    ),
  );
  // What is not text has no preview.
  await user.click(row("events.parquet"));
  const none = await waitFor(() => {
    const shown = document.querySelector<HTMLElement>(".fv-none");
    expect(shown?.textContent).toContain("No preview");
    return shown!;
  });
  await user.click(none.querySelector("button")!);
  await waitFor(() => expect(told).toEqual(["events.parquet"]));

  // A directory is saved as a ZIP file, and so is the workspace.
  await act(user, "docs", "Download as ZIP");
  await waitFor(() => expect(told).toEqual(["events.parquet", "docs.zip"]));
  await act(user, "Dockerfile", "Download");
  await waitFor(() => expect(told.at(-1)).toBe("Dockerfile"));
  await user.click(screen.getByRole("button", { name: "Download all as ZIP" }));
  await waitFor(() => expect(told.at(-1)).toBe("workspace.zip"));
});

it("shows a page that the agent wrote apart from the web UI", async () => {
  // happy-dom loads no page into a frame, and says so on a console that is
  // not that of the tests.
  const { stderr } = (
    globalThis as unknown as {
      process: { stderr: { write(...told: unknown[]): boolean } };
    }
  ).process;
  const write = stderr.write;
  vi.spyOn(stderr, "write").mockImplementation((...told) =>
    String(told[0]).includes("Iframe page loading is disabled")
      ? true
      : Reflect.apply(write, stderr, told),
  );
  const user = await files(await sessionOf(TRIAGE));
  await names();
  await user.click(row("reports"));
  await waitFor(() => expect(row("checkout-errors.html")).toBeTruthy());
  await user.click(row("checkout-errors.html"));
  const page = await waitFor(() => {
    const frame = document.querySelector<HTMLIFrameElement>(".fv-page");
    expect(frame).not.toBeNull();
    return frame!;
  });
  // The page shows in a frame without an origin, which has nothing of the
  // caller's, and not as the document of a frame of the web UI.
  expect(page.getAttribute("sandbox")).toBe("allow-scripts");
  expect(page.getAttribute("src")).toBe("/sandbox.html");
  expect(page.getAttribute("srcdoc")).toBeNull();
  // The frame is told the page once it has loaded.
  const told = vi.fn();
  Object.defineProperty(page, "contentWindow", {
    configurable: true,
    value: { postMessage: told },
  });
  fireEvent.load(page);
  expect(told).toHaveBeenCalledTimes(1);
  expect(told.mock.calls[0][0].page).toContain("<title>Checkout errors");
  expect(told.mock.calls[0][1]).toBe("*");
  // A document of the workspace opens the files that it links to.
  await user.click(row("summary.md"));
  await waitFor(() =>
    expect(document.querySelector(".fv-doc h1")?.textContent).toContain(
      "Checkout errors",
    ),
  );
});

it("shows the pictures that a document names, and opens the files that it links to", async () => {
  const user = await files(await sessionOf(TRIAGE));
  await names();
  await user.click(row("README.md"));
  const doc = await waitFor(() => {
    const shown = document.querySelector<HTMLElement>(".fv-doc");
    expect(shown?.querySelector("h1")).toBeTruthy();
    return shown!;
  });
  // The picture is a file of the workspace, read as the others are.
  await waitFor(() =>
    expect(doc.querySelector("img")?.getAttribute("src")).toMatch(/^blob:/),
  );
  expect(doc.querySelector("img")!.alt).toBe(
    "How a checkout flows through the services",
  );
  await user.click(
    screen.getByRole("link", { name: "the design of the retries" }),
  );
  expect(
    (
      await screen.findByRole("tab", { name: /webhook-retries\.md/ })
    ).getAttribute("aria-selected"),
  ).toBe("true");
  // The tree shows where the file is.
  await waitFor(() => expect(row("webhook-retries.md")).toBeTruthy());
  expect(row("docs").getAttribute("aria-expanded")).toBe("true");
});

it("finds files by their names", async () => {
  const user = await files(await sessionOf(TRIAGE));
  await names();
  const find = screen.getByRole("searchbox", { name: "Find a file by name" });
  await user.type(find, "TOTAL");
  const found = await screen.findByRole("list", { name: "Files found" });
  const hits = () =>
    [...found.querySelectorAll("li button")].map(
      (hit) =>
        hit.querySelector("small")!.textContent +
        "/" +
        hit.querySelector(".nm")!.textContent,
    );
  await waitFor(() =>
    expect(hits()).toEqual([
      "src/checkout/total.ts",
      "test/checkout/total.test.ts",
    ]),
  );
  // What is found opens, and the tree shows where it is.
  await user.click(found.querySelector("li button")!);
  expect(
    (await screen.findByRole("tab", { name: /total\.ts/ })).getAttribute(
      "aria-selected",
    ),
  ).toBe("true");
  await user.clear(find);
  await waitFor(() => expect(row("total.ts")).toBeTruthy());
  expect(row("src").getAttribute("aria-expanded")).toBe("true");
  expect(row("total.ts").getAttribute("aria-selected")).toBe("true");

  await user.type(find, "no such name");
  await waitFor(() =>
    expect(document.querySelector(".ft-note")?.textContent).toBe(
      "Nothing has “no such name” in its name.",
    ),
  );
});

it("puts files into the workspace, and asks before it replaces one", async () => {
  const id = await sessionOf(TRIAGE);
  const request = await as("alice");
  const user = await files(id);
  await names();
  pick(new File(["to do"], "todo.txt"), new File(["more"], "more.txt"));
  expect(await screen.findByText("2 files uploaded")).toBeTruthy();
  await waitFor(() => expect(row("todo.txt")).toBeTruthy());

  // A name that is taken is not replaced without a word.
  const stat = () =>
    request<{ entries: { name: string; size: number }[] }>(
      "GET",
      `/v1/sessions/${id}/files?limit=1000`,
    ).entries.find((entry) => entry.name === "README.md")!;
  const before = stat().size;
  pick(new File(["replaced"], "README.md"));
  const asked = await screen.findByRole("dialog");
  expect(asked.textContent).toContain("Replace “README.md”?");
  await user.click(screen.getByRole("button", { name: "Cancel" }));
  expect(stat().size).toBe(before);
  pick(new File(["replaced"], "README.md"));
  await screen.findByRole("dialog");
  await user.click(screen.getByRole("button", { name: "Replace" }));
  await waitFor(() => expect(stat().size).toBe(8));

  // A file goes into the directory that it is put into, with the folders
  // that it comes in.
  const inside = new File(["a"], "a.txt");
  Object.defineProperty(inside, "webkitRelativePath", {
    value: "bundle/deep/a.txt",
  });
  await act(user, "docs", "Upload files here…");
  pick(inside);
  await waitFor(() =>
    expect(
      request<{ entries: { name: string }[] }>(
        "GET",
        `/v1/sessions/${id}/files?path=docs/bundle/deep`,
      ).entries.map((entry) => entry.name),
    ).toEqual(["a.txt"]),
  );
  await waitFor(() => expect(row("bundle")).toBeTruthy());
});

it("leaves out a file that is larger than a file of a workspace may be, and says so", async () => {
  (await backend()).setLimits(64 << 20, 10, 4);
  await files(await sessionOf(TRIAGE));
  await names();
  pick(new File(["12345"], "large.txt"), new File(["1234"], "small.txt"));
  expect(await screen.findByText("1 file over 4 bytes left out")).toBeTruthy();
  await waitFor(() => expect(row("small.txt")).toBeTruthy());
  expect(row("large.txt")).toBeUndefined();
});

it("lets only who may send the session messages put files into it", async () => {
  const id = await sessionOf(TRIAGE);
  // Carol owns the project, and reads the sessions of others.
  const user = await files(id, "carol");
  await names();
  expect(screen.queryByRole("button", { name: "Upload" })).toBeNull();
  expect(picker()).toBeNull();
  await user.click(row("docs").querySelector('button[title="More"]')!);
  expect(screen.queryByRole("menuitem", { name: /Upload/ })).toBeNull();
  expect(
    screen.getByRole("menuitem", { name: "Download as ZIP" }),
  ).toBeTruthy();
});

it("says that no worker has room for the sandbox, and tries again", async () => {
  const mock = await backend();
  mock.mock.room = false;
  const user = await files(await sessionOf(TRIAGE));
  expect(
    await screen.findByText("No worker has room for the sandbox"),
  ).toBeTruthy();
  expect(screen.queryByRole("tree")).toBeNull();
  mock.mock.room = true;
  await user.click(screen.getByRole("button", { name: "Try again" }));
  expect((await names()).length).toBeGreaterThan(10);
});

it("begins with an empty workspace, which the first file gives a sandbox", async () => {
  const request = await as("alice");
  const { id } = request<{ id: string }>(
    "POST",
    `/v1/agents/${await agentOf()}/sessions`,
    { title: "Nothing yet" },
  );
  await files(id);
  expect(await screen.findByText("No files yet")).toBeTruthy();
  expect(
    screen.getByRole<HTMLButtonElement>("button", {
      name: "Download all as ZIP",
    }).disabled,
  ).toBe(true);
  expect(
    request<{ sandbox?: string }>("GET", "/v1/sessions/" + id).sandbox,
  ).toBeUndefined();
  pick(new File(["# Plan"], "plan.md"));
  await waitFor(() => expect(row("plan.md")).toBeTruthy());
  expect(
    request<{ sandbox?: string }>("GET", "/v1/sessions/" + id).sandbox,
  ).toBe("awake");
});

it("has no files of a session that has ended", async () => {
  const request = await as("alice");
  const { id } = request<{ id: string }>(
    "POST",
    `/v1/agents/${await agentOf()}/sessions`,
    { title: "Over" },
  );
  request("DELETE", "/v1/sessions/" + id);
  const mock = await backend();
  const asked = vi.spyOn(mock, "request");
  await files(id);
  expect(await screen.findByText("Deleted with the session")).toBeTruthy();
  expect(
    asked.mock.calls.filter(([, path]) => String(path).includes("/files")),
  ).toEqual([]);
});

it("hides the sessions, and keeps them so", async () => {
  const id = await sessionOf(TRIAGE);
  let user = open(pathOf(id), "alice");
  await screen.findByRole("navigation", { name: "Sessions" });
  await user.click(
    await screen.findByRole("button", { name: "Hide the sessions" }),
  );
  expect(screen.queryByRole("navigation", { name: "Sessions" })).toBeNull();
  cleanup();
  user = open(pathOf(id), "alice");
  const shows = await screen.findByRole("button", {
    name: "Show the sessions",
  });
  expect(screen.queryByRole("navigation", { name: "Sessions" })).toBeNull();
  await user.click(shows);
  expect(
    await screen.findByRole("navigation", { name: "Sessions" }),
  ).toBeTruthy();
});

it("lays the files over the conversation in a narrow window, where the details do not show", async () => {
  sized(1000);
  const id = await sessionOf(TRIAGE);
  const user = open(pathOf(id), "alice");
  await screen.findByRole("button", { name: "Files" });
  expect(document.querySelector(".pane.details")).toBeNull();
  await user.click(screen.getByRole("button", { name: "Files" }));
  await names();
  expect(document.querySelector(".over-pane .pane.files")).not.toBeNull();
});

it("says why files did not go, and sends them again", async () => {
  const request = await as("alice");
  const { id } = request<{ id: string }>(
    "POST",
    `/v1/agents/${await agentOf()}/sessions`,
    { title: "No room" },
  );
  const mock = await backend();
  const user = await files(id);
  await screen.findByText("No files yet");
  // The sandbox that the first file needs has no worker to run on.
  mock.mock.room = false;
  pick(new File(["# Plan"], "plan.md"));
  const told = () => document.querySelector(".ft-up")?.textContent ?? "";
  await waitFor(() => expect(told()).toContain("1 file failed"));
  expect(told()).toContain(
    "No worker has room for the sandbox of the session; retry later.",
  );
  mock.mock.room = true;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(row("plan.md")).toBeTruthy());
  expect(document.querySelector(".ft-up")).toBeNull();
});

it("reads a file again that could not be read", async () => {
  const user = await files(await sessionOf(TRIAGE));
  await names();
  vi.spyOn(api, "download").mockRejectedValueOnce(
    new ApiError(
      503,
      {
        code: "sandbox_unavailable",
        message: "the sandbox of the session is not available now; retry later",
      },
      { "retry-after": "5" },
    ),
  );
  await user.click(row("Makefile"));
  expect(
    await screen.findByText(
      "The sandbox of the session is not available now; retry later.",
    ),
  ).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Try again" }));
  await waitFor(() =>
    expect(document.querySelector(".fv .cm-content")?.textContent).toContain(
      "npm test",
    ),
  );
});

it("follows what the agent does in the workspace", async () => {
  const request = await as("alice");
  const { id } = request<{ id: string }>(
    "POST",
    `/v1/agents/${await agentOf("reviewer")}/sessions`,
    { title: "At work" },
  );
  const user = open(`/p/storefront/agents/reviewer/s/${id}`, "alice");
  await user.click(await screen.findByRole("button", { name: "Files" }));
  await screen.findByText("No files yet");
  await user.type(
    screen.getByRole("textbox", { name: "Message" }),
    "Write the request down and run the tests.{Enter}",
  );
  // The tree shows what the turn left, without being asked to.
  await waitFor(() => expect(row("NOTES.md")).toBeTruthy(), { timeout: 5000 });
  await user.click(row("coverage"));
  await waitFor(() => expect(row("coverage-summary.json")).toBeTruthy());
});
