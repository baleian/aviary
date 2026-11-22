import { isSlug } from "./slug.ts";

// What a form does not accept of what a person entered. Its message is shown
// to them.
export class Wrong extends Error {}

// named reads the name and the slug of a form, and throws if the slug is not
// one.
export function named(data: FormData): { slug: string; name: string } {
  const slug = text(data, "slug");
  if (!slug) throw new Wrong("Enter a slug.");
  if (!isSlug(slug)) {
    throw new Wrong(
      "A slug is 1 to 63 lowercase letters, digits and hyphens. It begins with a letter and does not end with a hyphen.",
    );
  }
  return { slug, name: text(data, "name") };
}

// text reads a field of a form, without the space around it.
export function text(data: FormData, field: string): string {
  const value = data.get(field);
  return typeof value === "string" ? value.trim() : "";
}
