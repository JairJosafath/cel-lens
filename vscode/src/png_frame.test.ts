import { deflateSync } from 'node:zlib';

import { describe, expect, it } from 'vitest';

import { decodePng } from './png_frame.ts';

const SIGNATURE = Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

/** A chunk with a zero CRC: the decoder reads the data, not the checksum. */
function chunk(type: string, body: Uint8Array): Uint8Array {
  const out = new Uint8Array(12 + body.length);
  new DataView(out.buffer).setUint32(0, body.length);
  out.set(Buffer.from(type, 'latin1'), 4);
  out.set(body, 8);
  return out;
}

/** An 8-bit PNG of `width` x `height` whose scanlines are `rows`, each with its filter byte first. */
function png(
  width: number,
  height: number,
  colorType: number,
  rows: readonly number[][],
  interlace = 0,
): Uint8Array {
  const header = new Uint8Array(13);
  const view = new DataView(header.buffer);
  view.setUint32(0, width);
  view.setUint32(4, height);
  header.set([8, colorType, 0, 0, interlace], 8);
  const idat = deflateSync(Uint8Array.from(rows.flat()));
  return Buffer.concat([
    SIGNATURE,
    chunk('IHDR', header),
    chunk('IDAT', idat.subarray(0, 5)),
    chunk('IDAT', idat.subarray(5)),
    chunk('IEND', new Uint8Array()),
  ]);
}

describe('decodePng', () => {
  it('reads the pixels of an RGBA PNG without a filter', () => {
    const decoded = decodePng(png(2, 1, 6, [[0, 1, 2, 3, 4, 5, 6, 7, 8]]));
    expect(decoded).toMatchObject({ width: 2, height: 1 });
    expect([...decoded.data]).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
  });

  it('gives an RGB PNG an opaque alpha', () => {
    expect([...decodePng(png(2, 1, 2, [[0, 10, 20, 30, 40, 50, 60]])).data]).toEqual([
      10, 20, 30, 255, 40, 50, 60, 255,
    ]);
  });

  it.each([
    {
      name: 'Sub adds the pixel to the left',
      rows: [[1, 10, 20, 30, 1, 2, 3]],
      expected: [10, 20, 30, 255, 11, 22, 33, 255],
    },
    {
      name: 'Up adds the pixel above',
      rows: [
        [0, 10, 20, 30],
        [2, 1, 2, 3],
      ],
      expected: [10, 20, 30, 255, 11, 22, 33, 255],
    },
    {
      name: 'Average adds the mean of the left and the one above',
      rows: [
        [0, 10, 20, 30],
        [3, 4, 4, 4],
      ],
      expected: [10, 20, 30, 255, 9, 14, 19, 255],
    },
    {
      name: 'Paeth adds the nearest of the left, the one above and the one above-left',
      rows: [
        [0, 10, 20, 30],
        [4, 1, 2, 3],
      ],
      expected: [10, 20, 30, 255, 11, 22, 33, 255],
    },
  ])('undoes the filter: $name', ({ rows, expected }) => {
    const wide = rows.length === 1;
    const decoded = decodePng(png(wide ? 2 : 1, wide ? 1 : 2, 2, rows));
    expect([...decoded.data]).toEqual(expected);
  });

  it.each([
    {
      name: 'not a PNG',
      bytes: Uint8Array.from([1, 2, 3, 4, 5, 6, 7, 8, 9]),
      message: 'not a PNG',
    },
    {
      name: 'a palette PNG',
      bytes: png(1, 1, 3, [[0, 0]]),
      message: 'colour type 3',
    },
    {
      name: 'an interlaced PNG',
      bytes: png(1, 1, 2, [[0, 1, 2, 3]], 1),
      message: 'interlace 1',
    },
    {
      name: 'a PNG with too little pixel data',
      bytes: png(2, 2, 2, [[0, 1, 2, 3, 4, 5, 6]]),
      message: 'wrong amount of pixel data',
    },
  ])('refuses $name', ({ bytes, message }) => {
    expect(() => decodePng(bytes)).toThrow(message);
  });

  describe('against an encoder written from the PNG specification', () => {
    const WIDTH = 9;
    const HEIGHT = 6;
    const BYTES_PER_PIXEL = 4;

    /** A deterministic image whose bytes jump about, so every predictor sees every kind of neighbour. */
    function image(): Uint8Array {
      const bytes = new Uint8Array(WIDTH * HEIGHT * BYTES_PER_PIXEL);
      let seed = 12345;
      for (let at = 0; at < bytes.length; at += 1) {
        seed = (seed * 1103515245 + 12345) % 2 ** 31;
        bytes[at] = (seed >> 16) & 255;
      }
      return bytes;
    }

    /** Which neighbour the Paeth predictor of the specification picks. */
    function paethPick(left: number, up: number, upLeft: number): 'left' | 'up' | 'upLeft' {
      const estimate = left + up - upLeft;
      const toLeft = Math.abs(estimate - left);
      const toUp = Math.abs(estimate - up);
      const toUpLeft = Math.abs(estimate - upLeft);
      if (toLeft <= toUp && toLeft <= toUpLeft) {
        return 'left';
      }
      return toUp <= toUpLeft ? 'up' : 'upLeft';
    }

    /** The scanlines of `bytes` filtered with `filter` (0 to 4), each starting with its filter byte. */
    function filtered(bytes: Uint8Array, filter: number): number[][] {
      const stride = WIDTH * BYTES_PER_PIXEL;
      const at = (row: number, column: number): number =>
        row < 0 || column < 0 ? 0 : (bytes[row * stride + column] ?? 0);
      return Array.from({ length: HEIGHT }, (_, row) => [
        filter,
        ...Array.from({ length: stride }, (_unused, column) => {
          const left = at(row, column - BYTES_PER_PIXEL);
          const up = at(row - 1, column);
          const upLeft = at(row - 1, column - BYTES_PER_PIXEL);
          const predictors = [
            0,
            left,
            up,
            (left + up) >> 1,
            { left, up, upLeft }[paethPick(left, up, upLeft)],
          ];
          return (at(row, column) - (predictors[filter] ?? 0) + 256) % 256;
        }),
      ]);
    }

    it('gives the image of each filter back, whatever its bytes', () => {
      for (const filter of [0, 1, 2, 3, 4]) {
        const decoded = decodePng(png(WIDTH, HEIGHT, 6, filtered(image(), filter)));
        expect([...decoded.data], `filter ${String(filter)}`).toEqual([...image()]);
      }
    });

    it('takes each of the three branches of the Paeth predictor, and a sum above 255 that wraps', () => {
      const bytes = image();
      const stride = WIDTH * BYTES_PER_PIXEL;
      const picks = new Set<string>();
      let wraps = 0;
      for (let row = 1; row < HEIGHT; row += 1) {
        for (let column = BYTES_PER_PIXEL; column < stride; column += 1) {
          const left = bytes[row * stride + column - BYTES_PER_PIXEL] ?? 0;
          const up = bytes[(row - 1) * stride + column] ?? 0;
          const upLeft = bytes[(row - 1) * stride + column - BYTES_PER_PIXEL] ?? 0;
          const pick = paethPick(left, up, upLeft);
          picks.add(pick);
          const predictor = { left, up, upLeft }[pick];
          // The stored byte is the pixel minus the predictor, mod 256: adding it back wraps when the sum passes 255.
          const stored = ((((bytes[row * stride + column] ?? 0) - predictor) % 256) + 256) % 256;
          if (stored + predictor > 255) {
            wraps += 1;
          }
        }
      }
      expect([...picks].sort()).toEqual(['left', 'up', 'upLeft']);
      expect(wraps).toBeGreaterThan(0);
    });

    it('decodes a row of 255s after a row of 255s with Paeth, which wraps to 254 and back', () => {
      // Stored 255 on a predictor of 255: 255 + 255 = 510, which is 254 as a byte.
      const decoded = decodePng(
        png(1, 2, 6, [
          [0, 255, 255, 255, 255],
          [4, 255, 255, 255, 255],
        ]),
      );
      expect([...decoded.data]).toEqual([255, 255, 255, 255, 254, 254, 254, 254]);
    });
  });
});
