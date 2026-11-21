import { describe, expect, it } from "vitest";
import { PERMISSIONS, permissionsIn } from "./project.ts";
import type { Me, Member, Permission, Role } from "./types.ts";

const roles: Role[] = [
  { name: "owner", permissions: [...PERMISSIONS] },
  { name: "user", permissions: ["project.view", "sessions.run"] },
  { name: "viewer", permissions: ["project.view"] },
];

const bob: Me = { id: "u-bob", groups: ["team-a"], admin: false };

// member returns a member of the project: a user by ID, or else a group,
// with a role, or else with permissions of its own.
function member(
  who: { user?: string; group?: string },
  grant: string | Permission[],
): Member {
  return {
    id: "m-" + (who.user ?? who.group),
    project_id: "p",
    user: who.user ? { id: who.user, username: who.user } : undefined,
    group: who.group,
    role: typeof grant === "string" ? grant : "custom",
    permissions: typeof grant === "string" ? undefined : grant,
    created_at: "",
    updated_at: "",
  };
}

describe("permissionsIn", () => {
  it("gives an administrator everything, member or not", () => {
    expect(permissionsIn({ admin: true }, [], roles)).toEqual(PERMISSIONS);
  });

  it("gives a member what their role allows", () => {
    const members = [member({ user: "u-bob" }, "user")];
    expect(permissionsIn(bob, members, roles)).toEqual([
      "project.view",
      "sessions.run",
    ]);
  });

  it("gives a caller what a group of theirs is allowed", () => {
    const members = [
      member({ group: "team-a" }, "viewer"),
      member({ group: "team-b" }, "owner"),
    ];
    expect(permissionsIn(bob, members, roles)).toEqual(["project.view"]);
  });

  it("gives a member with permissions of their own those", () => {
    const members = [
      member({ user: "u-bob" }, ["project.view", "sessions.view_all"]),
    ];
    expect(permissionsIn(bob, members, roles)).toEqual([
      "project.view",
      "sessions.view_all",
    ]);
  });

  it("adds up what a caller is allowed in several ways", () => {
    const members = [
      member({ user: "u-bob" }, ["agents.manage"]),
      member({ group: "team-a" }, "user"),
      member({ user: "u-carol" }, "owner"),
    ];
    expect(permissionsIn(bob, members, roles)).toEqual([
      "project.view",
      "agents.manage",
      "sessions.run",
    ]);
  });

  it("gives nothing to a caller who is no member", () => {
    const members = [
      member({ user: "u-carol" }, "owner"),
      // A member who has not signed in yet has no ID.
      { ...member({ user: "dana" }, "owner"), user: { username: "dana" } },
    ];
    expect(permissionsIn(bob, members, roles)).toEqual([]);
    expect(permissionsIn({ admin: false }, members, roles)).toEqual([]);
  });
});
