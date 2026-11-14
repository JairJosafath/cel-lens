import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import {
  FakeEditor,
  Uri,
  visibleEditors,
  type FakeTreeView,
  activeEditor,
  addWorkspaceFolder,
  changeActiveEditor,
  contextKeys,
  contextWrites,
  executedCommands,
  moveCursor,
  reset,
  settle,
  settings,
  treeViews,
} from '../test/vscode';
import { rawAnswer, rawDocument, rawRule } from './rule_list_samples';
import type { TreeNode } from './rule_tree';
import { W, crd, rig, start, there, type Answer, type Rig } from './rule_view_rig';
import { EDITOR_HAS_RULES_KEY, RULE_AT_CURSOR_KEY } from './rule_view_state';

const view = (): FakeTreeView<TreeNode> => there(treeViews[0]);

/** The values a key was set to, oldest first. */
function writes(key: string): unknown[] {
  return contextWrites.filter((write) => write.key === key).map((write) => write.value);
}

/**
 * A CRD file `a.yaml` of three rules, at 0-based lines 10-12, 14-16 and 18-20; the first has
 * example words.
 */
function withExamples(): Answer {
  const uri = `${W}/a.yaml`;
  const document = 'CustomResourceDefinition a.example.com';
  return rawDocument(
    uri,
    [
      rawRule(uri, document, 0, { examples: { state: 'pass', words: '3 pass' } }),
      rawRule(uri, document, 1),
      rawRule(uri, document, 2),
    ],
    { name: 'a.example.com', folder: W, file: 'a.yaml' },
  );
}

const ANSWER = rawAnswer([withExamples(), crd('b.yaml', 'b.example.com', 2)], { generation: 1 });

/** Makes `file` the active editor, as VS Code does when a tab is picked. */
function open(file: string, line = 0, character = 0): void {
  activeEditor.uri = file.startsWith('file:') ? file : `${W}/${file}`;
  activeEditor.line = line;
  activeEditor.character = character;
  changeActiveEditor('yaml');
}

beforeEach(() => {
  addWorkspaceFolder('repo');
});

afterEach(reset);

describe('the key cel-lens.editorHasRules', () => {
  it('is true for the active editor of a file the engine lists, and false for a YAML file it does not, a GitHub workflow', async () => {
    const made = rig([ANSWER]);
    await start(made);

    open('a.yaml');
    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(true);

    open('.github/workflows/ci.yaml');
    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(false);
    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBeUndefined();
  });

  it('is false with no editor, and before the engine has answered', async () => {
    const made = rig([ANSWER]);
    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(false);
    await start(made);

    open('a.yaml');
    changeActiveEditor(undefined);

    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(false);
  });

  it('is true for an open file outside every folder, which the engine lists under Open files', async () => {
    const uri = 'file:///workspace/active.yaml';
    const answer = rawAnswer(
      [
        rawDocument(uri, [rawRule(uri, 'CustomResourceDefinition o', 0)], {
          name: 'o',
          folder: null,
          file: uri,
        }),
      ],
      { generation: 1 },
    );
    const made = rig([answer]);
    await start(made);

    open(uri);

    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(true);
  });

  it('turns true once the answer that lists the file of the active editor comes', async () => {
    const made = rig([rawAnswer([], { generation: 1 }), ANSWER]);
    await start(made);
    open('a.yaml');
    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(false);

    await made.view.refresh();

    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(true);
  });

  it('turns false when an answer removes the file', async () => {
    const made = rig([
      ANSWER,
      rawAnswer([], { generation: 2, removed: [`${W}/a.yaml`], full: false }),
    ]);
    await start(made);
    open('a.yaml');
    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(true);

    await made.view.refresh();

    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(false);
  });

  it('turns false when the engine restarts, and true again when the new engine lists the file', async () => {
    const made = rig([ANSWER]);
    await start(made);
    open('a.yaml');

    made.view.engineStarting();
    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(false);

    made.view.engineStarted({});
    await settle();
    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(true);
  });

  it('is false while the engine is down, or cannot list rules', async () => {
    const made = rig([ANSWER]);
    await start(made);
    open('a.yaml');

    made.view.engineStopped();

    expect(contextKeys.get(EDITOR_HAS_RULES_KEY)).toBe(false);
  });

  it('is set only when its value changes, though the editor changes and answers come', async () => {
    const made = rig([ANSWER]);
    await start(made);

    open('a.yaml');
    open('b.yaml');
    open('a.yaml', 11);
    await made.view.refresh();
    open('.github/workflows/ci.yaml');
    open('.github/workflows/other.yaml');

    expect(writes(EDITOR_HAS_RULES_KEY)).toEqual([false, true, false]);
  });
});

describe('the key cel-lens.ruleAtCursor', () => {
  async function started(): Promise<Rig> {
    const made = rig([ANSWER]);
    await start(made);
    open('a.yaml');
    return made;
  }

  it('is ruleWithExamples with the cursor in a rule whose row shows example words, rule in another, and unset between them', async () => {
    await started();

    moveCursor(11, 4);
    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBe('ruleWithExamples');
    moveCursor(15, 4);
    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBe('rule');
    moveCursor(13, 0);
    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBeUndefined();
  });

  it('is rule, not ruleWithExamples, in a rule with example words in a folder that turns example results off', async () => {
    addWorkspaceFolder('other', { 'cel-lens.exampleResults': false });
    const uri = 'file:///workspace/other/a.yaml';
    const document = 'CustomResourceDefinition o.example.com';
    const made = rig([
      rawAnswer(
        [
          rawDocument(
            uri,
            [rawRule(uri, document, 0, { examples: { state: 'pass', words: '3 pass' } })],
            { name: 'o.example.com', folder: 'file:///workspace/other', file: 'a.yaml' },
          ),
          withExamples(),
        ],
        { generation: 1 },
      ),
    ]);
    await start(made);

    open(uri, 11, 4);
    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBe('rule');

    open('a.yaml', 11, 4);
    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBe('ruleWithExamples');
  });

  it('follows the setting for the folder of the file: example results off makes ruleWithExamples a rule', async () => {
    const made = await started();
    moveCursor(11, 4);
    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBe('ruleWithExamples');

    settings.set('cel-lens.exampleResults', false);
    made.view.redraw();

    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBe('rule');
  });

  it('is unset in a file the engine does not list, wherever the cursor is', async () => {
    await started();
    open('.github/workflows/ci.yaml');

    moveCursor(11, 4);

    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBeUndefined();
  });

  it('is set on each answer from where the cursor is, with no delay', async () => {
    const made = rig([rawAnswer([], { generation: 1 }), ANSWER]);
    await start(made);
    open('a.yaml', 11, 4);
    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBeUndefined();

    await made.view.refresh();

    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBe('ruleWithExamples');
  });

  it('is unset when the engine restarts', async () => {
    const made = await started();
    moveCursor(11, 4);

    made.view.engineStarting();

    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBeUndefined();
  });

  it('is set only when it changes, as the cursor moves inside a rule and between rules', async () => {
    const made = await started();

    moveCursor(10, 0);
    moveCursor(11, 5);
    moveCursor(12, 2);
    moveCursor(13, 0);
    moveCursor(13, 3);
    moveCursor(15, 0);
    moveCursor(16, 9);
    await made.view.refresh();

    expect(writes(RULE_AT_CURSOR_KEY)).toEqual(['ruleWithExamples', undefined, 'rule']);
  });
});

describe('the rule at the cursor, for the commands', () => {
  async function at(line: number, file = 'a.yaml'): Promise<Rig> {
    const made = rig([ANSWER]);
    await start(made);
    open(file, line, 4);
    return made;
  }

  it('is the location of the rule the cursor is in', async () => {
    const made = await at(15);

    expect(made.view.locationAtCursor()?.range.start).toEqual({ line: 14, character: 18 });
    expect(made.view.locationAtCursor()?.uri).toBe(`${W}/a.yaml`);
  });

  it('is none between two rules, and in a file with no rules', async () => {
    const between = await at(13);
    expect(between.view.locationAtCursor()).toBeUndefined();

    const elsewhere = await at(15, '.github/workflows/ci.yaml');
    expect(elsewhere.view.locationAtCursor()).toBeUndefined();
  });

  it('is looked up now, whatever the key said: a cursor that moved since is not run at its old rule', async () => {
    const made = await at(11);
    expect(contextKeys.get(RULE_AT_CURSOR_KEY)).toBe('ruleWithExamples');

    // The cursor moves with no event reaching the view yet.
    activeEditor.line = 19;

    expect(made.view.locationAtCursor()?.range.start.line).toBe(18);
  });
});

describe('the rule at the cursor of the editor a menu was opened in (review of #48)', () => {
  /** An editor group of a split editor, showing `file` with its cursor at `line`. */
  function group(file: string, line: number): FakeEditor {
    const editor = new FakeEditor({
      languageId: 'yaml',
      getText: () => '',
      uri: Uri.parse(`${W}/${file}`),
    });
    editor.selection = { active: { line, character: 4 } };
    visibleEditors.push(editor);
    return editor;
  }

  /** Two groups: the active editor shows a.yaml with the cursor in its rule 0, and another shows b.yaml with the cursor in its rule 1. */
  async function split(): Promise<Rig> {
    const made = rig([ANSWER]);
    await start(made);
    group('a.yaml', 11);
    group('b.yaml', 15);
    open('a.yaml', 11, 4);
    return made;
  }

  it('is the rule of the active editor for no URI, as from the palette', async () => {
    const made = await split();

    expect(made.view.locationAtCursor()?.uri).toBe(`${W}/a.yaml`);
    expect(made.view.locationAtCursor()?.range.start.line).toBe(10);
  });

  it('is the rule at the cursor of the other group, not of the active editor, for the URI of the other group’s file', async () => {
    const made = await split();

    const location = made.view.locationAtCursor(`${W}/b.yaml`);

    expect(location?.uri).toBe(`${W}/b.yaml`);
    expect(location?.range.start.line).toBe(14);
  });

  it('is the rule of the active editor for the URI of its own file', async () => {
    const made = await split();

    expect(made.view.locationAtCursor(`${W}/a.yaml`)?.range.start.line).toBe(10);
  });

  it('is none when no visible editor shows the file, whatever the active editor is in', async () => {
    const made = await split();

    expect(made.view.locationAtCursor(`${W}/c.yaml`)).toBeUndefined();
    expect(made.view.locationAtCursor(`${W}/crd-not-open.yaml`)).toBeUndefined();
  });

  it('is none for the file of the other group when its cursor is between two rules, though the active editor is in a rule', async () => {
    const made = rig([ANSWER]);
    await start(made);
    group('b.yaml', 13);
    open('a.yaml', 11, 4);

    expect(made.view.locationAtCursor(`${W}/b.yaml`)).toBeUndefined();
    expect(made.view.locationAtCursor()).toBeDefined();
  });
});

describe('Reveal in Rule View', () => {
  async function at(line: number, over: { problemsOnly?: boolean } = {}): Promise<Rig> {
    const made = rig([ANSWER]);
    await start(made);
    if (over.problemsOnly === true) {
      made.view.setProblemsOnly(true);
    }
    open('a.yaml', line, 4);
    return made;
  }

  it('selects the rule at the cursor, with the focus, expanded, and opens the view', async () => {
    const made = await at(15);
    view().revealed.length = 0;

    await made.view.revealAtCursor();

    expect(view().revealed).toEqual([
      {
        element: there(made.view.getChildren(there(made.view.getChildren()[0]))[1]),
        options: { select: true, focus: true, expand: true },
      },
    ]);
  });

  it('selects the document when the cursor is between two rules', async () => {
    const made = await at(13);
    view().revealed.length = 0;

    await made.view.revealAtCursor();

    expect(view().revealed.map((each) => each.element.kind)).toEqual(['document']);
    expect(view().revealed[0]?.options).toEqual({ select: true, focus: true, expand: true });
  });

  it('selects the document of a rule the filter hides', async () => {
    // Rules 0 and 2 of a.yaml compile, and the filter shows only rules with a problem.
    const uri = `${W}/a.yaml`;
    const document = 'CustomResourceDefinition a.example.com';
    const answer = rawAnswer(
      [
        rawDocument(
          uri,
          [
            rawRule(uri, document, 0),
            rawRule(uri, document, 1, { compile: 'error' }),
            rawRule(uri, document, 2),
          ],
          { name: 'a.example.com', folder: W, file: 'a.yaml' },
        ),
      ],
      { generation: 1 },
    );
    const made = rig([answer]);
    await start(made);
    made.view.setProblemsOnly(true);
    open('a.yaml', 11, 4);
    view().revealed.length = 0;

    await made.view.revealAtCursor();

    expect(view().revealed.map((each) => each.element.kind)).toEqual(['document']);
  });

  it('only opens the view when the file has no rule or document at the cursor', async () => {
    const made = await at(15);
    open('.github/workflows/ci.yaml', 3);
    view().revealed.length = 0;
    executedCommands.length = 0;

    await made.view.revealAtCursor();

    expect(view().revealed).toEqual([]);
    expect(executedCommands).toEqual([{ command: 'cel-lens.rules.focus', args: [] }]);
  });

  it('logs a reveal that fails, and does not reject', async () => {
    const made = await at(15);
    view().reveal = () => Promise.reject(new Error('no such row'));

    await expect(made.view.revealAtCursor()).resolves.toBeUndefined();

    expect(made.log.lines.some((line) => line.includes('no such row'))).toBe(true);
  });
});

describe('opening the view from the palette', () => {
  it('opens the view when it is not showing, and leaves it alone when it is', async () => {
    const made = rig([ANSWER]);
    await start(made);
    executedCommands.length = 0;

    view().visible = false;
    made.view.showView();
    view().visible = true;
    made.view.showView();

    expect(executedCommands).toEqual([{ command: 'cel-lens.rules.focus', args: [] }]);
  });
});
