import { spawnSync } from 'node:child_process';
import { cpSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { HAS_FACTORY_WORKFLOWS } from './factory_files';

// What the published source lacks: the factory's own files, and node_modules.
const ABSENT = new Set([
  '.gitea',
  'AGENTS.md',
  'docs/agents',
  '.c4factory.yaml',
  '.devcontainer',
  'node_modules',
  '.git',
  'out',
  'dist',
  'bin',
]);
const RUN_TIMEOUT_MS = 300_000;
// Deleting the copied node_modules outlasts vitest's 10 s hook default.
const CLEANUP_TIMEOUT_MS = 120_000;

let copy: string | undefined;

afterEach(() => {
  if (copy !== undefined) {
    rmSync(copy, { recursive: true, force: true });
    copy = undefined;
  }
}, CLEANUP_TIMEOUT_MS);

describe.skipIf(!HAS_FACTORY_WORKFLOWS)(
  'the public layout (skipped outside the factory: the copy is made from the factory tree)',
  () => {
    it(
      'passes vitest run in a copy without the factory files and without node_modules',
      () => {
        const root = resolve('.');
        copy = mkdtempSync(join(tmpdir(), 'public-layout-'));
        cpSync(root, copy, {
          recursive: true,
          filter: (source) => {
            const relative = source.slice(root.length + 1).replaceAll('\\', '/');
            return !ABSENT.has(relative);
          },
        });
        // The installed packages are copied, not linked: npm list rejects a linked tree.
        cpSync(join(root, 'node_modules'), join(copy, 'node_modules'), { recursive: true });
        // A clone is a git repository, and some tests list its files.
        for (const args of [
          ['init', '-q'],
          ['add', '.'],
        ]) {
          expect(spawnSync('git', args, { cwd: copy }).status).toBe(0);
        }

        const run = spawnSync('npx', ['vitest', 'run', '--exclude', 'src/public_layout.test.ts'], {
          cwd: copy,
          encoding: 'utf8',
          env: { ...process.env, TMPDIR: copy },
        });

        expect(run.stdout + run.stderr).toContain('skipped');
        expect(run.status, run.stdout + run.stderr).toBe(0);
      },
      RUN_TIMEOUT_MS,
    );
  },
);
