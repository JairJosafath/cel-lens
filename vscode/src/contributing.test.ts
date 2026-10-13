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
