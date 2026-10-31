import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

const manifest: unknown = JSON.parse(readFileSync('package.json', 'utf8'));

describe('the Apache-2.0 license', () => {
  it('is the license in package.json', () => {
    expect(manifest).toMatchObject({ license: 'Apache-2.0' });
  });

  it.each(['Apache License', 'Version 2.0, January 2004', 'END OF TERMS AND CONDITIONS'])(
    'LICENSE holds the full text: %s',
    (phrase) => {
      expect(readFileSync('LICENSE', 'utf8')).toContain(phrase);
    },
  );

  it('is re-included by .vscodeignore, so vsce packages it', () => {
    // `make package-check` packages for real; reading the ignore file keeps this test off the disk walk.
    const lines = readFileSync('.vscodeignore', 'utf8').split('\n');
    expect(lines).toContain('!LICENSE*');
  });

  it('is named in the README', () => {
    expect(readFileSync('README.md', 'utf8')).toContain('licensed under Apache-2.0');
  });
});
