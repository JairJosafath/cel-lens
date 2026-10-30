import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { compareExperimentalRows, experimentalRows } from './experimental_table';

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
