import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { describeClash, findClashes, type Clash, type Keybinding } from './key_clash';
import type { TakenKeys } from './taken_keys';

interface Manifest {
  contributes: { keybindings?: Keybinding[] };
}

const MANIFEST = JSON.parse(readFileSync('package.json', 'utf8')) as Manifest;
const TAKEN = JSON.parse(readFileSync('src/taken_keys.json', 'utf8')) as TakenKeys;
const KEYBINDINGS = MANIFEST.contributes.keybindings ?? [];

/**
 * The clashes the project accepts, each with why. Vim takes `Ctrl+K` in the editor while
 * `vim.use<C-k>` is on, as it does for every `Ctrl+K` chord of VS Code's own (`Ctrl+K Ctrl+L`
 * for one), so `Ctrl+K L` does not start there until a person gives the key back with
 * `vim.handleKeys`. It is not worked around: the key is the one the plan
 * of #3 chose, and no other layout-safe key is free.
 */
const ACCEPTED: readonly {
  source: string;
  platform: 'pc' | 'mac';
  taken: string;
  command: string;
  why: string;
}[] = [
  {
    source: 'Vim',
    platform: 'pc',
    taken: 'ctrl+k',
    command: 'extension.vim_ctrl+k',
    why: 'Vim takes Ctrl+K while vim.use<C-k>, for every Ctrl+K chord; vim.handleKeys gives it back',
  },
];

function accepted(clash: Clash): boolean {
  return ACCEPTED.some(
    (each) =>
      each.source === clash.source &&
      each.platform === clash.platform &&
      each.taken === clash.taken &&
      each.command === clash.command,
  );
}

/** The clashes of a binding that nobody accepted. */
function unaccepted(binding: Keybinding): string[] {
  return findClashes(binding, TAKEN.sources)
    .filter((clash) => !accepted(clash))
    .map((clash) => describeClash(binding, clash));
}

describe('the keybindings in package.json (#3)', () => {
  it.each(KEYBINDINGS.map((binding) => [binding.command, binding] as const))(
    'takes no key that VS Code or a listed extension uses: %s',
    (_command, binding) => {
      // Names the source, the key and its command, for each clash.
      expect(unaccepted(binding)).toEqual([]);
    },
  );

  it('fails on a key VS Code uses, ctrl+k ctrl+l, Toggle Fold (#3)', () => {
    const clashing = {
      command: 'cel-lens.fold',
      key: 'ctrl+k ctrl+l',
      mac: 'cmd+k cmd+l',
      when: 'editorTextFocus',
    };

    expect(unaccepted(clashing)).toContain(
      'cel-lens.fold (Windows and Linux: ctrl+k ctrl+l) same key: VS Code (Linux) binds ctrl+k ctrl+l to editor.toggleFold',
    );
    expect(unaccepted(clashing)).toContain(
      'cel-lens.fold (macOS: meta+k meta+l) same key: VS Code (macOS) binds meta+k meta+l to editor.toggleFold',
    );
  });

  it('fails on a key an extension uses: GitLens takes alt+, (#3)', () => {
    const gitlens = TAKEN.sources.find((source) => source.name === 'GitLens');
    const key = gitlens?.pc.find((each) => each.chords.length === 1);
    expect(key).toBeDefined();

    const found = unaccepted({ command: 'cel-lens.x', key: key?.chords[0] ?? '' });

    expect(found.some((line) => line.includes('GitLens'))).toBe(true);
  });

  it('fails on a single key that starts a chord VS Code uses (#3)', () => {
    expect(unaccepted({ command: 'cel-lens.x', key: 'ctrl+k' })).toContain(
      'cel-lens.x (Windows and Linux: ctrl+k) starts a taken chord: VS Code (Linux) binds ctrl+k ctrl+l to editor.toggleFold',
    );
  });

  it('accepts only the clash with Vim, and that clash is still there, so the exception is not stale', () => {
    const all = KEYBINDINGS.flatMap((binding) => findClashes(binding, TAKEN.sources));

    expect(all.filter(accepted).map((clash) => `${clash.source}: ${clash.taken}`)).toEqual([
      'Vim: ctrl+k',
    ]);
    expect(all.filter((clash) => !accepted(clash))).toEqual([]);
    for (const each of ACCEPTED) {
      expect(each.why).not.toBe('');
    }
  });

  it('does not clash with the keys of the macOS list on macOS, where Vim keeps ctrl+k and cel-lens uses cmd+k', () => {
    const binding = KEYBINDINGS[0];

    expect(
      binding === undefined
        ? []
        : findClashes(binding, TAKEN.sources).filter((clash) => clash.platform === 'mac'),
    ).toEqual([]);
  });
});
