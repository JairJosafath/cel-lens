import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

import { checkLinks, extractLinks, type Fetcher } from './check_links.ts';

describe('extractLinks', () => {
  it.each([
    ['an inline link', 'see [x](https://a.example/p) now', ['https://a.example/p']],
    [
      'a reference line',
      '[0.1.0]: https://a.example/compare/v1...v2',
      ['https://a.example/compare/v1...v2'],
    ],
    ['an autolink', 'at <http://a.example/x>.', ['http://a.example/x']],
    ['a bare link before a comma', 'go to https://a.example/r, then', ['https://a.example/r']],
    [
      'a repeated link once',
      '[a](https://a.example) [b](https://a.example)',
      ['https://a.example'],
    ],
    ['no link', 'plain text, ftp://a.example and mailto:a@b', []],
    ['an empty text', '', []],
  ])('finds %s', (_name, text, expected) => {
    expect(extractLinks(text)).toEqual(expected);
  });
});

describe('checkLinks', () => {
  it.each([
    [200, true],
    [301, true],
    [399, true],
    [404, false],
    [500, false],
  ])('treats status %i as ok: %s', async (status, ok) => {
    const fetcher: Fetcher = () => Promise.resolve({ status });
    expect(await checkLinks(['https://a.example'], fetcher)).toEqual([
      { url: 'https://a.example', outcome: status, ok },
    ]);
  });

  it('reports a failed request with its reason', async () => {
    const fetcher: Fetcher = () => Promise.reject(new Error('getaddrinfo ENOTFOUND'));
    expect(await checkLinks(['https://a.example'], fetcher)).toEqual([
      { url: 'https://a.example', outcome: 'getaddrinfo ENOTFOUND', ok: false },
    ]);
  });
});

describe('the release steps', () => {
  const manifest: unknown = JSON.parse(readFileSync('package.json', 'utf8'));
  const scripts = (manifest as { scripts: Record<string, string> }).scripts;

  it('offer npm run links, which runs the script', () => {
    expect(scripts['links']).toBe('node src/check_links.ts');
  });

  it('tell the releaser to run it before tagging', () => {
    const contributing = readFileSync('CONTRIBUTING.md', 'utf8');
    const steps = contributing.slice(contributing.indexOf('To release, follow'));
    expect(steps.indexOf('npm run links')).toBeGreaterThan(0);
    expect(steps.indexOf('npm run links')).toBeLessThan(steps.indexOf('Tag the commit'));
  });

  it('is not part of make check', () => {
    const makefile = readFileSync('Makefile', 'utf8');
    expect(makefile).not.toContain('links');
    expect(scripts['test']).not.toContain('links');
  });
});
