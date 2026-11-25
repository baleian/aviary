// What the web UI makes of a file from its name and its media type.

// How the web UI can show a file by itself.
export type View = "image" | "text";

// What files of a kind have in common, which colours their tile.
export type Family =
  | "image"
  | "pdf"
  | "sheet"
  | "slides"
  | "doc"
  | "media"
  | "archive"
  | "text"
  | "other";

export interface Kind {
  // The extension of the name, in lower case and without its dot: "" if it
  // has none.
  ext: string;
  family: Family;
  // How the file is shown, if the web UI can show it.
  view?: View;
  // The media type that a browser draws a picture as.
  type?: string;
}

// The pictures that browsers draw, and the media type of each.
const PICTURES: Record<string, string> = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
  avif: "image/avif",
  bmp: "image/bmp",
  svg: "image/svg+xml",
  ico: "image/x-icon",
};

const FAMILIES: [Family, string][] = [
  ["image", "heic heif tif tiff psd raw"],
  ["pdf", "pdf"],
  ["sheet", "xlsx xls xlsm ods numbers csv tsv"],
  ["slides", "pptx ppt odp key"],
  ["doc", "docx doc odt rtf pages hwp hwpx"],
  ["media", "mp3 wav m4a ogg flac aac mp4 mov webm mkv avi"],
  ["archive", "zip tar gz tgz bz2 xz zst 7z rar"],
];

// The extensions of files that are text.
const TEXTS = new Set(
  (
    "txt text log md markdown json jsonl ndjson csv tsv yaml yml toml ini conf " +
    "cfg env xml html htm css scss js mjs cjs jsx ts tsx py go rs java kt c h " +
    "cc cpp hpp cs rb php swift sh bash zsh sql diff patch tf proto graphql " +
    "gradle properties lock"
  ).split(" "),
);

const BY_EXT = new Map<string, Family>(
  FAMILIES.flatMap(([family, exts]) =>
    exts.split(" ").map((ext): [string, Family] => [ext, family]),
  ),
);

// split returns a name in two parts, of which the second is short and holds
// the extension: a name that does not fit is cut at the end of the first.
export function split(name: string): [string, string] {
  const dot = name.lastIndexOf(".");
  const ext = dot > 0 && name.length - dot <= 9 ? name.length - dot : 0;
  // The last of the stem tells names apart that begin alike: "… (2).png".
  const kept = name.slice(-Math.min(name.length, ext + 3)).trimStart();
  return name.length - kept.length < 8
    ? [name, ""]
    : [name.slice(0, -kept.length), kept];
}

// kindOf tells what a file is from its name, or else from the media type
// that whoever uploaded it gave.
export function kindOf(name: string, mediaType = ""): Kind {
  const dot = name.lastIndexOf(".");
  const ext = dot > 0 ? name.slice(dot + 1).toLowerCase() : "";
  const type = mediaType.split(";")[0].trim().toLowerCase();
  if (PICTURES[ext]) {
    return { ext, family: "image", view: "image", type: PICTURES[ext] };
  }
  const family = BY_EXT.get(ext);
  if (TEXTS.has(ext)) return { ext, family: family ?? "text", view: "text" };
  if (family) return { ext, family };
  // The name does not tell.
  if (Object.values(PICTURES).includes(type)) {
    return { ext, family: "image", view: "image", type };
  }
  if (type === "application/pdf") return { ext, family: "pdf" };
  if (type.startsWith("text/") || type === "application/json") {
    return { ext, family: "text", view: "text" };
  }
  return { ext, family: "other" };
}
