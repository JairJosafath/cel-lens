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

const GRAPHIC_CONTROL = 0xf9;

/** The delay of each frame of a GIF, in milliseconds; empty when the bytes are not a well-formed GIF. */
export function gifDelaysMs(bytes: Uint8Array): number[] {
  if (gifFrameCount(bytes) === 0) {
    return [];
  }
  const screenFlags = bytes[10] ?? 0;
  let at = 13 + (screenFlags & 0x80 ? 3 * 2 ** ((screenFlags & 7) + 1) : 0);
  const delays: number[] = [];
  let pending = 0;
  while (bytes[at] !== TRAILER) {
    if (bytes[at] === EXTENSION_INTRODUCER) {
      if (bytes[at + 1] === GRAPHIC_CONTROL) {
        pending = ((bytes[at + 4] ?? 0) | ((bytes[at + 5] ?? 0) << 8)) * 10;
      }
      at = skipSubBlocks(bytes, at + 2);
    } else {
      const flags = bytes[at + 9] ?? 0;
      at += 10 + (flags & 0x80 ? 3 * 2 ** ((flags & 7) + 1) : 0) + 1;
      at = skipSubBlocks(bytes, at);
      delays.push(pending);
      pending = 0;
    }
  }
  return delays;
}

/** The disposal method (0 to 7) each frame of a GIF asks for; empty when the bytes are not a well-formed GIF. */
export function gifDisposals(bytes: Uint8Array): number[] {
  if (gifFrameCount(bytes) === 0) {
    return [];
  }
  const screenFlags = bytes[10] ?? 0;
  let at = 13 + (screenFlags & 0x80 ? 3 * 2 ** ((screenFlags & 7) + 1) : 0);
  const disposals: number[] = [];
  let pending = 0;
  while (bytes[at] !== TRAILER) {
    if (bytes[at] === EXTENSION_INTRODUCER) {
      if (bytes[at + 1] === GRAPHIC_CONTROL) {
        pending = ((bytes[at + 3] ?? 0) >> 2) & 7;
      }
      at = skipSubBlocks(bytes, at + 2);
    } else {
      const flags = bytes[at + 9] ?? 0;
      at += 10 + (flags & 0x80 ? 3 * 2 ** ((flags & 7) + 1) : 0) + 1;
      at = skipSubBlocks(bytes, at);
      disposals.push(pending);
      pending = 0;
    }
  }
  return disposals;
}

/** Where a GIF keeps its colours: whether it has a global table, and how many frames carry a local one. */
export interface GifColorTables {
  hasGlobal: boolean;
  localCount: number;
}

/** Reads the colour tables of a GIF; no global table and no local ones when the bytes are not a well-formed GIF. */
export function gifColorTables(bytes: Uint8Array): GifColorTables {
  if (gifFrameCount(bytes) === 0) {
    return { hasGlobal: false, localCount: 0 };
  }
  const screenFlags = bytes[10] ?? 0;
  let at = 13 + (screenFlags & 0x80 ? 3 * 2 ** ((screenFlags & 7) + 1) : 0);
  let localCount = 0;
  while (bytes[at] !== TRAILER) {
    if (bytes[at] === EXTENSION_INTRODUCER) {
      at = skipSubBlocks(bytes, at + 2);
    } else {
      const flags = bytes[at + 9] ?? 0;
      localCount += flags & 0x80 ? 1 : 0;
      at += 10 + (flags & 0x80 ? 3 * 2 ** ((flags & 7) + 1) : 0) + 1;
      at = skipSubBlocks(bytes, at);
    }
  }
  return { hasGlobal: (screenFlags & 0x80) !== 0, localCount };
}
