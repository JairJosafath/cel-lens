import { describe, expect, it } from 'vitest';

import { forEachCelToken, tokenizeCel, type CelToken } from './cel_tokens';

const HEAD = 'x-kubernetes-validations:\n';

describe('tokenizeCel', () => {
  it('puts the tokens of a block scalar at their line and column', () => {
    const tokens = tokenizeCel(`${HEAD}  - rule: |\n      size(self.a)\n        < 3\n`);
    expect(tokens.map((token) => [token.kind, token.start, token.end])).toEqual([
      ['function', { line: 2, character: 6 }, { line: 2, character: 10 }],
      ['punctuation', { line: 2, character: 10 }, { line: 2, character: 11 }],
      ['variable', { line: 2, character: 11 }, { line: 2, character: 15 }],
      ['punctuation', { line: 2, character: 15 }, { line: 2, character: 16 }],
      ['field', { line: 2, character: 16 }, { line: 2, character: 17 }],
      ['punctuation', { line: 2, character: 17 }, { line: 2, character: 18 }],
      ['operator', { line: 3, character: 8 }, { line: 3, character: 9 }],
      ['number', { line: 3, character: 10 }, { line: 3, character: 11 }],
    ]);
  });

  it('puts the tokens after an escape in a double-quoted scalar at the right column', () => {
    const line = '  - rule: "self.a == \\"x\\" && \\u0041b"';
    const tokens = tokenizeCel(`${HEAD}${line}\n`);
    const spots = tokens.map((token) => [
      token.kind,
      line.slice(token.start.character, token.end.character),
    ]);
    expect(spots).toEqual([
      ['variable', 'self'],
      ['punctuation', '.'],
      ['field', 'a'],
      ['operator', '=='],
      ['string', '\\"x\\"'],
      ['operator', '&&'],
      ['variable', '\\u0041b'],
    ]);
    expect(tokens.every((token) => token.start.line === 1 && token.end.line === 1)).toBe(true);
  });

  it('counts columns in UTF-16 units', () => {
    const line = `  - rule: "'😀' + a"`;
    const tokens = tokenizeCel(`${HEAD}${line}\n`);
    expect(tokens.map((token) => token.start.character)).toEqual([11, 16, 18]);
  });

  it('gives no tokens to a plain message', () => {
    expect(tokenizeCel(`${HEAD}  - message: self.a < 3\n`)).toEqual([]);
  });

  it('finds and tokenizes the CEL of a CRD with many rules', () => {
    const rules = Array.from(
      { length: 500 },
      (_, i) => `  - rule: "self.f${String(i)} <= size(self.g) + 1"\n    message: m\n`,
    );
    const tokens = tokenizeCel(HEAD + rules.join(''));
    expect(tokens).toHaveLength(500 * 12);
  });
});

describe('forEachCelToken', () => {
  it('visits the tokens tokenizeCel returns, in the same order', () => {
    const text = `${HEAD}  - rule: "self.a == 'é😀' && size(self.b) > 0"\n  - rule: self.c\n`;
    const visited: unknown[] = [];
    forEachCelToken(text, (kind, start, end) => visited.push({ kind, start, end }));
    expect(visited).toEqual(tokenizeCel(text));
    expect(visited.length).toBeGreaterThan(0);
  });
});

describe('forEachCelToken with a window', () => {
  const rules = Array.from({ length: 1000 }, (_, i) => `  - rule: "self.f${String(i)} < 3"\n`);
  const text = HEAD + rules.join('');

  it.each([
    { name: 'the middle', window: { fromLine: 500, toLine: 509 }, first: 500, count: 10 },
    { name: 'the first line', window: { fromLine: 0, toLine: 1 }, first: 1, count: 1 },
    { name: 'past the end', window: { fromLine: 998, toLine: 5000 }, first: 998, count: 3 },
  ])('tokenizes only the rules that start in $name', ({ window, first, count }) => {
    const lines = new Set<number>();
    forEachCelToken(text, (_kind, start) => lines.add(start.line), window);
    expect([...lines].sort((a, b) => a - b)).toEqual(
      Array.from({ length: count }, (_, i) => first + i),
    );
  });

  it('gives the tokens the whole document gives for the same lines', () => {
    const windowed: unknown[] = [];
    forEachCelToken(text, (kind, start, end) => windowed.push({ kind, start, end }), {
      fromLine: 300,
      toLine: 320,
    });
    expect(windowed).toEqual(
      tokenizeCel(text).filter((token) => token.start.line >= 300 && token.start.line <= 320),
    );
  });
});

describe('forEachCelToken with a block scalar above the window', () => {
  const body = Array.from({ length: 300 }, (_, i) => `        self.f${String(i)} < 3 &&\n`);
  const text = `${HEAD}  - rule: |\n${body.join('')}        true\n  - rule: self.after\n`;
  const ruleLine = HEAD.split('\n').length - 1;
  const lastBodyLine = ruleLine + 301;

  it('paints the visible lines of a rule that starts above the window', () => {
    const lines = new Set<number>();
    forEachCelToken(text, (_kind, start) => lines.add(start.line), {
      fromLine: ruleLine + 200,
      toLine: ruleLine + 210,
    });
    expect(lines.has(ruleLine + 205)).toBe(true);
  });

  it('does not tokenize a rule that ends before the window', () => {
    const lines = new Set<number>();
    forEachCelToken(text, (_kind, start) => lines.add(start.line), {
      fromLine: lastBodyLine + 1,
      toLine: lastBodyLine + 1,
    });
    expect([...lines]).toEqual([lastBodyLine + 1]);
  });
});

describe.each([
  { kind: 'double-quoted', close: '"', rule: '  - rule: "' },
  { kind: 'plain', close: '', rule: '  - rule: ' },
])('forEachCelToken with a multi-line $kind rule above the window', ({ close, rule }) => {
  const body = Array.from({ length: 300 }, (_, i) => `        self.f${String(i)} < 3 &&\n`);
  const text = `${HEAD}${rule}self.first &&\n${body.join('')}        true${close}\n  - rule: self.after\n`;
  const ruleLine = HEAD.split('\n').length - 1;
  const lastBodyLine = ruleLine + 301;

  it('paints the visible lines of a rule that starts above the window', () => {
    const lines = new Set<number>();
    forEachCelToken(text, (_kind, start) => lines.add(start.line), {
      fromLine: ruleLine + 200,
      toLine: ruleLine + 210,
    });
    expect(lines.has(ruleLine + 205)).toBe(true);
  });

  it('does not tokenize a rule that ends before the window', () => {
    const lines = new Set<number>();
    forEachCelToken(text, (_kind, start) => lines.add(start.line), {
      fromLine: lastBodyLine + 1,
      toLine: lastBodyLine + 1,
    });
    expect([...lines]).toEqual([lastBodyLine + 1]);
  });
});

describe.each([
  { kind: 'double-quoted', rule: '"self.a"' },
  { kind: 'single-quoted', rule: "'self.a'" },
  { kind: 'plain', rule: 'self.a' },
  { kind: 'plain with a comment', rule: 'self.a # c' },
])('forEachCelToken with a $kind rule that ends before the window', ({ rule }) => {
  it('does not tokenize it when deeper lines follow', () => {
    const text = `x-kubernetes-validations:\n  - rule: ${rule}\n      # deeper\n      # deeper\n  - rule: self.b\n`;
    const lines = new Set<number>();
    forEachCelToken(text, (_kind, start) => lines.add(start.line), { fromLine: 3, toLine: 4 });
    expect([...lines]).toEqual([4]);
  });
});

describe.each([
  { kind: 'block', open: '|\n        self.first &&\n', close: '' },
  { kind: 'plain', open: 'self.first &&\n', close: '' },
  { kind: 'double-quoted', open: '"self.first &&\n', close: '"' },
  { kind: 'single-quoted', open: "'self.first &&\n", close: "'" },
])(
  'forEachCelToken with a multi-line $kind rule that starts above the window',
  ({ open, close }) => {
    const body = Array.from({ length: 100 }, (_, i) => `        self.f${String(i)} < 3 &&\n`);
    const text = `${HEAD}  - rule: ${open}${body.join('')}        true${close}\n  - rule: self.after\n`;
    const ruleLine = HEAD.split('\n').length - 1;

    it.each([
      { name: 'inside the rule', fromLine: ruleLine + 60, toLine: ruleLine + 70 },
      { name: 'from its second line to its end', fromLine: ruleLine + 1, toLine: ruleLine + 102 },
      { name: 'across its end', fromLine: ruleLine + 99, toLine: ruleLine + 104 },
    ])('gives the whole-document tokens on the lines of a window $name', (window) => {
      const onWindowLines = (token: CelToken): boolean =>
        token.start.line >= window.fromLine && token.start.line <= window.toLine;
      const windowed: CelToken[] = [];
      forEachCelToken(text, (kind, start, end) => windowed.push({ kind, start, end }), window);
      const expected = tokenizeCel(text).filter(onWindowLines);
      expect(expected.length).toBeGreaterThan(0);
      expect(windowed.filter(onWindowLines)).toEqual(expected);
    });
  },
);
