import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

import { afterEach, describe, expect, it } from 'vitest';

const SCRIPT = 'public-repo/.github/scripts/govulncheck-left.mjs';
// The published source has no public-repo/: the folder is kept out of it.
const HAS_SCRIPT = existsSync(SCRIPT);
const TOML_NAME = 'engine/osv-scanner.toml';

interface Check {
  lines: string[];
  exitCode: number;
}
interface Script {
  ignoredIds: (toml: string) => string[];
  parseStream: (text: string) => unknown[];
  check: (toml: string, output: string, tomlName: string) => Check;
}

// Loaded by its path at run time, as the folder may be missing: plain JavaScript, typed here.
async function load(): Promise<Script> {
  return (await import(pathToFileURL(resolve(SCRIPT)).href)) as Script;
}

// What the engine's osv-scanner.toml holds: comments, then one table per advisory left aside.
const TOML = [
  '# The advisories the dependency check leaves aside.',
  '#   id = "GO-0000-0000" in a comment is no entry.',
  '',
  '[[IgnoredVulns]]',
  'id = "GO-2025-3615"',
  'reason = "github.com/kyverno/kyverno v1.19.1: the engine does not build pkg/engine/apicall."',
  '',
  '[[IgnoredVulns]]',
  'id = "GO-2026-5337"',
  'reason = "github.com/kyverno/kyverno v1.19.1: id = \\"GO-1111-1111\\" in a reason is no entry."',
  '',
  '[[IgnoredVulns]]',
  '  id="GO-2026-5594"',
  'reason = "github.com/kyverno/kyverno v1.19.1: the engine does not build pkg/engine/apicall."',
  '',
  '[[PackageOverrides]]',
  'name = "lib"',
  'id = "GO-2222-2222"',
  '',
].join('\n');

const CONFIG = {
  config: {
    protocol_version: 'v1.0.0',
    scanner_name: 'govulncheck',
    go_version: 'go1.26.6',
    scan_level: 'symbol',
  },
};
const PROGRESS = { progress: { message: 'Scanning your code and 512 packages {across} modules' } };

function osv(id: string, summary: string): unknown {
  return { osv: { schema_version: '1.3.1', id, summary, affected: [{ ranges: [] }] } };
}

interface Frame {
  module: string;
  version?: string;
  package?: string;
  function?: string;
}

function finding(id: string, first: Frame, fixed?: string): unknown {
  const caller = {
    module: 'github.com/JairJosafath/cel-lens/engine',
    package: 'github.com/JairJosafath/cel-lens/engine/internal/kyverno',
    function: 'Compile',
    position: { filename: 'internal/kyverno/kyverno.go', line: 120, column: 40 },
  };
  return {
    finding: {
      osv: id,
      ...(fixed === undefined ? {} : { fixed_version: fixed }),
      trace: first.function === undefined ? [first] : [first, caller],
    },
  };
}

const KYVERNO = { module: 'github.com/kyverno/kyverno', version: 'v1.19.1' };
const KYVERNO_CALL = {
  ...KYVERNO,
  package: 'github.com/kyverno/kyverno/pkg/cel/compiler',
  function: 'CompileValidation',
};
const KYVERNO_IDS = ['GO-2025-3615', 'GO-2026-5337', 'GO-2026-5594'];
// govulncheck reports an advisory at each level: its module, its package, then each call.
const KYVERNO_MESSAGES = [
  CONFIG,
  PROGRESS,
  ...KYVERNO_IDS.flatMap((id) => [
    osv(id, `Kyverno advisory ${id}`),
    finding(id, KYVERNO),
    finding(id, { ...KYVERNO, package: KYVERNO_CALL.package }),
    finding(id, KYVERNO_CALL),
  ]),
];

// As govulncheck writes with -format json: each object over several lines, one after the other.
function stream(messages: unknown[]): string {
  return messages.map((message) => `${JSON.stringify(message, null, 2)}\n`).join('');
}

let folder: string | undefined;

afterEach(() => {
  if (folder !== undefined) {
    rmSync(folder, { recursive: true, force: true });
    folder = undefined;
  }
});

describe.skipIf(!HAS_SCRIPT)('public-repo/.github/scripts/govulncheck-left.mjs', () => {
  it('passes on the three Kyverno advisories the code calls, which the toml leaves aside', async () => {
    const { check } = await load();

    expect(check(TOML, stream(KYVERNO_MESSAGES), TOML_NAME)).toEqual({
      lines: [
        ...KYVERNO_IDS.map(
          (id) =>
            `${id} in github.com/kyverno/kyverno@v1.19.1 (no fixed version), Kyverno advisory ${id}: left aside by engine/osv-scanner.toml`,
        ),
        'govulncheck: 3 advisories called, 3 left aside by engine/osv-scanner.toml.',
      ],
      exitCode: 0,
    });
  });

  it('fails on a fourth advisory the code calls, and names it with its module and fixed version', async () => {
    const { check } = await load();
    const net = {
      module: 'golang.org/x/net',
      version: 'v0.58.0',
      package: 'golang.org/x/net/http2',
    };
    const fourth = [
      osv('GO-2026-6603', 'Excessive memory use in golang.org/x/net/http2'),
      finding('GO-2026-6603', { ...net, function: 'ConfigureServer' }, 'v0.59.0'),
    ];

    const result = check(TOML, stream([...KYVERNO_MESSAGES, ...fourth]), TOML_NAME);

    expect(result.exitCode).toBe(1);
    expect(result.lines.slice(3)).toEqual([
      'GO-2026-6603 in golang.org/x/net@v0.58.0 (fixed in v0.59.0), Excessive memory use in golang.org/x/net/http2: NOT left aside',
      'govulncheck: 4 advisories called, 1 NOT left aside by engine/osv-scanner.toml: GO-2026-6603.',
    ]);
  });

  it('fails on a called advisory whose osv message was not seen, without a summary', async () => {
    const { check } = await load();
    const called = finding('GO-2026-7777', {
      module: 'stdlib',
      version: 'go1.26.6',
      function: 'Do',
    });

    expect(check(TOML, stream([CONFIG, called]), TOML_NAME)).toEqual({
      lines: [
        'GO-2026-7777 in stdlib@go1.26.6 (no fixed version): NOT left aside',
        'govulncheck: 1 advisory called, 1 NOT left aside by engine/osv-scanner.toml: GO-2026-7777.',
      ],
      exitCode: 1,
    });
  });

  it('passes on an advisory of a module that is imported and not called, as information only', async () => {
    const { check } = await load();
    const imported = [
      osv('GO-2026-9999', 'An advisory nothing calls'),
      finding('GO-2026-9999', { module: 'example.com/lib', version: 'v1.0.0' }, 'v1.0.1'),
      finding(
        'GO-2026-9999',
        { module: 'example.com/lib', version: 'v1.0.0', package: 'lib' },
        'v1.0.1',
      ),
    ];

    expect(check(TOML, stream([CONFIG, ...imported]), TOML_NAME)).toEqual({
      lines: [
        'GO-2026-9999 in example.com/lib@v1.0.0 (fixed in v1.0.1), An advisory nothing calls: imported, not called (information only)',
        'govulncheck: 0 advisories called, 0 left aside by engine/osv-scanner.toml.',
      ],
      exitCode: 0,
    });
  });

  it.each([
    { name: 'an empty file', output: '' },
    { name: 'white space only', output: ' \n\n' },
    { name: 'findings without a config message', output: stream(KYVERNO_MESSAGES.slice(1)) },
  ])('fails on $name: the output has no config message', async ({ output }) => {
    const { check } = await load();

    expect(check(TOML, output, TOML_NAME)).toEqual({
      lines: ['govulncheck: the output has no config message: it is empty or cut off.'],
      exitCode: 1,
    });
  });

  it.each([
    { name: 'cut off inside an object', output: stream(KYVERNO_MESSAGES).slice(0, -20) },
    { name: 'cut off inside a string', output: '{"config": {"go_version": "go1.2' },
    { name: 'closed once too often', output: `${stream([CONFIG])}}` },
    { name: 'not JSON between its braces', output: `${stream([CONFIG])}{config}` },
  ])('fails on an output $name, saying it cannot be read', async ({ output }) => {
    const { check } = await load();
    const result = check(TOML, output, TOML_NAME);

    expect(result.exitCode).toBe(1);
    expect(result.lines).toHaveLength(1);
    expect(result.lines[0]).toMatch(/^govulncheck: the output cannot be read: .+\.$/);
  });

  it('reads the ids of the IgnoredVulns tables, and none from a comment, a reason or another table', async () => {
    const { ignoredIds } = await load();

    expect(ignoredIds(TOML)).toEqual(KYVERNO_IDS);
    expect(ignoredIds('')).toEqual([]);
  });

  it('reads objects written one after the other over several lines, with braces and quotes inside strings', async () => {
    const { parseStream } = await load();
    const messages = [
      CONFIG,
      PROGRESS,
      { osv: { id: 'GO-1', summary: 'a } in "quotes", a { and a \\ and a \\" at the end\\' } },
      { finding: { osv: 'GO-1', trace: [{ module: 'm', position: { line: 1 } }, {}] } },
    ];

    expect(parseStream(stream(messages))).toEqual(messages);
    expect(parseStream(messages.map((message) => JSON.stringify(message)).join(''))).toEqual(
      messages,
    );
    expect(parseStream('')).toEqual([]);
  });

  it('run by node as the workflow runs it, prints the lines and exits with the code', () => {
    folder = mkdtempSync(join(tmpdir(), 'govulncheck-left-'));
    const toml = join(folder, 'osv-scanner.toml');
    const output = join(folder, 'govulncheck.json');
    writeFileSync(toml, TOML);
    writeFileSync(output, stream(KYVERNO_MESSAGES));
    const run = (...paths: string[]): { status: number | null; stdout: string } =>
      spawnSync(process.execPath, [SCRIPT, ...paths], { encoding: 'utf8' });

    const passed = run(toml, output);
    writeFileSync(toml, '');
    const failed = run(toml, output);

    expect(passed.status).toBe(0);
    expect(passed.stdout.trimEnd().split('\n')).toHaveLength(4);
    expect(passed.stdout).toContain(`3 advisories called, 3 left aside by ${toml}.\n`);
    expect(failed.status).toBe(1);
    expect(failed.stdout).toContain(`3 NOT left aside by ${toml}: ${KYVERNO_IDS.join(', ')}.\n`);
    expect(run(toml).status).toBe(2);
  });

  it('run without the toml, leaves nothing aside and says so; without the output, fails', () => {
    folder = mkdtempSync(join(tmpdir(), 'govulncheck-left-'));
    const toml = join(folder, 'osv-scanner.toml');
    const output = join(folder, 'govulncheck.json');
    writeFileSync(output, stream([CONFIG]));
    const run = (...paths: string[]): { status: number | null; stdout: string } =>
      spawnSync(process.execPath, [SCRIPT, ...paths], { encoding: 'utf8' });

    const nothingCalled = run(toml, output);
    writeFileSync(output, stream(KYVERNO_MESSAGES));
    const called = run(toml, output);
    const noOutput = run(toml, join(folder, 'missing.json'));

    expect(nothingCalled.status).toBe(0);
    expect(nothingCalled.stdout).toBe(
      `govulncheck: ${toml} does not exist, so no advisory is left aside.\n` +
        `govulncheck: 0 advisories called, 0 left aside by ${toml}.\n`,
    );
    expect(called.status).toBe(1);
    expect(called.stdout).toContain(`3 NOT left aside by ${toml}: ${KYVERNO_IDS.join(', ')}.\n`);
    expect(noOutput.status).toBe(1);
    expect(noOutput.stdout).toContain('missing.json does not exist: govulncheck wrote no output.');
  });
});
