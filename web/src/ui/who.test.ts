import { describe, expect, it } from "vitest";
import type { Me, Member } from "../api/types.ts";
import { person } from "./who.ts";

const me: Me = { id: "u-alice", display_name: "Alice Kim", admin: true };
const members: Member[] = [
  {
    id: "m-1",
    project_id: "p",
    user: { id: "u-bob", username: "bob@example.com", display_name: "Bob Lee" },
    role: "user",
    created_at: "",
    updated_at: "",
  },
  {
    id: "m-2",
    project_id: "p",
    user: { id: "u-carol", username: "carol@example.com" },
    role: "owner",
    created_at: "",
    updated_at: "",
  },
];

describe("person", () => {
  it("knows the caller, member or not", () => {
    expect(person(me, members, "u-alice")).toEqual({
      name: "Alice Kim",
      known: true,
      key: "u-alice",
    });
  });

  it("knows the members of the project that are users", () => {
    expect(person(me, members, "u-bob").name).toBe("Bob Lee");
    expect(person(me, members, "u-carol")).toMatchObject({
      name: "carol@example.com",
      known: true,
    });
  });

  it("does not know anyone else, and shows the start of their ID", () => {
    const id = "0193f2a1-7c3e-7b10-8a4d-0c5e6f708192";
    expect(person(me, members, id)).toEqual({
      name: "User 0193f2a1",
      known: false,
      key: id,
      title: "User " + id,
    });
  });

  it("has a name for nobody", () => {
    expect(person(me, members, undefined)).toEqual({
      name: "Nobody",
      known: false,
    });
  });
});
