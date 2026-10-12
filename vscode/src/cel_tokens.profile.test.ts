import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const manifest = JSON.parse(readFileSync('package.json', 'utf8')) as {
  scripts: Record<string, string>;
};

describe('the tokenizeCel profile script', () => {
  it.each(['--cpu-prof', '--expose-gc', 'src/cel_tokens.profile.ts'])(
    'npm run profile uses %s',
    (part) => {
      expect(manifest.scripts['profile']).toContain(part);
    },
  );

  it('is named in cel_tokens.profile.md in place of the old driver', () => {
    const doc = readFileSync('src/cel_tokens.profile.md', 'utf8');
    expect(doc).toContain('npm run profile -- 5000');
    expect(doc).not.toContain('drv.cjs');
  });

  it.each([
    'Profiled 5000 rules: median',
    'ms per run (10 warm-up runs, 60 profiled runs).',
    'CPU profile written to out/profile/',
  ])('quotes the script output %s', (part) => {
    expect(readFileSync('src/cel_tokens.profile.md', 'utf8')).toContain(part);
  });
});
