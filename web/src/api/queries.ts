import { useQuery } from "@tanstack/react-query";
import { useToken } from "../session/auth.ts";
import { api } from "./client.ts";
import type { Me, Project } from "./types.ts";

// A record with the number that picks its colour: its place in the list
// that it came in, so that neighbours differ.
export type Tinted<T> = T & { tint: number };

// useMe reads the caller. The request fails with the status 401 if nobody is
// signed in and the installation has an identity provider.
export function useMe() {
  const token = useToken();
  return useQuery({
    queryKey: ["me", token],
    queryFn: () => api.get<Me>("/v1/me"),
  });
}

// useProjects reads the caller's projects, oldest first.
export function useProjects() {
  return useQuery({
    queryKey: ["projects"],
    queryFn: async () => {
      const { projects } = await api.get<{ projects: Project[] }>(
        "/v1/projects",
      );
      return projects.map((project, tint) => ({ ...project, tint }));
    },
  });
}
