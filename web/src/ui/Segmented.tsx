import { cls } from "../lib/cls.ts";
import { Icon } from "./Icon.tsx";
import type { IconName } from "./icons.ts";

// Segmented is a choice among a few values, each a button with its text and
// an icon if it has one.
export function Segmented<T extends string>({
  choices,
  value,
  onChange,
  size,
}: {
  choices: [value: T, text: string, glyph?: IconName][];
  value: T;
  onChange: (value: T) => void;
  size?: "lg";
}) {
  return (
    <span className={cls("seg", size)} role="group">
      {choices.map(([choice, text, glyph]) => (
        <button
          key={choice}
          type="button"
          aria-pressed={choice === value}
          onClick={() => onChange(choice)}
        >
          {glyph && <Icon name={glyph} />}
          {text}
        </button>
      ))}
    </span>
  );
}
