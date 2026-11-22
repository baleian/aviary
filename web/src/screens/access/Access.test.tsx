// @vitest-environment happy-dom
import { screen, within } from "@testing-library/react";
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { api } from "../../api/client.ts";
import { backend } from "../../api/fake.ts";
import { fast, leave, open } from "../../testing.tsx";

beforeAll(fast);
afterEach(leave);

// row returns the row of the member with the name, once it shows.
async function row(name: string) {
  const cell = await screen.findByText(name, { selector: ".who b" });
  return cell.closest("tr")!;
}

const cells = (line: HTMLElement) =>
  [...line.querySelectorAll("td")].map((cell) => cell.textContent);

it("shows who is in the project and what each may do", async () => {
  open("/p/storefront/access", "alice");
  const carol = await row("Carol Park");
  expect(cells(carol)[0]).toContain("Carol Parkcarol@example.com");
  expect(cells(carol).slice(1, 4)).toEqual([
    "User",
    "owner",
    "Everything in this project",
  ]);
  expect(cells(await row("Bob Lee"))[3]).toBe("See the project · run sessions");
  const group = await row("team-a");
  expect(cells(group)[0]).toContain("team-aGroup");
  expect(cells(group).slice(1, 4)).toEqual([
    "Group",
    "viewer",
    "See the project",
  ]);
  // A member who was added by username and has not signed in yet.
  const dana = await row("dana@example.com");
  expect(cells(dana)[0]).toContain("dana@example.comNot signed in yet");
  expect(cells(dana).slice(1, 4)).toEqual([
    "Awaited",
    "editor",
    "See the project · manage agents · manage connections · run sessions",
  ]);
  // Permissions of a member's own show as they are.
  expect(cells(await row("Erin Cho")).slice(2, 4)).toEqual([
    "custom",
    "project.viewsessions.runsessions.view_all",
  ]);
  expect(screen.getByText("You are an administrator")).toBeTruthy();
  expect(document.title).toBe("Access · Storefront · Aviary");
  expect(screen.getByRole("link", { name: "Access" }).className).toBe("on");
});

it("finds a member by name, group or role", async () => {
  const user = open("/p/storefront/access", "alice");
  await row("Carol Park");
  const find = screen.getByRole("textbox", { name: "Find a member" });
  await user.type(find, "TEAM");
  expect(screen.getAllByRole("row").length).toBe(2 + 6);
  expect(screen.queryByText("Carol Park")).toBeNull();
  expect(screen.getByText("team-a")).toBeTruthy();
  // Capitals make no difference, in what is typed or in what is sought.
  await user.clear(find);
  await user.type(find, "PARK");
  expect(screen.getByText("Carol Park")).toBeTruthy();
  expect(screen.getAllByRole("row").length).toBe(2 + 6);
  await user.clear(find);
  await user.type(find, "editor");
  expect(screen.getByText("dana@example.com")).toBeTruthy();
  expect(screen.queryByText("team-a", { selector: ".who b" })).toBeNull();
  await user.clear(find);
  await user.type(find, "zzz");
  expect(screen.getByText("No member matches “zzz”.")).toBeTruthy();
});

it("adds a person with a role, and a group with permissions of its own", async () => {
  const user = open("/p/storefront/access", "alice");
  await user.click(await screen.findByRole("button", { name: /Add member/ }));
  let form = within(screen.getByRole("dialog", { name: "Add a member" }));
  const add = form.getByRole("button", { name: "Add member" });
  await user.click(add);
  expect(form.getByRole("alert").textContent).toBe("Enter a username.");
  await user.type(
    form.getByRole("textbox", { name: "Username" }),
    "frank@example.com",
  );
  await user.selectOptions(
    form.getByRole("combobox", { name: "Role" }),
    "editor",
  );
  await user.click(add);
  expect(cells(await row("frank@example.com")).slice(1, 3)).toEqual([
    "Awaited",
    "editor",
  ]);
  expect(screen.getByRole("status").textContent).toContain(
    "Added frank@example.com",
  );

  await user.click(screen.getByRole("button", { name: /Add member/ }));
  form = within(screen.getByRole("dialog", { name: "Add a member" }));
  await user.click(form.getByRole("button", { name: "A group" }));
  await user.click(form.getByRole("button", { name: "Add member" }));
  expect(form.getByRole("alert").textContent).toBe("Enter a group.");
  await user.type(form.getByRole("textbox", { name: "Group" }), "ops");
  await user.selectOptions(
    form.getByRole("combobox", { name: "Role" }),
    "custom",
  );
  // Seeing the project is always held, and controlling every session
  // includes seeing them.
  const box = (name: RegExp) =>
    form.getByRole("checkbox", { name }) as HTMLInputElement;
  expect(box(/See the project/).disabled).toBe(true);
  expect(box(/See the project/).checked).toBe(true);
  expect(box(/Run sessions/).checked).toBe(true);
  await user.click(box(/Run sessions/));
  await user.click(box(/Control every session/));
  expect(box(/See every session/).checked).toBe(true);
  expect(box(/See every session/).disabled).toBe(true);
  await user.click(form.getByRole("button", { name: "Add member" }));
  expect(cells(await row("ops")).slice(1, 4)).toEqual([
    "Group",
    "custom",
    "project.viewsessions.view_allsessions.control_all",
  ]);
});

it("changes the role of a member, or gives them permissions of their own", async () => {
  const user = open("/p/storefront/access", "alice");
  const bob = await row("Bob Lee");
  await user.click(within(bob).getByRole("button", { name: /user/ }));
  const menu = within(screen.getByRole("menu"));
  expect(menu.getByText("Role of Bob Lee")).toBeTruthy();
  expect(
    menu.getByRole("menuitem", { name: /Works with the agents/ }).className,
  ).toContain("on");
  await user.click(menu.getByRole("menuitem", { name: /Reads every session/ }));
  expect(
    await within(bob).findByText("See the project · see every session"),
  ).toBeTruthy();
  expect(cells(bob)[2]).toBe("auditor");

  await user.click(within(bob).getByRole("button", { name: "More" }));
  await user.click(
    screen.getByRole("menuitem", { name: "Custom permissions…" }),
  );
  const form = within(
    screen.getByRole("dialog", { name: "Permissions of Bob Lee" }),
  );
  // The boxes begin as the role of the member has them.
  const seeAll = form.getByRole("checkbox", { name: /See every session/ });
  expect((seeAll as HTMLInputElement).checked).toBe(true);
  await user.click(form.getByRole("checkbox", { name: /Manage agents/ }));
  await user.click(form.getByRole("button", { name: "Save permissions" }));
  expect(
    await within(bob).findByText("agents.manage", { selector: ".tag" }),
  ).toBeTruthy();
  expect(cells(bob).slice(2, 4)).toEqual([
    "custom",
    "project.viewagents.managesessions.view_all",
  ]);
});

it("removes a member, once it is confirmed", async () => {
  const user = open("/p/storefront/access", "alice");
  const group = await row("team-a");
  await user.click(within(group).getByRole("button", { name: "More" }));
  await user.click(
    screen.getByRole("menuitem", { name: "Remove from the project" }),
  );
  let confirm = within(screen.getByRole("dialog", { name: "Remove team-a?" }));
  expect(
    confirm.getByText("Its members lose the access that it gives them."),
  ).toBeTruthy();
  await user.click(confirm.getByRole("button", { name: "Cancel" }));
  expect(screen.getByText("team-a")).toBeTruthy();

  await user.click(within(group).getByRole("button", { name: "More" }));
  await user.click(
    screen.getByRole("menuitem", { name: "Remove from the project" }),
  );
  confirm = within(screen.getByRole("dialog", { name: "Remove team-a?" }));
  await user.click(confirm.getByRole("button", { name: "Remove member" }));
  expect(await screen.findByText("Removed team-a")).toBeTruthy();
  expect(screen.queryByText("team-a", { selector: ".who b" })).toBeNull();
  expect(screen.getByText("Carol Park")).toBeTruthy();
});

it("changes what the roles allow, all together", async () => {
  const user = open("/p/storefront/access", "alice");
  await row("Carol Park");
  const box = (name: string) =>
    screen.getByRole("checkbox", { name }) as HTMLInputElement;
  const save = screen.getByRole("button", { name: "Save changes" });
  const discard = screen.getByRole("button", { name: "Discard" });
  expect((save as HTMLButtonElement).disabled).toBe(true);
  expect(box("viewer: See the project").disabled).toBe(true);

  await user.click(box("viewer: Run sessions"));
  expect(screen.getByText("1 unsaved change")).toBeTruthy();
  expect(box("viewer: Run sessions").closest("td")!.className).toBe("chg");
  await user.click(discard);
  expect(box("viewer: Run sessions").checked).toBe(false);
  expect(screen.queryByText(/unsaved/)).toBeNull();

  // Controlling every session includes seeing them, which counts too.
  await user.click(box("editor: Control every session"));
  expect(box("editor: See every session").checked).toBe(true);
  expect(box("editor: See every session").disabled).toBe(true);
  await user.click(box("viewer: Run sessions"));
  expect(screen.getByText("3 unsaved changes")).toBeTruthy();
  const put = vi.spyOn(api, "put");
  await user.click(save);
  expect(await screen.findByText("Roles saved")).toBeTruthy();
  // Only the roles that changed are sent.
  expect(put.mock.calls.map(([path]) => path)).toEqual([
    "/v1/roles/editor",
    "/v1/roles/viewer",
  ]);
  put.mockRestore();
  expect(screen.queryByText(/unsaved/)).toBeNull();
  expect(box("viewer: Run sessions").checked).toBe(true);
  expect(box("viewer: Run sessions").closest("td")!.className).toBe("");
  // The members of the roles may do what the roles now allow.
  expect(cells(await row("team-a"))[3]).toBe("See the project · run sessions");
  // An editor who also controls every session may do everything.
  expect(cells(await row("dana@example.com"))[3]).toBe(
    "Everything in this project",
  );
});

it("shows a member their own access, and lets them change nothing", async () => {
  open("/p/storefront/access", "bob");
  await row("Carol Park");
  const yours = document.querySelector(".yours")!;
  expect(yours.textContent).toBe(
    "BLYour accessSee the project · run sessionsuser",
  );
  expect(screen.getByText("you", { selector: ".you" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Add member/ })).toBeNull();
  expect(screen.queryByRole("button", { name: "More" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Change the role" })).toBeNull();
  expect(screen.queryByRole("checkbox")).toBeNull();
  expect(screen.queryByRole("button", { name: "Save changes" })).toBeNull();
  expect(
    screen.getByText("Only administrators can change members."),
  ).toBeTruthy();
  expect(
    screen.getByText("Only administrators can change roles."),
  ).toBeTruthy();
  // What a role allows shows as marks.
  expect(document.querySelectorAll(".mx .yes").length).toBeGreaterThan(6);
});

it("marks the group through which the caller is in the project", async () => {
  open("/p/data-platform/access", "carol");
  expect((await row("team-a")).textContent).toContain("includes you");
  expect(document.querySelector(".yours")!.textContent).toContain(
    "See the project · run sessions",
  );
});

it("says that members and roles do not apply without sign-in", async () => {
  (await backend()).setIdentityProvider(false);
  open("/p/playground/access");
  expect(
    await screen.findByText(
      "Without sign-in, everyone is. Members and roles do not apply.",
    ),
  ).toBeTruthy();
  expect(screen.getByText("No members yet")).toBeTruthy();
});
