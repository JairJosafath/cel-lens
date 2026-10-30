import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { access, mkdtemp, readFile, readdir, rm, stat, writeFile } from 'node:fs/promises';
import { createServer, type Server } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { promisify } from 'node:util';

import { CORE_RELEASES_ENV, releaseDownloadUrl } from './core_releases';
import { checkPinChecksums, describeFailure, parsePin, type CorePin } from './dist';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const run = promisify(execFile);

const TARGETS = {
  'linux-x64': 'core_linux_amd64',
  'linux-arm64': 'core_linux_arm64',
  'darwin-x64': 'core_darwin_amd64',
  'darwin-arm64': 'core_darwin_arm64',
  'win32-x64': 'core_windows_amd64.exe',
  'win32-arm64': 'core_windows_arm64.exe',
};

function sha256(text: string): string {
  return createHash('sha256').update(text).digest('hex');
}

describe('src/dist.ts fetch', { timeout: 60_000 }, () => {
  let server: Server;
  let baseUrl: string;
  let directory: string;
  /** What the stand-in release serves, by asset name. */
  let served: Map<string, string>;

  beforeEach(async () => {
    directory = await mkdtemp(join(tmpdir(), 'fetch-core-'));
    served = new Map(Object.values(TARGETS).map((name) => [name, `fake ${name}`]));
    served.set(
      'checksums.txt',
      Object.values(TARGETS)
        .map((name) => `${sha256(`fake ${name}`)}  ${name}\n`)
        .join(''),
    );
    server = createServer((request, response) => {
      const body = served.get((request.url ?? '').slice(1));
      response.statusCode = body === undefined ? 404 : 200;
      response.end(body);
    });
    await new Promise<void>((done) => {
      server.listen(0, '127.0.0.1', done);
    });
    const address = server.address();
    if (address === null || typeof address === 'string') {
      throw new Error('the stand-in server has no TCP address');
    }
    baseUrl = `http://127.0.0.1:${String(address.port)}`;
    const assets = Object.fromEntries(
      Object.entries(TARGETS).map(([target, name]) => [
        target,
        { name, sha256: sha256(`fake ${name}`) },
      ]),
    );
    await writeFile(
      join(directory, 'pin.json'),
      JSON.stringify({ version: 'v9.9.9', baseUrl, assets }),
    );
  });
  afterEach(async () => {
    await new Promise<void>((done) => {
      server.close(() => {
        done();
      });
    });
    await rm(directory, { recursive: true });
  });

  function fetchCore(): Promise<{ stdout: string; stderr: string }> {
    return run('node', [
      'src/dist.ts',
      'fetch',
      '--out',
      join(directory, 'out'),
      '--pin',
      join(directory, 'pin.json'),
    ]);
  }

  it('writes each verified binary into its own platform folder, executable', async () => {
    await fetchCore();

    expect((await readdir(join(directory, 'out'))).sort()).toEqual(Object.keys(TARGETS).sort());
    expect(await readFile(join(directory, 'out', 'linux-arm64', 'cel-lens-core'), 'utf8')).toBe(
      'fake core_linux_arm64',
    );
    expect(await readFile(join(directory, 'out', 'win32-x64', 'cel-lens-core.exe'), 'utf8')).toBe(
      'fake core_windows_amd64.exe',
    );
    expect((await stat(join(directory, 'out', 'darwin-x64', 'cel-lens-core'))).mode & 0o111).toBe(
      0o111,
    );
  });

  it('install puts this machine’s binary in the bin folder, executable, and keeps one already there', async () => {
    const bin = join(directory, 'bin');
    const install = (): Promise<{ stdout: string }> =>
      run('node', ['src/dist.ts', 'install', '--bin', bin, '--pin', join(directory, 'pin.json')]);

    await install();
    const names = await readdir(bin);
    const file = join(bin, names[0] ?? '');
    served.clear();
    const again = await install();

    expect(names).toHaveLength(1);
    expect(names[0]).toMatch(/^cel-lens-core(\.exe)?$/);
    expect(await readFile(file, 'utf8')).toMatch(/^fake core_/);
    expect((await stat(file)).mode & 0o111).toBe(0o111);
    expect(again.stdout).toContain('already there');
  });

  it('install logs where it fetched the engine from', async () => {
    const installed = await run('node', [
      'src/dist.ts',
      'install',
      '--bin',
      join(directory, 'bin'),
      '--pin',
      join(directory, 'pin.json'),
    ]);

    expect(installed.stdout).toContain(`Fetching cel-lens-core v9.9.9 from ${baseUrl}`);
  });

  it('install fails naming the missing checksums.txt and its URL', async () => {
    served.delete('checksums.txt');

    const failure = await run('node', [
      'src/dist.ts',
      'install',
      '--bin',
      join(directory, 'bin'),
      '--pin',
      join(directory, 'pin.json'),
    ]).then(
      () => undefined,
      (error: unknown) => error as { stderr: string },
    );

    expect(failure?.stderr).toContain(`Download of ${baseUrl}/checksums.txt failed: HTTP 404`);
  });

  it('fails naming the URL when the release cannot be reached at all', async () => {
    await new Promise<void>((done) => {
      server.close(() => {
        done();
      });
    });

    const failure = await fetchCore().then(
      () => undefined,
      (error: unknown) => error as { stderr: string },
    );

    expect(failure?.stderr).toContain(`Download of ${baseUrl}/checksums.txt failed:`);
    server = createServer();
  });

  it('states the error code when a refused dual-stack connection has no message', async () => {
    await new Promise<void>((done) => {
      server.close(() => {
        done();
      });
    });
    const dualStackUrl = baseUrl.replace('127.0.0.1', 'localhost');
    const pinFile = join(directory, 'pin.json');
    await writeFile(pinFile, (await readFile(pinFile, 'utf8')).replaceAll(baseUrl, dualStackUrl));

    const failure = await fetchCore().then(
      () => undefined,
      (error: unknown) => error as { stderr: string },
    );

    expect(failure?.stderr).toContain(`Download of ${dualStackUrl}/checksums.txt failed: `);
    expect(failure?.stderr).toContain('ECONNREFUSED');
    server = createServer();
  });

  it('install replaces a file in bin whose bytes are not the pinned ones', async () => {
    const bin = join(directory, 'bin');
    const install = (): Promise<{ stdout: string }> =>
      run('node', ['src/dist.ts', 'install', '--bin', bin, '--pin', join(directory, 'pin.json')]);
    await install();
    const file = join(bin, (await readdir(bin))[0] ?? '');
    await writeFile(file, 'junk');

    const again = await install();

    expect(await readFile(file, 'utf8')).toMatch(/^fake core_/);
    expect(again.stdout).toContain('Installed');
  });

  it('fails on a checksum mismatch, naming the asset, and writes nothing', async () => {
    served.set('core_darwin_arm64', 'tampered');

    const failure = await fetchCore().then(
      () => undefined,
      (error: unknown) => error as { stderr: string; code: number },
    );

    expect(failure?.code).toBe(1);
    expect(failure?.stderr).toContain('Checksum mismatch for core_darwin_arm64');
    await expect(access(join(directory, 'out'))).rejects.toThrow();
  });

  it('fails when checksums.txt differs from the pin for a non-host platform, writing nothing', async () => {
    served.set(
      'checksums.txt',
      (served.get('checksums.txt') ?? '').replace(
        sha256('fake core_windows_arm64.exe'),
        '0'.repeat(64),
      ),
    );

    const failure = await fetchCore().then(
      () => undefined,
      (error: unknown) => error as { stderr: string; code: number },
    );

    expect(failure?.code).toBe(1);
    expect(failure?.stderr).toContain('core_windows_arm64.exe: core-version.json pins');
    await expect(access(join(directory, 'out'))).rejects.toThrow();
  });

  it('fails when an asset cannot be downloaded, and writes nothing', async () => {
    served.delete('core_linux_amd64');
    served.delete('core_windows_arm64.exe');

    const failure = await fetchCore().then(
      () => undefined,
      (error: unknown) => error as { stderr: string },
    );

    expect(failure?.stderr).toContain('HTTP 404');
    await expect(access(join(directory, 'out'))).rejects.toThrow();
  });
});

describe('describeFailure', () => {
  it.each([
    [
      'the code of an AggregateError with an empty message',
      Object.assign(new AggregateError([]), { code: 'ECONNREFUSED' }),
      'ECONNREFUSED',
    ],
    ['the message of an error with one', new Error('connect failed'), 'connect failed'],
    ['the name of an error with neither message nor code', new TypeError(''), 'TypeError'],
    ['the text of a value that is not an Error', 'plain text', 'plain text'],
  ])('gives %s', (_name, error, expected) => {
    expect(describeFailure(error)).toBe(expected);
  });
});

describe('core-version.json', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('pins v0.21.0, takes its download URL from the public releases, and has a SHA-256 for each of the six platforms', async () => {
    const pin = JSON.parse(await readFile('core-version.json', 'utf8')) as {
      version: string;
      baseUrl?: string;
      assets: Record<string, { name: string; sha256: string }>;
    };

    vi.stubEnv(CORE_RELEASES_ENV, '');
    expect(pin.version).toBe('v0.21.0');
    expect(pin.baseUrl).toBeUndefined();
    expect(parsePin(pin).baseUrl).toBe(
      'https://github.com/JairJosafath/cel-lens/releases/download/core-v0.21.0',
    );
    expect(Object.keys(pin.assets).sort()).toEqual([
      'darwin-arm64',
      'darwin-x64',
      'linux-arm64',
      'linux-x64',
      'win32-arm64',
      'win32-x64',
    ]);
    for (const asset of Object.values(pin.assets)) {
      expect(asset.sha256).toMatch(/^[0-9a-f]{64}$/);
      expect(asset.sha256).not.toMatch(/^0+$/);
    }
  });
});

describe('checkPinChecksums', () => {
  const names = Object.entries(TARGETS);
  const pin: CorePin = {
    version: 'v9.9.9',
    baseUrl: 'http://example.invalid',
    assets: Object.fromEntries(
      names.map(([target, name]) => [target, { name, sha256: sha256(name) }]),
    ),
  };
  const lines = names.map(([, name]) => `${sha256(name)}  ${name}`);

  it.each([
    ['LF lines', lines.join('\n') + '\n'],
    ['CRLF lines', lines.join('\r\n') + '\r\n'],
    ['binary-mode asterisks', lines.map((line) => line.replace('  ', ' *')).join('\n')],
  ])('accepts a checksums.txt that matches all six assets (%s)', (_label, text) => {
    expect(() => {
      checkPinChecksums(pin, text);
    }).not.toThrow();
  });

  it.each(names.map(([target, name]) => [target, name]))(
    'fails when only the %s checksum differs',
    (_target, name) => {
      const text = lines
        .map((line) => (line.endsWith(name) ? `${'a'.repeat(64)}  ${name}` : line))
        .join('\n');

      expect(() => {
        checkPinChecksums(pin, text);
      }).toThrow(name);
    },
  );

  it('names every asset that is missing from checksums.txt', () => {
    expect(() => {
      checkPinChecksums(pin, lines.slice(2).join('\n'));
    }).toThrow(/core_linux_amd64 is not listed[\s\S]*core_linux_arm64 is not listed/);
  });
});

describe('releaseDownloadUrl', () => {
  it.each([
    [undefined, 'https://github.com/JairJosafath/cel-lens/releases/download/core-v0.21.0'],
    ['', 'https://github.com/JairJosafath/cel-lens/releases/download/core-v0.21.0'],
    [
      'https://ci.example/org/core/releases',
      'https://ci.example/org/core/releases/download/v0.21.0',
    ],
    [
      'https://ci.example/org/core/releases/',
      'https://ci.example/org/core/releases/download/v0.21.0',
    ],
  ])('with the override %j builds %s', (override, expected) => {
    expect(releaseDownloadUrl('v0.21.0', { [CORE_RELEASES_ENV]: override })).toBe(expected);
  });
});
