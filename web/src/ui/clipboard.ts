import { toast } from "./toast.ts";

// copy puts a text on the clipboard, and says whether it did. what names
// the text, such as "link".
export async function copy(text: string, what: string) {
  try {
    await navigator.clipboard.writeText(text);
    toast(`Copied the ${what}`, "ok", 1800);
  } catch {
    toast(`Could not copy the ${what}`, "ok", 1800);
  }
}
