import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ConfigurationTarget,
  type FakeTreeView,
  activeEditor,
  addWorkspaceFolder,
  changeActiveEditor,
  contextKeys,
  memento,
  moveCursor,
  reset,
  settle,
  settingUpdates,
  settings,
  treeViews,
  workspaceSettings,
} from '../test/vscode';
import { rawAnswer, rawDocument, rawRule } from './rule_list_samples';
import { W, crd, labels, rig, start, state, there, type Answer, type Rig } from './rule_view_rig';
import { PROBLEMS_ONLY_STATE, badgeTooltip } from './rule_view';
import { FOLLOW_EDITOR_KEY, PROBLEMS_ONLY_KEY } from './rule_view_state';
import type { TreeNode } from './rule_tree';

/** A CRD file with a rule that does not compile at index 1 and ones that do at the others. */
function mixed(file: string, rules: number, broken: readonly number[] = [1]): Answer {
  const uri = `${W}/${file}`;
  const document = `CustomResourceDefinition ${file}`;
  return rawDocument(
    uri,
    Array.from({ length: rules }, (_, index) =>
      rawRule(uri, document, index, { compile: broken.includes(index) ? 'error' : 'ok' }),
    ),
    { name: file, folder: W, file },
  );
}

const view = (): FakeTreeView<TreeNode> => there(treeViews[0]);

beforeEach(() => {
  addWorkspaceFolder('repo');
});

afterEach(() => {
  vi.useRealTimers();
  reset();
});

describe('the filter for problems', () => {
  const ANSWER = rawAnswer([mixed('a.yaml', 3), crd('b.yaml', 'b.example.com', 2)], {
    generation: 1,
  });

  it('shows the rules with a problem, with their documents, and the rest again when switched back', async () => {
    const made = rig([ANSWER]);
    await start(made);
    expect(labels(made.view.getChildren())).toEqual(['a.yaml', 'b.example.com']);
    const fired: unknown[] = [];
    made.view.onDidChangeTreeData((change) => fired.push(change));

    made.view.setProblemsOnly(true);

    expect(labels(made.view.getChildren())).toEqual(['a.yaml']);
    expect(labels(made.view.getChildren(made.view.getChildren()[0]))).toEqual([
      'rule 1 says something',
    ]);
    expect(fired).toEqual([undefined]);

    made.view.setProblemsOnly(false);

    expect(labels(made.view.getChildren())).toEqual(['a.yaml', 'b.example.com']);
  });

  it('sets the context key that picks the icon of the title bar, and its other value when switched back', async () => {
    const made = rig([ANSWER]);
    await start(made);
    expect(contextKeys.get(PROBLEMS_ONLY_KEY)).toBe(false);

    made.view.setProblemsOnly(true);
    expect(contextKeys.get(PROBLEMS_ONLY_KEY)).toBe(true);

    made.view.setProblemsOnly(false);
    expect(contextKeys.get(PROBLEMS_ONLY_KEY)).toBe(false);
  });

  it('keeps the choice in the workspace’s state, and reads it at start', async () => {
    const kept = memento();
    const first = rig([ANSWER], { state: kept });
    await start(first);

    first.view.setProblemsOnly(true);
    await settle();
    first.view.dispose();
    reset();
    addWorkspaceFolder('repo');
    const second = rig([ANSWER], { state: kept });
    await start(second);

    expect(kept.get(PROBLEMS_ONLY_STATE)).toBe(true);
    expect(second.view.showsProblemsOnly).toBe(true);
    expect(contextKeys.get(PROBLEMS_ONLY_KEY)).toBe(true);
    expect(labels(second.view.getChildren())).toEqual(['a.yaml']);
  });

  it('is off at start when nothing was kept, and works with no state to keep it in', async () => {
    const made = rig([ANSWER]);
    await start(made);

    made.view.setProblemsOnly(true);

    expect(made.view.showsProblemsOnly).toBe(true);
    expect(rig([ANSWER], { state: memento() }).view.showsProblemsOnly).toBe(false);
  });

  it('logs a choice it could not keep, and still filters', async () => {
    const failing = {
      get: <T>(_key: string, fallback?: T): T | undefined => fallback,
      update: () => Promise.reject(new Error('no space')),
    };
    const made = rig([ANSWER], { state: failing });
    await start(made);

    made.view.setProblemsOnly(true);
    await settle();

    expect(made.log.lines.some((line) => line.includes('Could not keep the filter'))).toBe(true);
    expect(labels(made.view.getChildren())).toEqual(['a.yaml']);
  });

  it('says no rule has a problem, with the filter on and no rule with one', async () => {
    const made = rig([rawAnswer([crd('b.yaml', 'b.example.com', 2)], { generation: 1 })]);
    await start(made);
    expect(state()).toBe('tree');

    made.view.setProblemsOnly(true);

    expect(made.view.getChildren()).toEqual([]);
    expect(state()).toBe('noProblems');

    made.view.setProblemsOnly(false);
    expect(state()).toBe('tree');
  });

  it('follows the answers while it is on: a rule that gets a problem shows, and the state goes when none is left', async () => {
    const made = rig([
      rawAnswer([crd('b.yaml', 'b.example.com', 2)], { generation: 1 }),
      rawAnswer([mixed('b.yaml', 2, [0])], { generation: 2 }),
      rawAnswer([crd('b.yaml', 'b.example.com', 2)], { generation: 3 }),
    ]);
    await start(made);
    made.view.setProblemsOnly(true);
    expect(state()).toBe('noProblems');

    await made.view.refresh();
    expect(labels(made.view.getChildren())).toEqual(['b.yaml']);
    expect(state()).toBe('tree');

    await made.view.refresh();
    expect(state()).toBe('noProblems');
  });

  it('says nothing was found, not that no rule has a problem, when the list is empty', async () => {
    const made = rig([rawAnswer([], { generation: 1 })]);
    await start(made);

    made.view.setProblemsOnly(true);

    expect(state()).toBe('empty');
  });
});

describe('the badge', () => {
  const badge = (): unknown => view().badge;

  it('is the number of rules with a problem, with a tooltip in words', async () => {
    const made = rig([
      rawAnswer([mixed('a.yaml', 4, [1, 2]), crd('b.yaml', 'b', 1)], { generation: 1 }),
    ]);

    await start(made);

    expect(badge()).toEqual({ value: 2, tooltip: '2 rules with problems' });
  });

  it.each([
    [1, '1 rule with a problem'],
    [2, '2 rules with problems'],
    [3, '3 rules with problems'],
  ])('says %i as “%s”', (count, tooltip) => {
    expect(badgeTooltip(count)).toBe(tooltip);
  });

  it('shows 1 rule with a problem for one', async () => {
    const made = rig([rawAnswer([mixed('a.yaml', 2)], { generation: 1 })]);

    await start(made);

    expect(badge()).toEqual({ value: 1, tooltip: '1 rule with a problem' });
  });

  it('is none at 0', async () => {
    const made = rig([rawAnswer([crd('b.yaml', 'b', 2)], { generation: 1 })]);

    await start(made);

    expect(badge()).toBeUndefined();
  });

  it('follows each answer, up and down, and a removed file', async () => {
    const made = rig([
      rawAnswer([mixed('a.yaml', 3, [0])], { generation: 1 }),
      rawAnswer([mixed('a.yaml', 3, [0, 1, 2])], { generation: 2 }),
      rawAnswer([], { generation: 3, removed: [`${W}/a.yaml`] }),
    ]);
    await start(made);
    expect(badge()).toMatchObject({ value: 1 });

    await made.view.refresh();
    expect(badge()).toMatchObject({ value: 3 });

    await made.view.refresh();
    expect(badge()).toBeUndefined();
  });

  it('is the same whatever the filter shows', async () => {
    const made = rig([
      rawAnswer([mixed('a.yaml', 3, [0, 1]), crd('b.yaml', 'b', 2)], { generation: 1 }),
    ]);
    await start(made);

    made.view.setProblemsOnly(true);
    expect(badge()).toMatchObject({ value: 2 });

    made.view.setProblemsOnly(false);
    expect(badge()).toMatchObject({ value: 2 });
  });

  it('counts what is known while a scan runs', async () => {
    const made = rig([
      rawAnswer([mixed('a.yaml', 2)], {
        generation: 1,
        scan: { state: 'scanning', yamlFiles: 40, filesWithRules: 1 },
      }),
      rawAnswer([mixed('c.yaml', 2)], {
        generation: 2,
        scan: { state: 'done', yamlFiles: 90, filesWithRules: 2 },
      }),
    ]);

    await start(made);
    expect(badge()).toMatchObject({ value: 1 });
    expect(view().message).toContain('Scanning');

    await made.view.refresh();
    expect(badge()).toMatchObject({ value: 2 });
    expect(view().message).toBe('');
  });

  it('counts the entries it could not read as problems, whether or not they have a file', async () => {
    const doc = { ...crd('a.yaml', 'a', 1), rules: [rawRule(`${W}/a.yaml`, 'd', 0), 'x'] };
    const bad = { uri: `${W}/b.yaml`, folder: W, file: 'b.yaml' };
    const made = rig([{ ...rawAnswer([doc, bad], { generation: 1 }), documents: [doc, bad, 'y'] }]);

    await start(made);

    expect(badge()).toMatchObject({ value: 3 });
    expect(labels(made.view.getChildren())).toEqual([
      '1 entry of the engine’s list could not be read',
      'a',
      'A document that could not be read',
    ]);
  });

  it('is gone when the engine goes', async () => {
    const made = rig([rawAnswer([mixed('a.yaml', 2)], { generation: 1 })]);
    await start(made);

    made.view.engineStopped();

    expect(badge()).toBeUndefined();
  });
});

describe('following the editor', () => {
  const ANSWER = rawAnswer([mixed('a.yaml', 3), crd('b.yaml', 'b.example.com', 1)], {
    generation: 1,
  });

  async function following(over: { problemsOnly?: boolean } = {}): Promise<Rig> {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    const made = rig([ANSWER], { followMs: 100 });
    await start(made);
    if (over.problemsOnly === true) {
      made.view.setProblemsOnly(true);
    }
    activeEditor.uri = `${W}/a.yaml`;
    changeActiveEditor('yaml');
    await vi.advanceTimersByTimeAsync(100);
    view().revealed.length = 0;
    view().selection = [];
    return made;
  }

  it('selects the rule whose span holds the cursor, 100 ms after the cursor moved, with no focus', async () => {
    const made = await following();

    moveCursor(15, 4);
    await vi.advanceTimersByTimeAsync(99);
    expect(view().revealed).toEqual([]);
    await vi.advanceTimersByTimeAsync(1);

    expect(view().revealed).toEqual([
      {
        element: there(made.view.getChildren(there(made.view.getChildren()[0]))[1]),
        options: { select: true, focus: false },
      },
    ]);
  });

  it('follows the last move of a burst of moves', async () => {
    await following();

    moveCursor(11, 0);
    await vi.advanceTimersByTimeAsync(60);
    moveCursor(19, 0);
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed.map((each) => each.element.label)).toEqual(['rule 2 says something']);
  });

  it('follows a change of the active editor', async () => {
    await following();
    activeEditor.uri = `${W}/b.yaml`;
    activeEditor.line = 11;

    changeActiveEditor('yaml');
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed.map((each) => each.element.id)).toEqual([
      `${W}/b.yaml#CustomResourceDefinition b.example.com#v1#spec.ports[]#0`,
    ]);
  });

  it('selects the document when the cursor is between two rules', async () => {
    const made = await following();

    moveCursor(30, 0);
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed.map((each) => each.element)).toEqual([made.view.getChildren()[0]]);
  });

  it('changes nothing when no row holds the cursor, or the file is not in the list', async () => {
    await following();

    moveCursor(99, 0);
    await vi.advanceTimersByTimeAsync(100);
    activeEditor.uri = `${W}/other.yaml`;
    moveCursor(11, 0);
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed).toEqual([]);
  });

  it('leaves a row that is selected alone, so a click on a rule, which puts the cursor on it, does not move the view', async () => {
    const made = await following();
    const rule = there(made.view.getChildren(there(made.view.getChildren()[0]))[2]);
    // The click selects the rule, and VS Code opens its file with the cursor at the start of its expression.
    view().selection = [rule];
    const start = there(rule.location).range.start;

    moveCursor(start.line, start.character);
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed).toEqual([]);
  });

  it('selects the document, not a rule the filter hides, when the document shows', async () => {
    await following({ problemsOnly: true });

    moveCursor(11, 0);
    await vi.advanceTimersByTimeAsync(100);
    moveCursor(15, 0);
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed.map((each) => each.element.label)).toEqual([
      'a.yaml',
      'rule 1 says something',
    ]);
  });

  it('changes nothing when the filter hides the document too', async () => {
    await following({ problemsOnly: true });
    activeEditor.uri = `${W}/b.yaml`;

    moveCursor(11, 0);
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed).toEqual([]);
  });

  it('does nothing while the view is not showing: it never opens the view', async () => {
    await following();
    view().visible = false;

    moveCursor(15, 4);
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed).toEqual([]);
  });

  it('does nothing while the setting is off, and follows again once it is on', async () => {
    await following();
    settings.set('cel-lens.ruleView.followEditor', false);

    moveCursor(15, 4);
    await vi.advanceTimersByTimeAsync(100);
    expect(view().revealed).toEqual([]);

    settings.set('cel-lens.ruleView.followEditor', true);
    moveCursor(15, 5);
    await vi.advanceTimersByTimeAsync(100);
    expect(view().revealed).toHaveLength(1);
  });

  it.each([
    [
      'an editor that is not YAML',
      () => {
        changeActiveEditor('typescript');
      },
    ],
    [
      'no editor',
      () => {
        changeActiveEditor(undefined);
      },
    ],
  ])('does nothing for %s', async (_what, act) => {
    await following();

    act();
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed).toEqual([]);
  });

  it('follows again when an answer changes the list under the cursor', async () => {
    const made = rig(
      [
        rawAnswer([mixed('a.yaml', 1)], { generation: 1 }),
        rawAnswer([mixed('a.yaml', 3)], { generation: 2 }),
      ],
      { followMs: 100 },
    );
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    await start(made);
    activeEditor.uri = `${W}/a.yaml`;
    changeActiveEditor('yaml');
    moveCursor(19, 0);
    await vi.advanceTimersByTimeAsync(100);
    // Past the only rule the list has, the cursor is in its document.
    expect(view().revealed.map((each) => each.element.label)).toEqual(['a.yaml']);

    await made.view.refresh();
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed.map((each) => each.element.label)).toEqual([
      'a.yaml',
      'rule 2 says something',
    ]);
  });

  it('moves from a rule to its document when the filter turns on and hides the rule', async () => {
    const made = await following();
    moveCursor(11, 0);
    await vi.advanceTimersByTimeAsync(100);
    expect(view().revealed.map((each) => each.element.label)).toEqual(['rule 0 says something']);

    made.view.setProblemsOnly(true);
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed.map((each) => each.element.label)).toEqual([
      'rule 0 says something',
      'a.yaml',
    ]);
  });

  // Review of #30: the view must not fight a row the person picked.
  it('leaves a document row the person clicked alone when an answer comes', async () => {
    const made = await following();
    moveCursor(15, 4);
    await vi.advanceTimersByTimeAsync(100);
    const clicked = there(made.view.getChildren()[1]);
    view().selection = [clicked];
    view().revealed.length = 0;

    await made.view.refresh();
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed).toEqual([]);
    expect(view().selection).toEqual([clicked]);
  });

  it('leaves a row the person moved to with the arrow keys alone, answer after answer', async () => {
    const made = await following();
    moveCursor(15, 4);
    await vi.advanceTimersByTimeAsync(100);
    const rule = there(made.view.getChildren(there(made.view.getChildren()[0]))[0]);
    view().selection = [rule];
    view().revealed.length = 0;

    for (let answers = 0; answers < 3; answers++) {
      await made.view.refresh();
      await vi.advanceTimersByTimeAsync(100);
    }

    expect(view().revealed).toEqual([]);
    expect(view().selection).toEqual([rule]);
  });

  it('leaves a row the person picked alone when the filter changes', async () => {
    const made = await following();
    const picked = there(made.view.getChildren()[1]);
    view().selection = [picked];

    made.view.setProblemsOnly(true);
    made.view.setProblemsOnly(false);
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed).toEqual([]);
  });

  it('leaves a row the person picked alone when the setting is turned on', async () => {
    const made = await following();
    view().selection = [there(made.view.getChildren()[1])];

    made.view.followEditorChanged();
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed).toEqual([]);
  });

  it('follows the cursor again, over a row the person picked, once the cursor moves', async () => {
    const made = await following();
    view().selection = [there(made.view.getChildren()[1])];

    moveCursor(15, 4);
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed.map((each) => each.element.label)).toEqual(['rule 1 says something']);
  });

  it('still follows after an answer, when what it selected itself is selected or nothing is', async () => {
    const made = await following();
    moveCursor(15, 4);
    await vi.advanceTimersByTimeAsync(100);
    // The rule it selected is still selected: an answer that changes what is under the cursor moves it along.
    view().revealed.length = 0;
    activeEditor.line = 11;

    await made.view.refresh();
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed.map((each) => each.element.label)).toEqual(['rule 0 says something']);

    view().selection = [];
    activeEditor.line = 19;
    await made.view.refresh();
    await vi.advanceTimersByTimeAsync(100);

    expect(view().revealed.map((each) => each.element.label)).toEqual([
      'rule 0 says something',
      'rule 2 says something',
    ]);
  });

  it('stops at dispose', async () => {
    const made = await following();

    made.view.dispose();
    moveCursor(15, 4);
    await vi.advanceTimersByTimeAsync(200);

    expect(view().revealed).toEqual([]);
  });
});

describe('the setting that makes the view follow the editor', () => {
  it('sets the context key of the title menu from the setting, and again when it changes', () => {
    const made = rig();
    expect(contextKeys.get(FOLLOW_EDITOR_KEY)).toBe(true);

    settings.set('cel-lens.ruleView.followEditor', false);
    made.view.followEditorChanged();

    expect(contextKeys.get(FOLLOW_EDITOR_KEY)).toBe(false);
  });

  it('is written at the user scope by Stop Following, and Follow Editor writes it on', async () => {
    const made = rig();

    await made.view.setFollowEditor(false);
    await made.view.setFollowEditor(true);

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.ruleView.followEditor', value: false, target: ConfigurationTarget.Global },
      { name: 'cel-lens.ruleView.followEditor', value: true, target: ConfigurationTarget.Global },
    ]);
  });

  it('is written at the workspace scope when the workspace sets it', async () => {
    workspaceSettings.set('cel-lens.ruleView.followEditor', true);
    const made = rig();

    await made.view.setFollowEditor(false);

    expect(settingUpdates).toEqual([
      {
        name: 'cel-lens.ruleView.followEditor',
        value: false,
        target: ConfigurationTarget.Workspace,
      },
    ]);
  });
});
