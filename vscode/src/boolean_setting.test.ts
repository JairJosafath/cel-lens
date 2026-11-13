import { describe, expect, it } from 'vitest';

import { settingInForce, toggleScope, type ScopedValues } from './boolean_setting';

describe('settingInForce', () => {
  it.each<[string, ScopedValues | undefined, boolean, boolean]>([
    ['nothing inspected, on by default', undefined, true, true],
    ['nothing inspected, off by default', undefined, false, false],
    ['no scope set, off by default', {}, false, false],
    ['the user on over an off default', { globalValue: true }, false, true],
    [
      'the workspace off over the user on',
      { globalValue: true, workspaceValue: false },
      true,
      false,
    ],
    [
      'the folder on over the workspace off',
      { workspaceValue: false, workspaceFolderValue: true },
      true,
      true,
    ],
    [
      'a value that is not a boolean, which is skipped',
      { workspaceValue: 'no', globalValue: false },
      true,
      false,
    ],
    ['only values that are not booleans', { globalValue: 0, workspaceValue: null }, false, false],
  ])('is %s', (_name, values, fallback, expected) => {
    expect(settingInForce(values, fallback)).toBe(expected);
  });
});

describe('toggleScope', () => {
  it.each<[string, ScopedValues | undefined, string]>([
    ['nothing inspected', undefined, 'user'],
    ['no scope set', {}, 'user'],
    ['the user set', { globalValue: false }, 'user'],
    ['the workspace set', { globalValue: true, workspaceValue: false }, 'workspace'],
    ['the folder set', { workspaceValue: false, workspaceFolderValue: true }, 'folder'],
    ['the workspace set to a value that is not a boolean', { workspaceValue: 'off' }, 'user'],
  ])('writes the user, workspace or folder scope when %s: %s', (_name, values, expected) => {
    expect(toggleScope(values)).toBe(expected);
  });
});
