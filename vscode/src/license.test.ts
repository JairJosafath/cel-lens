import { readFileSync } from 'node:fs';

import { listFiles } from '@vscode/vsce';
import { describe, expect, it } from 'vitest';

const LIST_TIMEOUT_MS = 60_000;
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

  it(
    'is listed among the files vsce packages',
    async () => {
      // listFiles applies .vscodeignore without running the vscode:prepublish build.
      const listed = await listFiles({ packagedDependencies: [] });
      expect(listed).toContain('LICENSE');
      // Loading vsce and walking the tree is slow on a loaded runner; the default 5 s is too short.
    },
    LIST_TIMEOUT_MS,
  );

  it('is named in the README', () => {
    expect(readFileSync('README.md', 'utf8')).toContain('licensed under Apache-2.0');
  });
});
