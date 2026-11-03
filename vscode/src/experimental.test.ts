import { describe, expect, it } from 'vitest';
import {
  closestKnownName,
  replaceExperimental,
  suggestions,
  unknownExperimental,
  unknownExperimentalMessage,
} from './experimental';

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
    expect(unknownExperimentalMessage(names, KNOWN)).toContain(text);
  });
});

describe('closestKnownName', () => {
  it.each([
    ['a letter too many', 'kyvernoo', 'kyverno'],
    ['a letter missing', 'kyvrno', 'kyverno'],
    ['another case', 'Kyverno', 'kyverno'],
    ['a swapped letter', 'fitting-completon', 'fitting-completion'],
    ['nothing close', 'zzzzzz', undefined],
    ['a short name one edit away from nothing known', 'ab', undefined],
    ['an empty name', '', undefined],
  ])('for %s returns the suggestion', (_what, name, expected) => {
    expect(closestKnownName(name, ['kyverno', 'crd-typed-policies', 'fitting-completion'])).toBe(
      expected,
    );
  });

  it('returns none when the engine lists nothing', () => {
    expect(closestKnownName('kyvernoo', [])).toBeUndefined();
  });
});

describe('unknownExperimentalMessage suggestions', () => {
  it.each([
    [['kyvernoo'], '"kyvernoo" (did you mean "kyverno"?)'],
    [['zzzzzz'], '"zzzzzz", which'],
  ])('for %j says %s', (names, text) => {
    expect(unknownExperimentalMessage(names, ['kyverno'])).toContain(text);
  });
});

describe('suggestions', () => {
  it.each([
    [['kyvernoo'], new Map([['kyvernoo', 'kyverno']])],
    [['kyvernoo', 'zzzzzz'], undefined],
    [[], undefined],
  ])('for %j gives %j', (names, expected) => {
    expect(suggestions(names, ['kyverno'])).toEqual(expected);
  });
});

describe('replaceExperimental', () => {
  const fixes = new Map([['kyvernoo', 'kyverno']]);

  it.each([
    [['kyvernoo'], ['kyverno']],
    [[' kyvernoo ', 'kyverno'], ['kyverno']],
    [
      ['a', 3, 'kyvernoo'],
      ['a', 3, 'kyverno'],
    ],
    [['a'], undefined],
    ['kyvernoo', undefined],
  ])('turns %j into %j', (value, expected) => {
    expect(replaceExperimental(value, fixes)).toEqual(expected);
  });
});
