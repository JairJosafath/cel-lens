import { describe, expect, it } from 'vitest';

import type { ScopedValues } from './boolean_setting';
import { inlineInfoInForce, inlineInfoLabel } from './inline_info';

describe('inlineInfoInForce', () => {
  it.each<[string, ScopedValues | undefined, boolean]>([
    ['nothing inspected', undefined, true],
    ['no scope set', {}, true],
    ['the user off', { globalValue: false }, false],
    ['the user on', { globalValue: true }, true],
    ['the workspace off over the user on', { globalValue: true, workspaceValue: false }, false],
    [
      'the folder on over the workspace off',
      { workspaceValue: false, workspaceFolderValue: true },
      true,
    ],
    [
      'a value that is not a boolean, which is skipped',
      { workspaceValue: 'no', globalValue: false },
      false,
    ],
    ['only values that are not booleans', { globalValue: 0, workspaceValue: null }, true],
  ])('is %s: %s', (_name, values, expected) => {
    expect(inlineInfoInForce(values)).toBe(expected);
  });
});

describe('inlineInfoLabel', () => {
  it.each([
    [true, 'Inline info: on'],
    [false, 'Inline info: off'],
  ])('names the state %s as %j', (on, label) => {
    expect(inlineInfoLabel(on)).toBe(label);
  });
});
