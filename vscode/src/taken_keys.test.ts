import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import {
  EXTENSIONS,
  collectTakenKeys,
  extensionKeys,
  normalizeKey,
  parseDefaultKeybindings,
  type TakenKeys,
} from './taken_keys';

describe('normalizeKey', () => {
  it.each([
    ['ctrl+k l', ['ctrl+k', 'l']],
    ['Ctrl+K L', ['ctrl+k', 'l']],
    ['cmd+k l', ['meta+k', 'l']],
    ['shift+ctrl+r', ['ctrl+shift+r']],
    ['Alt+Shift+Ctrl+F2', ['ctrl+shift+alt+f2']],
    ['ctrl+k   ctrl+l', ['ctrl+k', 'ctrl+l']],
    ['win+e', ['meta+e']],
    ['ctrl++', ['ctrl++']],
    ['ctrl+shift++', ['ctrl+shift++']],
    ['escape escape', ['escape', 'escape']],
    ['ctrl+k', ['ctrl+k']],
    ['', []],
  ])('reads %j as %j', (key, chords) => {
    expect(normalizeKey(key)).toEqual(chords);
  });
});

describe('parseDefaultKeybindings', () => {
  const FILE = [
    '// Default Keybindings of Visual Studio Code 1.146.0 for Linux',
    '  ',
    '[',
    '  { "key": "ctrl+k ctrl+l",         "command": "editor.toggleFold",',
    '                                       "when": "editorTextFocus && foldingEnabled" },',
    '  { "key": "Shift+Escape",          "command": "closeFoo" },',
    '  { "key": "ctrl+x",                "command": "-removed.command" }',
    ']',
  ].join('\n');

  it('reads the version of the header, and each key as chords with its command and when', () => {
    expect(parseDefaultKeybindings(FILE)).toEqual({
      version: '1.146.0',
      keys: [
        {
          chords: ['ctrl+k', 'ctrl+l'],
          command: 'editor.toggleFold',
          when: 'editorTextFocus && foldingEnabled',
        },
        { chords: ['shift+escape'], command: 'closeFoo' },
      ],
    });
  });

  it('refuses a file that does not say which VS Code it is for', () => {
    expect(() => parseDefaultKeybindings('[]')).toThrow(/which VS Code/);
  });
});

describe('extensionKeys', () => {
  it('reads the version, and the keys of contributes.keybindings for each platform: mac over key on macOS, win and linux beside key on the others', () => {
    const manifest = {
      version: '1.2.3',
      contributes: {
        keybindings: [
          { command: 'a.one', key: 'ctrl+shift+r', mac: 'cmd+shift+r', when: 'x' },
          { command: 'a.two', key: 'ctrl+t', linux: 'alt+t' },
          { command: 'a.three', key: 'f5' },
          { command: '-a.removed', key: 'f6' },
        ],
      },
    };

    expect(extensionKeys(manifest)).toEqual({
      version: '1.2.3',
      pc: [
        { chords: ['ctrl+shift+r'], command: 'a.one', when: 'x' },
        { chords: ['ctrl+t'], command: 'a.two' },
        { chords: ['alt+t'], command: 'a.two' },
        { chords: ['f5'], command: 'a.three' },
      ],
      mac: [
        { chords: ['shift+meta+r'], command: 'a.one', when: 'x' },
        { chords: ['ctrl+t'], command: 'a.two' },
        { chords: ['f5'], command: 'a.three' },
      ],
    });
  });

  it('gives no keys to an extension that contributes none', () => {
    expect(extensionKeys({ version: '0.1.0', contributes: {} })).toEqual({
      version: '0.1.0',
      pc: [],
      mac: [],
    });
  });

  it.each([[null], [{ contributes: {} }]])('refuses %j', (manifest) => {
    expect(() => extensionKeys(manifest)).toThrow();
  });
});

describe('collectTakenKeys', () => {
  const VS_CODE = (platform: string): string =>
    `// Default Keybindings of Visual Studio Code 9.9.9 for ${platform}\n[{ "key": "ctrl+k ctrl+l", "command": "editor.toggleFold" }]`;

  it('reads VS Code for the three platforms, then each extension of the plan, in a fixed order, from the URLs it names', async () => {
    const asked: string[] = [];
    const collected = await collectTakenKeys((url) => {
      asked.push(url);
      return Promise.resolve(
        url.includes('vs-code-default-keybindings')
          ? VS_CODE(url.includes('macos') ? 'macOS' : 'Linux or Windows')
          : JSON.stringify({
              version: '4.5.6',
              contributes: { keybindings: [{ command: 'x.y', key: 'ctrl+9' }] },
            }),
      );
    });

    expect(collected.sources.map((source) => source.name)).toEqual([
      'VS Code (Linux)',
      'VS Code (Windows)',
      'VS Code (macOS)',
      ...EXTENSIONS.map((extension) => extension.name),
    ]);
    expect(asked[0]).toBe(
      'https://raw.githubusercontent.com/codebling/vs-code-default-keybindings/master/linux.keybindings.json',
    );
    expect(asked).toContain(
      'https://raw.githubusercontent.com/golang/vscode-go/HEAD/extension/package.json',
    );
    expect(collected.sources[0]).toMatchObject({ version: '9.9.9' });
    expect(collected.sources[0]?.mac).toEqual([]);
    expect(collected.sources[2]?.pc).toEqual([]);
    expect(collected.sources[3]).toMatchObject({ name: 'GitLens', version: '4.5.6' });
  });
});

describe('src/taken_keys.json', () => {
  const taken = JSON.parse(readFileSync('src/taken_keys.json', 'utf8')) as TakenKeys;

  it('has VS Code for the three platforms and every extension the plan names, each with a version and an origin', () => {
    expect(taken.sources.map((source) => source.name)).toEqual([
      'VS Code (Linux)',
      'VS Code (Windows)',
      'VS Code (macOS)',
      ...EXTENSIONS.map((extension) => extension.name),
    ]);
    for (const source of taken.sources) {
      expect(source.version).toMatch(/^\d/);
      expect(source.origin).toMatch(/^https:\/\/raw\.githubusercontent\.com\//);
    }
  });

  it('holds its keys normalised: lower case, modifiers in one order, chords split', () => {
    for (const source of taken.sources) {
      for (const key of [...source.pc, ...source.mac]) {
        expect(key.chords.length).toBeGreaterThan(0);
        expect(key.chords).toEqual(normalizeKey(key.chords.join(' ')));
      }
    }
  });

  it('holds the key VS Code uses for Toggle Fold on every platform', () => {
    const fold = (platform: 'pc' | 'mac'): string[] =>
      taken.sources
        .flatMap((source) => source[platform])
        .filter((key) => key.command === 'editor.toggleFold')
        .map((key) => key.chords.join(' '));

    expect(fold('pc')).toContain('ctrl+k ctrl+l');
    expect(fold('mac')).toContain('meta+k meta+l');
  });
});

describe('npm run keys', () => {
  const manifest = JSON.parse(readFileSync('package.json', 'utf8')) as {
    scripts: Record<string, string>;
  };

  it('runs the script, which needs the network', () => {
    expect(manifest.scripts['keys']).toBe('node src/taken_keys.ts');
  });

  it('is not part of make check, nor of npm test', () => {
    expect(readFileSync('Makefile', 'utf8')).not.toContain('keys');
    expect(manifest.scripts['test']).not.toContain('keys');
  });

  it('is described in CONTRIBUTING, with the file it writes', () => {
    const contributing = readFileSync('CONTRIBUTING.md', 'utf8');

    expect(contributing).toContain('npm run keys');
    expect(contributing).toContain('src/taken_keys.json');
  });
});
