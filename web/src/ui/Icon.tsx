import type { SVGProps } from "react";
import type { IconName, SymbolName } from "./icons.ts";

// Icon shows an icon of the sprite. className adds to its class, such as
// "lg" for a larger one.
export function Icon({
  name,
  className,
}: {
  name: IconName;
  className?: string;
}) {
  return (
    <svg className={className ? `i ${className}` : "i"} aria-hidden="true">
      <use href={`#i-${name}`} />
    </svg>
  );
}

// Use shows another symbol of the sprite, such as the logo.
export function Use({
  symbol,
  ...props
}: { symbol: SymbolName } & SVGProps<SVGSVGElement>) {
  return (
    <svg aria-hidden="true" {...props}>
      <use href={`#${symbol}`} />
    </svg>
  );
}
