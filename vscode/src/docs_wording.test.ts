import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('engine-dependent wording', () => {
  it('names no engine version in the releaseDownloadUrl doc comment', () => {
    const source = readFileSync('src/core_releases.ts', 'utf8');
    expect(source).not.toMatch(/v\d+\.\d+\.\d+/);
  });

  it('says the crd-typed-policies scan runs only while the feature is on', () => {
    const row = readFileSync('README.md', 'utf8')
      .split('\n')
      .find((line) => line.startsWith('| `crd-typed-policies`'));
    expect(row).toContain(
      'runs only while the feature is on, at initialize or when the setting turns it on',
    );
  });
});
