// @vitest-environment happy-dom
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { Menu, MenuItem } from "./Menu.tsx";

afterEach(cleanup);

it("closes when an item is chosen, and then does what the item does", async () => {
  const chosen = vi.fn(() => {
    expect(screen.queryByRole("menu")).toBeNull();
  });
  render(
    <MemoryRouter>
      <Menu
        button={(props) => (
          <button type="button" {...props}>
            More
          </button>
        )}
      >
        <MenuItem text="Rename" onClick={chosen} />
        <MenuItem text="Delete" disabled onClick={chosen} />
      </Menu>
    </MemoryRouter>,
  );
  const user = userEvent.setup();
  const button = screen.getByRole("button", { name: "More" });
  expect(button.getAttribute("aria-expanded")).toBe("false");

  await user.click(button);
  expect(button.getAttribute("aria-expanded")).toBe("true");
  await user.click(screen.getByRole("menuitem", { name: "Delete" }));
  expect(chosen).not.toHaveBeenCalled();
  expect(screen.getByRole("menu")).toBeTruthy();

  await user.click(screen.getByRole("menuitem", { name: "Rename" }));
  expect(chosen).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("menu")).toBeNull();

  // The button closes the menu that it opened.
  await user.click(button);
  await user.click(button);
  expect(screen.queryByRole("menu")).toBeNull();
});
