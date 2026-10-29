import { spawnSync } from 'node:child_process';
import { cpSync, mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, relative, resolve } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
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

// Hard-linked, not copied: the same files without the minutes of copying, and unlike symbolic
// links npm list accepts the tree. Hard links need the repository's file system, so when the
// temporary directory is on another one the copy is made beside the repository, as CI does.
function copyTree(root: string, base: string): string {
  const modules = join(root, 'node_modules');
  const parents = [base, dirname(root)];
  for (const [index, parent] of parents.entries()) {
    let folder: string;
    try {
      folder = mkdtempSync(join(parent, 'public-layout-'));
    } catch {
      continue;
    }
    cpSync(root, folder, {
      recursive: true,
      filter: (source) => !ABSENT.has(source.slice(root.length + 1).replaceAll('\\', '/')),
    });
    const linked = spawnSync('cp', ['-al', modules, join(folder, 'node_modules')]);
    if (linked.status === 0) {
      return folder;
    }
    if (index === parents.length - 1) {
      // No file system to link on: copying is the last resort.
      cpSync(modules, join(folder, 'node_modules'), { recursive: true });
      return folder;
    }
    rmSync(folder, { recursive: true, force: true });
  }
  return expect.fail('No folder to copy the repository into.');
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
        copy = copyTree(root, tmpdir());
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
