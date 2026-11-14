import { describe, expect, it } from 'vitest';

import { appliesInEditor, describeClash, findClashes, whenImplies } from './key_clash';
import type { TakenKey, TakenSource } from './taken_keys';

function source(name: string, pc: TakenKey[], mac: TakenKey[] = pc): TakenSource {
  return { name, version: '1', origin: 'https://example.com', pc, mac };
}

const FOLD = {
  chords: ['ctrl+k', 'ctrl+l'],
  command: 'editor.toggleFold',
  when: 'editorTextFocus',
};

describe('appliesInEditor', () => {
  it.each([
    [undefined, true],
    ['editorTextFocus && editorLangId == yaml', true],
    ['!terminalFocus', true],
    ['false', false],
    ['terminalFocus && terminalHasBeenCreated', false],
    ["focusedView == 'workbench.panel.repl.view'", false],
    ["accessibleViewIsShown && accessibleViewCurrentProviderId == 'terminal'", false],
  ])('says %j is %s', (when, expected) => {
    expect(appliesInEditor(when)).toBe(expected);
  });
});

describe('findClashes', () => {
  const ours = { command: 'cel-lens.x', key: 'ctrl+k l', mac: 'cmd+k l', when: 'editorTextFocus' };

  it('finds the same key', () => {
    const taken = source('A', [{ chords: ['ctrl+k', 'l'], command: 'a.one' }]);

    expect(findClashes(ours, [taken])).toMatchObject([
      { source: 'A', platform: 'pc', key: 'ctrl+k l', taken: 'ctrl+k l', how: 'same key' },
    ]);
  });

  it('finds a taken single key that is the first part of the chord, which the chord could never start from', () => {
    const taken = source('Vim', [{ chords: ['ctrl+k'], command: 'extension.vim_ctrl+k' }]);

    expect(findClashes(ours, [taken])).toMatchObject([
      { source: 'Vim', platform: 'pc', how: 'takes its first part' },
    ]);
  });

  it('finds a single key that starts a taken chord', () => {
    expect(
      findClashes({ command: 'cel-lens.x', key: 'ctrl+k' }, [source('VS Code', [FOLD])]),
    ).toMatchObject([
      { platform: 'pc', taken: 'ctrl+k ctrl+l', how: 'starts a taken chord' },
      { platform: 'mac', taken: 'ctrl+k ctrl+l', how: 'starts a taken chord' },
    ]);
  });

  it('finds nothing for a key that shares only a part, or a different modifier order spelling of another key', () => {
    const taken = source('A', [
      { chords: ['ctrl+k', 'ctrl+l'], command: 'a.one' },
      { chords: ['l'], command: 'a.two' },
      { chords: ['ctrl+shift+k'], command: 'a.three' },
    ]);

    expect(findClashes(ours, [taken])).toEqual([]);
  });

  it('compares the mac key on macOS and key on the other platforms, each against its own list', () => {
    const taken = source(
      'A',
      [{ chords: ['ctrl+k', 'l'], command: 'pc.only' }],
      [{ chords: ['meta+k', 'l'], command: 'mac.only' }],
    );

    expect(findClashes(ours, [taken]).map((clash) => [clash.platform, clash.command])).toEqual([
      ['pc', 'pc.only'],
      ['mac', 'mac.only'],
    ]);
  });

  it('uses key on macOS when mac is not given', () => {
    const taken = source('A', [], [{ chords: ['ctrl+k', 'l'], command: 'mac.only' }]);

    expect(findClashes({ command: 'cel-lens.x', key: 'ctrl+k l' }, [taken])).toMatchObject([
      { platform: 'mac' },
    ]);
  });

  it.each([
    ['when is false', 'false'],
    ['it applies only with terminalFocus', 'terminalFocus && terminalHasBeenCreated'],
    ['it applies only in a focusedView', "focusedView == 'workbench.panel.repl.view'"],
  ])('skips a taken key whose %s', (_name, when) => {
    const taken = source('VS Code', [{ chords: ['ctrl+k'], command: 'a.one', when }]);

    expect(findClashes(ours, [taken])).toEqual([]);
  });

  it('skips a contributed binding that cannot apply in an editor', () => {
    const taken = source('A', [{ chords: ['ctrl+k', 'l'], command: 'a.one' }]);

    expect(findClashes({ ...ours, when: 'false' }, [taken])).toEqual([]);
    expect(findClashes({ ...ours, when: 'terminalFocus' }, [taken])).toEqual([]);
  });

  it('describes a clash by its command, the platform, the key, the source and what it binds', () => {
    const taken = source('Vim', [{ chords: ['ctrl+k'], command: 'extension.vim_ctrl+k' }]);
    const [clash] = findClashes(ours, [taken]);

    expect(clash === undefined ? '' : describeClash(ours, clash)).toBe(
      'cel-lens.x (Windows and Linux: ctrl+k l) takes its first part: Vim binds ctrl+k to extension.vim_ctrl+k',
    );
  });
});

describe('whenImplies', () => {
  it.each([
    ['editorTextFocus && editorLangId == yaml', 'editorLangId == yaml', true],
    ['editorLangId == yaml', 'editorLangId == yaml', true],
    ['editorTextFocus', 'editorLangId == yaml', false],
    [undefined, 'editorLangId == yaml', false],
    [undefined, 'true', true],
    ['editorTextFocus', 'true', true],
    ['editorLangId == yaml || editorLangId == json', 'editorLangId == yaml', false],
    ['editorLangId == yaml', 'editorLangId == yaml || editorLangId == json', false],
    [
      'editorLangId == yaml && cel-lens.cellensTypes',
      'editorLangId == yaml && cel-lens.cellensTypes',
      true,
    ],
  ])('says %j implies %j: %s', (binding, palette, expected) => {
    expect(whenImplies(binding, palette)).toBe(expected);
  });
});
