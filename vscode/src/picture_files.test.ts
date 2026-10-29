import { readFileSync, statSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

import { gifFrameCount, pictureKind } from './picture_files';

const PNG_HEAD = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
const GIF_HEAD = [0x47, 0x49, 0x46, 0x38, 0x39, 0x61];
// Header, a 1x1 screen without a color table, two empty 1x1 frames, the trailer.
const frame = [0x2c, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 2, 0x44, 0x01, 0];
const TWO_FRAMES = Uint8Array.from([...GIF_HEAD, 1, 0, 1, 0, 0, 0, 0, ...frame, ...frame, 0x3b]);

const PNGS = ['problems.png', 'hover.png', 'completion.png', 'quick-fix.png', 'colors.png'];
const GIFS = ['colors.gif', 'completion.gif'];
const TWO_MEGABYTES = 2 * 1024 * 1024;

describe('pictureKind', () => {
  it.each([
    ['a PNG', Uint8Array.from([...PNG_HEAD, 0]), 'png'],
    ['a GIF89a', Uint8Array.from(GIF_HEAD), 'gif'],
    ['a GIF87a', Uint8Array.from([0x47, 0x49, 0x46, 0x38, 0x37, 0x61]), 'gif'],
    ['text', new TextEncoder().encode('<svg></svg>'), 'unknown'],
    ['nothing', new Uint8Array(), 'unknown'],
  ])('reads %s as %s', (_name, bytes, expected) => {
    expect(pictureKind(bytes)).toBe(expected);
  });
});

describe('gifFrameCount', () => {
  it.each([
    ['two frames', TWO_FRAMES, 2],
    ['no frame', Uint8Array.from([...GIF_HEAD, 1, 0, 1, 0, 0, 0, 0, 0x3b]), 0],
    ['a GIF cut short', TWO_FRAMES.subarray(0, 25), 0],
    ['a PNG', Uint8Array.from(PNG_HEAD), 0],
  ])('counts %s', (_name, bytes, expected) => {
    expect(gifFrameCount(bytes)).toBe(expected);
  });
});

describe('the pictures in images/', () => {
  it.each(PNGS)('%s is a PNG', (name) => {
    expect(pictureKind(readFileSync(`images/${name}`))).toBe('png');
  });

  it.each(GIFS)('%s is a GIF of more than one frame, under 2 MB', (name) => {
    const bytes = readFileSync(`images/${name}`);
    expect(pictureKind(bytes)).toBe('gif');
    expect(gifFrameCount(bytes)).toBeGreaterThan(1);
    expect(statSync(`images/${name}`).size).toBeLessThan(TWO_MEGABYTES);
  });

  it('the .vsix takes only images/icon.png of them', () => {
    const ignore = readFileSync('.vscodeignore', 'utf8')
      .split('\n')
      .filter((line) => line.startsWith('!images'));
    expect(ignore).toEqual(['!images/icon.png']);
  });
});

describe('npm run pictures', () => {
  const manifest = JSON.parse(readFileSync('package.json', 'utf8')) as {
    scripts: Record<string, string>;
    devDependencies: Record<string, string>;
    pictures: { openvscodeServer: string };
  };

  it('runs the script in test/pictures/', () => {
    expect(manifest.scripts['pictures']).toBe('node test/pictures/take.ts');
    expect(statSync('test/pictures/take.ts').isFile()).toBe(true);
  });

  it.each([['playwright'], ['gifenc'], ['pngjs']])('pins %s to an exact version', (name) => {
    expect(manifest.devDependencies[name]).toMatch(/^\d+\.\d+\.\d+$/);
  });

  it('pins the version of openvscode-server', () => {
    expect(manifest.pictures.openvscodeServer).toMatch(/^\d+\.\d+\.\d+$/);
  });

  it.each(['widget_crd.yaml', 'pod_policy.yaml'])(
    'has the fixture %s, which the script uses',
    (name) => {
      expect(readFileSync(`test/pictures/${name}`, 'utf8').length).toBeGreaterThan(0);
      expect(readFileSync('test/pictures/take.ts', 'utf8')).toContain(name);
    },
  );

  it('the fixtures are a CRD with x-kubernetes-validations and a ValidatingAdmissionPolicy', () => {
    expect(readFileSync('test/pictures/widget_crd.yaml', 'utf8')).toContain(
      'x-kubernetes-validations:',
    );
    expect(readFileSync('test/pictures/pod_policy.yaml', 'utf8')).toContain(
      'kind: ValidatingAdmissionPolicy',
    );
  });
});
