import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { deflateRawSync } from 'node:zlib';

import {
  checkDist,
  checkVsix,
  manifestTarget,
  readVsix,
  verifyDist,
  type VsixContents,
} from './dist';

import { afterEach, beforeEach, describe, expect, it } from 'vitest';

function manifestFor(target: string): string {
  return `<PackageManifest><Metadata><Identity Language="en-US" Id="x" Version="0.2.4" TargetPlatform="${target}"/></Metadata></PackageManifest>`;
}

function contentsFor(target: string, binaries: readonly string[]): VsixContents {
  return {
    entries: ['extension.vsixmanifest', 'extension/package.json', ...binaries],
    manifest: manifestFor(target),
  };
}

/** A zip with the given entries, the manifest deflated and the rest stored. */
function zip(files: ReadonlyMap<string, string>): Buffer {
  const locals: Buffer[] = [];
  const centrals: Buffer[] = [];
  let offset = 0;
  for (const [name, text] of files) {
    const method = name === 'extension.vsixmanifest' ? 8 : 0;
    const data = method === 8 ? deflateRawSync(text) : Buffer.from(text);
    const nameBytes = Buffer.from(name);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(method, 8);
    local.writeUInt32LE(data.length, 18);
    local.writeUInt32LE(text.length, 22);
    local.writeUInt16LE(nameBytes.length, 26);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(method, 10);
    central.writeUInt32LE(data.length, 20);
    central.writeUInt32LE(text.length, 24);
    central.writeUInt16LE(nameBytes.length, 28);
    central.writeUInt32LE(offset, 42);
    locals.push(local, nameBytes, data);
    centrals.push(central, nameBytes);
    offset += local.length + nameBytes.length + data.length;
  }
  const directory = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(files.size, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}

describe('readVsix', () => {
  it('lists the entries and inflates the manifest', () => {
    const contents = readVsix(
      zip(
        new Map([
          ['extension.vsixmanifest', manifestFor('linux-x64')],
          ['extension/bin/cel-lens-core', 'binary'],
        ]),
      ),
    );
    expect(contents.entries).toEqual(['extension.vsixmanifest', 'extension/bin/cel-lens-core']);
    expect(manifestTarget(contents.manifest)).toBe('linux-x64');
  });

  it.each([
    ['not a zip', Buffer.from('plain text, not a zip file at all')],
    ['no manifest', zip(new Map([['extension/package.json', '{}']]))],
  ])('throws for %s', (_name, bytes) => {
    expect(() => readVsix(bytes)).toThrow();
  });
});

describe('checkVsix', () => {
  it.each([
    ['cel-lens-vscode-linux-x64-0.2.4.vsix', 'linux-x64', 'cel-lens-core'],
    ['cel-lens-vscode-darwin-arm64-0.2.4.vsix', 'darwin-arm64', 'cel-lens-core'],
    ['cel-lens-vscode-win32-x64-0.2.4.vsix', 'win32-x64', 'cel-lens-core.exe'],
    ['cel-lens-vscode-win32-arm64-0.2.4.vsix', 'win32-arm64', 'cel-lens-core.exe'],
  ])('accepts %s with its own binary and TargetPlatform', (file, target, binary) => {
    expect(checkVsix(file, contentsFor(target, [`extension/bin/${binary}`]))).toEqual([]);
  });

  it.each([
    ['a Windows binary in a Linux package', 'linux-x64', ['extension/bin/cel-lens-core.exe']],
    ['a Unix binary in a Windows package', 'win32-x64', ['extension/bin/cel-lens-core']],
    ['no binary', 'linux-x64', []],
    [
      'two binaries',
      'linux-x64',
      ['extension/bin/cel-lens-core', 'extension/bin/cel-lens-core.exe'],
    ],
    ['a binary outside bin/', 'linux-x64', ['extension/cel-lens-core']],
    ['an extra file in bin/', 'linux-x64', ['extension/bin/cel-lens-core', 'extension/bin/other']],
  ])('rejects %s', (_name, target, binaries) => {
    const file = `cel-lens-vscode-${target}-0.2.4.vsix`;
    expect(checkVsix(file, contentsFor(target, binaries))).not.toEqual([]);
  });

  it.each([
    ['another TargetPlatform', 'cel-lens-vscode-linux-x64-0.2.4.vsix', 'linux-arm64'],
    ['a missing TargetPlatform', 'cel-lens-vscode-linux-x64-0.2.4.vsix', ''],
  ])('rejects %s in the manifest', (_name, file, declared) => {
    const contents = {
      entries: ['extension/bin/cel-lens-core'],
      manifest: declared === '' ? '<Identity Id="x"/>' : manifestFor(declared),
    };
    expect(checkVsix(file, contents).join('\n')).toContain('TargetPlatform');
  });

  it('rejects a file name without a target', () => {
    expect(checkVsix('cel-lens-vscode-0.2.4.vsix', contentsFor('linux-x64', []))).not.toEqual([]);
  });
});

describe('checkDist', () => {
  const good = (target: string): [string, VsixContents] => [
    `cel-lens-vscode-${target}-0.2.4.vsix`,
    contentsFor(target, [`extension/bin/cel-lens-core${target.startsWith('win32') ? '.exe' : ''}`]),
  ];

  it('accepts one good file per expected target', () => {
    const files = new Map([good('linux-x64'), good('win32-x64')]);
    expect(checkDist(files, ['linux-x64', 'win32-x64'])).toEqual([]);
  });

  it('rejects a missing target and an empty folder', () => {
    expect(checkDist(new Map([good('linux-x64')]), ['linux-x64', 'win32-x64'])).toHaveLength(1);
    expect(checkDist(new Map(), ['linux-x64'])).toHaveLength(1);
  });
});

describe('verifyDist', () => {
  let directory: string;

  beforeEach(async () => {
    directory = await mkdtemp(join(tmpdir(), 'verify-dist-'));
  });

  afterEach(async () => {
    await rm(directory, { recursive: true, force: true });
  });

  function vsix(target: string, binary: string): Buffer {
    return zip(
      new Map([
        ['extension.vsixmanifest', manifestFor(target)],
        [`extension/bin/${binary}`, 'binary'],
      ]),
    );
  }

  it('resolves for good .vsix files and ignores other files, even broken ones', async () => {
    await writeFile(
      join(directory, 'cel-lens-vscode-linux-x64-0.2.4.vsix'),
      vsix('linux-x64', 'cel-lens-core'),
    );
    await writeFile(
      join(directory, 'cel-lens-vscode-win32-x64-0.2.4.vsix'),
      vsix('win32-x64', 'cel-lens-core.exe'),
    );
    await writeFile(join(directory, 'notes.txt'), 'not a zip');
    await writeFile(join(directory, 'cel-lens-vscode-linux-x64-0.2.4.vsix.sha256'), 'abc');
    await expect(verifyDist(directory, ['linux-x64', 'win32-x64'])).resolves.toBeUndefined();
  });

  it('rejects naming a .vsix that is not a zip file', async () => {
    await writeFile(
      join(directory, 'cel-lens-vscode-linux-x64-0.2.4.vsix'),
      vsix('linux-x64', 'cel-lens-core'),
    );
    await writeFile(join(directory, 'cel-lens-vscode-darwin-x64-0.2.4.vsix'), 'plain text');
    await expect(verifyDist(directory, ['linux-x64'])).rejects.toThrow(
      /^cel-lens-vscode-darwin-x64-0\.2\.4\.vsix: not a zip file/,
    );
  });

  it('rejects naming a .vsix whose manifest says another target', async () => {
    await writeFile(
      join(directory, 'cel-lens-vscode-linux-x64-0.2.4.vsix'),
      vsix('linux-arm64', 'cel-lens-core'),
    );
    await expect(verifyDist(directory, ['linux-x64'])).rejects.toThrow(
      /cel-lens-vscode-linux-x64-0\.2\.4\.vsix: the manifest TargetPlatform is linux-arm64/,
    );
  });

  it('rejects when an expected target has no .vsix', async () => {
    await writeFile(
      join(directory, 'cel-lens-vscode-linux-x64-0.2.4.vsix'),
      vsix('linux-x64', 'cel-lens-core'),
    );
    await expect(verifyDist(directory, ['linux-x64', 'darwin-arm64'])).rejects.toThrow(
      /0 \.vsix files for darwin-arm64/,
    );
  });

  it('rejects an empty folder naming each missing target', async () => {
    const result = verifyDist(directory, ['linux-x64', 'darwin-arm64']);
    await expect(result).rejects.toThrow(/0 \.vsix files for linux-x64/);
    await expect(result).rejects.toThrow(/0 \.vsix files for darwin-arm64/);
  });

  it('rejects a missing folder naming it and saying how to create it', async () => {
    const missing = join(directory, 'nope');
    await expect(verifyDist(missing, ['linux-x64'])).rejects.toThrow(
      `The folder ${missing} does not exist. Run "make dist" to create it.`,
    );
  });
});
