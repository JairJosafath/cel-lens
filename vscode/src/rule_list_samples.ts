// Answers of `cel-lens.ruleList` as the engine writes them, for the tests and the bench of the
// rule view: plain objects, which the model reads as it reads the engine's.

export interface RawRuleOptions {
  readonly label?: string;
  readonly labelFrom?: string;
  readonly compile?: 'ok' | 'warning' | 'error';
  readonly problems?: number;
  readonly examples?: { state: string; words: string } | null;
  readonly problem?: boolean;
  readonly field?: string;
  readonly name?: string;
}

/** One rule of a CRD at `index`, in the file `uri`, as the engine writes it. */
export function rawRule(
  uri: string,
  document: string,
  index: number,
  options: RawRuleOptions = {},
): Record<string, unknown> {
  const line = 10 + index * 4;
  const compile = options.compile ?? 'ok';
  const rule: Record<string, unknown> = {
    id: `${uri}#${document}#${options.field === undefined ? `v1#spec.ports[]#${String(index)}` : `${options.field}#${options.name ?? ''}#${String(index)}`}`,
    document,
    index,
    label: options.label ?? `rule ${String(index)} says something`,
    labelFrom: options.labelFrom ?? 'message',
    location: {
      uri,
      range: { start: { line, character: 18 }, end: { line, character: 40 } },
    },
    span: { start: { line, character: 0 }, end: { line: line + 2, character: 30 } },
    compile: {
      state: compile,
      problems: options.problems ?? (compile === 'ok' ? 0 : 1),
      messages: compile === 'ok' ? [] : [{ message: "undefined field 'nmae'", severity: compile }],
    },
  };
  if (options.field === undefined) {
    rule['version'] = 'v1';
    rule['path'] = 'spec.ports[]';
  } else {
    rule['field'] = options.field;
    if (options.name !== undefined) {
      rule['name'] = options.name;
    }
  }
  if (options.examples !== undefined) {
    rule['examples'] = options.examples;
  }
  if (options.problem !== undefined) {
    rule['problem'] = options.problem;
  }
  return rule;
}

export interface RawDocumentOptions {
  readonly folder?: string | null;
  readonly file?: string;
  readonly kind?: string;
  readonly name?: string;
}

/** A document of `rules` in the file `uri`. */
export function rawDocument(
  uri: string,
  rules: readonly Record<string, unknown>[],
  options: RawDocumentOptions = {},
): Record<string, unknown> {
  const kind = options.kind ?? 'CustomResourceDefinition';
  const name = options.name ?? 'widgets.example.com';
  const folder = options.folder === undefined ? 'file:///repo' : options.folder;
  return {
    uri,
    ...(folder !== null && { folder }),
    file: options.file ?? uri.replace('file:///repo/', ''),
    document: `${kind} ${name}`,
    kind,
    name,
    range: { start: { line: 0, character: 0 }, end: { line: 60, character: 0 } },
    rules,
  };
}

/** `files` CRD files of `rules` rules each, named `crd0.yaml`, `crd1.yaml`, in a folder. */
export function rawFiles(files: number, rules: number, start = 0): Record<string, unknown>[] {
  return Array.from({ length: files }, (_, at) => {
    const uri = `file:///repo/config/crd${String(start + at).padStart(4, '0')}.yaml`;
    const name = `things${String(start + at)}.example.com`;
    const document = `CustomResourceDefinition ${name}`;
    return rawDocument(
      uri,
      Array.from({ length: rules }, (_unused, index) => rawRule(uri, document, index)),
      { name },
    );
  });
}

/** An answer of `cel-lens.ruleList`. */
export function rawAnswer(
  documents: readonly Record<string, unknown>[],
  extra: Record<string, unknown> = {},
): Record<string, unknown> {
  return { kubernetesVersion: '1.37', documents, ...extra };
}
