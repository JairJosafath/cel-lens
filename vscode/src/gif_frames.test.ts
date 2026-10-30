import { applyPalette, quantize } from 'gifenc';
import { describe, expect, it } from 'vitest';

import { indexFrames, type RawFrame } from './gif_frames.ts';

const COLORS = 3;
const SIZE = 16;

/** A SIZE x SIZE frame: `background` everywhere, `box` in its top-left quarter. */
function frame(background: readonly number[], box: readonly number[]): RawFrame {
  const data = new Uint8Array(SIZE * SIZE * 4);
  for (let pixel = 0; pixel < SIZE * SIZE; pixel += 1) {
    const inBox = pixel % SIZE < SIZE / 2 && pixel < (SIZE * SIZE) / 2;
    data.set([...(inBox ? box : background), 255], pixel * 4);
  }
  return { width: SIZE, height: SIZE, data };
}

describe('indexFrames', () => {
  it.each([
    { name: 'a few levels of JPEG noise', noise: 2 },
    { name: 'no noise', noise: 0 },
  ])('writes nothing for a background that only differs by $name', ({ noise }) => {
    const base = [30, 30, 30];
    const noisy = base.map((level) => level + noise);
    const result = indexFrames(
      [frame(base, [200, 200, 200]), frame(noisy, [200, 200, 200]), frame(noisy, base)],
      COLORS,
      { applyPalette, quantize },
    );
    const [, second, third] = result.frames;
    expect(second?.index.every((value) => value === result.transparentIndex)).toBe(true);
    // The box closes: its pixels are written, in the background colour; the rest is kept.
    const written = third?.index.filter((value) => value !== result.transparentIndex).length;
    expect(written).toBe((SIZE / 2) * (SIZE / 2));
  });

  it('gives every frame the one palette, with the transparent entry last', () => {
    const result = indexFrames(
      [frame([0, 0, 0], [255, 0, 0]), frame([0, 0, 0], [0, 0, 255])],
      COLORS,
      { applyPalette, quantize },
    );
    expect(result.palette).toHaveLength(COLORS);
    expect(result.transparentIndex).toBe(COLORS - 1);
    expect(result.palette[COLORS - 1]).toEqual([0, 0, 0]);
  });

  it('keeps every pixel of the first frame', () => {
    const result = indexFrames([frame([30, 30, 30], [200, 0, 0])], COLORS, {
      applyPalette,
      quantize,
    });
    expect(result.frames[0]?.index.includes(result.transparentIndex)).toBe(false);
  });
});
