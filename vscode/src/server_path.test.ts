import { describe, expect, it } from 'vitest';

import { bundledBinaryPath, resolveServer, SERVER_ARGS } from './server_path';

/** An `isFile` that knows exactly these files. */
function filesOf(...files: string[]): (path: string) => Promise<boolean> {
  return (path) => Promise.resolve(files.includes(path));
}

describe('bundledBinaryPath', () => {
  it.each([
    ['linux', '/ext/cel-lens', '/ext/cel-lens/bin/cel-lens-core'],
    ['darwin', '/ext/cel-lens', '/ext/cel-lens/bin/cel-lens-core'],
    ['win32', 'C:\\ext\\cel-lens', 'C:\\ext\\cel-lens\\bin\\cel-lens-core.exe'],
  ])('on %s the binary is %s', (platform, extensionPath, expected) => {
    expect(bundledBinaryPath(extensionPath, platform)).toBe(expected);
  });
});

describe('resolveServer', () => {
  it.each([
    {
      name: 'uses the bundled binary when the setting is empty',
      input: { setting: '', extensionPath: '/ext', platform: 'linux' },
      files: ['/ext/bin/cel-lens-core', '/usr/bin/cel-lens-core'],
      expected: { found: true, command: '/ext/bin/cel-lens-core', source: 'bundled' },
    },
    {
      name: 'treats a blank setting as unset',
      input: { setting: '  ', extensionPath: '/ext', platform: 'darwin' },
      files: ['/ext/bin/cel-lens-core'],
      expected: { found: true, command: '/ext/bin/cel-lens-core', source: 'bundled' },
    },
    {
      name: 'uses the bundled .exe on Windows',
      input: { setting: '', extensionPath: 'C:\\ext', platform: 'win32' },
      files: ['C:\\ext\\bin\\cel-lens-core.exe'],
      expected: { found: true, command: 'C:\\ext\\bin\\cel-lens-core.exe', source: 'bundled' },
    },
    {
      name: 'prefers the setting over the bundled binary',
      input: { setting: '/opt/core', extensionPath: '/ext', platform: 'linux' },
      files: ['/opt/core', '/ext/bin/cel-lens-core'],
      expected: { found: true, command: '/opt/core', source: 'setting' },
    },
    {
      name: 'trims the setting',
      input: { setting: '  /opt/core ', extensionPath: '/ext', platform: 'linux' },
      files: ['/opt/core'],
      expected: { found: true, command: '/opt/core', source: 'setting' },
    },
  ])('$name', async ({ input, files, expected }) => {
    const result = await resolveServer({ ...input, isFile: filesOf(...files) });

    expect(result).toEqual({ ...expected, args: SERVER_ARGS });
  });

  it('never looks on the PATH: a missing bundled binary is an error', async () => {
    const result = await resolveServer({
      setting: '',
      extensionPath: '/ext',
      platform: 'linux',
      isFile: filesOf('/usr/bin/cel-lens-core'),
    });

    expect(result).toMatchObject({ found: false, source: 'bundled' });
    expect(result.found ? '' : result.message).toContain('Reinstall the extension');
  });

  it('does not fall back to the bundled binary when the setting points nowhere', async () => {
    const result = await resolveServer({
      setting: '/missing',
      extensionPath: '/ext',
      platform: 'linux',
      isFile: filesOf('/ext/bin/cel-lens-core'),
    });

    expect(result).toMatchObject({ found: false, source: 'setting' });
    expect(result.found ? '' : result.message).toContain('engine file was not found: /missing.');
  });
});
