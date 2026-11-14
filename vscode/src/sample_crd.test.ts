import { describe, expect, it } from 'vitest';
import { parse } from 'yaml';

import { SAMPLE_CRD, SAMPLE_PATH, SAMPLE_RULES_LINE } from './sample_crd';

interface Schema {
  properties?: Record<string, Schema>;
  'x-kubernetes-validations'?: { rule: string; message?: string }[];
}

interface Crd {
  apiVersion: string;
  kind: string;
  metadata: { name: string };
  spec: { versions: { schema: { openAPIV3Schema: Schema } }[] };
}

const crd = parse(SAMPLE_CRD) as Crd;
const spec = crd.spec.versions[0]?.schema.openAPIV3Schema.properties?.['spec'];
const rules = spec?.['x-kubernetes-validations'] ?? [];

/** The fields of `self` a rule reads, such as `replicas` for `self.replicas > 0`. */
function fieldsRead(rule: string): string[] {
  return [...rule.matchAll(/\bself\.(\w+)/g)].map((match) => match[1] ?? '');
}

describe('SAMPLE_CRD', () => {
  it('is the CustomResourceDefinition widgets.example.com of apiextensions.k8s.io/v1', () => {
    expect(crd).toMatchObject({
      apiVersion: 'apiextensions.k8s.io/v1',
      kind: 'CustomResourceDefinition',
      metadata: { name: 'widgets.example.com' },
    });
  });

  it('has the fields name, replicas and minReplicas', () => {
    expect(Object.keys(spec?.properties ?? {})).toEqual(['name', 'replicas', 'minReplicas']);
  });

  it('has four rules, one of them self.replica > 0', () => {
    expect(rules.map((each) => each.rule)).toHaveLength(4);
    expect(rules.map((each) => each.rule)).toContain('self.replica > 0');
  });

  it('reads one field the schema does not have, replica, in one rule', () => {
    const known = Object.keys(spec?.properties ?? {});
    const wrong = rules.flatMap((each) =>
      fieldsRead(each.rule).filter((field) => !known.includes(field)),
    );
    expect(wrong).toEqual(['replica']);
  });

  it('gives every rule a message', () => {
    expect(rules.every((each) => (each.message ?? '').length > 0)).toBe(true);
  });

  it('opens at its rules: the line of x-kubernetes-validations', () => {
    expect(SAMPLE_CRD.split('\n')[SAMPLE_RULES_LINE]?.trim()).toBe('x-kubernetes-validations:');
  });

  it('is written to get-started/widgets.yaml', () => {
    expect(SAMPLE_PATH.join('/')).toBe('get-started/widgets.yaml');
  });
});
