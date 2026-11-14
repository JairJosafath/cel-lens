import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  compareExperimentalRows,
  withoutLinkTargets,
  descriptionReason,
  experimentalReasons,
  experimentalRows,
  featureNameProblems,
} from './experimental_table';

const README = [
  '## Features',
  '',
  'Stable:',
  '',
  '| Feature | What it does |',
  '|---|---|',
  '| Checks | Compiles |',
  '',
  'Experimental:',
  '',
  '| Name | What it does | Why |',
  '| ---- | ------------ | --- |',
  '| `alpha` | Does a | because |',
  '| `beta` | Does b | later |',
  '',
  '## Next',
  '',
  '| `gamma` | Not experimental | x |',
].join('\n');

describe('experimentalRows', () => {
  it('reads the name and the "What it does" cell of the Experimental table only', () => {
    expect([...experimentalRows(README)]).toEqual([
      ['alpha', 'Does a'],
      ['beta', 'Does b'],
    ]);
  });
});

describe('experimentalReasons', () => {
  it('reads the third cell of each Experimental row and nothing from other sections', () => {
    expect([...experimentalReasons(README)]).toEqual([
      ['alpha', 'because'],
      ['beta', 'later'],
    ]);
  });
});

describe('experimentalReasons with an incomplete row', () => {
  const incomplete = [
    'Experimental:',
    '',
    '| Name | What it does | Why |',
    '| ---- | ------------ | --- |',
    '| `short` | Does s |',
    '| `blank` | Does b |  |',
    '| `full` | Does f | because |',
  ].join('\n');

  it('leaves out a row with two cells and gives an empty reason for a blank Why cell', () => {
    expect([...experimentalReasons(incomplete)]).toEqual([
      ['blank', ''],
      ['full', 'because'],
    ]);
  });

  it('still lists both incomplete rows in experimentalRows', () => {
    expect([...experimentalRows(incomplete).keys()]).toEqual(['short', 'blank', 'full']);
  });
});

describe('descriptionReason', () => {
  it.each([
    ['a description without the marker', 'Gives typed variables', undefined],
    ['an empty description', '', undefined],
    [
      'the marker with a reason',
      'It is experimental because the scan is slow.',
      'the scan is slow.',
    ],
    ['the marker after other text', 'Types. It is experimental because x', 'x'],
  ])('reads %s', (_label, description, expected) => {
    expect(descriptionReason(description)).toBe(expected);
  });
});

describe('compareExperimentalRows', () => {
  const base = new Map([['alpha', 'Does a']]);

  it.each([
    ['equal tables', base, base, []],
    [
      'a feature only the extension lists',
      new Map([...base, ['beta', 'b']]),
      base,
      ['beta: the engine README lacks this feature'],
    ],
    [
      'a feature only the engine lists',
      base,
      new Map([...base, ['beta', 'b']]),
      ['beta: the extension README lacks this feature'],
    ],
    [
      'a different cell',
      base,
      new Map([['alpha', 'Does c']]),
      ['alpha: differs\n  extension: Does a\n  engine:    Does c'],
    ],
    [
      'a color note added to the engine text',
      new Map([['alpha', 'Does a. Off, it gets no colors']]),
      base,
      [],
    ],
    [
      'an on and off color note added to the engine text',
      new Map([['alpha', 'Does a. On, the extension colors it; off, it does not']]),
      base,
      [],
    ],
    [
      'a color note naming a setting added to the engine text',
      new Map([['alpha', 'Does a. Off, or with cel-lens.colorStyle off, it gets no colors']]),
      base,
      [],
    ],
    [
      'a color note that names only the colorStyle setting',
      new Map([['alpha', 'Does a. Styled by cel-lens.colorStyle']]),
      base,
      [],
    ],
    [
      'a note that mixes colors with another feature',
      new Map([['alpha', 'Does a. Off, it gets no colors or hover']]),
      base,
      ['alpha: differs\n  extension: Does a. Off, it gets no colors or hover\n  engine:    Does a'],
    ],
    [
      'a note in other words added to the engine text',
      new Map([['alpha', 'Does a. Off, it gets no hover']]),
      base,
      ['alpha: differs\n  extension: Does a. Off, it gets no hover\n  engine:    Does a'],
    ],
    [
      'a color note that also changes the engine text',
      new Map([['alpha', 'Does b. Off, it gets no colors']]),
      base,
      ['alpha: differs\n  extension: Does b. Off, it gets no colors\n  engine:    Does a'],
    ],
  ])('reports %s', (_name, extension, engine, expected) => {
    expect(compareExperimentalRows(extension, engine)).toEqual(expected);
  });
});

const ENGINE_DIR = process.env['CEL_LENS_ENGINE_DIR'];

describe('the experimental table against the engine README', () => {
  if (ENGINE_DIR === undefined || ENGINE_DIR === '') {
    it.skip('is skipped: CEL_LENS_ENGINE_DIR is unset, so there is no engine README to compare', () => {
      // Skipped so that make check passes offline.
    });
    // eslint-disable-next-line no-console -- the reason the test is skipped must show in the run
    console.info('experimental table check skipped: CEL_LENS_ENGINE_DIR is unset');
    return;
  }

  it('lists the same features with the same "What it does" cells', () => {
    const extension = experimentalRows(readFileSync(join(__dirname, '..', 'README.md'), 'utf8'));
    const engine = experimentalRows(readFileSync(join(ENGINE_DIR, 'README.md'), 'utf8'));

    expect(extension.size).toBeGreaterThan(0);
    expect(compareExperimentalRows(extension, engine)).toEqual([]);
  });
});

describe('featureNameProblems', () => {
  it.each([
    ['the same names', ['a', 'b'], ['b', 'a'], []],
    [
      'an extra name',
      ['a', 'b'],
      ['a'],
      ['package.json lists "b", which the engine does not know (extra)'],
    ],
    [
      'a missing name',
      ['a'],
      ['a', 'b'],
      ['package.json lacks "b", which the engine knows (missing)'],
    ],
  ])('reports %s', (_label, listed, engine, expected) => {
    expect(featureNameProblems('package.json', listed, engine)).toEqual(expected);
  });
});

describe('withoutLinkTargets', () => {
  it.each([
    ['a link', 'see [docs/cellens.md](docs/cellens.md) for more', 'see docs/cellens.md for more'],
    ['two links', '[a](x) and [b](https://e/y)', 'a and b'],
    ['no link', 'plain `text` (in brackets)', 'plain `text` (in brackets)'],
    ['an empty text', '[](x)', ''],
  ])('keeps the words of %s', (_name, text, expected) => {
    expect(withoutLinkTargets(text)).toBe(expected);
  });

  it('lets a row differ from the engine’s only in where a link points', () => {
    const extension = new Map([
      ['f', 'Does a ([guide](https://github.com/o/r/blob/main/docs/g.md))'],
    ]);
    const engine = new Map([['f', 'Does a ([guide](docs/g.md))']]);

    expect(compareExperimentalRows(extension, engine)).toEqual([]);
  });

  it('still reports a row whose link text differs', () => {
    const extension = new Map([['f', 'Does a ([guide](docs/g.md))']]);
    const engine = new Map([['f', 'Does a ([manual](docs/g.md))']]);

    expect(compareExperimentalRows(extension, engine)).toHaveLength(1);
  });
});
