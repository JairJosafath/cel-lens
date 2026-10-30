import { spawnSync } from 'node:child_process';
import { describe, expect, it } from 'vitest';
import packageJson from '../package.json';

const run = (version: string, packageVersion: string) =>
  spawnSync(
    'make',
    ['release-version', `VERSION=${version}`, `PACKAGE_VERSION=${packageVersion}`],
    {
      encoding: 'utf8',
    },
  );

describe('make release-version', () => {
  it('passes on the current release commit', () => {
    const result = run(`v${packageJson.version}`, packageJson.version);
    expect(result.status).toBe(0);
  });

  it('fails with a clear message when CHANGELOG.md has no heading for the version', () => {
    const result = run('v99.99.99', '99.99.99');
    expect(result.status).not.toBe(0);
    expect(result.stderr).toContain('CHANGELOG.md has no "## [99.99.99]" heading.');
  });
});
