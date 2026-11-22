// Assembles the items of a turn from its events, the way the Supervisor does
// when a turn ends (supervisor/events/items.go).

// add applies the next event to the list of items, and reports whether it
// changed them. data is the decoded data of the event.
function add(items, type, data) {
  switch (type) {
    case "message.delta":
    case "thought.delta": {
      if (!data.text) return false;
      const kind = type === "message.delta" ? "message" : "thought";
      const from = data.message_id ?? null;
      let last = items[items.length - 1];
      if (!last || last.type !== kind || last.from !== from) {
        last = { type: kind, text: "" };
        // from is not part of the item: it tells the pieces of one message
        // from the next message.
        Object.defineProperty(last, "from", { value: from, enumerable: false });
        items.push(last);
      }
      last.text += data.text;
      return true;
    }
    case "tool.call":
    case "tool.update":
      Object.assign(find(items, "tool_call", data.id), data);
      return true;
    case "plan.update": {
      let plan = items.find((item) => item.type === "plan");
      if (!plan) items.push((plan = { type: "plan" }));
      for (const key of Object.keys(plan)) if (key !== "type") delete plan[key];
      Object.assign(plan, data);
      return true;
    }
    case "approval.requested":
    case "approval.resolved":
      Object.assign(find(items, "approval", data.id), data);
      return true;
    default:
      return false;
  }
}

function find(items, type, id) {
  let item = items.find((other) => other.type === type && other.id === id);
  if (!item) items.push((item = { type }));
  return item;
}

// assemble returns the items of a list of events, each {type, data} with data
// as JSON text.
export function assemble(events) {
  const items = [];
  for (const event of events) add(items, event.type, JSON.parse(event.data));
  return items;
}
