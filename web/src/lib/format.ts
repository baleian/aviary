// How the console writes times, names and messages.

const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];
const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

const two = (n: number) => String(n).padStart(2, "0");

export function clock(value: string | Date): string {
  const date = new Date(value);
  return two(date.getHours()) + ":" + two(date.getMinutes());
}

// when returns a short form of a time: "Today 14:02", "Yesterday 09:15",
// "Mon 16:20" within a week, and then "Nov 12", with the year if it is
// another one.
export function when(value: string | Date, now = new Date()): string {
  const date = new Date(value);
  const midnight = (day: Date) =>
    new Date(day.getFullYear(), day.getMonth(), day.getDate()).getTime();
  const days = Math.round((midnight(now) - midnight(date)) / 86400000);
  if (days === 0) return "Today " + clock(date);
  if (days === 1) return "Yesterday " + clock(date);
  if (days > 1 && days < 7) return DAYS[date.getDay()] + " " + clock(date);
  const day = MONTHS[date.getMonth()] + " " + date.getDate();
  return date.getFullYear() === now.getFullYear()
    ? day
    : day + ", " + date.getFullYear();
}

export function fullTime(value: string | Date): string {
  const date = new Date(value);
  const day = `${MONTHS[date.getMonth()]} ${date.getDate()}, ${date.getFullYear()}`;
  return `${day}, ${clock(date)}:${two(date.getSeconds())}`;
}

// initials returns one or two letters for a name or a username.
export function initials(name: string): string {
  const words = (name || "?")
    .replace(/@.*/, "")
    .split(/[\s._-]+/)
    .filter(Boolean);
  const letters =
    words.length > 1
      ? words[0][0] + words[words.length - 1][0]
      : (words[0] || "?").slice(0, 2);
  return letters.toUpperCase();
}

// pick returns a number below count that stays the same for a text, so that
// the same person always has the same colour.
export function pick(text: string, count: number): number {
  let sum = 0;
  for (const letter of text) sum = (sum * 31 + letter.codePointAt(0)!) >>> 0;
  return sum % count;
}

// sentence makes a message of the API a sentence: its messages begin in
// lower case and end without a full stop.
export function sentence(message: string): string {
  const text = (message || "Something went wrong").trim();
  const stop = /[.!?…]$/.test(text) ? "" : ".";
  return text[0].toUpperCase() + text.slice(1) + stop;
}

// label returns the name to show for a project or an agent: its name, or its
// slug if it has none.
export function label(record: { name: string; slug: string }): string {
  return record.name || record.slug;
}

export function num(value: number): string {
  return value.toLocaleString("en-US");
}

// plural counts things in words: "1 member", "3 members".
export function plural(count: number, one: string, many = one + "s"): string {
  return `${num(count)} ${count === 1 ? one : many}`;
}

// duration writes a length of time: "42s", "3m 07s", "1h 05m".
export function duration(ms: number): string {
  const seconds = Math.max(0, Math.round(ms / 1000));
  if (seconds < 60) return seconds + "s";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${two(seconds % 60)}s`;
  return `${Math.floor(minutes / 60)}h ${two(minutes % 60)}m`;
}

// clip writes a text on one line, cut short if it is longer than longest
// characters.
export function clip(text: string, longest: number): string {
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length > longest
    ? flat.slice(0, longest - 1).trimEnd() + "…"
    : flat;
}

// transportName names how an MCP server is reached.
export function transportName(transport: "http" | "sse"): string {
  return transport === "sse" ? "SSE" : "Streamable HTTP";
}
