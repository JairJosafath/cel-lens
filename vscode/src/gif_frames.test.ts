import { quantize } from 'gifenc';
import { describe, expect, it } from 'vitest';

import { indexFrames, type RawFrame } from './gif_frames.ts';

const COLORS = 3;
const SIZE = 16;
const TEXT_COLORS = 48;

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
    { name: 'a few levels of noise', noise: 2 },
    { name: 'no noise', noise: 0 },
  ])('writes nothing for a background that only differs by $name', ({ noise }) => {
    const base = [30, 30, 30];
    const noisy = base.map((level) => level + noise);
    const result = indexFrames(
      [frame(base, [200, 200, 200]), frame(noisy, [200, 200, 200]), frame(noisy, base)],
      COLORS,
      { quantize },
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
      { quantize },
    );
    expect(result.palette).toHaveLength(COLORS);
    expect(result.transparentIndex).toBe(COLORS - 1);
    expect(result.palette[COLORS - 1]).toEqual([0, 0, 0]);
  });

  it('writes a black box that appears in a later frame', () => {
    const white = [255, 255, 255];
    const result = indexFrames([frame(white, white), frame(white, [0, 0, 0])], COLORS, {
      quantize,
    });
    const written = result.frames[1]?.index.filter((value) => value !== result.transparentIndex);
    expect(written).toHaveLength((SIZE / 2) * (SIZE / 2));
  });

  it('keeps every pixel of the first frame', () => {
    const result = indexFrames([frame([30, 30, 30], [200, 0, 0])], COLORS, { quantize });
    expect(result.frames[0]?.index.includes(result.transparentIndex)).toBe(false);
  });
});

describe('indexFrames with a page of flat colours and text', () => {
  const BACKGROUND = [31, 31, 31];
  const BAR = [24, 24, 24];
  const WIDTH = 64;
  const HEIGHT = 32;

  /** A page: a bar on top, the background below, and a row of `TEXT_COLORS` anti-aliased greys and tints as text. */
  function page(): RawFrame {
    const data = new Uint8Array(WIDTH * HEIGHT * 4);
    for (let pixel = 0; pixel < WIDTH * HEIGHT; pixel += 1) {
      const row = Math.floor(pixel / WIDTH);
      const column = pixel % WIDTH;
      const text = row === 20 && column < TEXT_COLORS;
      const tint = 40 + column * 4;
      const color = text ? [tint, tint - 20, 200 - column * 2] : row < 4 ? BAR : BACKGROUND;
      data.set([...color, 255], pixel * 4);
    }
    return { width: WIDTH, height: HEIGHT, data };
  }

  it('gives the background and the bar entries of their own, exactly, however many colours the text has', () => {
    const result = indexFrames([page(), page()], 16, { quantize });
    const first = result.frames[0];
    const background = result.palette.findIndex((entry) => entry.join() === BACKGROUND.join());
    const bar = result.palette.findIndex((entry) => entry.join() === BAR.join());
    expect(background).toBeGreaterThanOrEqual(0);
    expect(bar).toBeGreaterThanOrEqual(0);
    const at = (row: number, column: number): number | undefined =>
      first?.index[row * WIDTH + column];
    // Every pixel of the background and of the bar, next to the text and away from it, is one entry.
    const backgroundPixels = [10, 19, 21, 31].flatMap((row) =>
      Array.from({ length: WIDTH }, (_, column) => at(row, column)),
    );
    expect(new Set(backgroundPixels)).toEqual(new Set([background]));
    const barPixels = [0, 3].flatMap((row) =>
      Array.from({ length: WIDTH }, (_, column) => at(row, column)),
    );
    expect(new Set(barPixels)).toEqual(new Set([bar]));
    // Nothing of the second, identical page is written again.
    expect(result.frames[1]?.index.every((value) => value === result.transparentIndex)).toBe(true);
  });

  it('draws the noise of a few levels around the background as the background, not as blotches of near colours', () => {
    const noisy = page();
    for (let pixel = 0; pixel < WIDTH * HEIGHT; pixel += 1) {
      const row = Math.floor(pixel / WIDTH);
      if (row > 4 && row !== 20 && pixel % 5 === 0) {
        const level = BACKGROUND[0] ?? 0;
        noisy.data.set([level - 3, level + 2, level - 2], pixel * 4);
      }
    }
    const result = indexFrames([noisy], 16, { quantize });
    const background = result.palette.findIndex((entry) => entry.join() === BACKGROUND.join());
    const rows = [10, 19, 21, 31].flatMap((row) =>
      Array.from({ length: WIDTH }, (_, column) => result.frames[0]?.index[row * WIDTH + column]),
    );
    expect(new Set(rows)).toEqual(new Set([background]));
  });
});
