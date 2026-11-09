/**
 * A stand-in for the `vscode` module, which exists only inside VS Code's
 * extension host. vitest.config.mts points `vscode` here, so unit tests run in
 * plain Node. Each part `satisfies` the real API's types, so a stand-in that
 * drifts from VS Code fails the type check. Add only what tests need, and
 * clear its state in reset().
 */
import type * as vscode from 'vscode';

/** Runs a callback once when disposed, like vscode.Disposable. */
export class Disposable implements vscode.Disposable {
  private disposed = false;

  constructor(private readonly onDispose: () => void) {}

  dispose(): void {
    if (!this.disposed) {
      this.disposed = true;
      this.onDispose();
    }
  }
}

const registry = new Map<string, (...args: unknown[]) => unknown>();

/** The commands run with commands.executeCommand, oldest first. */
export const executedCommands: { command: string; args: unknown[] }[] = [];

/** The error the next calls to commands.executeCommand reject with, after recording the call, if any. */
export const commandFailure: { error: Error | undefined } = { error: undefined };

/** What commands.executeCommand resolves to, by command; undefined for the others. */
export const commandResults = new Map<string, unknown>();

export const commands = {
  registerCommand(command: string, callback: (...args: unknown[]) => unknown): Disposable {
    // VS Code refuses a second registration, which is how leaks show.
    if (registry.has(command)) {
      throw new Error(`command '${command}' already exists`);
    }
    registry.set(command, callback);
    return new Disposable(() => registry.delete(command));
  },
  getCommands(): Promise<string[]> {
    return Promise.resolve([...registry.keys()]);
  },
  executeCommand<T>(command: string, ...args: unknown[]): Thenable<T> {
    executedCommands.push({ command, args });
    if (commandFailure.error !== undefined) {
      return Promise.reject(commandFailure.error);
    }
    return Promise.resolve(commandResults.get(command) as T);
  },
} satisfies Partial<typeof vscode.commands>;

/** Runs a registered command, as vscode.commands.executeCommand does. */
export async function runCommand(command: string, ...args: unknown[]): Promise<unknown> {
  const callback = registry.get(command);
  if (callback === undefined) {
    throw new Error(`command '${command}' not found`);
  }
  return await callback(...args);
}

/** Drains the microtask queue, so a follow-up chained on a resolved promise has run. */
export async function settle(): Promise<void> {
  for (let turn = 0; turn < 10; turn += 1) {
    await Promise.resolve();
  }
}

/** The messages shown with window.showErrorMessage, oldest first. */
export const shownErrors: { message: string; buttons: string[] }[] = [];

/** The messages shown with window.showWarningMessage, oldest first. */
export const shownWarnings: { message: string; buttons: string[] }[] = [];

/** The button the user "clicks" in the next warning message, if any. */
export const warningChoice: { next: string | undefined } = { next: undefined };

/**
 * The buttons the user "clicks" in the next warning and error messages, in order: each
 * showWarningMessage or showErrorMessage call takes the first one (undefined dismisses it).
 * Once the queue is empty, the calls fall back to warningChoice and errorChoice.
 */
export const notificationChoices: (string | undefined)[] = [];

/** The messages shown with window.showInformationMessage, oldest first. */
export const shownInfos: string[] = [];

/** The button the user "clicks" in the next information message, if any. */
export const infoChoice: { next: string | undefined } = { next: undefined };

/** The button the user "clicks" in the next error message, if any. */
export const errorChoice: { next: string | undefined; never: boolean } = {
  next: undefined,
  never: false,
};

/** Every log output channel created, by name. */
export const channels = new Map<string, LogChannel>();

/** A log output channel that remembers its lines. */
export class LogChannel implements vscode.LogOutputChannel {
  readonly lines: string[] = [];
  // eslint-disable-next-line @typescript-eslint/no-unsafe-enum-assignment -- the enum exists only inside VS Code; 2 is LogLevel.Info
  readonly logLevel: vscode.LogLevel = 2;
  readonly onDidChangeLogLevel: vscode.Event<vscode.LogLevel> = () =>
    new Disposable(() => undefined);
  shown = false;
  disposed = false;

  constructor(readonly name: string) {}

  trace(message: string): void {
    this.lines.push(message);
  }

  debug(message: string): void {
    this.lines.push(message);
  }

  info(message: string): void {
    this.lines.push(message);
  }

  warn(message: string): void {
    this.lines.push(message);
  }

  error(message: string | Error): void {
    this.lines.push(String(message));
  }

  append(value: string): void {
    this.lines.push(value);
  }

  appendLine(value: string): void {
    this.lines.push(value);
  }

  replace(value: string): void {
    this.lines.splice(0, this.lines.length, value);
  }

  clear(): void {
    this.lines.length = 0;
  }

  show(): void {
    this.shown = true;
  }

  hide(): void {
    this.shown = false;
  }

  dispose(): void {
    this.disposed = true;
  }
}

/** Every status bar item created, oldest first. */
export const statusBarItems: FakeStatusBarItem[] = [];

/** A status bar item that remembers what it shows. */
class FakeStatusBarItem {
  text = '';
  tooltip: string | undefined;
  command: string | undefined;
  name: string | undefined;
  visible = false;
  disposed = false;

  show(): void {
    this.visible = true;
  }

  hide(): void {
    this.visible = false;
  }

  dispose(): void {
    this.disposed = true;
  }
}

/** The editor `window.activeTextEditor` returns: only its document's language and URI. */
export const activeEditor: { languageId: string | undefined; uri: string } = {
  languageId: undefined,
  uri: 'file:///workspace/active.yaml',
};

const editorListeners = new Set<() => void>();

/** Tells the listeners of onDidChangeActiveTextEditor that the editor changed. */
export function changeActiveEditor(languageId: string | undefined): void {
  activeEditor.languageId = languageId;
  for (const listener of editorListeners) {
    listener();
  }
}

/** The lines shown in quick picks, and the one the user "picks" next, by label. */
export const quickPicks: {
  shown: unknown[][];
  active: unknown[][];
  /** The options of each window.showQuickPick call, oldest first. */
  options: (vscode.QuickPickOptions | undefined)[];
  /** Each quick pick window.createQuickPick made, oldest first, as it was when shown. */
  created: {
    title: string | undefined;
    placeholder: string | undefined;
    matchOnDescription: boolean;
    matchOnDetail: boolean;
    busy: boolean;
    /** The labels of the lines it showed at once. */
    labels: string[];
  }[];
  next: string | undefined;
  /** What the user types in the filter of the next quick pick window.createQuickPick made. */
  typed: string;
  acceptAtOnce: boolean;
} = {
  shown: [],
  active: [],
  options: [],
  created: [],
  next: undefined,
  typed: '',
  acceptAtOnce: false,
};

/** The documents shown with window.showTextDocument, oldest first, with the selection asked for. */
export const shownDocuments: { uri: string; selection: unknown }[] = [];

/** The members of vscode.QuickPick the code under test uses; the stand-in is checked against them. */
type UsedQuickPick<T extends vscode.QuickPickItem> = Pick<
  vscode.QuickPick<T>,
  | 'title'
  | 'placeholder'
  | 'matchOnDescription'
  | 'matchOnDetail'
  | 'busy'
  | 'value'
  | 'items'
  | 'activeItems'
  | 'selectedItems'
  | 'onDidAccept'
  | 'onDidHide'
  | 'show'
  | 'hide'
  | 'dispose'
>;

/**
 * A quick pick that, once shown, moves the highlight to the `next` label and
 * accepts it, or accepts the highlight as it is when `acceptAtOnce`, or is
 * dismissed. Like VS Code, it highlights the first item unless told otherwise.
 * Shown busy, it waits until it is no longer busy, when its lines have come.
 */
class FakeQuickPick<T extends vscode.QuickPickItem> implements UsedQuickPick<T> {
  title: string | undefined;
  placeholder: string | undefined;
  matchOnDescription = false;
  matchOnDetail = false;
  /** The filter text. */
  value = '';
  selectedItems: T[] = [];
  private busyNow = false;
  private waiting = false;
  activeItems: T[] = [];
  private itemList: T[] = [];
  private readonly acceptListeners = new Set<() => void>();
  private readonly hideListeners = new Set<() => void>();

  get items(): T[] {
    return this.itemList;
  }

  set items(items: T[]) {
    this.itemList = items;
    this.activeItems = items.slice(0, 1);
  }

  get busy(): boolean {
    return this.busyNow;
  }

  set busy(busy: boolean) {
    this.busyNow = busy;
    if (!busy && this.waiting) {
      this.waiting = false;
      this.act();
    }
  }

  onDidAccept(listener: () => void): Disposable {
    this.acceptListeners.add(listener);
    return new Disposable(() => this.acceptListeners.delete(listener));
  }

  onDidHide(listener: () => void): Disposable {
    this.hideListeners.add(listener);
    return new Disposable(() => this.hideListeners.delete(listener));
  }

  show(): void {
    quickPicks.created.push({
      title: this.title,
      placeholder: this.placeholder,
      matchOnDescription: this.matchOnDescription,
      matchOnDetail: this.matchOnDetail,
      busy: this.busyNow,
      labels: this.itemList.map((item) => item.label),
    });
    if (this.busyNow) {
      this.waiting = true;
    } else {
      this.act();
    }
  }

  /** What the user does once the lines are there: picks the `next` label, or Escape. */
  private act(): void {
    quickPicks.shown.push(this.itemList);
    quickPicks.active.push([...this.activeItems]);
    this.value = quickPicks.typed;
    const target = this.itemList.find((item) => item.label === quickPicks.next);
    if (target !== undefined) {
      this.activeItems = [target];
    }
    if (target !== undefined || quickPicks.acceptAtOnce) {
      this.selectedItems = [...this.activeItems];
      for (const listener of this.acceptListeners) {
        listener();
      }
    } else {
      this.hide();
    }
  }

  hide(): void {
    for (const listener of this.hideListeners) {
      listener();
    }
  }

  dispose(): void {
    this.acceptListeners.clear();
    this.hideListeners.clear();
  }
}

/** The members of vscode.InputBox the code under test uses; the stand-in is checked against them. */
type UsedInputBox = Pick<
  vscode.InputBox,
  | 'title'
  | 'placeholder'
  | 'prompt'
  | 'value'
  | 'validationMessage'
  | 'busy'
  | 'enabled'
  | 'buttons'
  | 'onDidTriggerButton'
  | 'onDidChangeValue'
  | 'onDidAccept'
  | 'onDidHide'
  | 'show'
  | 'hide'
  | 'dispose'
>;

/** Every input box window.createInputBox made, oldest first. */
export const inputBoxes: FakeInputBox[] = [];

/** An input box a test drives as a person would: it types, presses Enter or Escape. */
export class FakeInputBox implements UsedInputBox {
  title: string | undefined;
  placeholder: string | undefined;
  prompt: string | undefined;
  value = '';
  validationMessage: string | vscode.InputBoxValidationMessage | undefined;
  busy = false;
  enabled = true;
  buttons: readonly vscode.QuickInputButton[] = [];
  visible = false;
  disposed = false;
  private readonly buttonListeners = new Set<(button: vscode.QuickInputButton) => void>();
  private readonly changeListeners = new Set<(value: string) => void>();
  private readonly acceptListeners = new Set<() => void>();
  private readonly hideListeners = new Set<() => void>();

  onDidTriggerButton(listener: (button: vscode.QuickInputButton) => void): Disposable {
    this.buttonListeners.add(listener);
    return new Disposable(() => this.buttonListeners.delete(listener));
  }

  onDidChangeValue(listener: (value: string) => void): Disposable {
    this.changeListeners.add(listener);
    return new Disposable(() => this.changeListeners.delete(listener));
  }

  onDidAccept(listener: () => void): Disposable {
    this.acceptListeners.add(listener);
    return new Disposable(() => this.acceptListeners.delete(listener));
  }

  onDidHide(listener: () => void): Disposable {
    this.hideListeners.add(listener);
    return new Disposable(() => this.hideListeners.delete(listener));
  }

  show(): void {
    this.visible = true;
  }

  /** Escape, or the code closing the box: tells the listeners once. */
  hide(): void {
    if (!this.visible) {
      return;
    }
    this.visible = false;
    for (const listener of this.hideListeners) {
      listener();
    }
  }

  dispose(): void {
    this.disposed = true;
    this.buttonListeners.clear();
    this.changeListeners.clear();
    this.acceptListeners.clear();
    this.hideListeners.clear();
  }

  /** Replaces the text, as typing does, and tells the listeners. */
  type(text: string): void {
    this.value = text;
    for (const listener of this.changeListeners) {
      listener(text);
    }
  }

  /** Clicks the button with this tooltip. */
  press(tooltip: string): void {
    for (const button of this.buttons.filter((each) => each.tooltip === tooltip)) {
      for (const listener of this.buttonListeners) {
        listener(button);
      }
    }
  }

  /** Presses Enter. */
  accept(): void {
    for (const listener of this.acceptListeners) {
      listener();
    }
  }
}

export const QuickPickItemKind = { Separator: -1, Default: 0 } as const;

/** A product icon, by its name. */
export class ThemeIcon {
  constructor(readonly id: string) {}
}

export const StatusBarAlignment = { Left: 1, Right: 2 } as const;

export const ConfigurationTarget = { Global: 1, Workspace: 2, WorkspaceFolder: 3 } as const;

/**
 * The window members whose real types are too large to rebuild (a whole
 * TextEditor, the overloaded quick pick). The stand-in has only what the code
 * under test reads; the type check still covers every other member.
 */
type LooseWindowMember =
  | 'createStatusBarItem'
  | 'showTextDocument'
  | 'activeTextEditor'
  | 'onDidChangeActiveTextEditor'
  | 'showQuickPick'
  | 'createTextEditorDecorationType'
  | 'visibleTextEditors'
  | 'onDidChangeVisibleTextEditors'
  | 'onDidChangeTextEditorVisibleRanges'
  | 'activeColorTheme'
  | 'onDidChangeActiveColorTheme';

export const ColorThemeKind = { Light: 1, Dark: 2, HighContrast: 3, HighContrastLight: 4 } as const;

/** A range of the document; the tests compare these fields. */
export class Range {
  constructor(
    readonly startLine: number,
    readonly startCharacter: number,
    readonly endLine: number,
    readonly endCharacter: number,
  ) {}
}

/** A decoration type that remembers its color and whether it was disposed. */
export class FakeDecorationType {
  disposed = false;

  constructor(readonly color: unknown) {}

  dispose(): void {
    this.disposed = true;
  }
}

/** Every decoration type created, oldest first. */
export const decorationTypes: FakeDecorationType[] = [];

/** A visible editor: a document, and the ranges set for each decoration type. */
export class FakeEditor {
  readonly decorations = new Map<FakeDecorationType, Range[]>();
  /** The lines in view; the whole document unless a test scrolls. */
  visibleRanges: { start: { line: number }; end: { line: number } }[] = [
    { start: { line: 0 }, end: { line: Number.MAX_SAFE_INTEGER - 1000 } },
  ];

  constructor(readonly document: { languageId: string; getText(): string; uri?: vscode.Uri }) {}

  setDecorations(type: FakeDecorationType, ranges: Range[]): void {
    this.decorations.set(type, ranges);
  }
}

/** The editors `window.visibleTextEditors` returns. */
export const visibleEditors: FakeEditor[] = [];

/** The theme kind `window.activeColorTheme` returns. */
export const theme = { kind: ColorThemeKind.Dark as number };

const visibleListeners = new Set<() => void>();
const themeListeners = new Set<() => void>();

/** The part of a TextDocumentChangeEvent the tests fire, typed from the real API. */
interface DocumentChange {
  document: Pick<vscode.TextDocument, 'languageId' | 'getText'> &
    Partial<Pick<vscode.TextDocument, 'uri'>>;
}

/**
 * A real vscode.Event narrowed to what the tests fire: the listener takes
 * `Fired`, which the real payload must be assignable to, else this is `never`
 * and the `satisfies` clause fails.
 */
type FiredEvent<E, Fired> =
  E extends vscode.Event<infer Payload>
    ? Payload extends Fired
      ? (listener: (payload: Fired) => void) => vscode.Disposable
      : never
    : never;

const documentListeners = new Set<(event: DocumentChange) => void>();
/** The part of a TextDocument the tests open, typed from the real API. */
type OpenedDocument = DocumentChange['document'];

const openListeners = new Set<(document: OpenedDocument) => void>();

/** Tells the listeners of onDidChangeVisibleTextEditors that the editors changed. */
export function changeVisibleEditors(): void {
  for (const listener of visibleListeners) {
    listener();
  }
}

const visibleRangesListeners = new Set<(event: { textEditor: FakeEditor }) => void>();

/** Scrolls `editor` to `fromLine`..`toLine` and tells the listeners of onDidChangeTextEditorVisibleRanges. */
export function scrollEditor(editor: FakeEditor, fromLine: number, toLine: number): void {
  editor.visibleRanges = [{ start: { line: fromLine }, end: { line: toLine } }];
  for (const listener of visibleRangesListeners) {
    listener({ textEditor: editor });
  }
}

/** Switches the theme kind and tells the listeners of onDidChangeActiveColorTheme. */
export function changeTheme(kind: number): void {
  theme.kind = kind;
  for (const listener of themeListeners) {
    listener();
  }
}

/** Tells the listeners of onDidChangeTextDocument that a document changed. */
export function changeDocument(document: DocumentChange['document']): void {
  for (const listener of documentListeners) {
    listener({ document });
  }
}

/** Tells the listeners of onDidOpenTextDocument that a document was opened, as after a language mode change. */
export function openDocument(document: OpenedDocument): void {
  for (const listener of openListeners) {
    listener(document);
  }
}

export const window = {
  createTextEditorDecorationType(options: { color?: unknown }): FakeDecorationType {
    const type = new FakeDecorationType(options.color);
    decorationTypes.push(type);
    return type;
  },
  get visibleTextEditors(): FakeEditor[] {
    return visibleEditors;
  },
  get activeColorTheme(): { kind: number } {
    return theme;
  },
  onDidChangeVisibleTextEditors(listener: () => void): Disposable {
    visibleListeners.add(listener);
    return new Disposable(() => visibleListeners.delete(listener));
  },
  onDidChangeTextEditorVisibleRanges(
    listener: (event: { textEditor: FakeEditor }) => void,
  ): Disposable {
    visibleRangesListeners.add(listener);
    return new Disposable(() => visibleRangesListeners.delete(listener));
  },
  onDidChangeActiveColorTheme(listener: () => void): Disposable {
    themeListeners.add(listener);
    return new Disposable(() => themeListeners.delete(listener));
  },
  createStatusBarItem(): FakeStatusBarItem {
    const item = new FakeStatusBarItem();
    statusBarItems.push(item);
    return item;
  },
  get activeTextEditor(): { document: { languageId: string; uri: vscode.Uri } } | undefined {
    return activeEditor.languageId === undefined
      ? undefined
      : { document: { languageId: activeEditor.languageId, uri: Uri.parse(activeEditor.uri) } };
  },
  onDidChangeActiveTextEditor(listener: () => void): Disposable {
    editorListeners.add(listener);
    return new Disposable(() => editorListeners.delete(listener));
  },
  showQuickPick<T extends { label: string }>(
    items: T[],
    options?: vscode.QuickPickOptions,
  ): Promise<T | undefined> {
    quickPicks.shown.push(items);
    quickPicks.options.push(options);
    return Promise.resolve(items.find((item) => item.label === quickPicks.next));
  },
  showTextDocument(uri: vscode.Uri, options?: vscode.TextDocumentShowOptions): Promise<object> {
    shownDocuments.push({ uri: uri.toString(), selection: options?.selection });
    return Promise.resolve({});
  },
  createQuickPick<T extends vscode.QuickPickItem>(): FakeQuickPick<T> {
    return new FakeQuickPick<T>();
  },
  createInputBox(): FakeInputBox {
    const box = new FakeInputBox();
    inputBoxes.push(box);
    return box;
  },
  showErrorMessage<T extends string>(message: string, ...buttons: T[]): Promise<T | undefined> {
    shownErrors.push({ message, buttons });
    if (errorChoice.never) {
      return new Promise<T | undefined>(() => undefined);
    }
    const wanted = notificationChoices.length > 0 ? notificationChoices.shift() : errorChoice.next;
    return Promise.resolve(buttons.find((button) => button === wanted));
  },
  showWarningMessage<T extends string>(message: string, ...buttons: T[]): Promise<T | undefined> {
    shownWarnings.push({ message, buttons });
    const wanted =
      notificationChoices.length > 0 ? notificationChoices.shift() : warningChoice.next;
    return Promise.resolve(buttons.find((button) => button === wanted));
  },
  showInformationMessage<T extends string>(
    message: string,
    ...buttons: T[]
  ): Promise<T | undefined> {
    shownInfos.push(message);
    return Promise.resolve(buttons.find((button) => button === infoChoice.next));
  },
  createOutputChannel(name: string): LogChannel {
    // Both overloads of the real function return a channel; tests only ask for the log kind.
    const channel = new LogChannel(name);
    channels.set(name, channel);
    return channel;
  },
} satisfies Partial<
  Omit<typeof vscode.window, LooseWindowMember | 'createQuickPick' | 'createInputBox'>
> &
  Partial<Record<LooseWindowMember, unknown>> & {
    createQuickPick?: <T extends vscode.QuickPickItem>() => UsedQuickPick<T>;
    createInputBox?: () => UsedInputBox;
  };

/** The settings `workspace.getConfiguration` returns, by full name. */
export const settings = new Map<string, unknown>();

/** The settings set at workspace scope, by full name; `settings` holds the user scope. */
export const workspaceSettings = new Map<string, unknown>();

/** The workspace folders `workspace.workspaceFolders` returns, by name. */
export const workspaceFolderNames: string[] = [];

/** The settings set at workspace-folder scope, by folder name and then full name. */
export const workspaceFolderSettings = new Map<string, Map<string, unknown>>();

/** Adds a workspace folder with these settings (by full name), as in a multi-root workspace. */
export function addWorkspaceFolder(
  name: string,
  folderSettings: Record<string, unknown> = {},
): void {
  workspaceFolderNames.push(name);
  workspaceFolderSettings.set(name, new Map(Object.entries(folderSettings)));
}

const FOLDER_URI_PREFIX = 'file:///workspace/';

/** A configuration section that reads from `settings`. */
class Configuration implements vscode.WorkspaceConfiguration {
  // The real type has this index signature, and it is `any`.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- required by vscode.WorkspaceConfiguration
  [key: string]: any;

  /** Like VS Code, folder values are visible only to a configuration scoped to that folder's resource. */
  private readonly folder: string | undefined;

  constructor(
    private readonly section: string,
    resource?: vscode.ConfigurationScope | null,
  ) {
    this.folder = workspaceFolderNames.find(
      (name) =>
        resource instanceof FakeUri &&
        (resource.toString() === `${FOLDER_URI_PREFIX}${name}` ||
          resource.toString().startsWith(`${FOLDER_URI_PREFIX}${name}/`)),
    );
    this.scoped = resource !== undefined && resource !== null;
  }

  private readonly scoped: boolean;

  // eslint-disable-next-line @typescript-eslint/no-unnecessary-type-parameters -- mirrors the real overload
  get<T>(key: string): T | undefined;
  get<T>(key: string, defaultValue: T): T;
  get<T>(key: string, defaultValue?: T): T | undefined {
    const value = settings.get(`${this.section}.${key}`);
    return value === undefined ? defaultValue : (value as T);
  }

  has(key: string): boolean {
    return settings.has(`${this.section}.${key}`);
  }

  inspect<T>(key: string): {
    key: string;
    globalValue?: T;
    workspaceValue?: T;
    workspaceFolderValue?: T;
  } {
    const name = `${this.section}.${key}`;
    return {
      key: name,
      ...(settings.has(name) && { globalValue: settings.get(name) as T }),
      ...(workspaceSettings.has(name) && { workspaceValue: workspaceSettings.get(name) as T }),
      ...(this.folder !== undefined &&
        workspaceFolderSettings.get(this.folder)?.has(name) === true && {
          workspaceFolderValue: workspaceFolderSettings.get(this.folder)?.get(name) as T,
        }),
    };
  }

  update(key: string, value: unknown, target: number): Promise<void> {
    if (updateFailure.error !== undefined) {
      return Promise.reject(updateFailure.error);
    }
    if (target === ConfigurationTarget.WorkspaceFolder && !this.scoped) {
      return Promise.reject(
        new Error('Unable to write to Workspace Folder Settings without a resource'),
      );
    }
    settingUpdates.push({
      name: `${this.section}.${key}`,
      value,
      target,
      ...(this.folder !== undefined && { folder: this.folder }),
    });
    return Promise.resolve();
  }
}

/** The error the next calls to `update` reject with, if any. */
export const updateFailure: { error: Error | undefined } = { error: undefined };

interface ConfigurationChange {
  affectsConfiguration(section: string): boolean;
}

const configListeners = new Set<(event: ConfigurationChange) => void>();

/** Tells the listeners of onDidChangeConfiguration that these settings changed. */
export function changeConfiguration(...names: string[]): void {
  for (const listener of configListeners) {
    listener({ affectsConfiguration: (section) => names.includes(section) });
  }
}

/** The calls to `update` on a configuration section, oldest first. */
export const settingUpdates: { name: string; value: unknown; target: number; folder?: string }[] =
  [];

export const workspace = {
  onDidChangeTextDocument(listener: (event: DocumentChange) => void): Disposable {
    documentListeners.add(listener);
    return new Disposable(() => documentListeners.delete(listener));
  },
  onDidOpenTextDocument(listener: (document: OpenedDocument) => void): Disposable {
    openListeners.add(listener);
    return new Disposable(() => openListeners.delete(listener));
  },
  getConfiguration(section?: string, scope?: vscode.ConfigurationScope | null): Configuration {
    return new Configuration(section ?? '', scope);
  },
  get workspaceFolders(): vscode.WorkspaceFolder[] | undefined {
    return workspaceFolderNames.length === 0
      ? undefined
      : workspaceFolderNames.map((name, index) => ({
          name,
          index,
          uri: Uri.parse(`${FOLDER_URI_PREFIX}${name}`),
        }));
  },
  getWorkspaceFolder(uri: vscode.Uri): vscode.WorkspaceFolder | undefined {
    return this.workspaceFolders?.find(
      (folder) =>
        uri.toString() === folder.uri.toString() ||
        uri.toString().startsWith(`${folder.uri.toString()}/`),
    );
  },
  onDidChangeConfiguration(listener: (event: ConfigurationChange) => void): Disposable {
    configListeners.add(listener);
    return new Disposable(() => configListeners.delete(listener));
  },
} satisfies Partial<
  Omit<typeof vscode.workspace, 'onDidChangeTextDocument' | 'onDidOpenTextDocument'>
> & {
  onDidChangeTextDocument?: FiredEvent<
    typeof vscode.workspace.onDidChangeTextDocument,
    DocumentChange
  >;
  onDidOpenTextDocument?: FiredEvent<typeof vscode.workspace.onDidOpenTextDocument, OpenedDocument>;
};

/** A diagnostic as the code under test reads it: the members of vscode.Diagnostic with plain positions. */
export interface FakeDiagnostic {
  range: { start: { line: number; character: number }; end: { line: number; character: number } };
  severity: number;
  source?: string;
  message: string;
}

/** The diagnostics `languages.getDiagnostics` returns, by document URI. */
export const diagnostics = new Map<string, FakeDiagnostic[]>();

/** The overloaded getDiagnostics returns real Diagnostics; the stand-in's carry only what is read. */
type LooseLanguagesMember = 'getDiagnostics';

export const languages = {
  getDiagnostics(resource: vscode.Uri): FakeDiagnostic[] {
    return diagnostics.get(resource.toString()) ?? [];
  },
} satisfies Partial<Omit<typeof vscode.languages, LooseLanguagesMember>> &
  Partial<Record<LooseLanguagesMember, unknown>>;

/** The URLs opened with env.openExternal, oldest first. */
export const openedUrls: string[] = [];

/** A URI that is only its text, which is all the tests look at. */
class FakeUri implements vscode.Uri {
  readonly scheme = '';
  readonly authority = '';
  readonly path = '';
  readonly query = '';
  readonly fragment = '';
  readonly fsPath = '';

  constructor(private readonly text: string) {}

  with(): vscode.Uri {
    return this;
  }

  toString(): string {
    return this.text;
  }

  toJSON(): string {
    return this.text;
  }
}

export const Uri = {
  parse(value: string): vscode.Uri {
    return new FakeUri(value);
  },
} satisfies Partial<typeof vscode.Uri>;

export const env = {
  remoteName: undefined as string | undefined,
  openExternal(target: vscode.Uri): Promise<boolean> {
    openedUrls.push(target.toString());
    return Promise.resolve(true);
  },
} satisfies Partial<typeof vscode.env>;

/** Forgets every registration and message, for the next test. */
export function reset(): void {
  registry.clear();
  shownErrors.length = 0;
  shownInfos.length = 0;
  shownWarnings.length = 0;
  warningChoice.next = undefined;
  notificationChoices.length = 0;
  infoChoice.next = undefined;
  workspaceSettings.clear();
  workspaceFolderSettings.clear();
  workspaceFolderNames.length = 0;
  executedCommands.length = 0;
  commandFailure.error = undefined;
  commandResults.clear();
  inputBoxes.length = 0;
  shownDocuments.length = 0;
  diagnostics.clear();
  openedUrls.length = 0;
  env.remoteName = undefined;
  channels.clear();
  settings.clear();
  statusBarItems.length = 0;
  settingUpdates.length = 0;
  updateFailure.error = undefined;
  errorChoice.next = undefined;
  quickPicks.shown.length = 0;
  quickPicks.active.length = 0;
  quickPicks.options.length = 0;
  quickPicks.created.length = 0;
  quickPicks.next = undefined;
  quickPicks.acceptAtOnce = false;
  quickPicks.typed = '';
  editorListeners.clear();
  configListeners.clear();
  decorationTypes.length = 0;
  visibleEditors.length = 0;
  visibleListeners.clear();
  visibleRangesListeners.clear();
  themeListeners.clear();
  documentListeners.clear();
  openListeners.clear();
  theme.kind = ColorThemeKind.Dark;
  activeEditor.languageId = undefined;
  activeEditor.uri = 'file:///workspace/active.yaml';
  errorChoice.next = undefined;
  errorChoice.never = false;
}

/** A memento that keeps its values in memory, for the extension's global state. */
export function memento(): Pick<vscode.Memento, 'get' | 'update'> {
  const values = new Map<string, unknown>();
  return {
    // eslint-disable-next-line @typescript-eslint/no-unnecessary-type-parameters -- mirrors the signature of vscode.Memento.get
    get<T>(key: string): T | undefined {
      return values.get(key) as T | undefined;
    },
    update(key: string, value: unknown): Promise<void> {
      values.set(key, value);
      return Promise.resolve();
    },
  };
}
