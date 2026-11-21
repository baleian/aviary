import { describe, expect, it } from "vitest";
import { clip, fullTime, initials, pick, sentence, when } from "./format.ts";

describe("when", () => {
  const now = new Date(2026, 10, 22, 15, 30);

  it("names the day of a recent time", () => {
    expect(when(new Date(2026, 10, 22, 9, 5), now)).toBe("Today 09:05");
    expect(when(new Date(2026, 10, 21, 23, 59), now)).toBe("Yesterday 23:59");
    expect(when(new Date(2026, 10, 18, 16, 20), now)).toBe("Wed 16:20");
  });

  it("gives the date of an older time", () => {
    expect(when(new Date(2026, 10, 15, 16, 20), now)).toBe("Nov 15");
    expect(when(new Date(2025, 11, 31, 8, 0), now)).toBe("Dec 31, 2025");
  });
});

it("writes a time in full", () => {
  expect(fullTime(new Date(2026, 0, 5, 7, 8, 9))).toBe("Jan 5, 2026, 07:08:09");
});

describe("initials", () => {
  it("takes the first letters of the first and the last word", () => {
    expect(initials("Alice Kim")).toBe("AK");
    expect(initials("Ana Maria de Souza")).toBe("AS");
  });

  it("takes two letters of a single word", () => {
    expect(initials("erin")).toBe("ER");
  });

  it("reads a username without its domain", () => {
    expect(initials("bob.lee@example.com")).toBe("BL");
    expect(initials("dana@example.com")).toBe("DA");
  });

  it("has a letter for no name", () => {
    expect(initials("")).toBe("?");
  });
});

it("picks the same number for the same text", () => {
  const picked = pick("6f1c2d3e-0001-4a10-9c1e-5a1d2b3c4d01", 4);
  expect(picked).toBe(pick("6f1c2d3e-0001-4a10-9c1e-5a1d2b3c4d01", 4));
  expect(picked).toBeGreaterThanOrEqual(0);
  expect(picked).toBeLessThan(4);
  expect(pick("a", 1000)).not.toBe(pick("b", 1000));
});

describe("sentence", () => {
  it("makes a message of the API a sentence", () => {
    expect(sentence("the slug is already taken")).toBe(
      "The slug is already taken.",
    );
  });

  it("keeps the end of a message that has one", () => {
    expect(sentence("is it there?")).toBe("Is it there?");
  });

  it("has words for no message", () => {
    expect(sentence("")).toBe("Something went wrong.");
  });
});

it("clips a text to one line of a length", () => {
  expect(clip("  Fix\n the   cart ", 40)).toBe("Fix the cart");
  expect(clip("Fix the cart", 12)).toBe("Fix the cart");
  expect(clip("Fix the cart now", 12)).toBe("Fix the car…");
  // No space is left before the mark of the cut.
  expect(clip("Fix the cart now", 9)).toBe("Fix the…");
});
