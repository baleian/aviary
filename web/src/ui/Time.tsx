import { fullTime, when } from "../lib/format.ts";

// Time shows a time in its short form and, on hover, in full.
export function Time({ value }: { value: string }) {
  return (
    <time dateTime={new Date(value).toISOString()} title={fullTime(value)}>
      {when(value)}
    </time>
  );
}
