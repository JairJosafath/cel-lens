// Tooling for test/pictures/take.ts; it lives in src/ only so vitest's include finds its test.
// Turns the frames of a recording into palette indices for a GIF: one palette for
// every frame, and a pixel counts as unchanged when its palette index is the same.
// Per-frame palettes (or comparing raw JPEG pixels) let a pixel kept from an earlier
// frame and one rewritten later differ by a few levels, which leaves a ghost.
export type Palette = number[][];

/** The colour reduction of the GIF encoder, handed in so this module stays free of it. */
export interface Quantizer {
  quantize: (rgba: Uint8Array, maxColors: number) => Palette;
  applyPalette: (rgba: Uint8Array, palette: Palette) => Uint8Array;
}

/** Every this-many-th pixel of each frame feeds the palette: enough to see every colour. */
const PALETTE_SAMPLE_STEP = 8;

export interface RawFrame {
  readonly width: number;
  readonly height: number;
  /** RGBA bytes. */
  readonly data: Uint8Array;
}

export interface IndexedFrame {
  readonly width: number;
  readonly height: number;
  /** Palette index of each pixel; the transparent index where the pixel is the one of the frame before. */
  readonly index: Uint8Array;
}

export interface IndexedFrames {
  /** at most `colors` entries; the last is the transparent one. Colour matching excludes it: pixels are matched against the quantized entries only. */
  readonly palette: Palette;
  readonly transparentIndex: number;
  readonly frames: readonly IndexedFrame[];
}

function samplePixels(frames: readonly RawFrame[]): Uint8Array {
  const parts: Uint8Array[] = [];
  for (const { data } of frames) {
    const pixels = Math.floor(data.length / 4);
    const sample = new Uint8Array(Math.ceil(pixels / PALETTE_SAMPLE_STEP) * 4);
    for (let pixel = 0, out = 0; pixel < pixels; pixel += PALETTE_SAMPLE_STEP, out += 4) {
      sample.set(data.subarray(pixel * 4, pixel * 4 + 4), out);
    }
    parts.push(sample);
  }
  const all = new Uint8Array(parts.reduce((sum, part) => sum + part.length, 0));
  let at = 0;
  for (const part of parts) {
    all.set(part, at);
    at += part.length;
  }
  return all;
}

/**
 * Quantizes all `frames` with one palette of `colors` entries (the last stands for
 * "unchanged"). Frames after the first keep the transparent index wherever their
 * quantized colour equals the one of the frame before.
 */
export function indexFrames(
  frames: readonly RawFrame[],
  colors: number,
  { quantize, applyPalette }: Quantizer,
): IndexedFrames {
  const palette = quantize(samplePixels(frames), colors - 1);
  const quantized = palette.slice();
  palette.push([0, 0, 0]);
  const transparentIndex = palette.length - 1;
  const indexed: IndexedFrame[] = [];
  let previous: Uint8Array | undefined;
  for (const { width, height, data } of frames) {
    const full = applyPalette(data, quantized);
    const index = previous === undefined ? full : full.slice();
    if (previous !== undefined) {
      for (let pixel = 0; pixel < index.length; pixel += 1) {
        if (full[pixel] === previous[pixel]) {
          index[pixel] = transparentIndex;
        }
      }
    }
    indexed.push({ width, height, index });
    previous = full;
  }
  return { palette, transparentIndex, frames: indexed };
}
