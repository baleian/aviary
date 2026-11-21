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
