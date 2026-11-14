import { describe, expect, it } from 'vitest';

import {
  RuleList,
  parseRuleListAnswer,
  parseRuleListChange,
  parseScanLimits,
  type RuleListAnswer,
} from './rule_list';
import { rawAnswer, rawDocument, rawFiles, rawRule } from './rule_list_samples';

const A = 'file:///repo/config/a.yaml';
const B = 'file:///repo/config/b.yaml';
const C = 'file:///repo/config/c.yaml';

function read(value: unknown): RuleListAnswer {
  const answer = parseRuleListAnswer(value);
  if (answer === undefined) {
    throw new Error('the sample answer was not read');
  }
  return answer;
}

function doc(uri: string, rules: number, name = 'x.example.com'): Record<string, unknown> {
  const document = `CustomResourceDefinition ${name}`;
  return rawDocument(
    uri,
    Array.from({ length: rules }, (_, index) => rawRule(uri, document, index)),
    { name, file: uri.replace('file:///repo/', '') },
  );
}

function files(list: RuleList): string[] {
  return [...list.files];
}

describe('parseRuleListAnswer', () => {
  it('reads a document with its rules, their compile state and their examples', () => {
    const answer = read(
      rawAnswer(
        [
          rawDocument(A, [
            rawRule(A, 'CustomResourceDefinition x', 0, {
              label: 'at most 3',
              compile: 'error',
              problems: 2,
              examples: { state: 'wrong', words: 'refuses 1 example it should allow · 2 pass' },
              problem: true,
            }),
          ]),
        ],
        {
          generation: 7,
          scan: { state: 'done', yamlFiles: 3, filesWithRules: 1, filesLeftOut: 0 },
        },
      ),
    );

    expect(answer).toMatchObject({
      kubernetesVersion: '1.37',
      generation: 7,
      full: false,
      removed: [],
      scan: { state: 'done', yamlFiles: 3, filesWithRules: 1, filesLeftOut: 0 },
    });
    expect(answer.documents).toMatchObject([
      {
        uri: A,
        folder: 'file:///repo',
        kind: 'CustomResourceDefinition',
        name: 'widgets.example.com',
        rules: [
          {
            label: 'at most 3',
            labelFrom: 'message',
            compile: { state: 'error', problems: 2, messages: [{ severity: 'error' }] },
            examples: { state: 'wrong', words: 'refuses 1 example it should allow · 2 pass' },
            problem: true,
            version: 'v1',
            path: 'spec.ports[]',
            location: { uri: A, range: { start: { line: 10, character: 18 } } },
          },
        ],
      },
    ]);
  });

  it('reads a policy rule by its field and name, and an open file with no folder', () => {
    const [document] = read(
      rawAnswer([
        rawDocument(
          'file:///other/p.yaml',
          [
            rawRule('file:///other/p.yaml', 'ValidatingAdmissionPolicy p', 1, {
              field: 'validation',
              name: 'limit',
            }),
          ],
          { folder: null, kind: 'ValidatingAdmissionPolicy', name: 'p' },
        ),
      ]),
    ).documents;

    expect(document).not.toHaveProperty('folder');
    expect(document?.rules[0]).toMatchObject({ field: 'validation', name: 'limit', index: 1 });
  });

  it('works out a rule problem the engine did not set from what it says', () => {
    const [document] = read(
      rawAnswer([
        rawDocument(A, [
          rawRule(A, 'd', 0),
          rawRule(A, 'd', 1, { compile: 'warning' }),
          rawRule(A, 'd', 2, { examples: { state: 'error', words: '1 example errors' } }),
          rawRule(A, 'd', 3, { examples: { state: 'notRun', words: 'not run: CRD refused' } }),
        ]),
      ]),
    ).documents;

    expect(document?.rules.map((rule) => rule.problem)).toEqual([false, true, true, false]);
  });

  it.each([
    ['a string', 'no'],
    ['null', null],
    ['an answer with no documents', { kubernetesVersion: '1.37' }],
  ])('reads %s as no list', (_name, value) => {
    expect(parseRuleListAnswer(value)).toBeUndefined();
  });

  // Review of #27 and the criterion it added to #25: an entry that is not read is never dropped.
  it.each([
    ['a document with no kind', [{ uri: A, name: 'n', rules: [] }]],
    ['a document with no rules list', [{ uri: A, kind: 'k', name: 'n' }]],
    ['a document whose rules list is a string', [{ uri: A, kind: 'k', name: 'n', rules: 'x' }]],
  ])('shows %s as a warning row in its file, which counts as a problem', (_name, bad) => {
    const answer = read({ documents: [...bad, rawDocument(B, [rawRule(B, 'd', 0)])] });

    expect(answer.documents.map((document) => document.uri)).toEqual([A, B]);
    expect(answer.documents[0]).toMatchObject({ uri: A, unreadable: true, rules: [] });
    expect(answer.skipped).toBe(1);
    expect(answer.unattributed).toBe(0);
    const list = new RuleList();
    list.apply(answer, 'all');
    expect(list.problems).toBe(1);
  });

  it.each([
    ['a document that is no object', ['x']],
    ['a document with no uri', [{ kind: 'k', name: 'n', rules: [] }]],
  ])('counts %s as one with no file, and as a problem', (_name, bad) => {
    const answer = read({ documents: [...bad, rawDocument(B, [rawRule(B, 'd', 0)])] });

    expect(answer.documents.map((document) => document.uri)).toEqual([B]);
    expect(answer.unattributed).toBe(1);
    expect(answer.skipped).toBe(1);
    const list = new RuleList();
    list.apply(answer, 'all');
    expect(list.problems).toBe(1);
  });

  it.each([
    ['a rule that is no object', 'x'],
    ['a rule with no compile state', { id: 'i', location: {} }],
    ['a rule with no location', { id: 'i', compile: { state: 'ok', problems: 0 } }],
    [
      'a rule whose compile state it does not know',
      { ...rawRule(A, 'd', 1), compile: { state: 'perhaps', problems: 0 } },
    ],
  ])('shows %s as a warning row in its document, beside the rules it reads', (_name, bad) => {
    const answer = read({
      documents: [{ ...rawDocument(A, []), rules: [rawRule(A, 'd', 0), bad] }],
    });

    const [first, placeholder] = answer.documents[0]?.rules ?? [];
    expect(answer.documents[0]?.rules).toHaveLength(2);
    expect(first?.unreadable).toBeUndefined();
    expect(placeholder).toMatchObject({
      label: 'A rule that could not be read',
      unreadable: true,
      problem: true,
      index: 1,
      compile: { state: 'warning', problems: 1 },
    });
    expect(answer.skipped).toBe(1);
    const list = new RuleList();
    list.apply(answer, 'all');
    expect(list.problems).toBe(1);
  });

  it('puts the warning row of a rule where the rule is, when the engine said where', () => {
    const bad = { ...rawRule(A, 'd', 1), compile: { state: 'perhaps', problems: 0 } };

    const [, placeholder] =
      read({ documents: [rawDocument(A, [rawRule(A, 'd', 0), bad])] }).documents[0]?.rules ?? [];

    expect(placeholder?.id).toBe(rawRule(A, 'd', 1)['id']);
    expect(placeholder?.location).toEqual((rawRule(A, 'd', 1) as { location: unknown }).location);
  });

  it('puts the warning row of a rule with no location at the start of its document', () => {
    const [, placeholder] =
      read({ documents: [{ ...rawDocument(A, []), rules: [rawRule(A, 'd', 0), 'x'] }] })
        .documents[0]?.rules ?? [];

    expect(placeholder?.location).toEqual({
      uri: A,
      range: { start: { line: 0, character: 0 }, end: { line: 60, character: 0 } },
    });
    expect(placeholder?.id).toContain('#unreadable#1');
  });

  it('replaces the old rows of a file with the warning row when a since answer has the file unreadable', () => {
    const list = new RuleList();
    list.apply(
      read(
        rawAnswer([rawDocument(A, [rawRule(A, 'd', 0), rawRule(A, 'd', 1)])], { generation: 1 }),
      ),
      'all',
    );
    expect(list.problems).toBe(0);

    const change = list.apply(
      read(rawAnswer([{ uri: A, kind: 'k', name: 'n' }], { generation: 2 })),
      'since',
    );

    expect(change.files).toEqual([A]);
    expect(list.documentsOf(A)).toMatchObject([{ unreadable: true, rules: [] }]);
    expect(list.problems).toBe(1);
  });

  it('counts the problems of the files, follows each answer and drops a removed file’s', () => {
    const list = new RuleList();
    list.apply(
      read(
        rawAnswer(
          [
            rawDocument(A, [rawRule(A, 'd', 0, { compile: 'error' }), rawRule(A, 'd', 1)]),
            rawDocument(B, [rawRule(B, 'd', 0, { compile: 'warning' })]),
          ],
          { generation: 1 },
        ),
      ),
      'all',
    );
    expect(list.problems).toBe(2);

    list.apply(read(rawAnswer([rawDocument(A, [rawRule(A, 'd', 0)])], { generation: 2 })), 'since');
    expect(list.problems).toBe(1);

    list.apply(read(rawAnswer([], { generation: 3, removed: [B] })), 'since');
    expect(list.problems).toBe(0);

    list.apply(read(rawAnswer([rawDocument(A, [rawRule(A, 'd', 0, { problem: true })])])), 'all');
    expect(list.problems).toBe(1);
    list.reset();
    expect(list.problems).toBe(0);
  });

  it('keeps what it knows of entries with no file across a since answer, and replaces it on the whole list', () => {
    const list = new RuleList();
    list.apply(read({ generation: 1, documents: ['x', 'y'] }), 'all');
    expect(list.problems).toBe(2);

    list.apply(read({ generation: 2, documents: [] }), 'since');
    expect(list.problems).toBe(2);

    list.apply(read({ generation: 3, documents: [] }), 'all');
    expect(list.problems).toBe(0);
  });

  it('counts nothing skipped for an answer that is all read', () => {
    expect(read(rawAnswer([rawDocument(A, [rawRule(A, 'd', 0)])])).skipped).toBe(0);
  });

  it.each([
    ['an empty name, as the engine lists a document with no metadata.name', { name: '' }],
    ['no name at all', { name: undefined }],
  ])('reads a document with %s (review of #27)', (_what, over) => {
    const raw = rawDocument(A, [rawRule(A, 'CustomResourceDefinition', 0)], { name: 'x' });
    const answer = read({ documents: [{ ...raw, ...over, document: 'CustomResourceDefinition' }] });

    expect(answer.documents).toHaveLength(1);
    expect(answer.documents[0]).toMatchObject({ name: '', document: 'CustomResourceDefinition' });
    expect(answer.documents[0]?.rules).toHaveLength(1);
    expect(answer.skipped).toBe(0);
  });

  it('drops an example state it does not know, and keeps the rule', () => {
    const [document] = read(
      rawAnswer([
        rawDocument(A, [rawRule(A, 'd', 0, { examples: { state: 'maybe', words: 'w' } })]),
      ]),
    ).documents;

    expect(document?.rules[0]).not.toHaveProperty('examples');
  });
});

describe('parseRuleListChange', () => {
  it('reads the generation and the scan of a notification', () => {
    expect(
      parseRuleListChange({
        generation: 12,
        scan: { state: 'scanning', yamlFiles: 4210, filesWithRules: 37, filesLeftOut: 0 },
      }),
    ).toEqual({
      generation: 12,
      scan: { state: 'scanning', yamlFiles: 4210, filesWithRules: 37, filesLeftOut: 0 },
    });
  });

  it.each([null, 'x', {}, { generation: 'one' }])('reads %j as nothing', (value) => {
    expect(parseRuleListChange(value)).toBeUndefined();
  });
});

describe('parseScanLimits', () => {
  it('reads the limits the engine reports in its initialize result', () => {
    expect(
      parseScanLimits({
        capabilities: {
          experimental: {
            celLens: { workspaceScanMaxFiles: 20000, workspaceScanMaxFileBytes: 1048576 },
          },
        },
      }),
    ).toEqual({ files: 20000, fileBytes: 1048576 });
  });

  it.each([
    undefined,
    {},
    { capabilities: {} },
    { capabilities: { experimental: { celLens: 1 } } },
  ])('reads %j as no limits', (value) => {
    expect(parseScanLimits(value)).toEqual({});
  });
});

describe('RuleList', () => {
  it('gives the whole list from a series of answers applied to an empty one', () => {
    const list = new RuleList();

    const first = list.apply(read(rawAnswer([doc(A, 2), doc(C, 1)], { generation: 1 })), 'all');
    const second = list.apply(read(rawAnswer([doc(B, 3)], { generation: 2 })));
    const third = list.apply(read(rawAnswer([doc(A, 4)], { generation: 3 })));

    expect(first).toEqual({
      files: expect.arrayContaining([A, C]) as string[],
      full: true,
      stale: false,
    });
    expect(second).toEqual({ files: [B], full: false, stale: false });
    expect(third).toEqual({ files: [A], full: false, stale: false });
    expect(files(list)).toEqual([A, B, C]);
    expect(list.documentsOf(A)[0]?.rules).toHaveLength(4);
    expect(list.documentsOf(B)[0]?.rules).toHaveLength(3);
    expect(list.totals).toEqual({ documents: 3, rules: 8 });
    expect(list.generation).toBe(3);
  });

  it('replaces the whole list with an answer that is full', () => {
    const list = new RuleList();
    list.apply(read(rawAnswer([doc(A, 2), doc(B, 2)], { generation: 1 })));

    const change = list.apply(read(rawAnswer([doc(C, 1)], { generation: 5, full: true })));

    expect(files(list)).toEqual([C]);
    expect(change.full).toBe(true);
    expect(change.files).toEqual(expect.arrayContaining([A, B, C]));
    expect(list.generation).toBe(5);
  });

  it('replaces the whole list with the answer to a request for the whole list, whether or not it says full', () => {
    const list = new RuleList();
    list.apply(read(rawAnswer([doc(A, 2), doc(B, 2)], { generation: 1 })), 'all');

    list.apply(read(rawAnswer([doc(C, 1)], { generation: 2 })), 'all');

    expect(files(list)).toEqual([C]);
  });

  it('replaces the whole list with an answer that has no generation, as an engine without since answers', () => {
    const list = new RuleList();
    list.apply(read(rawAnswer([doc(A, 2), doc(B, 2)])));

    list.apply(read(rawAnswer([doc(B, 1)])));

    expect(files(list)).toEqual([B]);
    expect(list.generation).toBeUndefined();
  });

  it('drops the files an answer says were removed, and says they changed', () => {
    const list = new RuleList();
    list.apply(read(rawAnswer([doc(A, 1), doc(B, 1), doc(C, 1)], { generation: 1 })), 'all');

    const change = list.apply(
      read(rawAnswer([], { generation: 2, removed: [B, 'file:///no.yaml'] })),
    );

    expect(files(list)).toEqual([A, C]);
    expect(change.files).toEqual([B]);
  });

  it('puts a file a later answer brings among the files of its folder by path, in the engine’s order', () => {
    const list = new RuleList();
    list.apply(read(rawAnswer([doc(A, 1), doc(C, 1)], { generation: 1 })));
    list.apply(read(rawAnswer([doc(B, 1)], { generation: 2 })));
    list.apply(
      read(
        rawAnswer(
          [
            rawDocument('file:///elsewhere/z.yaml', [], {
              folder: null,
              file: 'file:///elsewhere/z.yaml',
            }),
          ],
          { generation: 3 },
        ),
      ),
    );
    list.apply(read(rawAnswer([doc('file:///repo/config/d.yaml', 1)], { generation: 4 })));

    expect(files(list)).toEqual([
      A,
      B,
      C,
      'file:///repo/config/d.yaml',
      'file:///elsewhere/z.yaml',
    ]);
  });

  it('keeps a file’s documents together, in the file’s order', () => {
    const list = new RuleList();
    list.apply(
      read(
        rawAnswer([doc(A, 1, 'one.example.com'), doc(A, 2, 'two.example.com')], { generation: 1 }),
      ),
    );

    expect(list.documentsOf(A).map((document) => document.name)).toEqual([
      'one.example.com',
      'two.example.com',
    ]);
    expect(files(list)).toEqual([A]);
  });

  it('leaves out an answer that is older than the list, and says so', () => {
    const list = new RuleList();
    list.apply(read(rawAnswer([doc(A, 2)], { generation: 5 })));

    const change = list.apply(read(rawAnswer([doc(A, 1)], { generation: 4 })));

    expect(change).toEqual({ files: [], full: false, stale: true });
    expect(list.documentsOf(A)[0]?.rules).toHaveLength(2);
    expect(list.generation).toBe(5);
  });

  it('starts over when the engine restarts: nothing is kept and no generation is asked after', () => {
    const list = new RuleList();
    list.apply(
      read(
        rawAnswer([doc(A, 2)], {
          generation: 5,
          scan: { state: 'scanning', yamlFiles: 1, filesWithRules: 1, filesLeftOut: 2 },
        }),
      ),
    );

    const change = list.reset();

    expect(change.files).toEqual([A]);
    expect(files(list)).toEqual([]);
    expect(list.generation).toBeUndefined();
    expect(list.hasAnswer).toBe(false);
    expect(list.scan).toMatchObject({ state: 'done', filesLeftOut: 0 });
    // The engine’s generations start again: an answer of generation 1 is the first, not an old one.
    list.apply(read(rawAnswer([doc(B, 1)], { generation: 1 })));
    expect(files(list)).toEqual([B]);
  });

  it('keeps the scan the last answer or notification gave', () => {
    const list = new RuleList();

    list.apply(
      read(
        rawAnswer([], {
          generation: 1,
          scan: { state: 'scanning', yamlFiles: 9, filesWithRules: 2, filesLeftOut: 0 },
        }),
      ),
    );
    expect(list.scan).toMatchObject({ state: 'scanning', yamlFiles: 9 });
    list.setScan({ state: 'done', yamlFiles: 20, filesWithRules: 5, filesLeftOut: 1203 });

    expect(list.scan).toMatchObject({ state: 'done', filesLeftOut: 1203 });
  });

  it('says to log an answer it cannot read once, until one it can read comes', () => {
    const list = new RuleList();

    expect(list.shouldLogUnread()).toBe(true);
    expect(list.shouldLogUnread()).toBe(false);
    list.apply(read(rawAnswer([doc(A, 1)], { generation: 1 })));
    expect(list.shouldLogUnread()).toBe(true);
  });

  it('holds a big list: 200 files of 25 rules', () => {
    const list = new RuleList();

    list.apply(read(rawAnswer(rawFiles(200, 25), { generation: 1 })));

    expect(list.totals).toEqual({ documents: 200, rules: 5000 });
  });
});
