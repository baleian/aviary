import { Icon } from "./Icon.tsx";
import { toast } from "./toast.ts";

// CopyButton copies a text, such as an ID, and says whether it did. what
// names the text.
export function CopyButton({
  text,
  what = "ID",
}: {
  text: string;
  what?: string;
}) {
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      toast(`Copied the ${what}`, "ok", 1800);
    } catch {
      toast(`Could not copy the ${what}`, "ok", 1800);
    }
  }
  return (
    <button
      className="ib sm"
      type="button"
      title={`Copy the ${what}`}
      onClick={(event) => {
        event.stopPropagation();
        void copy();
      }}
    >
      <Icon name="copy" />
    </button>
  );
}
