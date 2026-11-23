import type { McpServerTools, Tool, Transport } from "../api/types.ts";
import { plural, sentence, transportName } from "./format.ts";

// What the console finds among the MCP servers of a project, and what it
// says of them.

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

// said puts what came of asking a server for its tools in words, as a test
// of the connection to it. transport is how the server was reached.
export function said(
  asked: McpServerTools,
  transport: Transport,
): { tone: "ok" | "bad" | ""; text: string; tools?: Tool[] } {
  if (!asked.reached) {
    return { tone: "bad", text: sentence("Not reached: " + asked.reason) };
  }
  const status = asked.status ?? 0;
  // A server that answered, but did not list its tools, says why.
  if (status >= 200 && status < 300 && asked.reason) {
    return { tone: "bad", text: sentence(asked.reason) };
  }
  if (status >= 200 && status < 300) {
    const tools = asked.tools ?? [];
    return {
      tone: "ok",
      text: `Connected in ${asked.duration_ms} ms · ${plural(tools.length, "tool")}`,
      tools,
    };
  }
  if (status === 401 || status === 403) {
    return { tone: "bad", text: `The server refused the request (${status}).` };
  }
  if (status === 404 || status === 405) {
    const other = transportName(transport === "http" ? "sse" : "http");
    return {
      tone: "bad",
      text: `No MCP server answers ${transportName(transport)} at this URL (${status}). Try ${other}?`,
    };
  }
  return { tone: "bad", text: `The server answered ${status}.` };
}
