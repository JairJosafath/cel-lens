import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  MarkdownString,
  ThemeIcon,
  Uri,
  addWorkspaceFolder,
  changeConfiguration,
  changeDocument,
  changeWorkspaceFolders,
  closeDocument,
  contextKeys,
  executedCommands,
  fileTexts,
  FakeTextDocument,
  openDocuments,
  openedByApi,
  openDocument,
  progressRuns,
  reset,
  saveDocument,
  settle,
  settings,
  treeViews,
} from '../test/vscode';
import { SHOW_EXAMPLE_RESULTS_COMMAND } from './example_results';
import { W, crd, labels, rig, start, state, there, type Answer, type Rig } from './rule_view_rig';
import { SHOW_RULE_COMMAND } from './rule_parts';
import { rawAnswer, rawDocument, rawRule } from './rule_list_samples';
import type { TreeNode } from './rule_tree';
import { CHANGE_PAUSE_MS, ruleLocationOf } from './rule_view';
import { KYVERNO_OFF_KEY, RULE_VIEW_ID } from './rule_view_state';

beforeEach(() => {
  addWorkspaceFolder('repo');
});

afterEach(() => {
  vi.useRealTimers();
  reset();
});

describe('the view', () => {
  it('is the tree view cel-lens.rules, with the view as its provider', () => {
    const { view } = rig();

    expect(treeViews.map((each) => each.viewId)).toEqual([RULE_VIEW_ID]);
    expect(treeViews[0]?.provider).toBe(view);
  });

  it('asks the engine for the whole list, with no argument, once the engine runs, and draws it', async () => {
    const made = rig([rawAnswer([crd('a.yaml', 'a.example.com', 2)], { generation: 1 })]);

    await start(made);

    expect(made.run.mock.calls).toEqual([['cel-lens.ruleList', undefined]]);
    expect(labels(made.view.getChildren())).toEqual(['a.example.com']);
    expect(labels(made.view.getChildren(made.view.getChildren()[0]))).toEqual([
      'rule 0 says something',
      'rule 1 says something',
    ]);
    expect(state()).toBe('tree');
  });

  it('asks for what changed since the generation it has, and draws the answer file by file', async () => {
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1), crd('b.yaml', 'b.example.com', 1)], {
        generation: 4,
      }),
      rawAnswer([crd('a.yaml', 'a.example.com', 2)], { generation: 5 }),
    ]);
    await start(made);
    const [a, b] = made.view.getChildren();

    await made.view.refresh();

    expect(made.run.mock.calls[1]).toEqual(['cel-lens.ruleList', { since: 4 }]);
    expect(made.view.getChildren()).toEqual([a, b]);
    expect(made.view.getChildren()[0]).toBe(a);
    expect(made.view.getChildren()[1]).toBe(b);
    expect(a?.children).toHaveLength(2);
  });

  it('asks for the whole list each time of an engine that gives no generation', async () => {
    const made = rig([rawAnswer([crd('a.yaml', 'a.example.com', 1)])]);
    await start(made);

    await made.view.refresh();
    await made.view.refresh();

    expect(made.run.mock.calls).toEqual([
      ['cel-lens.ruleList', undefined],
      ['cel-lens.ruleList', undefined],
      ['cel-lens.ruleList', undefined],
    ]);
  });

  it('fires a tree change only for the documents an answer changed', async () => {
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1), crd('b.yaml', 'b.example.com', 1)], {
        generation: 1,
      }),
      rawAnswer([crd('a.yaml', 'a.example.com', 3)], { generation: 2 }),
      rawAnswer([crd('a.yaml', 'a.example.com', 3)], { generation: 3 }),
    ]);
    await start(made);
    const [a] = made.view.getChildren();
    const fired: unknown[] = [];
    made.view.onDidChangeTreeData((change) => fired.push(change));

    await made.view.refresh();
    await made.view.refresh();

    expect(fired).toEqual([[a]]);
  });

  it('fires for the root when a document comes', async () => {
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 1 }),
      rawAnswer([crd('b.yaml', 'b.example.com', 1)], { generation: 2 }),
    ]);
    await start(made);
    const fired: unknown[] = [];
    made.view.onDidChangeTreeData((change) => fired.push(change));

    await made.view.refresh();

    expect(fired).toEqual([undefined]);
    expect(labels(made.view.getChildren())).toEqual(['a.example.com', 'b.example.com']);
  });

  it('asks again once when told to while it is asking, never two at a time', async () => {
    const made = rig();
    let release: (answer: unknown) => void = () => undefined;
    const inFlight: number[] = [];
    let open = 0;
    made.run.mockImplementation(() => {
      open += 1;
      inFlight.push(open);
      return new Promise((resolve) => {
        release = (answer) => {
          open -= 1;
          resolve(answer);
        };
      });
    });
    made.view.engineStarting();
    made.view.engineStarted({});
    await settle();

    void made.view.refresh();
    void made.view.refresh();
    release(rawAnswer([], { generation: 1 }));
    await settle();
    release(rawAnswer([], { generation: 2 }));
    await settle();

    expect(made.run.mock.calls).toEqual([
      ['cel-lens.ruleList', undefined],
      ['cel-lens.ruleList', { since: 1 }],
    ]);
    expect(Math.max(...inFlight)).toBe(1);
  });

  it('asks nothing of an engine that is not running', async () => {
    const made = rig();

    await made.view.refresh();
    made.view.engineStopped();
    await made.view.refresh();

    expect(made.run).not.toHaveBeenCalled();
  });
});

describe('the engine restarting', () => {
  it('forgets the list, and the generation, so the new engine is asked for the whole list', async () => {
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 9 }),
      rawAnswer([crd('b.yaml', 'b.example.com', 1)], { generation: 1 }),
    ]);
    await start(made);

    made.view.engineStopped();
    expect(made.view.getChildren()).toEqual([]);
    await start(made);

    expect(made.run.mock.calls[1]).toEqual(['cel-lens.ruleList', undefined]);
    expect(labels(made.view.getChildren())).toEqual(['b.example.com']);
  });

  it('drops the answer of an engine that has gone', async () => {
    const made = rig();
    let release: (answer: unknown) => void = () => undefined;
    made.run.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          release = resolve;
        }),
    );
    made.view.engineStarting();
    made.view.engineStarted({});
    made.view.engineStopped();

    release(rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 1 }));
    await settle();

    expect(made.view.getChildren()).toEqual([]);
    expect(state()).toBe('engineDown');
  });

  it('follows the controller’s states: starting and every state of an engine that is gone', () => {
    const made = rig();

    made.view.engineState({ kind: 'starting' });
    expect(state()).toBe('scanning');
    made.view.engineState({ kind: 'failed' });
    expect(state()).toBe('engineDown');
    made.view.engineState({ kind: 'starting' });
    made.view.engineState({ kind: 'crashed' });
    expect(state()).toBe('engineDown');
    made.view.engineState({ kind: 'starting' });
    made.view.engineState({ kind: 'stopped' });
    expect(state()).toBe('engineDown');
    made.view.engineState({ kind: 'starting' });
    made.view.engineState({ kind: 'running', info: undefined });
    expect(state()).toBe('scanning');
  });
});

describe('what the view says with no row', () => {
  it('says no folder is open', async () => {
    reset();
    const made = rig([rawAnswer([], { generation: 1 })]);

    await start(made);

    expect(state()).toBe('noFolder');
  });

  it('says the engine is not running', async () => {
    const made = rig();
    await start(made);

    made.view.engineStopped();

    expect(state()).toBe('engineDown');
  });

  it('says the engine cannot list rules when it refuses the command and never answered, and logs it once', async () => {
    const made = rig([new Error('unknown command cel-lens.ruleList')]);

    await start(made);
    await made.view.refresh();

    expect(state()).toBe('unsupported');
    expect(made.log.lines.filter((line) => line.includes('could not list the rules'))).toHaveLength(
      1,
    );
  });

  // Review of #27: only an engine that does not know the command cannot list rules.
  it.each([
    ['a cancelled request', Object.assign(new Error('Request cancelled'), { code: -32800 })],
    ['a content that was modified', Object.assign(new Error('ContentModified'), { code: -32801 })],
    ['a plain failure', new Error('the engine is busy')],
  ])(
    'does not say the engine cannot list rules for %s, and stops the progress bar',
    async (_what, error) => {
      const made = rig([error]);

      await start(made);
      await settle();

      expect(state()).toBe('scanning');
      expect(progressRuns.at(-1)?.done).toBe(true);
      expect(made.log.lines.some((line) => line.includes('could not list the rules'))).toBe(true);
    },
  );

  it('asks again after a failure, and draws the list when it comes', async () => {
    const made = rig([
      new Error('Request cancelled'),
      rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 1 }),
    ]);
    await start(made);

    await made.view.refresh();

    expect(labels(made.view.getChildren())).toEqual(['a.example.com']);
    expect(state()).toBe('tree');
  });

  it.each([
    ['a method not found response', Object.assign(new Error('x'), { code: -32601 })],
    ['an unknown command message', new Error('unknown command: cel-lens.ruleList')],
  ])('says the engine cannot list rules for %s', async (_what, error) => {
    const made = rig([error]);

    await start(made);

    expect(state()).toBe('unsupported');
  });

  it('says the engine cannot list rules, and asks nothing, when it lists its commands and not this one', async () => {
    const made = rig();
    made.view.engineStarting();

    made.view.engineStarted({
      capabilities: { executeCommandProvider: { commands: ['cel-lens.setType'] } },
    });
    await settle();
    await made.view.refresh();

    expect(made.run).not.toHaveBeenCalled();
    expect(state()).toBe('unsupported');
  });

  it('asks an engine that lists its commands including this one', async () => {
    const made = rig();
    made.view.engineStarting();

    made.view.engineStarted({
      capabilities: { executeCommandProvider: { commands: ['cel-lens.ruleList'] } },
    });
    await settle();

    expect(made.run).toHaveBeenCalledTimes(1);
  });

  it('says the engine is looking while it starts and until the first answer', async () => {
    const made = rig();
    made.view.engineStarting();
    expect(state()).toBe('scanning');
    made.run.mockImplementation(() => new Promise(() => undefined));

    made.view.engineStarted({});
    await settle();

    expect(state()).toBe('scanning');
  });

  it('says nothing was found when the first answer holds no document and the scan is done', async () => {
    const made = rig([
      rawAnswer([], { generation: 1, scan: { state: 'done', yamlFiles: 120, filesWithRules: 0 } }),
    ]);

    await start(made);

    expect(state()).toBe('empty');
  });

  it('keeps the list it has when a later ask fails', async () => {
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 1 }),
      new Error('the engine is busy'),
    ]);
    await start(made);

    await made.view.refresh();

    expect(labels(made.view.getChildren())).toEqual(['a.example.com']);
    expect(state()).toBe('tree');
  });

  it('sets the context key for the kyverno text while the feature is off, and clears it while on', () => {
    rig();
    expect(contextKeys.get(KYVERNO_OFF_KEY)).toBe(true);

    reset();
    addWorkspaceFolder('repo');
    settings.set('cel-lens.experimental', ['kyverno']);
    rig();

    expect(contextKeys.get(KYVERNO_OFF_KEY)).toBe(false);
  });

  it('sets the key again when the experimental setting changes', () => {
    const made = rig();
    settings.set('cel-lens.experimental', ['kyverno']);

    made.view.redraw();

    expect(contextKeys.get(KYVERNO_OFF_KEY)).toBe(false);
  });
});

describe('a list the extension does not read', () => {
  it('is logged once and leaves the list as it was', async () => {
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 1 }),
      { documents: 'none' },
      'surprise',
      rawAnswer([crd('a.yaml', 'a.example.com', 2)], { generation: 2 }),
    ]);
    await start(made);

    await made.view.refresh();
    await made.view.refresh();

    expect(made.log.lines.filter((line) => line.includes('does not read'))).toHaveLength(1);
    expect(made.view.getChildren()[0]?.children).toHaveLength(1);

    await made.view.refresh();

    expect(made.view.getChildren()[0]?.children).toHaveLength(2);
  });

  it('says the engine cannot list rules when the first answer is the one that is not read', async () => {
    const made = rig(['surprise']);

    await start(made);

    expect(state()).toBe('unsupported');
  });
});

describe('a scan', () => {
  const SCANNING = { state: 'scanning', yamlFiles: 4210, filesWithRules: 37, filesLeftOut: 0 };

  it('shows the progress bar on the view, and says how far it is, until the scan ends', async () => {
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 1, scan: SCANNING }),
      rawAnswer([], { generation: 2, scan: { ...SCANNING, state: 'done' } }),
    ]);

    await start(made);

    expect(progressRuns).toMatchObject([{ options: { location: { viewId: 'cel-lens.rules' } } }]);
    expect(progressRuns[0]?.done).toBe(false);
    expect(treeViews[0]?.message).toBe('Scanning: 4,210 YAML files read, 37 with rules');

    await made.view.refresh();

    expect(progressRuns[0]?.done).toBe(true);
    expect(treeViews[0]?.message).toBe('');
  });

  it('shows the progress bar once for a scan, however many answers it takes', async () => {
    const made = rig([
      rawAnswer([], { generation: 1, scan: SCANNING }),
      rawAnswer([], { generation: 2, scan: { ...SCANNING, yamlFiles: 5000 } }),
    ]);
    await start(made);

    await made.view.refresh();

    expect(progressRuns).toHaveLength(1);
    expect(treeViews[0]?.message).toBe('Scanning: 5,000 YAML files read, 37 with rules');
  });

  it('takes the scan from a notification at once', async () => {
    const made = rig([rawAnswer([], { generation: 1 })]);
    await start(made);

    made.view.onRuleListChanged({ generation: 1, scan: SCANNING });

    expect(treeViews[0]?.message).toBe('Scanning: 4,210 YAML files read, 37 with rules');
    expect(made.run).toHaveBeenCalledTimes(1);

    made.view.onRuleListChanged({ generation: 1, scan: { ...SCANNING, state: 'done' } });
    await settle();

    expect(treeViews[0]?.message).toBe('');
    expect(progressRuns.at(-1)?.done).toBe(true);
  });

  it('shows no progress bar while the engine is down or has no folder to scan', async () => {
    reset();
    const made = rig();

    await start(made);

    expect(progressRuns).toEqual([]);
  });

  it('puts a warning first when files were left out, with the limits the engine said, and shows the logs when clicked', async () => {
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1)], {
        generation: 1,
        scan: { state: 'done', yamlFiles: 21203, filesWithRules: 1, filesLeftOut: 1203 },
      }),
    ]);

    await start(made);

    const [warning, first] = made.view.getChildren();
    expect(warning?.label).toBe(
      '1,203 YAML files not read: the scan reads at most 20,000 files of at most 1 MiB',
    );
    expect(first?.label).toBe('a.example.com');
    expect(made.view.getTreeItem(there(warning))).toMatchObject({
      command: { command: 'cel-lens.showLogs', title: 'Show Logs' },
      iconPath: { id: 'warning', color: { id: 'problemsWarningIcon.foreground' } },
    });
  });

  it('takes the warning away when a later scan leaves nothing out', async () => {
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1)], {
        generation: 1,
        scan: { state: 'done', yamlFiles: 5, filesWithRules: 1, filesLeftOut: 3 },
      }),
      rawAnswer([], {
        generation: 2,
        scan: { state: 'done', yamlFiles: 2, filesWithRules: 1, filesLeftOut: 0 },
      }),
    ]);
    await start(made);

    await made.view.refresh();

    expect(labels(made.view.getChildren())).toEqual(['a.example.com']);
  });
});

describe('the notification of a change', () => {
  it('asks for what changed when it is ahead of the list', async () => {
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 3 }),
      rawAnswer([crd('a.yaml', 'a.example.com', 2)], { generation: 4 }),
    ]);
    await start(made);

    made.view.onRuleListChanged({ generation: 4 });
    await settle();

    expect(made.run.mock.calls[1]).toEqual(['cel-lens.ruleList', { since: 3 }]);
    expect(made.view.getChildren()[0]?.children).toHaveLength(2);
  });

  it.each([[3], [2]])(
    'asks nothing when it is at generation %i, which the list has',
    async (generation) => {
      const made = rig([rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 3 })]);
      await start(made);

      made.view.onRuleListChanged({ generation });
      await settle();

      expect(made.run).toHaveBeenCalledTimes(1);
    },
  );

  it('asks when the list has no generation to compare', async () => {
    const made = rig([rawAnswer([])]);
    await start(made);

    made.view.onRuleListChanged({ generation: 1 });
    await settle();

    expect(made.run).toHaveBeenCalledTimes(2);
  });

  it.each([[undefined], ['text'], [{}], [{ generation: -1 }]])(
    'ignores a notification that holds neither a generation nor a scan: %j',
    async (params) => {
      const made = rig();
      await start(made);

      made.view.onRuleListChanged(params);
      await settle();

      expect(made.run).toHaveBeenCalledTimes(1);
    },
  );
});

describe('what makes it ask again', () => {
  const YAML = {
    languageId: 'yaml',
    getText: () => '',
    uri: Uri.parse('file:///workspace/repo/a.yaml'),
  };

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  });

  async function ready(): Promise<Rig> {
    const made = rig([rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 1 })]);
    await start(made);
    made.run.mockClear();
    return made;
  }

  it('asks once typing pauses, not at each keystroke', async () => {
    const made = await ready();

    changeDocument(YAML);
    await vi.advanceTimersByTimeAsync(CHANGE_PAUSE_MS - 100);
    changeDocument(YAML);
    await vi.advanceTimersByTimeAsync(CHANGE_PAUSE_MS - 100);
    expect(made.run).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(200);

    expect(made.run.mock.calls).toEqual([['cel-lens.ruleList', { since: 1 }]]);
  });

  it.each([
    [
      'a YAML file is saved',
      () => {
        saveDocument(YAML);
      },
    ],
    [
      'a YAML file is opened',
      () => {
        openDocument(YAML);
      },
    ],
    [
      'a .cellens file is changed',
      () => {
        changeDocument({
          languageId: 'plaintext',
          getText: () => '',
          uri: Uri.parse('file:///w/.cellens'),
        });
      },
    ],
    [
      'a file is closed',
      () => {
        closeDocument({ uri: Uri.parse('file:///workspace/repo/a.yaml') });
      },
    ],
    [
      'the workspace folders change',
      () => {
        changeWorkspaceFolders();
      },
    ],
  ])('asks when %s', async (_what, act) => {
    const made = await ready();

    act();
    await vi.advanceTimersByTimeAsync(CHANGE_PAUSE_MS);

    expect(made.run).toHaveBeenCalledTimes(1);
  });

  it.each([
    [
      'a file that is neither YAML nor a .cellens is changed',
      () => {
        changeDocument({
          languageId: 'typescript',
          getText: () => '',
          uri: Uri.parse('file:///workspace/repo/a.ts'),
        });
      },
    ],
    [
      'a YAML document that is not a file is opened, as in a diff or an untitled editor',
      () => {
        for (const uri of ['git:/workspace/repo/a.yaml', 'untitled:Untitled-1']) {
          openDocument({ languageId: 'yaml', getText: () => '', uri: Uri.parse(uri) });
          changeDocument({ languageId: 'yaml', getText: () => '', uri: Uri.parse(uri) });
          saveDocument({ languageId: 'yaml', getText: () => '', uri: Uri.parse(uri) });
        }
      },
    ],
    [
      'one that is not a file is closed',
      () => {
        closeDocument({ uri: Uri.parse('output:cel-lens') });
      },
    ],
  ])('does not ask when %s', async (_what, act) => {
    const made = await ready();

    act();
    await vi.advanceTimersByTimeAsync(CHANGE_PAUSE_MS * 2);

    expect(made.run).not.toHaveBeenCalled();
  });

  it('asks nothing for the text of a file once the engine has sent its notification, which says when the list changed', async () => {
    const made = await ready();
    made.view.onRuleListChanged({ generation: 1 });

    changeDocument(YAML);
    saveDocument(YAML);
    openDocument(YAML);
    closeDocument({ uri: Uri.parse('file:///workspace/repo/a.yaml') });
    await vi.advanceTimersByTimeAsync(CHANGE_PAUSE_MS * 2);

    expect(made.run).not.toHaveBeenCalled();
  });

  it('still asks when the workspace folders change, with the notification seen', async () => {
    const made = await ready();
    made.view.onRuleListChanged({ generation: 1 });

    changeWorkspaceFolders();
    await vi.advanceTimersByTimeAsync(CHANGE_PAUSE_MS);

    expect(made.run).toHaveBeenCalledTimes(1);
  });

  it('asks for the text of a file again after the engine restarts, until its notification comes', async () => {
    const made = await ready();
    made.view.onRuleListChanged({ generation: 1 });
    made.view.engineState({ kind: 'starting' });
    made.view.engineStarted({});
    await settle();
    made.run.mockClear();

    changeDocument(YAML);
    await vi.advanceTimersByTimeAsync(CHANGE_PAUSE_MS);

    expect(made.run).toHaveBeenCalledTimes(1);
  });

  it('ignores a notification it does not read, and keeps asking for the text of a file', async () => {
    const made = await ready();
    made.view.onRuleListChanged('x');

    changeDocument(YAML);
    await vi.advanceTimersByTimeAsync(CHANGE_PAUSE_MS);

    expect(made.run).toHaveBeenCalledTimes(1);
  });

  it('takes generations as large as the Unix clock in milliseconds', async () => {
    const GENERATION = 1_763_000_000_123;
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: GENERATION }),
      rawAnswer([crd('a.yaml', 'a.example.com', 2)], { generation: GENERATION + 7 }),
    ]);
    await start(made);

    made.view.onRuleListChanged({ generation: GENERATION + 7 });
    await settle();

    expect(made.run.mock.calls[1]).toEqual(['cel-lens.ruleList', { since: GENERATION }]);
    expect(made.view.getChildren()[0]?.children).toHaveLength(2);
  });

  it('does not ask after the engine went, and not after the view is disposed', async () => {
    const made = await ready();
    changeDocument(YAML);
    made.view.engineStopped();
    await vi.advanceTimersByTimeAsync(CHANGE_PAUSE_MS);

    expect(made.run).not.toHaveBeenCalled();

    made.view.dispose();
    changeDocument(YAML);
    await vi.advanceTimersByTimeAsync(CHANGE_PAUSE_MS);

    expect(made.run).not.toHaveBeenCalled();
  });

  it('draws the folders again when they change, at once', async () => {
    const made = await ready();
    addWorkspaceFolder('other');
    const fired: unknown[] = [];
    made.view.onDidChangeTreeData((change) => fired.push(change));

    changeWorkspaceFolders();

    expect(fired).toEqual([undefined]);
    expect(labels(made.view.getChildren())).toEqual(['repo']);
  });
});

describe('the items', () => {
  const RULE_ANSWER = rawAnswer(
    [
      rawDocument(
        `${W}/a.yaml`,
        [
          rawRule(`${W}/a.yaml`, 'CustomResourceDefinition a.example.com', 0, {
            examples: { state: 'wrong', words: 'refuses 1 example it should allow · 2 pass' },
          }),
          rawRule(`${W}/a.yaml`, 'CustomResourceDefinition a.example.com', 1, { compile: 'error' }),
        ],
        { name: 'a.example.com', folder: W, file: 'config/a.yaml' },
      ),
    ],
    { generation: 1 },
  );

  async function items(): Promise<{ made: Rig; document: TreeNode; rules: TreeNode[] }> {
    const made = rig([RULE_ANSWER]);
    await start(made);
    const document = there(made.view.getChildren()[0]);
    return { made, document, rules: made.view.getChildren(document) };
  }

  it('draws a document with the engine’s id, its name, its kind and file, collapsed, and no command', async () => {
    const { made, document } = await items();

    const item = made.view.getTreeItem(document);

    expect(item).toMatchObject({
      id: `${W}/a.yaml#CustomResourceDefinition a.example.com`,
      label: 'a.example.com',
      description: 'CRD · config/a.yaml',
      collapsibleState: 1,
      contextValue: 'document',
      iconPath: { id: 'symbol-class' },
      accessibilityInformation: { label: 'a.example.com, CRD, 2 rules, 2 with a problem' },
    });
    expect(item.command).toBeUndefined();
  });

  it('draws a rule with the engine’s id, label, words, icon in a theme colour, and a click that opens it', async () => {
    const { made, rules } = await items();

    const item = made.view.getTreeItem(there(rules[0]));

    expect(item).toMatchObject({
      id: `${W}/a.yaml#CustomResourceDefinition a.example.com#v1#spec.ports[]#0`,
      label: 'rule 0 says something',
      description: 'compiles · refuses 1 example it should allow · 2 pass',
      collapsibleState: 0,
      contextValue: 'ruleWithExamples',
      iconPath: { id: 'warning', color: { id: 'problemsWarningIcon.foreground' } },
      accessibilityInformation: {
        label:
          'rule 0 says something, warning, compiles · refuses 1 example it should allow · 2 pass',
      },
    });
    expect(item.iconPath).toBeInstanceOf(ThemeIcon);
    expect(item.command?.command).toBe('vscode.open');
    expect(String((item.command?.arguments as unknown[] | undefined)?.[0])).toBe(`${W}/a.yaml`);
    expect(item.command?.arguments?.[1]).toMatchObject({
      selection: { startLine: 10, startCharacter: 18, endLine: 10, endCharacter: 18 },
      preview: true,
      preserveFocus: false,
    });
  });

  it('says a rule with a compile error in words for a screen reader, with the first problem', async () => {
    const { made, rules } = await items();

    const item = made.view.getTreeItem(there(rules[1]));

    expect(item).toMatchObject({
      description: '1 problem',
      contextValue: 'rule',
      iconPath: { id: 'error', color: { id: 'problemsErrorIcon.foreground' } },
      accessibilityInformation: {
        label: "rule 1 says something, error, 1 problem: undefined field 'nmae'",
      },
    });
  });

  it('gives the parent of a rule and none for a row at the top', async () => {
    const { made, document, rules } = await items();

    expect(made.view.getParent(there(rules[0]))).toBe(document);
    expect(made.view.getParent(document)).toBeUndefined();
  });

  it('makes a rule’s tooltip when asked: Markdown with its label and CEL read from the file, and no command link', async () => {
    const { made, rules } = await items();
    fileTexts.set(
      `${W}/a.yaml`,
      Array.from({ length: 12 }, (_, line) =>
        line === 10 ? `${'x'.repeat(18)}self.a < 3 && true` : '',
      ).join('\n'),
    );
    const item = made.view.getTreeItem(there(rules[0]));

    const resolved = await made.view.resolveTreeItem(item, there(rules[0]));

    expect(resolved.tooltip).toBeInstanceOf(MarkdownString);
    const text = (resolved.tooltip as MarkdownString).value;
    expect(text).toContain('**rule 0 says something**');
    expect(text).toContain('v1 · spec.ports\\[\\] · rule 0');
    expect(text).toContain('```\nself.a < 3 && true\n```');
    expect(text).toContain('refuses 1 example it should allow · 2 pass');
    expect(text).not.toMatch(/command:/);
    expect((resolved.tooltip as MarkdownString).isTrusted).toBeUndefined();
  });

  it('makes the tooltip without the CEL when the file cannot be read', async () => {
    const { made, rules } = await items();
    const item = made.view.getTreeItem(there(rules[1]));

    const resolved = await made.view.resolveTreeItem(item, there(rules[1]));

    const text = (resolved.tooltip as MarkdownString).value;
    expect(text).toContain("error: undefined field 'nmae'");
    expect(text).not.toContain('```');
  });

  it('makes no tooltip for a row that is no rule', async () => {
    const { made, document } = await items();
    const item = made.view.getTreeItem(document);

    expect((await made.view.resolveTreeItem(item, document)).tooltip).toBe(
      'a.example.com\nCRD · config/a.yaml',
    );
  });

  it('says in a tooltip that example results do not cover a kind they do not', async () => {
    const policy = rawDocument(
      `${W}/p.yaml`,
      [rawRule(`${W}/p.yaml`, 'MutatingAdmissionPolicy p', 0, { field: 'mutation' })],
      { kind: 'MutatingAdmissionPolicy', name: 'p', folder: W, file: 'p.yaml' },
    );
    const made = rig([rawAnswer([policy], { generation: 1 })]);
    await start(made);
    const [document] = made.view.getChildren();
    const rule = there(made.view.getChildren(document)[0]);

    const resolved = await made.view.resolveTreeItem(made.view.getTreeItem(rule), rule);

    expect((resolved.tooltip as MarkdownString).value).toContain(
      'Example results do not cover a MutatingAdmissionPolicy.',
    );
  });
});

describe('a tooltip (review of #27)', () => {
  const FILE = `${W}/a.yaml`;
  const TEXT = Array.from({ length: 12 }, (_, line) =>
    line === 10 ? `${'x'.repeat(18)}self.a < 3 && true` : '',
  ).join('\n');

  async function rule(): Promise<{ made: Rig; node: TreeNode }> {
    const made = rig([rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 1 })]);
    await start(made);
    made.run.mockClear();
    return { made, node: there(made.view.getChildren(made.view.getChildren()[0])[0]) };
  }

  it('opens no document for a closed file, so the engine is told no didOpen and the view asks nothing', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    const { made, node } = await rule();
    fileTexts.set(FILE, TEXT);

    const resolved = await made.view.resolveTreeItem(made.view.getTreeItem(node), node);
    await vi.advanceTimersByTimeAsync(CHANGE_PAUSE_MS * 2);

    expect((resolved.tooltip as MarkdownString).value).toContain('```\nself.a < 3 && true\n```');
    expect(openedByApi).toEqual([]);
    expect(made.run).not.toHaveBeenCalled();
  });

  it('takes the CEL from the document when the file is open, as the editor has it', async () => {
    const { made, node } = await rule();
    openDocuments.push(
      new FakeTextDocument(
        Uri.parse(FILE),
        'yaml',
        TEXT.replace('self.a < 3 && true', 'self.a < 9 && edited'),
      ),
    );

    const resolved = await made.view.resolveTreeItem(made.view.getTreeItem(node), node);

    expect((resolved.tooltip as MarkdownString).value).toContain('self.a < 9 && edited');
    expect(openedByApi).toEqual([]);
  });

  it('cuts the CEL by lines that end with a carriage return too', async () => {
    const { made, node } = await rule();
    fileTexts.set(FILE, TEXT.replaceAll('\n', '\r\n'));

    const resolved = await made.view.resolveTreeItem(made.view.getTreeItem(node), node);

    expect((resolved.tooltip as MarkdownString).value).toContain('```\nself.a < 3 && true\n```');
  });
});

describe('an answer with an entry that is not read (review of #27)', () => {
  it('lists the rest, and logs how many were left out once', async () => {
    const broken = { uri: `${W}/b.yaml`, folder: W, file: 'b.yaml', name: 'b', rules: [] };
    const made = rig([
      rawAnswer([crd('a.yaml', 'a.example.com', 1), broken], { generation: 1 }),
      rawAnswer([crd('a.yaml', 'a.example.com', 2), broken], { generation: 2 }),
    ]);

    await start(made);
    await made.view.refresh();

    expect(labels(made.view.getChildren())).toEqual([
      'a.example.com',
      'A document that could not be read',
    ]);
    expect(made.log.lines.filter((line) => line.includes('does not read'))).toHaveLength(1);
    expect(state()).toBe('tree');
  });

  it('draws a document with no metadata.name by what it is', async () => {
    const unnamed = { ...crd('a.yaml', 'x', 1), name: '', document: 'CustomResourceDefinition' };
    const made = rig([rawAnswer([unnamed], { generation: 1 })]);

    await start(made);

    expect(labels(made.view.getChildren())).toEqual(['(no name) CRD']);
    expect(made.view.getTreeItem(there(made.view.getChildren()[0]))).toMatchObject({
      description: 'CRD · a.yaml',
      accessibilityInformation: { label: '(no name) CRD, CRD, 1 rule' },
    });
    expect(state()).toBe('tree');
  });
});

describe('a folder that turns example results off', () => {
  const examples = { state: 'pass', words: '2 examples pass' };

  function answer(): Answer {
    const rule = (uri: string): Answer =>
      rawRule(uri, 'CustomResourceDefinition x', 0, { examples });
    return rawAnswer(
      [
        rawDocument('file:///workspace/a/x.yaml', [rule('file:///workspace/a/x.yaml')], {
          folder: 'file:///workspace/a',
          file: 'x.yaml',
        }),
        rawDocument('file:///workspace/b/x.yaml', [rule('file:///workspace/b/x.yaml')], {
          folder: 'file:///workspace/b',
          file: 'x.yaml',
        }),
      ],
      { generation: 1 },
    );
  }

  beforeEach(() => {
    reset();
    addWorkspaceFolder('a');
    addWorkspaceFolder('b', { 'cel-lens.exampleResults': false });
  });

  it('shows no example words or icon for the rules of that folder, and none of the lines’ switches for the other', async () => {
    const made = rig([answer()]);
    await start(made);

    const [folderA, folderB] = made.view.getChildren();
    const ruleA = there(made.view.getChildren(made.view.getChildren(folderA)[0])[0]);
    const ruleB = there(made.view.getChildren(made.view.getChildren(folderB)[0])[0]);

    expect(made.view.getTreeItem(ruleA)).toMatchObject({
      description: 'compiles · 2 examples pass',
      iconPath: { id: 'pass' },
    });
    expect(made.view.getTreeItem(ruleB)).toMatchObject({
      description: 'compiles',
      iconPath: { id: 'check' },
      contextValue: 'rule',
    });
  });

  it('draws the rows again when the setting changes, in the same items', async () => {
    const made = rig([answer()]);
    await start(made);
    const [, folderB] = made.view.getChildren();
    const ruleB = there(made.view.getChildren(made.view.getChildren(folderB)[0])[0]);
    const fired: unknown[] = [];
    made.view.onDidChangeTreeData((change) => fired.push(change));

    settings.set('cel-lens.exampleResults', true);
    reset();
    addWorkspaceFolder('a');
    addWorkspaceFolder('b');
    made.view.redraw();

    expect(fired).toEqual([undefined]);
    expect(made.view.getTreeItem(ruleB).description).toBe('compiles · 2 examples pass');
    changeConfiguration('cel-lens.exampleResults');
  });
});

describe('the context menu', () => {
  async function rule(): Promise<{ made: Rig; node: TreeNode }> {
    const made = rig([rawAnswer([crd('a.yaml', 'a.example.com', 1)], { generation: 1 })]);
    await start(made);
    return { made, node: there(made.view.getChildren(made.view.getChildren()[0])[0]) };
  }

  it('runs cel-lens.showRule with the rule’s location for Show Problems', async () => {
    const { made, node } = await rule();

    await made.view.showProblems(node);

    expect(executedCommands).toEqual([{ command: SHOW_RULE_COMMAND, args: [node.location] }]);
  });

  it('runs cel-lens.showExampleResults with the rule’s location for Show Example Results', async () => {
    const { made, node } = await rule();

    await made.view.showExampleResults(node);

    expect(executedCommands).toEqual([
      { command: SHOW_EXAMPLE_RESULTS_COMMAND, args: [node.location] },
    ]);
  });

  it.each([[undefined], ['text'], [{ kind: 'document' }], [{ kind: 'rule' }]])(
    'logs and runs nothing for an argument that is no rule: %j',
    async (argument) => {
      const { made } = await rule();

      await made.view.showProblems(argument);

      expect(executedCommands).toEqual([]);
      expect(made.log.lines.some((line) => line.includes('no rule'))).toBe(true);
    },
  );

  it('reads the location of a rule node and of nothing else', async () => {
    const { node } = await rule();

    expect(ruleLocationOf(node)).toEqual(node.location);
    expect(ruleLocationOf({ kind: 'warning', location: node.location })).toBeUndefined();
  });
});

describe('disposing the view', () => {
  it('ends its progress bar and disposes the tree view', async () => {
    const made = rig([
      rawAnswer([], {
        generation: 1,
        scan: { state: 'scanning', yamlFiles: 1, filesWithRules: 0 },
      }),
    ]);
    await start(made);

    made.view.dispose();
    await settle();

    expect(progressRuns[0]?.done).toBe(true);
    expect(treeViews[0]?.disposed).toBe(true);
  });
});
