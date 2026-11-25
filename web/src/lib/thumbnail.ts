import type { Kind } from "./files.ts";

// The side of a thumbnail, in pixels: twice that of the tile it is shown
// in, for the screens that have the pixels.
const SIDE = 96;

// thumbnail draws a small square picture of a file: the middle of a picture.
// It returns the picture itself, as a data URL, or null for a file that the
// browser cannot draw.
export async function thumbnail(
  file: Blob,
  kind: Kind,
): Promise<string | null> {
  if (kind.view !== "image") return null;
  // The file is drawn as what its name says it is, whatever it came as.
  const address = URL.createObjectURL(file.slice(0, file.size, kind.type));
  try {
    const picture = new Image();
    picture.src = address;
    await picture.decode();
    return square(picture, picture.naturalWidth, picture.naturalHeight);
  } catch {
    // The file is not what its name says, or the browser does not draw it.
    return null;
  } finally {
    URL.revokeObjectURL(address);
  }
}

// square returns the middle of a picture, as much of it as fills a square.
function square(
  picture: CanvasImageSource,
  width: number,
  height: number,
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
    (SIDE - height * scale) / 2,
    width * scale,
    height * scale,
  );
  return canvas.toDataURL("image/webp", 0.85);
}
