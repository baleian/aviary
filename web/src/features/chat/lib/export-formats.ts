/**
 * Export helpers — turn a list of messages into printable HTML.
 *
 * Two flavours:
 *   - renderChatHTML: visual export (clones the live DOM with stylesheets)
 *   - renderChatMarkdownHTML: structured markdown rendered to HTML for print
 */

type Block = Record<string, unknown>;

function escapeHtml(str: string): string {
  return str.replace(/[&<>"']/g, (c) => {
    switch (c) {
      case "&": return "&amp;";
      case "<": return "&lt;";
      case ">": return "&gt;";
      case '"': return "&quot;";
      default: return "&#39;";
    }
  });
}

function renderToolTree(tools: Block[], indent: number): string {
  return tools
    .map((t) => {
      const parts: string[] = [];
      const name = t.name ?? "unknown";
      const err = t.is_error ? " [ERROR]" : "";
      parts.push(`<div class="tool-block" style="margin-left:${indent * 12}px">`);
      parts.push(`<strong>Tool: ${escapeHtml(String(name))}</strong>${err}`);

      const input = t.input as Record<string, unknown> | undefined;
      if (input && Object.keys(input).length > 0) {
        parts.push(`<pre>${escapeHtml(JSON.stringify(input, null, 2))}</pre>`);
      }
      if (t.result != null) {
        const result = String(t.result);
        const short = result.length > 2000 ? result.slice(0, 2000) + "\n..." : result;
        parts.push(`<pre>${escapeHtml(short)}</pre>`);
      }
      const children = t.children as Block[] | undefined;
      if (children && children.length > 0) {
        parts.push(renderToolTree(children, indent + 1));
      }
      parts.push("</div>");
      return parts.join("\n");
    })
    .join("\n");
}

function renderBlocks(blocks: Block[]): string {
  // Build tree from flat blocks (same logic as build-block-tree, inlined to keep this pure)
  const toolMap = new Map<string, Block & { children: Block[] }>();
  const roots: Block[] = [];
  for (const b of blocks) {
    if (b.type === "tool_call") {
      const node = { ...b, children: [] as Block[] };
      toolMap.set(String(b.tool_use_id ?? b.id ?? ""), node);
      if (b.parent_tool_use_id) {
        const parent = toolMap.get(String(b.parent_tool_use_id));
        if (parent) {
          parent.children.push(node);
          continue;
        }
      }
      roots.push(node);
    } else {
      roots.push(b);
    }
  }

  return roots
    .map((b) => {
      if (b.type === "thinking") {
        const text = String(b.content ?? "").slice(0, 300);
        const ellipsis = String(b.content ?? "").length > 300 ? "..." : "";
        return `<div class="thinking">Thinking: ${escapeHtml(text)}${ellipsis}</div>`;
      }
      if (b.type === "tool_call") return renderToolTree([b], 0);
      return escapeHtml(String(b.content ?? ""));
    })
    .join("\n\n");
}

export interface ExportableMessage {
  sender_type: "user" | "agent";
  content: string;
  metadata?: Record<string, unknown>;
}

export function buildMarkdownExport(messages: ExportableMessage[], title: string): string {
  const lines = messages
    .map((msg) => {
      const role = msg.sender_type === "user" ? "User" : "Agent";
      const header = `### ${role}\n`;
      const blocks = msg.metadata?.blocks as Block[] | undefined;
      const body = blocks && blocks.length > 0 ? renderBlocks(blocks) : msg.content;
      return header + "\n" + body;
    })
    .join("\n\n---\n\n");

  return `# ${title}\n\n${lines}`;
}

export const PRINT_STYLES = `
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; max-width: 800px; margin: 40px auto; padding: 0 20px; line-height: 1.5; color: #222; font-size: 12px; }
  h1 { border-bottom: 2px solid #eee; padding-bottom: 6px; font-size: 18px; }
  h3 { margin-bottom: 2px; font-size: 13px; }
  hr { border: none; border-top: 1px solid #ddd; margin: 16px 0; }
  p { margin: 4px 0; }
  code { background: #f4f4f4; padding: 1px 3px; border-radius: 3px; font-size: 0.85em; }
  pre { background: #f4f4f4; padding: 8px; border-radius: 4px; font-size: 10px; line-height: 1.4; white-space: pre-wrap; word-break: break-all; }
  strong { font-weight: 600; }
  .thinking { background: #f9f9f0; border-left: 3px solid #d4c87a; padding: 4px 8px; margin: 4px 0; font-size: 10px; color: #666; line-height: 1.4; white-space: pre-wrap; }
  .tool-block { font-size: 10px; color: #555; margin: 2px 0; }
  .tool-block pre { font-size: 9px; margin: 2px 0; padding: 4px 6px; }
  @media print { body { margin: 10px; } }
`;
