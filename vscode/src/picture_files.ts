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

/** The size in bytes of the colour table a flags byte announces; 0 when it announces none. */
function colorTableSize(flags: number): number {
  return flags & 0x80 ? 3 * 2 ** ((flags & 7) + 1) : 0;
}

const GRAPHIC_CONTROL = 0xf9;

/** One image of a GIF with the settings of the graphic control extension before it. */
interface GifFrame {
  hasLocalTable: boolean;
  delayMs: number;
  disposal: number;
}

/** Where a GIF keeps its colours: whether it has a global table, and how many frames carry a local one. */
export interface GifColorTables {
  hasGlobal: boolean;
  localCount: number;
}

/** Walks the blocks of a GIF; gives its frames and global table flag, or undefined when the bytes are not a well-formed GIF with a frame. */
function walkGif(bytes: Uint8Array): { frames: GifFrame[]; hasGlobal: boolean } | undefined {
  if (pictureKind(bytes) !== 'gif' || bytes.length < 13) {
    return undefined;
  }
  const screenFlags = bytes[10] ?? 0;
  let at = 13 + colorTableSize(screenFlags);
  const frames: GifFrame[] = [];
  let delayMs = 0;
  let disposal = 0;
  while (at < bytes.length) {
    const block = bytes[at];
    if (block === TRAILER) {
      return frames.length > 0 ? { frames, hasGlobal: (screenFlags & 0x80) !== 0 } : undefined;
    }
    if (block === EXTENSION_INTRODUCER) {
      if (bytes[at + 1] === GRAPHIC_CONTROL) {
        disposal = ((bytes[at + 3] ?? 0) >> 2) & 7;
        delayMs = ((bytes[at + 4] ?? 0) | ((bytes[at + 5] ?? 0) << 8)) * 10;
      }
      at = skipSubBlocks(bytes, at + 2);
    } else if (block === IMAGE_DESCRIPTOR) {
      const flags = bytes[at + 9] ?? 0;
      frames.push({ hasLocalTable: (flags & 0x80) !== 0, delayMs, disposal });
      delayMs = 0;
      disposal = 0;
      at = skipSubBlocks(bytes, at + 10 + colorTableSize(flags) + 1);
    } else {
      return undefined;
    }
  }
  return undefined;
}

/** Counts the frames of a GIF; 0 when the bytes are not a well-formed GIF. */
export function gifFrameCount(bytes: Uint8Array): number {
  return walkGif(bytes)?.frames.length ?? 0;
}

/** The delay of each frame of a GIF, in milliseconds; empty when the bytes are not a well-formed GIF. */
export function gifDelaysMs(bytes: Uint8Array): number[] {
  return walkGif(bytes)?.frames.map((frame) => frame.delayMs) ?? [];
}

/** The disposal method (0 to 7) each frame of a GIF asks for; empty when the bytes are not a well-formed GIF. */
export function gifDisposals(bytes: Uint8Array): number[] {
  return walkGif(bytes)?.frames.map((frame) => frame.disposal) ?? [];
}

/** Reads the colour tables of a GIF; no global table and no local ones when the bytes are not a well-formed GIF. */
export function gifColorTables(bytes: Uint8Array): GifColorTables {
  const gif = walkGif(bytes);
  if (gif === undefined) {
    return { hasGlobal: false, localCount: 0 };
  }
  return {
    hasGlobal: gif.hasGlobal,
    localCount: gif.frames.filter((frame) => frame.hasLocalTable).length,
  };
}
