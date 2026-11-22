// @vitest-environment happy-dom
import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, beforeAll, expect, it } from "vitest";
import { fast, leave, open } from "../testing.tsx";

beforeAll(fast);
afterEach(leave);

const newProject = { name: /New project/ };

it("creates a project, and opens it", async () => {
  const user = open("/projects", "alice");
  await user.click((await screen.findAllByRole("button", newProject))[0]);
  const dialog = within(screen.getByRole("dialog", { name: "New project" }));
  const slug = dialog.getByRole("textbox", { name: /Slug/ });

  // The slug follows the name until it is written by hand.
  await user.type(dialog.getByRole("textbox", { name: /Name/ }), "Billing 2");
  expect((slug as HTMLInputElement).value).toBe("billing-2");
  await user.clear(slug);
  await user.type(slug, "billing");
  await user.type(dialog.getByRole("textbox", { name: /Name/ }), "!");
  expect((slug as HTMLInputElement).value).toBe("billing");

  await user.click(dialog.getByRole("button", { name: "Create project" }));
  // The project opens. A new project begins with its first steps.
  expect(
    await screen.findByRole("link", { name: "Add a provider" }),
  ).toBeTruthy();
  expect(screen.getByRole("heading", { name: /Billing 2!/ })).toBeTruthy();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByRole("status").textContent).toContain(
    "Created Billing 2!",
  );
  // The bar shows the project that is in view.
  expect(
    screen.getByRole("button", { name: /Billing 2!/ }).className,
  ).toContain("sw-b");
});

it("says what is wrong with a project, and keeps the dialog", async () => {
  const user = open("/projects", "alice");
  await user.click((await screen.findAllByRole("button", newProject))[0]);
  const dialog = within(screen.getByRole("dialog", { name: "New project" }));
  const slug = dialog.getByRole("textbox", { name: /Slug/ });
  const create = dialog.getByRole("button", { name: "Create project" });

  await user.click(create);
  expect(dialog.getByRole("alert").textContent).toBe("Enter a slug.");

  await user.type(slug, "Not A Slug");
  await user.click(create);
  expect(dialog.getByRole("alert").textContent).toContain("A slug is 1 to 63");

  // What the API refuses shows the same way.
  await user.clear(slug);
  await user.type(slug, "storefront");
  await user.click(create);
  expect((await dialog.findByRole("alert")).textContent).toBe(
    "The slug is already taken.",
  );

  await user.click(dialog.getByRole("button", { name: "Cancel" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByRole("heading", { name: "Projects" })).toBeTruthy();
});

it("lets only an administrator create a project", async () => {
  const user = open("/projects", "bob");
  await screen.findByRole("heading", { name: "Projects" });
  expect(screen.queryByRole("button", newProject)).toBeNull();
  await user.click(screen.getByRole("button", { name: "Choose a project" }));
  expect(screen.queryByRole("menuitem", newProject)).toBeNull();
  expect(screen.getAllByRole("menuitem").length).toBe(3);
});

it("moves between the projects in the bar", async () => {
  const user = open("/p/storefront/agents", "alice");
  const switcher = await screen.findByRole("button", { name: /Storefront/ });
  await user.click(switcher);
  const menu = within(screen.getByRole("menu"));
  expect(
    menu.getByRole("menuitem", { name: /Storefront/ }).className,
  ).toContain("on");
  await user.click(menu.getByRole("menuitem", { name: /Data Platform/ }));

  expect(
    await screen.findByRole("button", { name: /Data Platform/ }),
  ).toBeTruthy();
  expect(screen.queryByRole("menu")).toBeNull();
  expect(document.title).toBe("Overview · Data Platform · Aviary");

  await user.click(screen.getByRole("button", { name: /Data Platform/ }));
  await user.click(screen.getByRole("menuitem", { name: "New project" }));
  const dialog = screen.getByRole("dialog", { name: "New project" });
  // The browser tells a dialog that Escape was pressed.
  fireEvent(dialog, new Event("cancel", { cancelable: true }));
  expect(screen.queryByRole("dialog")).toBeNull();

  await user.click(screen.getByRole("button", { name: /Data Platform/ }));
  await user.click(screen.getByRole("menuitem", { name: "All projects" }));
  expect(await screen.findByRole("heading", { name: "Projects" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Choose a project" })).toBeTruthy();
});

it("comes back to the project that was last in view", async () => {
  open("/p/data-platform/agents", "alice");
  await screen.findByRole("heading", { name: "Agents" });
  again("/", "alice");
  expect(
    await screen.findByRole("heading", { name: /Data Platform/, level: 1 }),
  ).toBeTruthy();
});

it("begins at the projects for a caller who was in none", async () => {
  open("/", "alice");
  expect(await screen.findByRole("heading", { name: "Projects" })).toBeTruthy();
});

it("does not find a project that the caller is no member of", async () => {
  open("/p/data-platform/agents", "bob");
  expect(
    await screen.findByRole("heading", { name: "Not found" }),
  ).toBeTruthy();
  expect(
    screen.getByText("It does not exist, or you do not have access to it."),
  ).toBeTruthy();
});

// again shows the console again, as after a reload: what the browser
// keeps stays.
function again(path: string, account: string) {
  cleanup();
  return open(path, account);
}
