// Tooling for test/pictures/take.ts; it lives in src/ only so vitest's include finds its test.
// Reads the pixels of the PNG frames the browser's screencast sends. A PNG is lossless, so a
// flat area of the page is the same colour in every pixel and every frame; a JPEG frame is not,
// and its noise around the text became blotches in the GIFs.
import { inflateSync } from 'node:zlib';

export interface PngPixels {
  readonly width: number;
  readonly height: number;
  /** RGBA bytes. */
  readonly data: Uint8Array;
}

const SIGNATURE = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
const RGB = 2;
const RGBA = 6;
const SAMPLES: Readonly<Record<number, number>> = { [RGB]: 3, [RGBA]: 4 };

/** The Paeth predictor of PNG's filter type 4. */
function paeth(left: number, up: number, upLeft: number): number {
  const estimate = left + up - upLeft;
  const toLeft = Math.abs(estimate - left);
  const toUp = Math.abs(estimate - up);
  const toUpLeft = Math.abs(estimate - upLeft);
  if (toLeft <= toUp && toLeft <= toUpLeft) {
    return left;
  }
  return toUp <= toUpLeft ? up : upLeft;
}

/** Undoes the filter of each row, in place, and gives the rows without their filter bytes. */
function unfilter(
  raw: Uint8Array,
  width: number,
  height: number,
  bytesPerPixel: number,
): Uint8Array {
  const stride = width * bytesPerPixel;
  if (raw.length !== (stride + 1) * height) {
    throw new Error('The PNG frame has the wrong amount of pixel data.');
  }
  const rows = new Uint8Array(stride * height);
  for (let row = 0; row < height; row += 1) {
    const filter = raw[row * (stride + 1)] ?? 0;
    const from = row * (stride + 1) + 1;
    const to = row * stride;
    for (let at = 0; at < stride; at += 1) {
      const left = at >= bytesPerPixel ? (rows[to + at - bytesPerPixel] ?? 0) : 0;
      const up = row > 0 ? (rows[to - stride + at] ?? 0) : 0;
      const upLeft =
        row > 0 && at >= bytesPerPixel ? (rows[to - stride + at - bytesPerPixel] ?? 0) : 0;
      const value = raw[from + at] ?? 0;
      switch (filter) {
        case 0:
          rows[to + at] = value;
          break;
        case 1:
          rows[to + at] = value + left;
          break;
        case 2:
          rows[to + at] = value + up;
          break;
        case 3:
          rows[to + at] = value + ((left + up) >> 1);
          break;
        case 4:
          rows[to + at] = value + paeth(left, up, upLeft);
          break;
        default:
          throw new Error(`The PNG frame has a row with the unknown filter ${String(filter)}.`);
      }
    }
  }
  return rows;
}

/**
 * The pixels of an 8-bit RGB or RGBA PNG that is not interlaced, which is what Chromium's
 * screencast sends; throws on any other PNG, naming what it is.
 */
export function decodePng(png: Uint8Array): PngPixels {
  if (!SIGNATURE.every((byte, index) => png[index] === byte)) {
    throw new Error('The screencast frame is not a PNG.');
  }
  const view = new DataView(png.buffer, png.byteOffset, png.byteLength);
  let width = 0;
  let height = 0;
  let colorType = -1;
  const compressed: Uint8Array[] = [];
  for (let at = SIGNATURE.length; at + 8 <= png.length;) {
    const size = view.getUint32(at);
    const type = String.fromCharCode(...png.subarray(at + 4, at + 8));
    const body = png.subarray(at + 8, at + 8 + size);
    if (type === 'IHDR') {
      width = view.getUint32(at + 8);
      height = view.getUint32(at + 12);
      const [depth, color, , , interlace] = body.subarray(8, 13);
      colorType = color ?? -1;
      if (depth !== 8 || SAMPLES[colorType] === undefined || interlace !== 0) {
        throw new Error(
          `The PNG frame is ${String(depth)}-bit, colour type ${String(colorType)}, interlace ${String(interlace)}: only 8-bit RGB and RGBA without interlace are read.`,
        );
      }
    } else if (type === 'IDAT') {
      compressed.push(body);
    }
    at += 12 + size;
  }
  const bytesPerPixel = SAMPLES[colorType];
  if (bytesPerPixel === undefined || width === 0 || height === 0) {
    throw new Error('The PNG frame has no header.');
  }
  const rows = unfilter(
    new Uint8Array(inflateSync(Buffer.concat(compressed))),
    width,
    height,
    bytesPerPixel,
  );
  if (bytesPerPixel === 4) {
    return { width, height, data: rows };
  }
  const data = new Uint8Array(width * height * 4);
  for (let pixel = 0; pixel < width * height; pixel += 1) {
    data.set(rows.subarray(pixel * 3, pixel * 3 + 3), pixel * 4);
    data[pixel * 4 + 3] = 255;
  }
  return { width, height, data };
}
