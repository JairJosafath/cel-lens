import { spawnSync } from 'node:child_process';
import { mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, relative, resolve } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { copyTree } from './copy_tree';
import { HAS_FACTORY_WORKFLOWS } from './factory_files';
import { describeFailures, type NestedReport } from './nested_report';

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
let nestedTmp: string | undefined;

afterEach(() => {
  if (nestedTmp !== undefined) {
    rmSync(nestedTmp, { recursive: true, force: true });
    nestedTmp = undefined;
  }
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
        copy = copyTree(root, [tmpdir(), dirname(root)], (path) => ABSENT.has(path));
        // A clone is a git repository, and some tests list its files.
        for (const args of [
          ['init', '-q'],
          ['add', '.'],
        ]) {
          expect(spawnSync('git', args, { cwd: copy }).status).toBe(0);
        }

        // Beside the copy, not in it: the tools' short-lived folders must not appear in a tree
        // that other tests copy.
        nestedTmp = mkdtempSync(join(tmpdir(), 'public-layout-tmp-'));
        const report = join(nestedTmp, 'report.json');
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
            env: { ...process.env, TMPDIR: nestedTmp },
          },
        );
        const output = run.stdout + run.stderr;
        let result: NestedReport;
        try {
          result = JSON.parse(readFileSync(report, 'utf8')) as NestedReport;
        } catch {
          expect.fail(`The nested run wrote no readable report:\n${output}`);
        }
        expect(run.status, describeFailures(result, copy) || output).toBe(0);

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
