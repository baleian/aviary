// What the console reads out of a tool call. Harnesses shape the input and
// the output of their tools differently, and these are the fields they use.

// findDiff looks for a unified diff in the input or the output of a tool
// call.
export function findDiff(value: unknown, depth = 0): string | null {
  if (typeof value === "string") {
    const diff = /^(@@ |--- |\+\+\+ )/m.test(value) && /^[+-]/m.test(value);
    return diff ? value : null;
  }
  if (!value || typeof value !== "object" || depth > 3) return null;
  for (const inner of Object.values(value)) {
    const found = findDiff(inner, depth + 1);
    if (found) return found;
  }
  return null;
}

// changed makes a diff of an edit that gives the text before and after.
export function changed(input: Record<string, unknown>): string | null {
  const before = input.oldString ?? input.old_string ?? input.old_str;
  const after = input.newString ?? input.new_string ?? input.new_str;
  if (typeof before !== "string" || typeof after !== "string") return null;
  const lines = (text: string, sign: string) =>
    text ? text.split("\n").map((line) => sign + " " + line) : [];
  return ["@@", ...lines(before, "-"), ...lines(after, "+")].join("\n");
}

// outputText returns the text of a tool's output, if it has one.
export function outputText(output: unknown): string {
  if (typeof output === "string") return output;
  if (!output || typeof output !== "object") return "";
  const fields = output as Record<string, unknown>;
  for (const key of [
    "output",
    "stdout",
    "formatted_output",
    "aggregated_output",
    "content",
    "error",
    "stderr",
  ]) {
    const text = fields[key];
    if (typeof text === "string" && text) return text;
  }
  return "";
}

export function exitCode(output: unknown): number | null {
  const fields = (output ?? {}) as {
    exit_code?: unknown;
    metadata?: { exit?: unknown };
  };
  const code = fields.exit_code ?? fields.metadata?.exit;
  return typeof code === "number" ? code : null;
}

// pathOf returns the file that a tool call is about, if its input names one.
export function pathOf(input: Record<string, unknown>): string {
  const path = input.filePath || input.file_path || input.path;
  return typeof path === "string" ? path : "";
}

// inputOf returns the input of a tool call as fields, or none.
export function inputOf(input: unknown): Record<string, unknown> {
  return input && typeof input === "object"
    ? (input as Record<string, unknown>)
    : {};
}

// outsideOf returns the folders outside the workspace that a tool call
// reaches, if its input names them. OpenCode asks for them before it asks
// for the command that reaches them.
export function outsideOf(input: Record<string, unknown>): string[] {
  const folders = input.directories;
  if (!Array.isArray(folders)) return [];
  return folders.filter((folder) => typeof folder === "string");
}
