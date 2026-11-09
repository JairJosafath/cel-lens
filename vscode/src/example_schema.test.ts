import { describe, expect, it } from 'vitest';

import {
  EXAMPLE_CURSOR_LINE,
  exampleComment,
  pastedAsLines,
  pastedKinds,
  pastedPath,
  moveNotes,
  notePlaces,
  parseInferredSchema,
  type NotePlace,
} from './example_schema';

/** A `.cellens` with two entries whose schemas the engine wrote, as block YAML with sorted keys. */
const LINES = [
  '# .cellens: types cel-lens assumes where Kubernetes gives dyn. Commit it.',
  'types:',
  '  - file: policies/limits.yaml',
  '    document: ValidatingAdmissionPolicy limits',
  '    variable: params',
  '    schema:',
  '      properties:',
  '        apiVersion:',
  '          type: string',
  '        spec:',
  '          properties:',
  '            "on":',
  '              type: string',
  '            labels:',
  '              additionalProperties:',
  '                x-kubernetes-preserve-unknown-fields: true',
  '              type: object',
  '            ports:',
  '              items:',
  '                properties:',
  '                  name:',
  '                    x-kubernetes-preserve-unknown-fields: true',
  '                type: object',
  '              type: array',
  '            teams:',
  '              items:',
  '                x-kubernetes-preserve-unknown-fields: true',
  '              type: array',
  '          type: object',
  '      type: object',
  '  - file: policies/other.yaml',
  '    document: ValidatingAdmissionPolicy other',
  '    variable: params',
  '    schema:',
  '      properties:',
  '        teams:',
  '          type: array',
  '      type: object',
];
const TEXT = LINES.join('\n');

function lineOf(text: string, after = 0): number {
  return LINES.indexOf(text, after);
}

describe('parseInferredSchema', () => {
  it('reads the schema and the notes, as the engine documents them', () => {
    expect(
      parseInferredSchema({
        schema: { type: 'object' },
        notes: ['items: an empty list says nothing about its items', 3, ''],
      }),
    ).toEqual({
      schema: { type: 'object' },
      notes: ['items: an empty list says nothing about its items'],
    });
  });

  it.each([
    ['no notes', { schema: { type: 'object' } }, { schema: { type: 'object' }, notes: [] }],
    ['no schema', { notes: [] }, undefined],
    ['a schema that is no object', { schema: 'type: object', notes: [] }, undefined],
    ['null', null, undefined],
  ])('reads an answer with %s', (_name, answer, expected) => {
    expect(parseInferredSchema(answer)).toEqual(expected);
  });
});

describe('exampleComment', () => {
  it("starts with a short line, which names the editor's tab, then says what to paste and how to use it, and leaves the cursor below it (#9's review)", () => {
    const comment = exampleComment('params.spec.config', true);
    const lines = comment.split('\n');

    expect(lines[0]).toBe('# An example of params.spec.config');
    expect(comment).toContain('or a whole resource that holds it, as YAML or JSON');
    expect(comment).toContain('"Use this example as the type"');
    expect(comment).toContain('Closing this editor writes nothing');
    expect(lines).toHaveLength(EXAMPLE_CURSOR_LINE + 1);
    expect(lines[EXAMPLE_CURSOR_LINE]).toBe('');
  });

  it('asks for the value alone when it is a whole object, such as params', () => {
    expect(exampleComment('params', false)).not.toContain('whole resource');
  });
});

describe('pastedKinds', () => {
  it.each([
    ['a YAML resource', 'apiVersion: v1\nkind: ConfigMap\ndata: {}\n', ['ConfigMap']],
    ['YAML documents, the comment left out', '# An example\n---\napiVersion: v1\nkind: X\n', ['X']],
    [
      'a YAML list of resources',
      '- apiVersion: v1\n  kind: A\n  spec: {}\n- kind: B # second\n  apiVersion: v1\n',
      ['A', 'B'],
    ],
    ["a YAML list indented more (#14's review)", '-   apiVersion: v1\n    kind: A\n', ['A']],
    [
      "a flow-style YAML resource (#14's review)",
      '{apiVersion: v1, kind: ConfigMap}\n',
      ['ConfigMap'],
    ],
    ['a quoted kind', 'apiVersion: "apps/v1"\nkind: "Deployment"\n', ['Deployment']],
    ['a JSON resource after the comment', '# An example\n{"apiVersion": "v1", "kind": "X"}', ['X']],
    ['a JSON list of resources', '[{"apiVersion": "v1", "kind": "X"}, {"kind": "Y"}]', ['X']],
    [
      'a v1 List, by its items',
      'apiVersion: v1\nkind: List\nitems:\n- {apiVersion: v1, kind: A}\n',
      ['A'],
    ],
    [
      'an object reference, which looks like a resource',
      'apiVersion: v1\nkind: Secret\nname: s\n',
      ['Secret'],
    ],
    ['a value', 'teams: []\nmax: 3\n', []],
    ['a value that holds a resource', 'template:\n  apiVersion: v1\n  kind: Pod\n', []],
    ['a list of values', '- name: a\n  kind: x\n', []],
    ['a JSON value that holds a resource', '{"template": {"apiVersion": "v1", "kind": "Pod"}}', []],
    ['YAML it cannot read', 'apiVersion: v1\nkind: [\n', []],
    ['nothing', '', []],
  ])('reads the kinds of %s at its root', (_name, text, expected) => {
    expect(pastedKinds(text)).toEqual(expected);
  });
});

describe('pastedPath', () => {
  const limit = 'apiVersion: rules.example.com/v1\nkind: ReplicaLimit\nspec: {}\n';
  const reference = 'apiVersion: v1\nkind: Secret\nname: quota\n';

  it.each([
    ['a value at the root of its object', limit, undefined, ['ReplicaLimit'], 'none'],
    ['the value alone', 'teams: []\n', 'spec.config', ['ReplicaLimit'], 'none'],
    ["a resource of the value's kind", limit, 'spec.config', ['ReplicaLimit'], 'send'],
    [
      'a resource of another kind, such as an object reference',
      reference,
      'spec.ref',
      ['ReplicaLimit'],
      'none',
    ],
    [
      "a resource when the value's kinds are not known, which may be either (#14's review)",
      reference,
      'spec.ref',
      [],
      'ask',
    ],
  ])('takes the path for %s: %s', (_name, text, path, kinds, expected) => {
    expect(pastedPath(text, path, kinds)).toBe(expected);
  });

  it("asks whether the resource holds the value first, then whether it is the value (#14's review)", () => {
    expect(pastedAsLines('Deployment', 'spec.template')).toEqual([
      {
        label: "$(symbol-class) Use the whole Deployment's spec.template",
        detail: 'The value is the spec.template of each pasted Deployment',
        value: 'whole',
      },
      {
        label: '$(symbol-field) Use it as the value itself',
        detail: 'The pasted Deployment is an example of the value, such as an object reference',
        value: 'value',
      },
    ]);
  });
});

describe('notePlaces', () => {
  const schema = lineOf('    schema:');

  it.each([
    ['a field', 'spec.teams: an empty list says nothing about its items', '            teams:'],
    ["a list's items", 'spec.ports[].name: only ever null', '                  name:'],
    [
      "a map's values",
      'spec.labels[*]: an empty object says nothing about its fields',
      '              additionalProperties:',
    ],
    ['a quoted key', 'spec.on: on is a boolean to kubectl: quote it', '            "on":'],
    ['the parent of a field not there', 'spec.owner: only ever null', '        spec:'],
    ['schema: for a note that names no value', 'the examples disagree', '    schema:'],
  ])('puts a note about %s at its key', (_name, note, line) => {
    expect(notePlaces(TEXT, schema, [note])).toEqual([
      expect.objectContaining({ line: lineOf(line), message: note }),
    ]);
  });

  it('marks the key itself and knows its block, which an edit addresses', () => {
    const [place] = notePlaces(TEXT, schema, ['spec.teams: an empty list says nothing']);

    expect(place).toEqual({
      line: lineOf('            teams:'),
      start: 12,
      end: 17,
      blockEnd: lineOf('              type: array', lineOf('            teams:')),
      message: 'spec.teams: an empty list says nothing',
    });
    expect(notePlaces(TEXT, schema, ['spec.on: quote it'])[0]).toMatchObject({
      start: 12,
      end: 16,
    });
  });

  it.each([
    ['the line of schema:', 0],
    ['the line of its type, below schema:', 1],
    ['the line the entry starts at', -3],
  ])('finds the schema of the entry from %s', (_name, offset) => {
    expect(notePlaces(TEXT, schema + offset, ['spec.teams: x'])[0]?.line).toBe(
      lineOf('            teams:'),
    );
  });

  it('looks only in its own entry', () => {
    const other = lineOf('    schema:', schema + 1);

    expect(notePlaces(TEXT, other, ['teams: x', 'spec.teams: y']).map((p) => p.line)).toEqual([
      lineOf('        teams:', other),
      other,
    ]);
  });

  it("leaves out the value's own path when the note starts with it", () => {
    const text = ['  - file: a.yaml', '    schema:', '      properties:', '        teams:'].join(
      '\n',
    );

    expect(notePlaces(text, 1, ['spec.config.teams: x'], 'spec.config')[0]?.line).toBe(3);
  });
});

describe('moveNotes', () => {
  const note: NotePlace = { line: 10, start: 4, end: 9, blockEnd: 13, message: 'm' };

  it.each([
    [
      'moves a note down with lines added above it',
      [{ startLine: 2, endLine: 2, text: 'a\nb\n' }],
      [12, 15],
    ],
    ['moves it up with lines removed above it', [{ startLine: 2, endLine: 5, text: '' }], [7, 10]],
    [
      'keeps it in place for a change below its block',
      [{ startLine: 14, endLine: 20, text: '' }],
      [10, 13],
    ],
    [
      'takes the changes in order',
      [
        { startLine: 0, endLine: 0, text: '\n' },
        { startLine: 30, endLine: 30, text: '\n\n' },
      ],
      [11, 14],
    ],
  ])('%s', (_name, changes, [line, blockEnd]) => {
    expect(moveNotes([note], changes)).toEqual([{ ...note, line, blockEnd }]);
  });

  it.each([
    ['its key', 10],
    ['its block', 12],
    ['the last line of its block', 13],
  ])('drops a note once an edit touches %s', (_name, line) => {
    expect(moveNotes([note], [{ startLine: line, endLine: line, text: 'x' }])).toEqual([]);
  });

  it('drops a note a change replaces with the lines around it', () => {
    expect(moveNotes([note], [{ startLine: 8, endLine: 11, text: '' }])).toEqual([]);
  });
});
