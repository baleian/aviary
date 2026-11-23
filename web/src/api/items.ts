import type { Item } from "./types.ts";

// What a turn has said and done so far, assembled from its events the way
// the API assembles the items of a turn that has ended.
export interface Said {
  items: Item[];
  // The message that the last item is a piece of, if that item is text.
  message?: unknown;
}

export const NOTHING: Said = { items: [] };

// add returns what the turn has said and done after its next event. An
// event that is not of an item returns the same.
export function add(
  said: Said,
  type: string,
  data: Record<string, unknown>,
): Said {
  switch (type) {
    case "message.delta":
    case "thought.delta": {
      if (typeof data.text !== "string" || !data.text) return said;
      const kind = type === "message.delta" ? "message" : "thought";
      const message = data.message_id ?? null;
      const last = said.items.at(-1);
      // The pieces of a message that follow each other are one text.
      if (last?.type === kind && said.message === message) {
        const joined = { ...last, text: last.text + data.text };
        return { items: said.items.with(-1, joined), message };
      }
      const begun: Item = { type: kind, text: data.text };
      return { items: [...said.items, begun], message };
    }
    case "tool.call":
    case "tool.update":
      return { ...said, items: merge(said.items, "tool_call", data) };
    case "plan.update": {
      // The plan stays where it began, as it last stood.
      const plan = { ...data, type: "plan" } as Item;
      const at = said.items.findIndex((item) => item.type === "plan");
      const items = at < 0 ? [...said.items, plan] : said.items.with(at, plan);
      return { ...said, items };
    }
    case "mcp.unsupported": {
      const unsupported = { ...data, type: "mcp_unsupported" } as Item;
      return { ...said, items: [...said.items, unsupported] };
    }
    case "approval.requested":
    case "approval.resolved":
      return { ...said, items: merge(said.items, "approval", data) };
    default:
      return said;
  }
}

// merge puts the fields of an event into the item with its ID, which begins
// with the first event that names it.
function merge(
  items: Item[],
  type: "tool_call" | "approval",
  data: Record<string, unknown>,
): Item[] {
  const at = items.findIndex(
    (item) => item.type === type && item.id === data.id,
  );
  if (at < 0) return [...items, { ...data, type } as Item];
  return items.with(at, { ...items[at], ...data } as Item);
}
