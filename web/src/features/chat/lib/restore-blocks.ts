import type { StreamBlock } from "@/types";
import { buildBlockTree } from "./build-block-tree";

/**
 * Restore saved block metadata into a StreamBlock tree.
 *
 * Used by MessageBubble when rendering historical messages — these are
 * stored as flat JSON in `message.metadata.blocks` and need to be
 * rehydrated to the same shape live streaming blocks have.
 */
export function restoreBlocks(
  raw: Array<Record<string, unknown>>,
  cancelled = false,
): StreamBlock[] {
  const flat: StreamBlock[] = raw.map((block, i) => {
    if (block.type === "tool_call") {
      const hasResult = block.result != null;
      return {
        type: "tool_call" as const,
        id: String(block.tool_use_id ?? `saved-${i}`),
        name: String(block.name ?? "unknown"),
        input: (block.input as Record<string, unknown>) ?? {},
        status: "complete" as const,
        result: hasResult
          ? String(block.result)
          : cancelled
            ? "[Cancelled]"
            : undefined,
        is_error:
          block.is_error === true
            ? true
            : !hasResult && cancelled
              ? true
              : undefined,
        parent_tool_use_id: block.parent_tool_use_id
          ? String(block.parent_tool_use_id)
          : undefined,
      };
    }
    if (block.type === "thinking") {
      return {
        type: "thinking" as const,
        id: `thinking-${i}`,
        content: String(block.content ?? ""),
      };
    }
    return {
      type: "text" as const,
      id: `text-${i}`,
      content: String(block.content ?? ""),
    };
  });

  return buildBlockTree(flat);
}
