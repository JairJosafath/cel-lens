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

const REPO = 'https://github.com/JairJosafath/cel-lens';

function expectedLink(versions: string[], index: number): string {
  const version = versions[index] ?? '';
  const previous = versions[index + 1];
  const tag = (name: string): string => `vscode-v${name}`;
  if (version === 'Unreleased') {
    return `[Unreleased]: ${REPO}/compare/${tag(versions[1] ?? '')}...HEAD`;
  }
  return previous === undefined
    ? `[${version}]: ${REPO}/releases/tag/${tag(version)}`
    : `[${version}]: ${REPO}/compare/${tag(previous)}...${tag(version)}`;
}

function linkProblems(versions: string[], refs: string[]): string[] {
  return versions.flatMap((_, index) => {
    const expected = expectedLink(versions, index);
    return refs.includes(expected) ? [] : [expected];
  });
}

function issueReferenceLines(text: string): string[] {
  return text.split('\n').filter((line) => /#\d/.test(line));
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

  it('links every version in the public vscode-v form', () => {
    const refs = CHANGELOG.slice(CHANGELOG.lastIndexOf('\n\n') + 2).split('\n');
    expect(
      linkProblems(
        sections.map(({ version }) => version),
        refs,
      ),
    ).toEqual([]);
  });

  it('names no issue number on any line', () => {
    expect(issueReferenceLines(CHANGELOG)).toEqual([]);
  });

  const GOOD_REFS = [
    `[Unreleased]: ${REPO}/compare/vscode-v0.2.0...HEAD`,
    `[0.2.0]: ${REPO}/compare/vscode-v0.1.0...vscode-v0.2.0`,
    `[0.1.0]: ${REPO}/releases/tag/vscode-v0.1.0`,
  ];
  const VERSIONS = ['Unreleased', '0.2.0', '0.1.0'];

  it.each([
    { name: 'the right links', refs: GOOD_REFS, problems: 0 },
    {
      name: 'a link in the v form',
      refs: [GOOD_REFS[0], `[0.2.0]: ${REPO}/compare/v0.1.0...v0.2.0`, GOOD_REFS[2]],
      problems: 1,
    },
    {
      name: 'a link that compares the wrong versions',
      refs: [GOOD_REFS[0], `[0.2.0]: ${REPO}/compare/vscode-v0.0.9...vscode-v0.2.0`, GOOD_REFS[2]],
      problems: 1,
    },
    {
      name: 'a stale Unreleased link',
      refs: [`[Unreleased]: ${REPO}/compare/vscode-v0.1.0...HEAD`, GOOD_REFS[1], GOOD_REFS[2]],
      problems: 1,
    },
    {
      name: 'an oldest link that compares',
      refs: [GOOD_REFS[0], GOOD_REFS[1], `[0.1.0]: ${REPO}/compare/vscode-v0.0.1...vscode-v0.1.0`],
      problems: 1,
    },
    { name: 'a missing link', refs: [GOOD_REFS[0], GOOD_REFS[2]], problems: 1 },
  ])('link check: $name has $problems problems', ({ refs, problems }) => {
    expect(
      linkProblems(
        VERSIONS,
        refs.filter((ref) => ref !== undefined),
      ),
    ).toHaveLength(problems);
  });

  it.each([
    { name: 'a plain line', text: '- Fixes a thing.', found: 0 },
    { name: 'a reference at the end', text: '- Fixes a thing (#63).', found: 1 },
    { name: 'two references', text: '- Fixes a thing (#63, #64).', found: 1 },
    { name: 'a bare reference', text: 'The change for #65 is merged.', found: 1 },
    { name: 'a heading', text: '## [0.1.0] - 2026-01-01\n### Added', found: 0 },
  ])('issue check: $name finds $found lines', ({ text, found }) => {
    expect(issueReferenceLines(text)).toHaveLength(found);
  });
});
