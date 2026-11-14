import * as vscode from 'vscode';

import { SHOW_LOGS_COMMAND } from './actions_menu';
import { SHOW_LOGS_LABEL } from './button_labels';
import { SHOW_EXAMPLE_RESULTS_COMMAND } from './example_results';
import { readExampleResults } from './example_results_setting';
import { EXPERIMENTAL_SETTING, parseExperimental } from './experimental';
import { isRecord } from './is_record';
import type { ServerState } from './kubernetes_version';
import { ruleTooltip, textInRange } from './rule_items';
import { cursorKey, ruleAtCursor, type CursorKey } from './rule_at_cursor';
import { FOLLOW_PAUSE_MS, FollowEditor, readFollowEditor, writeFollowEditor } from './rule_follow';
import {
  ENGINE_RULE_LIST,
  RuleList,
  parseRuleListAnswer,
  parseListedCommands,
  parseRuleListChange,
  isUnknownCommandError,
  parseScanLimits,
  type ScanLimits,
} from './rule_list';
import { SHOW_RULE_COMMAND, parseRuleLocation, type RuleLocation } from './rule_parts';
import { RuleTree, scanMessage, type Refresh, type TreeContext, type TreeNode } from './rule_tree';
import {
  EDITOR_HAS_RULES_KEY,
  FOLLOW_EDITOR_KEY,
  KYVERNO_OFF_KEY,
  PROBLEMS_ONLY_KEY,
  RULE_AT_CURSOR_KEY,
  RULE_VIEW_ID,
  RULE_VIEW_STATE_KEY,
  isLooking,
  ruleViewState,
  type EngineStatus,
  type RuleViewState,
  type ViewFacts,
} from './rule_view_state';

/** The context menu's *Show Problems*: `cel-lens.showRule` for the rule the menu was opened on. */
export const SHOW_RULE_PROBLEMS_COMMAND = 'cel-lens.rules.showProblems';

/** The context menu's *Show Example Results*: `cel-lens.showExampleResults` for that rule. */
export const SHOW_RULE_EXAMPLES_COMMAND = 'cel-lens.rules.showExampleResults';

/** How long typing pauses before the extension asks the engine what changed. */
export const CHANGE_PAUSE_MS = 500;

/** The title menu's *Show Problems Only* and *Show All Rules*: the filter. */
export const SHOW_PROBLEMS_ONLY_COMMAND = 'cel-lens.rules.showProblemsOnly';
export const SHOW_ALL_RULES_COMMAND = 'cel-lens.rules.showAll';

/** The title menu's *Follow Editor* and *Stop Following*. */
export const FOLLOW_EDITOR_COMMAND = 'cel-lens.rules.followEditor';
export const STOP_FOLLOWING_COMMAND = 'cel-lens.rules.stopFollowing';

/** The command VS Code makes for a view: it opens the view and gives it the focus. */
export const FOCUS_RULE_VIEW_COMMAND = `${RULE_VIEW_ID}.focus`;

/** The key under which the filter is kept in the workspace's state. */
export const PROBLEMS_ONLY_STATE = 'cel-lens.ruleView.problemsOnly';

/** The most characters of a rule's CEL a tooltip shows. */
const TOOLTIP_CEL_LIMIT = 2000;

/** Runs one of the engine's commands: `ServerController.runEngineCommand`. */
export type RunEngineCommand = (command: string, argument?: unknown) => Promise<unknown>;

type ViewLog = Pick<vscode.LogOutputChannel, 'debug' | 'warn' | 'error'>;

/** What the view is made with besides the engine and the log. */
export interface RuleViewOptions {
  /** The workspace's state, where the filter is kept; the filter is not kept without it. */
  readonly state?: Pick<vscode.Memento, 'get' | 'update'>;
  /** How long typing pauses before the list is asked for again. */
  readonly pauseMs?: number;
  /** How long the cursor rests before the view follows it. */
  readonly followMs?: number;
}

/** The badge's tooltip: how many rules have a problem, in words. */
export function badgeTooltip(problems: number): string {
  return problems === 1 ? '1 rule with a problem' : `${String(problems)} rules with problems`;
}

const COLLAPSIBLE = {
  none: vscode.TreeItemCollapsibleState.None,
  collapsed: vscode.TreeItemCollapsibleState.Collapsed,
  expanded: vscode.TreeItemCollapsibleState.Expanded,
} as const;

/** A URI in one spelling, so VS Code's and the engine's of one folder compare equal. */
function normalize(uri: string): string {
  return vscode.Uri.parse(uri).toString().replace(/\/+$/, '');
}

/** Whether a document is one the engine reads rules from: YAML, or the `.cellens` that types its values. */
function isListed(document: { languageId: string; uri: vscode.Uri }): boolean {
  // The engine reads files on disk only: not `untitled:`, `git:` or the other schemes of a diff.
  return (
    document.uri.scheme === 'file' &&
    (document.languageId === 'yaml' || document.uri.toString().endsWith('.cellens'))
  );
}

/** The location of the rule a menu was opened on; the argument is the tree's own node. */
export function ruleLocationOf(node: unknown): RuleLocation | undefined {
  return isRecord(node) && node['kind'] === 'rule'
    ? parseRuleLocation(node['location'])
    : undefined;
}

/**
 * The cel-lens view: the rules of the workspace in a tree. It asks the engine for the list
 * (`cel-lens.ruleList`), keeps it in a model, draws only what an answer changed, and says why when
 * there is nothing to draw. It decides nothing about a rule: the engine says its words and state.
 *
 * What makes it ask again: the engine starting, the workspace folders or a setting changing, and
 * `onRuleListChanged`, which is where the engine's notification `cel-lens/ruleListChanged` is
 * handed in. Until the engine has sent that notification (an engine that sends none, such as the
 * first one to list rules) a file saved, changed (once typing pauses), opened or closed makes it
 * ask too; after it, the engine says when its list changed and the text of a file does not.
 * After each answer it follows the editor, and it keeps the filter for problems and the badge.
 */
export class RuleView implements vscode.TreeDataProvider<TreeNode>, vscode.Disposable {
  private readonly emitter = new vscode.EventEmitter<TreeNode | TreeNode[] | undefined>();
  readonly onDidChangeTreeData = this.emitter.event;

  private readonly list = new RuleList();
  private readonly tree: RuleTree;
  private readonly treeView: vscode.TreeView<TreeNode>;
  private readonly follower: FollowEditor;
  private readonly state: Pick<vscode.Memento, 'get' | 'update'> | undefined;
  /** Only the rules with a problem show. */
  private problemsOnly: boolean;
  /** The count the badge shows now. */
  private shownProblems = 0;
  /** The engine's spelling of the files the editor spells otherwise, found when asked. */
  private readonly engineUris = new Map<string, string | undefined>();
  private readonly subscriptions: vscode.Disposable[] = [];
  /** What `cel-lens.editorHasRules` and `cel-lens.ruleAtCursor` were last set to; `undefined` before the first. */
  private shownHasRules: boolean | undefined;
  private shownAtCursor: CursorKey | undefined;

  private engine: EngineStatus = 'starting';
  private limits: ScanLimits = {};
  /** Goes up whenever the engine starts or stops: an answer of an older engine is dropped. */
  private epoch = 0;
  private fetching: Promise<void> | undefined;
  private again = false;
  /** The engine refused the command, or answered what this extension does not read, and never a list. */
  private refused = false;
  /** The last ask failed for a reason that is not the engine not knowing the command, and nothing was answered. */
  private failed = false;
  /** How many entries of the answers the extension left out, as last logged. */
  private loggedSkipped = 0;
  private lastError: string | undefined;
  private shownState: RuleViewState | undefined;
  private endProgress: (() => void) | undefined;
  private timer: NodeJS.Timeout | undefined;
  private readonly pauseMs: number;
  /** The engine has sent `cel-lens/ruleListChanged`: it says itself when the list changes, so the text of a file need not make the view ask. */
  private notified = false;

  constructor(
    private readonly run: RunEngineCommand,
    private readonly log: ViewLog,
    options: RuleViewOptions = {},
  ) {
    this.pauseMs = options.pauseMs ?? CHANGE_PAUSE_MS;
    this.state = options.state;
    this.problemsOnly = this.state?.get<boolean>(PROBLEMS_ONLY_STATE, false) === true;
    this.tree = new RuleTree(this.list, this.context());
    this.treeView = vscode.window.createTreeView(RULE_VIEW_ID, {
      treeDataProvider: this,
      showCollapseAll: true,
    });
    this.follower = new FollowEditor(
      this.treeView,
      (uri, line, character) => this.nodeAt(uri, line, character),
      readFollowEditor,
      this.log,
      options.followMs ?? FOLLOW_PAUSE_MS,
    );
    const later = (document: { languageId: string; uri: vscode.Uri }): void => {
      if (isListed(document)) {
        this.fileChanged();
      }
    };
    this.subscriptions.push(
      this.emitter,
      this.treeView,
      this.follower,
      vscode.workspace.onDidSaveTextDocument(later),
      vscode.workspace.onDidChangeTextDocument((event) => {
        later(event.document);
      }),
      vscode.workspace.onDidOpenTextDocument(later),
      vscode.workspace.onDidCloseTextDocument((document) => {
        // A closed document has no language to ask for; an open file that left the list is the engine's to say.
        if (document.uri.scheme === 'file') {
          this.fileChanged();
        }
      }),
      vscode.workspace.onDidChangeWorkspaceFolders(() => {
        this.redraw();
        this.refreshSoon();
      }),
      // The editor's submenu follows the file and the cursor with no pause: a context key is cheap.
      vscode.window.onDidChangeActiveTextEditor(() => {
        this.syncEditorKeys();
      }),
      vscode.window.onDidChangeTextEditorSelection((event) => {
        if (
          event.textEditor.document.uri.toString() ===
          vscode.window.activeTextEditor?.document.uri.toString()
        ) {
          this.syncEditorKeys();
        }
      }),
    );
    this.syncKyverno();
    this.syncKeys();
    this.syncEditorKeys();
    this.syncState();
  }

  // The engine's life: the controller says when it starts, is up and goes.

  /** Follows the controller's state: starting and the states of an engine that is gone. A running engine says so through `engineStarted`. */
  engineState(state: ServerState): void {
    if (state.kind === 'starting') {
      this.engineStarting();
    } else if (state.kind !== 'running') {
      this.engineStopped();
    }
  }

  /** The engine is starting: the list it gave is no longer true. */
  engineStarting(): void {
    this.engine = 'starting';
    this.forget();
  }

  /** The engine is not there: stopped, failed to start, or crashed. */
  engineStopped(): void {
    this.engine = 'down';
    this.forget();
  }

  /** The engine is up and has said its limits; the whole list is asked for. */
  engineStarted(initializeResult: unknown): void {
    this.limits = parseScanLimits(initializeResult);
    this.engine = 'running';
    this.fire(this.tree.rebuild(this.context()));
    // An engine that lists its commands and not this one cannot answer; asking would only fail.
    const listed = parseListedCommands(initializeResult);
    this.refused = listed.length > 0 && !listed.includes(ENGINE_RULE_LIST);
    this.syncState();
    void this.refresh();
  }

  /**
   * The seam for the engine's notification `cel-lens/ruleListChanged`, `{generation, scan}`: it
   * takes the scan's progress at once, and asks for what changed `since` the list's generation
   * when the notification is ahead of it. The controller hands the notification's params here.
   */
  onRuleListChanged(params: unknown): void {
    const change = parseRuleListChange(params);
    if (change === undefined) {
      this.log.debug('The engine sent a cel-lens/ruleListChanged this extension does not read.');
      return;
    }
    if (change.scan !== undefined) {
      this.list.setScan(change.scan);
      this.fire(this.tree.setFilesLeftOut(change.scan.filesLeftOut));
      this.syncState();
    }
    this.notified = true;
    // The view asks only when the notification's generation is ahead of the list's. That depends on
    // the engine seeding its generations from the clock when it starts (cel-lens-core 36): an
    // engine that the language client restarts by itself, with no state change reaching this view,
    // is then caught by its first notification being ahead of the old generation, and is asked
    // `since`, which it answers with the whole list. An engine that restarted at a generation at
    // or below the old one would never be asked again.
    const known = this.list.generation;
    if (change.generation === undefined || known === undefined || change.generation > known) {
      void this.refresh();
    }
  }

  // Asking.

  /** Asks the engine for the list, or for what changed since the last answer; asks again once if told to meanwhile. */
  refresh(): Promise<void> {
    if (this.engine !== 'running' || (this.refused && !this.list.hasAnswer)) {
      return Promise.resolve();
    }
    if (this.fetching !== undefined) {
      this.again = true;
      return this.fetching;
    }
    const epoch = this.epoch;
    const fetching: Promise<void> = this.fetchUntilDone(epoch).finally(() => {
      if (this.fetching === fetching) {
        this.fetching = undefined;
      }
    });
    this.fetching = fetching;
    return fetching;
  }

  /**
   * A file was saved, changed, opened or closed. An engine that sends the notification of a change
   * says when its list changed, so the view asks only on that; one that does not is asked once
   * typing pauses.
   */
  private fileChanged(): void {
    if (!this.notified) {
      this.refreshSoon();
    }
  }

  /** Asks once typing pauses, so a burst of changes is one question. */
  refreshSoon(): void {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void this.refresh();
    }, this.pauseMs);
  }

  /** Draws the rows again with the folders and settings as they are now: the list is not asked for. */
  redraw(): void {
    this.fire(this.tree.rebuild(this.context()));
    this.syncKyverno();
    this.syncState();
    this.syncEditorKeys();
  }

  private async fetchUntilDone(epoch: number): Promise<void> {
    this.again = true;
    while (this.again && this.epoch === epoch && this.engine === 'running') {
      this.again = false;
      await this.fetchOnce(epoch);
    }
  }

  private async fetchOnce(epoch: number): Promise<void> {
    const generation = this.list.generation;
    const asked = generation === undefined ? 'all' : 'since';
    let value: unknown;
    try {
      value = await this.run(
        ENGINE_RULE_LIST,
        asked === 'since' ? { since: generation } : undefined,
      );
    } catch (error) {
      if (epoch === this.epoch) {
        this.refuse(error);
      }
      return;
    }
    if (epoch !== this.epoch) {
      return;
    }
    const answer = parseRuleListAnswer(value);
    if (answer === undefined) {
      if (this.list.shouldLogUnread()) {
        this.log.warn(
          `${ENGINE_RULE_LIST} answered something this extension does not read, so the list stays as it was; the engine may not match this extension.`,
        );
      }
      this.refused = !this.list.hasAnswer;
      this.syncState();
      return;
    }
    this.refused = false;
    this.failed = false;
    this.lastError = undefined;
    if (answer.skipped !== this.loggedSkipped) {
      this.loggedSkipped = answer.skipped;
      if (answer.skipped > 0) {
        this.log.warn(
          `${ENGINE_RULE_LIST} listed ${String(answer.skipped)} documents or rules this extension does not read; each shows as a warning and counts as a problem.`,
        );
      }
    }
    const refresh = this.tree.update(this.list.apply(answer, asked));
    this.engineUris.clear();
    this.fire([
      ...refresh,
      ...this.tree.setFilesLeftOut(this.list.scan.filesLeftOut),
      ...this.tree.setUnreadable(this.list.unattributed),
    ]);
    this.syncState();
    this.syncEditorKeys();
    this.follower.schedule();
  }

  private refuse(error: unknown): void {
    const message = error instanceof Error ? error.message : String(error);
    if (message !== this.lastError) {
      this.lastError = message;
      this.log.error(`The engine could not list the rules: ${message}`);
    }
    // Only an engine that does not know the command cannot list rules; another error is a failure of this ask.
    this.refused = !this.list.hasAnswer && isUnknownCommandError(error);
    this.failed = !this.list.hasAnswer && !this.refused;
    this.syncState();
  }

  /** Starts over for an engine that knows no generation of ours. */
  private forget(): void {
    this.epoch += 1;
    this.fetching = undefined;
    this.again = false;
    this.refused = false;
    this.failed = false;
    this.loggedSkipped = 0;
    this.notified = false;
    this.lastError = undefined;
    clearTimeout(this.timer);
    this.timer = undefined;
    this.list.reset();
    this.fire(this.tree.rebuild(this.context()));
    this.fire(this.tree.setFilesLeftOut(0));
    this.fire(this.tree.setUnreadable(0));
    this.engineUris.clear();
    this.syncState();
    this.syncEditorKeys();
  }

  // What the tree is drawn with.

  private context(): TreeContext {
    return {
      folders: (vscode.workspace.workspaceFolders ?? []).map((folder) => ({
        uri: folder.uri.toString(),
        name: folder.name,
      })),
      normalize,
      showExamples: (uri) => readExampleResults(vscode.Uri.parse(uri)),
      limits: this.limits,
      problemsOnly: this.problemsOnly,
    };
  }

  private fire(refresh: Refresh): void {
    if (refresh.length === 0) {
      return;
    }
    const nodes = refresh.filter((node): node is TreeNode => node !== undefined);
    this.emitter.fire(nodes.length < refresh.length ? undefined : nodes);
  }

  // What the view says besides its rows.

  private facts(): ViewFacts {
    return {
      engine: this.engine,
      unsupported: this.refused,
      failed: this.failed,
      folders: vscode.workspace.workspaceFolders?.length ?? 0,
      answered: this.list.hasAnswer,
      scanning: this.list.scan.state === 'scanning',
      problemsOnly: this.problemsOnly,
      listed: this.list.files.length > 0,
      rows: !this.tree.isEmpty,
    };
  }

  /** Sets the context key the welcome texts read, the message and the progress bar. */
  private syncState(): void {
    const facts = this.facts();
    const state = ruleViewState(facts);
    if (state !== this.shownState) {
      this.shownState = state;
      void vscode.commands.executeCommand('setContext', RULE_VIEW_STATE_KEY, state);
    }
    const scan = this.list.scan;
    this.treeView.message =
      this.engine === 'running' && scan.state === 'scanning'
        ? scanMessage(scan.yamlFiles, scan.filesWithRules)
        : '';
    this.syncProgress(isLooking(facts));
    this.syncBadge();
  }

  /** Shows how many rules have a problem on the view's badge, whatever the filter, and none at 0. */
  private syncBadge(): void {
    const problems = this.list.problems;
    if (problems === this.shownProblems) {
      return;
    }
    this.shownProblems = problems;
    this.treeView.badge =
      problems > 0 ? { value: problems, tooltip: badgeTooltip(problems) } : undefined;
  }

  private syncProgress(looking: boolean): void {
    if (looking && this.endProgress === undefined) {
      void vscode.window.withProgress({ location: { viewId: RULE_VIEW_ID } }, () => {
        return new Promise<void>((resolve) => {
          this.endProgress = resolve;
        });
      });
    } else if (!looking && this.endProgress !== undefined) {
      this.endProgress();
      this.endProgress = undefined;
    }
  }

  /** Sets the context keys of the title menu: whether the filter is on, and whether the view follows the editor. */
  private syncKeys(): void {
    void vscode.commands.executeCommand('setContext', PROBLEMS_ONLY_KEY, this.problemsOnly);
    void vscode.commands.executeCommand('setContext', FOLLOW_EDITOR_KEY, readFollowEditor());
  }

  private syncKyverno(): void {
    const features = parseExperimental(
      vscode.workspace.getConfiguration('cel-lens').get<unknown>(EXPERIMENTAL_SETTING, []),
    );
    void vscode.commands.executeCommand(
      'setContext',
      KYVERNO_OFF_KEY,
      !features.includes('kyverno'),
    );
  }

  // The title menu.

  /** Shows only the rules with a problem, or all, and keeps the choice for this workspace. */
  setProblemsOnly(on: boolean): void {
    if (on === this.problemsOnly) {
      return;
    }
    this.problemsOnly = on;
    this.syncKeys();
    this.fire(this.tree.rebuild(this.context()));
    this.syncState();
    this.follower.schedule();
    if (this.state !== undefined) {
      this.state.update(PROBLEMS_ONLY_STATE, on).then(undefined, (error: unknown) => {
        this.log.error(
          `Could not keep the filter of the view for this workspace: ${String(error)}`,
        );
      });
    }
  }

  /** Whether only the rules with a problem show. */
  get showsProblemsOnly(): boolean {
    return this.problemsOnly;
  }

  /** Turns following the editor on or off, by the setting. A write that fails is logged. */
  async setFollowEditor(on: boolean): Promise<void> {
    try {
      await writeFollowEditor(on);
    } catch (error) {
      this.log.error(`Could not change cel-lens.ruleView.followEditor: ${String(error)}`);
    }
    this.followEditorChanged();
  }

  /** The setting `cel-lens.ruleView.followEditor` changed: the title menu shows the other command, and the view follows from now on. */
  followEditorChanged(): void {
    this.syncKeys();
    this.follower.schedule();
  }

  /** The row at a position of an editor's file, which the engine may spell otherwise than the editor does. */
  private nodeAt(uri: vscode.Uri, line: number, character: number): TreeNode | undefined {
    const engine = this.engineUriOf(uri);
    return engine === undefined ? undefined : this.tree.at(engine, line, character);
  }

  private engineUriOf(uri: vscode.Uri): string | undefined {
    const text = uri.toString();
    if (this.list.documentsOf(text).length > 0) {
      return text;
    }
    if (!this.engineUris.has(text)) {
      const wanted = normalize(text);
      this.engineUris.set(
        text,
        this.list.files.find((each) => normalize(each) === wanted),
      );
    }
    return this.engineUris.get(text);
  }

  // The editor's submenu.

  /**
   * Sets `cel-lens.editorHasRules` (the active editor's file is one the engine lists rules for, in
   * the editor's spelling of its URI) and `cel-lens.ruleAtCursor` (`rule`, `ruleWithExamples` or
   * unset), each only when it changes. It reads the list the view holds: nothing is asked of the
   * engine, so with no list (the engine down, or unable to list rules) both are off.
   */
  private syncEditorKeys(): void {
    const editor = vscode.window.activeTextEditor;
    const engine = editor === undefined ? undefined : this.engineUriOf(editor.document.uri);
    const has = engine !== undefined;
    if (has !== this.shownHasRules) {
      this.shownHasRules = has;
      void vscode.commands.executeCommand('setContext', EDITOR_HAS_RULES_KEY, has);
    }
    const key =
      editor === undefined || engine === undefined
        ? undefined
        : cursorKey(
            this.list.documentsOf(engine),
            editor.selection.active.line,
            editor.selection.active.character,
            readExampleResults(editor.document.uri),
          );
    if (key !== this.shownAtCursor) {
      this.shownAtCursor = key;
      void vscode.commands.executeCommand('setContext', RULE_AT_CURSOR_KEY, key);
    }
  }

  /**
   * The location of the rule at a cursor, looked up now; `undefined` in no rule. With no `uri` (the
   * palette) it is the cursor of the active editor. With the URI of a file (an editor's menu gives
   * its editor's), it is the cursor of the visible editor showing that file, so the menu of one
   * group of a split editor never acts on the rule of the active editor of another; with no visible
   * editor showing it there is no rule.
   */
  locationAtCursor(uri?: string): RuleLocation | undefined {
    const editor = uri === undefined ? vscode.window.activeTextEditor : this.editorShowing(uri);
    const engine = editor === undefined ? undefined : this.engineUriOf(editor.document.uri);
    if (editor === undefined || engine === undefined) {
      return undefined;
    }
    const { line, character } = editor.selection.active;
    return ruleAtCursor(this.list.documentsOf(engine), line, character)?.rule?.location;
  }

  /** The visible editor showing the file at `uri`: the active one when it does, else the first. */
  private editorShowing(
    uri: string,
  ): Pick<vscode.TextEditor, 'document' | 'selection'> | undefined {
    const active = vscode.window.activeTextEditor;
    if (active?.document.uri.toString() === uri) {
      return active;
    }
    return vscode.window.visibleTextEditors.find((each) => each.document.uri.toString() === uri);
  }

  /**
   * *Reveal in Rule View*: selects the rule at the cursor in the view, else its document (also for
   * a rule the filter hides), with the focus, expanded, opening the view. With neither it only
   * opens the view.
   */
  async revealAtCursor(): Promise<void> {
    const editor = vscode.window.activeTextEditor;
    const engine = editor === undefined ? undefined : this.engineUriOf(editor.document.uri);
    const node =
      editor === undefined || engine === undefined
        ? undefined
        : this.tree.at(engine, editor.selection.active.line, editor.selection.active.character);
    try {
      if (node === undefined) {
        await vscode.commands.executeCommand(FOCUS_RULE_VIEW_COMMAND);
        return;
      }
      await this.treeView.reveal(node, { select: true, focus: true, expand: true });
    } catch (error) {
      this.log.error(`Could not reveal the rule in the view: ${String(error)}`);
    }
  }

  /** Opens the view when it is not showing, so what a command changed in it is seen. */
  showView(): void {
    if (!this.treeView.visible) {
      void vscode.commands.executeCommand(FOCUS_RULE_VIEW_COMMAND);
    }
  }

  // The tree's provider.

  getChildren(node?: TreeNode): TreeNode[] {
    return node === undefined ? this.tree.roots() : node.children;
  }

  getParent(node: TreeNode): TreeNode | undefined {
    return node.parent;
  }

  getTreeItem(node: TreeNode): vscode.TreeItem {
    const item = new vscode.TreeItem(node.label, COLLAPSIBLE[node.collapsible]);
    item.id = node.id;
    if (node.description !== undefined) {
      item.description = node.description;
    }
    item.iconPath = new vscode.ThemeIcon(
      node.icon.id,
      node.icon.color === undefined ? undefined : new vscode.ThemeColor(node.icon.color),
    );
    item.contextValue = node.contextValue;
    item.accessibilityInformation = { label: node.accessible };
    if (node.kind === 'document' && node.document !== undefined) {
      item.tooltip = `${node.label}\n${node.description ?? ''}`;
    } else if (node.kind === 'unreadable') {
      item.tooltip =
        'cel-lens could not read this document in the engine’s answer, so it counts as a problem; the engine may not match this extension.';
    }
    if (node.action === 'openRule' && node.location !== undefined) {
      const start = node.location.range.start;
      item.command = {
        command: 'vscode.open',
        title: 'Open Rule',
        arguments: [
          vscode.Uri.parse(node.location.uri),
          {
            selection: new vscode.Range(start.line, start.character, start.line, start.character),
            preview: true,
            preserveFocus: false,
          } satisfies vscode.TextDocumentShowOptions,
        ],
      };
    } else if (node.action === 'showLogs') {
      item.command = { command: SHOW_LOGS_COMMAND, title: SHOW_LOGS_LABEL };
    }
    return item;
  }

  /** A rule's tooltip is made when it is asked for: it reads the rule's CEL, which the list may not carry. */
  async resolveTreeItem(item: vscode.TreeItem, node: TreeNode): Promise<vscode.TreeItem> {
    if (node.kind === 'rule' && node.rule !== undefined && node.document !== undefined) {
      const expression = node.rule.expression ?? (await this.readExpression(node.location));
      item.tooltip = new vscode.MarkdownString(
        ruleTooltip(node.rule, node.document.kind, node.showExamples, expression),
      );
    }
    return item;
  }

  /**
   * The CEL of a rule for its tooltip, read without opening a document: opening one would make the
   * language client send the engine a didOpen, so the engine would check the file as open (and put
   * its problems in the Problems view), and this view would ask for the list again. An open
   * document gives its text; a closed file is read as bytes.
   */
  private async readExpression(location: RuleLocation | undefined): Promise<string | undefined> {
    if (location === undefined) {
      return undefined;
    }
    try {
      const uri = vscode.Uri.parse(location.uri);
      const { start, end } = location.range;
      const open = vscode.workspace.textDocuments.find(
        (document) => document.uri.toString() === uri.toString(),
      );
      const text =
        open === undefined
          ? textInRange(
              new TextDecoder().decode(await vscode.workspace.fs.readFile(uri)),
              start,
              end,
            )
          : open.getText(new vscode.Range(start.line, start.character, end.line, end.character));
      return text.length > TOOLTIP_CEL_LIMIT ? `${text.slice(0, TOOLTIP_CEL_LIMIT)}…` : text;
    } catch (error) {
      this.log.debug(`Could not read the rule's CEL for its tooltip: ${String(error)}`);
      return undefined;
    }
  }

  // The context menu.

  /** *Show Problems*: the rule's own `cel-lens.showRule`, with the rule's location. */
  async showProblems(node: unknown): Promise<void> {
    await this.runOnRule(node, SHOW_RULE_COMMAND);
  }

  /** *Show Example Results*: the rule's own `cel-lens.showExampleResults`. */
  async showExampleResults(node: unknown): Promise<void> {
    await this.runOnRule(node, SHOW_EXAMPLE_RESULTS_COMMAND);
  }

  private async runOnRule(node: unknown, command: string): Promise<void> {
    const location = ruleLocationOf(node);
    if (location === undefined) {
      this.log.warn(`${command} was run from the view on a row that is no rule.`);
      return;
    }
    try {
      await vscode.commands.executeCommand(command, location);
    } catch (error) {
      this.log.error(`Could not run ${command}: ${String(error)}`);
    }
  }

  dispose(): void {
    clearTimeout(this.timer);
    this.epoch += 1;
    this.endProgress?.();
    this.endProgress = undefined;
    for (const each of this.subscriptions) {
      each.dispose();
    }
  }
}
