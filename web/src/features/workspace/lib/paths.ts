export function joinPath(parent: string, name: string): string {
  return parent === "/" ? `/${name}` : `${parent}/${name}`;
}

export function parentOf(path: string): string {
  if (path === "/" || !path.includes("/")) return "/";
  const trimmed = path.replace(/\/+$/g, "");
  const idx = trimmed.lastIndexOf("/");
  return idx <= 0 ? "/" : trimmed.slice(0, idx);
}

export function basename(path: string): string {
  const slash = path.lastIndexOf("/");
  return slash === -1 ? path : path.slice(slash + 1);
}

/** Display form — matches what agents see inside the bwrap sandbox. */
export function sandboxPath(path: string): string {
  if (path === "/") return "/workspace";
  return `/workspace${path.startsWith("/") ? "" : "/"}${path}`;
}
