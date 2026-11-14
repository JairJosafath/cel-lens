import { describe, expect, it } from 'vitest';

import { RuleList, parseRuleListAnswer, type ListChange, type RuleListAnswer } from './rule_list';
import { rawAnswer, rawDocument, rawRule } from './rule_list_samples';
import {
  RuleTree,
  filesLeftOutText,
  scanMessage,
  type TreeContext,
  type TreeNode,
} from './rule_tree';

const REPO = 'file:///repo';
const OTHER = 'file:///other';
const F = (name: string): string => `${REPO}/${name}`;

function read(value: unknown): RuleListAnswer {
  const answer = parseRuleListAnswer(value);
  if (answer === undefined) {
    throw new Error('the sample answer was not read');
  }
  return answer;
}

function crd(
  uri: string,
  name: string,
  rules: number,
  folder: string | null = REPO,
  file = uri.replace(`${REPO}/`, ''),
): Record<string, unknown> {
  const document = `CustomResourceDefinition ${name}`;
  return rawDocument(
    uri,
    Array.from({ length: rules }, (_, index) => rawRule(uri, document, index)),
    { name, folder, file },
  );
}

const ONE: TreeContext = {
  folders: [{ uri: REPO, name: 'repo' }],
  normalize: (uri) => uri,
  showExamples: () => true,
  limits: { files: 20000, fileBytes: 1048576 },
  problemsOnly: false,
};
const TWO: TreeContext = {
  ...ONE,
  folders: [
    { uri: REPO, name: 'repo' },
    { uri: OTHER, name: 'other' },
  ],
};

interface Drawn {
  readonly list: RuleList;
  readonly tree: RuleTree;
  apply(
    answer: unknown,
    asked?: 'all' | 'since',
  ): { change: ListChange; refresh: readonly (TreeNode | undefined)[] };
}

function draw(context: TreeContext = ONE, first?: unknown): Drawn {
  const list = new RuleList();
  const tree = new RuleTree(list, context);
  const drawn: Drawn = {
    list,
    tree,
    apply(answer, asked = 'since') {
      const change = list.apply(read(answer), asked);
      return { change, refresh: tree.update(change) };
    },
  };
  if (first !== undefined) {
    drawn.apply(first, 'all');
  }
  return drawn;
}

/** The value, which a test knows is there. */
function there<T>(value: T | undefined): T {
  if (value === undefined) {
    throw new Error('not there');
  }
  return value;
}

function shape(nodes: readonly TreeNode[]): unknown[] {
  return nodes.map((each) =>
    each.children.length === 0 ? each.label : { [each.label]: shape(each.children) },
  );
}

describe('the tree of one workspace folder', () => {
  const answer = rawAnswer(
    [crd(F('a.yaml'), 'a.example.com', 2), crd(F('b.yaml'), 'b.example.com', 1)],
    { generation: 1 },
  );

  it('lists the documents at the top, in the engine’s order, each with its rules', () => {
    const { tree } = draw(ONE, answer);

    expect(shape(tree.roots())).toEqual([
      { 'a.example.com': ['rule 0 says something', 'rule 1 says something'] },
      { 'b.example.com': ['rule 0 says something'] },
    ]);
  });

  it('draws a document as its name, kind and file, collapsed, and a rule as its label and words', () => {
    const [first] = draw(ONE, answer).tree.roots();

    expect(first).toMatchObject({
      kind: 'document',
      label: 'a.example.com',
      description: 'CRD · a.yaml',
      collapsible: 'collapsed',
      icon: { id: 'symbol-class' },
      contextValue: 'document',
    });
    expect(first?.children[0]).toMatchObject({
      kind: 'rule',
      description: 'compiles',
      collapsible: 'none',
      icon: { id: 'check' },
      action: 'openRule',
      contextValue: 'rule',
      location: { uri: F('a.yaml'), range: { start: { line: 10, character: 18 } } },
    });
  });

  it('gives every item the id the engine gives, so expansion and selection survive', () => {
    const [first] = draw(ONE, answer).tree.roots();

    expect(first?.children.map((each) => each.id)).toEqual([
      `${F('a.yaml')}#CustomResourceDefinition a.example.com#v1#spec.ports[]#0`,
      `${F('a.yaml')}#CustomResourceDefinition a.example.com#v1#spec.ports[]#1`,
    ]);
    expect(first?.id).toBe(`${F('a.yaml')}#CustomResourceDefinition a.example.com`);
  });

  it('ids a second document of the same name in a file apart, in the file’s order', () => {
    const { tree } = draw(
      ONE,
      rawAnswer([crd(F('a.yaml'), 'a.example.com', 1), crd(F('a.yaml'), 'a.example.com', 1)], {
        generation: 1,
      }),
    );

    expect(tree.roots().map((each) => each.id)).toEqual([
      `${F('a.yaml')}#CustomResourceDefinition a.example.com`,
      `${F('a.yaml')}#CustomResourceDefinition a.example.com#2`,
    ]);
  });

  it('has no row when the list holds no document', () => {
    const { tree } = draw(ONE, rawAnswer([], { generation: 1 }));

    expect(tree.roots()).toEqual([]);
    expect(tree.isEmpty).toBe(true);
  });

  it('puts open files outside every folder last, under Open files', () => {
    const { tree } = draw(
      ONE,
      rawAnswer(
        [
          crd(F('a.yaml'), 'a.example.com', 1),
          crd('file:///elsewhere/z.yaml', 'z.example.com', 1, null, 'file:///elsewhere/z.yaml'),
        ],
        { generation: 1 },
      ),
    );

    expect(shape(tree.roots())).toEqual([
      { 'a.example.com': ['rule 0 says something'] },
      { 'Open files': [{ 'z.example.com': ['rule 0 says something'] }] },
    ]);
    expect(tree.roots()[1]).toMatchObject({ kind: 'openFiles', collapsible: 'expanded' });
  });

  it('puts a document whose folder is none of the workspace’s under Open files too', () => {
    const { tree } = draw(
      ONE,
      rawAnswer([crd('file:///gone/a.yaml', 'a.example.com', 1, 'file:///gone')], {
        generation: 1,
      }),
    );

    expect(tree.roots().map((each) => each.label)).toEqual(['Open files']);
  });

  it('matches the engine’s folder and the workspace’s by the spelling normalize gives', () => {
    const { tree } = draw(
      {
        ...ONE,
        folders: [{ uri: 'FILE:///repo/', name: 'repo' }],
        normalize: (uri) => uri.toLowerCase().replace(/\/$/, ''),
      },
      rawAnswer([crd(F('a.yaml'), 'a.example.com', 1)], { generation: 1 }),
    );

    expect(tree.roots().map((each) => each.label)).toEqual(['a.example.com']);
  });
});

describe('the tree of several workspace folders', () => {
  const answer = rawAnswer(
    [
      crd(`${OTHER}/o.yaml`, 'o.example.com', 1, OTHER, 'o.yaml'),
      crd(F('a.yaml'), 'a.example.com', 1),
    ],
    { generation: 1 },
  );

  it('lists the folders in the workspace’s order, each with its name and its documents', () => {
    const { tree } = draw(TWO, answer);

    expect(shape(tree.roots())).toEqual([
      { repo: [{ 'a.example.com': ['rule 0 says something'] }] },
      { other: [{ 'o.example.com': ['rule 0 says something'] }] },
    ]);
    expect(tree.roots()[0]).toMatchObject({
      kind: 'folder',
      collapsible: 'expanded',
      icon: { id: 'folder' },
      id: `cel-lens.folder:${REPO}`,
    });
  });

  it('leaves out a folder with no document with rules', () => {
    const { tree } = draw(
      TWO,
      rawAnswer([crd(F('a.yaml'), 'a.example.com', 1)], { generation: 1 }),
    );

    expect(tree.roots().map((each) => each.label)).toEqual(['repo']);
  });

  it('puts Open files after the folders', () => {
    const { tree } = draw(
      TWO,
      rawAnswer(
        [
          crd('file:///x/z.yaml', 'z.example.com', 1, null, 'file:///x/z.yaml'),
          crd(F('a.yaml'), 'a.example.com', 1),
        ],
        { generation: 1 },
      ),
    );

    expect(tree.roots().map((each) => each.label)).toEqual(['repo', 'Open files']);
  });
});

describe('the example words of a folder that turns them off', () => {
  const examples = { state: 'wrong', words: 'refuses 1 example it should allow · 2 pass' };
  const answer = rawAnswer(
    [
      rawDocument(F('a.yaml'), [rawRule(F('a.yaml'), 'd', 0, { examples })]),
      rawDocument(`${OTHER}/o.yaml`, [rawRule(`${OTHER}/o.yaml`, 'd', 0, { examples })], {
        folder: OTHER,
        file: 'o.yaml',
      }),
    ],
    { generation: 1 },
  );

  it('shows none, and no example icon, for the rules of that folder only', () => {
    const { tree } = draw({ ...TWO, showExamples: (uri) => !uri.startsWith(OTHER) }, answer);

    const [repo, other] = tree.roots();
    expect(repo?.children[0]?.children[0]).toMatchObject({
      description: 'compiles · refuses 1 example it should allow · 2 pass',
      icon: { id: 'warning' },
      contextValue: 'ruleWithExamples',
    });
    expect(other?.children[0]?.children[0]).toMatchObject({
      description: 'compiles',
      icon: { id: 'check' },
      contextValue: 'rule',
    });
  });

  it('draws again when the setting changes, keeping the nodes', () => {
    const drawn = draw(TWO, answer);
    const before = drawn.tree.roots()[1]?.children[0]?.children[0];

    const refresh = drawn.tree.rebuild({ ...TWO, showExamples: () => false });

    expect(refresh).toEqual([undefined]);
    const after = drawn.tree.roots()[1]?.children[0]?.children[0];
    expect(after).toBe(before);
    expect(after?.description).toBe('compiles');
  });
});

describe('an update', () => {
  const base = rawAnswer(
    [crd(F('a.yaml'), 'a.example.com', 2), crd(F('c.yaml'), 'c.example.com', 1)],
    { generation: 1 },
  );

  it('refreshes only the document an answer changed, and keeps the node objects of the others', () => {
    const drawn = draw(ONE, base);
    const [a, c] = drawn.tree.roots();
    const aRules = [...(a?.children ?? [])];

    const { refresh } = drawn.apply(
      rawAnswer([crd(F('a.yaml'), 'a.example.com', 3)], { generation: 2 }),
    );

    expect(refresh).toEqual([a]);
    expect(refresh[0]).toBe(a);
    expect(drawn.tree.roots()[0]).toBe(a);
    expect(drawn.tree.roots()[1]).toBe(c);
    expect(a?.children).toHaveLength(3);
    expect(a?.children[0]).toBe(aRules[0]);
  });

  it('refreshes only the document, not its folder, when a rule changes in a folder of several', () => {
    const drawn = draw(
      TWO,
      rawAnswer(
        [
          crd(F('a.yaml'), 'a.example.com', 1),
          crd(`${OTHER}/o.yaml`, 'o.example.com', 1, OTHER, 'o.yaml'),
        ],
        { generation: 1 },
      ),
    );
    const document = drawn.tree.roots()[0]?.children[0];

    const { refresh } = drawn.apply(
      rawAnswer([crd(F('a.yaml'), 'a.example.com', 2)], { generation: 2 }),
    );

    expect(refresh).toEqual([document]);
  });

  it('refreshes the root when a document is renamed in its file, which gives it another id', () => {
    const drawn = draw(ONE, base);

    const { refresh } = drawn.apply(
      rawAnswer([crd(F('a.yaml'), 'renamed.example.com', 2)], { generation: 2 }),
    );

    expect(refresh).toEqual([undefined]);
    expect(drawn.tree.roots().map((each) => each.label)).toEqual([
      'renamed.example.com',
      'c.example.com',
    ]);
  });

  it('refreshes nothing when an answer repeats a file as it was', () => {
    const drawn = draw(ONE, base);

    const { refresh } = drawn.apply(
      rawAnswer([crd(F('a.yaml'), 'a.example.com', 2)], { generation: 2 }),
    );

    expect(refresh).toEqual([]);
  });

  it('refreshes the changed document alone when the answer is the whole list and one file differs', () => {
    const drawn = draw(ONE, base);
    const [a] = drawn.tree.roots();

    const { refresh } = drawn.apply(
      rawAnswer([crd(F('a.yaml'), 'a.example.com', 1), crd(F('c.yaml'), 'c.example.com', 1)]),
      'all',
    );

    expect(refresh).toEqual([a]);
  });

  it('refreshes the root when a document comes, in the engine’s order', () => {
    const drawn = draw(ONE, base);

    const { refresh } = drawn.apply(
      rawAnswer([crd(F('b.yaml'), 'b.example.com', 1)], { generation: 2 }),
    );

    expect(refresh).toEqual([undefined]);
    expect(drawn.tree.roots().map((each) => each.label)).toEqual([
      'a.example.com',
      'b.example.com',
      'c.example.com',
    ]);
  });

  it('refreshes the root when a document goes with its file', () => {
    const drawn = draw(ONE, base);

    const { refresh } = drawn.apply(rawAnswer([], { generation: 2, removed: [F('a.yaml')] }));

    expect(refresh).toEqual([undefined]);
    expect(drawn.tree.roots().map((each) => each.label)).toEqual(['c.example.com']);
    expect(
      drawn.tree.find(`${F('a.yaml')}#CustomResourceDefinition a.example.com`),
    ).toBeUndefined();
  });

  it('refreshes the folder, not the root, when a document comes or goes in a folder that stays', () => {
    const drawn = draw(
      TWO,
      rawAnswer(
        [
          crd(F('a.yaml'), 'a.example.com', 1),
          crd(`${OTHER}/o.yaml`, 'o.example.com', 1, OTHER, 'o.yaml'),
        ],
        {
          generation: 1,
        },
      ),
    );
    const [repo] = drawn.tree.roots();

    const { refresh } = drawn.apply(
      rawAnswer([crd(F('b.yaml'), 'b.example.com', 1)], { generation: 2 }),
    );

    expect(refresh).toEqual([repo]);
    expect(repo?.children.map((each) => each.label)).toEqual(['a.example.com', 'b.example.com']);
  });

  it('refreshes the root when the first document of a folder comes, and when the last goes', () => {
    const drawn = draw(TWO, rawAnswer([crd(F('a.yaml'), 'a.example.com', 1)], { generation: 1 }));

    const added = drawn.apply(
      rawAnswer([crd(`${OTHER}/o.yaml`, 'o.example.com', 1, OTHER, 'o.yaml')], { generation: 2 }),
    );
    const removed = drawn.apply(rawAnswer([], { generation: 3, removed: [F('a.yaml')] }));

    expect(added.refresh).toEqual([undefined]);
    expect(removed.refresh).toEqual([undefined]);
    expect(drawn.tree.roots().map((each) => each.label)).toEqual(['other']);
  });

  it('draws a rule’s new state in the node it had, so its handle stays good', () => {
    const drawn = draw(ONE, base);
    const rule = drawn.tree.roots()[0]?.children[0];

    drawn.apply(
      rawAnswer(
        [
          rawDocument(
            F('a.yaml'),
            [
              rawRule(F('a.yaml'), 'CustomResourceDefinition a.example.com', 0, {
                compile: 'error',
              }),
              rawRule(F('a.yaml'), 'CustomResourceDefinition a.example.com', 1),
            ],
            { name: 'a.example.com' },
          ),
        ],
        { generation: 2 },
      ),
    );

    expect(drawn.tree.roots()[0]?.children[0]).toBe(rule);
    expect(rule).toMatchObject({ description: '1 problem', icon: { id: 'error' } });
  });

  it('leaves the tree as it was for an answer that is older than the list', () => {
    const drawn = draw(ONE, base);

    const { refresh } = drawn.apply(
      rawAnswer([crd(F('a.yaml'), 'a.example.com', 5)], { generation: 0 }),
    );

    expect(refresh).toEqual([]);
    expect(drawn.tree.roots()[0]?.children).toHaveLength(2);
  });

  it('moves a document to Open files when its file is outside every folder later', () => {
    const drawn = draw(ONE, base);

    const { refresh } = drawn.apply(
      rawAnswer([crd(F('a.yaml'), 'a.example.com', 2, null)], { generation: 2 }),
    );

    expect(refresh).toEqual([undefined]);
    expect(drawn.tree.roots().map((each) => each.label)).toEqual(['c.example.com', 'Open files']);
  });
});

describe('the row of files left out', () => {
  it('is the first row, with the limits the engine reported, and shows the logs when clicked', () => {
    const drawn = draw(ONE, rawAnswer([crd(F('a.yaml'), 'a.example.com', 1)], { generation: 1 }));

    const refresh = drawn.tree.setFilesLeftOut(1203);

    expect(refresh).toEqual([undefined]);
    expect(drawn.tree.roots()[0]).toMatchObject({
      kind: 'warning',
      label: '1,203 YAML files not read: the scan reads at most 20,000 files of at most 1 MiB',
      icon: { id: 'warning', color: 'problemsWarningIcon.foreground' },
      action: 'showLogs',
    });
    expect(drawn.tree.roots()[1]?.label).toBe('a.example.com');
  });

  it('is the first row of several folders too, and goes when none is left out', () => {
    const drawn = draw(TWO, rawAnswer([crd(F('a.yaml'), 'a.example.com', 1)], { generation: 1 }));

    drawn.tree.setFilesLeftOut(2);
    expect(drawn.tree.roots().map((each) => each.kind)).toEqual(['warning', 'folder']);
    expect(drawn.tree.setFilesLeftOut(0)).toEqual([undefined]);

    expect(drawn.tree.roots().map((each) => each.kind)).toEqual(['folder']);
  });

  it('refreshes nothing when its words are the same', () => {
    const drawn = draw(ONE, rawAnswer([], { generation: 1 }));
    drawn.tree.setFilesLeftOut(5);

    expect(drawn.tree.setFilesLeftOut(5)).toEqual([]);
    expect(drawn.tree.setFilesLeftOut(5)).toEqual([]);
  });

  it('is a row of its own, so the tree is not empty while it shows', () => {
    const drawn = draw(ONE, rawAnswer([], { generation: 1 }));

    drawn.tree.setFilesLeftOut(5);

    expect(drawn.tree.isEmpty).toBe(false);
  });

  it.each([
    [
      1,
      { files: 20000, fileBytes: 1048576 },
      '1 YAML files not read: the scan reads at most 20,000 files of at most 1 MiB',
    ],
    [
      30000,
      { files: 1000, fileBytes: 2048 },
      '30,000 YAML files not read: the scan reads at most 1,000 files of at most 2 KiB',
    ],
    [7, { files: 20000 }, "7 YAML files not read: the scan's limits left them out"],
    [7, {}, "7 YAML files not read: the scan's limits left them out"],
  ])('says %i files with the limits %j', (count, limits, words) => {
    expect(filesLeftOutText(count, limits)).toBe(words);
  });
});

describe('the message of a scan', () => {
  it('says how many YAML files were read and how many have rules', () => {
    expect(scanMessage(4210, 37)).toBe('Scanning: 4,210 YAML files read, 37 with rules');
  });
});

describe('the filter for problems', () => {
  const ONLY: TreeContext = { ...ONE, problemsOnly: true };
  const mixed = rawAnswer(
    [
      rawDocument(
        F('a.yaml'),
        [
          rawRule(F('a.yaml'), 'CustomResourceDefinition a.example.com', 0),
          rawRule(F('a.yaml'), 'CustomResourceDefinition a.example.com', 1, { compile: 'error' }),
        ],
        { name: 'a.example.com', folder: REPO, file: 'a.yaml' },
      ),
      crd(F('b.yaml'), 'b.example.com', 2),
    ],
    { generation: 1 },
  );

  it('shows the rules with a problem, with their documents, and no document with none', () => {
    const { tree } = draw(ONLY, mixed);

    expect(shape(tree.roots())).toEqual([{ 'a.example.com': ['rule 1 says something'] }]);
  });

  it('shows every rule when the filter is off', () => {
    const { tree } = draw(ONE, mixed);

    expect(shape(tree.roots())).toEqual([
      { 'a.example.com': ['rule 0 says something', 'rule 1 says something'] },
      { 'b.example.com': ['rule 0 says something', 'rule 1 says something'] },
    ]);
  });

  it('shows only the folders that have a rule with a problem', () => {
    const { tree } = draw(
      { ...TWO, problemsOnly: true },
      rawAnswer(
        [
          (mixed['documents'] as Record<string, unknown>[])[0] ?? {},
          crd(`${OTHER}/o.yaml`, 'o.example.com', 1, OTHER, 'o.yaml'),
        ],
        { generation: 1 },
      ),
    );

    expect(tree.roots().map((each) => each.label)).toEqual(['repo']);
  });

  it('counts, in a document’s accessible label, the rules it shows', () => {
    const { tree } = draw(ONLY, mixed);

    expect(tree.roots()[0]?.accessible).toBe('a.example.com, CRD, 1 rule, 1 with a problem');
  });

  it('switches with a rebuild, keeping the nodes of what shows both ways', () => {
    const drawn = draw(ONE, mixed);
    const [a] = drawn.tree.roots();
    const rule = a?.children[1];
    const hidden = there(a?.children[0]).id;

    const on = drawn.tree.rebuild(ONLY);
    const off = drawn.tree.rebuild(ONE);

    expect(on).toEqual([undefined]);
    expect(off).toEqual([undefined]);
    // What the filter hid is not left behind: after a switch on, its rule has no node (and none by id).
    drawn.tree.rebuild(ONLY);
    expect(drawn.tree.find(hidden)).toBeUndefined();
    expect(drawn.tree.find(there(rule).id)).toBe(rule);
    drawn.tree.rebuild(ONE);
    expect(drawn.tree.roots()[0]).toBe(a);
    expect(drawn.tree.roots()[0]?.children[1]).toBe(rule);
    expect(drawn.tree.roots()).toHaveLength(2);
  });

  it('keeps the filter while answers come: a rule that gets a problem shows, one that loses it goes', () => {
    const drawn = draw(ONLY, mixed);

    drawn.apply(
      rawAnswer(
        [
          rawDocument(
            F('a.yaml'),
            [
              rawRule(F('a.yaml'), 'CustomResourceDefinition a.example.com', 0, {
                compile: 'warning',
              }),
              rawRule(F('a.yaml'), 'CustomResourceDefinition a.example.com', 1),
            ],
            { name: 'a.example.com', folder: REPO, file: 'a.yaml' },
          ),
        ],
        { generation: 2 },
      ),
    );

    expect(shape(drawn.tree.roots())).toEqual([{ 'a.example.com': ['rule 0 says something'] }]);
  });

  it('draws nothing when no rule has a problem', () => {
    const { tree } = draw(
      ONLY,
      rawAnswer([crd(F('b.yaml'), 'b.example.com', 2)], { generation: 1 }),
    );

    expect(tree.roots()).toEqual([]);
    expect(tree.isEmpty).toBe(true);
  });
});

describe('entries the extension could not read', () => {
  const broken = { uri: F('x.yaml'), folder: REPO, file: 'x.yaml', kind: 'k', name: 'x' };

  it('draws a document that could not be read as a warning row in its place, with the file, that opens the file', () => {
    const { tree } = draw(
      ONE,
      rawAnswer([crd(F('a.yaml'), 'a.example.com', 1), broken], { generation: 1 }),
    );

    const [, row] = tree.roots();
    expect(row).toMatchObject({
      kind: 'unreadable',
      label: 'A document that could not be read',
      description: 'x.yaml',
      icon: { id: 'warning', color: 'problemsWarningIcon.foreground' },
      collapsible: 'none',
      action: 'openRule',
      location: { uri: F('x.yaml'), range: { start: { line: 0, character: 0 } } },
    });
    expect(row?.accessible).toContain('Warning: A document that could not be read');
  });

  it('draws a rule that could not be read as a warning row in its document, among the others', () => {
    const doc = {
      ...crd(F('a.yaml'), 'a.example.com', 1),
      rules: [rawRule(F('a.yaml'), 'CustomResourceDefinition a.example.com', 0), 'x'],
    };
    const { tree } = draw(ONE, rawAnswer([doc], { generation: 1 }));

    const [document] = tree.roots();
    expect(document?.children.map((each) => each.label)).toEqual([
      'rule 0 says something',
      'A rule that could not be read',
    ]);
    expect(document?.children[1]).toMatchObject({
      description: '1 problem',
      icon: { id: 'warning' },
    });
    expect(document?.children[1]?.accessible).toContain('warning, 1 problem');
  });

  it('keeps the warning rows when the filter shows problems only', () => {
    const doc = {
      ...crd(F('a.yaml'), 'a.example.com', 1),
      rules: [rawRule(F('a.yaml'), 'CustomResourceDefinition a.example.com', 0), 'x'],
    };
    const { tree } = draw(
      { ...ONE, problemsOnly: true },
      rawAnswer([doc, broken], { generation: 1 }),
    );

    expect(shape(tree.roots())).toEqual([
      { 'a.example.com': ['A rule that could not be read'] },
      'A document that could not be read',
    ]);
  });

  it('replaces the rows of a file with the warning row when a since answer has the file unreadable', () => {
    const drawn = draw(
      ONE,
      rawAnswer([crd(F('x.yaml'), 'x.example.com', 2), crd(F('c.yaml'), 'c.example.com', 1)], {
        generation: 1,
      }),
    );

    const { refresh } = drawn.apply(rawAnswer([broken], { generation: 2 }));

    expect(refresh).toEqual([undefined]);
    expect(shape(drawn.tree.roots())).toEqual([
      'A document that could not be read',
      { 'c.example.com': ['rule 0 says something'] },
    ]);
    expect(
      drawn.tree.find(`${F('x.yaml')}#CustomResourceDefinition x.example.com`),
    ).toBeUndefined();
  });

  it('draws a row at the top for entries with no file, which says how many and shows the logs', () => {
    const drawn = draw(ONE, rawAnswer([crd(F('a.yaml'), 'a.example.com', 1)], { generation: 1 }));

    expect(drawn.tree.setUnreadable(2)).toEqual([undefined]);

    expect(drawn.tree.roots()[0]).toMatchObject({
      kind: 'warning',
      label: '2 entries of the engine’s list could not be read',
      action: 'showLogs',
    });
    expect(drawn.tree.setUnreadable(2)).toEqual([]);
    drawn.tree.setUnreadable(1);
    expect(drawn.tree.roots()[0]?.label).toBe('1 entry of the engine’s list could not be read');
    drawn.tree.setUnreadable(0);
    expect(drawn.tree.roots().map((each) => each.kind)).toEqual(['document']);
  });

  it('puts the row for entries with no file after the row of files left out', () => {
    const drawn = draw(ONE, rawAnswer([crd(F('a.yaml'), 'a.example.com', 1)], { generation: 1 }));

    drawn.tree.setUnreadable(1);
    drawn.tree.setFilesLeftOut(3);

    expect(drawn.tree.roots().map((each) => each.label.slice(0, 5))).toEqual([
      '3 YAM',
      '1 ent',
      'a.exa',
    ]);
  });
});

describe('the row at a position', () => {
  const answer = rawAnswer(
    [
      {
        ...crd(F('a.yaml'), 'a.example.com', 2),
        range: { start: { line: 0, character: 0 }, end: { line: 40, character: 0 } },
      },
      crd(F('b.yaml'), 'b.example.com', 1),
    ],
    { generation: 1 },
  );

  it('is the node of the rule whose span holds the position', () => {
    const { tree } = draw(ONE, answer);

    expect(tree.at(F('a.yaml'), 15, 4)).toBe(tree.roots()[0]?.children[1]);
    expect(tree.at(F('a.yaml'), 11, 4)).toBe(tree.roots()[0]?.children[0]);
  });

  it('is the node of the document between two rules', () => {
    const { tree } = draw(ONE, answer);

    expect(tree.at(F('a.yaml'), 30, 0)).toBe(tree.roots()[0]);
  });

  it('is nothing outside the documents, in a file the list does not have, and in a document with no range', () => {
    const { tree } = draw(ONE, answer);

    expect(tree.at(F('a.yaml'), 90, 0)).toBeUndefined();
    expect(tree.at(F('nope.yaml'), 1, 0)).toBeUndefined();
    expect(tree.at(F('b.yaml'), 90, 0)).toBeUndefined();
  });

  it('is the document, not the rule, when the filter hides the rule', () => {
    const { tree } = draw(
      { ...ONE, problemsOnly: true },
      rawAnswer(
        [
          {
            ...rawDocument(
              F('a.yaml'),
              [
                rawRule(F('a.yaml'), 'CustomResourceDefinition a.example.com', 0),
                rawRule(F('a.yaml'), 'CustomResourceDefinition a.example.com', 1, {
                  compile: 'error',
                }),
              ],
              { name: 'a.example.com', folder: REPO, file: 'a.yaml' },
            ),
          },
        ],
        { generation: 1 },
      ),
    );

    expect(tree.at(F('a.yaml'), 11, 4)).toBe(tree.roots()[0]);
    expect(tree.at(F('a.yaml'), 15, 4)).toBe(tree.roots()[0]?.children[0]);
  });

  it('is nothing when the filter hides the document too', () => {
    const { tree } = draw({ ...ONE, problemsOnly: true }, answer);

    expect(tree.at(F('a.yaml'), 11, 4)).toBeUndefined();
    expect(tree.at(F('a.yaml'), 30, 0)).toBeUndefined();
  });

  // Review of #30: the list holds new objects when an answer sends a file unchanged.
  it.each([
    ['a since answer', 'since' as const, 2],
    ['a full answer, as after a restart of the engine', 'all' as const, 2],
  ])(
    'finds the document between two rules after %s that sends the file unchanged',
    (_what, asked, generation) => {
      const drawn = draw(ONE, answer);
      const document = drawn.tree.roots()[0];

      const { refresh } = drawn.apply(
        rawAnswer(
          [
            {
              ...crd(F('a.yaml'), 'a.example.com', 2),
              range: { start: { line: 0, character: 0 }, end: { line: 40, character: 0 } },
            },
            crd(F('b.yaml'), 'b.example.com', 1),
          ],
          { generation },
        ),
        asked,
      );

      expect(refresh).toEqual([]);
      expect(drawn.tree.at(F('a.yaml'), 30, 0)).toBe(document);
      expect(drawn.tree.at(F('a.yaml'), 15, 4)).toBe(document?.children[1]);
    },
  );

  it('finds the second document of one name in a file by its own node, with the filter on or off', () => {
    const twice = (): Record<string, unknown> =>
      rawAnswer(
        [
          {
            ...rawDocument(F('a.yaml'), [rawRule(F('a.yaml'), 'd', 0)], { name: 'same' }),
            range: { start: { line: 0, character: 0 }, end: { line: 20, character: 0 } },
          },
          {
            ...rawDocument(F('a.yaml'), [rawRule(F('a.yaml'), 'd2', 0, { compile: 'error' })], {
              name: 'same',
            }),
            range: { start: { line: 21, character: 0 }, end: { line: 40, character: 0 } },
          },
        ],
        { generation: 1 },
      );
    const all = draw(ONE, twice());
    const only = draw({ ...ONE, problemsOnly: true }, twice());

    expect(all.tree.at(F('a.yaml'), 30, 0)).toBe(all.tree.roots()[1]);
    expect(all.tree.roots()[1]?.id).toBe(`${F('a.yaml')}#CustomResourceDefinition same#2`);
    expect(only.tree.at(F('a.yaml'), 30, 0)).toBe(only.tree.roots()[0]);
    expect(only.tree.roots()[0]?.id).toBe(`${F('a.yaml')}#CustomResourceDefinition same#2`);
    expect(only.tree.at(F('a.yaml'), 10, 0)).toBeUndefined();
  });
});
