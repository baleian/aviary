import { useQueries } from "@tanstack/react-query";
import { useMemo } from "react";
import { api, ApiError } from "./client.ts";
import { type Tinted, useMe, useProjects } from "./queries.ts";
import type {
  Agent,
  Me,
  Member,
  Permission,
  Project,
  Provider,
  Role,
} from "./types.ts";

export const PERMISSIONS: Permission[] = [
  "project.view",
  "agents.manage",
  "connections.manage",
  "sessions.run",
  "sessions.view_all",
  "sessions.control_all",
];

// What the console shows of a project on every screen of it.
export interface ProjectContext {
  project: Tinted<Project>;
  agents: Tinted<Agent>[];
  providers: Provider[];
  members: Member[];
  roles: Role[];
  // What the caller may do in the project.
  permissions: Permission[];
}

// The keys under which the console keeps what it read of a project, for
// reading it again after a change.
export const keys = {
  agents: (project: string) => ["agents", project],
  providers: (project: string) => ["providers", project],
  members: (project: string) => ["members", project],
  roles: ["roles"],
};

// permissionsIn works out what the caller may do in a project, from its
// members and the roles. The API has no request that returns it.
export function permissionsIn(
  me: Me,
  members: Member[],
  roles: Role[],
): Permission[] {
  if (me.admin) return [...PERMISSIONS];
  const held = new Set<Permission>();
  for (const member of members) {
    // A member who has not signed in yet has no ID, and is nobody so far.
    const mine = member.user
      ? !!member.user.id && member.user.id === me.id
      : (me.groups ?? []).includes(member.group ?? "");
    if (!mine) continue;
    const role = roles.find((other) => other.name === member.role);
    const granted =
      member.role === "custom" ? member.permissions : role?.permissions;
    for (const permission of granted ?? []) held.add(permission);
  }
  return PERMISSIONS.filter((permission) => held.has(permission));
}

export function can(context: ProjectContext, permission: Permission): boolean {
  return context.permissions.includes(permission);
}

// What useProjectContext found: the project, or that it is still being
// read, or that the caller has no such project, or that reading it failed.
export type Found =
  | { state: "ready"; context: ProjectContext }
  | { state: "pending" }
  | { state: "missing" }
  | { state: "failed"; error: Error };

// useProjectContext reads the project with the slug. The API does not tell
// a project that does not exist from one that the caller is no member of,
// and neither does the console.
export function useProjectContext(slug: string): Found {
  const me = useMe().data;
  const projects = useProjects().data;
  const project = projects?.find((other) => other.slug === slug);
  const id = project?.id ?? "";
  const base = "/v1/projects/" + id;
  const [agents, providers, members, roles] = useQueries({
    queries: [
      {
        queryKey: keys.agents(id),
        queryFn: async () =>
          (await api.get<{ agents: Agent[] }>(base + "/agents")).agents,
        enabled: !!project,
      },
      {
        queryKey: keys.providers(id),
        queryFn: async () =>
          (await api.get<{ providers: Provider[] }>(base + "/providers"))
            .providers,
        enabled: !!project,
      },
      {
        queryKey: keys.members(id),
        queryFn: async () =>
          (await api.get<{ members: Member[] }>(base + "/members")).members,
        enabled: !!project,
      },
      {
        queryKey: keys.roles,
        queryFn: async () =>
          (await api.get<{ roles: Role[] }>("/v1/roles")).roles,
        enabled: !!project,
      },
    ],
  });

  const context = useMemo(() => {
    if (!me || !project) return undefined;
    if (!agents.data || !providers.data || !members.data || !roles.data) {
      return undefined;
    }
    return {
      project,
      agents: agents.data.map((agent, tint) => ({ ...agent, tint })),
      providers: providers.data,
      members: members.data,
      roles: roles.data,
      permissions: permissionsIn(me, members.data, roles.data),
    };
  }, [me, project, agents.data, providers.data, members.data, roles.data]);

  if (!projects) return { state: "pending" };
  if (!project) return { state: "missing" };
  const error = [agents, providers, members, roles].find(
    (query) => query.error,
  )?.error;
  if (error) {
    const gone = error instanceof ApiError && error.status === 404;
    return gone ? { state: "missing" } : { state: "failed", error };
  }
  return context ? { state: "ready", context } : { state: "pending" };
}
