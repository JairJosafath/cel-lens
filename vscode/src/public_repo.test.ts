import { execFileSync, spawnSync } from 'node:child_process';
import {
  chmodSync,
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

import MarkdownIt from 'markdown-it';
import { afterEach, describe, expect, it } from 'vitest';
import { parse } from 'yaml';

const FOLDER = 'public-repo';
// The published source has no public-repo/: the folder is kept out of it.
const HAS_FOLDER = existsSync(FOLDER);
// A copy made by another test is a repository without a commit.
const HAS_HEAD = spawnSync('git', ['rev-parse', '--verify', 'HEAD']).status === 0;
const WORKFLOWS = `${FOLDER}/.github/workflows`;
const CI_PATH = `${WORKFLOWS}/ci.yml`;
const CODEQL_PATH = `${WORKFLOWS}/codeql.yml`;
const SCORECARD_PATH = `${WORKFLOWS}/scorecard.yml`;
const SIGN_RELEASE_PATH = `${WORKFLOWS}/sign-release.yml`;
const DEPENDABOT_PATH = `${FOLDER}/.github/dependabot.yml`;
// Every workflow the folder holds, so one added later is checked without being named here.
const WORKFLOW_PATHS = HAS_FOLDER
  ? readdirSync(WORKFLOWS)
      .sort()
      .map((name) => `${WORKFLOWS}/${name}`)
  : [];
const SHA_PINNED = /^[^@\s]+@[0-9a-f]{40}$/;
const REPOSITORY = 'JairJosafath/cel-lens';
const SIGNING_WORKFLOW = `${REPOSITORY}/.github/workflows/sign-release.yml`;
// The identity in the certificate Sigstore gives a run of the signing workflow, up to the ref the
// run started from: SIGNER@ref.
const SIGNER = `https://github.com/${SIGNING_WORKFLOW}`;
// Who gives a GitHub workflow that identity.
const ISSUER = 'https://token.actions.githubusercontent.com';
const TAG = 'core-v0.27.0';
// A run for a published release starts from the release's tag, a run by hand from main.
const TAG_REF = `refs/tags/${TAG}`;
const MAIN_REF = 'refs/heads/main';
// The expression that gives the signing step its tag.
const TAG_EXPRESSION = '${{ github.event.release.tag_name || inputs.tag }}';
// The files of a release, in the order the shell lists them.
const RELEASE_FILES = {
  'cel-lens-core_v0.27.0_linux_amd64': 'the engine',
  'checksums.txt': 'the sums',
};
// A stand-in for gh, where the release of a tag is the folder $RELEASES/<tag>. It reads its
// arguments as gh does: up to a --, one that starts with a dash is a flag, and the others are the
// tag, then the files. As gh does, it downloads from the latest release when it gets no tag, the
// source in place of the files with --archive, fails on a tag without a release and on a release
// without files, and uploads over a file of the release only with --clobber.
const GH = [
  '#!/bin/bash',
  'set -eu',
  'echo "gh $*" >>"$CALLS"',
  'command="$1 $2"',
  'shift 2',
  'dir=.',
  'clobber=false',
  'archive=false',
  'names=()',
  'while [ $# -gt 0 ]; do',
  '  case "$1" in',
  '  --repo) shift ;;',
  '  --dir)',
  '    dir=$2',
  '    shift',
  '    ;;',
  '  --clobber) clobber=true ;;',
  '  --archive=*) archive=true ;;',
  '  --)',
  '    shift',
  '    names+=("$@")',
  '    break',
  '    ;;',
  '  -*)',
  '    echo "unknown flag: $1" >&2',
  '    exit 1',
  '    ;;',
  '  *) names+=("$1") ;;',
  '  esac',
  '  shift',
  'done',
  'case "$command" in',
  "'release download')",
  '  release="$RELEASES/${names[0]:-$(ls "$RELEASES" | tail -n 1)}"',
  '  if [ ! -d "$release" ]; then',
  "    echo 'release not found' >&2",
  '    exit 1',
  '  fi',
  '  mkdir "$dir"',
  '  if $archive; then',
  '    echo "the source of ${release##*/}" >"$dir/source.zip"',
  '  elif [ -z "$(ls "$release")" ]; then',
  "    echo 'no assets to download' >&2",
  '    exit 1',
  '  else',
  '    cp "$release"/* "$dir/"',
  '  fi',
  '  ;;',
  "'release upload')",
  '  if [ ${#names[@]} -lt 2 ]; then',
  "    echo 'requires at least 2 arg(s)' >&2",
  '    exit 1',
  '  fi',
  '  release="$RELEASES/${names[0]}"',
  '  if [ -z "${names[0]}" ] || [ ! -d "$release" ]; then',
  "    echo 'release not found' >&2",
  '    exit 1',
  '  fi',
  '  for file in "${names[@]:1}"; do',
  '    if [ -e "$release/${file##*/}" ] && ! $clobber; then',
  '      echo "asset under the same name already exists: ${file##*/}" >&2',
  '      exit 1',
  '    fi',
  '    cp "$file" "$release/"',
  '  done',
  '  ;;',
  'esac',
  '',
].join('\n');
// A stand-in for cosign: a signature is the text of the file and the identity that signed it, and
// verify-blob answers what $VERIFY_STATUS says.
const COSIGN = [
  '#!/bin/bash',
  'set -eu',
  'echo "cosign $*" >>"$CALLS"',
  'case "$1" in',
  'sign-blob)',
  '  while [ $# -gt 1 ]; do',
  '    if [ "$1" = --bundle ]; then',
  '      bundle=$2',
  '    fi',
  '    shift',
  '  done',
  '  echo "$(cat "$1"), signed by https://github.com/$GITHUB_WORKFLOW_REF" >"$bundle"',
  '  ;;',
  'verify-blob)',
  '  exit "$VERIFY_STATUS"',
  '  ;;',
  'esac',
  '',
].join('\n');
const NEWER_RUNS_CANCEL_OLDER = {
  group: '${{ github.workflow }}-${{ github.ref }}',
  'cancel-in-progress': true,
};
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
  if?: string;
  uses?: string;
  with?: Record<string, unknown>;
  env?: Record<string, string>;
  run?: string;
  'working-directory'?: string;
}
interface Job {
  permissions?: unknown;
  strategy?: { matrix?: unknown };
  steps?: Step[];
}
interface Workflow {
  on?: Record<string, unknown>;
  permissions?: unknown;
  jobs?: Record<string, Job>;
  concurrency?: unknown;
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

// The script of every step of the workflow that runs one.
function scriptsOf(workflow: Workflow): string[] {
  return Object.values(workflow.jobs ?? {})
    .flatMap((job) => job.steps ?? [])
    .flatMap((step) => (step.run === undefined ? [] : [step.run]));
}

// Each permission a job of the workflow asks to write, as "job: permission".
function writesOf(workflow: Workflow): string[] {
  return Object.entries(workflow.jobs ?? {}).flatMap(([name, job]) =>
    Object.entries(job.permissions ?? {})
      .filter(([, access]) => access === 'write')
      .map(([permission]) => `${name}: ${permission}`),
  );
}

// The part of a Markdown text under the `## heading`, up to the next heading of that level.
function sectionOf(text: string, heading: string): string {
  return text.split(/^(?=## )/m).find((part) => part.startsWith(`## ${heading}\n`)) ?? '';
}

// Every link, picture and reference definition a CommonMark renderer sees: text in code blocks and
// code spans is not a link. Targets stay as written (no percent-encoding) and appear once.
function markdownTargets(text: string): string[] {
  const parser = new MarkdownIt();
  parser.normalizeLink = (url) => url;
  parser.validateLink = () => true;
  const env: { references?: Record<string, { href: string; title: string }> } = {};
  // A link's target follows its content, so a badge's picture comes before its outer link.
  const open: string[] = [];
  const inline: string[] = [];
  for (const token of parser.parse(text, env).flatMap((block) => block.children ?? [])) {
    const target = token.attrGet('href') ?? token.attrGet('src');
    if (token.type === 'link_open') {
      open.push(String(target));
    } else if (token.type === 'link_close') {
      inline.push(open.pop() ?? '');
    } else if (token.type === 'image' && target !== null) {
      inline.push(String(target));
    }
  }
  const definitions = Object.entries(env.references ?? {})
    .filter(([label]) => !label.startsWith('^'))
    .map(([, reference]) => reference.href);
  return [...new Set([...inline, ...definitions])];
}

function badges(readme: string): string[] {
  return [...readme.matchAll(/\[!\[[^\]]*\]\(([^)\s]+)\)\]\([^)\s]+\)/g)].flatMap((match) =>
    match[1] === undefined ? [] : [match[1]],
  );
}

// Whether the command SECURITY.md gives to verify a release accepts a signature made under the
// identity.
function verifies(identity: string): boolean {
  const section = sectionOf(readFileSync(`${FOLDER}/SECURITY.md`, 'utf8'), 'Verifying a release');
  const pattern = /--certificate-identity-regexp '([^']+)'/.exec(section)?.[1];
  return pattern !== undefined && new RegExp(pattern).test(identity);
}

let layout: string | undefined;
let sandbox: string | undefined;

// A repository whose one release has these files, and the stand-ins for gh and cosign that work
// on it.
function publish(files: Record<string, string>, tag = TAG): string {
  const root = mkdtempSync(join(tmpdir(), 'sign-release-'));
  sandbox = root;
  mkdirSync(join(root, 'releases', tag), { recursive: true });
  for (const [name, text] of Object.entries(files)) {
    writeFileSync(join(root, 'releases', tag, name), text);
  }
  mkdirSync(join(root, 'bin'));
  for (const [name, text] of Object.entries({ gh: GH, cosign: COSIGN })) {
    writeFileSync(join(root, 'bin', name), text);
    chmodSync(join(root, 'bin', name), 0o755);
  }
  return root;
}

interface Signing {
  status: number | null;
  stderr: string;
  // Every call of gh and cosign, as it was typed.
  calls: string[];
  // The files of the repository's release after the run, each with its text.
  release: Record<string, string>;
}

// One run of the signing step's script, as the runner runs it: bash -e in an empty folder, with the
// step's env, the runner's own variables and nothing else the script could read. The workflow ran
// from the ref, for the tag; cosign's verify-blob answers the status.
function sign(
  root: string,
  ref: string,
  { tag = TAG, verifyStatus = 0 }: { tag?: string; verifyStatus?: number } = {},
): Signing {
  const step = readWorkflow(SIGN_RELEASE_PATH).jobs?.['sign']?.steps?.at(-1);
  const script = join(root, 'step.sh');
  const calls = join(root, 'calls');
  const releases = join(root, 'releases');
  const release = join(releases, readdirSync(releases)[0] ?? '');
  const expressions: Record<string, string> = {
    '${{ github.token }}': 'a-token',
    [TAG_EXPRESSION]: tag,
  };
  writeFileSync(script, step?.run ?? 'exit 64');
  writeFileSync(calls, '');
  const run = spawnSync('/bin/bash', ['-e', script], {
    cwd: mkdtempSync(join(root, 'run-')),
    encoding: 'utf8',
    env: {
      ...Object.fromEntries(
        Object.entries(step?.env ?? {}).map(([name, value]) => [name, expressions[value] ?? value]),
      ),
      PATH: `${join(root, 'bin')}:${process.env['PATH'] ?? ''}`,
      LC_ALL: 'C',
      GITHUB_REPOSITORY: REPOSITORY,
      GITHUB_REF: ref,
      GITHUB_WORKFLOW_REF: `${SIGNING_WORKFLOW}@${ref}`,
      CALLS: calls,
      RELEASES: releases,
      VERIFY_STATUS: String(verifyStatus),
    },
  });
  return {
    status: run.status,
    stderr: run.stderr,
    calls: readFileSync(calls, 'utf8').split('\n').filter(Boolean),
    release: Object.fromEntries(
      readdirSync(release).map((name) => [name, readFileSync(join(release, name), 'utf8')]),
    ),
  };
}

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
    ['[a](x.md (The title))', ['x.md']],
    ['[a](<a b.md>) ![p](<p q.gif> "t")', ['a b.md', 'p q.gif']],
    ['[a][id]\n\n[id]:\n  docs/x.md\n[b]: <c d.md>', ['docs/x.md', 'c d.md']],
    ['text[^1]\n\n[^1]: a note', []],
    ['[a](foo(bar).md) [b](x\\)y.md)', ['foo(bar).md', 'x)y.md']],
    ['```\n[a](in-fence.md)\n```\n\n~~~\n[b](tilde.md)\n~~~', []],
    ['`[a](in-span.md)` and [b](real.md)', ['real.md']],
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
  for (const folder of [layout, sandbox]) {
    if (folder !== undefined) {
      rmSync(folder, { recursive: true, force: true });
    }
  }
  layout = undefined;
  sandbox = undefined;
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
    '.github/workflows/codeql.yml',
    '.github/workflows/scorecard.yml',
    '.github/workflows/sign-release.yml',
    '.github/dependabot.yml',
    '.github/scripts/govulncheck-left.mjs',
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

  it('holds four workflows: CI, CodeQL, Scorecard and the one that signs a release', () => {
    expect(WORKFLOW_PATHS).toEqual([CI_PATH, CODEQL_PATH, SCORECARD_PATH, SIGN_RELEASE_PATH]);
  });

  it.each(WORKFLOW_PATHS)('parses %s as YAML with every action pinned by SHA', (path) => {
    const uses = usesOf(readWorkflow(path));

    expect(uses.length).toBeGreaterThan(0);
    expect(uses.filter((use) => !SHA_PINNED.test(use))).toEqual([]);
  });

  it.each(WORKFLOW_PATHS)('has a version comment after every pin in %s', (path) => {
    const lines = readFileSync(path, 'utf8')
      .split('\n')
      .filter((line) => line.includes('uses:'));

    expect(lines.filter((line) => !/@[0-9a-f]{40} # v\d/.test(line))).toEqual([]);
  });

  it.each(WORKFLOW_PATHS)(
    'has no expression inside a script of %s: what a script needs comes through env',
    (path) => {
      expect(scriptsOf(readWorkflow(path)).filter((script) => script.includes('${{'))).toEqual([]);
    },
  );

  it('asks for read-all at the top of every workflow, and writes only in the Scorecard, CodeQL and signing jobs', () => {
    const ci = readWorkflow(CI_PATH);
    const codeql = readWorkflow(CODEQL_PATH);
    const scorecard = readWorkflow(SCORECARD_PATH);
    const signRelease = readWorkflow(SIGN_RELEASE_PATH);

    expect(WORKFLOW_PATHS.map((path) => readWorkflow(path).permissions)).toEqual(
      WORKFLOW_PATHS.map(() => 'read-all'),
    );
    expect(Object.values(ci.jobs ?? {}).map((job) => job.permissions)).toEqual([
      undefined,
      undefined,
      undefined,
    ]);
    expect(Object.values(scorecard.jobs ?? {}).map((job) => job.permissions)).toEqual([
      { 'security-events': 'write', 'id-token': 'write' },
    ]);
    expect(Object.values(codeql.jobs ?? {}).map((job) => job.permissions)).toEqual([
      { 'security-events': 'write', actions: 'read', contents: 'read' },
    ]);
    expect(Object.values(signRelease.jobs ?? {}).map((job) => job.permissions)).toEqual([
      { contents: 'write', 'id-token': 'write' },
    ]);
    expect(
      WORKFLOW_PATHS.flatMap((path) =>
        writesOf(readWorkflow(path)).map((write) => `${basename(path)} ${write}`),
      ),
    ).toEqual([
      'codeql.yml analyze: security-events',
      'scorecard.yml scorecard: security-events',
      'scorecard.yml scorecard: id-token',
      'sign-release.yml sign: contents',
      'sign-release.yml sign: id-token',
    ]);
  });

  it('analyses Go, JavaScript and TypeScript, and the workflows with CodeQL, one language per job', () => {
    const job = readWorkflow(CODEQL_PATH).jobs?.['analyze'];
    const init = job?.steps?.find((step) => step.uses?.startsWith('github/codeql-action/init@'));

    expect(job?.strategy?.matrix).toEqual({
      include: [
        { language: 'go', 'build-mode': 'manual' },
        { language: 'javascript-typescript', 'build-mode': 'none' },
        { language: 'actions', 'build-mode': 'none' },
      ],
    });
    expect(init?.with).toEqual({
      languages: '${{ matrix.language }}',
      'build-mode': '${{ matrix.build-mode }}',
    });
  });

  it('builds the engine for CodeQL with the Go version of engine/go.mod, between init and analyze', () => {
    const steps = readWorkflow(CODEQL_PATH).jobs?.['analyze']?.steps ?? [];
    const names = steps.map((step) => step.uses?.replace(/@.*$/, '') ?? step.run);

    expect(names).toEqual([
      'actions/checkout',
      'actions/setup-go',
      'github/codeql-action/init',
      'go build ./...',
      'github/codeql-action/analyze',
    ]);
    expect(steps[1]).toMatchObject({
      if: "matrix.language == 'go'",
      with: { 'go-version-file': 'engine/go.mod' },
    });
    expect(steps[3]).toMatchObject({
      if: "matrix.build-mode == 'manual'",
      'working-directory': 'engine',
    });
  });

  it('pins checkout, setup-go and the two CodeQL actions at the commits of their releases', () => {
    expect(usesOf(readWorkflow(CODEQL_PATH))).toEqual([
      'actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803',
      'actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16',
      'github/codeql-action/init@9b3254085494b55a0f7c4c2b3d2d52cc712ce370',
      'github/codeql-action/analyze@9b3254085494b55a0f7c4c2b3d2d52cc712ce370',
    ]);
    expect(readFileSync(CODEQL_PATH, 'utf8').match(/# v[\d.]+$/gm)).toEqual([
      '# v6.1.0',
      '# v6.5.0',
      '# v4.39.1',
      '# v4.39.1',
    ]);
  });

  it('runs CodeQL on push to main, on pull requests and weekly off the hour, newer runs cancel older', () => {
    const codeql = readWorkflow(CODEQL_PATH);

    expect(Object.keys(codeql.on ?? {})).toEqual(['push', 'pull_request', 'schedule']);
    expect(codeql.on).toHaveProperty('push.branches', ['main']);
    expect(codeql.on?.['schedule']).toHaveLength(1);
    expect(codeql.on).toHaveProperty(
      'schedule.0.cron',
      expect.stringMatching(/^[1-5]?[1-9] \d\d? \* \* [0-6]$/),
    );
    expect(codeql.concurrency).toEqual(NEWER_RUNS_CANCEL_OLDER);
  });

  it('runs the Scorecard action weekly and on demand, not on push', () => {
    const scorecard = readWorkflow(SCORECARD_PATH);

    expect(scorecard.on).toHaveProperty('schedule');
    expect(scorecard.on).toHaveProperty('workflow_dispatch');
    expect(scorecard.on).not.toHaveProperty('push');
    expect(usesOf(scorecard).some((use) => use.startsWith('ossf/scorecard-action@'))).toBe(true);
  });

  it('signs when a release is published, and by hand for the tag it is given', () => {
    expect(readWorkflow(SIGN_RELEASE_PATH).on).toEqual({
      release: { types: ['published'] },
      workflow_dispatch: {
        inputs: {
          tag: {
            description: 'The tag of the release to sign, such as core-v0.27.0',
            required: true,
            type: 'string',
          },
        },
      },
    });
  });

  it('signs one release at a time per tag, and never cancels a run in progress', () => {
    expect(readWorkflow(SIGN_RELEASE_PATH).concurrency).toEqual({
      group: '${{ github.workflow }}-${{ github.event.release.tag_name || inputs.tag }}',
      'cancel-in-progress': false,
    });
  });

  it('says beside each permission the signing job writes with why it needs it', () => {
    const text = readFileSync(SIGN_RELEASE_PATH, 'utf8');

    expect(text).toMatch(/^ +contents: write # \S.*\brelease\b/m);
    expect(text).toMatch(/^ +id-token: write # \S.*\bidentity\b/m);
  });

  it('installs cosign at the commit of its release, checks nothing out, and gives the script the token and the tag through env', () => {
    const steps = readWorkflow(SIGN_RELEASE_PATH).jobs?.['sign']?.steps ?? [];

    expect(steps.map((step) => Object.keys(step))).toEqual([['uses'], ['env', 'run']]);
    expect(steps[0]?.uses).toBe(
      'sigstore/cosign-installer@756fe2d284c7292c4e9b62c8a28f1e5629c59532',
    );
    expect(readFileSync(SIGN_RELEASE_PATH, 'utf8')).toContain(
      'sigstore/cosign-installer@756fe2d284c7292c4e9b62c8a28f1e5629c59532 # v4.1.3',
    );
    expect(steps[1]?.env).toEqual({ GH_TOKEN: '${{ github.token }}', TAG: TAG_EXPRESSION });
    expect(steps[1]?.run?.split('\n')[0]).toBe('set -euo pipefail');
  });

  it.each([
    ['a release is published', TAG_REF],
    ['it is run by hand from main', MAIN_REF],
    ['it is run by hand from another tag', 'refs/tags/vscode-v0.36.0'],
  ])(
    'signs each file of the release when %s, checks each signature against the identity of its own run, then uploads the signatures',
    (_when, ref) => {
      const names = Object.keys(RELEASE_FILES);
      const signing = sign(publish(RELEASE_FILES), ref);

      expect(verifies(`${SIGNER}@${ref}`)).toBe(true);
      expect(signing.stderr).toBe('');
      expect(signing.status).toBe(0);
      expect(signing.calls).toEqual([
        `gh release download --repo ${REPOSITORY} --dir assets -- ${TAG}`,
        ...names.flatMap((name) => [
          `cosign sign-blob --yes --bundle assets/${name}.sigstore.json assets/${name}`,
          `cosign verify-blob --bundle assets/${name}.sigstore.json --certificate-identity ${SIGNER}@${ref} --certificate-oidc-issuer ${ISSUER} assets/${name}`,
        ]),
        `gh release upload --repo ${REPOSITORY} --clobber -- ${TAG} ${names.map((name) => `assets/${name}.sigstore.json`).join(' ')}`,
      ]);
      expect(signing.release).toEqual({
        ...RELEASE_FILES,
        'cel-lens-core_v0.27.0_linux_amd64.sigstore.json': `the engine, signed by ${SIGNER}@${ref}\n`,
        'checksums.txt.sigstore.json': `the sums, signed by ${SIGNER}@${ref}\n`,
      });
    },
  );

  it('signs no signature on a second run, and replaces the ones the first run made', () => {
    const release = publish(RELEASE_FILES);
    const first = sign(release, TAG_REF);
    const second = sign(release, MAIN_REF);

    expect([first.status, second.status]).toEqual([0, 0]);
    expect(second.calls.filter((call) => call.startsWith('cosign sign-blob'))).toEqual(
      Object.keys(RELEASE_FILES).map(
        (name) => `cosign sign-blob --yes --bundle assets/${name}.sigstore.json assets/${name}`,
      ),
    );
    expect(second.release).toEqual({
      ...RELEASE_FILES,
      'cel-lens-core_v0.27.0_linux_amd64.sigstore.json': `the engine, signed by ${SIGNER}@${MAIN_REF}\n`,
      'checksums.txt.sigstore.json': `the sums, signed by ${SIGNER}@${MAIN_REF}\n`,
    });
  });

  it.each([
    ['no file at all', {}, 'no assets to download'],
    [
      'only signatures',
      { 'checksums.txt.sigstore.json': 'a signature of a file that is gone' },
      `The release ${TAG} has no file to sign.`,
    ],
  ])('fails when the release has %s, and signs and uploads nothing', (_name, files, message) => {
    const signing = sign(publish(files), MAIN_REF);

    expect(signing.status).toBe(1);
    expect(signing.stderr).toContain(message);
    expect(signing.calls).toEqual([
      `gh release download --repo ${REPOSITORY} --dir assets -- ${TAG}`,
    ]);
    expect(signing.release).toEqual(files);
  });

  it.each([
    'refs/heads/sign-anything',
    `${MAIN_REF}-2`,
    `refs/heads/tags/${TAG}`,
    'refs/pull/7/merge',
    'refs/tags/',
  ])(
    'stops a run from %s, whose signatures the command in SECURITY.md refuses, before it asks gh for anything: the signatures of the release stay, and it says to run from main',
    (ref) => {
      const files = {
        ...RELEASE_FILES,
        'checksums.txt.sigstore.json': 'a signature made from main',
      };
      const signing = sign(publish(files), ref);

      expect(verifies(`${SIGNER}@${ref}`)).toBe(false);
      expect(signing.stderr).toBe(
        `Run this workflow from main, not from ${ref}: the command in SECURITY.md refuses a signature made from another branch.\n`,
      );
      expect(signing.status).toBe(1);
      expect(signing.calls).toEqual([]);
      expect(signing.release).toEqual(files);
    },
  );

  it('stops a run without a tag before it asks gh for anything, where gh would take the latest release, and says to give the tag', () => {
    const signing = sign(publish(RELEASE_FILES), MAIN_REF, { tag: '' });

    expect(signing.stderr).toBe(
      'No tag was given. Run this workflow again with the tag of the release to sign.\n',
    );
    expect(signing.status).toBe(1);
    expect(signing.calls).toEqual([]);
    expect(signing.release).toEqual(RELEASE_FILES);
  });

  it('asks gh for the release of a tag that looks like a flag, where gh would read a flag and take the latest release: none has that tag, and nothing is signed', () => {
    const signing = sign(publish(RELEASE_FILES), MAIN_REF, { tag: '--archive=zip' });

    expect(signing.stderr).toContain('release not found');
    expect(signing.status).toBe(1);
    expect(signing.calls.filter((call) => !call.startsWith('gh release download'))).toEqual([]);
    expect(signing.release).toEqual(RELEASE_FILES);
  });

  it('signs the release of a tag that looks like a flag: its files, and the signatures go to that release', () => {
    const tag = '--archive=zip';
    const signing = sign(publish(RELEASE_FILES, tag), MAIN_REF, { tag });

    expect(signing.stderr).toBe('');
    expect(signing.status).toBe(0);
    expect(signing.release).toEqual({
      ...RELEASE_FILES,
      'cel-lens-core_v0.27.0_linux_amd64.sigstore.json': `the engine, signed by ${SIGNER}@${MAIN_REF}\n`,
      'checksums.txt.sigstore.json': `the sums, signed by ${SIGNER}@${MAIN_REF}\n`,
    });
  });

  it('fails and uploads no signature when one it made does not verify', () => {
    const signing = sign(publish(RELEASE_FILES), MAIN_REF, { verifyStatus: 1 });

    expect(signing.status).toBe(1);
    expect(signing.calls.map((call) => call.split(' ').slice(0, 2).join(' '))).toEqual([
      'gh release',
      'cosign sign-blob',
      'cosign verify-blob',
    ]);
    expect(signing.release).toEqual(RELEASE_FILES);
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

  it('runs CI once per publish: push to main only, no tags, newer runs cancel older', () => {
    const ci = readWorkflow(CI_PATH);

    expect(ci.on).toHaveProperty('pull_request');
    expect(ci.on).toHaveProperty('push.branches', ['main']);
    expect(ci.on).not.toHaveProperty('push.tags');
    expect(ci.concurrency).toEqual(NEWER_RUNS_CANCEL_OLDER);
  });

  it('fails CI on a call into vulnerable code that engine/osv-scanner.toml does not leave aside: govulncheck over engine/ as JSON, on the Go version of engine/go.mod, then the script that reads it', () => {
    const ci = readWorkflow(CI_PATH);

    expect(Object.keys(ci.jobs ?? {})).toEqual(['engine', 'vscode', 'govulncheck']);
    // The action's go-version-input is 'stable' unless set, and setup-go then ignores the file.
    // With an output file the action writes it from the repository's root, and JSON never fails.
    expect(ci.jobs?.['govulncheck']?.steps).toEqual([
      {
        uses: 'golang/govulncheck-action@032d45514ae346b1db93c04b0c90b841c370344f',
        with: {
          'go-version-input': '',
          'go-version-file': 'engine/go.mod',
          'cache-dependency-path': 'engine/go.sum',
          'work-dir': 'engine',
          'go-package': './...',
          'output-format': 'json',
          'output-file': 'govulncheck.json',
        },
      },
      { run: 'node .github/scripts/govulncheck-left.mjs engine/osv-scanner.toml govulncheck.json' },
    ]);
    expect(readFileSync(CI_PATH, 'utf8')).toContain(
      'golang/govulncheck-action@032d45514ae346b1db93c04b0c90b841c370344f # v1.1.0',
    );
  });

  it('has Dependabot propose updates weekly for the engine, the extension and the workflows', () => {
    const config = parse(readFileSync(DEPENDABOT_PATH, 'utf8')) as {
      version: number;
      updates: {
        'package-ecosystem': string;
        directory: string;
        schedule: unknown;
        'open-pull-requests-limit': number;
        'commit-message': { prefix: string };
        groups: Record<string, unknown>;
      }[];
    };

    expect(config.version).toBe(2);
    expect(config.updates.map((update) => [update['package-ecosystem'], update.directory])).toEqual(
      [
        ['gomod', '/engine'],
        ['npm', '/vscode'],
        ['github-actions', '/'],
      ],
    );
    expect(config.updates.map((update) => update.schedule)).toEqual(
      config.updates.map(() => ({ interval: 'weekly' })),
    );
    expect(config.updates.map((update) => update['open-pull-requests-limit'])).toEqual([3, 3, 3]);
    expect(config.updates.map((update) => update['commit-message'].prefix)).toEqual([
      'build(engine)',
      'build(vscode)',
      'ci',
    ]);
  });

  it('groups the minor and patch updates of each ecosystem in one pull request, and leaves majors on their own', () => {
    const config = parse(readFileSync(DEPENDABOT_PATH, 'utf8')) as {
      updates: { groups: Record<string, unknown> }[];
    };

    expect(config.updates.map((update) => update.groups)).toEqual([
      { 'engine-minor-and-patch': { patterns: ['*'], 'update-types': ['minor', 'patch'] } },
      { 'vscode-minor-and-patch': { patterns: ['*'], 'update-types': ['minor', 'patch'] } },
      { 'actions-minor-and-patch': { patterns: ['*'], 'update-types': ['minor', 'patch'] } },
    ]);
  });

  it.each([
    'Dependabot opens a pull request here',
    'is not merged on GitHub',
    'where every change is reviewed',
    'A maintainer makes the same bump there',
    'the next published release carries it',
    'Dependabot then closes its pull request by itself',
    'A security update is released as soon as its checks pass',
  ])('says in CONTRIBUTING.md how a Dependabot pull request is applied: %s', (text) => {
    const contributing = readFileSync(`${FOLDER}/CONTRIBUTING.md`, 'utf8');

    expect(sectionOf(contributing, 'Dependency updates')).toContain(text);
  });

  it('says in SECURITY.md how the code and its dependencies are watched, in one sentence', () => {
    const sentence = readFileSync(`${FOLDER}/SECURITY.md`, 'utf8')
      .split('\n')
      .find((line) => line.includes('Dependabot'));

    expect(sentence).toBe(
      'The code and its dependencies are watched: Dependabot proposes dependency updates every week, CodeQL analyses every change, govulncheck checks the engine against the Go vulnerability database on every change, and the OpenSSF Scorecard runs every week.',
    );
  });

  it.each([
    '[sign-release.yml](.github/workflows/sign-release.yml)',
    '`.sigstore.json`',
    'cosign verify-blob',
    "--certificate-identity-regexp '^https://github\\.com/JairJosafath/cel-lens/\\.github/workflows/sign-release\\.yml@refs/(heads/main|tags/.+)$'",
    'a signature made from any other branch is refused',
    `--certificate-oidc-issuer ${ISSUER}`,
    "the file is the one this repository's workflow saw when it signed it",
    'It is not a statement about how the file was built',
    'Releases published before the workflow existed were signed afterwards',
  ])('says in SECURITY.md how a release is verified: %s', (text) => {
    const security = readFileSync(`${FOLDER}/SECURITY.md`, 'utf8');

    expect(sectionOf(security, 'Verifying a release')).toContain(text);
  });

  it.each([
    ['accepts a release signed when it was published', `${SIGNER}@${TAG_REF}`, true],
    ['accepts a release signed by hand from main', `${SIGNER}@${MAIN_REF}`, true],
    ['refuses the workflow at another branch', `${SIGNER}@refs/heads/sign-anything`, false],
    ['refuses a branch whose name starts with main', `${SIGNER}@${MAIN_REF}-2`, false],
    ['refuses a branch named as the tags are', `${SIGNER}@refs/heads/tags/${TAG}`, false],
    ['refuses a pull request', `${SIGNER}@refs/pull/7/merge`, false],
    ['refuses a tag without a name', `${SIGNER}@refs/tags/`, false],
    [
      'refuses another workflow of the repository',
      `${SIGNER.replace('sign-release', 'ci')}@${MAIN_REF}`,
      false,
    ],
    [
      'refuses the workflow of a fork',
      `${SIGNER.replace('JairJosafath', 'someone')}@${MAIN_REF}`,
      false,
    ],
    [
      'refuses a repository whose name starts the same',
      `${SIGNER.replace('lens/', 'lens-2/')}@${MAIN_REF}`,
      false,
    ],
    ['refuses a file whose name starts the same', `${SIGNER}.disabled@${MAIN_REF}`, false],
    [
      'refuses a host with another character for a dot',
      `${SIGNER.replace('github.com', 'githubxcom')}@${MAIN_REF}`,
      false,
    ],
    [
      'refuses the identity inside a longer address',
      `https://example.com/?${SIGNER}@${MAIN_REF}`,
      false,
    ],
  ])(
    'verifies in SECURITY.md against the identity of the signing workflow at main or at a tag: %s',
    (_name, identity, accepted) => {
      expect(verifies(identity)).toBe(accepted);
    },
  );

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
    "Release files are signed by this repository's own workflow; [Verifying a release](SECURITY.md#verifying-a-release) says how to check one.",
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
