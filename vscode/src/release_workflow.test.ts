import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve, sep } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { parse } from 'yaml';
import { factoryTitle, HAS_FACTORY_WORKFLOWS } from './factory_files';

const WORKFLOW = '.gitea/workflows/release.yaml';
const MAKE_DIST = /\bmake\s+dist\b/;
// Records the folders a step makes instead of making them.
const MKDIR_PRELUDE = `mkdir() { for a; do case $a in -*) ;; *) printf '%s\\n' "$a" >>"$MKDIR_LOG" ;; esac; done; }\n`;

interface Step {
  name: string;
  run: string;
  env: Record<string, string>;
}

const sandboxes: string[] = [];

afterEach(() => {
  for (const dir of sandboxes.splice(0)) {
    rmSync(dir, { recursive: true, force: true });
  }
});

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function field(record: Record<string, unknown>, key: string): unknown {
  return record[key];
}

function toEnv(value: unknown): Record<string, string> {
  if (!isRecord(value)) {
    return {};
  }
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, String(item)]));
}

function toStep(value: unknown): Step {
  const step = isRecord(value) ? value : {};
  return {
    name: typeof field(step, 'name') === 'string' ? String(field(step, 'name')) : '',
    run: typeof field(step, 'run') === 'string' ? String(field(step, 'run')) : '',
    env: toEnv(field(step, 'env')),
  };
}

function findDistJob(workflow: string): { env: Record<string, string>; steps: Step[] } | undefined {
  const doc: unknown = parse(workflow);
  const jobsField = isRecord(doc) ? field(doc, 'jobs') : undefined;
  const jobs = isRecord(jobsField) ? Object.values(jobsField) : [];
  for (const job of jobs) {
    const stepsField = isRecord(job) ? field(job, 'steps') : undefined;
    if (!isRecord(job) || !Array.isArray(stepsField)) {
      continue;
    }
    const steps = stepsField.map(toStep);
    if (steps.some((step) => MAKE_DIST.test(step.run))) {
      return { env: toEnv(field(job, 'env')), steps };
    }
  }
  return undefined;
}

/** Runs a step's script in a throwaway checkout and returns what it set. */
function runStep(
  step: Step,
  jobEnv: Record<string, string>,
  state: { env: Record<string, string>; root: string; made: string[] },
): void {
  const workspace = join(state.root, 'work', 'repo');
  const expand = (text: string): string =>
    text
      .replace(/\$\{\{\s*(?:github|gitea)\.workspace\s*\}\}/g, workspace)
      .replace(/\$\{\{[^}]*\}\}/g, 'x');
  const githubEnv = join(state.root, 'github_env');
  const mkdirLog = join(state.root, 'mkdir_log');
  writeFileSync(githubEnv, '');
  writeFileSync(mkdirLog, '');
  const env: Record<string, string> = { ...jobEnv, ...state.env, ...step.env };
  const expanded = Object.fromEntries(
    Object.entries(env).map(([key, value]) => [key, expand(value)]),
  );
  const result = spawnSync('sh', ['-c', MKDIR_PRELUDE + expand(step.run)], {
    cwd: workspace,
    env: {
      PATH: process.env['PATH'] ?? '',
      ...expanded,
      GITHUB_WORKSPACE: workspace,
      GITHUB_ENV: githubEnv,
      MKDIR_LOG: mkdirLog,
    },
    encoding: 'utf8',
  });
  if (result.status !== 0) {
    return;
  }
  for (const line of readFileSync(githubEnv, 'utf8').split('\n')) {
    const at = line.indexOf('=');
    if (at > 0) {
      state.env[line.slice(0, at)] = line.slice(at + 1);
    }
  }
  for (const made of readFileSync(mkdirLog, 'utf8').split('\n')) {
    if (made !== '') {
      state.made.push(resolve(workspace, made));
    }
  }
}

/** What is wrong with the TMPDIR that the job's `make dist` step runs with. */
function tmpdirProblems(workflow: string): string[] {
  const job = findDistJob(workflow);
  if (job === undefined) {
    return ['no job runs make dist'];
  }
  const distAt = job.steps.findIndex((step) => MAKE_DIST.test(step.run));
  const root = mkdtempSync(join(process.cwd(), 'node_modules', '.release-workflow-'));
  sandboxes.push(root);
  mkdirSync(join(root, 'work', 'repo'), { recursive: true });
  const workspace = join(root, 'work', 'repo');
  const state = { env: {} as Record<string, string>, root, made: [] as string[] };
  for (const step of job.steps.slice(0, distAt)) {
    if (/TMPDIR|mkdir/.test(step.run)) {
      runStep(step, job.env, state);
    }
  }
  const distStep = job.steps[distAt];
  const tmpdir = distStep?.env['TMPDIR'] ?? state.env['TMPDIR'] ?? job.env['TMPDIR'];
  if (tmpdir === undefined) {
    return ['TMPDIR is not set before the make dist step'];
  }
  const dir = resolve(
    workspace,
    tmpdir.replace(/\$\{\{\s*(?:github|gitea)\.workspace\s*\}\}/g, workspace),
  );
  const problems: string[] = [];
  if (!tmpdir.startsWith('/') && !tmpdir.includes('${{')) {
    problems.push(`TMPDIR ${tmpdir} is a relative path`);
  }
  if (dir === '/tmp' || dir.startsWith('/tmp/')) {
    problems.push(`TMPDIR ${dir} is in /tmp`);
  }
  if (dir === workspace || dir.startsWith(workspace + sep)) {
    problems.push(`TMPDIR ${dir} is inside the checkout`);
  }
  if (!state.made.includes(dir)) {
    problems.push(`no step before make dist creates ${dir}`);
  }
  return problems;
}

/** Where the line of the named step starts; names the step when it is missing. */
function stepStart(text: string, name: string): number {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const at = text.search(new RegExp(`^[ \\t]*- name: ${escaped}[ \\t]*$`, 'm'));
  if (at < 0) {
    throw new Error(`the release workflow has no step named "${name}"`);
  }
  return at;
}

function stepBounds(text: string): { tmpdirAt: number; distAt: number; attachAt: number } {
  const bounds = {
    tmpdirAt: stepStart(text, 'Temporary folder'),
    distAt: stepStart(text, 'make dist'),
    attachAt: stepStart(text, 'Attach dist/ to the release'),
  };
  if (bounds.tmpdirAt > bounds.distAt) {
    throw new Error(
      'the release workflow has the "make dist" step before the "Temporary folder" step',
    );
  }
  if (bounds.distAt > bounds.attachAt) {
    throw new Error(
      'the release workflow has the "Attach dist/ to the release" step before the "make dist" step',
    );
  }
  return bounds;
}

const JOB_ENV = '    env:\n';
const DIR_LINE = /^ +dir=.*$/m;

/** Adds a TMPDIR line to the job's env: block; names env: when the block is missing. */
function addJobEnv(text: string, tmpdir: string): string {
  if (!text.includes(JOB_ENV)) {
    throw new Error('the release workflow has no job env: block');
  }
  return text.replace(JOB_ENV, `${JOB_ENV}      TMPDIR: ${tmpdir}\n`);
}

/** Replaces the script's dir= line; names it when the line is missing. */
function replaceDirLine(text: string, line: string): string {
  if (!DIR_LINE.test(text)) {
    throw new Error('the release workflow has no dir= line in its script');
  }
  return text.replace(DIR_LINE, `          ${line}`);
}

// Without the factory's file a placeholder lets the tables below build; their tests skip.
const workflow = HAS_FACTORY_WORKFLOWS
  ? readFileSync(WORKFLOW, 'utf8')
  : `${JOB_ENV}          dir=placeholder\n`;
const {
  tmpdirAt: TMPDIR_STEP_AT,
  distAt: DIST_STEP_AT,
  attachAt: ATTACH_STEP_AT,
} = HAS_FACTORY_WORKFLOWS ? stepBounds(workflow) : { tmpdirAt: 0, distAt: 0, attachAt: 0 };
const tmpdirStep = workflow.slice(TMPDIR_STEP_AT, DIST_STEP_AT);
const distStep = workflow.slice(DIST_STEP_AT, ATTACH_STEP_AT);

function withTmpdirStep(replacement: string): string {
  return workflow.slice(0, TMPDIR_STEP_AT) + replacement + workflow.slice(DIST_STEP_AT);
}

function withJobEnv(tmpdir: string, step = ''): string {
  return addJobEnv(withTmpdirStep(step), tmpdir);
}

function withDirLine(line: string): string {
  return replaceDirLine(workflow, line);
}

const MKDIR_STEP = (dir: string): string =>
  `      - name: Make folder\n        run: mkdir -p "${dir}"\n`;

describe.skipIf(!HAS_FACTORY_WORKFLOWS)(factoryTitle('release workflow TMPDIR'), () => {
  it.each(['Temporary folder', 'make dist', 'Attach dist/ to the release'])(
    'fails naming the step when "%s" is renamed',
    (name) => {
      const renamed = workflow.replace(`- name: ${name}`, `- name: ${name} renamed`);
      expect(() => stepBounds(renamed)).toThrow(`no step named "${name}"`);
    },
  );

  it('fails naming env: when the job has no env: block', () => {
    const without = workflow.replace(/^ {4}env:\n(?: {6}.*\n)+/m, '');
    expect(() => addJobEnv(without, '/var/job-tmp')).toThrow('env:');
  });

  it('fails naming the dir= line when the script has none', () => {
    const without = workflow.replace(/^ +dir=.*$/m, '');
    expect(() => replaceDirLine(without, 'dir=/tmp/job')).toThrow('dir=');
  });

  it('fails naming both steps when make dist comes before Temporary folder', () => {
    const swapped =
      workflow.slice(0, TMPDIR_STEP_AT) + distStep + tmpdirStep + workflow.slice(ATTACH_STEP_AT);
    expect(() => stepBounds(swapped)).toThrow(/"make dist".*"Temporary folder"/);
  });

  it('is outside /tmp and the checkout, created before make dist, as the workflow stands', () => {
    expect(tmpdirProblems(workflow)).toEqual([]);
  });

  it('is accepted as a job env outside /tmp and the checkout that an earlier step creates', () => {
    expect(tmpdirProblems(withJobEnv('/var/job-tmp', MKDIR_STEP('/var/job-tmp')))).toEqual([]);
  });

  it.each([
    ['sets no TMPDIR', withTmpdirStep(''), 'is not set'],
    [
      'sets TMPDIR only after make dist',
      workflow.slice(0, TMPDIR_STEP_AT) + distStep + tmpdirStep + workflow.slice(ATTACH_STEP_AT),
      'is not set',
    ],
    ['sets TMPDIR to /tmp', withJobEnv('/tmp', MKDIR_STEP('/tmp')), 'is in /tmp'],
    ['sets TMPDIR under /tmp', withJobEnv('/tmp/job', MKDIR_STEP('/tmp/job')), 'is in /tmp'],
    ['has its script choose a folder in /tmp', withDirLine('dir=/tmp/job'), 'is not set'],
    [
      'has its script choose a folder in the checkout',
      withDirLine('dir="$GITHUB_WORKSPACE/tmp"'),
      'inside the checkout',
    ],
    [
      'sets TMPDIR inside the checkout',
      withJobEnv('${{ github.workspace }}/tmp', MKDIR_STEP('${{ github.workspace }}/tmp')),
      'inside the checkout',
    ],
    ['sets TMPDIR to a relative path', withJobEnv('tmp', MKDIR_STEP('tmp')), 'relative path'],
    ['never creates the TMPDIR folder', withJobEnv('/var/job-tmp'), 'creates'],
    [
      'creates a different folder than TMPDIR',
      withJobEnv('/var/job-tmp', MKDIR_STEP('/var/other')),
      'creates',
    ],
  ])('fails when the workflow %s', (_name, broken, expected) => {
    expect(tmpdirProblems(broken).join('\n')).toContain(expected);
  });
});
