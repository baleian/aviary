import { describe, expect, it } from "vitest";
import { isSlug, slugify } from "./slug.ts";

describe("isSlug", () => {
  it("takes lowercase letters, digits and hyphens", () => {
    expect(isSlug("a")).toBe(true);
    expect(isSlug("data-platform-2")).toBe(true);
    expect(isSlug("a".repeat(63))).toBe(true);
  });

  it("refuses what the API refuses", () => {
    expect(isSlug("")).toBe(false);
    expect(isSlug("2fast")).toBe(false);
    expect(isSlug("-a")).toBe(false);
    expect(isSlug("a-")).toBe(false);
    expect(isSlug("Storefront")).toBe(false);
    expect(isSlug("store front")).toBe(false);
    expect(isSlug("a".repeat(64))).toBe(false);
  });
});

describe("slugify", () => {
  it("makes a slug of a name", () => {
    expect(slugify("Backend engineer")).toBe("backend-engineer");
    expect(slugify("  Data & Platform!  ")).toBe("data-platform");
    expect(slugify("Café Crème")).toBe("cafe-creme");
  });

  it("begins with a letter and stays within the length", () => {
    expect(slugify("42 answers")).toBe("answers");
    expect(slugify("x".repeat(80))).toBe("x".repeat(63));
    expect(slugify("x".repeat(62) + " yz")).toBe("x".repeat(62));
  });

  it("has nothing to suggest for a name without letters", () => {
    expect(slugify("123")).toBe("");
    expect(isSlug(slugify("Storefront v2"))).toBe(true);
  });
});
