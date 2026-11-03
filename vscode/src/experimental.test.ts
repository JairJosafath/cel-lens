import { describe, expect, it } from 'vitest';
import { unknownExperimental, unknownExperimentalMessage } from './experimental';

const KNOWN = ['kyverno', 'crd-typed-policies', 'fitting-completion'];

describe('unknownExperimental', () => {
  it.each([
    ['an empty setting', [], KNOWN, []],
    ['only known names', ['kyverno', 'fitting-completion'], KNOWN, []],
    ['one unknown name', ['kyverno', 'kyvernoo'], KNOWN, ['kyvernoo']],
    ['several unknown names, in order', ['b', 'kyverno', 'a'], KNOWN, ['b', 'a']],
    ['a name with other casing', ['Kyverno'], KNOWN, ['Kyverno']],
    ['every name when the engine lists none', ['kyverno'], [], ['kyverno']],
  ])('for %s returns the unknown names', (_what, setting, known, expected) => {
    expect(unknownExperimental(setting, known)).toEqual(expected);
  });
});

describe('unknownExperimentalMessage', () => {
  it.each([
    [['a'], 'lists "a", which the engine does not know, so it is ignored. Remove or correct it'],
    [
      ['a', 'b'],
      'lists "a", "b", which the engine does not know, so they are ignored. Remove or correct them',
    ],
  ])('names %j and says what to do', (names, text) => {
    expect(unknownExperimentalMessage(names)).toContain(text);
  });
});
