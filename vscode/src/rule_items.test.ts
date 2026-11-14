import { describe, expect, it } from 'vitest';

import { parseRuleListAnswer, type ListedRule } from './rule_list';
import { rawAnswer, rawDocument, rawRule, type RawRuleOptions } from './rule_list_samples';
import {
  codeBlock,
  compileWords,
  coversExamples,
  documentAccessibleLabel,
  documentLabel,
  documentDescription,
  documentIcon,
  escapeMarkdown,
  kindWords,
  ruleAccessibleLabel,
  textInRange,
  ruleDescription,
  ruleIcon,
  rulePlace,
  ruleTooltip,
} from './rule_items';

const URI = 'file:///repo/config/crd.yaml';

function rule(options: RawRuleOptions = {}, extra: Record<string, unknown> = {}): ListedRule {
  const answer = parseRuleListAnswer(
    rawAnswer([rawDocument(URI, [{ ...rawRule(URI, 'd', 0, options), ...extra }])]),
  );
  const found = answer?.documents[0]?.rules[0];
  if (found === undefined) {
    throw new Error('the sample rule was not read');
  }
  return found;
}

const PASS = { state: 'pass', words: '3 examples pass' };
const WRONG = { state: 'wrong', words: 'refuses 1 example it should allow · 2 pass' };
const ERRORS = { state: 'error', words: '1 example errors' };
const NOT_RUN = { state: 'notRun', words: 'not run: needs an update example' };

describe('a rule’s icon, worst first', () => {
  it.each([
    ['a compile error', { compile: 'error' }, 'error', 'problemsErrorIcon.foreground'],
    [
      'a compile error over wrong examples',
      { compile: 'error', examples: WRONG },
      'error',
      'problemsErrorIcon.foreground',
    ],
    ['a compile warning', { compile: 'warning' }, 'warning', 'problemsWarningIcon.foreground'],
    ['wrong examples', { examples: WRONG }, 'warning', 'problemsWarningIcon.foreground'],
    ['an example that errors', { examples: ERRORS }, 'warning', 'problemsWarningIcon.foreground'],
    ['passing examples', { examples: PASS }, 'pass', 'testing.iconPassed'],
    ['a rule that compiles', {}, 'check', undefined],
    ['a rule that compiles and ran on no example', { examples: NOT_RUN }, 'check', undefined],
  ] as const)('is %s: %s', (_name, options, id, color) => {
    expect(ruleIcon(rule(options as RawRuleOptions), true)).toEqual(
      color === undefined ? { id } : { id, color },
    );
  });

  it('shows no example state for a folder that turns example results off', () => {
    expect(ruleIcon(rule({ examples: WRONG }), false)).toEqual({ id: 'check' });
    expect(ruleIcon(rule({ examples: PASS }), false)).toEqual({ id: 'check' });
    expect(ruleIcon(rule({ compile: 'error', examples: PASS }), false).id).toBe('error');
  });
});

describe('a rule’s description', () => {
  it.each([
    ['compiles', {}, 'compiles'],
    ['one problem', { compile: 'error', problems: 1 }, '1 problem'],
    ['several problems', { compile: 'warning', problems: 3 }, '3 problems'],
    ['the example words after a dot', { examples: PASS }, 'compiles · 3 examples pass'],
    [
      'problems and wrong examples',
      { compile: 'error', problems: 2, examples: WRONG },
      '2 problems · refuses 1 example it should allow · 2 pass',
    ],
    [
      'an example that did not run',
      { examples: NOT_RUN },
      'compiles · not run: needs an update example',
    ],
  ] as const)('says %s', (_name, options, words) => {
    expect(ruleDescription(rule(options as RawRuleOptions), true)).toBe(words);
  });

  it('says only the line’s words of the CEL for a folder that turns example results off', () => {
    expect(ruleDescription(rule({ examples: WRONG }), false)).toBe('compiles');
    expect(compileWords(rule({ compile: 'error', problems: 2 }))).toBe('2 problems');
  });
});

describe('documents', () => {
  it.each([
    ['CustomResourceDefinition', 'CRD'],
    ['ValidatingAdmissionPolicy', 'ValidatingAdmissionPolicy'],
    ['MutatingAdmissionPolicy', 'MutatingAdmissionPolicy'],
    ['ValidatingPolicy', 'Kyverno ValidatingPolicy'],
    ['Other', 'Other'],
  ])('writes the kind %s as %s', (kind, words) => {
    expect(kindWords(kind)).toBe(words);
  });

  it('describes a document by its kind and file', () => {
    const answer = parseRuleListAnswer(
      rawAnswer([rawDocument(URI, [], { file: 'config/crd/bases/widgets.yaml' })]),
    );

    const [document] = answer?.documents ?? [];

    expect(document && documentDescription(document)).toBe('CRD · config/crd/bases/widgets.yaml');
  });

  it.each([
    ['CustomResourceDefinition', 'symbol-class'],
    ['ValidatingAdmissionPolicy', 'shield'],
    ['MutatingAdmissionPolicy', 'tools'],
    ['ValidatingPolicy', 'law'],
    ['Other', 'file-code'],
  ])('gives the kind %s the product icon %s', (kind, id) => {
    expect(documentIcon(kind)).toEqual({ id });
  });

  it('says its name, kind, rules and how many have a problem to a screen reader', () => {
    const answer = parseRuleListAnswer(
      rawAnswer([rawDocument(URI, [rawRule(URI, 'd', 0), rawRule(URI, 'd', 1)])]),
    );
    const [document] = answer?.documents ?? [];

    expect(document && documentAccessibleLabel(document, 0)).toBe(
      'widgets.example.com, CRD, 2 rules',
    );
    expect(document && documentAccessibleLabel(document, 1)).toBe(
      'widgets.example.com, CRD, 2 rules, 1 with a problem',
    );
  });

  it('labels a document with no name by what it is, in the row and for a screen reader', () => {
    const answer = parseRuleListAnswer({
      documents: [{ ...rawDocument(URI, [rawRule(URI, 'd', 0)]), name: '' }],
    });
    const [document] = answer?.documents ?? [];

    expect(document && documentLabel(document)).toBe('(no name) CRD');
    expect(document && documentAccessibleLabel(document, 0)).toBe('(no name) CRD, CRD, 1 rule');
  });
});

describe('where a rule is', () => {
  it('says a CRD’s rule by its version, path and index', () => {
    expect(rulePlace(rule())).toBe('v1 · spec.ports[] · rule 0');
  });

  it('leaves out the path of a rule at the root', () => {
    expect(rulePlace(rule({}, { path: '' }))).toBe('v1 · rule 0');
  });

  it.each([
    ['validation', 'validations[2]'],
    ['matchCondition', 'matchConditions[2]'],
    ['variable', 'variables[2]'],
    ['auditAnnotation', 'auditAnnotations[2]'],
    ['mutation', 'mutations[2]'],
  ])('says a policy’s %s as %s', (field, place) => {
    expect(rulePlace(rule({ field, name: 'n' }, { index: 2 }))).toBe(place);
  });
});

describe('a rule’s tooltip', () => {
  it('has the label, where it is, its CEL in a code block, its problems and its example words', () => {
    const tooltip = ruleTooltip(
      rule({ label: 'port names are short', compile: 'error', examples: WRONG }),
      'CustomResourceDefinition',
      true,
      'self.name.size() <= 15',
    );

    expect(tooltip).toBe(
      [
        '**port names are short**',
        'v1 · spec.ports\\[\\] · rule 0',
        '```\nself.name.size() <= 15\n```',
        "- error: undefined field 'nmae'",
        'refuses 1 example it should allow · 2 pass',
      ].join('\n\n'),
    );
  });

  it('says it compiles when there is no problem, and shows no CEL it was not given', () => {
    const tooltip = ruleTooltip(rule({ label: 'ok' }), 'CustomResourceDefinition', true);

    expect(tooltip).toBe(['**ok**', 'v1 · spec.ports\\[\\] · rule 0', 'compiles'].join('\n\n'));
  });

  it('lists the first three problems only', () => {
    const many = rule(
      { compile: 'error' },
      {
        compile: {
          state: 'error',
          problems: 5,
          messages: ['a', 'b', 'c', 'd', 'e'].map((message) => ({ message, severity: 'error' })),
        },
      },
    );

    const tooltip = ruleTooltip(many, 'CustomResourceDefinition', false);

    expect(tooltip).toContain('- error: a\n- error: b\n- error: c');
    expect(tooltip).not.toContain('- error: d');
  });

  it.each([
    ['MutatingAdmissionPolicy', 'Example results do not cover a MutatingAdmissionPolicy.'],
    ['ValidatingPolicy', 'Example results do not cover a Kyverno ValidatingPolicy.'],
  ])('says example results do not cover a %s', (kind, words) => {
    expect(ruleTooltip(rule(), kind, true)).toContain(words);
    expect(coversExamples(kind)).toBe(false);
  });

  it('says nothing of examples for a kind they cover that has none, or for a folder with them off', () => {
    expect(ruleTooltip(rule(), 'CustomResourceDefinition', true)).not.toContain('Example');
    expect(ruleTooltip(rule({ examples: WRONG }), 'CustomResourceDefinition', false)).not.toContain(
      'refuses',
    );
    expect(coversExamples('CustomResourceDefinition')).toBe(true);
    expect(coversExamples('ValidatingAdmissionPolicy')).toBe(true);
  });

  it('has no command link, whatever the CEL or the label hold', () => {
    const tooltip = ruleTooltip(
      rule({ label: '[run](command:rm) <script>' }),
      'CustomResourceDefinition',
      true,
      'a ? "[x](command:y)" : b',
    );

    // Markdown renders no link inside a fenced code block, so only the text around it matters.
    const outsideCode = tooltip.replace(/```[\s\S]*?```/g, '');
    expect(outsideCode).not.toMatch(/(?<!\\)\]\(command:/);
    expect(tooltip).toContain('\\[run\\]\\(command:rm\\) \\<script\\>');
  });

  it('uses the CEL the engine sent when there is one', () => {
    expect(
      ruleTooltip(rule({}, { expression: 'self.a < 1' }), 'CustomResourceDefinition', true),
    ).toContain('```\nself.a < 1\n```');
  });
});

describe('Markdown helpers', () => {
  it.each([
    ['*bold*', '\\*bold\\*'],
    ['a_b', 'a\\_b'],
    ['`code`', '\\`code\\`'],
    ['a - b', 'a \\- b'],
    ['plain text', 'plain text'],
  ])('escapes %j as %j', (input, output) => {
    expect(escapeMarkdown(input)).toBe(output);
  });

  it('fences code with more backticks than any run in it', () => {
    expect(codeBlock('a')).toBe('```\na\n```');
    expect(codeBlock('a ``` b')).toBe('````\na ``` b\n````');
  });
});

describe('what a screen reader says of a rule', () => {
  it('says its label and its words, and the first problem for a rule that has one', () => {
    expect(ruleAccessibleLabel(rule({ label: 'ok', examples: PASS }), true)).toBe(
      'ok, compiles · 3 examples pass',
    );
  });

  // Review of #27: a screen reader cannot see the red or the yellow icon.
  it('says error for a compile error, with its first problem', () => {
    expect(ruleAccessibleLabel(rule({ label: 'bad', compile: 'error' }), true)).toBe(
      "bad, error, 1 problem: undefined field 'nmae'",
    );
  });

  it('says warning for a compile warning, with its first problem', () => {
    expect(ruleAccessibleLabel(rule({ label: 'meh', compile: 'warning' }), true)).toBe(
      "meh, warning, 1 problem: undefined field 'nmae'",
    );
  });

  it('says warning for a wrong example on a rule that compiles', () => {
    const wrong = { state: 'wrong' as const, words: 'refuses 1 example it should allow' };

    expect(ruleAccessibleLabel(rule({ label: 'r', examples: wrong }), true)).toBe(
      'r, warning, compiles · refuses 1 example it should allow',
    );
  });

  it('says no state word for a wrong example in a folder that shows no example results', () => {
    const wrong = { state: 'wrong' as const, words: 'refuses 1 example it should allow' };

    expect(ruleAccessibleLabel(rule({ label: 'r', examples: wrong }), false)).toBe('r, compiles');
  });
});

describe('the text between two positions', () => {
  const TEXT = 'abc\ndef ghi\njkl';

  it.each([
    ['within a line', { line: 1, character: 4 }, { line: 1, character: 7 }, 'ghi'],
    ['across lines', { line: 0, character: 1 }, { line: 2, character: 2 }, 'bc\ndef ghi\njk'],
    ['past the last line', { line: 5, character: 0 }, { line: 5, character: 2 }, ''],
    ['an empty range', { line: 1, character: 2 }, { line: 1, character: 2 }, ''],
  ])('cuts %s', (_what, start, end, expected) => {
    expect(textInRange(TEXT, start, end)).toBe(expected);
  });

  it('counts a line that ends with a carriage return, or a carriage return and a line feed', () => {
    expect(textInRange('a\r\nbcd\re', { line: 1, character: 1 }, { line: 2, character: 1 })).toBe(
      'cd\ne',
    );
  });
});
