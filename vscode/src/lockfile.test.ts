import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { describe, expect, it } from 'vitest';

const ROOT = `${__dirname}/..`;

function readJson(name: string): unknown {
  return JSON.parse(readFileSync(`${ROOT}/${name}`, 'utf8'));
}

function field(value: unknown, key: string): unknown {
  return typeof value === 'object' && value !== null && key in value
    ? Reflect.get(value, key)
    : undefined;
}

function lockRoot(lock: unknown): unknown {
  return field(field(lock, 'packages'), '');
}

/** The versions of `name` the lockfile holds, wherever a package nests it. */
function lockedVersions(lock: unknown, name: string): string[] {
  const packages = field(lock, 'packages');
  if (typeof packages !== 'object' || packages === null) {
    return [];
  }
  return Object.entries(packages).flatMap(([path, entry]) => {
    const version: unknown = field(entry, 'version');
    return path.endsWith(`node_modules/${name}`) && typeof version === 'string' ? [version] : [];
  });
}

/** The version of `name` a package at `path` of the lockfile gets: its own copy, or the nearest above. */
function versionFor(lock: unknown, path: string, name: string): string | undefined {
  const packages = field(lock, 'packages');
  let base = path;
  for (;;) {
    const version = field(
      field(packages, base === '' ? `node_modules/${name}` : `${base}/node_modules/${name}`),
      'version',
    );
    if (typeof version === 'string' || base === '') {
      return typeof version === 'string' ? version : undefined;
    }
    const above = base.lastIndexOf('/node_modules/');
    base = above === -1 ? '' : base.slice(0, above);
  }
}

/** Each package of the lockfile that depends on `name`, with the version of `name` it gets. */
function usersOf(lock: unknown, name: string): { user: string; version: string | undefined }[] {
  const packages = field(lock, 'packages');
  if (typeof packages !== 'object' || packages === null) {
    return [];
  }
  return Object.entries(packages)
    .filter(([, entry]) => field(field(entry, 'dependencies'), name) !== undefined)
    .map(([path]) => ({
      user: path.slice(path.lastIndexOf('node_modules/') + 'node_modules/'.length),
      version: versionFor(lock, path, name),
    }));
}

/** Negative when version `a` (x.y.z) is older than `b`, zero when they are the same. */
function compareVersions(a: string, b: string): number {
  const parts = (version: string): number[] => version.split('.').map(Number);
  const [left, right] = [parts(a), parts(b)];
  const differing = left.findIndex((part, index) => part !== right[index]);
  return differing === -1 ? 0 : (left[differing] ?? 0) - (right[differing] ?? 0);
}

interface IgnoredVuln {
  id: string;
  ignoreUntil: string;
  reason: string;
}

/** The entries of an osv-scanner.toml, as this repository writes them: a reason in `"""`, its lines joined by `\`. */
function ignoredVulns(text: string): IgnoredVuln[] {
  return text
    .split(/^\[\[IgnoredVulns\]\]$/m)
    .slice(1)
    .map((entry) => ({
      id: /^id = "([^"]+)"$/m.exec(entry)?.[1] ?? '',
      ignoreUntil: /^ignoreUntil = (\d{4}-\d{2}-\d{2})$/m.exec(entry)?.[1] ?? '',
      reason: (/^reason = """\\\n\s*([^]*?)\\\n\s*"""$/m.exec(entry)?.[1] ?? '').replace(
        /\\\n\s*/g,
        '',
      ),
    }));
}

// The version of each package that fixes its advisories. launch-editor is bundled into Vite, so
// the lockfile has no entry of its own for it.
const FIXES = [
  {
    name: 'vite',
    fixed: '8.3.3',
    advisories: [
      'GHSA-587h-7wmj-h5pm',
      'GHSA-698v-4r3x-3vcv',
      'GHSA-9jrq-w75r-8gcw',
      'GHSA-r825-w8mm-5pxw',
      'GHSA-rq7h-c2jc-7f22',
      'GHSA-vfpm-58rq-9qcg',
    ],
  },
  {
    name: 'launch-editor',
    fixed: '2.14.2',
    advisories: ['GHSA-587h-7wmj-h5pm', 'GHSA-698v-4r3x-3vcv', 'GHSA-r825-w8mm-5pxw'],
  },
  { name: 'qs', fixed: '6.16.1', advisories: ['GHSA-6pq8-jhm2-2vcw'] },
  { name: 'minimatch', fixed: '11.0.1', advisories: ['GHSA-g3hv-75wg-m249'] },
  { name: '@azure/msal-node', fixed: '7.1.0', advisories: ['GHSA-xcx6-jx8p-x3h5'] },
];
// The one package that keeps a version with an advisory, which osv-scanner.toml ignores.
const KEPT = { name: 'minimatch', user: 'vscode-languageclient' };
const IGNORED = ignoredVulns(readFileSync(`${ROOT}/osv-scanner.toml`, 'utf8'));

describe('package-lock.json', () => {
  it.each(['name', 'version', 'license'])(
    'records the same %s as package.json for the root package',
    (key) => {
      const expected = field(readJson('package.json'), key);
      expect(typeof expected).toBe('string');
      expect(field(lockRoot(readJson('package-lock.json')), key)).toBe(expected);
    },
  );

  it.each(FIXES.filter(({ name }) => name !== KEPT.name))(
    'holds no $name older than $fixed, the version that fixes its advisories',
    ({ name, fixed }) => {
      const older = lockedVersions(readJson('package-lock.json'), name).filter(
        (version) => compareVersions(version, fixed) < 0,
      );
      expect(older).toEqual([]);
    },
  );

  it('holds vite, qs, minimatch and @azure/msal-node, so the checks read them', () => {
    const lock = readJson('package-lock.json');
    const missing = ['vite', 'qs', 'minimatch', '@azure/msal-node'].filter(
      (name) => lockedVersions(lock, name).length === 0,
    );
    expect(missing).toEqual([]);
  });

  it('gives a minimatch older than 11.0.1, which fixes GHSA-g3hv-75wg-m249, to vscode-languageclient only', () => {
    const lock = readJson('package-lock.json');
    const users = usersOf(lock, KEPT.name);
    const older = users.filter(({ version }) => compareVersions(version ?? '0', '11.0.1') < 0);

    expect(users.map(({ user }) => user)).toContain(KEPT.user);
    expect(older.map(({ user }) => user)).toEqual([KEPT.user]);
    // No other old copy lies about: the lockfile's old versions are the one the client gets.
    expect(
      lockedVersions(lock, KEPT.name).filter((version) => compareVersions(version, '11.0.1') < 0),
    ).toEqual(older.map(({ version }) => version));
  });

  it('overrides minimatch with one exact version for each package that uses it, but vscode-languageclient', () => {
    const lock = readJson('package-lock.json');
    const overrides = field(readJson('package.json'), 'overrides');
    const others = usersOf(lock, KEPT.name).filter(({ user }) => user !== KEPT.user);

    expect(others.length).toBeGreaterThan(0);
    expect(
      Object.keys(typeof overrides === 'object' && overrides !== null ? overrides : {}),
    ).toEqual(others.map(({ user }) => user).sort());
    for (const { user, version } of others) {
      expect(version, user).toMatch(/^\d+\.\d+\.\d+$/);
      expect(field(overrides, user), user).toEqual({ minimatch: version });
    }
  });

  it('gives vscode-languageclient the minimatch it asks for, whose makeRe() and match() it calls', () => {
    // Resolved as the client's own files resolve it, which is also what esbuild bundles.
    const clientRequire = createRequire(`${ROOT}/node_modules/${KEPT.user}/package.json`);
    const asked = field(field(clientRequire('./package.json'), 'dependencies'), KEPT.name);
    const version = field(clientRequire(`${KEPT.name}/package.json`), 'version');
    const Minimatch = field(clientRequire(KEPT.name), 'Minimatch');
    const matcher: unknown =
      typeof Minimatch === 'function' ? Reflect.construct(Minimatch, ['**/*.yaml']) : undefined;
    const call = (method: string, ...args: unknown[]): unknown => {
      const found = field(matcher, method);
      return typeof found === 'function' ? Reflect.apply(found, matcher, args) : undefined;
    };

    expect(asked).toBe('^10.2.6');
    expect(version).toBe(
      versionFor(readJson('package-lock.json'), `node_modules/${KEPT.user}`, KEPT.name),
    );
    expect(String(version).split('.')[0]).toBe('10');
    expect(call('makeRe')).toBeInstanceOf(RegExp);
    expect(call('match', '/workspace/crds/widget.yaml')).toBe(true);
    expect(call('match', '/workspace/crds/widget.json')).toBe(false);
  });

  it.each([
    { name: 'the same version', a: '8.3.3', b: '8.3.3', sign: 0 },
    { name: 'an older patch', a: '8.3.2', b: '8.3.3', sign: -1 },
    { name: 'a newer patch of two digits', a: '6.16.10', b: '6.16.9', sign: 1 },
    { name: 'an older minor of fewer digits', a: '6.9.9', b: '6.16.1', sign: -1 },
    { name: 'a newer major', a: '11.0.0', b: '10.2.6', sign: 1 },
  ])('version check: $name compares as $sign', ({ a, b, sign }) => {
    expect(Math.sign(compareVersions(a, b))).toBe(sign);
  });

  it('version check: finds a nested copy of a package, and no package that only ends alike', () => {
    const lock = {
      packages: {
        '': { version: '1.0.0' },
        'node_modules/qs': { version: '6.17.1' },
        'node_modules/a/node_modules/qs': { version: '6.16.0' },
        'node_modules/not-qs': { version: '0.1.0' },
        'node_modules/@scope/qs': { version: '0.2.0' },
      },
    };
    expect(lockedVersions(lock, 'qs')).toEqual(['6.17.1', '6.16.0']);
  });

  it('version check: a package gets its own copy of a dependency, else the nearest one above it', () => {
    const lock = {
      packages: {
        'node_modules/qs': { version: '6.17.1' },
        'node_modules/a': { version: '1.0.0', dependencies: { qs: '^6.9.1' } },
        'node_modules/a/node_modules/qs': { version: '6.16.0' },
        'node_modules/a/node_modules/@scope/b': { version: '1.0.0', dependencies: { qs: '*' } },
        'node_modules/c': { version: '1.0.0', dependencies: { qs: '*' } },
        'node_modules/d': { version: '1.0.0', dependencies: { other: '*' } },
      },
    };
    expect(usersOf(lock, 'qs')).toEqual([
      { user: 'a', version: '6.16.0' },
      { user: '@scope/b', version: '6.16.0' },
      { user: 'c', version: '6.17.1' },
    ]);
    expect(usersOf(lock, 'other')).toEqual([{ user: 'd', version: undefined }]);
  });
});

describe('osv-scanner.toml', () => {
  it('ignores one advisory: the one of the minimatch vscode-languageclient keeps', () => {
    expect(IGNORED.map(({ id }) => id)).toEqual(['GHSA-g3hv-75wg-m249']);
  });

  it.each(IGNORED)(
    'says why $id does not apply, and when to look again',
    ({ ignoreUntil, reason }) => {
      expect(ignoreUntil).toMatch(/^\d{4}-\d{2}-\d{2}$/);
      expect(reason.length).toBeGreaterThan(200);
      expect(reason).not.toMatch(/\s{2}|\\|\n/);
    },
  );

  it.each(IGNORED)(
    'ignores $id for a package and version the lockfile still holds, older than the fix',
    ({ id, reason }) => {
      const fix = FIXES.find(({ advisories }) => advisories.includes(id));
      const [, name, version] = /^(\S+) (\d+\.\d+\.\d+) /.exec(reason) ?? [];

      expect(name).toBe(fix?.name);
      expect(lockedVersions(readJson('package-lock.json'), name ?? '')).toContain(version);
      expect(compareVersions(version ?? '', fix?.fixed ?? '')).toBeLessThan(0);
    },
  );

  it.each([
    'vscode-languageclient',
    'ask for minimatch ^10.2.6 and call Minimatch.makeRe(), which minimatch 11 removed',
    'a denial of service through a glob pattern an attacker chooses',
    'runs only with optimizationLevel 2 or more. The client never sets that option',
    'the ones the language server registers',
    'the engine the extension itself carries and starts',
    'once a vscode-languageclient release asks for minimatch 11.0.1 or newer',
  ])('gives the reason a reader can check: %s', (text) => {
    expect(IGNORED[0]?.reason).toContain(text);
  });

  it('entry check: reads the id, the date and a reason written over several lines', () => {
    const text = [
      '# A comment.',
      '[[IgnoredVulns]]',
      'id = "GHSA-aaaa-bbbb-cccc"',
      'ignoreUntil = 2027-01-31',
      'reason = """\\',
      '  left-pad 1.0.0 stays, \\',
      '  for a reason.\\',
      '  """',
      '',
      '[[IgnoredVulns]]',
      'id = "GHSA-dddd-eeee-ffff"',
      '',
    ].join('\n');

    expect(ignoredVulns(text)).toEqual([
      {
        id: 'GHSA-aaaa-bbbb-cccc',
        ignoreUntil: '2027-01-31',
        reason: 'left-pad 1.0.0 stays, for a reason.',
      },
      { id: 'GHSA-dddd-eeee-ffff', ignoreUntil: '', reason: '' },
    ]);
  });
});
