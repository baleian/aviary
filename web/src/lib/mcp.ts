// What the console finds among the MCP servers of a project.

// matches reports whether an MCP server answers to what was sought: its
// name, its slug, or one of its tools. It returns the tool that matched, if
// a tool did.
export function matches(
  server: { name: string; slug: string; tools?: { name: string }[] },
  sought: string,
): { tool?: string } | null {
  const words = sought.trim().toLowerCase();
  if (!words) return {};
  if (`${server.name} ${server.slug}`.toLowerCase().includes(words)) return {};
  const tool = server.tools?.find((each) =>
    each.name.toLowerCase().includes(words),
  );
  return tool ? { tool: tool.name } : null;
}
