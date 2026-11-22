// A picture made ready for a message (#12973), as the iPhone makes it (MessagePhotos.payload): its long side scaled down
// to max_side, JPEG at 0.75; again at 0.6 and then at 1280 px when it is still over image_bytes. One already within
// max_side and image_bytes goes as it is, so a PNG screenshot stays sharp and a GIF keeps moving. Loaded only when a
// picture is added (it is not on the first screen).
import { imagePayload } from "./attachments";
import type { ImageLimits } from "./image-limits";

/** Types a message carries as they are. */
const AS_IS = new Set(["image/jpeg", "image/png", "image/webp", "image/gif"]);

/** `file` as a message's picture, or null when the page cannot read it or bring it under image_bytes (it goes as a file). */
export async function fitPicture(file: Blob, l: ImageLimits): Promise<{ mime: string; data: string } | null> {
  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(file, { imageOrientation: "from-image" });
  } catch {
    return null;
  }
  try {
    if (Math.max(bitmap.width, bitmap.height) <= l.max_side && file.size <= l.image_bytes && AS_IS.has(file.type)) return await imagePayload(file);
    for (const [side, quality] of [[l.max_side, 0.75], [l.max_side, 0.6], [Math.min(l.max_side, 1280), 0.6]]) {
      const jpeg = await draw(bitmap, side, quality);
      if (jpeg && jpeg.size <= l.image_bytes) return await imagePayload(jpeg);
    }
    return null;
  } finally {
    bitmap.close();
  }
}

/** `bitmap` with its long side at most `side`, on white (a transparent PNG is not black in a JPEG), as a JPEG. */
async function draw(bitmap: ImageBitmap, side: number, quality: number): Promise<Blob | null> {
  const scale = Math.min(1, side / Math.max(bitmap.width, bitmap.height));
  const canvas = document.createElement("canvas");
  canvas.width = Math.max(1, Math.round(bitmap.width * scale));
  canvas.height = Math.max(1, Math.round(bitmap.height * scale));
  const g = canvas.getContext("2d");
  if (!g) return null;
  g.fillStyle = "white";
  g.fillRect(0, 0, canvas.width, canvas.height);
  g.imageSmoothingQuality = "high";
  g.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  return new Promise((ok) => canvas.toBlob(ok, "image/jpeg", quality));
}
