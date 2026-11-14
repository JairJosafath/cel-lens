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

  const V42 = '0.42.0';
  const V41 = '0.41.0';
  const V40 = '0.40.0';
  const V39 = '0.39.0';
  const V38 = '0.38.0';

  it('has the notes of the latest release right under Unreleased, with the date of its preparation, and the version of package.json', () => {
    expect(sectionOf(V42)).toMatch(/^## \[0\.42\.0\] - \d{4}-\d{2}-\d{2}\n/);
    expect(sections[1]?.version).toBe(V42);
    const version = (
      JSON.parse(readFileSync(`${__dirname}/../package.json`, 'utf8')) as { version: string }
    ).version;
    expect(version).toBe(V42);
  });

  it('lists the kinds of the latest release in order: Added, Changed, Fixed', () => {
    expect(sections.find(({ version }) => version === V42)?.kinds).toEqual([
      'Added',
      'Changed',
      'Fixed',
    ]);
  });

  it('says nothing twice in the notes of the latest release', () => {
    const entries = sectionOf(V42)
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

  it('names no engine in the notes of the latest release: it carries the engine of 0.41.0', () => {
    expect(engineVersions(sectionOf(V42))).toEqual([]);
    expect(sectionOf(V42)).not.toMatch(/engine v\d|`cel-lens-core`/);
  });

  it('tells the latest release in the order a person meets it: get started, the toggles, the editor’s submenu, the shortcut, the palette, then themes', () => {
    const added = partOf(V42, 'Added') ?? '';
    const at = [
      '_Get started with cel-lens_ walkthrough',
      '**cel-lens: Toggle Colors** turns every color',
      '**cel-lens: Toggle Value Types**',
      'A _cel-lens_ submenu in the editor',
      'A shortcut for **cel-lens: Toggle Inline Info**',
      'The Command Palette has every command',
      'Light, dark and high contrast',
    ].map((text) => added.indexOf(text));
    expect(at.every((index) => index >= 0)).toBe(true);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    expect((partOf(V42, 'Changed') ?? '').split('\n- ')[1]).toMatch(/^The status bar menu/);
  });

  it.each([
    '**Pick Kubernetes Version** in a window with no folder or workspace file open',
    '`npm run engine` and `make dist` no longer fail on a download',
  ])('names %s under Fixed in the notes of the latest release', (text) => {
    expect(partOf(V42, 'Fixed')).toContain(text);
  });

  it('keeps the notes of 0.41.0 under its heading, with the date of its preparation, after those of 0.42.0', () => {
    expect(sectionOf(V41)).toMatch(/^## \[0\.41\.0\] - \d{4}-\d{2}-\d{2}\n/);
    expect(sections[2]?.version).toBe(V41);
  });

  it('lists the kinds of 0.41.0 in order: Added, Changed', () => {
    expect(sections.find(({ version }) => version === V41)?.kinds).toEqual(['Added', 'Changed']);
  });

  it('says nothing twice in the notes of 0.41.0', () => {
    const entries = sectionOf(V41)
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

  it('names the engine of 0.41.0 once, and says it needs nothing set', () => {
    expect(engineVersions(sectionOf(V41))).toEqual(['v0.33.0']);
    expect(partOf(V41, 'Changed')).toContain(
      'The extension ships `cel-lens-core` v0.33.0, which has all of the above, with nothing to set in `cel-lens.serverPath`',
    );
  });

  it('says in the notes of 0.41.0 what engine v0.33.0 brings a user: the line says not checked, and folders are followed', () => {
    expect(partOf(V41, 'Changed')).toContain(
      '`not checked: schema error` or `not checked: unreadable document`',
    );
    expect(partOf(V41, 'Added')).toContain(
      'follows a workspace folder added or removed without a restart',
    );
  });

  it.each([
    'A cel-lens view in the activity bar, with its own icon, lists every CEL rule of the workspace',
    '_Show Problems Only_',
    'The view follows the editor',
    '`cel-lens.ruleView.followEditor`',
    "An entry of the engine's list that the extension cannot read is no longer left out",
    'The README has a _Rule view_ section, with a picture, `images/rule-view.png`',
    "`v1beta1` CRD and Kyverno's kinds other than `ValidatingPolicy`",
  ])('names %s under Added in the notes of 0.41.0', (text) => {
    expect(partOf(V41, 'Added')).toContain(text);
  });

  it('says nothing of the view needing an engine that no release has', () => {
    expect(sectionOf(V41)).not.toContain('does not yet');
  });

  it('keeps the notes of 0.40.0 under its heading, with the date of its preparation, after those of 0.41.0 and 0.42.0', () => {
    expect(sectionOf(V40)).toMatch(/^## \[0\.40\.0\] - \d{4}-\d{2}-\d{2}\n/);
    expect(sections[3]?.version).toBe(V40);
  });

  it('lists the kinds of the 0.40.0 in order: Added, Changed', () => {
    expect(sections.find(({ version }) => version === V40)?.kinds).toEqual(['Added', 'Changed']);
  });

  it('says nothing twice in the notes of the 0.40.0', () => {
    const entries = sectionOf(V40)
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

  it('names the engine of the 0.40.0 once, and says it needs nothing set', () => {
    expect(engineVersions(sectionOf(V40))).toEqual(['v0.32.0']);
    expect(partOf(V40, 'Changed')).toContain(
      'The extension ships `cel-lens-core` v0.32.0, which has all of the above, with nothing to set in `cel-lens.serverPath`',
    );
  });

  it('says plainly under Changed that a workspace with a .cellens now needs cellens-types in cel-lens.experimental', () => {
    const changed = partOf(V40, 'Changed') ?? '';

    expect(changed).toContain(
      '**A workspace with a `.cellens` needs `cellens-types` in `cel-lens.experimental` from this version on.**',
    );
    expect(partOf(V40, 'Added')).toContain('Turn on cellens-types');
  });

  it.each([
    'the experimental feature `cellens-types`',
    '`images/cellens-types.gif`',
    '**cel-lens: Give a Value a Type…**',
    'cel-lens.experimental',
  ])('names %s under Added in the notes of the 0.40.0', (text) => {
    expect(partOf(V40, 'Added')).toContain(text);
  });

  it('keeps the notes of 0.39.0 under its heading, with the date of its preparation, after those of 0.40.0', () => {
    expect(sectionOf(V39)).toMatch(/^## \[0\.39\.0\] - \d{4}-\d{2}-\d{2}\n/);
    expect(sections[4]?.version).toBe(V39);
  });

  it('lists the kinds of 0.39.0 in order: Added, Changed, Fixed', () => {
    expect(sections.find(({ version }) => version === V39)?.kinds).toEqual([
      'Added',
      'Changed',
      'Fixed',
    ]);
  });

  it('says nothing twice in the notes of 0.39.0', () => {
    const entries = sectionOf(V39)
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

  it('names the engine of 0.39.0 once, and says it needs nothing set', () => {
    expect(engineVersions(sectionOf(V39))).toEqual(['v0.31.0']);
    expect(partOf(V39, 'Changed')).toContain(
      'The extension ships `cel-lens-core` v0.31.0, which has all of the above, with nothing to set in `cel-lens.serverPath`',
    );
  });

  it.each([
    '`cel-lens-core examples [--format text|json] [--kube-version <1.x>] [--fail-on wrong|error|none] [--verbose] [paths...]`',
    "on their examples, with the results the editor shows, and exits 1 when a verdict is not what its example expects, so a repository's examples are a test",
    'a file given alone is the workspace of its own, but for the folders given',
    '`12 rules, 40 examples: 2 wrong, 1 error, 3 not run`',
    'with nothing wrong that line is all it prints, and `--verbose` lists every result',
    '`--format json` prints one object',
    '`wrong` (the default) a verdict that is not what its example expects',
    '`error` also a rule that stopped with an error on an example that expects to be refused',
    'a rule that did not run never fails',
    'Exit code 0 means nothing failed, 1 that a verdict did, 2 a usage or read error, 130 an interrupted run',
    "not only the first 100 of a CRD or that a policy matches, which is the editor's cap",
    'files over 1 MiB and past the first 20000 are not read',
    'reports each CustomResourceDefinition, ValidatingAdmissionPolicy and ValidatingAdmissionPolicyBinding of the workspace that the API server cannot read',
    'a CI check passed on a policy the API server refuses',
    'It counts as wrong, so the default exits 1',
    'The editor says the same on such a file of the workspace that is not open',
    'The README says how to run the examples in CI',
    '`go install github.com/JairJosafath/cel-lens/engine@v0.31.0`',
  ])('names %s under Added in the notes of 0.39.0', (text) => {
    expect(partOf(V39, 'Added')).toContain(text);
  });

  it.each([
    'A plain `y`, `n`, `yes`, `no`, `on` or `off`',
    'is read as the boolean that kubectl and the API server read, not as a string',
    'a policy whose audit annotation `key` is `n` passed the check',
    'a string enum `[y, n]` in a CRD',
    'A field that wants a string is now an error at the word',
    'kubectl reads an unquoted n as one (YAML 1.1), so quote it if a string is meant',
    'a string enum with such an entry is a warning at the entry',
    'A quoted word, or one tagged `!!str`, is a string, as before',
    "The schema's problems on an example of a CRD that come of such a word name its line",
    'Not covered yet: a mapping key such as `on:` is still read as written',
    'A CustomResourceDefinition whose version says `served: yes` or `storage: on`',
    'and its examples run, which they did not, as the CRD was left out',
    'A CEL expression that is a plain `yes` is one problem',
  ])('names %s under Fixed in the notes of 0.39.0', (text) => {
    expect(partOf(V39, 'Fixed')).toContain(text);
  });

  const V37 = '0.37.0';

  it('keeps the notes of 0.38.0 under its heading, with the date of its preparation, after those of 0.39.0 and 0.40.0', () => {
    expect(sectionOf(V38)).toMatch(/^## \[0\.38\.0\] - \d{4}-\d{2}-\d{2}\n/);
    expect(sections[5]?.version).toBe(V38);
  });

  it('lists the kinds of 0.38.0 in order: Added, Changed, Fixed', () => {
    expect(sections.find(({ version }) => version === V38)?.kinds).toEqual([
      'Added',
      'Changed',
      'Fixed',
    ]);
  });

  it('says nothing twice in the notes of 0.38.0', () => {
    const entries = sectionOf(V38)
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

  it('names the engine of 0.38.0 once, says it needs nothing set, and the size it adds', () => {
    expect(engineVersions(sectionOf(V38))).toEqual(['v0.30.0']);
    const changed = partOf(V38, 'Changed') ?? '';
    expect(changed).toContain(
      'The extension ships `cel-lens-core` v0.30.0, which has all of the above, with nothing to set in `cel-lens.serverPath`',
    );
    expect(changed).toContain('The engine is about 7 MB bigger (52.6 MB to 59.7 MB)');
    expect(changed).toContain(
      'so the extension takes about 7 MB more on disk for each platform, and its `.vsix`, which is compressed, is 1.5 to 1.7 MB bigger to download',
    );
  });

  it('names the engine of 0.37.0 once', () => {
    expect(engineVersions(sectionOf(V37))).toEqual(['v0.29.0']);
    expect(partOf(V37, 'Changed')).toContain(
      'The extension ships `cel-lens-core` v0.29.0, which has all of the above, with nothing to set in `cel-lens.serverPath`',
    );
  });

  it("names the fix of the release workflow's sign job under Fixed in the notes of 0.38.0", () => {
    const fixed = partOf(V38, 'Fixed') ?? '';
    expect(fixed).toContain(
      'A release made by the `release.yml` workflow of JairJosafath/cel-lens no longer shows a failed run when it is complete and signed',
    );
    expect(fixed).toContain('24 reads 5 seconds apart');
  });

  it('describes nothing of example results as needing an engine that no release has, or as pictures still to take', () => {
    const notes = sectionOf(V37);
    expect(notes).not.toContain('does not yet');
    expect(notes).not.toContain('still to take');
    expect(notes).not.toContain('no engine release has');
  });

  it('describes nothing of the policies as needing an engine that has none, or as pictures still to take', () => {
    const notes = sectionOf(V38);
    expect(notes).not.toContain('does not yet');
    expect(notes).not.toContain('still to take');
  });

  it.each([
    "Example results for a ValidatingAdmissionPolicy, as for a CRD's rules",
    'what each validation, match condition and audit annotation of a policy does to the example objects of the workspace',
    "The verdict is the API server's own",
    'compiled as its ValidatingAdmissionPolicy plugin compiles it',
    "the plugin's own dispatcher and matcher admit each example as a request by no user in particular",
    '`matchConstraints`',
    "`excludeResourceRules`, `matchPolicy`, `namespaceSelector` against the workspace's Namespace documents, `objectSelector`",
    "of built-in kinds and of the workspace's CRDs, outside the policy's own file, at most 100",
    '`kubernetes.io/metadata.name`',
    'as an update from the last example of the same object that the API server would have stored',
    'Each example runs under each ValidatingAdmissionPolicyBinding of the workspace that binds the policy and matches it',
    'with the params its `paramRef` names or selects',
    '`parameterNotFoundAction` followed (`Deny` refuses the example, `Allow` runs nothing)',
    "A failed validation refuses the example under `Deny`, allows it with the API server's warning under `Warn` and records the audit annotation under `Audit`",
    "an audit annotation's value never refuses, but one that fails under `failurePolicy: Fail` refuses the example under any binding",
    "the message it refuses with may be another param's",
    'With no binding the policy runs as if bound with `Deny` and no params',
    '`warns 1 example`',
    '`audits 1 example`',
    '`excludes 1 example` (a match condition is false)',
    '`annotates 1 example`',
    '`under binding <name>` (`under 2 bindings`, `under no binding`)',
    'an example run under several bindings or params counts once, as the first of what they did',
    'The hover and the warnings name the binding and the param',
    'right when the schema, a rule of its CRD or any policy under any binding refuses it; a warning or an audit annotation refuses nothing',
    '`not run: excluded by matchConditions`',
    '`not run: no binding gives params`',
    '`not run: reads authorizer` (cel-lens runs no authorizer check)',
    '`not run: policy refused`',
    'takes about 40 ms from an empty cache and 6 ms with nothing changed',
    'lists each example under each binding and param, the wrong verdicts first',
    '`warned · allowed, with a warning`',
    '`excluded · a match condition is false`',
    '`big.yaml:1 · deny-limits`',
    "the param it ran with and the API server's answer: what it denied with, its warnings and its audit annotations",
    'Before, a result with one of these verdicts was left out of the picker',
    'with a picture of the picker of a policy',
    "_Not supported yet_ lists only what is still not covered: example results for a MutatingAdmissionPolicy and for Kyverno's policies",
    'gets no defaulting or validation of its own',
    "a Deployment's `spec.strategy`",
  ])('names %s under Added in the notes of 0.38.0', (text) => {
    expect(partOf(V38, 'Added')).toContain(text);
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
  ])('names %s under Added in the notes of 0.37.0', (text) => {
    expect(partOf(V37, 'Added')).toContain(text);
  });

  it.each([
    'The engine checks CRDs for the same Kubernetes version at once, where it checked one at a time',
    'the check of an edited open CRD no longer waits for the background check of a CRD that is not open',
    'the worst wait for a change and a hover in a session is 12 to 23 ms, as without examples, where it was 21 to 48 ms',
    'has the engine work the results out again for the new version',
  ])('names %s under Changed in the notes of 0.37.0', (text) => {
    expect(partOf(V37, 'Changed')).toContain(text);
  });

  const RELEASE = '0.36.0';
  const release = sectionOf(RELEASE);

  /** The part of the section of the release under the heading of `kind`. */
  function releasePart(kind: string): string | undefined {
    return release.split(/^(?=### )/m).find((part) => part.startsWith(`### ${kind}\n`));
  }

  it('keeps the notes of 0.36.0 under its heading, with the date of its preparation, after those of 0.37.0, 0.38.0, 0.39.0 and 0.40.0', () => {
    expect(release).toMatch(/^## \[0\.36\.0\] - \d{4}-\d{2}-\d{2}\n/);
    expect(sections[7]?.version).toBe(RELEASE);
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
