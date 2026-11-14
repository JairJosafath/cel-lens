import * as vscode from 'vscode';

import type { TreeNode } from './rule_tree';

/** The setting that makes the view follow the editor, under `cel-lens.`. */
export const FOLLOW_EDITOR_SETTING = 'ruleView.followEditor';

/** Follows the editor by default. */
export const DEFAULT_FOLLOW_EDITOR = true;

/** How long after the cursor last moved the view follows it. */
export const FOLLOW_PAUSE_MS = 100;

/** What the follower needs of the tree view: whether it shows, what is selected, and to select a row. */
export type FollowedView = Pick<vscode.TreeView<TreeNode>, 'visible' | 'selection' | 'reveal'>;

/** Whether the view follows the editor: the setting `cel-lens.ruleView.followEditor`. */
export function readFollowEditor(): boolean {
  return vscode.workspace
    .getConfiguration('cel-lens')
    .get<boolean>(FOLLOW_EDITOR_SETTING, DEFAULT_FOLLOW_EDITOR);
}

/**
 * Writes the setting, at the scope that sets it now (the workspace's or else the user's), so the
 * change takes effect. The window scope has no folder value.
 */
export async function writeFollowEditor(value: boolean): Promise<void> {
  const configuration = vscode.workspace.getConfiguration('cel-lens');
  const inspected = configuration.inspect<boolean>(FOLLOW_EDITOR_SETTING);
  await configuration.update(
    FOLLOW_EDITOR_SETTING,
    value,
    inspected?.workspaceValue === undefined
      ? vscode.ConfigurationTarget.Global
      : vscode.ConfigurationTarget.Workspace,
  );
}

/**
 * Selects, in the view, the rule the cursor is in, a moment after the cursor last moved or the
 * active editor changed. It selects and nothing more: the view's focus stays where it is, a view
 * that is not showing is not opened, and a row that is selected already is left alone, so a click
 * on a rule, which moves the cursor to it, does not move the view.
 */
export class FollowEditor implements vscode.Disposable {
  private timer: NodeJS.Timeout | undefined;
  /** The cursor or the editor moved since the follower last ran, so the person wants the view to move. */
  private cursorMoved = false;
  /** The id of the row the follower last selected: a selection that is another row is the person's. */
  private last: string | undefined;
  private readonly subscriptions: vscode.Disposable[];

  /**
   * `find` answers the row of the file `uri` (the editor's spelling) at a position, `undefined`
   * for none; `enabled` is whether following is on.
   */
  constructor(
    private readonly view: FollowedView,
    private readonly find: (
      uri: vscode.Uri,
      line: number,
      character: number,
    ) => TreeNode | undefined,
    private readonly enabled: () => boolean,
    private readonly log: Pick<vscode.LogOutputChannel, 'debug'>,
    private readonly delayMs = FOLLOW_PAUSE_MS,
  ) {
    this.subscriptions = [
      vscode.window.onDidChangeTextEditorSelection((event) => {
        if (
          event.textEditor.document.uri.toString() ===
          vscode.window.activeTextEditor?.document.uri.toString()
        ) {
          this.schedule('cursor');
        }
      }),
      vscode.window.onDidChangeActiveTextEditor(() => {
        this.schedule('cursor');
      }),
    ];
  }

  /**
   * Follows once the cursor has rested for the pause. `cursor`: the cursor or the active editor
   * moved, so the view follows whatever is selected. `view`: the list, the filter or the setting
   * changed under a cursor that did not move, so the view follows only where the person has not
   * picked a row: with nothing selected, or the row the follower selected itself.
   */
  schedule(reason: 'cursor' | 'view' = 'view'): void {
    clearTimeout(this.timer);
    this.cursorMoved ||= reason === 'cursor';
    this.timer = setTimeout(() => {
      this.timer = undefined;
      const moved = this.cursorMoved;
      this.cursorMoved = false;
      this.follow(moved);
    }, this.delayMs);
  }

  private follow(cursorMoved: boolean): void {
    const editor = vscode.window.activeTextEditor;
    if (
      !this.enabled() ||
      !this.view.visible ||
      editor?.document.uri.scheme !== 'file' ||
      editor.document.languageId !== 'yaml'
    ) {
      return;
    }
    const { line, character } = editor.selection.active;
    const node = this.find(editor.document.uri, line, character);
    if (node === undefined || this.view.selection.some((each) => each.id === node.id)) {
      return;
    }
    const picked = this.view.selection.find((each) => each.id !== this.last);
    if (!cursorMoved && picked !== undefined) {
      return;
    }
    this.last = node.id;
    void this.view
      .reveal(node, { select: true, focus: false })
      .then(undefined, (error: unknown) => {
        this.log.debug(`Could not show the rule the cursor is in: ${String(error)}`);
      });
  }

  dispose(): void {
    clearTimeout(this.timer);
    for (const each of this.subscriptions) {
      each.dispose();
    }
  }
}
