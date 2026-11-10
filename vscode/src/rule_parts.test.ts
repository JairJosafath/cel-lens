import { describe, expect, it } from 'vitest';

import {
  isCellensLocation,
  listValuesArguments,
  moreValuesTitle,
  parseMoreValues,
  parseRuleLocation,
  ruleTarget,
  valueItems,
  type Problem,
  type ProblemOrder,
  type Range,
} from './rule_parts';

const ERROR = 0;
const WARNING = 1;
const HINT = 3;

function range(line: number, from: number, to: number): Range {
  return { start: { line, character: from }, end: { line, character: to } };
}

function problem(at: Range, severity: number, source = 'cel-lens'): Problem {
  return { range: at, severity, source };
}

/** The rule `self.replicas >= self.minReplicas` on line 16 (15 from 0), after `- rule: `. */
const RULE = range(15, 10, 44);

describe('parseRuleLocation', () => {
  const uri = 'file:///w/crd.yaml';

  it('reads the uri and range the engine sends', () => {
    expect(parseRuleLocation({ uri, range: RULE })).toEqual({ uri, range: RULE });
  });

  it.each([
    ['nothing', undefined],
    ['a string', uri],
    ['no uri', { range: RULE }],
    ['an empty uri', { uri: '', range: RULE }],
    ['no range', { uri }],
    ['a range without an end', { uri, range: { start: RULE.start } }],
    ['a negative line', { uri, range: range(-1, 0, 1) }],
    ['a fractional character', { uri, range: range(1, 0.5, 1) }],
    ['a line that is text', { uri, range: { start: { line: '1', character: 0 }, end: RULE.end } }],
  ])('gives undefined for %s', (_name, value) => {
    expect(parseRuleLocation(value)).toBeUndefined();
  });
});

describe('isCellensLocation', () => {
  it.each([
    ['a .cellens', 'file:///ws/.cellens', true],
    ['a .cellens below a folder', 'file:///ws/policies/.cellens', true],
    ['the rule of a YAML file', 'file:///ws/crd.yaml', false],
    ['a file whose name ends the same', 'file:///ws/a.cellens', false],
    ['a folder of that name', 'file:///ws/.cellens/crd.yaml', false],
  ])('reads %s as a location in a .cellens: %s (#16)', (_name, uri, expected) => {
    expect(isCellensLocation({ uri, range: RULE })).toBe(expected);
  });
});

describe('ruleTarget', () => {
  it.each<[string, Problem[], ProblemOrder, { kind: string; at: Range['start'] }]>([
    ['no problem: the hover at the rule', [], 'severity', { kind: 'hover', at: RULE.start }],
    [
      'only problems of another rule',
      [problem(range(17, 10, 12), ERROR)],
      'severity',
      { kind: 'hover', at: RULE.start },
    ],
    [
      "another extension's problem in the rule",
      [problem(range(15, 12, 14), ERROR, 'yaml')],
      'severity',
      { kind: 'hover', at: RULE.start },
    ],
    [
      'one problem: the peek at it',
      [problem(range(15, 15, 23), ERROR)],
      'severity',
      { kind: 'peek', at: { line: 15, character: 15 } },
    ],
    [
      'two errors: the peek at the first',
      [problem(range(15, 30, 44), ERROR), problem(range(15, 15, 23), ERROR)],
      'severity',
      { kind: 'peek', at: { line: 15, character: 15 } },
    ],
    [
      'a warning then an error, in severity order: the error first',
      [problem(range(15, 10, 14), WARNING), problem(range(15, 30, 44), ERROR)],
      'severity',
      { kind: 'peek', at: { line: 15, character: 30 } },
    ],
    [
      'a warning then an error, in position order: the warning first',
      [problem(range(15, 10, 14), WARNING), problem(range(15, 30, 44), ERROR)],
      'position',
      { kind: 'peek', at: { line: 15, character: 10 } },
    ],
    [
      'a warning, and an error later in the file that severity order reaches first: the hover',
      [problem(range(15, 10, 14), WARNING), problem(range(30, 10, 14), ERROR)],
      'severity',
      { kind: 'hover', at: { line: 15, character: 10 } },
    ],
    [
      'a warning, and an error later in the file, in position order: the peek',
      [problem(range(15, 10, 14), WARNING), problem(range(30, 10, 14), ERROR)],
      'position',
      { kind: 'peek', at: { line: 15, character: 10 } },
    ],
    [
      'a warning, and an error earlier in the file: the peek',
      [problem(range(15, 10, 14), WARNING), problem(range(3, 10, 14), ERROR)],
      'severity',
      { kind: 'peek', at: { line: 15, character: 10 } },
    ],
    [
      'only a hint, which the navigation skips: the hover at it',
      [problem(range(15, 20, 24), HINT)],
      'severity',
      { kind: 'hover', at: { line: 15, character: 20 } },
    ],
    [
      "an engine hint, another extension's warning, then an engine error, in position order: the peek at the error",
      [
        problem(range(15, 10, 14), HINT),
        problem(range(15, 20, 24), WARNING, 'yaml'),
        problem(range(15, 30, 34), ERROR),
      ],
      'position',
      { kind: 'peek', at: { line: 15, character: 30 } },
    ],
    [
      "an engine hint, another extension's warning, then an engine error, in severity order: the peek at the error",
      [
        problem(range(15, 10, 14), HINT),
        problem(range(15, 20, 24), WARNING, 'yaml'),
        problem(range(15, 30, 34), ERROR),
      ],
      'severity',
      { kind: 'peek', at: { line: 15, character: 30 } },
    ],
    [
      'a hint before a warning, in position order: the peek at the warning, not the hint',
      [
        problem(range(15, 10, 14), HINT),
        problem(range(15, 20, 24), WARNING),
        problem(range(30, 10, 14), ERROR),
      ],
      'position',
      { kind: 'peek', at: { line: 15, character: 20 } },
    ],
    [
      'a problem at the last character of the rule',
      [problem(range(15, 44, 44), ERROR)],
      'severity',
      { kind: 'peek', at: { line: 15, character: 44 } },
    ],
    [
      'a problem that starts after the rule',
      [problem(range(15, 45, 47), ERROR)],
      'severity',
      { kind: 'hover', at: RULE.start },
    ],
  ])('%s', (_name, problems, order, expected) => {
    expect(ruleTarget(RULE, problems, order)).toEqual(expected);
  });

  it('counts characters as UTF-16 code units, as LSP does, after an emoji in the rule', () => {
    // `"✓ 🙂" == self.a`: the emoji takes two code units, so `self` starts at 18.
    const rule = range(4, 10, 24);
    const found = ruleTarget(rule, [problem(range(4, 18, 22), ERROR)], 'severity');

    expect(found).toEqual({ kind: 'peek', at: { line: 4, character: 18 } });
  });
});

describe('listValuesArguments', () => {
  it.each([
    [15, [{ title: 'a' }], [{ line: 16, parts: [{ title: 'a' }] }]],
    [0, undefined, [{ line: 1, parts: [] }]],
    [15, [{ line: 16, parts: [{ title: 'a' }] }], [{ line: 16, parts: [{ title: 'a' }] }]],
  ])('turns the lens on line %j (from 0) and its arguments %j into %j', (line, args, expected) => {
    expect(listValuesArguments(line, args)).toEqual(expected);
  });
});

describe('parseMoreValues', () => {
  const give = {
    title: 'self.spec.port: dyn · Give it a type',
    command: 'cel-lens.giveType',
    arguments: [{ path: 'spec.port' }],
  };

  it('reads the line and the parts, with a missing arguments list as empty', () => {
    expect(
      parseMoreValues({ line: 16, parts: [give, { title: 'only words', command: '' }] }),
    ).toEqual({
      line: 16,
      parts: [give, { title: 'only words', command: '', arguments: [] }],
    });
  });

  it('drops the parts that are no Command', () => {
    expect(parseMoreValues({ line: 16, parts: [give, 3, { command: 'x' }] })?.parts).toEqual([
      give,
    ]);
  });

  it.each([
    ['nothing', undefined],
    ['the parts alone', [give]],
    ['no line', { parts: [give] }],
    ['a line that is text', { line: '16', parts: [give] }],
    ['no part', { line: 16, parts: [] }],
    ['parts that are not a list', { line: 16, parts: give }],
    ['a command that is not text', { line: 16, parts: [{ title: 'a', command: 1 }] }],
    ['arguments that are not a list', { line: 16, parts: [{ ...give, arguments: {} }] }],
  ])('gives undefined for %s', (_name, value) => {
    expect(parseMoreValues(value)).toBeUndefined();
  });
});

describe('moreValuesTitle', () => {
  it('names the line of the rule', () => {
    expect(moreValuesTitle(16)).toBe('More of the rule on line 16');
  });
});

describe('valueItems', () => {
  it.each([
    [
      'self.spec.port: dyn · Give it a type',
      { label: 'self.spec.port: dyn', description: 'Give it a type' },
    ],
    [
      'self.spec.config: {name, …} · Edit',
      { label: 'self.spec.config: {name, …}', description: 'Edit' },
    ],
    ['object.…valueFrom: dyn · Remove', { label: 'object.…valueFrom: dyn', description: 'Remove' }],
    ['a part without an action', { label: 'a part without an action', description: '' }],
    ['', { label: '', description: '' }],
  ])('shows %j with its value as the label and its action as the description', (title, item) => {
    const part = { title, command: 'cel-lens.giveType', arguments: [] };

    expect(valueItems([part])).toEqual([{ ...item, part }]);
  });
});
