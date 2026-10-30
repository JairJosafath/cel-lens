import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

const CONTRIBUTING = readFileSync('CONTRIBUTING.md', 'utf8');
const README = readFileSync('README.md', 'utf8');
const ISSUES = 'https://github.com/JairJosafath/cel-lens/issues';

describe('CONTRIBUTING.md', () => {
  it.each([
    'cel-lens-core',
    'stdio',
    'https://github.com/JairJosafath/cel-lens/releases',
    'npm ci',
    'npm run engine',
    'npm run build',
    'make check',
    'make dist',
    '--extensionDevelopmentPath',
    ISSUES,
  ])('mentions %s', (text) => {
    expect(CONTRIBUTING).toContain(text);
  });

  it('names the same Node version as engines.node in package.json', () => {
    const manifest: unknown = JSON.parse(readFileSync('package.json', 'utf8'));
    const engines = (manifest as { engines?: { node?: string } }).engines;
    const documented = /\bNode (\d+)\b/.exec(CONTRIBUTING)?.[1] ?? '';

    expect(documented).not.toBe('');
    expect(engines?.node).toBe(`>=${documented}`);
  });

  it('says Kyverno ValidatingPolicy fields are colored only with kyverno in cel-lens.experimental', () => {
    const flat = CONTRIBUTING.replace(/\s+/g, ' ');

    expect(flat).toContain('only when `kyverno` is in the `cel-lens.experimental` setting');
  });

  it('puts retaking the pictures and committing images/ in the release steps', () => {
    const start = CONTRIBUTING.indexOf('## Releases');
    const section = CONTRIBUTING.slice(start, CONTRIBUTING.indexOf('\n## ', start + 1));

    expect(section).toContain('npm run pictures');
    expect(section).toContain('images/');
  });

  it.each([
    ['version bump', '`version` in `package.json`'],
    ['CHANGELOG', '`CHANGELOG.md`'],
    ['pictures', '`npm run pictures`'],
    ['check', '`make check`'],
    ['tagging', '`vX.Y.Z`'],
  ])('lists the %s step in the numbered release steps', (_name, text) => {
    const start = CONTRIBUTING.indexOf('## Releases');
    const section = CONTRIBUTING.slice(start, CONTRIBUTING.indexOf('\n## ', start + 1));
    const steps = section.split('\n').filter((line) => /^\d+\. /.test(line));

    expect(steps.some((line) => line.includes(text))).toBe(true);
  });

  it('numbers the release steps from 1 and tags last', () => {
    const start = CONTRIBUTING.indexOf('## Releases');
    const section = CONTRIBUTING.slice(start, CONTRIBUTING.indexOf('\n## ', start + 1));
    const steps = section.split('\n').filter((line) => /^\d+\. /.test(line));
    const numbers = steps.map((line) => Number(/^(\d+)\./.exec(line)?.[1]));

    expect(numbers).toEqual(steps.map((_line, i) => i + 1));
    expect(steps.at(-1)).toContain('`vX.Y.Z`');
  });

  it('names no factory image or devcontainer', () => {
    expect(CONTRIBUTING).not.toMatch(/devcontainer|factory|git[e]a|docker/i);
  });
});

describe('README.md Contributing section', () => {
  it('is the last section and links the guide and the issues page', () => {
    const headings = README.split('\n').filter((line) => line.startsWith('## '));
    const section = README.slice(README.lastIndexOf('## Contributing'));

    expect(headings.at(-1)).toBe('## Contributing');
    expect(section).toContain('(CONTRIBUTING.md)');
    expect(section).toContain(ISSUES);
  });
});
