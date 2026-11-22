import type { ReactNode } from "react";

// Chip is a small label of a state, in the colour of the state.
export function Chip({
  tone,
  children,
}: {
  tone: string;
  children: ReactNode;
}) {
  return <span className={`chip st-${tone}`}>{children}</span>;
}
