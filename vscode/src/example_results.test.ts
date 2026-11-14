import { describe, expect, it } from 'vitest';

import {
  COMPUTING_LINE,
  exampleResultsInForce,
  failedLine,
  parseRuleResults,
  resultLine,
  resultLines,
  resultsTitle,
  type ExampleResult,
} from './example_results';
import type { ScopedValues } from './boolean_setting';

const START = { line: 38, character: 30 };

function example(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    uri: 'file:///repo/config/samples/widget.yaml',
    file: 'config/samples/widget.yaml',
    line: 1,
    apiVersion: 'example.com/v1',
    kind: 'Widget',
    name: 'w',
    operation: 'create',
    expect: 'allowed',
    ...over,
  };
}

function answer(...results: Record<string, unknown>[]): unknown {
  return {
    kubernetesVersion: '1.37',
    rules: [
      {
        document: 'CustomResourceDefinition widgets.example.com',
        path: 'spec.ports[]',
        index: 0,
        rule: 'self.name.size() <= 3',
        location: { uri: 'file:///repo/crd.yaml', range: { start: START, end: START } },
        results,
      },
    ],
  };
}

function result(over: Partial<ExampleResult> = {}): ExampleResult {
  return {
    uri: 'file:///repo/a.yaml',
    file: 'a.yaml',
    line: 1,
    operation: 'create',
    expect: 'allowed',
    result: 'allowed',
    refusals: [],
    wrong: false,
    ...over,
  };
}

describe('exampleResultsInForce', () => {
  it.each<[string, ScopedValues | undefined, boolean]>([
    ['nothing inspected', undefined, true],
    ['no scope set', {}, true],
    ['the user off', { globalValue: false }, false],
    ['the workspace off over the user on', { globalValue: true, workspaceValue: false }, false],
    [
      'the folder on over the workspace off',
      { workspaceValue: false, workspaceFolderValue: true },
      true,
    ],
  ])('is on unless a scope turns it off: %s', (_name, values, expected) => {
    expect(exampleResultsInForce(values)).toBe(expected);
  });
});

describe('resultsTitle', () => {
  it('names the rule by its line, from 1', () => {
    expect(resultsTitle(16)).toBe('Example results of the rule on line 16');
  });
});

describe('parseRuleResults', () => {
  it('reads each example of the rule: its file and line, what it expects, its verdict and the API server’s message', () => {
    const found = parseRuleResults(
      answer({
        example: example({ operation: 'update', expect: 'refused', message: 'too long' }),
        result: 'refused',
        refusals: [
          { path: 'spec.ports[2]', message: 'spec.ports[2]: Invalid value: name is too long' },
        ],
        ran: ['spec.ports[2]'],
        expected: false,
        wrong: 'example',
      }),
      START,
    );

    expect(found).toEqual([
      {
        uri: 'file:///repo/config/samples/widget.yaml',
        file: 'config/samples/widget.yaml',
        line: 1,
        operation: 'update',
        expect: 'refused',
        expectMessage: 'too long',
        result: 'refused',
        refusals: ['spec.ports[2]: Invalid value: name is too long'],
        wrong: true,
      },
    ]);
  });

  it.each<[string, Record<string, unknown>, Partial<ExampleResult>]>([
    [
      'a rule that did not run, with its reason',
      { result: 'not run', reason: 'the rule needs oldSelf' },
      { result: 'not run', note: 'the rule needs oldSelf' },
    ],
    [
      'a rule that failed, with what stopped it',
      { result: 'error', error: 'division by zero evaluating rule', expected: true },
      { result: 'error', note: 'division by zero evaluating rule', wrong: false },
    ],
    [
      'an example the rule allows as expected',
      { result: 'allowed', expected: true },
      { wrong: false },
    ],
  ])('reads %s', (_name, over, expected) => {
    expect(parseRuleResults(answer({ example: example(), ...over }), START)).toMatchObject([
      expected,
    ]);
  });

  it('takes the rule that starts at the location', () => {
    const two = {
      rules: [
        {
          location: { range: { start: { line: 1, character: 1 } } },
          results: [{ example: example({ file: 'one.yaml' }), result: 'allowed' }],
        },
        {
          location: { range: { start: START } },
          results: [{ example: example({ file: 'two.yaml' }), result: 'allowed' }],
        },
      ],
    };

    expect(parseRuleResults(two, START)).toMatchObject([{ file: 'two.yaml' }]);
  });

  it.each<[string, unknown, { line: number; character: number }]>([
    [
      'a stale location with two rules in the answer',
      {
        rules: [
          { location: { range: { start: { line: 1, character: 1 } } }, results: [] },
          { location: { range: { start: START } }, results: [] },
        ],
      },
      { line: 9, character: 9 },
    ],
    [
      'a stale location with the only rule of the answer, whose results are not the requested rule’s (#13)',
      answer({ example: example({ file: 'other.yaml' }), result: 'refused', expected: false }),
      { line: 40, character: 30 },
    ],
    ['a rule that moved along its line (#13)', answer(), { line: 38, character: 31 }],
    ['an answer with no rule', { rules: [] }, START],
    ['a rule with no location', { rules: [{ results: [] }] }, START],
  ])('shows no other rule’s results for %s: the rule changed', (_name, given, at) => {
    expect(parseRuleResults(given, at)).toBe('changed');
  });

  it('gives no result for a rule with no results', () => {
    expect(parseRuleResults(answer(), START)).toEqual([]);
  });

  it.each([
    ['null', null],
    ['a string', 'no'],
    ['an object without rules', { kubernetesVersion: '1.37' }],
    ['rules that are no list', { rules: {} }],
  ])('gives undefined for %s, which no engine of this extension answers', (_name, given) => {
    expect(parseRuleResults(given, START)).toBeUndefined();
  });

  it('drops a result with no example, no known verdict or no URI, and names a file the engine left out by its URI', () => {
    const found = parseRuleResults(
      answer(
        { result: 'allowed' },
        { example: example(), result: 'maybe' },
        { example: example({ uri: '' }), result: 'allowed' },
        { example: { uri: 'file:///ws/config/samples/w.yaml' }, result: 'allowed' },
      ),
      START,
    );

    expect(found).toEqual([
      expect.objectContaining({ file: 'samples/w.yaml', line: 1, operation: 'create' }),
    ]);
  });
});

describe('resultLine', () => {
  it.each<[string, Partial<ExampleResult>, string, string | undefined]>([
    ['an example that passes', {}, '$(pass) a.yaml:1', 'allowed'],
    [
      'an example the rule refuses but expects to be allowed',
      {
        result: 'refused',
        wrong: true,
        refusals: ['spec.replicas: Invalid value: 20: at most 10 replicas'],
      },
      '$(warning) a.yaml:1',
      'refused · expected allowed',
    ],
    [
      'an example that expects a message the refusal lacks',
      { result: 'refused', expect: 'refused', expectMessage: 'at most 10', wrong: true },
      '$(warning) a.yaml:1',
      'refused · expected refused with "at most 10"',
    ],
    [
      'an example that expects to be refused and is allowed',
      { expect: 'refused', wrong: true },
      '$(warning) a.yaml:1',
      'allowed · expected refused',
    ],
    ['an update', { operation: 'update' }, '$(pass) a.yaml:1', 'allowed · as an update'],
    ['a rule that failed', { result: 'error', note: 'cost limit' }, '$(error) a.yaml:1', 'error'],
    [
      'a rule that did not run',
      { result: 'not run', note: 'field absent' },
      '$(circle-slash) a.yaml:1',
      'not run',
    ],
    ['a later line', { line: 14, file: 'config/b.yaml' }, '$(pass) config/b.yaml:14', 'allowed'],
  ])(
    'shows %s with its icon, file and line, and its verdict',
    (_name, over, label, description) => {
      expect(resultLine(result(over))).toMatchObject({ label, description });
    },
  );

  it.each<[string, Partial<ExampleResult>, string | undefined]>([
    ['no message for an example that passes', {}, undefined],
    [
      'the refusals, each with its path',
      { result: 'refused', refusals: ['a: Invalid value: 1', 'b: Invalid value: 2'] },
      'a: Invalid value: 1; b: Invalid value: 2',
    ],
    ['why the rule did not run', { result: 'not run', note: 'needs an update' }, 'needs an update'],
    [
      'the refusals, then what stopped the rule',
      { result: 'error', refusals: ['a: Invalid value: 1'], note: 'budget' },
      'a: Invalid value: 1; budget',
    ],
  ])('has %s as its detail', (_name, over, detail) => {
    expect(resultLine(result(over)).detail).toBe(detail);
  });

  it('opens the example at its line when picked', () => {
    expect(resultLine(result({ line: 7 })).value).toEqual({
      action: 'open',
      uri: 'file:///repo/a.yaml',
      file: 'a.yaml',
      line: 7,
    });
  });
});

describe('resultLines', () => {
  const ok = result({ file: 'ok.yaml' });
  const refused = result({ file: 'refused.yaml', result: 'refused', expect: 'refused' });
  const wrongOne = result({ file: 'wrong1.yaml', result: 'refused', wrong: true });
  const wrongTwo = result({ file: 'wrong2.yaml', expect: 'refused', wrong: true });
  const errored = result({ file: 'error.yaml', result: 'error' });

  it('puts the wrong verdicts first, then the errors, then the others, each as the engine gave them, under a heading for each group', () => {
    const lines = resultLines([ok, errored, wrongOne, refused, wrongTwo]);

    expect(lines.map((line) => line.label)).toEqual([
      'Not as expected',
      '$(warning) wrong1.yaml:1',
      '$(warning) wrong2.yaml:1',
      'As expected',
      '$(error) error.yaml:1',
      '$(pass) ok.yaml:1',
      '$(pass) refused.yaml:1',
    ]);
    expect(lines.filter((line) => line.separator === true)).toHaveLength(2);
  });

  it.each([
    ['only right verdicts', [ok, refused], ['$(pass) ok.yaml:1', '$(pass) refused.yaml:1']],
    [
      'only wrong verdicts',
      [wrongTwo, wrongOne],
      ['$(warning) wrong2.yaml:1', '$(warning) wrong1.yaml:1'],
    ],
  ])('shows no heading for %s', (_name, results, labels) => {
    expect(resultLines(results).map((line) => line.label)).toEqual(labels);
  });
});

describe('the lines that say nothing is there', () => {
  it('says the results are being computed while the engine runs the rule', () => {
    expect(COMPUTING_LINE.label).toContain('Computing the example results');
  });

  it('offers the logs only for a failure with no reason of its own', () => {
    expect(failedLine('the engine refused', false).value).toEqual({ action: 'none' });
    expect(failedLine('Show the logs for the reason', true).value).toEqual({ action: 'showLogs' });
  });
});

/** A result of a ValidatingAdmissionPolicy's expression, as the engine gives it. */
function policyResult(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    example: example({
      file: 'config/samples/big.yaml',
      kind: 'Deployment',
      apiVersion: 'apps/v1',
    }),
    binding: 'warn-limits',
    param: {
      uri: 'file:///repo/config/samples/limits.yaml',
      file: 'config/samples/limits.yaml',
      line: 1,
      apiVersion: 'v1',
      kind: 'ConfigMap',
      name: 'limits',
    },
    result: 'warned',
    message: 'a deployment may not run more replicas than its limit',
    admission: {
      warnings: [
        "Validation failed for ValidatingAdmissionPolicy 'replica-limit' with binding 'warn-limits': a deployment may not run more replicas than its limit",
      ],
      annotations: { 'replica-limit/replicas': '20' },
    },
    expected: true,
    ...over,
  };
}

describe('the results of a ValidatingAdmissionPolicy', () => {
  it('reads the binding and the param an example ran under, and the API server’s answer', () => {
    const found = parseRuleResults(
      answer(
        policyResult(),
        policyResult({
          binding: 'deny-limits',
          result: 'refused',
          expected: false,
          wrong: 'rule',
          admission: {
            denied:
              "deployments.apps \"big\" is forbidden: ValidatingAdmissionPolicy 'replica-limit' with binding 'deny-limits' denied request: too many",
            annotations: { 'replica-limit/replicas': '20' },
          },
        }),
      ),
      START,
    );

    expect(found).toMatchObject([
      {
        result: 'warned',
        binding: 'warn-limits',
        param: 'config/samples/limits.yaml:1',
        message: 'a deployment may not run more replicas than its limit',
        admission: { annotations: { 'replica-limit/replicas': '20' } },
        wrong: false,
      },
      {
        result: 'refused',
        binding: 'deny-limits',
        admission: {
          denied:
            "deployments.apps \"big\" is forbidden: ValidatingAdmissionPolicy 'replica-limit' with binding 'deny-limits' denied request: too many",
          warnings: [],
          annotations: { 'replica-limit/replicas': '20' },
        },
        wrong: true,
      },
    ]);
  });

  it.each(['warned', 'audited', 'annotated', 'excluded'])('knows the verdict %s', (verdict) => {
    expect(parseRuleResults(answer(policyResult({ result: verdict })), START)).toMatchObject([
      { result: verdict },
    ]);
  });

  it('leaves the binding, the param and the answer out of a CRD’s result', () => {
    const [only] = parseRuleResults(answer({ example: example(), result: 'allowed' }), START) ?? [];

    expect(only).not.toHaveProperty('binding');
    expect(only).not.toHaveProperty('param');
    expect(only).not.toHaveProperty('admission');
  });

  it.each<[string, Partial<ExampleResult>, string, string]>([
    [
      'an example the policy warns about',
      { result: 'warned', binding: 'warn-limits', param: 'config/samples/limits.yaml:1' },
      '$(megaphone) a.yaml:1 · warn-limits',
      'warned · allowed, with a warning',
    ],
    [
      'an example the policy audits',
      { result: 'audited', binding: 'audit-limits' },
      '$(eye) a.yaml:1 · audit-limits',
      'audited · allowed, audited',
    ],
    [
      'an audit annotation with a value',
      { result: 'annotated', binding: 'b' },
      '$(tag) a.yaml:1 · b',
      'annotated · an annotation has a value',
    ],
    [
      'an example a match condition excludes',
      { result: 'excluded', binding: 'b' },
      '$(filter) a.yaml:1 · b',
      'excluded · a match condition is false',
    ],
    [
      'an example the policy refuses under a binding, though it expects to be allowed',
      {
        result: 'refused',
        wrong: true,
        binding: 'deny-limits',
        param: 'config/samples/limits.yaml:1',
        operation: 'update',
      },
      '$(warning) a.yaml:1 · deny-limits',
      'refused · expected allowed · as an update',
    ],
    [
      'an example the policy warns about, though it expects to be refused',
      { result: 'warned', wrong: true, expect: 'refused', binding: 'b' },
      '$(warning) a.yaml:1 · b',
      'warned · allowed, with a warning · expected refused',
    ],
  ])('shows %s with its own icon and words', (_name, over, label, description) => {
    expect(resultLine(result(over))).toMatchObject({ label, description });
  });

  it.each<[string, Partial<ExampleResult>, string | undefined]>([
    [
      'what the API server denied with, and the annotations it recorded',
      {
        result: 'refused',
        admission: {
          denied: 'deployments.apps "big" is forbidden: denied request: too many',
          warnings: [],
          annotations: { 'replica-limit/replicas': '20' },
        },
      },
      'denied: too many; annotations: replica-limit/replicas=20',
    ],
    [
      'the warnings the API server sent, without the words that name the policy and the binding',
      {
        result: 'warned',
        admission: {
          warnings: [
            "Validation failed for ValidatingAdmissionPolicy 'replica-limit' with binding 'warn-limits': too many",
            'another',
          ],
          annotations: {},
        },
      },
      'warnings: too many; another',
    ],
    [
      'what the API server denied with, as it words it when it does not say a request was denied',
      {
        result: 'refused',
        admission: { denied: 'forbidden for another reason', warnings: [], annotations: {} },
      },
      'denied: forbidden for another reason',
    ],
    [
      'the audit annotation of a failure, without the domain of its key',
      {
        result: 'audited',
        admission: {
          warnings: [],
          annotations: {
            'validation.policy.admission.k8s.io/validation_failure': '[{"message":"m"}]',
          },
        },
      },
      'annotations: validation_failure=[{"message":"m"}]',
    ],
    [
      'the annotations of an example that passes',
      {
        admission: { warnings: [], annotations: { 'p/a': '1', 'p/b': '2' } },
      },
      'annotations: p/a=1, p/b=2',
    ],
    [
      'the param first, then the answer, so the binding in the description is not cut short by it',
      {
        result: 'refused',
        param: 'config/params/limits.yaml:1',
        admission: { denied: 'x denied request: too many', warnings: [], annotations: {} },
      },
      'param config/params/limits.yaml:1; denied: too many',
    ],
    [
      'the param of an expression for which the API server said nothing',
      { result: 'annotated', param: 'p.yaml:2', message: '20' },
      'param p.yaml:2; 20',
    ],
    [
      'the message of an expression for which the API server said nothing',
      { result: 'annotated', message: '20' },
      '20',
    ],
    [
      'why a policy did not run',
      { result: 'not run', note: 'excluded by matchConditions' },
      'excluded by matchConditions',
    ],
    [
      'a long annotation cut short',
      { admission: { warnings: [], annotations: { k: 'x'.repeat(100) } } },
      `annotations: k=${'x'.repeat(59)}…`,
    ],
  ])('has %s as its detail', (_name, over, detail) => {
    expect(resultLine(result(over)).detail).toBe(detail);
  });

  it('lists the results of one example under two bindings as two lines', () => {
    const lines = resultLines([
      result({ result: 'refused', wrong: true, binding: 'deny-limits' }),
      result({ result: 'warned', binding: 'warn-limits' }),
    ]);

    expect(lines.filter((line) => !line.separator).map((line) => line.label)).toEqual([
      '$(warning) a.yaml:1 · deny-limits',
      '$(megaphone) a.yaml:1 · warn-limits',
    ]);
  });
});
