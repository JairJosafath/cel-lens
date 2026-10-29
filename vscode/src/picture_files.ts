/** The signature every PNG starts with. */
const PNG_SIGNATURE = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];

/** What the bytes of a picture file are, judged by their header. */
export type PictureKind = 'png' | 'gif' | 'unknown';

const IMAGE_DESCRIPTOR = 0x2c;
const EXTENSION_INTRODUCER = 0x21;
const TRAILER = 0x3b;

/** Reads the kind of an image file from its header, not its name. */
export function pictureKind(bytes: Uint8Array): PictureKind {
  if (PNG_SIGNATURE.every((byte, index) => bytes[index] === byte)) {
    return 'png';
  }
  const head = String.fromCharCode(...bytes.subarray(0, 6));
  return head === 'GIF87a' || head === 'GIF89a' ? 'gif' : 'unknown';
}

/** Skips the data sub-blocks that start at `offset`; gives the offset after them. */
function skipSubBlocks(bytes: Uint8Array, offset: number): number {
  let at = offset;
  for (let size = bytes[at] ?? 0; size > 0; size = bytes[at] ?? 0) {
    at += size + 1;
  }
  return at + 1;
}

/** Counts the frames of a GIF; 0 when the bytes are not a well-formed GIF. */
export function gifFrameCount(bytes: Uint8Array): number {
  if (pictureKind(bytes) !== 'gif' || bytes.length < 13) {
    return 0;
  }
  const screenFlags = bytes[10] ?? 0;
  let at = 13 + (screenFlags & 0x80 ? 3 * 2 ** ((screenFlags & 7) + 1) : 0);
  let frames = 0;
  while (at < bytes.length) {
    const block = bytes[at];
    if (block === TRAILER) {
      return frames;
    }
    if (block === EXTENSION_INTRODUCER) {
      at = skipSubBlocks(bytes, at + 2);
    } else if (block === IMAGE_DESCRIPTOR) {
      const flags = bytes[at + 9] ?? 0;
      at += 10 + (flags & 0x80 ? 3 * 2 ** ((flags & 7) + 1) : 0) + 1;
      at = skipSubBlocks(bytes, at);
      frames += 1;
    } else {
      return 0;
    }
  }
  return 0;
}
