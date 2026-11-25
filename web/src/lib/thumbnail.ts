import type { Kind } from "./files.ts";

// The side of a thumbnail, in pixels: twice that of the tile it is shown
// in, for the screens that have the pixels.
const SIDE = 96;

// thumbnail draws a small square picture of a file: the middle of a picture,
// or the top of the first page of a PDF. It returns the picture itself, as a
// data URL, or null for a file that the browser cannot draw.
export async function thumbnail(
  file: Blob,
  kind: Kind,
): Promise<string | null> {
  try {
    if (kind.view === "pdf") return await ofPage(file);
    if (kind.view === "image") return await ofPicture(file, kind.type);
  } catch {
    // The file is not what its name says, or the browser does not draw it.
  }
  return null;
}

async function ofPicture(file: Blob, type = ""): Promise<string | null> {
  // The file is drawn as what its name says it is, whatever it came as.
  const address = URL.createObjectURL(file.slice(0, file.size, type));
  try {
    const picture = new Image();
    picture.src = address;
    await picture.decode();
    return square(picture, picture.naturalWidth, picture.naturalHeight, 0.5);
  } finally {
    URL.revokeObjectURL(address);
  }
}

async function ofPage(file: Blob): Promise<string | null> {
  const { open } = await import("./pdf.ts");
  const pages = await open(file);
  try {
    const canvas = document.createElement("canvas");
    await pages.draw(1, canvas, 2 * SIDE).done;
    return square(canvas, canvas.width, canvas.height, 0);
  } finally {
    pages.close();
  }
}

// square returns as much of a picture as fills a square: from its top if at
// is 0, and from its middle if at is 0.5.
function square(
  picture: CanvasImageSource,
  width: number,
  height: number,
  at: number,
): string | null {
  if (!width || !height) return null;
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = SIDE;
  const context = canvas.getContext("2d");
  if (!context) return null;
  const scale = SIDE / Math.min(width, height);
  context.imageSmoothingQuality = "high";
  context.drawImage(
    picture,
    (SIDE - width * scale) / 2,
    (SIDE - height * scale) * at,
    width * scale,
    height * scale,
  );
  return canvas.toDataURL("image/webp", 0.85);
}
