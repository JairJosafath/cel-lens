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

  it('has an Unreleased section, empty, and a released version for every other section', () => {
    expect(sections[0]?.version).toBe('Unreleased');
    expect(sections[0]?.kinds).toEqual([]);
    expect(sections.length).toBeGreaterThan(1);
  });

  /** The section of `version` of the changelog, and the part of it under the heading of `kind`. */
  function sectionOf(version: string): string {
    return CHANGELOG.split(/^(?=## \[)/m).find((text) => text.startsWith(`## [${version}]`)) ?? '';
  }
  function partOf(version: string, kind: string): string | undefined {
    return sectionOf(version)
      .split(/^(?=### )/m)
      .find((part) => part.startsWith(`### ${kind}\n`));
  }

  const LATEST = '0.37.0';

  it('has the notes of the latest release right under Unreleased, with the date of its preparation, and the version of package.json', () => {
    expect(sectionOf(LATEST)).toMatch(/^## \[0\.37\.0\] - \d{4}-\d{2}-\d{2}\n/);
    expect(sections[1]?.version).toBe(LATEST);
    const version = (
      JSON.parse(readFileSync(`${__dirname}/../package.json`, 'utf8')) as { version: string }
    ).version;
    expect(version).toBe(LATEST);
  });

  it('lists the kinds of the latest release in order: Added, Changed', () => {
    expect(sections.find(({ version }) => version === LATEST)?.kinds).toEqual(['Added', 'Changed']);
  });

  it('says nothing twice in the notes of the latest release', () => {
    const entries = sectionOf(LATEST)
      .split('\n')
      .filter((line) => line.startsWith('- '));
    expect(new Set(entries).size).toBe(entries.length);
    const sentences = entries.flatMap((entry) =>
      entry
        .slice(2)
        .split(/(?<=[.:;])\s+/)
        .filter((sentence) => sentence.length > 60),
    );
    expect(sentences.filter((sentence, at) => sentences.indexOf(sentence) !== at)).toEqual([]);
  });

  it('names the engine of the latest release once, and says it needs nothing set', () => {
    expect(engineVersions(sectionOf(LATEST))).toEqual(['v0.29.0']);
    expect(partOf(LATEST, 'Changed')).toContain(
      'The extension ships `cel-lens-core` v0.29.0, which has all of the above, with nothing to set in `cel-lens.serverPath`',
    );
  });

  it('describes nothing of example results as needing an engine that no release has, or as pictures still to take', () => {
    const notes = sectionOf(LATEST);
    expect(notes).not.toContain('does not yet');
    expect(notes).not.toContain('still to take');
    expect(notes).not.toContain('no engine release has');
  });

  it.each([
    "what each rule of a CRD's `x-kubernetes-validations` does to the example objects of the workspace",
    'There is no cluster and nothing to register',
    "at most 100 a CRD and never in the CRD's own file",
    '`invalid`, `bad`, `refused`, `rejected` or `denied`',
    '`# cel-lens: refused "part of the message"`',
    'another `# cel-lens:` comment there is an error naming the three forms',
    'The same kind, namespace and name twice in a file is an update from the first',
    'decoding, defaulting and validation of a custom resource',
    "what it refuses is an error on the example at the field, in the API server's words, unless the example expects to be refused",
    'whatever the experimental features',
    'take as long with 100 examples as with none, except that a request that arrives just as a check starts, in the first 25 ms or so after typing pauses, takes 1.5 to 4 ms longer than its usual 4.5 ms or so',
    'no admission policy is covered yet',
    '`3 examples pass`',
    '`refuses 1 example it should allow · 2 pass`',
    '`allows 1 example it should refuse`',
    '`1 example errors`',
    '`not run: needs an update example`',
    '`not run: CRD refused`',
    'A refused example that expects to be refused passes, and a rule without examples gets no part',
    'The part shortens first (`2 wrong · 3 pass`, then `2 wrong`)',
    "a picker titled after the rule's line",
    "each with its verdict, its file and line, and the API server's message",
    'says the results are being computed until the engine answers',
    'picking an example opens it at its line beside the rule',
    "never shows another rule's results",
    "The rule's hover gains a section listing each example",
    'at most 10 and then `and N more`',
    'A wrong verdict is a warning, source `cel-lens`, on the rule',
    "at the example's first key",
    'With the setting off, the part says so and offers a button that turns it on',
    'The setting `cel-lens.exampleResults` (on by default, resource scope)',
    '**cel-lens: Toggle Example Results**',
    '**Example results: on** or **off**',
    'open editors follow at once, without a reload',
    'with a picture of the line above a rule with its part and one of the picker it opens',
    'Dry runs stay under _Not supported yet_ for admission policies only',
  ])('names %s under Added in the notes of the latest release', (text) => {
    expect(partOf(LATEST, 'Added')).toContain(text);
  });

  it.each([
    'The engine checks CRDs for the same Kubernetes version at once, where it checked one at a time',
    'the check of an edited open CRD no longer waits for the background check of a CRD that is not open',
    'the worst wait for a change and a hover in a session is 12 to 23 ms, as without examples, where it was 21 to 48 ms',
    'has the engine work the results out again for the new version',
  ])('names %s under Changed in the notes of the latest release', (text) => {
    expect(partOf(LATEST, 'Changed')).toContain(text);
  });

  const RELEASE = '0.36.0';
  const release = sectionOf(RELEASE);

  /** The part of the section of the release under the heading of `kind`. */
  function releasePart(kind: string): string | undefined {
    return release.split(/^(?=### )/m).find((part) => part.startsWith(`### ${kind}\n`));
  }

  it('keeps the notes of 0.36.0 under its heading, with the date of its preparation, after those of 0.37.0', () => {
    expect(release).toMatch(/^## \[0\.36\.0\] - \d{4}-\d{2}-\d{2}\n/);
    expect(sections[2]?.version).toBe(RELEASE);
  });

  it('lists the kinds of the release in order: Added, Changed, Fixed, Security', () => {
    expect(sections.find(({ version }) => version === RELEASE)?.kinds).toEqual([
      'Added',
      'Changed',
      'Fixed',
      'Security',
    ]);
  });

  it('says nothing twice: no entry, and no sentence of some length, is repeated', () => {
    const entries = release.split('\n').filter((line) => line.startsWith('- '));
    expect(new Set(entries).size).toBe(entries.length);
    const sentences = entries.flatMap((entry) =>
      entry
        .slice(2)
        .split(/(?<=[.:;])\s+/)
        .filter((sentence) => sentence.length > 60),
    );
    expect(sentences.filter((sentence, at) => sentences.indexOf(sentence) !== at)).toEqual([]);
  });

  it('describes nothing added and then changed in this release as two things: the line above a rule and Give it a type are Added only', () => {
    expect(releasePart('Changed')).not.toContain('Give it a type');
    expect(releasePart('Changed')).not.toContain('inlineInfo');
    expect(releasePart('Fixed')).not.toContain('Give it a type');
  });

  it.each([
    '`cel-lens.inlineInfo`',
    '**cel-lens: Toggle Inline Info**',
    '**+N more**',
    '`self: Widget.spec · compiles`',
    "the problems peek at the rule's first problem",
    '`.cellens`, a file that says what a value is where Kubernetes gives it no type',
    '`go: ./api/v1#QuotaSpec`',
    '`kind: apps/v1 Deployment`',
    '**Give it a type**',
    '**Write a schema in .cellens…** (OpenAPI), **Use a Go type…** and **Write a short type in .cellens…** first',
    'open `.cellens` beside the rule',
    'in the editor group that already shows it',
    'each with its folder and doc comment',
    'A Go type or a kind is written at once',
    'has a **Back** button to it',
    'A value of a CRD is offered `int` and `string` only',
    '**Edit** opens the same picker on what the value has',
    '**Remove** removes the entry at once',
    '**Undo** there takes a change back',
    '**cel-lens: Give a Value a Type…**',
    '**Give it a type** can take an example',
    'Under **Examples in the workspace**',
    '`config/samples/limit.yaml` with `rules.example.com/v1 ReplicaLimit` and the line it starts at',
    '**Paste an example…** opens an editor beside the rule, named after the value (such as **Example of params.yaml**) and kept in memory, so closing it never asks to save',
    '**Use this example as the type** above it and in its editor title',
    '**cel-lens: Use This Example as the Type** in the Command Palette, on that editor only',
    'shows once as an information problem at its key in `.cellens`',
    'values that disagree are merged as Kubernetes accepts both, or left without a type with a note naming the field and the kinds',
    "A schema is checked as the API server checks a CRD's",
    'A Go type is read from the files of its package without building or running anything',
    'a window reload closes the examples of the window before',
    "A whole resource of the value's kind pasted for a value inside it",
    'a resource of another kind, such as an object reference, is the value itself',
    'An example over 1 MiB, the most the engine reads, is refused before anything reads it',
    'is saved when the `.cellens` the line names had no unsaved edits and nothing but that write changed it; nothing is saved when the engine writes another `.cellens`',
  ])('names %s under Added in the notes of 0.36.0', (text) => {
    expect(releasePart('Added')).toContain(text);
  });

  it.each(['input box', 'Write a struct…', 'Pick a kind…', 'untitled', 'Use the whole'])(
    'names no %s, which no release had, in the notes of 0.36.0',
    (text) => {
      expect(releasePart('Added')).not.toContain(text);
    },
  );

  it('tells nobody under Added to set cel-lens.serverPath, or that an engine is too old, to get Give it a type', () => {
    expect(releasePart('Added')).not.toContain('serverPath');
    expect(releasePart('Added')).not.toContain('too old');
  });

  it.each([
    'The extension ships `cel-lens-core` v0.28.0',
    'with nothing to set in `cel-lens.serverPath`',
    'types `object` and `oldObject` of a Kyverno ValidatingPolicy from a workspace CRD, with `kyverno` and `crd-typed-policies` on',
    'its hover says `crd-typed-policies` is off when a workspace CRD would type `object`',
    'every other picture is retaken with the v0.28.0 engine',
    'take the minor and patch updates Dependabot proposed',
    '`@vscode/vsce` 4.1.0, eslint 10.14.0, playwright 1.65.0, prettier 3.9.10, tinybench 6.2.3, typescript-eslint 8.72.1, vitest 5.1.0 and `vscode-languageserver-protocol` 3.18.5',
    '`@types/vscode` stays 1.100.0, the VS Code that `engines.vscode` asks for',
    'None of them is part of the extension',
  ])('names %s under Changed in the notes of 0.36.0', (text) => {
    expect(releasePart('Changed')).toContain(text);
  });

  it.each([
    '`object.spec.replicaz` for a Deployment',
    "says it is cel-lens's own check",
    'Hover said such a variable does not compile and nothing said why',
    'read only regular files of at most 1 MiB inside the workspace folder',
    'a symbolic link to `/dev/zero`, to a pipe, out of the folder or to a bigger file is skipped, where before it could hold the engine or fill its memory',
    "is published once, when the scan of that folder ends, with that folder's CRDs",
    'Repeated notifications of created folders keep the progress of the scan they start',
  ])('names %s under Fixed in the notes of 0.36.0', (text) => {
    expect(releasePart('Fixed')).toContain(text);
  });

  it.each([
    'The bundled engine is built with Go 1.26.11 and newer modules',
    'which is what a vulnerability scanner of the `.vsix` sees',
    'GO-2026-6599',
    'GO-2026-6866 in `golang.org/x/net`',
    'GO-2026-5932 in `golang.org/x/crypto`',
    "GO-2026-6505 in OpenTelemetry's SDK and trace exporters",
    'GO-2026-6865 in `github.com/klauspost/compress`',
    'GO-2025-3615, GO-2026-5337 and GO-2026-5594, do not apply',
    'what the engine reports does not change',
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
    'The files of a release can be checked',
    "signed with Sigstore by the repository's own `sign-release.yml` workflow, under the workflow's own identity, so there is no key to keep",
    'a signature made from a branch other than `main` is refused by the command',
    "a release is built and published by the repository's own `release.yml` workflow, and its files carry build provenance in `provenance.intoto.jsonl`",
    '"Verifying a release"',
    'the `cosign verify-blob` command for a signature',
    "the file is the one the repository's workflow saw when it signed it and not how it was built",
    'the `gh attestation verify` command for the provenance',
    'a run of `release.yml` built the file, and which run',
    "CodeQL analyses the engine's Go, the extension's JavaScript and TypeScript and the workflows on every push to `main`, every pull request and every week",
    "Dependabot proposes updates every week for the engine's Go modules, the extension's npm packages and the GitHub Actions",
    "its pull request is not merged on GitHub, as the bump is made in the maintainers' source and the next published release carries it",
    '`github/codeql-action` v4.39.3, `actions/checkout` v7.0.1, `actions/setup-go` v7.0.0 and `actions/setup-node` v7.1.0, each at the commit of its release',
    'a `govulncheck` job in CI fails on a call into code with a known vulnerability that `engine/osv-scanner.toml` does not leave aside',
  ])('names %s under Security in the notes of 0.36.0', (text) => {
    expect(releasePart('Security')).toContain(text);
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
