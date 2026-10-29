import { describe, expect, it } from 'vitest';

import { findCel, offsetMap } from './cel_ranges';

function crd(items: string): string {
  return `spec:\n  versions:\n    - schema:\n        x-kubernetes-validations:\n${items}`;
}

describe('findCel', () => {
  it.each([
    ['a plain scalar', '          - rule: self.a < 3\n', ['self.a < 3']],
    [
      'a plain scalar over several lines',
      '          - rule: self.a < 3 &&\n              self.b > 1\n            message: m\n',
      ['self.a < 3 && self.b > 1'],
    ],
    ['a plain scalar with a comment', '          - rule: self.a < 3 # why\n', ['self.a < 3']],
    ['a single-quoted scalar', "          - rule: 'self.a == ''x'''\n", ["self.a == 'x'"]],
    ['a double-quoted scalar', '          - rule: "self.a == \\"x\\""\n', ['self.a == "x"']],
    [
      'a literal block scalar',
      '          - rule: |\n              self.a < 3 &&\n              self.b\n',
      ['self.a < 3 &&\nself.b'],
    ],
    [
      'a folded block scalar',
      '          - rule: >-\n              self.a < 3 &&\n              self.b\n',
      ['self.a < 3 && self.b'],
    ],
    ['a block scalar with keep and an indent', '          - rule: |1+\n              x\n', [' x']],
    [
      'messageExpression',
      "          - rule: 'true'\n            messageExpression: \"'a' + self.b\"\n",
      ['true', "'a' + self.b"],
    ],
  ])('finds the CEL in %s', (_name, items, expected) => {
    const sources = findCel(crd(items)).map((range) => range.source);
    expect(sources).toEqual(expected);
  });

  it.each([
    [
      'a plain message',
      crd("          - rule: 'true'\n            message: self must be big\n"),
      ['true'],
    ],
    [
      'a rule outside x-kubernetes-validations',
      'spec:\n  rule: self.a\n  other:\n    - rule: self.b\n',
      [],
    ],
    [
      'a rule after the list ended',
      crd("          - rule: 'true'\n        description: x\n        rule: self.c\n"),
      ['true'],
    ],
    [
      'a comment',
      crd('          # rule: self.a\n          - rule: self.b # rule: self.c\n'),
      ['self.b'],
    ],
    [
      'a nested rule key',
      crd('          - rule: self.a\n            extra:\n              rule: self.z\n'),
      ['self.a'],
    ],
    ['an empty value', crd('          - rule:\n'), []],
    ['an empty document', '', []],
    ['broken YAML', ':::\n\t- [\n"\n', []],
  ])('gives %s the right ranges', (_name, text, expected) => {
    expect(findCel(text).map((range) => range.source)).toEqual(expected);
  });

  it('keeps going after another list in the same document', () => {
    const text = crd('          - rule: self.a\n') + '---\n' + crd('          - rule: self.b\n');
    expect(findCel(text).map((range) => range.source)).toEqual(['self.a', 'self.b']);
  });

  it('maps every character of a double-quoted scalar with escapes to the document', () => {
    const text = crd('          - rule: "a\\u0041\\n\\"b"\n');
    const [range] = findCel(text);
    expect(range?.source).toBe('aA\n"b');
    const offsets = range && offsetMap(range);
    const ends = Array.from({ length: 5 }, (_, i) => offsets?.end(i));
    const quote = text.indexOf('\\u0041');
    expect(ends).toEqual([quote, quote + 6, quote + 8, quote + 10, quote + 11]);
  });

  it('folds a quoted scalar over several lines', () => {
    expect(
      findCel(crd('          - rule: "self.a\n              && self.b"\n')).map(
        (range) => range.source,
      ),
    ).toEqual(['self.a && self.b']);
  });

  it('handles CRLF line ends', () => {
    expect(
      findCel(crd('          - rule: self.a\r\n').replaceAll('\n', '\r\n')).map(
        (range) => range.source,
      ),
    ).toEqual(['self.a']);
  });

  it.each([
    [
      'a literal block',
      '          - rule: |\n              a\n' + '\n'.repeat(20000) + '              b\n',
    ],
    ['an open double quote', '          - rule: "a\n' + '\n'.repeat(20000)],
  ])('does not throw on %s with 20000 blank lines', (_name, items) => {
    expect(() => findCel(crd(items))).not.toThrow();
  });

  it('keeps each character of a multi-line plain scalar at its document offset', () => {
    const text = crd('          - rule: ab\n              cd\n');
    const [range] = findCel(text);
    expect(range?.source).toBe('ab cd');
    expect(range && offsetMap(range).start(3)).toBe(text.indexOf('cd'));
  });
});

const VAP_FIELDS = [
  ['spec.validations[].expression', 'validations', 'expression'],
  ['spec.validations[].messageExpression', 'validations', 'messageExpression'],
  ['spec.matchConditions[].expression', 'matchConditions', 'expression'],
  ['spec.variables[].expression', 'variables', 'expression'],
  ['spec.auditAnnotations[].valueExpression', 'auditAnnotations', 'valueExpression'],
] as const;

const SCALAR_STYLES = [
  ['plain', 'a < 3', 'a < 3'],
  ['single-quoted', "'a == ''x'''", "a == 'x'"],
  ['double-quoted', '"a == \\"x\\""', 'a == "x"'],
  ['literal block', '|\n              a &&\n              b', 'a &&\nb'],
  ['folded block', '>-\n              a &&\n              b', 'a && b'],
] as const;

function policy(kind: string, body: string): string {
  return `apiVersion: admissionregistration.k8s.io/v1\nkind: ${kind}\nspec:\n${body}`;
}

describe('findCel in admission policies', () => {
  describe.each(VAP_FIELDS)('%s', (_path, list, key) => {
    it.each(SCALAR_STYLES)('finds a %s scalar', (_style, value, expected) => {
      const text = policy(
        'ValidatingAdmissionPolicy',
        `  ${list}:\n    - name: n\n      ${key}: ${value}\n`,
      );
      expect(findCel(text).map((range) => range.source)).toEqual([expected]);
    });
  });

  describe.each([
    ['spec.matchConditions[].expression', '  matchConditions:\n    - expression: '],
    ['spec.variables[].expression', '  variables:\n    - expression: '],
    [
      'spec.mutations[].applyConfiguration.expression',
      '  mutations:\n    - applyConfiguration:\n        expression: ',
    ],
    [
      'spec.mutations[].jsonPatch.expression',
      '  mutations:\n    - patchType: JSONPatch\n      jsonPatch:\n        expression: ',
    ],
  ])('MutatingAdmissionPolicy %s', (_path, prefix) => {
    it.each(SCALAR_STYLES)('finds a %s scalar', (_style, value, expected) => {
      const text = policy('MutatingAdmissionPolicy', `${prefix}${value}\n`);
      expect(findCel(text).map((range) => range.source)).toEqual([expected]);
    });
  });

  describe.each(VAP_FIELDS)('Kyverno ValidatingPolicy %s', (_path, list, key) => {
    it.each(SCALAR_STYLES)('finds a %s scalar', (_style, value, expected) => {
      const text = `apiVersion: policies.kyverno.io/v1\nkind: ValidatingPolicy\nspec:\n  ${list}:\n    - name: n\n      ${key}: ${value}\n`;
      expect(findCel(text, undefined, { kyverno: true }).map((range) => range.source)).toEqual([
        expected,
      ]);
    });

    it('finds nothing without the kyverno feature', () => {
      const text = `apiVersion: policies.kyverno.io/v1\nkind: ValidatingPolicy\nspec:\n  ${list}:\n    - name: n\n      ${key}: a\n`;
      expect(findCel(text)).toEqual([]);
      expect(findCel(text, undefined, { kyverno: false })).toEqual([]);
    });
  });

  it.each([
    ['a Kyverno policy of another version', 'policies.kyverno.io/v2', 'ValidatingPolicy'],
    ['a Kyverno kind not supported yet', 'policies.kyverno.io/v1', 'ClusterPolicy'],
  ])('leaves out the fields of %s', (_name, apiVersion, kind) => {
    const text = `apiVersion: ${apiVersion}\nkind: ${kind}\nspec:\n  validations:\n    - expression: a\n`;
    expect(findCel(text, undefined, { kyverno: true })).toEqual([]);
  });

  it.each([
    ['a ConfigMap', 'v1', 'ConfigMap', '  validations:\n    - expression: a\n'],
    ['a Deployment', 'apps/v1', 'Deployment', '  variables:\n    - expression: a\n'],
    [
      'a policy of another group',
      'example.com/v1',
      'ValidatingAdmissionPolicy',
      '  validations:\n    - expression: a\n',
    ],
    [
      'a mutating field in a validating policy',
      'admissionregistration.k8s.io/v1',
      'ValidatingAdmissionPolicy',
      '  mutations:\n    - jsonPatch:\n        expression: a\n',
    ],
  ])('leaves out the fields of %s', (_name, apiVersion, kind, body) => {
    expect(findCel(`apiVersion: ${apiVersion}\nkind: ${kind}\nspec:\n${body}`)).toEqual([]);
  });

  it('leaves out an expression key in the data of a ConfigMap', () => {
    const text = 'apiVersion: v1\nkind: ConfigMap\ndata:\n  expression: a < 3\n';
    expect(findCel(text)).toEqual([]);
  });

  it('colors the policy documents of a file and leaves the others as they are', () => {
    const text = [
      'apiVersion: v1',
      'kind: ConfigMap',
      'data:',
      '  expression: nope',
      '---',
      policy('ValidatingAdmissionPolicy', '  validations:\n    - expression: a\n'),
      '---',
      'kind: Other',
      'spec:',
      '  validations:',
      '    - expression: nope',
      '---',
      policy('MutatingAdmissionPolicy', '  variables:\n    - expression: b\n'),
    ].join('\n');
    expect(findCel(text).map((range) => range.source)).toEqual(['a', 'b']);
  });

  it('reads the CEL of a policy under a window', () => {
    const text = policy(
      'ValidatingAdmissionPolicy',
      '  validations:\n    - expression: a\n    - expression: b\n',
    );
    expect(findCel(text, { fromLine: 5, toLine: 5 }).map((range) => range.source)).toEqual(['b']);
  });
});
