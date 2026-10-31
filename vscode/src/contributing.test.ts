import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

const CONTRIBUTING = readFileSync('CONTRIBUTING.md', 'utf8');
const README = readFileSync('README.md', 'utf8');
const ISSUES = 'https://github.com/JairJosafath/cel-lens/issues';

/** The text of a CONTRIBUTING.md section, up to the next `## ` heading; fails the test when the heading is missing. */
function section(heading: string, { flat = false }: { flat?: boolean } = {}): string {
  const start = CONTRIBUTING.indexOf(`## ${heading}`);
  if (start < 0) {
    expect.fail(`CONTRIBUTING.md has no "## ${heading}" heading`);
  }
  const end = CONTRIBUTING.indexOf('\n## ', start + 1);
  const text = CONTRIBUTING.slice(start, end < 0 ? undefined : end);
  return flat ? text.replace(/\s+/g, ' ') : text;
}

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

  it('tells contributors to export CEL_LENS_ENGINE_DIR at the core-version.json tag before make check, naming the layout test', () => {
    const body = section('Test', { flat: true });

    expect(body).toContain('export CEL_LENS_ENGINE_DIR=');
    expect(body).toContain('`cel-lens-core` checkout');
    expect(body).toContain('`core-version.json`');
    expect(body).toContain('Before `make check`');
    expect(body).toContain('public-repo layout test');
    expect(body).toContain('`src/public_repo.test.ts`');
  });

  it('says Kyverno ValidatingPolicy fields are colored only with kyverno in cel-lens.experimental', () => {
    const flat = CONTRIBUTING.replace(/\s+/g, ' ');

    expect(flat).toContain('only when `kyverno` is in the `cel-lens.experimental` setting');
  });

  it('puts retaking the pictures and committing images/ in the release steps', () => {
    const body = section('Releases');

    expect(body).toContain('npm run pictures');
    expect(body).toContain('images/');
  });

  it.each([
    ['version bump', '`version` in `package.json`'],
    ['CHANGELOG', '`CHANGELOG.md`'],
    ['pictures', '`npm run pictures`'],
    ['check', '`make check`'],
    ['tagging', '`vX.Y.Z`'],
  ])('lists the %s step in the numbered release steps', (_name, text) => {
    const body = section('Releases');
    const steps = body.split('\n').filter((line) => /^\d+\. /.test(line));

    expect(steps.some((line) => line.includes(text))).toBe(true);
  });

  it('numbers the release steps from 1 and tags last', () => {
    const body = section('Releases');
    const steps = body.split('\n').filter((line) => /^\d+\. /.test(line));
    const numbers = steps.map((line) => Number(/^(\d+)\./.exec(line)?.[1]));

    expect(numbers).toEqual(steps.map((_line, i) => i + 1));
    expect(steps.at(-1)).toContain('`vX.Y.Z`');
  });

  it('tells release step 2 to use the vscode-v compare form and move the Unreleased link', () => {
    const body = section('Releases');
    const step = body.split('\n').find((line) => line.startsWith('2. ')) ?? '';

    expect(step).toContain('`vscode-v` form');
    expect(step).toContain('compare/vscode-v<previous>...vscode-vX.Y.Z');
    expect(step).toContain('move the `[Unreleased]` link');
    expect(step).toContain('`vscode-vX.Y.Z` to `HEAD`');
  });

  it('says in Send a change that a CHANGELOG line names no issue number', () => {
    const body = section('Send a change', { flat: true });
    expect(body).toContain('a CHANGELOG line names no issue number');
  });

  it('names no factory image or devcontainer', () => {
    expect(CONTRIBUTING).not.toMatch(/devcontainer|factory|git[e]a|docker/i);
  });
});

describe('README.md Contributing section', () => {
  it('is the last section and links the guide and the issues page', () => {
    const headings = README.split('\n').filter((line) => line.startsWith('## '));
    const readmeSection = README.slice(README.lastIndexOf('## Contributing'));

    expect(headings.at(-1)).toBe('## Contributing');
    expect(readmeSection).toContain('(CONTRIBUTING.md)');
    expect(readmeSection).toContain(ISSUES);
  });
});
