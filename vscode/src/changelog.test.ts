import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const CHANGELOG = readFileSync(`${__dirname}/../CHANGELOG.md`, 'utf8');
const KINDS = ['Added', 'Changed', 'Deprecated', 'Removed', 'Fixed', 'Security'];

function isInOrder(kinds: string[]): boolean {
  const known = kinds.filter((kind) => KINDS.includes(kind));
  return (
    known.length === kinds.length &&
    known.every((kind, i) => i === 0 || KINDS.indexOf(kind) > KINDS.indexOf(known[i - 1] ?? ''))
  );
}

function engineVersions(text: string): string[] {
  const found = text
    .split('\n')
    .flatMap((line) => line.split('cel-lens-core').slice(1))
    .flatMap((after) => {
      const version = /\bv?(\d+\.\d+\.\d+)\b/.exec(after)?.[1];
      return version === undefined ? [] : [`v${version}`];
    });
  return [...new Set(found)];
}

const sections = CHANGELOG.split(/^(?=## \[)/m)
  .slice(1)
  .map((text) => ({
    version: /^## \[([^\]]+)\]/.exec(text)?.[1] ?? '',
    engines: engineVersions(text),
    kinds: [...text.matchAll(/^### (.+)$/gm)].map((match) => match[1] ?? ''),
  }));

describe('CHANGELOG.md', () => {
  it.each(sections)('lists each kind once, in Keep a Changelog order, in $version', ({ kinds }) => {
    expect(isInOrder(kinds)).toBe(true);
  });

  it.each([
    { name: 'all six kinds in order', kinds: KINDS, ok: true },
    { name: 'a subset in order', kinds: ['Changed', 'Security'], ok: true },
    { name: 'a duplicated kind', kinds: ['Added', 'Added'], ok: false },
    { name: 'Fixed before Removed', kinds: ['Fixed', 'Removed'], ok: false },
    { name: 'Security before Deprecated', kinds: ['Security', 'Deprecated'], ok: false },
    { name: 'an unknown kind', kinds: ['Added', 'Improved'], ok: false },
  ])('kind check: $name is $ok', ({ kinds, ok }) => {
    expect(isInOrder(kinds)).toBe(ok);
  });

  it.each(sections)('names at most one cel-lens-core engine in $version', ({ engines }) => {
    expect(
      engines.length,
      `cel-lens-core versions found: ${engines.join(', ')}`,
    ).toBeLessThanOrEqual(1);
  });

  it.each([
    {
      name: 'standard wording',
      text: 'carries the `cel-lens-core` v0.9.0 engine',
      found: ['v0.9.0'],
    },
    { name: 'bundles, no backticks', text: 'bundles cel-lens-core 0.10.1 now', found: ['v0.10.1'] },
    {
      name: 'two versions in any wording',
      text: 'carries the `cel-lens-core` v0.9.0.\n- Ships cel-lens-core v0.11.0 too',
      found: ['v0.9.0', 'v0.11.0'],
    },
    {
      name: 'two versions on one line',
      text: 'cel-lens-core v0.9.0 and cel-lens-core v0.11.0',
      found: ['v0.9.0', 'v0.11.0'],
    },
    {
      name: 'an unrelated version later on the same line',
      text: 'carries the `cel-lens-core` v0.11.0 engine, which needs VS Code 1.90.0',
      found: ['v0.11.0'],
    },
    {
      name: 'the same version twice',
      text: 'cel-lens-core v0.9.0 and `cel-lens-core` v0.9.0',
      found: ['v0.9.0'],
    },
    { name: 'no engine named', text: 'Fixes v1.2.3 of something else', found: [] },
  ])('engine check: $name finds $found', ({ text, found }) => {
    expect(engineVersions(text)).toEqual(found);
  });

  it('has an Unreleased section and a released version for every section', () => {
    expect(sections[0]?.version).toBe('Unreleased');
    expect(sections.length).toBeGreaterThan(1);
  });

  it.each(sections)('ends with a link reference for $version', ({ version }) => {
    const refs = CHANGELOG.slice(CHANGELOG.lastIndexOf('\n\n') + 2).split('\n');
    expect(refs.filter((line) => line.startsWith(`[${version}]: https://`))).toHaveLength(1);
  });
});
