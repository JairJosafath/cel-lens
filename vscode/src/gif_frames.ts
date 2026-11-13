// Tooling for test/pictures/take.ts; it lives in src/ only so vitest's include finds its test.
// Turns the frames of a recording into palette indices for a GIF: one palette for
// every frame, and a pixel counts as unchanged when its palette index is the same.
// Per-frame palettes (or comparing raw pixels) let a pixel kept from an earlier
// frame and one rewritten later differ by a few levels, which leaves a ghost. The flat colours
// of the page (the editor's background, the tab bar's) are palette entries of their own, exact,
// so no quantizer can smear one of them into a blotch of its neighbours.
export type Palette = number[][];

/** The colour reduction of the GIF encoder, handed in so this module stays free of it. */
export interface Quantizer {
  quantize: (rgba: Uint8Array, maxColors: number) => Palette;
}

/** Every this-many-th pixel of each frame feeds the palette: enough to see every colour. */
const PALETTE_SAMPLE_STEP = 8;

/** Flat colours this close (in every channel) are one colour: noise around a flat colour is not a second one. */
const SAME_FLAT_COLOR_LEVELS = 4;

/** A colour that fills at least this share of the sampled pixels is a flat colour of the page. */
const FLAT_COLOR_SHARE = 0.01;

/** The most flat colours that get an exact palette entry: a background, a bar, a panel, a status bar. */
const MAX_FLAT_COLORS = 8;

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

/** The colours that fill a share of the sampled pixels, the most frequent first. */
function flatColors(sample: Uint8Array): number[][] {
  const counts = new Map<number, number>();
  const pixels = Math.floor(sample.length / 4);
  for (let pixel = 0; pixel < pixels; pixel += 1) {
    const key =
      ((sample[pixel * 4] ?? 0) << 16) |
      ((sample[pixel * 4 + 1] ?? 0) << 8) |
      (sample[pixel * 4 + 2] ?? 0);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return [...counts]
    .filter(([, count]) => count >= pixels * FLAT_COLOR_SHARE)
    .sort((a, b) => b[1] - a[1])
    .slice(0, MAX_FLAT_COLORS)
    .map(([key]) => [(key >> 16) & 255, (key >> 8) & 255, key & 255])
    .reduce<number[][]>(
      (kept, color) =>
        kept.some((other) =>
          color.every(
            (level, channel) => Math.abs(level - (other[channel] ?? 0)) <= SAME_FLAT_COLOR_LEVELS,
          ),
        )
          ? kept
          : [...kept, color],
      [],
    );
}

function distance(a: readonly number[], b: readonly number[]): number {
  return a.reduce((sum, level, channel) => sum + (level - (b[channel] ?? 0)) ** 2, 0);
}

/**
 * The palette with each flat colour as an entry of its own: the nearest entry that is not one
 * already is replaced by it. A quantizer averages the colours of a bucket, so a background it
 * shares with a few text pixels would otherwise come out a level or two off, or as two entries.
 */
function withFlatColors(palette: Palette, flat: readonly number[][]): Palette {
  const pinned = new Set<number>();
  const result = palette.map((entry) => entry.slice());
  for (const color of flat) {
    let nearest = -1;
    for (const [entry, candidate] of result.entries()) {
      if (
        !pinned.has(entry) &&
        (nearest < 0 || distance(candidate, color) < distance(result[nearest] ?? [], color))
      ) {
        nearest = entry;
      }
    }
    if (nearest >= 0) {
      result[nearest] = color;
      pinned.add(nearest);
    }
  }
  return result;
}

/**
 * The index of the palette entry nearest to each pixel, by its exact colour (a colour of the
 * palette maps to itself, which gifenc's own mapping, working on 5 or 6 bits a channel, does not
 * promise). Equal colours are looked up once.
 */
function applyExactly(data: Uint8Array, palette: Palette): Uint8Array {
  const nearest = new Map<number, number>();
  const index = new Uint8Array(Math.floor(data.length / 4));
  for (let pixel = 0; pixel < index.length; pixel += 1) {
    const red = data[pixel * 4] ?? 0;
    const green = data[pixel * 4 + 1] ?? 0;
    const blue = data[pixel * 4 + 2] ?? 0;
    const key = (red << 16) | (green << 8) | blue;
    let found = nearest.get(key);
    if (found === undefined) {
      found = 0;
      let best = Infinity;
      for (const [entry, color] of palette.entries()) {
        const away = distance([red, green, blue], color);
        if (away < best) {
          best = away;
          found = entry;
        }
      }
      nearest.set(key, found);
    }
    index[pixel] = found;
  }
  return index;
}

/**
 * Quantizes all `frames` with one palette of `colors` entries (the last stands for
 * "unchanged"). Frames after the first keep the transparent index wherever their
 * quantized colour equals the one of the frame before.
 */
export function indexFrames(
  frames: readonly RawFrame[],
  colors: number,
  { quantize }: Quantizer,
): IndexedFrames {
  const sample = samplePixels(frames);
  const palette = withFlatColors(quantize(sample, colors - 1), flatColors(sample));
  const quantized = palette.slice();
  palette.push([0, 0, 0]);
  const transparentIndex = palette.length - 1;
  const indexed: IndexedFrame[] = [];
  let previous: Uint8Array | undefined;
  for (const { width, height, data } of frames) {
    const full = applyExactly(data, quantized);
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
