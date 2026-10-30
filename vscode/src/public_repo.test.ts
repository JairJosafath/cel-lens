import { execFileSync, spawnSync } from 'node:child_process';
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';

import { afterEach, describe, expect, it } from 'vitest';
import { parse } from 'yaml';

const FOLDER = 'public-repo';
// The published source has no public-repo/: the folder is kept out of it.
const HAS_FOLDER = existsSync(FOLDER);
// A copy made by another test is a repository without a commit.
const HAS_HEAD = spawnSync('git', ['rev-parse', '--verify', 'HEAD']).status === 0;
const CI_PATH = `${FOLDER}/.github/workflows/ci.yml`;
const SCORECARD_PATH = `${FOLDER}/.github/workflows/scorecard.yml`;
const SHA_PINNED = /^[^@\s]+@[0-9a-f]{40}$/;
const BADGE_SOURCES = [
  /^https:\/\/github\.com\/JairJosafath\/cel-lens\/actions\/workflows\/ci\.yml\/badge\.svg$/,
  /^https:\/\/img\.shields\.io\/github\/v\/release\/JairJosafath\/cel-lens$/,
  /^https:\/\/img\.shields\.io\/github\/license\/JairJosafath\/cel-lens$/,
  /^https:\/\/api\.scorecard\.dev\/projects\/github\.com\/JairJosafath\/cel-lens\/badge$/,
  /^https:\/\/pkg\.go\.dev\/badge\/github\.com\/JairJosafath\/cel-lens\/engine(\.svg)?$/,
];
// What the published vscode/ lacks: the factory's files, public-repo/, and build output.
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
  FOLDER,
]);
// What the root documents link to in engine/.
const ENGINE_FILES = ['README.md', 'CONTRIBUTING.md', 'SECURITY.md', 'go.mod'];

interface Step {
  uses?: string;
  run?: string;
  'working-directory'?: string;
}
interface Job {
  permissions?: unknown;
  steps?: Step[];
}
interface Workflow {
  on?: Record<string, unknown>;
  permissions?: unknown;
  jobs?: Record<string, Job>;
}

function readWorkflow(path: string): Workflow {
  return parse(readFileSync(path, 'utf8')) as Workflow;
}

function filesUnder(dir: string): string[] {
  return readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) => join(entry.parentPath, entry.name));
}

function usesOf(workflow: Workflow): string[] {
  return Object.values(workflow.jobs ?? {})
    .flatMap((job) => job.steps ?? [])
    .flatMap((step) => (step.uses === undefined ? [] : [step.uses]));
}

function markdownTargets(text: string): string[] {
  // Every ](target) or ](target "title") part, so a badge's picture and its outer link are both
  // targets, then every [id]: target definition of a reference-style link.
  const inline = text.matchAll(/\]\(\s*([^)\s]+)(?:\s+(?:"[^"]*"|'[^']*'))?\s*\)/g);
  const definitions = text.matchAll(/^ {0,3}\[(?!\^)[^\]]+\]:[ \t]*(\S+)/gm);
  return [...inline, ...definitions].flatMap((match) => (match[1] === undefined ? [] : [match[1]]));
}

function badges(readme: string): string[] {
  return [...readme.matchAll(/\[!\[[^\]]*\]\(([^)\s]+)\)\]\([^)\s]+\)/g)].flatMap((match) =>
    match[1] === undefined ? [] : [match[1]],
  );
}

let layout: string | undefined;

describe('markdownTargets', () => {
  it.each([
    ['[a](x.md)', ['x.md']],
    ['![pic](p.gif)', ['p.gif']],
    ['[![badge](b.svg)](LICENSE)', ['b.svg', 'LICENSE']],
    ['[![a](a.svg)](A) and [![b](b.svg)](B)', ['a.svg', 'A', 'b.svg', 'B']],
    ['[a](x.md "The title")', ['x.md']],
    ['[a](x.md \'The title\') ![p](p.gif "t")', ['x.md', 'p.gif']],
    ['[a][id]\n\n[id]: docs/x.md', ['docs/x.md']],
    ['[a][id]\n\n[id]: docs/x.md "Title"\n[Other]: y.md', ['docs/x.md', 'y.md']],
    ['text[^1]\n\n[^1]: a note', []],
  ])('finds the targets of %s', (text, expected) => {
    expect(markdownTargets(text)).toEqual(expected);
  });
});

// Builds the published layout and lists the relative links of public-repo/ that point at nothing.
// engine/ comes from the checkout when one is given, else from empty files.
function missingLinks(engineCheckout: string | undefined): string[] {
  layout = mkdtempSync(join(tmpdir(), 'public-repo-'));
  const root = resolve('.');
  cpSync(FOLDER, layout, { recursive: true });
  // Entry by entry: a copy cannot go inside itself, and the layout may sit under the root.
  for (const entry of readdirSync(root)) {
    const source = join(root, entry);
    if (!ABSENT.has(entry) && !layout.startsWith(source)) {
      cpSync(source, join(layout, 'vscode', entry), { recursive: true });
    }
  }
  rmSync(join(layout, 'vscode', 'docs', 'agents'), { recursive: true, force: true });
  if (engineCheckout === undefined || engineCheckout === '') {
    mkdirSync(join(layout, 'engine'));
    for (const file of ENGINE_FILES) {
      writeFileSync(join(layout, 'engine', file), '');
    }
  } else {
    cpSync(engineCheckout, join(layout, 'engine'), {
      recursive: true,
      filter: (source) => !['.git', 'node_modules'].includes(basename(source)),
    });
  }

  const built = layout;
  return filesUnder(FOLDER)
    .filter((file) => file.endsWith('.md'))
    .flatMap((file) =>
      markdownTargets(readFileSync(file, 'utf8'))
        .filter((target) => !/^([a-z][a-z0-9+.-]*:|#)/i.test(target))
        .map((target) => ({ file, target }))
        .filter(
          ({ file: from, target }) =>
            !existsSync(
              join(built, dirname(from.slice(FOLDER.length + 1)), target.split('#')[0] ?? ''),
            ),
        )
        .map(({ file: from, target }) => `${from}: ${target}`),
    );
}

afterEach(() => {
  if (layout !== undefined) {
    rmSync(layout, { recursive: true, force: true });
    layout = undefined;
  }
});

describe.skipIf(!HAS_FOLDER)('public-repo/', () => {
  const readme = HAS_FOLDER ? readFileSync(`${FOLDER}/README.md`, 'utf8') : '';

  it.each([
    'README.md',
    'CONTRIBUTING.md',
    'SECURITY.md',
    'CODE_OF_CONDUCT.md',
    'LICENSE',
    '.github/ISSUE_TEMPLATE/bug.yml',
    '.github/ISSUE_TEMPLATE/feature.yml',
    '.github/ISSUE_TEMPLATE/config.yml',
    '.github/workflows/ci.yml',
    '.github/workflows/scorecard.yml',
  ])('holds %s', (file) => {
    expect(existsSync(`${FOLDER}/${file}`)).toBe(true);
  });

  it('has the same LICENSE as this repository', () => {
    expect(readFileSync(`${FOLDER}/LICENSE`, 'utf8')).toBe(readFileSync('LICENSE', 'utf8'));
  });

  it('keeps .gitattributes export-ignoring the folder', () => {
    expect(readFileSync('.gitattributes', 'utf8').split('\n')).toContain(
      `${FOLDER}/ export-ignore`,
    );
  });

  it.skipIf(!HAS_HEAD)('is left out of git archive HEAD', () => {
    const entries = execFileSync('git', ['archive', 'HEAD'], { maxBuffer: 1 << 28 });
    const names = execFileSync('tar', ['-t'], { input: entries, encoding: 'utf8' });

    expect(names.split('\n').filter((name) => name.startsWith(`${FOLDER}/`))).toEqual([]);
  });

  it.each([CI_PATH, SCORECARD_PATH])(
    'parses %s as YAML with every action pinned by SHA',
    (path) => {
      const uses = usesOf(readWorkflow(path));

      expect(uses.length).toBeGreaterThan(0);
      expect(uses.filter((use) => !SHA_PINNED.test(use))).toEqual([]);
    },
  );

  it.each([CI_PATH, SCORECARD_PATH])('has a version comment after every pin in %s', (path) => {
    const lines = readFileSync(path, 'utf8')
      .split('\n')
      .filter((line) => line.includes('uses:'));

    expect(lines.filter((line) => !/@[0-9a-f]{40} # v\d/.test(line))).toEqual([]);
  });

  it('asks for read-all at the top of both workflows, and writes only in the Scorecard job', () => {
    const ci = readWorkflow(CI_PATH);
    const scorecard = readWorkflow(SCORECARD_PATH);

    expect(ci.permissions).toBe('read-all');
    expect(scorecard.permissions).toBe('read-all');
    expect(Object.values(ci.jobs ?? {}).map((job) => job.permissions)).toEqual([
      undefined,
      undefined,
    ]);
    expect(Object.values(scorecard.jobs ?? {}).map((job) => job.permissions)).toEqual([
      { 'security-events': 'write', 'id-token': 'write' },
    ]);
  });

  it('runs the Scorecard action on push to main and weekly', () => {
    const scorecard = readWorkflow(SCORECARD_PATH);

    expect(scorecard.on).toMatchObject({ push: { branches: ['main'] } });
    expect(scorecard.on).toHaveProperty('schedule');
    expect(usesOf(scorecard).some((use) => use.startsWith('ossf/scorecard-action@'))).toBe(true);
  });

  it('runs both test commands in their folders on push and pull request', () => {
    const ci = readWorkflow(CI_PATH);
    const steps = Object.values(ci.jobs ?? {}).flatMap((job) => job.steps ?? []);
    const run = (command: string): string | undefined =>
      steps.find((step) => step.run === command)?.['working-directory'];

    expect(Object.keys(ci.on ?? {})).toEqual(['push', 'pull_request']);
    expect(run('go test ./...')).toBe('engine');
    expect(run('npm ci && npm test')).toBe('vscode');
  });

  it('uses the Go version of engine/go.mod and Node 24', () => {
    const text = readFileSync(CI_PATH, 'utf8');

    expect(text).toContain('go-version-file: engine/go.mod');
    expect(text).toContain('node-version: 24');
  });

  it('has exactly five badges, each from one of the five live sources', () => {
    const found = badges(readme);

    expect(found).toHaveLength(5);
    expect(BADGE_SOURCES.map((source) => found.filter((url) => source.test(url)).length)).toEqual([
      1, 1, 1, 1, 1,
    ]);
  });

  it('checks the outer link of each of the five badges among its targets', () => {
    const outer = [...readme.matchAll(/\[!\[[^\]]*\]\([^)\s]+\)\]\(([^)\s]+)\)/g)].flatMap(
      (match) => (match[1] === undefined ? [] : [match[1]]),
    );

    expect(outer).toHaveLength(5);
    expect(markdownTargets(readme)).toEqual(expect.arrayContaining(outer));
  });

  it('shows the two GIFs by their published paths', () => {
    const targets = markdownTargets(readme);

    expect(targets).toContain('vscode/images/colors.gif');
    expect(targets).toContain('vscode/images/completion.gif');
  });

  it.each([
    'releases',
    'go install github.com/JairJosafath/cel-lens/engine@',
    '`cel-lens.experimental`',
    '(engine/)',
    '(vscode/)',
    '(CONTRIBUTING.md)',
    '(SECURITY.md)',
  ])('has %s in the README', (text) => {
    expect(readme).toContain(text);
  });

  it('sends security reports to private reporting and disables blank issues', () => {
    const config = parse(readFileSync(`${FOLDER}/.github/ISSUE_TEMPLATE/config.yml`, 'utf8')) as {
      blank_issues_enabled: boolean;
      contact_links: { url: string }[];
    };

    expect(config.blank_issues_enabled).toBe(false);
    expect(config.contact_links.map((link) => link.url)).toContain(
      'https://github.com/JairJosafath/cel-lens/security/advisories/new',
    );
    expect(readFileSync(`${FOLDER}/SECURITY.md`, 'utf8')).toContain(
      'private vulnerability reporting',
    );
  });

  it('gives the CNCF Code of Conduct a reporting address', () => {
    const text = readFileSync(`${FOLDER}/CODE_OF_CONDUCT.md`, 'utf8');

    expect(text).toContain('CNCF');
    expect(text).toContain('conduct@cncf.io');
  });

  it('says nothing is paused, and names no factory, Gitea or devcontainer', () => {
    const offenders = filesUnder(FOLDER).filter((file) =>
      /paused|factory|gitea|devcontainer|c4factory/i.test(readFileSync(file, 'utf8')),
    );

    expect(offenders).toEqual([]);
  });

  it('resolves every relative link and picture of its Markdown in the published layout', () => {
    // CEL_LENS_ENGINE_DIR names a checkout of the engine; without it engine/ is an empty tree.
    expect(missingLinks(process.env['CEL_LENS_ENGINE_DIR'])).toEqual([]);
  });

  it('fails on a link to a file the engine checkout lacks', () => {
    const checkout = mkdtempSync(join(tmpdir(), 'engine-'));
    try {
      writeFileSync(join(checkout, 'README.md'), '');
      writeFileSync(join(checkout, 'go.mod'), '');

      const missing = missingLinks(checkout);

      expect(missing.some((entry) => entry.endsWith('engine/CONTRIBUTING.md'))).toBe(true);
      expect(missing.some((entry) => entry.endsWith('engine/SECURITY.md'))).toBe(true);
    } finally {
      rmSync(checkout, { recursive: true, force: true });
    }
  });

  it('passes with the empty engine tree when no checkout is given', () => {
    expect(missingLinks(undefined)).toEqual([]);
  });
});
