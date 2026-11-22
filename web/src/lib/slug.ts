const SLUG = /^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$/;

// isSlug reports whether a text can be the slug of a project or an agent: 1
// to 63 lowercase letters, digits and hyphens, beginning with a letter and
// not ending with a hyphen.
export function isSlug(text: string): boolean {
  return SLUG.test(text);
}

// slugify suggests a slug for a name.
export function slugify(name: string): string {
  return (
    name
      .toLowerCase()
      .normalize("NFKD")
      // The accents, which the line above has parted from their letters.
      .replace(/[\u0300-\u036f]/g, "")
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^[^a-z]+/, "")
      .replace(/-+$/, "")
      .slice(0, 63)
      .replace(/-+$/, "")
  );
}
