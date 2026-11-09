import { describe, expect, it } from 'vitest';

import {
  EXAMPLE_CURSOR_LINE,
  MAX_EXAMPLE_BYTES,
  exampleTooBig,
  exampleComment,
  looksLikeResource,
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

describe('exampleTooBig', () => {
  it.each([
    ['an example of 1 MiB', 'a'.repeat(MAX_EXAMPLE_BYTES), undefined],
    [
      'one byte over',
      'a'.repeat(MAX_EXAMPLE_BYTES + 1),
      'the example is 1.0 MiB, and the engine reads at most 1 MiB',
    ],
    [
      '1.5 MiB',
      'a'.repeat(MAX_EXAMPLE_BYTES * 1.5),
      'the example is 1.5 MiB, and the engine reads at most 1 MiB',
    ],
    [
      'bytes, not characters: é is two',
      'é'.repeat(MAX_EXAMPLE_BYTES / 2 + 1),
      'the example is 1.0 MiB, and the engine reads at most 1 MiB',
    ],
    ['a short one', 'teams: []\n', undefined],
  ])("says when %s is over the engine's limit (#16)", (_name, text, expected) => {
    expect(exampleTooBig(text)).toBe(expected);
  });
});

describe('looksLikeResource', () => {
  it.each([
    ['a YAML resource', 'apiVersion: v1\nkind: ConfigMap\ndata: {}\n', true],
    ['YAML documents, the comment left out', '# An example\n---\napiVersion: v1\nkind: X\n', true],
    [
      'a YAML list of resources',
      '- apiVersion: v1\n  kind: A\n- apiVersion: v1\n  kind: B\n',
      true,
    ],
    ['a JSON resource after the comment', '# An example\n{"apiVersion": "v1", "kind": "X"}', true],
    ['a JSON list of resources', '[{"apiVersion": "v1", "kind": "X"}]', true],
    ['a value', 'teams: []\nmax: 3\n', false],
    ['a value that holds a resource', 'template:\n  apiVersion: v1\n  kind: Pod\n', false],
    [
      'a JSON value that holds a resource',
      '{"template": {"apiVersion": "v1", "kind": "Pod"}}',
      false,
    ],
    ['nothing', '', false],
  ])(
    'reads %s as a resource, for an engine that cannot tell: %s (#16)',
    (_name, text, expected) => {
      expect(looksLikeResource(text)).toBe(expected);
    },
  );
});

describe('pastedPath', () => {
  const limit = 'apiVersion: rules.example.com/v1\nkind: ReplicaLimit\nspec: {}\n';
  const kinds = [{ apiVersion: 'rules.example.com/v1', kind: 'ReplicaLimit' }];

  it.each([
    ['a value at the root of its object', limit, undefined, kinds, 'none'],
    [
      "any example, when the engine knows the value's kinds and decides itself (#16)",
      'teams: []\n',
      'spec.config',
      kinds,
      'send',
    ],
    ["a resource when the value's kinds are not known", limit, 'spec.config', [], 'try'],
    ['a resource for an engine with no valueKinds (#16)', limit, 'spec.config', undefined, 'try'],
    [
      'the value alone for an engine with no valueKinds',
      'teams: []\n',
      'spec.config',
      undefined,
      'none',
    ],
  ])('sends the path with %s: %s', (_name, text, path, valueKinds, expected) => {
    expect(pastedPath(text, path, valueKinds)).toBe(expected);
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
