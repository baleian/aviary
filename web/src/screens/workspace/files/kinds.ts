import { type Kind, kindOf } from "../../../lib/files.ts";
import type { IconName } from "../../../ui/icons.ts";

// How a file of a workspace is looked at: as code, or as what it makes.
export type Look = "code" | "markdown" | "page" | "image" | "pdf";

export interface Sort {
  kind: Kind;
  // How the file shows. A file whose name does not tell shows as code if it
  // turns out to be text.
  look?: Look;
  // Whether the file can also show as it is written.
  written: boolean;
  glyph: IconName;
}

// The extensions of files that are code or data, which the tree marks.
const CODE = new Set(
  (
    "json jsonl yaml yml toml xml html htm css scss js mjs cjs jsx ts tsx py " +
    "go rs java kt c h cc cpp hpp cs rb php swift sh bash zsh sql tf proto " +
    "graphql gradle ipynb"
  ).split(" "),
);

// sortOf tells how a file is looked at, from its name.
export function sortOf(name: string): Sort {
  const kind = kindOf(name);
  const { ext } = kind;
  if (ext === "md" || ext === "markdown")
    return { kind, look: "markdown", written: true, glyph: "doc" };
  if (ext === "html" || ext === "htm")
    return { kind, look: "page", written: true, glyph: "code" };
  if (kind.view === "image")
    return { kind, look: "image", written: ext === "svg", glyph: "image" };
  if (kind.view === "pdf")
    return { kind, look: "pdf", written: false, glyph: "doc" };
  if (kind.view === "text" || ext === "ipynb")
    return {
      kind,
      look: "code",
      written: false,
      glyph: CODE.has(ext) ? "code" : "doc",
    };
  // What is known not to be text has no look. Any other may be text.
  return { kind, written: false, glyph: "file" };
}

// How much of a file is read to tell whether it is text, in bytes.
export const SNIFFED = 8192;

// isText tells whether the bytes are those of a text: a text has no byte
// that is zero.
export async function isText(bytes: Blob): Promise<boolean> {
  const read = new Uint8Array(await bytes.slice(0, SNIFFED).arrayBuffer());
  return !read.includes(0);
}
