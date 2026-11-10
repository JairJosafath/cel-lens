import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const CHANGELOG = readFileSync(`${__dirname}/../CHANGELOG.md`, 'utf8');
const KINDS = ['Added', 'Changed', 'Deprecated', 'Removed', 'Fixed', 'Security'];

function isInOrder(kinds: string[]): boolean {
  const known = kinds.filter((kind) => KINDS.includes(kind));
  return (
    known.length === kinds.length &&
    known.every((kind, i) => i === 0 || KINDS.indexOf(kind) > KINDS.indexOf(known[i - 1] ?? ''))
  );
}

function engineVersions(text: string): string[] {
  const found = text
    .split('\n')
    .flatMap((line) => line.split('cel-lens-core').slice(1))
    .flatMap((after) => {
      const version = /\bv?(\d+\.\d+\.\d+)\b/.exec(after)?.[1];
      return version === undefined ? [] : [`v${version}`];
    });
  return [...new Set(found)];
}

const REPO = 'https://github.com/JairJosafath/cel-lens';
const OLDEST_PUBLIC_VERSION = '0.14.0';

function linkProblems(versions: string[], refs: string[]): string[] {
  const tag = (name: string): string => `vscode-v${name}`;
  const oldest = versions.indexOf(OLDEST_PUBLIC_VERSION);
  return versions.flatMap((version, index) => {
    if (index > oldest) {
      const stray = refs.filter((ref) => ref.startsWith(`[${version}]:`));
      return stray.map((ref) => `no link line for ${version}, found: ${ref}`);
    }
    const previous = versions[index + 1];
    const expected =
      version === 'Unreleased'
        ? `[Unreleased]: ${REPO}/compare/${tag(versions[1] ?? '')}...HEAD`
        : index === oldest
          ? `[${version}]: ${REPO}/releases/tag/${tag(version)}`
          : `[${version}]: ${REPO}/compare/${tag(previous ?? '')}...${tag(version)}`;
    return refs.includes(expected) ? [] : [expected];
  });
}

function issueReferenceLines(text: string): string[] {
  return text.split('\n').filter((line) => /#\d/.test(line));
}

const sections = CHANGELOG.split(/^(?=## \[)/m)
  .slice(1)
  .map((text) => ({
    version: /^## \[([^\]]+)\]/.exec(text)?.[1] ?? '',
    engines: engineVersions(text),
    kinds: [...text.matchAll(/^### (.+)$/gm)].map((match) => match[1] ?? ''),
  }));

describe('CHANGELOG.md', () => {
  it.each(sections)('lists each kind once, in Keep a Changelog order, in $version', ({ kinds }) => {
    expect(isInOrder(kinds)).toBe(true);
  });

  it.each([
    { name: 'all six kinds in order', kinds: KINDS, ok: true },
    { name: 'a subset in order', kinds: ['Changed', 'Security'], ok: true },
    { name: 'a duplicated kind', kinds: ['Added', 'Added'], ok: false },
    { name: 'Fixed before Removed', kinds: ['Fixed', 'Removed'], ok: false },
    { name: 'Security before Deprecated', kinds: ['Security', 'Deprecated'], ok: false },
    { name: 'an unknown kind', kinds: ['Added', 'Improved'], ok: false },
  ])('kind check: $name is $ok', ({ kinds, ok }) => {
    expect(isInOrder(kinds)).toBe(ok);
  });

  it.each(sections)('names at most one cel-lens-core engine in $version', ({ engines }) => {
    expect(
      engines.length,
      `cel-lens-core versions found: ${engines.join(', ')}`,
    ).toBeLessThanOrEqual(1);
  });

  it.each([
    {
      name: 'standard wording',
      text: 'carries the `cel-lens-core` v0.9.0 engine',
      found: ['v0.9.0'],
    },
    { name: 'bundles, no backticks', text: 'bundles cel-lens-core 0.10.1 now', found: ['v0.10.1'] },
    {
      name: 'two versions in any wording',
      text: 'carries the `cel-lens-core` v0.9.0.\n- Ships cel-lens-core v0.11.0 too',
      found: ['v0.9.0', 'v0.11.0'],
    },
    {
      name: 'two versions on one line',
      text: 'cel-lens-core v0.9.0 and cel-lens-core v0.11.0',
      found: ['v0.9.0', 'v0.11.0'],
    },
    {
      name: 'an unrelated version later on the same line',
      text: 'carries the `cel-lens-core` v0.11.0 engine, which needs VS Code 1.90.0',
      found: ['v0.11.0'],
    },
    {
      name: 'the same version twice',
      text: 'cel-lens-core v0.9.0 and `cel-lens-core` v0.9.0',
      found: ['v0.9.0'],
    },
    { name: 'no engine named', text: 'Fixes v1.2.3 of something else', found: [] },
  ])('engine check: $name finds $found', ({ text, found }) => {
    expect(engineVersions(text)).toEqual(found);
  });

  it('has an Unreleased section and a released version for every section', () => {
    expect(sections[0]?.version).toBe('Unreleased');
    expect(sections.length).toBeGreaterThan(1);
  });

  /** The part of the Unreleased section under the heading of `kind`. */
  function unreleasedPart(kind: string): string | undefined {
    const unreleased = CHANGELOG.split(/^(?=## \[)/m)[1] ?? '';
    return unreleased.split(/^(?=### )/m).find((part) => part.startsWith(`### ${kind}\n`));
  }

  it.each([
    '`cel-lens.inlineInfo`',
    '**cel-lens: Toggle Inline Info**',
    '**Give it a type**',
    '**Write a schema in .cellens…** (OpenAPI), **Use a Go type…** and **Write a short type in .cellens…** first',
    'open `.cellens` beside the rule',
    'in the editor group that already shows it',
    "with the engine's completion, hover and problems",
    'each with its folder and doc comment',
    'A Go type or a kind is written at once',
    'the Go struct types of the workspace folder of the `.cellens`',
    'has a **Back** button to it',
    'A value of a CRD is offered `int` and `string` only',
    'says so once, with a button to `cel-lens.serverPath`',
    'where the two lines that write in `.cellens` open it at the entry',
    '**Remove** removes the entry at once',
    '**cel-lens: Give a Value a Type…**',
    'how to give a type in each form',
    '**Give it a type** can take an example',
    'Under **Examples in the workspace**',
    '`config/samples/limit.yaml` with `rules.example.com/v1 ReplicaLimit` and the line it starts at',
    '**Paste an example…** opens an editor beside the rule, named after the value (such as **Example of params.yaml**) and kept in memory, so closing it never asks to save',
    '**Use this example as the type** above it and in its editor title',
    '**cel-lens: Use This Example as the Type** in the Command Palette, on that editor only',
    "writes it as the value's `schema:`, and `.cellens` opens beside the rule at the entry",
    'shows once as an information problem at its key in `.cellens`',
    'Using a pasted example closes its editor; closing it writes nothing, even while the engine works on it, and a window reload closes the examples of the window before',
    "A whole resource of the value's kind pasted for a value inside it, such as a ReplicaLimit for `params.spec.config`, gives the schema of that value, also from a YAML list of resources",
    'a resource of another kind, such as an object reference, is the value itself',
    "which the engine tells from the value's kinds and says in a note; when it does not know them, a paste with `apiVersion` and `kind` at its root is read as a resource",
    'is saved when the `.cellens` the line names had no unsaved edits and nothing but that write changed it; nothing is saved when the engine writes another `.cellens`',
    'An engine too old to work out a type from an example says so, whenever the example is used',
    'An example over 1 MiB, the most the engine reads, is refused before anything reads it',
    'A part whose type comes from `.cellens`, such as `variables.limit: int`, opens that entry beside the rule, selected',
  ])('names %s under Added in the Unreleased section', (text) => {
    expect(unreleasedPart('Added')).toContain(text);
  });

  it.each(['input box', 'Write a struct…', 'Pick a kind…', 'untitled', 'Use the whole'])(
    'names no %s, which no release had, in the Unreleased section',
    (text) => {
      expect(unreleasedPart('Added')).not.toContain(text);
    },
  );

  it.each([
    'Vite 8.3.7 replaces 8.3.2',
    'qs 6.17.1 replaces 6.16.0',
    '`@azure/msal-node` 7.2.1 (with `@azure/identity` 4.14.1) replaces 6.0.1',
    '`overrides` in `package.json` give `eslint`, `@eslint/config-array`, `@typescript-eslint/typescript-estree` and `@vscode/vsce` minimatch 11.0.5 in place of 10.2.6',
    'GHSA-587h-7wmj-h5pm',
    'GHSA-698v-4r3x-3vcv',
    'GHSA-9jrq-w75r-8gcw',
    'GHSA-r825-w8mm-5pxw',
    'GHSA-rq7h-c2jc-7f22',
    'GHSA-vfpm-58rq-9qcg',
    'GHSA-6pq8-jhm2-2vcw (qs)',
    'GHSA-xcx6-jx8p-x3h5 (`@azure/msal-node`)',
    'GHSA-g3hv-75wg-m249 (minimatch)',
    'One advisory stays, and does not apply',
    '`vscode-languageclient`, which is part of the extension, keeps minimatch 10.2.6, as it calls `Minimatch.makeRe()`, which minimatch 11 removed, and has no release without it',
    "a denial of service through a glob pattern an attacker chooses, and the client matches only the extension's own patterns and the ones the engine registers",
    '`osv-scanner.toml` ignores it for that copy until 2027-03-01',
    '`npm audit` still reports it, as 2 moderate vulnerabilities',
    'The front page of JairJosafath/cel-lens (`public-repo/`) gains a CodeQL workflow',
    "analyses the engine's Go, the extension's JavaScript and TypeScript, and the workflows on every push to `main`, on every pull request and every week",
    "a Dependabot configuration, which proposes updates every week for the engine's Go modules, the extension's npm packages and the GitHub Actions",
    'the minor and patch updates of each in one pull request and each major update in its own',
    'a `govulncheck` job in CI, with the Go version of `engine/go.mod`, which fails on a call into code with a known vulnerability that `engine/osv-scanner.toml` does not leave aside',
    'govulncheck has no ignore list, and an advisory without symbols marks every call into its module, so a script reads its JSON output against that file',
    "the bump is made in the maintainers' source, the next published release carries it, and the pull request is not merged on GitHub",
    'Its `SECURITY.md` says how the code and its dependencies are watched',
    'The files of each release of JairJosafath/cel-lens get a signature',
    'gains a `sign-release.yml` workflow, which runs when a release is published, and by hand with a `tag` for the releases already out',
    "signs each with Sigstore under the workflow's own identity, so there is no key to keep, verifies each signature it made, and uploads them beside the files, each named after its file with `.sigstore.json` after it",
    'A second run replaces the signatures and signs none of them, and a release without a file to sign fails the run',
    'Its `SECURITY.md` gains "Verifying a release": the `cosign verify-blob` command for a downloaded file',
    "the file is the one the repository's workflow saw when it signed it, not how the file was built",
    'Its README points there from Install',
  ])('names %s under Security in the Unreleased section', (text) => {
    expect(unreleasedPart('Security')).toContain(text);
  });

  it.each(
    sections.slice(0, sections.findIndex(({ version }) => version === OLDEST_PUBLIC_VERSION) + 1),
  )('ends with one link reference for $version', ({ version }) => {
    const refs = CHANGELOG.slice(CHANGELOG.lastIndexOf('\n\n') + 2).split('\n');
    expect(refs.filter((line) => line.startsWith(`[${version}]: https://`))).toHaveLength(1);
  });

  it('links every public version in the vscode-v form and no older one', () => {
    const refs = CHANGELOG.slice(CHANGELOG.lastIndexOf('\n\n') + 2).split('\n');
    expect(
      linkProblems(
        sections.map(({ version }) => version),
        refs,
      ),
    ).toEqual([]);
  });

  it('names no issue number on any line', () => {
    expect(issueReferenceLines(CHANGELOG)).toEqual([]);
  });

  const GOOD_REFS = [
    `[Unreleased]: ${REPO}/compare/vscode-v0.15.0...HEAD`,
    `[0.15.0]: ${REPO}/compare/vscode-v0.14.0...vscode-v0.15.0`,
    `[${OLDEST_PUBLIC_VERSION}]: ${REPO}/releases/tag/vscode-v${OLDEST_PUBLIC_VERSION}`,
  ];
  const VERSIONS = ['Unreleased', '0.15.0', OLDEST_PUBLIC_VERSION, '0.13.1'];

  it.each([
    { name: 'the right links', refs: GOOD_REFS, problems: 0 },
    {
      name: 'a link in the v form',
      refs: [GOOD_REFS[0], `[0.15.0]: ${REPO}/compare/v0.14.0...v0.15.0`, GOOD_REFS[2]],
      problems: 1,
    },
    {
      name: 'a link that compares the wrong versions',
      refs: [
        GOOD_REFS[0],
        `[0.15.0]: ${REPO}/compare/vscode-v0.0.9...vscode-v0.15.0`,
        GOOD_REFS[2],
      ],
      problems: 1,
    },
    {
      name: 'a stale Unreleased link',
      refs: [`[Unreleased]: ${REPO}/compare/vscode-v0.14.0...HEAD`, GOOD_REFS[1], GOOD_REFS[2]],
      problems: 1,
    },
    {
      name: 'the oldest public version linked with a compare',
      refs: [
        GOOD_REFS[0],
        GOOD_REFS[1],
        `[0.14.0]: ${REPO}/compare/vscode-v0.13.1...vscode-v0.14.0`,
      ],
      problems: 1,
    },
    {
      name: 'a link on a version older than the oldest public one',
      refs: [...GOOD_REFS, `[0.13.1]: ${REPO}/compare/vscode-v0.13.0...vscode-v0.13.1`],
      problems: 1,
    },
    { name: 'a missing link', refs: [GOOD_REFS[0], GOOD_REFS[2]], problems: 1 },
  ])('link check: $name has $problems problems', ({ refs, problems }) => {
    expect(
      linkProblems(
        VERSIONS,
        refs.filter((ref) => ref !== undefined),
      ),
    ).toHaveLength(problems);
  });

  it.each([
    { name: 'a plain line', text: '- Fixes a thing.', found: 0 },
    { name: 'a reference at the end', text: '- Fixes a thing (#63).', found: 1 },
    { name: 'two references', text: '- Fixes a thing (#63, #64).', found: 1 },
    { name: 'a bare reference', text: 'The change for #65 is merged.', found: 1 },
    { name: 'a heading', text: '## [0.1.0] - 2026-01-01\n### Added', found: 0 },
  ])('issue check: $name finds $found lines', ({ text, found }) => {
    expect(issueReferenceLines(text)).toHaveLength(found);
  });
});
