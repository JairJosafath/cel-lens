import { spawnSync } from 'node:child_process';
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { parse } from 'yaml';
import { factoryTitle, HAS_FACTORY_WORKFLOWS } from './factory_files';

const WORKFLOWS = ['.gitea/workflows/check.yaml', '.gitea/workflows/release.yaml'];
const CLEANUP = 'rm -rf "$TMPDIR"';
const GUARD = ['case "${TMPDIR:-}" in', '"" | /tmp | /tmp/*) exit 0 ;;', 'esac'];

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Whether a run script ends with the cleanup, possibly after a guard that skips it. */
function endsWithCleanup(run: unknown): boolean {
  return typeof run === 'string' && run.trimEnd().split('\n').at(-1) === CLEANUP;
}

/** Whether the script skips the cleanup, before it, when TMPDIR is empty, /tmp or under /tmp. */
function guardsCleanup(run: unknown): boolean {
  if (typeof run !== 'string') {
    return false;
  }
  const lines = run
    .trimEnd()
    .split('\n')
    .map((line) => line.trim());
  return GUARD.join('\n') === lines.slice(0, -1).join('\n');
}

/** What is wrong with the cleanup at the end of each job of a workflow. */
function cleanupProblems(workflow: string): string[] {
  const doc: unknown = parse(workflow);
  const jobs = isRecord(doc) && isRecord(doc['jobs']) ? Object.entries(doc['jobs']) : [];
  const problems: string[] = [];
  for (const [name, job] of jobs) {
    const steps = isRecord(job) && Array.isArray(job['steps']) ? job['steps'] : [];
    const runs = steps.map((step) => (isRecord(step) ? step : {}));
    const at = runs.findIndex((step) => endsWithCleanup(step['run']));
    if (at < 0) {
      problems.push(`job ${name} has no step that runs ${CLEANUP}`);
    } else if (at !== runs.length - 1) {
      problems.push(`job ${name} does not end with the cleanup step`);
    } else if (runs[at]?.['if'] !== 'always()') {
      problems.push(`job ${name} cleanup step is not if: always()`);
    } else if (!guardsCleanup(runs[at]['run'])) {
      problems.push(`job ${name} cleanup step lacks the guard against an empty or /tmp TMPDIR`);
    }
  }
  return jobs.length === 0 ? ['no jobs'] : problems;
}

/** The run script of the last step of every job of a workflow. */
function cleanupScripts(workflow: string): string[] {
  const doc: unknown = parse(workflow);
  const jobs = isRecord(doc) && isRecord(doc['jobs']) ? Object.values(doc['jobs']) : [];
  return jobs.flatMap((job) => {
    const steps = isRecord(job) && Array.isArray(job['steps']) ? job['steps'] : [];
    const last: unknown = steps.at(-1);
    const run = isRecord(last) ? last['run'] : undefined;
    return typeof run === 'string' ? [run] : [];
  });
}

/**
 * Runs a cleanup script with a stand-in `rm` that only records its arguments, so a
 * wrong guard cannot delete anything. Returns the arguments `rm` got, one per line.
 */
function removedBy(script: string, tmpdirValue: string | undefined): string[] {
  const dir = mkdtempSync(join(tmpdir(), 'cleanup-test-'));
  try {
    const log = join(dir, 'rm.log');
    const bin = join(dir, 'rm');
    writeFileSync(bin, `#!/bin/sh\nprintf '%s\\n' "$@" >>'${log}'\n`);
    chmodSync(bin, 0o755);
    const env: Record<string, string> = { PATH: dir };
    if (tmpdirValue !== undefined) {
      env['TMPDIR'] = tmpdirValue;
    }
    spawnSync('/bin/bash', ['-e', '-c', script], { env });
    try {
      return readFileSync(log, 'utf8').trimEnd().split('\n');
    } catch {
      return [];
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

const OUTSIDE = '/var/ci/work';
const KEPT = [undefined, '', '/tmp', '/tmp/x'];

/** The TMPDIR values for which the script removes something it must keep. */
function keptProblems(script: string): string[] {
  return KEPT.filter((value) => removedBy(script, value).length > 0).map(
    (value) => `removes ${value ?? 'unset TMPDIR'}`,
  );
}

const GOOD = `jobs:
  check:
    steps:
      - run: make check
      - if: always()
        run: |
          case "\${TMPDIR:-}" in
          "" | /tmp | /tmp/*) exit 0 ;;
          esac
          rm -rf "$TMPDIR"
`;

describe('workflow TMPDIR cleanup', () => {
  it.skipIf(!HAS_FACTORY_WORKFLOWS).each(WORKFLOWS)(
    factoryTitle('%s removes TMPDIR at the end of the job even after a failure'),
    (file) => {
      expect(cleanupProblems(readFileSync(file, 'utf8'))).toEqual([]);
    },
  );

  it('accepts a job that ends with an always-run cleanup', () => {
    expect(cleanupProblems(GOOD)).toEqual([]);
  });

  it.each([
    ['has no cleanup step', GOOD.replace(/ {6}- if: always\(\)\n {8}run: .*\n/, ''), 'has no step'],
    ['lacks always()', GOOD.replace('if: always()', 'if: success()'), 'not if: always()'],
    [
      'has no if at all',
      GOOD.replace(/ {6}- if: always\(\)\n {8}run/, '      - run'),
      'not if: always()',
    ],
    ['has the cleanup not last', GOOD + '      - run: echo done\n', 'does not end'],
    ['has no guard', GOOD.replace(/ {10}case[\s\S]*esac\n/, ''), 'lacks the guard'],
    ['lets /tmp through the guard', GOOD.replace('/tmp | /tmp/*', '/tmp/*'), 'lacks the guard'],
    ['removes the wrong path', GOOD.replace('$TMPDIR', '/tmp'), 'has no step'],
  ])('fails when the workflow %s', (_name, broken, expected) => {
    expect(cleanupProblems(broken).join('\n')).toContain(expected);
  });

  describe('cleanup script', () => {
    const scripts: [string, string][] = WORKFLOWS.flatMap((file) =>
      HAS_FACTORY_WORKFLOWS
        ? cleanupScripts(readFileSync(file, 'utf8')).map((script): [string, string] => [
            file,
            script,
          ])
        : [[file, '']],
    );

    it.skipIf(!HAS_FACTORY_WORKFLOWS).each(scripts)(
      factoryTitle('%s removes nothing when TMPDIR is unset, /tmp or /tmp/x'),
      (_file, script) => {
        expect(keptProblems(script)).toEqual([]);
      },
    );

    it.skipIf(!HAS_FACTORY_WORKFLOWS).each(scripts)(
      factoryTitle('%s removes a TMPDIR outside /tmp'),
      (_file, script) => {
        expect(removedBy(script, OUTSIDE)).toEqual(['-rf', OUTSIDE]);
      },
    );

    it('accepts the sample script', () => {
      expect(keptProblems(cleanupScripts(GOOD)[0] ?? '')).toEqual([]);
      expect(removedBy(cleanupScripts(GOOD)[0] ?? '', OUTSIDE)).toEqual(['-rf', OUTSIDE]);
    });

    it.each([
      ['always exits before the rm', GOOD.replace('esac\n', 'esac\n          exit 0\n')],
      ['has no /tmp guard', GOOD.replace(/ {10}case[\s\S]*esac\n/, '')],
      ['lets /tmp through', GOOD.replace('/tmp | /tmp/*', '/tmp/*')],
    ])('is caught when the script %s', (_name, broken) => {
      const script = cleanupScripts(broken)[0] ?? '';
      const removed = removedBy(script, OUTSIDE);
      expect(keptProblems(script).length > 0 || removed.join(' ') !== `-rf ${OUTSIDE}`).toBe(true);
    });
  });
});
