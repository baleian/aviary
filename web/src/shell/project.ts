import { useOutletContext } from "react-router";
import type { ProjectContext } from "../api/project.ts";

// useProject returns the project in view, to a screen inside ProjectFrame.
export function useProject(): ProjectContext {
  return useOutletContext<ProjectContext>();
}
