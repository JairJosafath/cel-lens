import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { readFileSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { deflateSync, inflateSync } from 'node:zlib';

import { describe, expect, it } from 'vitest';

const run = promisify(execFile);
const PACKAGE_TIMEOUT_MS = 120_000;
const PNG_SIGNATURE = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
const MIN_CONTRAST = 3;
const WHITE = [255, 255, 255] as const;
const NEAR_BLACK = [30, 30, 30] as const;
// A pixel in the solid badge and one on the light lens ring, in 128x128 pixels.
const BADGE_PIXEL = [8, 64] as const;
const MARK_PIXEL = [23, 58] as const;

type Rgb = readonly [number, number, number] | readonly number[];

const manifest: unknown = JSON.parse(readFileSync('package.json', 'utf8'));

function luminance(rgb: Rgb): number {
  const [red = 0, green = 0, blue = 0] = rgb.map((value) => {
    const unit = value / 255;
    return unit <= 0.03928 ? unit / 12.92 : ((unit + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * red + 0.7152 * green + 0.0722 * blue;
}

function contrast(first: Rgb, second: Rgb): number {
  const [light = 0, dark = 0] = [luminance(first), luminance(second)].sort((a, b) => b - a);
  return (light + 0.05) / (dark + 0.05);
}

const BYTES_PER_PIXEL = 4;

function paeth(left: number, up: number, upLeft: number): number {
  const estimate = left + up - upLeft;
  const distanceLeft = Math.abs(estimate - left);
  const distanceUp = Math.abs(estimate - up);
  const distanceUpLeft = Math.abs(estimate - upLeft);
  if (distanceLeft <= distanceUp && distanceLeft <= distanceUpLeft) {
    return left;
  }
  return distanceUp <= distanceUpLeft ? up : upLeft;
}

/** The RGBA pixels of an 8-bit RGBA PNG, with every row's filter (0 to 4) undone. */
function readPixels(png: Buffer): (x: number, y: number) => number[] {
  const width = png.readUInt32BE(16);
  const idat: Buffer[] = [];
  for (let offset = 8; offset < png.length;) {
    const length = png.readUInt32BE(offset);
    if (png.toString('latin1', offset + 4, offset + 8) === 'IDAT') {
      idat.push(png.subarray(offset + 8, offset + 8 + length));
    }
    offset += length + 12;
  }
  const raw = inflateSync(Buffer.concat(idat));
  const stride = width * BYTES_PER_PIXEL;
  const rows = raw.length / (stride + 1);
  const pixels = Buffer.alloc(rows * stride);
  for (let y = 0; y < rows; y++) {
    const filter = raw.readUInt8(y * (stride + 1));
    for (let i = 0; i < stride; i++) {
      const value = raw.readUInt8(y * (stride + 1) + 1 + i);
      const left = i >= BYTES_PER_PIXEL ? pixels.readUInt8(y * stride + i - BYTES_PER_PIXEL) : 0;
      const up = y > 0 ? pixels.readUInt8((y - 1) * stride + i) : 0;
      const upLeft =
        y > 0 && i >= BYTES_PER_PIXEL
          ? pixels.readUInt8((y - 1) * stride + i - BYTES_PER_PIXEL)
          : 0;
      let predicted: number;
      switch (filter) {
        case 0:
          predicted = 0;
          break;
        case 1:
          predicted = left;
          break;
        case 2:
          predicted = up;
          break;
        case 3:
          predicted = (left + up) >> 1;
          break;
        case 4:
          predicted = paeth(left, up, upLeft);
          break;
        default:
          throw new Error(`Unknown PNG filter type ${String(filter)}`);
      }
      pixels.writeUInt8((value + predicted) & 0xff, y * stride + i);
    }
  }
  return (x, y) => {
    const start = y * stride + x * BYTES_PER_PIXEL;
    return [...pixels.subarray(start, start + BYTES_PER_PIXEL)];
  };
}

/** A PNG of the given rows of RGBA bytes, each row stored with the filter of the same index. */
function encodePng(rows: readonly (readonly number[])[], filters: readonly number[]): Buffer {
  const width = (rows[0]?.length ?? 0) / BYTES_PER_PIXEL;
  const scanlines = rows.map((row, y) => {
    const filter = filters[y] ?? 0;
    const bytes = row.map((value, i) => {
      const left = i >= BYTES_PER_PIXEL ? (row[i - BYTES_PER_PIXEL] ?? 0) : 0;
      const up = rows[y - 1]?.[i] ?? 0;
      const upLeft = i >= BYTES_PER_PIXEL ? (rows[y - 1]?.[i - BYTES_PER_PIXEL] ?? 0) : 0;
      const predictions = [0, left, up, (left + up) >> 1, paeth(left, up, upLeft)];
      return (value - (predictions[filter] ?? 0)) & 0xff;
    });
    return Buffer.from([filter, ...bytes]);
  });
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(rows.length, 4);
  header.set([8, 6, 0, 0, 0], 8);
  const chunk = (type: string, data: Buffer): Buffer => {
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    // The decoder never checks the CRC.
    return Buffer.concat([length, Buffer.from(type, 'latin1'), data, Buffer.alloc(4)]);
  };
  return Buffer.concat([
    PNG_SIGNATURE,
    chunk('IHDR', header),
    chunk('IDAT', deflateSync(Buffer.concat(scanlines))),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

async function runVscePackage(out: string): Promise<string> {
  const args = ['package', '--no-dependencies', '--skip-license', '--allow-missing-repository'];
  const { stdout, stderr } = await run(
    join('node_modules', '.bin', 'vsce'),
    [...args, '--out', out],
    {
      env: { ...process.env, NO_COLOR: '1' },
    },
  );
  return `${stdout}\n${stderr}`;
}

describe('readPixels', () => {
  const rows = [
    [10, 20, 30, 255, 200, 90, 40, 255, 5, 250, 128, 255],
    [12, 18, 33, 255, 190, 95, 45, 255, 0, 255, 120, 255],
    [255, 0, 7, 255, 1, 2, 3, 255, 100, 150, 200, 255],
  ];

  it.each([
    ['None', 0],
    ['Sub', 1],
    ['Up', 2],
    ['Average', 3],
    ['Paeth', 4],
  ])('decodes rows stored with the %s filter', (_name, filter) => {
    const pixel = readPixels(encodePng(rows, [filter, filter, filter]));
    expect([0, 1, 2].map((x) => pixel(x, 1))).toEqual([
      rows[1]?.slice(0, 4),
      rows[1]?.slice(4, 8),
      rows[1]?.slice(8, 12),
    ]);
    expect(pixel(2, 2)).toEqual(rows[2]?.slice(8, 12));
  });

  it('decodes a PNG that mixes filters row by row', () => {
    const pixel = readPixels(encodePng(rows, [1, 4, 3]));
    expect(pixel(1, 0)).toEqual(rows[0]?.slice(4, 8));
    expect(pixel(1, 1)).toEqual(rows[1]?.slice(4, 8));
    expect(pixel(0, 2)).toEqual(rows[2]?.slice(0, 4));
  });
});

describe('the store listing', () => {
  it('keeps the publisher cel-lens', () => {
    expect(manifest).toMatchObject({ publisher: 'cel-lens' });
  });

  it.each(['cel', 'kubernetes', 'crd', 'x-kubernetes-validations', 'validatingadmissionpolicy'])(
    'has the keyword %s',
    (keyword) => {
      expect(manifest).toMatchObject({ keywords: expect.arrayContaining([keyword]) as unknown });
    },
  );

  it('names the icon file in package.json', () => {
    expect(manifest).toMatchObject({ icon: 'images/icon.png' });
  });

  describe('the icon', () => {
    const png = readFileSync('images/icon.png');

    it('is a 128x128 PNG', () => {
      expect(png.subarray(0, 8)).toEqual(PNG_SIGNATURE);
      expect(png.toString('latin1', 12, 16)).toBe('IHDR');
      expect([png.readUInt32BE(16), png.readUInt32BE(20)]).toEqual([128, 128]);
    });

    it.each([
      ['white', WHITE],
      ['near-black', NEAR_BLACK],
    ])(
      'has a badge that contrasts with %s and a mark that contrasts with the badge',
      (_name, background) => {
        const pixel = readPixels(png);
        const badge = pixel(...BADGE_PIXEL);
        const mark = pixel(...MARK_PIXEL);
        expect(badge[3]).toBe(255);
        expect(mark[3]).toBe(255);
        // The badge stands out from this background; the mark stands out from the badge.
        expect(contrast(badge, background)).toBeGreaterThanOrEqual(MIN_CONTRAST);
        expect(contrast(mark, badge)).toBeGreaterThanOrEqual(MIN_CONTRAST);
      },
    );
  });

  it(
    'packages with vsce without a warning',
    async () => {
      const directory = await mkdtemp(join(tmpdir(), 'vsce-package-'));
      try {
        const output = await runVscePackage(join(directory, 'cel-lens-vscode.vsix'));
        expect(output).toContain('Packaged:');
        expect(output).not.toMatch(/\bWARNING\b/);
      } finally {
        await rm(directory, { recursive: true, force: true });
      }
    },
    PACKAGE_TIMEOUT_MS,
  );
});
