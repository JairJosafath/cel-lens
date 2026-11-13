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
const RELEASE_PATH = `${WORKFLOWS}/release.yml`;
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
// The commit of the release each action is pinned at, with the release in its comment: one pin per
// action, whichever workflow uses it.
const PINS: Record<string, string> = {
  'actions/attest': '1e69f48acb82d1966a394da916b4c1698aa569d6 # v4.2.2',
  'actions/checkout': '3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1',
  'actions/download-artifact': '9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2',
  'actions/setup-go': 'b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0',
  'actions/setup-node': '949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0',
  'actions/upload-artifact': 'cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2',
  'github/codeql-action': '33d7b85da1a04171e211befe2c739303b9a4c066 # v4.39.3',
  'golang/govulncheck-action': '032d45514ae346b1db93c04b0c90b841c370344f # v1.1.0',
  'ossf/scorecard-action': 'fa7117db83c52d787ae5a89f350999701505d78e # v2.5.0',
  'sigstore/cosign-installer': '756fe2d284c7292c4e9b62c8a28f1e5629c59532 # v4.1.3',
};
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
// What a stand-in does first: it notes the call as it was typed, and fails before it does
// anything when the test asked that of a call that starts as $FAIL does.
const STAND_IN = [
  '#!/bin/bash',
  'set -eu',
  'shopt -s nullglob',
  'line="${0##*/} $*"',
  'echo "$line" >>"$CALLS"',
  'case "$line" in',
  '"${FAIL:-no call}"*)',
  '  echo "${0##*/}: the test has this call fail" >&2',
  '  exit 1',
  '  ;;',
  'esac',
];
// What a stand-in does last: it fails after it did everything when the test asked that of a call
// that starts as $FAIL_LATE does, as when the answer of a call that worked is lost.
const STAND_IN_END = [
  'case "$line" in',
  '"${FAIL_LATE:-no call}"*)',
  '  echo "${0##*/}: the test has this call fail after it did its work" >&2',
  '  exit 1',
  '  ;;',
  'esac',
  '',
];
// A stand-in for gh, where the release of a tag is the folder $RELEASES/<tag>. It reads its
// arguments as gh does: up to a --, one that starts with a dash is a flag, and the others are the
// tag, then the files. As gh does, it downloads from the latest release when it gets no tag, the
// source in place of the files with --archive, fails on a tag without a release and on a release
// without files, and uploads over a file of the release only with --clobber.
//
// For the release workflow, what GitHub keeps of a release beside its files is in $META/<tag>: its
// title, notes and target, and a file named draft while it is one; $META/latest names the release
// marked latest. A tag has one release here, so a draft left behind must be deleted before the
// next is made, where GitHub would keep both and publish either. Publishing creates the tag at the
// target when it is missing, as GitHub does. A workflow_dispatch runs the script $SCRIPTS/<file>
// of the workflow at once, from the branch and with the tag as TAG; run view answers 404 for the
// first $UNREADABLE reads of a run, views and watches counted together, as GitHub does for a run
// it has not yet made readable, and run watch answers how that run ended.
const GH = [
  ...STAND_IN,
  'command="$1 $2"',
  'shift 2',
  'dir=.',
  'clobber=false',
  'archive=false',
  'draft=',
  'latest=',
  'title=',
  'notes=',
  'target=',
  'exit_status=false',
  'fields=()',
  'names=()',
  'while [ $# -gt 0 ]; do',
  '  case "$1" in',
  '  --repo | --limit | --json | --jq | --method | --interval) shift ;;',
  '  --dir)',
  '    dir=$2',
  '    shift',
  '    ;;',
  '  --clobber) clobber=true ;;',
  '  --archive=*) archive=true ;;',
  '  --draft) draft=true ;;',
  '  --draft=false) draft=false ;;',
  '  --latest=*) latest=${1#*=} ;;',
  '  --target)',
  '    target=$2',
  '    shift',
  '    ;;',
  '  --title)',
  '    title=$2',
  '    shift',
  '    ;;',
  '  --notes-file)',
  '    notes=$2',
  '    shift',
  '    ;;',
  '  --yes) ;;',
  '  --exit-status) exit_status=true ;;',
  '  -f | -F)',
  '    fields+=("$2")',
  '    shift',
  '    ;;',
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
  'publish() {',
  '  rm -f "$META/$1/draft"',
  '  if [ ! -e "$TAGS/$1" ]; then',
  '    cp "$META/$1/target" "$TAGS/$1"',
  '  fi',
  '  if [ "$latest" = true ]; then',
  '    echo "$1" >"$META/latest"',
  '  fi',
  '}',
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
  "'release list')",
  '  for release in "$RELEASES"/*/; do',
  '    tag=$(basename "$release")',
  '    if [ -e "$META/$tag/draft" ]; then',
  '      echo "$tag true"',
  '    else',
  '      echo "$tag false"',
  '    fi',
  '  done',
  '  ;;',
  "'release create')",
  '  tag=${names[0]}',
  '  if [ -d "$RELEASES/$tag" ]; then',
  '    echo "a release with the same tag name already exists: $tag" >&2',
  '    exit 1',
  '  fi',
  '  mkdir -p "$RELEASES/$tag" "$META/$tag"',
  '  touch "$META/$tag/draft"',
  '  echo "$title" >"$META/$tag/title"',
  '  cp "$notes" "$META/$tag/notes"',
  '  echo "$target" >"$META/$tag/target"',
  '  for file in "${names[@]:1}"; do',
  '    cp "$file" "$RELEASES/$tag/"',
  '  done',
  '  if [ "$draft" != true ]; then',
  '    publish "$tag"',
  '  fi',
  '  ;;',
  "'release delete')",
  '  if [ ! -d "$RELEASES/${names[0]}" ]; then',
  "    echo 'release not found' >&2",
  '    exit 1',
  '  fi',
  '  rm -r "$RELEASES/${names[0]}" "$META/${names[0]}"',
  '  ;;',
  "'release edit')",
  '  if [ ! -d "$RELEASES/${names[0]}" ]; then',
  "    echo 'release not found' >&2",
  '    exit 1',
  '  fi',
  '  if [ "$draft" = false ]; then',
  '    publish "${names[0]}"',
  '  fi',
  '  ;;',
  '"api repos/$GITHUB_REPOSITORY/actions/workflows/"*/dispatches)',
  '  workflow=${command#*/workflows/}',
  '  workflow=${workflow%/dispatches}',
  '  ref=',
  '  tag=',
  '  details=false',
  '  for field in "${fields[@]}"; do',
  '    case "$field" in',
  '    ref=*) ref=${field#*=} ;;',
  "    'inputs[tag]='*) tag=${field#*=} ;;",
  '    return_run_details=true) details=true ;;',
  '    esac',
  '  done',
  '  if [ ! -f "$SCRIPTS/$workflow" ]; then',
  "    echo 'gh: Not Found (HTTP 404)' >&2",
  '    exit 1',
  '  fi',
  '  run=$(($(ls "$RUNS" | wc -l) + 1))',
  '  mkdir "$RUNS/$run"',
  '  status=0',
  '  (',
  '    cd "$RUNS/$run"',
  '    export TAG=$tag GITHUB_REF=refs/heads/$ref',
  '    export GITHUB_WORKFLOW_REF=$GITHUB_REPOSITORY/.github/workflows/$workflow@$GITHUB_REF',
  '    /bin/bash -e "$SCRIPTS/$workflow" >log 2>&1',
  '  ) || status=$?',
  '  echo "$status" >"$RUNS/$run/status"',
  '  if $details && [ "${RUN_DETAILS:-true}" = true ]; then',
  '    echo "$run"',
  '  fi',
  '  ;;',
  "'run view' | 'run watch')",
  '  reads=$RUNS/${names[0]}/reads',
  '  echo x >>"$reads"',
  '  if [ ! -d "$RUNS/${names[0]}" ] || [ "$(wc -l <"$reads")" -le "${UNREADABLE:-0}" ]; then',
  "    echo 'failed to get run: HTTP 404: Not Found' >&2",
  '    exit 1',
  '  fi',
  '  if [ "$command" = \'run watch\' ] && $exit_status; then',
  '    exit "$(cat "$RUNS/${names[0]}/status")"',
  '  fi',
  '  ;;',
  '*)',
  '  echo "gh: the stand-in does not know: $line" >&2',
  '  exit 2',
  '  ;;',
  'esac',
  ...STAND_IN_END,
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
// A stand-in for git, where the repository is two things: $COMMITS/<hash> holds the files of each
// commit, and $TAGS/<tag> holds the hash a tag points at, or, for an annotated tag, "<hash of the
// tag object> <hash of its commit>". As git does, it answers refs/tags/<tag>^{} only for an
// annotated tag, with the commit. It knows the three calls the release workflow makes. As git
// does, it pushes both tags or neither: a tag that is at another commit already refuses the whole
// push.
const GIT = [
  ...STAND_IN,
  'case "$1 $2" in',
  "'show '*)",
  '  if [ ! -f "$COMMITS/${2%%:*}/${2#*:}" ]; then',
  "    echo \"fatal: path '${2#*:}' does not exist in '${2%%:*}'\" >&2",
  '    exit 128',
  '  fi',
  '  cat "$COMMITS/${2%%:*}/${2#*:}"',
  '  ;;',
  "'ls-remote --tags')",
  '  if [ "$3" != origin ] || [ -z "${4:-}" ]; then',
  '    echo "git: the stand-in does not know: $line" >&2',
  '    exit 2',
  '  fi',
  '  for ref in "${@:4}"; do',
  '    name=${ref#refs/tags/}',
  '    if [ -f "$TAGS/${name%"^{}"}" ]; then',
  '      read -r object peeled <"$TAGS/${name%"^{}"}"',
  '      if [ "$name" = "${name%"^{}"}" ]; then',
  '        printf \'%s\\t%s\\n\' "$object" "$ref"',
  '      elif [ -n "$peeled" ]; then',
  '        printf \'%s\\t%s\\n\' "$peeled" "$ref"',
  '      fi',
  '    fi',
  '  done',
  '  ;;',
  "'push --atomic')",
  '  if [ "$3" != origin ] || [ -z "${4:-}" ]; then',
  '    echo "git: the stand-in does not know: $line" >&2',
  '    exit 2',
  '  fi',
  '  for spec in "${@:4}"; do',
  '    tag=$TAGS/${spec#*:refs/tags/}',
  '    if [ -f "$tag" ] && [ "$(cat "$tag")" != "${spec%%:*}" ]; then',
  '      echo " ! [rejected] ${spec%%:*} -> ${spec#*:refs/tags/} (already exists)" >&2',
  '      exit 1',
  '    fi',
  '  done',
  '  for spec in "${@:4}"; do',
  '    tag=$TAGS/${spec#*:refs/tags/}',
  '    mkdir -p "${tag%/*}"',
  '    echo "${spec%%:*}" >"$tag"',
  '  done',
  '  ;;',
  '*)',
  '  echo "git: the stand-in does not know: $line" >&2',
  '  exit 2',
  '  ;;',
  'esac',
  ...STAND_IN_END,
].join('\n');
// A stand-in for make, which knows one call: make -C <part> dist VERSION=v<version>. It writes
// into the part's dist/ the files the part's own make dist writes, each named as the real one and
// holding the line of source it was built from, and a dotfile, which is no release file.
const MAKE = [
  ...STAND_IN,
  'if [ "$1" != -C ] || [ "$3" != dist ] || [ "${4%%=*}" != VERSION ] || [ $# -ne 4 ]; then',
  '  echo "make: the stand-in does not know: $line" >&2',
  '  exit 2',
  'fi',
  'version=${4#VERSION=}',
  'mkdir -p "$2/dist"',
  'touch "$2/dist/.gitignore"',
  'case "$2" in',
  'engine)',
  '  for platform in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64.exe windows_arm64.exe; do',
  '    echo "$platform of $(cat engine/main.go)" >"engine/dist/cel-lens-core_${version}_$platform"',
  '  done',
  '  echo "the sums of $(cat engine/main.go)" >engine/dist/checksums.txt',
  '  ;;',
  'vscode)',
  '  for target in darwin-arm64 darwin-x64 linux-arm64 linux-x64 win32-arm64 win32-x64; do',
  '    echo "$target of $(cat vscode/src/extension.ts)" >"vscode/dist/cel-lens-vscode-$target-${version#v}.vsix"',
  '  done',
  '  ;;',
  'esac',
  ...STAND_IN_END,
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
  id?: string;
  if?: string;
  uses?: string;
  with?: Record<string, unknown>;
  env?: Record<string, string>;
  run?: string;
  'working-directory'?: string;
}
interface Job {
  needs?: string | string[];
  outputs?: Record<string, string>;
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

// An expression that takes what it gives from an input of the run, another job or the head of a
// pull request, which a checkout in a job that can write must never use.
const INPUT_DERIVED =
  /\$\{\{[^}]*\b(needs|inputs|github\.event\.inputs|github\.head_ref|github\.event\.pull_request\.head)\b/;

// Whether a job's permissions let it write: `write-all`, or a permission set to write.
function canWrite(permissions: unknown): boolean {
  return typeof permissions === 'string'
    ? permissions === 'write-all'
    : Object.values(permissions ?? {}).includes('write');
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

// Writes the files, each with its text, below the folder.
function writeFiles(folder: string, files: Record<string, string>): void {
  for (const [name, text] of Object.entries(files)) {
    mkdirSync(dirname(join(folder, name)), { recursive: true });
    writeFileSync(join(folder, name), text);
  }
}

// The files below the folder, each with its text, named from the folder.
function readFiles(folder: string): Record<string, string> {
  return Object.fromEntries(
    filesUnder(folder)
      .sort()
      .map((file) => [file.slice(folder.length + 1), readFileSync(file, 'utf8')]),
  );
}

// A repository whose one release has these files, and the stand-ins for gh, cosign, git and make
// that work on it.
function publish(files: Record<string, string>, tag = TAG): string {
  const root = mkdtempSync(join(tmpdir(), 'sign-release-'));
  sandbox = root;
  mkdirSync(join(root, 'releases', tag), { recursive: true });
  writeFiles(join(root, 'releases', tag), files);
  writeFiles(join(root, 'bin'), {
    gh: GH,
    cosign: COSIGN,
    git: GIT,
    make: MAKE,
    sleep: [...STAND_IN, ...STAND_IN_END].join('\n'),
  });
  for (const name of readdirSync(join(root, 'bin'))) {
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

// The commits of the stand-in repository, the three of main, oldest first.
const BEFORE = 'b0'.repeat(20);
const PREPARED = 'c1'.repeat(20);
const LATER = 'd2'.repeat(20);
// The hash of the object of an annotated tag; its commit is another hash.
const TAG_OBJECT = 'f4'.repeat(20);
// The versions PREPARED prepares, and their notes.
const ENGINE_VERSION = '0.28.0';
const VSCODE_VERSION = '0.36.0';
const ENGINE_NOTES = '### Added\n\n- A line above every CEL rule.';
const VSCODE_NOTES = '### Added\n\n- **Give it a type** on the line above a rule.';

function changelogOf(...sections: string[]): string {
  return `${['# Changelog', ...sections].join('\n\n')}\n`;
}

// The files of a commit that the release workflow reads or builds from. The engine's changelog
// and the extension's have these sections, and package.json names the version.
function sourceOf(
  label: string,
  engine: string[],
  vscode: string[],
  version: string,
): Record<string, string> {
  return {
    'engine/main.go': `the engine ${label}`,
    'engine/CHANGELOG.md': changelogOf(...engine, '## [0.27.0] - 2026-11-04\n\n- An older fix.'),
    'vscode/src/extension.ts': `the extension ${label}`,
    'vscode/package.json': `${JSON.stringify({ name: 'cel-lens-vscode', version }, null, 2)}\n`,
    'vscode/CHANGELOG.md': changelogOf(...vscode, '## [0.35.0] - 2026-11-04\n\n- An older change.'),
  };
}

const ENGINE_SECTION = `## [${ENGINE_VERSION}] - 2026-11-13\n\n${ENGINE_NOTES}`;
const VSCODE_SECTION = `## [${VSCODE_VERSION}] - 2026-11-13\n\n${VSCODE_NOTES}`;
// BEFORE has the notes under Unreleased, and package.json names the version before the changelog
// does. PREPARED names both versions. LATER, the head of main, has later work on top.
const SOURCES: Record<string, Record<string, string>> = {
  [BEFORE]: sourceOf(
    'before its version was prepared',
    [`## [Unreleased]\n\n${ENGINE_NOTES}`],
    [`## [Unreleased]\n\n${VSCODE_NOTES}`],
    VSCODE_VERSION,
  ),
  [PREPARED]: sourceOf(
    'as its version was prepared',
    ['## [Unreleased]', ENGINE_SECTION],
    ['## [Unreleased]', VSCODE_SECTION],
    VSCODE_VERSION,
  ),
  [LATER]: sourceOf(
    'with later work',
    ['## [Unreleased]\n\n### Changed\n\n- Later work.', ENGINE_SECTION],
    ['## [Unreleased]\n\n### Changed\n\n- Later work.', VSCODE_SECTION],
    VSCODE_VERSION,
  ),
};

type Part = 'engine' | 'vscode';

// What the stand-in for make writes for the part from the source of the commit.
function built(part: Part, commit: string): Record<string, string> {
  const source =
    SOURCES[commit]?.[part === 'engine' ? 'engine/main.go' : 'vscode/src/extension.ts'];
  if (part === 'engine') {
    return {
      ...Object.fromEntries(
        [
          'darwin_amd64',
          'darwin_arm64',
          'linux_amd64',
          'linux_arm64',
          'windows_amd64.exe',
          'windows_arm64.exe',
        ].map((platform) => [
          `cel-lens-core_v${ENGINE_VERSION}_${platform}`,
          `${platform} of ${source ?? ''}\n`,
        ]),
      ),
      'checksums.txt': `the sums of ${source ?? ''}\n`,
    };
  }
  return Object.fromEntries(
    ['darwin-arm64', 'darwin-x64', 'linux-arm64', 'linux-x64', 'win32-arm64', 'win32-x64'].map(
      (target) => [
        `cel-lens-vscode-${target}-${VSCODE_VERSION}.vsix`,
        `${target} of ${source ?? ''}\n`,
      ],
    ),
  );
}

// The identity in the certificate of what a run of the release workflow attests, up to the ref.
const BUILDER = `https://github.com/${REPOSITORY}/.github/workflows/release.yml`;
// The file of a release that holds the build provenance of the others.
const PROVENANCE = 'provenance.intoto.jsonl';

// What the stand-in for actions/attest writes: one line that names every file it was given and
// the identity of the run that attests them.
function provenanceOf(subjects: string[], ref = MAIN_REF): string {
  return `${JSON.stringify({ subjects, signer: `${BUILDER}@${ref}` })}\n`;
}

interface Release {
  draft: boolean;
  title: string;
  notes: string;
  files: Record<string, string>;
}
// What the stand-in repository holds that a release changes.
interface Published {
  // The commit each tag points at.
  tags: Record<string, string>;
  releases: Record<string, Release>;
  // The tag of the release marked latest.
  latest: string | undefined;
}

function publishedIn(root: string): Published {
  const meta = join(root, 'meta');
  return {
    tags: Object.fromEntries(
      Object.entries(readFiles(join(root, 'tags'))).map(([tag, commit]) => [tag, commit.trim()]),
    ),
    releases: Object.fromEntries(
      readdirSync(join(root, 'releases')).map((tag) => [
        tag,
        {
          draft: existsSync(join(meta, tag, 'draft')),
          title: readFileSync(join(meta, tag, 'title'), 'utf8').trim(),
          notes: readFileSync(join(meta, tag, 'notes'), 'utf8'),
          files: readFiles(join(root, 'releases', tag)),
        },
      ]),
    ),
    latest: existsSync(join(meta, 'latest'))
      ? readFileSync(join(meta, 'latest'), 'utf8').trim()
      : undefined,
  };
}

// The two tags of a version of the part: the one in the form of its folder, which go install
// reads for the engine, and the one its release has.
function tagsOf(part: Part): [string, string] {
  return part === 'engine'
    ? [`engine/v${ENGINE_VERSION}`, `core-v${ENGINE_VERSION}`]
    : [`vscode/v${VSCODE_VERSION}`, `vscode-v${VSCODE_VERSION}`];
}

// The release of the part as the workflow leaves it before sign-release.yml ran: the files built
// from the commit, and their provenance.
function releaseOf(part: Part, commit: string, draft = false): Release {
  const files = built(part, commit);
  return {
    draft,
    title:
      part === 'engine'
        ? `cel-lens core v${ENGINE_VERSION}`
        : `cel-lens for VS Code v${VSCODE_VERSION}`,
    notes: `${part === 'engine' ? ENGINE_NOTES : VSCODE_NOTES}\n\n`,
    files: { ...files, [PROVENANCE]: provenanceOf(Object.keys(files)) },
  };
}

// The same release once sign-release.yml ran from main: a signature beside every file but the
// provenance.
function signedFrom(release: Release): Release {
  const signatures = Object.entries(release.files)
    .filter(([name]) => name !== PROVENANCE)
    .map(([name, text]): [string, string] => [
      `${name}.sigstore.json`,
      `${text.trimEnd()}, signed by ${SIGNER}@${MAIN_REF}\n`,
    ]);
  return { ...release, files: { ...release.files, ...Object.fromEntries(signatures) } };
}

// A repository as the release workflow finds it: main with its three commits, the tags and
// releases given, and the stand-ins. A workflow_dispatch of sign-release.yml runs the script of
// its signing step.
function repository(
  { tags = {}, releases = {}, latest }: Partial<Published> = {},
  commits = SOURCES,
): string {
  const root = publish({}, 'none');
  rmSync(join(root, 'releases', 'none'), { recursive: true });
  for (const folder of ['tags', 'meta', 'runs', 'scripts']) {
    mkdirSync(join(root, folder));
  }
  for (const [commit, files] of Object.entries(commits)) {
    writeFiles(join(root, 'commits', commit), files);
  }
  writeFiles(root, {
    'scripts/sign-release.yml':
      readWorkflow(SIGN_RELEASE_PATH).jobs?.['sign']?.steps?.at(-1)?.run ?? 'exit 64',
    ...(latest === undefined ? {} : { 'meta/latest': `${latest}\n` }),
  });
  writeFiles(
    join(root, 'tags'),
    Object.fromEntries(Object.entries(tags).map(([tag, commit]) => [tag, `${commit}\n`])),
  );
  for (const [tag, release] of Object.entries(releases)) {
    mkdirSync(join(root, 'releases', tag));
    writeFiles(join(root, 'releases', tag), release.files);
    writeFiles(join(root, 'meta', tag), {
      title: `${release.title}\n`,
      notes: release.notes,
      target: `${tags[tag] ?? ''}\n`,
      ...(release.draft ? { draft: '' } : {}),
    });
  }
  return root;
}

// What goes wrong in one attempt of a run: a call that starts as `fail` does fails before it does
// anything, one that starts as `failLate` does fails after it did its work, the attest action
// fails, cosign's verify-blob answers the status in the run of sign-release.yml, or GitHub does
// not say which run it started.
interface Mishap {
  fail?: string;
  failLate?: string;
  attestFails?: boolean;
  verifyStatus?: number;
  runDetails?: boolean;
  // How many reads of the run of sign-release.yml answer 404 before it can be read.
  unreadable?: number;
}

// A run of the release workflow, after an attempt.
interface ReleaseRun {
  root: string;
  ref: string;
  // The head of main when the run started: the commit it releases.
  sha: string;
  // The folder of the artifacts its jobs uploaded: a run sees no artifact of another run.
  artifacts: string;
  // What the expressions of the workflow give: the inputs, the token, and what each step and job
  // that passed gave the ones after it.
  context: Record<string, string>;
  // The jobs that passed, in this attempt or an earlier one, and the one that failed in this one.
  passed: string[];
  failed: string | undefined;
  stdout: string;
  stderr: string;
  // Every call of a stand-in in this attempt, as it was typed, and every action it used.
  calls: string[];
  actions: string[];
}

// The text with the value of each ${{ expression }} in it.
function evaluate(text: string, context: Record<string, string>): string {
  return text.replaceAll(/\$\{\{ (.+?) \}\}/g, (_all, expression: string) => {
    const value = context[expression];
    if (value === undefined) {
      throw new Error(`The stand-in for the runner has no value for ${expression}`);
    }
    return value;
  });
}

// Whether the condition of a step holds: the one form the release workflow uses.
function holds(condition: string, context: Record<string, string>): boolean {
  const [, name, value] = /^(\S+) == '([^']*)'$/.exec(condition) ?? [];
  if (name === undefined) {
    throw new Error(`The stand-in for the runner cannot read the condition ${condition}`);
  }
  return context[name] === value;
}

// What the action of a step does in the job's folder, as far as the scripts after it can tell:
// checkout puts the files of the commit there, upload-artifact and download-artifact carry a
// folder from one job to another, without its dotfiles, and attest writes the provenance of the
// files its pattern names. It answers the outputs of the step, or nothing when the action fails.
function use(
  run: ReleaseRun,
  action: string,
  settings: Record<string, unknown>,
  folder: string,
  mishap: Mishap,
): Record<string, string> | undefined {
  const setting = (name: string): string => {
    const value = settings[name];
    return typeof value === 'string' ? value : '';
  };
  const artifact = join(run.artifacts, setting('name'));
  run.actions.push(action);
  switch (action) {
    case 'actions/checkout':
      cpSync(join(run.root, 'commits', run.sha), folder, { recursive: true });
      return {};
    case 'actions/setup-go':
    case 'actions/setup-node':
      return {};
    case 'actions/upload-artifact':
      if (existsSync(artifact) && settings['overwrite'] !== true) {
        run.stderr += `an artifact named ${basename(artifact)} exists already\n`;
        return undefined;
      }
      rmSync(artifact, { recursive: true, force: true });
      cpSync(join(folder, setting('path')), artifact, {
        recursive: true,
        filter: (source) => !basename(source).startsWith('.'),
      });
      return {};
    case 'actions/download-artifact':
      cpSync(artifact, join(folder, setting('path')), { recursive: true });
      return {};
    case 'actions/attest': {
      if (mishap.attestFails === true) {
        run.stderr += 'the test has the attestation fail\n';
        return undefined;
      }
      const subjects = readdirSync(join(folder, dirname(setting('subject-path'))));
      const bundle = join(mkdtempSync(join(run.root, 'attest-')), 'attestation.json');
      writeFileSync(bundle, provenanceOf(subjects.sort(), run.ref));
      return { 'bundle-path': bundle };
    }
    default:
      throw new Error(`The stand-in for the runner knows no action ${action}`);
  }
}

// One run of a step's script, as the runner runs it: bash -e in the job's folder, with the step's
// env, the runner's own variables and what the stand-ins need. It answers the outputs the script
// wrote, or nothing when it failed.
function execute(
  run: ReleaseRun,
  step: Step,
  folder: string,
  mishap: Mishap,
): Record<string, string> | undefined {
  const script = join(run.root, 'step.sh');
  const output = join(run.root, 'output');
  writeFileSync(script, step.run ?? 'exit 64');
  writeFileSync(output, '');
  const done = spawnSync('/bin/bash', ['-e', script], {
    cwd: folder,
    encoding: 'utf8',
    env: {
      ...Object.fromEntries(
        Object.entries(step.env ?? {}).map(([name, value]) => [name, evaluate(value, run.context)]),
      ),
      PATH: `${join(run.root, 'bin')}:${process.env['PATH'] ?? ''}`,
      LC_ALL: 'C',
      GITHUB_REPOSITORY: REPOSITORY,
      GITHUB_REF: run.ref,
      GITHUB_SHA: run.sha,
      GITHUB_OUTPUT: output,
      CALLS: join(run.root, 'calls'),
      COMMITS: join(run.root, 'commits'),
      TAGS: join(run.root, 'tags'),
      RELEASES: join(run.root, 'releases'),
      META: join(run.root, 'meta'),
      RUNS: join(run.root, 'runs'),
      SCRIPTS: join(run.root, 'scripts'),
      VERIFY_STATUS: String(mishap.verifyStatus ?? 0),
      RUN_DETAILS: String(mishap.runDetails ?? true),
      UNREADABLE: String(mishap.unreadable ?? 0),
      ...(mishap.fail === undefined ? {} : { FAIL: mishap.fail }),
      ...(mishap.failLate === undefined ? {} : { FAIL_LATE: mishap.failLate }),
    },
  });
  run.stdout += done.stdout;
  run.stderr += done.stderr;
  if (done.status !== 0) {
    return undefined;
  }
  return Object.fromEntries(
    readFileSync(output, 'utf8')
      .split('\n')
      .filter(Boolean)
      .map((line) => [line.slice(0, line.indexOf('=')), line.slice(line.indexOf('=') + 1)]),
  );
}

// One job of the run, in a folder of its own: its steps in order, up to one that fails. What the
// steps and the job give goes into the run's context. It answers whether the job passed.
function runJob(run: ReleaseRun, name: string, job: Job, mishap: Mishap): boolean {
  const folder = mkdtempSync(join(run.root, `${name}-`));
  for (const step of job.steps ?? []) {
    if (step.if !== undefined && !holds(step.if, run.context)) {
      continue;
    }
    const settings = Object.fromEntries(
      Object.entries(step.with ?? {}).map(([key, value]) => [
        key,
        typeof value === 'string' ? evaluate(value, run.context) : value,
      ]),
    );
    const outputs =
      step.uses === undefined
        ? execute(run, step, folder, mishap)
        : use(run, step.uses.replace(/@.*$/, ''), settings, folder, mishap);
    if (outputs === undefined) {
      return false;
    }
    for (const [key, value] of Object.entries(outputs)) {
      run.context[`steps.${step.id ?? ''}.outputs.${key}`] = value;
    }
  }
  for (const [key, value] of Object.entries(job.outputs ?? {})) {
    run.context[`needs.${name}.outputs.${key}`] = evaluate(value, run.context);
  }
  return true;
}

// An attempt of the run: every job that has not passed yet, in the workflow's order, up to one
// that fails. With a run that failed, it is what "Re-run failed jobs" does; a new run is another
// call of release() on the same repository.
function attempt(run: ReleaseRun, mishap: Mishap = {}): ReleaseRun {
  const next: ReleaseRun = {
    ...run,
    context: { ...run.context },
    passed: [...run.passed],
    failed: undefined,
    stdout: '',
    stderr: '',
    calls: [],
    actions: [],
  };
  writeFileSync(join(run.root, 'calls'), '');
  for (const [name, job] of Object.entries(readWorkflow(RELEASE_PATH).jobs ?? {})) {
    if (next.passed.includes(name)) {
      continue;
    }
    if (![job.needs ?? []].flat().every((needed) => next.passed.includes(needed))) {
      throw new Error(`The job ${name} comes before a job it needs`);
    }
    if (!runJob(next, name, job, mishap)) {
      next.failed = name;
      break;
    }
    next.passed.push(name);
  }
  next.calls = readFileSync(join(run.root, 'calls'), 'utf8').split('\n').filter(Boolean);
  return next;
}

// A new run of the release workflow in the repository, started by hand from the ref, when main's
// head is sha, with the inputs.
function release(
  root: string,
  inputs: { part: string; version: string },
  { ref = MAIN_REF, sha = LATER, ...mishap }: Mishap & { ref?: string; sha?: string } = {},
): ReleaseRun {
  return attempt(
    {
      root,
      ref,
      sha,
      artifacts: mkdtempSync(join(root, 'artifacts-')),
      context: {
        'github.token': 'a-token',
        'inputs.part': inputs.part,
        'inputs.version': inputs.version,
      },
      passed: [],
      failed: undefined,
      stdout: '',
      stderr: '',
      calls: [],
      actions: [],
    },
    mishap,
  );
}

// The inputs of a run of the release workflow for the version PREPARED prepares.
const ENGINE = { part: 'engine', version: ENGINE_VERSION };
const VSCODE = { part: 'vscode', version: VSCODE_VERSION };

// What the check of the release workflow says when it stops.
function notVersion(version: string): string {
  return `The version "${version}" is not X.Y.Z. Run this workflow again with the version as three numbers, such as 1.2.3, without a v in front.`;
}

function tagAlone(tag: string, version: string): string {
  return `Only the tag ${tag} exists, not the other tag of version ${version}, so this run cannot tell what it was for. Delete the tag if the release was never made, then run this workflow again; or prepare and release the next version.`;
}

function tagsElsewhere(
  sourceTag: string,
  releaseTag: string,
  at: string,
  commit: string,
  version: string,
): string {
  return `The tags ${sourceTag} and ${releaseTag} both point at ${at}, not at ${commit}, and the release ${releaseTag} is not published: a run that failed was releasing version ${version} from ${at}. Finish that release by re-running the failed jobs of the run that created the tags, which keeps its commit ${at}; a new run releases only the head of main, so it cannot finish it.`;
}

function tagElsewhere(tag: string, at: string, commit: string, version: string): string {
  return `The tag ${tag} points at ${at}, not at ${commit}: version ${version} was released from that commit, or a run that failed was releasing it from there, and a run releases only the head of main. Prepare the next version and release that.`;
}

function tagAnnotated(tag: string): string {
  return `The tag ${tag} is an annotated tag, which a run cannot reuse: it pushes the commit itself as the tag, and git refuses that over an annotated tag. Delete the tag if nothing uses it, then run this workflow again; or prepare and release the next version.`;
}

function notInChangelog(part: Part, commit: string, version: string): string {
  return `${part}/CHANGELOG.md at ${commit} has no "## [${version}]" heading. Prepare the version first: move its notes under that heading in a change of its own, then run this workflow again once it is merged to main.`;
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
    '.github/workflows/release.yml',
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

  it('holds five workflows: CI, CodeQL, the one that makes a release, Scorecard and the one that signs a release', () => {
    expect(WORKFLOW_PATHS).toEqual([
      CI_PATH,
      CODEQL_PATH,
      RELEASE_PATH,
      SCORECARD_PATH,
      SIGN_RELEASE_PATH,
    ]);
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
    'pins each action of %s at the commit and release every workflow pins it at',
    (path) => {
      const pins = readFileSync(path, 'utf8')
        .split('\n')
        .filter((line) => line.includes('uses:'))
        .map((line) => line.replace(/^.*uses: /, ''));

      // An action is the repository, also when the step names a folder of it.
      expect(pins).toEqual(
        pins.map((pin) => {
          const path = pin.split('@')[0] ?? '';
          return `${path}@${PINS[path.split('/').slice(0, 2).join('/')] ?? 'an action the table lacks'}`;
        }),
      );
    },
  );

  it.each(Object.keys(PINS))('uses %s in a workflow, so its pin is one that is read', (action) => {
    const used = WORKFLOW_PATHS.flatMap((path) => usesOf(readWorkflow(path)));

    expect(used.some((use) => use.startsWith(`${action}@`) || use.startsWith(`${action}/`))).toBe(
      true,
    );
  });

  it.each(WORKFLOW_PATHS)(
    'has no expression inside a script of %s: what a script needs comes through env',
    (path) => {
      expect(scriptsOf(readWorkflow(path)).filter((script) => script.includes('${{'))).toEqual([]);
    },
  );

  it('asks for read-all at the top of every workflow, and writes only in the Scorecard, CodeQL and signing jobs and in the two jobs of the release workflow that publish and have the release signed', () => {
    const ci = readWorkflow(CI_PATH);
    const codeql = readWorkflow(CODEQL_PATH);
    const release = readWorkflow(RELEASE_PATH);
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
      Object.entries(release.jobs ?? {}).map(([name, job]) => [name, job.permissions]),
    ).toEqual([
      ['check', undefined],
      ['build', undefined],
      ['publish', { contents: 'write', 'id-token': 'write', attestations: 'write' }],
      ['sign', { actions: 'write' }],
    ]);
    expect(
      WORKFLOW_PATHS.flatMap((path) =>
        writesOf(readWorkflow(path)).map((write) => `${basename(path)} ${write}`),
      ),
    ).toEqual([
      'codeql.yml analyze: security-events',
      'release.yml publish: contents',
      'release.yml publish: id-token',
      'release.yml publish: attestations',
      'release.yml sign: actions',
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
      'actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1',
      'actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e',
      'github/codeql-action/init@33d7b85da1a04171e211befe2c739303b9a4c066',
      'github/codeql-action/analyze@33d7b85da1a04171e211befe2c739303b9a4c066',
    ]);
    expect(readFileSync(CODEQL_PATH, 'utf8').match(/# v[\d.]+$/gm)).toEqual([
      '# v7.0.1',
      '# v7.0.0',
      '# v4.39.3',
      '# v4.39.3',
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

  it('uploads the Scorecard results with the CodeQL action of the CodeQL workflow, after a checkout that keeps no credentials', () => {
    const steps = readWorkflow(SCORECARD_PATH).jobs?.['scorecard']?.steps ?? [];

    expect(steps.map((step) => step.uses)).toEqual([
      'actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1',
      'ossf/scorecard-action@fa7117db83c52d787ae5a89f350999701505d78e',
      'github/codeql-action/upload-sarif@33d7b85da1a04171e211befe2c739303b9a4c066',
    ]);
    expect(steps[0]?.with).toEqual({ 'persist-credentials': false });
    expect(readFileSync(SCORECARD_PATH, 'utf8')).toContain(
      'github/codeql-action/upload-sarif@33d7b85da1a04171e211befe2c739303b9a4c066 # v4.39.3',
    );
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
    [
      'only build provenance',
      { [PROVENANCE]: 'the provenance of files that are gone' },
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

  it('signs no build provenance: the .intoto.jsonl file of a release gets no signature', () => {
    const files = { ...RELEASE_FILES, [PROVENANCE]: 'the provenance' };
    const signing = sign(publish(files), MAIN_REF);

    expect(signing.stderr).toBe('');
    expect(signing.status).toBe(0);
    expect(signing.calls.filter((call) => call.includes(PROVENANCE))).toEqual([]);
    expect(signing.release).toEqual({
      ...files,
      'cel-lens-core_v0.27.0_linux_amd64.sigstore.json': `the engine, signed by ${SIGNER}@${MAIN_REF}\n`,
      'checksums.txt.sigstore.json': `the sums, signed by ${SIGNER}@${MAIN_REF}\n`,
    });
  });

  it('makes a release only by hand, with the part as a choice of engine or vscode and the version, and no other input: it releases the head of main', () => {
    expect(readWorkflow(RELEASE_PATH).on).toEqual({
      workflow_dispatch: {
        inputs: {
          part: {
            description: 'The part to release',
            required: true,
            type: 'choice',
            options: ['engine', 'vscode'],
          },
          version: {
            description: 'The version to release, as X.Y.Z; the source names it already',
            required: true,
            type: 'string',
          },
        },
      },
    });
  });

  it('has no input other than the part and the version: a release is always of the commit the run started at, the head of main', () => {
    const dispatch = readWorkflow(RELEASE_PATH).on?.['workflow_dispatch'] as {
      inputs?: Record<string, unknown>;
    };

    expect(Object.keys(dispatch.inputs ?? {}).sort()).toEqual(['part', 'version']);
  });

  it('makes one release at a time per part and version, and never cancels a run in progress', () => {
    expect(readWorkflow(RELEASE_PATH).concurrency).toEqual({
      group: '${{ github.workflow }}-${{ inputs.part }}-${{ inputs.version }}',
      'cancel-in-progress': false,
    });
  });

  it('gives each script of the release workflow its inputs through env, and has each stop at the first command that fails', () => {
    const jobs = readWorkflow(RELEASE_PATH).jobs ?? {};
    const scripts = Object.entries(jobs).flatMap(([name, job]) =>
      (job.steps ?? []).filter((step) => step.run !== undefined).map((step) => ({ name, step })),
    );

    expect(scripts.map(({ name, step }) => [name, step.env])).toEqual([
      [
        'check',
        {
          GH_TOKEN: '${{ github.token }}',
          PART: '${{ inputs.part }}',
          RELEASE_VERSION: '${{ inputs.version }}',
        },
      ],
      ['build', { PART: '${{ inputs.part }}', RELEASE_VERSION: '${{ inputs.version }}' }],
      [
        'publish',
        {
          GH_TOKEN: '${{ github.token }}',
          COMMIT: '${{ needs.check.outputs.commit }}',
          SOURCE_TAG: '${{ needs.check.outputs.source-tag }}',
          RELEASE_TAG: '${{ needs.check.outputs.release-tag }}',
          TITLE: '${{ needs.check.outputs.title }}',
          LATEST: '${{ needs.check.outputs.latest }}',
          PROVENANCE: '${{ steps.attest.outputs.bundle-path }}',
        },
      ],
      [
        'sign',
        { GH_TOKEN: '${{ github.token }}', RELEASE_TAG: '${{ needs.check.outputs.release-tag }}' },
      ],
    ]);
    expect(scripts.map(({ step }) => step.run?.split('\n')[0])).toEqual(
      scripts.map(() => 'set -euo pipefail'),
    );
  });

  it.each<
    [
      string,
      { part: string; version: string },
      { ref?: string; sha?: string; published?: Partial<Published> },
      string,
    ]
  >([
    [
      'the run started from another branch',
      ENGINE,
      { ref: 'refs/heads/release-anything' },
      'Run this workflow from main, not from refs/heads/release-anything: the commands in SECURITY.md accept only a release that the workflow made as main has it.',
    ],
    [
      'the run started from a tag',
      ENGINE,
      { ref: TAG_REF },
      `Run this workflow from main, not from ${TAG_REF}: the commands in SECURITY.md accept only a release that the workflow made as main has it.`,
    ],
    ['the version has a v in front', { ...ENGINE, version: 'v0.28.0' }, {}, notVersion('v0.28.0')],
    ['the version has two numbers', { ...ENGINE, version: '0.28' }, {}, notVersion('0.28')],
    [
      'the version has four numbers',
      { ...ENGINE, version: '0.28.0.1' },
      {},
      notVersion('0.28.0.1'),
    ],
    [
      'the version is a pre-release',
      { ...ENGINE, version: '0.28.0-rc.1' },
      {},
      notVersion('0.28.0-rc.1'),
    ],
    [
      'a number of the version starts with 0',
      { ...VSCODE, version: '0.036.0' },
      {},
      notVersion('0.036.0'),
    ],
    ['the version is empty', { ...VSCODE, version: '' }, {}, notVersion('')],
    [
      'the version has a space after it',
      { ...ENGINE, version: '0.28.0 ' },
      {},
      notVersion('0.28.0 '),
    ],
    [
      'the part is neither of the two',
      { ...ENGINE, part: 'docs' },
      {},
      'The part "docs" is not engine or vscode. Run this workflow again with one of the two.',
    ],
    [
      'the engine tag in the form of its folder is at another commit, and the other tag is missing',
      ENGINE,
      { published: { tags: { 'engine/v0.28.0': BEFORE } } },
      tagElsewhere('engine/v0.28.0', BEFORE, LATER, ENGINE_VERSION),
    ],
    [
      'both tags of the engine are at another commit and the release is missing',
      ENGINE,
      { published: { tags: { 'engine/v0.28.0': BEFORE, 'core-v0.28.0': BEFORE } } },
      tagsElsewhere('engine/v0.28.0', 'core-v0.28.0', BEFORE, LATER, ENGINE_VERSION),
    ],
    [
      'both tags of the engine are at another commit and the release is a draft',
      ENGINE,
      {
        published: {
          tags: { 'engine/v0.28.0': BEFORE, 'core-v0.28.0': BEFORE },
          releases: { 'core-v0.28.0': releaseOf('engine', BEFORE, true) },
        },
      },
      tagsElsewhere('engine/v0.28.0', 'core-v0.28.0', BEFORE, LATER, ENGINE_VERSION),
    ],
    [
      'both tags of the extension are at another commit and the release is published',
      VSCODE,
      {
        published: {
          tags: { 'vscode/v0.36.0': BEFORE, 'vscode-v0.36.0': BEFORE },
          releases: { 'vscode-v0.36.0': releaseOf('vscode', BEFORE) },
        },
      },
      tagElsewhere('vscode/v0.36.0', BEFORE, LATER, VSCODE_VERSION),
    ],
    [
      'the extension tags are at different commits, the first at the commit to release',
      VSCODE,
      { published: { tags: { 'vscode/v0.36.0': LATER, 'vscode-v0.36.0': BEFORE } } },
      tagElsewhere('vscode-v0.36.0', BEFORE, LATER, VSCODE_VERSION),
    ],
    [
      'only the tag of the engine release exists, though at the commit to release',
      ENGINE,
      { published: { tags: { 'core-v0.28.0': LATER } } },
      tagAlone('core-v0.28.0', ENGINE_VERSION),
    ],
    [
      'only the extension tag in the form of its folder exists, though at the commit to release',
      VSCODE,
      { published: { tags: { 'vscode/v0.36.0': LATER } } },
      tagAlone('vscode/v0.36.0', VSCODE_VERSION),
    ],
    [
      'the tag of the extension release is at another commit, and the other tag is missing',
      VSCODE,
      { published: { tags: { 'vscode-v0.36.0': BEFORE } } },
      tagElsewhere('vscode-v0.36.0', BEFORE, LATER, VSCODE_VERSION),
    ],
    [
      'the tag of the engine release is annotated, and peels to another commit than the one to release',
      ENGINE,
      {
        published: {
          tags: { 'engine/v0.28.0': LATER, 'core-v0.28.0': `${TAG_OBJECT} ${BEFORE}` },
        },
      },
      tagElsewhere('core-v0.28.0', BEFORE, LATER, ENGINE_VERSION),
    ],
    [
      'the tag of the engine release is annotated, and peels to the commit to release, which a push of the commit cannot replace',
      ENGINE,
      {
        published: {
          tags: { 'engine/v0.28.0': LATER, 'core-v0.28.0': `${TAG_OBJECT} ${LATER}` },
        },
      },
      tagAnnotated('core-v0.28.0'),
    ],
    [
      'both tags are at the commit to release and the release is published',
      ENGINE,
      {
        published: {
          tags: { 'engine/v0.28.0': LATER, 'core-v0.28.0': LATER },
          releases: { 'core-v0.28.0': { draft: false, title: 'A release', notes: '', files: {} } },
        },
      },
      'The release core-v0.28.0 exists already. Prepare and release the next version.',
    ],
    [
      'the release exists, though its tag is gone',
      ENGINE,
      {
        published: {
          releases: { 'core-v0.28.0': { draft: false, title: 'A release', notes: '', files: {} } },
        },
      },
      'The release core-v0.28.0 exists already. Prepare and release the next version.',
    ],
    [
      "the engine's changelog has no heading for the version",
      { ...ENGINE, version: '0.29.0' },
      {},
      notInChangelog('engine', LATER, '0.29.0'),
    ],
    [
      "the engine's changelog gets the heading only after the head of main the run started at",
      ENGINE,
      { sha: BEFORE },
      notInChangelog('engine', BEFORE, ENGINE_VERSION),
    ],
    [
      "the extension's package.json names another version",
      { ...VSCODE, version: '0.37.0' },
      {},
      `vscode/package.json at ${LATER} names version ${VSCODE_VERSION}, not 0.37.0. Release ${VSCODE_VERSION}; or prepare 0.37.0 first, merge it, and run this workflow again.`,
    ],
    [
      "the extension's package.json names the version and its changelog has no heading for it",
      VSCODE,
      { sha: BEFORE },
      notInChangelog('vscode', BEFORE, VSCODE_VERSION),
    ],
  ])(
    'stops a release before anything is built when %s, and says what to do',
    (_when, inputs, { ref, sha, published }, message) => {
      const root = repository(published);
      const before = publishedIn(root);
      const run = release(root, inputs, {
        ...(ref === undefined ? {} : { ref }),
        ...(sha === undefined ? {} : { sha }),
      });

      expect(run.stderr).toBe(`${message}\n`);
      expect(run.failed).toBe('check');
      expect(run.passed).toEqual([]);
      expect(run.actions).toEqual(['actions/checkout']);
      expect(
        run.calls.filter((call) => !/^(git (ls-remote|show)|gh release list) /.test(call)),
      ).toEqual([]);
      expect(readdirSync(run.artifacts)).toEqual([]);
      expect(publishedIn(root)).toEqual(before);
    },
  );

  it.each<[Part, string, string, string]>([
    ['engine', 'the head of main', LATER, 'actions/setup-go'],
    ['engine', 'a head of main that has no later work yet', PREPARED, 'actions/setup-go'],
    ['vscode', 'the head of main', LATER, 'actions/setup-node'],
    ['vscode', 'a head of main that has no later work yet', PREPARED, 'actions/setup-node'],
  ])(
    "releases the %s at %s: builds the source of the run's commit with the make dist of the part, tags the commit twice, publishes the release named as the releases are with the files, their provenance and the notes of the version, only the extension as latest, and leaves it signed",
    (part, _from, source, setup) => {
      const earlier: Published = {
        tags: { 'vscode-v0.35.0': BEFORE },
        releases: {
          'vscode-v0.35.0': {
            draft: false,
            title: 'cel-lens for VS Code v0.35.0',
            notes: '- An older change.\n',
            files: { 'cel-lens-vscode-linux-x64-0.35.0.vsix': 'the extension before' },
          },
        },
        latest: 'vscode-v0.35.0',
      };
      const root = repository(earlier);
      const version = part === 'engine' ? ENGINE_VERSION : VSCODE_VERSION;
      const [sourceTag, releaseTag] = tagsOf(part);
      const run = release(root, { part, version }, { sha: source });

      expect(run.stderr).toBe('');
      expect(run.failed).toBeUndefined();
      expect(run.passed).toEqual(['check', 'build', 'publish', 'sign']);
      expect(run.actions).toEqual([
        'actions/checkout',
        'actions/checkout',
        setup,
        'actions/upload-artifact',
        'actions/checkout',
        'actions/download-artifact',
        'actions/attest',
      ]);
      expect(run.calls.filter((call) => call.startsWith('make '))).toEqual([
        `make -C ${part} dist VERSION=v${version}`,
      ]);
      expect(publishedIn(root)).toEqual({
        tags: { ...earlier.tags, [sourceTag]: source, [releaseTag]: source },
        releases: { ...earlier.releases, [releaseTag]: signedFrom(releaseOf(part, source)) },
        latest: part === 'engine' ? 'vscode-v0.35.0' : releaseTag,
      });
    },
  );

  it('types these calls for a release of the engine, in this order: nothing is written before the files are built and attested, the tags come after the draft, and the release is published last', () => {
    const run = release(repository(), ENGINE);
    const files = Object.keys(built('engine', LATER)).map((name) => `release/files/${name}`);
    const list = `gh release list --repo ${REPOSITORY} --limit 1000 --json tagName,isDraft --jq .[] | "\\(.tagName) \\(.isDraft)"`;

    expect(run.failed).toBeUndefined();
    // Without the calls of the run of sign-release.yml, which other tests hold.
    expect(
      run.calls.filter((call) => !/^(cosign|gh release (download|upload)) /.test(call)),
    ).toEqual([
      list,
      'git ls-remote --tags origin refs/tags/engine/v0.28.0 refs/tags/engine/v0.28.0^{}',
      'git ls-remote --tags origin refs/tags/core-v0.28.0 refs/tags/core-v0.28.0^{}',
      `git show ${LATER}:engine/CHANGELOG.md`,
      'make -C engine dist VERSION=v0.28.0',
      list,
      `gh release create core-v0.28.0 ${files.join(' ')} release/${PROVENANCE} --repo ${REPOSITORY} --draft --target ${LATER} --title cel-lens core v0.28.0 --notes-file release/notes.md`,
      `git push --atomic origin ${LATER}:refs/tags/engine/v0.28.0 ${LATER}:refs/tags/core-v0.28.0`,
      `gh release edit core-v0.28.0 --repo ${REPOSITORY} --draft=false --latest=false`,
      `gh api repos/${REPOSITORY}/actions/workflows/sign-release.yml/dispatches --method POST -f ref=main -f inputs[tag]=core-v0.28.0 -F return_run_details=true --jq .workflow_run_id // empty`,
      `gh run view 1 --repo ${REPOSITORY} --json status`,
      `gh run watch 1 --repo ${REPOSITORY} --exit-status --interval 10`,
    ]);
  });

  it('builds in a job that may write nothing, from the commit of the run, on the Go version of engine/go.mod or Node 24 as CI does, without a cache', () => {
    const ci = readWorkflow(CI_PATH).jobs ?? {};
    const build = readWorkflow(RELEASE_PATH).jobs?.['build'];
    const steps = build?.steps ?? [];
    const setupOf = (job: string): Step | undefined =>
      ci[job]?.steps?.find((step) => step.uses?.startsWith('actions/setup-'));

    expect(build?.needs).toBe('check');
    expect(build?.permissions).toBeUndefined();
    expect(steps.map((step) => step.uses ?? 'a script')).toEqual([
      'actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1',
      'actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e',
      'actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1',
      'a script',
      'actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9',
    ]);
    expect(steps[0]?.with).toEqual({ 'persist-credentials': false });
    expect(steps[1]).toMatchObject({
      if: "inputs.part == 'engine'",
      uses: setupOf('engine')?.uses,
      with: { ...setupOf('engine')?.with, cache: false },
    });
    expect(steps[1]?.with).toHaveProperty('go-version-file', 'engine/go.mod');
    expect(steps[2]).toMatchObject({
      if: "inputs.part == 'vscode'",
      uses: setupOf('vscode')?.uses,
      with: { ...setupOf('vscode')?.with, 'package-manager-cache': false },
    });
    expect(steps[2]?.with).toHaveProperty('node-version', 24);
    expect(steps[4]?.with).toEqual({
      name: 'release',
      path: 'release/',
      'if-no-files-found': 'error',
      overwrite: true,
    });
  });

  it('attests the build provenance of every file it built and of no other, in the job that publishes, and the attestation is the one file of the release whose name ends in .intoto.jsonl', () => {
    const root = repository();
    const run = release(root, ENGINE);
    const job = readWorkflow(RELEASE_PATH).jobs?.['publish'];
    const files = publishedIn(root).releases['core-v0.28.0']?.files ?? {};

    expect(run.failed).toBeUndefined();
    expect(job?.needs).toEqual(['check', 'build']);
    expect(job?.steps?.map((step) => step.uses ?? 'a script')).toEqual([
      'actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1',
      'actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333',
      'actions/attest@1e69f48acb82d1966a394da916b4c1698aa569d6',
      'a script',
    ]);
    expect(job?.steps?.slice(0, 3).map((step) => step.with)).toEqual([
      undefined,
      { name: 'release', path: 'release' },
      { 'subject-path': 'release/files/*' },
    ]);
    expect(job?.steps?.[2]?.id).toBe('attest');
    expect(Object.keys(files).filter((name) => name.endsWith('.intoto.jsonl'))).toEqual([
      PROVENANCE,
    ]);
    // The dotfile of dist/ is no file of the release, and so not attested.
    expect(files[PROVENANCE]).toBe(provenanceOf(Object.keys(built('engine', LATER))));
  });

  it("checks out the run's own commit in every job of every workflow: no checkout passes a ref, and none of a job that can write takes a repository or a path from an input, a job or a pull request", () => {
    const checkouts = WORKFLOW_PATHS.flatMap((path) =>
      Object.entries(readWorkflow(path).jobs ?? {}).flatMap(([name, job]) =>
        (job.steps ?? [])
          .filter((step) => step.uses?.startsWith('actions/checkout@') === true)
          .map((step) => ({
            where: `${basename(path)} ${name}`,
            writes: canWrite(job.permissions),
            settings: step.with ?? {},
          })),
      ),
    );
    const derived = checkouts
      .filter(({ writes: canWrite }) => canWrite)
      .filter(({ settings }) =>
        ['ref', 'repository', 'path'].some((key) => {
          const value = settings[key];
          return typeof value === 'string' && INPUT_DERIVED.test(value);
        }),
      )
      .map(({ where }) => where);

    expect(checkouts.length).toBeGreaterThan(0);
    expect(checkouts.filter(({ settings }) => 'ref' in settings).map(({ where }) => where)).toEqual(
      [],
    );
    expect(derived).toEqual([]);
  });

  it('has the guard against a checkout taken from an input see a job that writes with `write-all`, and the head of a pull request as an input', () => {
    expect([canWrite('write-all'), canWrite('read-all'), canWrite(undefined)]).toEqual([
      true,
      false,
      false,
    ]);
    expect([canWrite({ contents: 'write' }), canWrite({ contents: 'read' })]).toEqual([
      true,
      false,
    ]);
    expect(
      [
        '${{ needs.check.outputs.commit }}',
        '${{ inputs.commit }}',
        '${{ github.event.inputs.commit }}',
        '${{ github.head_ref }}',
        '${{ github.event.pull_request.head.sha }}',
        '${{ github.sha }}',
      ].map((value) => INPUT_DERIVED.test(value)),
    ).toEqual([true, true, true, true, true, false]);
  });

  it('leaves the release signed with nobody starting a second workflow: it starts sign-release.yml from main for the tag and waits for that run, and the command in SECURITY.md accepts its signatures; the provenance gets none', () => {
    const root = repository();
    const run = release(root, ENGINE);
    const files = publishedIn(root).releases['core-v0.28.0']?.files ?? {};
    const signatures = Object.entries(files).filter(([name]) => name.endsWith('.sigstore.json'));
    const names = Object.keys(built('engine', LATER));
    const dispatch = run.calls.findIndex((call) => call.startsWith('gh api '));

    expect(readWorkflow(RELEASE_PATH).jobs?.['sign']?.needs).toEqual(['check', 'publish']);
    expect(run.calls[dispatch - 1]).toBe(
      `gh release edit core-v0.28.0 --repo ${REPOSITORY} --draft=false --latest=false`,
    );
    expect(run.calls[dispatch]).toBe(
      `gh api repos/${REPOSITORY}/actions/workflows/sign-release.yml/dispatches --method POST -f ref=main -f inputs[tag]=core-v0.28.0 -F return_run_details=true --jq .workflow_run_id // empty`,
    );
    // The run of sign-release.yml that the call starts, then the wait for it.
    expect(run.calls.slice(dispatch + 1)).toEqual([
      `gh release download --repo ${REPOSITORY} --dir assets -- core-v0.28.0`,
      ...names.flatMap((name) => [
        `cosign sign-blob --yes --bundle assets/${name}.sigstore.json assets/${name}`,
        `cosign verify-blob --bundle assets/${name}.sigstore.json --certificate-identity ${SIGNER}@${MAIN_REF} --certificate-oidc-issuer ${ISSUER} assets/${name}`,
      ]),
      `gh release upload --repo ${REPOSITORY} --clobber -- core-v0.28.0 ${names.map((name) => `assets/${name}.sigstore.json`).join(' ')}`,
      `gh run view 1 --repo ${REPOSITORY} --json status`,
      `gh run watch 1 --repo ${REPOSITORY} --exit-status --interval 10`,
    ]);
    expect(verifies(`${SIGNER}@${MAIN_REF}`)).toBe(true);
    expect(signatures.map(([name]) => name.replace(/\.sigstore\.json$/, '')).sort()).toEqual(
      [...names].sort(),
    );
    expect(
      signatures.filter(([, text]) => !text.endsWith(`, signed by ${SIGNER}@${MAIN_REF}\n`)),
    ).toEqual([]);
  });

  it.each<[string, Mishap, string, 'no release' | 'a draft' | 'the release', boolean]>([
    ['make dist fails', { fail: 'make ' }, 'build', 'no release', false],
    ['the attestation fails', { attestFails: true }, 'publish', 'no release', false],
    ['gh release create fails', { fail: 'gh release create' }, 'publish', 'no release', false],
    [
      'gh release create fails after it made the draft',
      { failLate: 'gh release create' },
      'publish',
      'a draft',
      false,
    ],
    ['git push fails', { fail: 'git push' }, 'publish', 'a draft', false],
    ['gh release edit fails', { fail: 'gh release edit' }, 'publish', 'a draft', true],
    [
      'gh release edit fails after it published the release',
      { failLate: 'gh release edit' },
      'publish',
      'the release',
      true,
    ],
    ['sign-release.yml does not start', { fail: 'gh api' }, 'sign', 'the release', true],
    ['the run of sign-release.yml fails', { verifyStatus: 1 }, 'sign', 'the release', true],
  ])(
    'when %s, leaves nothing half done that a re-run of the failed jobs does not finish: no tag without the other, no published release without its files, and after the re-run the release as a run without a failure leaves it',
    (_when, mishap, job, left, tagged) => {
      const root = repository();
      const [sourceTag, releaseTag] = tagsOf('engine');
      const tags = { [sourceTag]: LATER, [releaseTag]: LATER };
      const failed = release(root, ENGINE, mishap);
      const half = publishedIn(root);
      const again = attempt(failed);

      expect(failed.failed).toBe(job);
      expect(half.tags).toEqual(tagged ? tags : {});
      expect(half.releases).toEqual(
        left === 'no release'
          ? {}
          : { [releaseTag]: releaseOf('engine', LATER, left === 'a draft') },
      );
      expect(again.stderr).toBe('');
      expect(again.failed).toBeUndefined();
      expect(again.passed).toEqual(['check', 'build', 'publish', 'sign']);
      // The jobs that passed do not run again, and only a draft is ever deleted.
      expect(again.calls.filter((call) => call.startsWith('make '))).toHaveLength(
        job === 'build' ? 1 : 0,
      );
      expect(again.calls.filter((call) => call.startsWith('gh release delete'))).toEqual(
        left === 'a draft' ? [`gh release delete ${releaseTag} --repo ${REPOSITORY} --yes`] : [],
      );
      expect(publishedIn(root)).toEqual({
        tags,
        releases: { [releaseTag]: signedFrom(releaseOf('engine', LATER)) },
        latest: undefined,
      });
    },
  );

  it('lets a new run finish what a failed run left before the tags existed: it deletes the draft and makes the release', () => {
    const root = repository();
    const [sourceTag, releaseTag] = tagsOf('engine');
    const failed = release(root, ENGINE, { fail: 'git push' });
    const run = release(root, ENGINE);

    expect(failed.failed).toBe('publish');
    expect(run.stderr).toBe('');
    expect(run.failed).toBeUndefined();
    expect(run.calls).toContain(`gh release delete ${releaseTag} --repo ${REPOSITORY} --yes`);
    expect(publishedIn(root)).toEqual({
      tags: { [sourceTag]: LATER, [releaseTag]: LATER },
      releases: { [releaseTag]: signedFrom(releaseOf('engine', LATER)) },
      latest: undefined,
    });
  });

  it('lets a new run with the same inputs finish a release whose first run pushed both tags and failed at gh release edit: the tags stay, the draft is replaced, and the release is published and signed', () => {
    const root = repository();
    const [sourceTag, releaseTag] = tagsOf('engine');
    const failed = release(root, ENGINE, { fail: 'gh release edit' });
    const half = publishedIn(root);
    const fresh = release(root, ENGINE);

    expect(failed.failed).toBe('publish');
    expect(half.tags).toEqual({ [sourceTag]: LATER, [releaseTag]: LATER });
    expect(half.releases).toEqual({ [releaseTag]: releaseOf('engine', LATER, true) });
    expect(fresh.stderr).toBe('');
    expect(fresh.failed).toBeUndefined();
    expect(fresh.passed).toEqual(['check', 'build', 'publish', 'sign']);
    expect(fresh.calls).toContain(`gh release delete ${releaseTag} --repo ${REPOSITORY} --yes`);
    // Both tags are at the commit, so the push is a no-op: it moves nothing.
    expect(fresh.calls).toContain(
      `git push --atomic origin ${LATER}:refs/tags/${sourceTag} ${LATER}:refs/tags/${releaseTag}`,
    );
    expect(publishedIn(root)).toEqual({
      tags: { [sourceTag]: LATER, [releaseTag]: LATER },
      releases: { [releaseTag]: signedFrom(releaseOf('engine', LATER)) },
      latest: undefined,
    });
  });

  it('lets a new run finish a release whose draft was deleted by hand after a run pushed both tags: both plain tags are at its commit and no release exists, so it makes the release, publishes it and has it signed', () => {
    const root = repository();
    const [sourceTag, releaseTag] = tagsOf('engine');
    const failed = release(root, ENGINE, { fail: 'gh release edit' });
    rmSync(join(root, 'releases', releaseTag), { recursive: true });
    rmSync(join(root, 'meta', releaseTag), { recursive: true });
    const fresh = release(root, ENGINE);

    expect(failed.failed).toBe('publish');
    expect(publishedIn(root).tags).toEqual({ [sourceTag]: LATER, [releaseTag]: LATER });
    expect(fresh.stderr).toBe('');
    expect(fresh.failed).toBeUndefined();
    expect(fresh.calls.filter((call) => call.startsWith('gh release delete'))).toEqual([]);
    expect(publishedIn(root)).toEqual({
      tags: { [sourceTag]: LATER, [releaseTag]: LATER },
      releases: { [releaseTag]: signedFrom(releaseOf('engine', LATER)) },
      latest: undefined,
    });
  });

  it('stops a new run that starts after main has moved on from the commit of a run that pushed both tags and failed before publishing (#1): it says to re-run the failed jobs of that run, naming its commit, and changes nothing; the re-run, which keeps its commit, finishes the release', () => {
    const root = repository();
    const [sourceTag, releaseTag] = tagsOf('engine');
    const failed = release(root, ENGINE, { fail: 'gh release edit', sha: PREPARED });
    const half = publishedIn(root);
    const fresh = release(root, ENGINE);

    expect(failed.failed).toBe('publish');
    expect(fresh.stderr).toBe(
      `${tagsElsewhere(sourceTag, releaseTag, PREPARED, LATER, ENGINE_VERSION)}\n`,
    );
    expect(fresh.failed).toBe('check');
    expect(publishedIn(root)).toEqual(half);

    const rerun = attempt(failed);

    expect(rerun.stderr).toBe('');
    expect(rerun.failed).toBeUndefined();
    expect(publishedIn(root)).toEqual({
      tags: { [sourceTag]: PREPARED, [releaseTag]: PREPARED },
      releases: { [releaseTag]: signedFrom(releaseOf('engine', PREPARED)) },
      latest: undefined,
    });
  });

  it('creates both tags or neither: when one of them appears at another commit after the check, it creates none and publishes nothing', () => {
    const root = repository();
    const [, releaseTag] = tagsOf('engine');
    const checked = release(root, ENGINE, { fail: 'make ' });
    writeFiles(join(root, 'tags'), { [releaseTag]: `${BEFORE}\n` });
    const run = attempt(checked);

    expect(checked.passed).toEqual(['check']);
    expect(run.failed).toBe('publish');
    expect(run.stderr).toBe(` ! [rejected] ${LATER} -> ${releaseTag} (already exists)\n`);
    expect(run.calls.filter((call) => call.startsWith('gh release edit'))).toEqual([]);
    expect(publishedIn(root)).toEqual({
      tags: { [releaseTag]: BEFORE },
      releases: { [releaseTag]: releaseOf('engine', LATER, true) },
      latest: undefined,
    });
  });

  it('fails the job that has the release signed, with what to do, when GitHub does not say which run of sign-release.yml it started', () => {
    const run = release(repository(), ENGINE, { runDetails: false });

    expect(run.failed).toBe('sign');
    expect(run.stderr).toBe(
      'GitHub started sign-release.yml for core-v0.28.0 and did not say which run it is. Find the run under Actions; if it failed or is not there, run sign-release.yml by hand from main with the tag core-v0.28.0.\n',
    );
    expect(run.calls.filter((call) => call.startsWith('gh run watch'))).toEqual([]);
  });

  it.each([1, 2, 10, 23])(
    'waits for the run of sign-release.yml to be readable, as it was not at once in run 43465064181 of core v0.30.0 (#18): %i unreadable reads, 5 seconds apart, then it watches the run and passes',
    (unreadable) => {
      const run = release(repository(), ENGINE, { unreadable });
      const calls = run.calls.filter((call) => /^(sleep|gh run) /.test(call));

      expect(run.stderr).toBe('');
      expect(run.failed).toBeUndefined();
      expect(run.passed).toEqual(['check', 'build', 'publish', 'sign']);
      expect(calls.filter((call) => call.startsWith('gh run view'))).toHaveLength(unreadable + 1);
      expect(calls.filter((call) => call === 'sleep 5')).toHaveLength(unreadable);
      expect(calls.at(-1)).toBe(`gh run watch 1 --repo ${REPOSITORY} --exit-status --interval 10`);
      expect(calls.filter((call) => call.startsWith('gh run watch'))).toHaveLength(1);
    },
  );

  it('fails the job that has the release signed, with what to do, when the run of sign-release.yml stays unreadable for 24 reads, about 2 minutes (#18): it never watches the run, and the message has what GitHub answered', () => {
    const run = release(repository(), ENGINE, { unreadable: 1000 });
    const calls = run.calls.filter((call) => /^(sleep|gh run) /.test(call));

    expect(run.failed).toBe('sign');
    expect(run.stderr).toBe(
      'GitHub started sign-release.yml for core-v0.28.0 as run 1, and the run was still not readable after 2 minutes; GitHub answered: failed to get run: HTTP 404: Not Found. Find the run under Actions; if it failed or is not there, run sign-release.yml by hand from main with the tag core-v0.28.0.\n',
    );
    expect(calls.filter((call) => call.startsWith('gh run view'))).toHaveLength(24);
    expect(calls.filter((call) => call === 'sleep 5')).toHaveLength(23);
    expect(calls.filter((call) => call.startsWith('gh run watch'))).toEqual([]);
  });

  it('does not fail the job that has the release signed on a 404 of the run, as run 43462904507 of core v0.30.0 did (#18): the watch of a run that is not readable yet answers 404, and the job waits for the run before it watches', () => {
    const run = release(repository(), ENGINE, { unreadable: 3 });

    expect(run.stderr).toBe('');
    expect(run.failed).toBeUndefined();
    expect(run.passed).toEqual(['check', 'build', 'publish', 'sign']);
  });

  it('still fails the job that has the release signed when the run of sign-release.yml, once readable, fails', () => {
    const run = release(repository(), ENGINE, { unreadable: 2, verifyStatus: 1 });

    expect(run.failed).toBe('sign');
    expect(run.calls).toContain(`gh run watch 1 --repo ${REPOSITORY} --exit-status --interval 10`);
  });

  it('says beside each permission a job of the release workflow writes with why it needs it', () => {
    const text = readFileSync(RELEASE_PATH, 'utf8');

    expect(text).toMatch(/^ +contents: write # \S.*\btags\b.*\brelease\b/m);
    expect(text).toMatch(/^ +id-token: write # \S.*\bidentity\b.*\bprovenance\b/m);
    expect(text).toMatch(/^ +attestations: write # \S.*\bprovenance\b/m);
    expect(text).toMatch(/^ +actions: write # \S.*\bsign-release\.yml\b/m);
    expect(text.split('\n').filter((line) => /^ +[a-z-]+: write$/.test(line))).toEqual([]);
  });

  it.each([
    'A new run finishes the release as long as it is not published, and both tags, if they exist, are at the commit it releases.',
    'When a run fails, re-run its failed jobs, or run this workflow again with the same inputs: each job says below what that does.',
    'A re-run checks again; nothing was done.',
    'When this job fails, nothing of the release exists; a re-run builds again.',
    'A re-run of this job uploads over what an earlier attempt left.',
    'a re-run attests the same files again, which harms nothing.',
    'gh release create: a draft may be left, with some of the files. The re-run deletes it and makes a new one. So does a new run.',
    'git push: neither tag exists, and the draft does. The re-run makes the draft again and pushes both tags. So does a new run.',
    'gh release edit: the tags exist and the draft does. The re-run makes the draft again, pushes nothing new, as both tags are at the commit already, and publishes. So does a new run: its check lets it pass, as both tags are at its commit.',
    'when the release is published and the step still failed: the re-run sees the release and leaves it as it is.',
    'No re-run deletes or moves a tag, or deletes a published release.',
    'A re-run starts sign-release.yml again, which signs every file again and replaces the signatures it finds.',
  ])('says in the comments of the release workflow what a re-run does: %s', (sentence) => {
    const comments = readFileSync(RELEASE_PATH, 'utf8')
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line.startsWith('#'))
      .map((line) => line.replace(/^# ?/, '').trim())
      .join(' ');

    expect(comments).toContain(sentence);
  });

  it('pins the actions of the release workflow at the commits of their releases, where the github-actions entry of Dependabot finds them', () => {
    const config = parse(readFileSync(DEPENDABOT_PATH, 'utf8')) as {
      updates: { 'package-ecosystem': string; directory: string }[];
    };

    // Dependabot reads the workflows of .github/workflows for the directory /.
    expect(
      config.updates.filter((update) => update['package-ecosystem'] === 'github-actions'),
    ).toMatchObject([{ directory: '/' }]);
    expect(dirname(RELEASE_PATH)).toBe(`${FOLDER}/.github/workflows`);
    expect(readFileSync(RELEASE_PATH, 'utf8').match(/uses: \S+ # v[\d.]+$/gm)).toEqual([
      'uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1',
      'uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1',
      'uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0',
      'uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0',
      'uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2',
      'uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1',
      'uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2',
      'uses: actions/attest@1e69f48acb82d1966a394da916b4c1698aa569d6 # v4.2.2',
    ]);
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

  it('has Dependabot propose no @types/vscode and no major of @types/node for the extension, and ignore nothing else', () => {
    const config = parse(readFileSync(DEPENDABOT_PATH, 'utf8')) as {
      updates: { ignore?: unknown }[];
    };

    expect(config.updates.map((update) => update.ignore)).toEqual([
      undefined,
      [
        { 'dependency-name': '@types/vscode' },
        { 'dependency-name': '@types/node', 'update-types': ['version-update:semver-major'] },
      ],
      undefined,
    ]);
  });

  it.each([
    ['@types/vscode', /engines\.vscode/, /by hand/],
    ['@types/node', /the Node of VS Code's extension host/, /by hand/],
  ])('says above the entry of %s in dependabot.yml why it is ignored', (name, ...reasons) => {
    const text = readFileSync(DEPENDABOT_PATH, 'utf8');
    const entry = text.indexOf(`- dependency-name: '${name}'`);
    const before = text.slice(0, entry).split('\n').slice(0, -1);
    // The comment lines right above the entry.
    const comment = before
      .slice(before.findLastIndex((line) => !line.trim().startsWith('#')) + 1)
      .map((line) => line.trim().replace(/^# ?/, ''))
      .join(' ');

    expect(entry).toBeGreaterThan(-1);
    for (const reason of reasons) {
      expect(comment).toMatch(reason);
    }
  });

  it('keeps @types/vscode at the VS Code that engines.vscode asks for, which is why it moves by hand', () => {
    const manifest = JSON.parse(readFileSync('package.json', 'utf8')) as {
      engines: { vscode: string };
      devDependencies: Record<string, string>;
    };

    expect(manifest.engines.vscode).toBe(`^${manifest.devDependencies['@types/vscode'] ?? ''}`);
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

  it.each([
    'A release of the engine or of the extension is made by the [release.yml](.github/workflows/release.yml) workflow, from the source on `main`',
    "Prepare the version in a change of its own: the heading `## [X.Y.Z]` with its notes in the part's `CHANGELOG.md`, and for the extension the `version` in `vscode/package.json` too.",
    'run the workflow by hand from `main` (**Actions**, **Release**, **Run workflow**) with the part (`engine` or `vscode`) and the version',
    'Right after that change is merged, run the workflow by hand from `main`',
    'It always releases the head of `main`: if other changes landed after yours, prepare the release again, or release them too.',
    "it builds the files with the part's `make dist`",
    'creates the tags (`engine/vX.Y.Z` and `core-vX.Y.Z`, or `vscode/vX.Y.Z` and `vscode-vX.Y.Z`)',
    "publishes the release with the files, their build provenance and the version's changelog section as its notes, and has the files signed",
    '[Verifying a release](SECURITY.md#verifying-a-release) says how to check a file.',
    'When a run fails, re-run its failed jobs, or run the workflow again with the same inputs: a new run finishes the release when the tags exist and are both at the commit it releases, and the release is not published.',
  ])('says in CONTRIBUTING.md how a release is made: %s', (text) => {
    const contributing = readFileSync(`${FOLDER}/CONTRIBUTING.md`, 'utf8');

    expect(sectionOf(contributing, 'Releases')).toContain(text);
  });

  it('says in CONTRIBUTING.md how a release is made in a few lines: a sentence, two steps and a paragraph', () => {
    const section = sectionOf(readFileSync(`${FOLDER}/CONTRIBUTING.md`, 'utf8'), 'Releases');

    expect(section.split('\n').filter(Boolean)).toHaveLength(5);
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
    'The releases after cel-lens core v0.27.0 and cel-lens for VS Code v0.35.0 are built by the [release.yml](.github/workflows/release.yml) workflow of this repository',
    'each carries the build provenance of its files: one attestation that names every file the workflow built, in the file `provenance.intoto.jsonl` of the release',
    'gh attestation verify checksums.txt',
    '--bundle provenance.intoto.jsonl',
    '--repo JairJosafath/cel-lens',
    '--cert-identity https://github.com/JairJosafath/cel-lens/.github/workflows/release.yml@refs/heads/main',
    "The provenance proves what a signature does not: that a run of this repository's `release.yml`, as `main` had it, built the file, and which run that was.",
    'The provenance names the workflow run and the commit it released, the head of `main` when the run started (the `sha` of the run)',
    "which is also the commit the release's tags name",
    'the log of the run says it too: "Releasing … from <commit>"',
  ])('says in SECURITY.md how a release is verified: %s', (text) => {
    const security = readFileSync(`${FOLDER}/SECURITY.md`, 'utf8');

    expect(sectionOf(security, 'Verifying a release')).toContain(text);
  });

  it('verifies provenance in SECURITY.md with the file the release workflow adds to a release, for a file it attests, against the identity of a run of it from main', () => {
    const security = readFileSync(`${FOLDER}/SECURITY.md`, 'utf8');
    const command = /```sh\ngh attestation verify (\S+) [^`]*```/.exec(
      sectionOf(security, 'Verifying a release'),
    );
    const root = repository();
    const run = release(root, ENGINE);
    const files = publishedIn(root).releases['core-v0.28.0']?.files ?? {};

    expect(run.failed).toBeUndefined();
    expect(/--bundle (\S+)/.exec(command?.[0] ?? '')?.[1]).toBe(PROVENANCE);
    expect(Object.keys(built('engine', LATER))).toContain(command?.[1]);
    expect(/--repo (\S+)/.exec(command?.[0] ?? '')?.[1]).toBe(REPOSITORY);
    expect(/--cert-identity (\S+)/.exec(command?.[0] ?? '')?.[1]).toBe(`${BUILDER}@${MAIN_REF}`);
    expect(files[PROVENANCE]).toContain(`"signer":"${BUILDER}@${MAIN_REF}"`);
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
