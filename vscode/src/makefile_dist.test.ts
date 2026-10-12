import { execFileSync } from 'node:child_process';
import { copyFileSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

const MAKE_TIMEOUT_MS = 60_000;
const dirs: string[] = [];

function dryRunDist(scripts: Record<string, string>): string {
  const dir = mkdtempSync(join(tmpdir(), 'make-dist-'));
  dirs.push(dir);
  copyFileSync('Makefile', join(dir, 'Makefile'));
  writeFileSync(join(dir, 'package.json'), JSON.stringify({ version: '1.2.3', scripts }));
  return execFileSync('make', ['-n', 'dist'], { cwd: dir, encoding: 'utf8' });
}

afterEach(() => {
  for (const dir of dirs.splice(0)) {
    rmSync(dir, { recursive: true, force: true });
  }
});

describe('make dist', () => {
  it.each([
    ['runs the dist script when package.json has one', { dist: 'node dist.mjs' }, true],
    ['packages one .vsix when package.json has no dist script', { build: 'node build.mjs' }, false],
    ['packages one .vsix when package.json has no scripts', {}, false],
    ['ignores a script that only mentions dist', { 'build:dist': 'x' }, false],
  ])(
    '%s',
    (_name, scripts, usesDistScript) => {
      const plan = dryRunDist(scripts);
      expect(plan.includes('npm run dist')).toBe(usesDistScript);
      expect(plan.includes('cel-lens-vscode-1.2.3.vsix')).toBe(!usesDistScript);
    },
    // Each dry run starts `node` twice through make; a loaded CI runner needs more than 5 s.
    MAKE_TIMEOUT_MS,
  );
});
