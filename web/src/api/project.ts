import { useQueries } from "@tanstack/react-query";
import { useMemo } from "react";
import { api, ApiError } from "./client.ts";
import { type Tinted, useMe, useProjects } from "./queries.ts";
import type {
  Agent,
  McpServer,
  Me,
  Member,
  Permission,
  Project,
  Provider,
  Role,
  SecretStore,
  Session,
  Turn,
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
  mcpServers: McpServer[];
  stores: SecretStore[];
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
  models: (provider: string) => ["models", provider],
  mcpServers: (project: string) => ["mcp-servers", project],
  stores: (project: string) => ["stores", project],
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
  for (const member of mine(me, members)) {
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

// sessionAccess returns what the caller may do with a session: whether it is
// the caller's own, whether the caller may send messages to it, and whether
// the caller may rename and end it. Without an identity provider every
// session is the caller's.
export function sessionAccess(
  context: ProjectContext,
  me: Me,
  session: Session,
): { own: boolean; send: boolean; control: boolean } {
  const own = !me.id || session.created_by === me.id;
  const runs = can(context, "sessions.run");
  return {
    own,
    send: me.admin || (own && runs),
    control: me.admin || (own ? runs : can(context, "sessions.control_all")),
  };
}

// controls reports whether the caller may stop a turn and answer its
// requests for approval.
export function controls(context: ProjectContext, me: Me, turn: Turn): boolean {
  if (me.admin) return true;
  return turn.created_by === me.id
    ? can(context, "sessions.run")
    : can(context, "sessions.control_all");
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
  const [agents, providers, mcpServers, stores, members, roles] = useQueries({
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
        queryKey: keys.mcpServers(id),
        queryFn: async () =>
          (await api.get<{ mcp_servers: McpServer[] }>(base + "/mcp-servers"))
            .mcp_servers,
        enabled: !!project,
      },
      {
        queryKey: keys.stores(id),
        queryFn: async () =>
          (
            await api.get<{ secret_stores: SecretStore[] }>(
              base + "/secret-stores",
            )
          ).secret_stores,
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
    if (!agents.data || !providers.data || !stores.data) return undefined;
    if (!mcpServers.data || !members.data || !roles.data) return undefined;
    return {
      project,
      agents: agents.data.map((agent, tint) => ({ ...agent, tint })),
      providers: providers.data,
      mcpServers: mcpServers.data,
      stores: stores.data,
      members: members.data,
      roles: roles.data,
      permissions: permissionsIn(me, members.data, roles.data),
    };
  }, [
    me,
    project,
    agents.data,
    providers.data,
    mcpServers.data,
    stores.data,
    members.data,
    roles.data,
  ]);

  if (!projects) return { state: "pending" };
  if (!project) return { state: "missing" };
  const error = [agents, providers, mcpServers, stores, members, roles].find(
    (query) => query.error,
  )?.error;
  if (error) {
    const gone = error instanceof ApiError && error.status === 404;
    return gone ? { state: "missing" } : { state: "failed", error };
  }
  return context ? { state: "ready", context } : { state: "pending" };
}

// What each permission allows, in the words of the console.
export const ALLOWS: Record<Permission, string> = {
  "project.view": "See the project",
  "agents.manage": "Manage agents",
  "connections.manage": "Manage connections",
  "sessions.run": "Run sessions",
  "sessions.view_all": "See every session",
  "sessions.control_all": "Control every session",
};

// The roles of the installation, and what each is for.
export const ROLES = ["owner", "editor", "user", "viewer", "auditor"];
export const ROLE_NOTES: Record<string, string> = {
  owner: "Runs the project",
  editor: "Builds agents and connections",
  user: "Works with the agents",
  viewer: "Looks, and touches nothing",
  auditor: "Reads every session",
};

// summary says in a few words what a set of permissions allows.
export function summary(permissions: Permission[]): string {
  if (PERMISSIONS.every((permission) => permissions.includes(permission))) {
    return "Everything in this project";
  }
  // Seeing the project goes first, and controlling every session includes
  // seeing them.
  const controls = permissions.includes("sessions.control_all");
  const rest = permissions.filter(
    (permission) =>
      permission !== "project.view" &&
      !(permission === "sessions.view_all" && controls),
  );
  const words = rest.map((permission) => ALLOWS[permission].toLowerCase());
  return ["See the project", ...words].join(" · ");
}

// mine returns the members of a project through which the caller is in it:
// the caller as a user, and the caller's groups. A member who has not signed
// in yet has no ID, and is nobody so far.
export function mine(me: Me, members: Member[]): Member[] {
  return members.filter((member) =>
    member.user
      ? !!member.user.id && member.user.id === me.id
      : (me.groups ?? []).includes(member.group ?? ""),
  );
}
