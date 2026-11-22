import { expect, it } from "vitest";
import sprite from "../assets/sprite.svg?raw";
import { ICONS } from "./icons.ts";

it("lists the icons of the sprite", () => {
  const symbols = [...sprite.matchAll(/<symbol id="i-([a-z]+)"/g)];
  expect([...ICONS]).toEqual(symbols.map((match) => match[1]));
});
