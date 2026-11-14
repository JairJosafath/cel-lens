import { describe, expect, it } from 'vitest';

import { contains, ruleAtCursor } from './rule_at_cursor';
import { parseRuleListAnswer, type ListedDocument } from './rule_list';
import { rawAnswer, rawDocument, rawRule } from './rule_list_samples';

const URI = 'file:///repo/crd.yaml';

function span(from: number, to: number, endCharacter = 30): Record<string, unknown> {
  return {
    start: { line: from, character: 0 },
    end: { line: to, character: endCharacter },
  };
}

/** A rule with the span of lines `from` to `to`. */
function rule(index: number, from: number, to: number): Record<string, unknown> {
  return { ...rawRule(URI, 'd', index), span: span(from, to) };
}

function documents(raw: Record<string, unknown>[]): readonly ListedDocument[] {
  return parseRuleListAnswer(rawAnswer(raw))?.documents ?? [];
}

/** A CRD of 0-based lines 0 to 40: its rules at lines 10-12 (a block scalar inside), 14-15 and 30-31. */
const CRD = documents([
  {
    ...rawDocument(URI, [rule(0, 10, 12), rule(1, 14, 15), rule(2, 30, 31)]),
    range: { start: { line: 0, character: 0 }, end: { line: 40, character: 0 } },
  },
]);

function idOf(target: ReturnType<typeof ruleAtCursor>): string | undefined {
  return target?.rule?.id.split('#').at(-1);
}

describe('contains', () => {
  const range = { start: { line: 2, character: 4 }, end: { line: 4, character: 8 } };

  it.each([
    [2, 4, true],
    [2, 3, false],
    [3, 0, true],
    [4, 8, true],
    [4, 9, false],
    [1, 99, false],
    [5, 0, false],
  ])('says line %i, character %i is %s', (line, character, expected) => {
    expect(contains(range, line, character)).toBe(expected);
  });
});

describe('the rule at the cursor', () => {
  it('finds a rule with the cursor on its key, the first line of its item', () => {
    expect(idOf(ruleAtCursor(CRD, 10, 4))).toBe('0');
    expect(idOf(ruleAtCursor(CRD, 14, 0))).toBe('1');
  });

  it('finds a rule with the cursor inside a block scalar, on any of its lines, and at the end of the item', () => {
    expect(idOf(ruleAtCursor(CRD, 11, 12))).toBe('0');
    expect(idOf(ruleAtCursor(CRD, 12, 30))).toBe('0');
  });

  it('finds the document, not a rule, with the cursor between two rules', () => {
    const target = ruleAtCursor(CRD, 13, 0);

    expect(target?.rule).toBeUndefined();
    expect(target?.document.name).toBe('widgets.example.com');
  });

  it('finds nothing with the cursor outside every document', () => {
    expect(ruleAtCursor(CRD, 41, 0)).toBeUndefined();
    expect(ruleAtCursor([], 3, 3)).toBeUndefined();
  });

  it('finds the document of a file of several documents, and the rule in the one that holds it', () => {
    const two = documents([
      {
        ...rawDocument(URI, [rule(0, 5, 6)], { name: 'first' }),
        range: { start: { line: 0, character: 0 }, end: { line: 19, character: 0 } },
      },
      {
        ...rawDocument(URI, [rule(0, 25, 27)], { name: 'second' }),
        range: { start: { line: 20, character: 0 }, end: { line: 40, character: 0 } },
      },
    ]);

    expect(ruleAtCursor(two, 6, 1)?.document.name).toBe('first');
    expect(ruleAtCursor(two, 26, 1)?.document.name).toBe('second');
    expect(ruleAtCursor(two, 26, 1)?.rule).toBeDefined();
    expect(ruleAtCursor(two, 22, 0)?.document.name).toBe('second');
    expect(ruleAtCursor(two, 22, 0)?.rule).toBeUndefined();
  });

  it('finds the inner of two spans that both hold the cursor', () => {
    const nested = documents([
      {
        ...rawDocument(URI, [rule(0, 10, 30), rule(1, 14, 16)]),
        range: { start: { line: 0, character: 0 }, end: { line: 40, character: 0 } },
      },
    ]);

    expect(idOf(ruleAtCursor(nested, 15, 2))).toBe('1');
    expect(idOf(ruleAtCursor(nested, 20, 2))).toBe('0');
  });

  it('answers from the lines the list has, after an edit the list does not have yet', () => {
    // A line typed above moved everything down by one: the cursor on what is now line 15, the key of
    // the rule the list has at 14, is still in the span of rule 1 (14-15) and picks it, and one
    // line further down, past the old document, picks nothing and changes nothing.
    expect(idOf(ruleAtCursor(CRD, 15, 0))).toBe('1');
    expect(ruleAtCursor(CRD, 41, 5)).toBeUndefined();
  });

  it('finds a rule that has no span by its document only', () => {
    const bare = documents([
      {
        ...rawDocument(URI, [{ ...rawRule(URI, 'd', 0), span: undefined }]),
        range: { start: { line: 0, character: 0 }, end: { line: 40, character: 0 } },
      },
    ]);
    const [first] = bare[0]?.rules ?? [];

    expect(first?.span).toBeUndefined();
    expect(ruleAtCursor(bare, 11, 20)?.rule).toBeUndefined();
    expect(ruleAtCursor(bare, 11, 20)?.document).toBeDefined();
  });

  it('finds, for the start of each rule’s location, the rule itself: a click on a rule does not make the view jump', () => {
    const many = documents([
      {
        ...rawDocument(
          URI,
          Array.from({ length: 50 }, (_, index) => rawRule(URI, 'd', index)),
        ),
        range: { start: { line: 0, character: 0 }, end: { line: 400, character: 0 } },
      },
    ]);

    for (const each of many[0]?.rules ?? []) {
      const { line, character } = each.location.range.start;
      expect(ruleAtCursor(many, line, character)?.rule?.id).toBe(each.id);
    }
  });
});
