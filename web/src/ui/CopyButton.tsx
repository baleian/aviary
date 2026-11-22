import { copy } from "./clipboard.ts";
import { Icon } from "./Icon.tsx";

// CopyButton copies a text, such as an ID, and says whether it did. what
// names the text.
export function CopyButton({
  text,
  what = "ID",
}: {
  text: string;
  what?: string;
}) {
  return (
    <button
      className="ib sm"
      type="button"
      title={`Copy the ${what}`}
      onClick={(event) => {
        event.stopPropagation();
        void copy(text, what);
      }}
    >
      <Icon name="copy" />
    </button>
  );
}
