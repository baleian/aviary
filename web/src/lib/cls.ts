// cls joins the class names that are given, and leaves out what is not one.
export function cls(...names: (string | false | null | undefined)[]): string {
  return names.filter(Boolean).join(" ");
}
