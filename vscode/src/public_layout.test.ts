import { spawnSync } from 'node:child_process';
import { cpSync, mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, relative, resolve } from 'node:path';
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
const WORKFLOW_SKIP_MARK = 'skipped when .gitea/ is absent';
const RUN_TIMEOUT_MS = 300_000;

function listTestFiles(root: string): string[] {
  return readdirSync(root, { recursive: true, encoding: 'utf8' })
    .map((file) => file.replaceAll('\\', '/'))
    .filter((file) => /^src\/.*\.test\.ts$/.test(file));
}

let copy: string | undefined;

afterEach(() => {
  if (copy !== undefined) {
    rmSync(copy, { recursive: true, force: true });
    copy = undefined;
  }
});

describe.skipIf(!HAS_FACTORY_WORKFLOWS)(
  'the public layout (skipped outside the factory: the copy is made from the factory tree)',
  () => {
    it(
      'passes vitest run in a copy without the factory files, with the installed packages hard-linked',
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
        // Hard-linked, not copied: the same files without the minutes of copying, and
        // unlike symbolic links npm list accepts the tree. Copying is the fallback when
        // the temporary directory is on another file system.
        const modules = join(root, 'node_modules');
        const linked = spawnSync('cp', ['-al', modules, join(copy, 'node_modules')]);
        if (linked.status !== 0) {
          cpSync(modules, join(copy, 'node_modules'), { recursive: true });
        }
        // A clone is a git repository, and some tests list its files.
        for (const args of [
          ['init', '-q'],
          ['add', '.'],
        ]) {
          expect(spawnSync('git', args, { cwd: copy }).status).toBe(0);
        }

        const report = join(copy, 'report.json');
        const run = spawnSync(
          'npx',
          [
            'vitest',
            'run',
            '--exclude',
            'src/public_layout.test.ts',
            '--reporter=json',
            `--outputFile=${report}`,
          ],
          {
            cwd: copy,
            encoding: 'utf8',
            env: { ...process.env, TMPDIR: copy },
          },
        );
        const output = run.stdout + run.stderr;
        expect(run.status, output).toBe(0);

        const result = JSON.parse(readFileSync(report, 'utf8')) as {
          testResults: { name: string; assertionResults: { title: string; status: string }[] }[];
        };
        const nested = copy;
        const ran = result.testResults.map((file) => relative(nested, file.name));
        const expected = listTestFiles(root).filter((file) => file !== 'src/public_layout.test.ts');
        expect(ran.sort()).toEqual(expected.sort());
        // The workflow tests are the ones the missing .gitea/ skips: there must be some,
        // and each one must have been skipped, not run.
        const workflowTests = result.testResults
          .flatMap((file) => file.assertionResults)
          .filter((test) => test.title.includes(WORKFLOW_SKIP_MARK));
        expect(workflowTests.length).toBeGreaterThan(0);
        expect(workflowTests.filter((test) => test.status !== 'skipped')).toEqual([]);
      },
      RUN_TIMEOUT_MS,
    );
  },
);
