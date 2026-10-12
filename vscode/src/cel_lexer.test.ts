import { describe, expect, it } from 'vitest';

import { lexCel, type CelTokenKind } from './cel_lexer';

function lex(source: string): [CelTokenKind, string][] {
  return lexCel(source).map((lexeme) => [lexeme.kind, source.slice(lexeme.start, lexeme.end)]);
}

describe('lexCel', () => {
  it.each<[string, string, [CelTokenKind, string][]]>([
    [
      'fields and an operator',
      'self.spec.replicas <= self.spec.maxReplicas',
      [
        ['variable', 'self'],
        ['punctuation', '.'],
        ['field', 'spec'],
        ['punctuation', '.'],
        ['field', 'replicas'],
        ['operator', '<='],
        ['variable', 'self'],
        ['punctuation', '.'],
        ['field', 'spec'],
        ['punctuation', '.'],
        ['field', 'maxReplicas'],
      ],
    ],
    [
      'a macro with a method call and a string',
      "self.all(x, x.matches('^[a-z]+$'))",
      [
        ['variable', 'self'],
        ['punctuation', '.'],
        ['function', 'all'],
        ['punctuation', '('],
        ['variable', 'x'],
        ['punctuation', ','],
        ['variable', 'x'],
        ['punctuation', '.'],
        ['function', 'matches'],
        ['punctuation', '('],
        ['string', "'^[a-z]+$'"],
        ['punctuation', ')'],
        ['punctuation', ')'],
      ],
    ],
    [
      'a function and an unsigned number',
      'size(self.name) < 64u',
      [
        ['function', 'size'],
        ['punctuation', '('],
        ['variable', 'self'],
        ['punctuation', '.'],
        ['field', 'name'],
        ['punctuation', ')'],
        ['operator', '<'],
        ['number', '64u'],
      ],
    ],
    [
      'a triple-quoted string',
      'a == """x\n"y" """',
      [
        ['variable', 'a'],
        ['operator', '=='],
        ['string', '"""x\n"y" """'],
      ],
    ],
    ['a raw string', String.raw`r'\d+'`, [['string', String.raw`r'\d+'`]]],
    [
      'an escaped quote',
      String.raw`'it\'s' + b`,
      [
        ['string', String.raw`'it\'s'`],
        ['operator', '+'],
        ['variable', 'b'],
      ],
    ],
    [
      'keywords',
      'a in [true, false, null]',
      [
        ['variable', 'a'],
        ['keyword', 'in'],
        ['punctuation', '['],
        ['keyword', 'true'],
        ['punctuation', ','],
        ['keyword', 'false'],
        ['punctuation', ','],
        ['keyword', 'null'],
        ['punctuation', ']'],
      ],
    ],
    [
      'numbers',
      '1 1u 1.5 0x1F .5 1e3',
      [
        ['number', '1'],
        ['number', '1u'],
        ['number', '1.5'],
        ['number', '0x1F'],
        ['number', '.5'],
        ['number', '1e3'],
      ],
    ],
    [
      'operators',
      '!a && b || c ? d : e != f',
      [
        ['operator', '!'],
        ['variable', 'a'],
        ['operator', '&&'],
        ['variable', 'b'],
        ['operator', '||'],
        ['variable', 'c'],
        ['operator', '?'],
        ['variable', 'd'],
        ['operator', ':'],
        ['variable', 'e'],
        ['operator', '!='],
        ['variable', 'f'],
      ],
    ],
    [
      'has',
      'has(oldSelf.a)',
      [
        ['function', 'has'],
        ['punctuation', '('],
        ['variable', 'oldSelf'],
        ['punctuation', '.'],
        ['field', 'a'],
        ['punctuation', ')'],
      ],
    ],
  ])('tokenizes %s', (_name, source, expected) => {
    expect(lex(source)).toEqual(expected);
  });

  it.each<[string, string, [CelTokenKind, string][]]>([
    [
      'an unterminated string',
      "a == 'oops\n&& b",
      [
        ['variable', 'a'],
        ['operator', '=='],
        ['string', "'oops"],
        ['operator', '&&'],
        ['variable', 'b'],
      ],
    ],
    [
      'an unterminated triple-quoted string',
      'a + """oops',
      [
        ['variable', 'a'],
        ['operator', '+'],
        ['string', '"""oops'],
      ],
    ],
    ['a trailing backslash', "'a\\", [['string', "'a\\"]]],
    [
      'an unknown character',
      'a # b @ c',
      [
        ['variable', 'a'],
        ['variable', 'b'],
        ['variable', 'c'],
      ],
    ],
    [
      'non-ASCII text',
      "a == 'é😀' && b",
      [
        ['variable', 'a'],
        ['operator', '=='],
        ['string', "'é😀'"],
        ['operator', '&&'],
        ['variable', 'b'],
      ],
    ],
    ['an empty source', '', []],
  ])('never throws on %s', (_name, source, expected) => {
    expect(lex(source)).toEqual(expected);
  });
});
