import * as vscode from 'vscode';

import {
  EXAMPLE_CURSOR_LINE,
  USE_EXAMPLE_TITLE,
  exampleComment,
  moveNotes,
  type NotePlace,
} from './example_schema';
import type { PasteUse } from './type_flow';

/** The command that uses the example pasted in an editor `Paste an example…` opened. */
export const USE_EXAMPLE_COMMAND = 'cel-lens.useExample';

/**
 * The scheme of the documents of pasted examples, which the extension keeps in memory: the `when`
 * clauses of the command in the editor title and the Command Palette name it.
 */
export const EXAMPLE_SCHEME = 'cel-lens-example';

/**
 * The files of the pasted examples, in memory. Every change of one is saved here at once, so its
 * editor is never dirty and closes without asking to save. A window reload loses them, and the
 * editors of the window before are closed (`ExampleEditors`).
 */
export class ExampleFiles implements vscode.FileSystemProvider {
  private readonly files = new Map<string, { data: Uint8Array; mtime: number }>();

  /** The files change only through VS Code, which needs no event for that. */
  readonly onDidChangeFile: vscode.Event<vscode.FileChangeEvent[]> = () =>
    new vscode.Disposable(() => undefined);

  watch(): vscode.Disposable {
    return new vscode.Disposable(() => undefined);
  }

  stat(uri: vscode.Uri): vscode.FileStat {
    const file = this.file(uri);
    return { type: vscode.FileType.File, ctime: 0, mtime: file.mtime, size: file.data.byteLength };
  }

  readDirectory(): [string, vscode.FileType][] {
    return [];
  }

  createDirectory(): void {
    throw vscode.FileSystemError.NoPermissions('pasted examples have no folders');
  }

  readFile(uri: vscode.Uri): Uint8Array {
    return this.file(uri).data;
  }

  writeFile(uri: vscode.Uri, content: Uint8Array): void {
    this.files.set(uri.toString(), { data: content, mtime: Date.now() });
  }

  delete(uri: vscode.Uri): void {
    this.files.delete(uri.toString());
  }

  rename(): void {
    throw vscode.FileSystemError.NoPermissions('pasted examples cannot be renamed');
  }

  private file(uri: vscode.Uri): { data: Uint8Array; mtime: number } {
    const file = this.files.get(uri.toString());
    if (file === undefined) {
      throw vscode.FileSystemError.FileNotFound(uri);
    }
    return file;
  }
}

/**
 * An editor of a pasted example: its document, what using it runs, whether that runs now, and
 * what fires when it closes.
 */
interface Example {
  /** A language change gives the same URI a new document. */
  document: vscode.TextDocument;
  readonly use: PasteUse;
  readonly closed: AbortController;
  running: boolean;
}

/** Whether a command's argument is the URI the editor title gives it. */
function isUri(value: unknown): value is vscode.Uri {
  return value instanceof Object && 'scheme' in value && 'fsPath' in value;
}

/** The tabs of the window that show a text document whose URI `match` takes. */
function tabsOf(match: (uri: vscode.Uri) => boolean): vscode.Tab[] {
  return vscode.window.tabGroups.all
    .flatMap((group) => group.tabs)
    .filter((tab) => tab.input instanceof vscode.TabInputText && match(tab.input.uri));
}

/**
 * The command that saves the active editor with no save participant: no trim, no format. In VS
 * Code 1.100 it saves the active group's active editor, or the Open Editors view's selection when
 * that view has the focus, which it has not while a person types in the editor.
 */
const SAVE_WITHOUT_FORMATTING = 'workbench.action.files.saveWithoutFormatting';

/** The command that reverts the active editor and closes it, so it asks nothing. */
const REVERT_AND_CLOSE = 'workbench.action.revertAndCloseActiveEditor';

/**
 * The editors `Paste an example…` opens beside the rule, of in-memory YAML files named after the
 * value, such as `Example of params.yaml`: each starts with a comment that says what to paste,
 * and has `Use this example as the type` as a code lens above it and in its editor title. Using
 * it hands its text to the flow, which closes it once the type is written; closing it otherwise
 * writes nothing, even while it is being used. A change of the active editor is saved to memory in
 * the same turn, with Save without Formatting, so no save participant (trimming, formatting, code
 * actions on save) changes what the person types, and the editor is not dirty by the time they
 * close it, however soon; `TextDocument.save()` would run them on every keystroke. An example
 * changed while another editor is active is saved once it is active again. A language change, which closes the
 * document and opens it again, keeps it. The editors of a window before a reload are closed, as
 * their files are gone.
 */
export class ExampleEditors implements vscode.CodeLensProvider, vscode.Disposable {
  private readonly examples = new Map<string, Example>();
  private readonly files = new ExampleFiles();
  private readonly registrations: vscode.Disposable[];

  constructor(private readonly log: Pick<vscode.LogOutputChannel, 'warn'>) {
    this.registrations = [
      vscode.workspace.registerFileSystemProvider(EXAMPLE_SCHEME, this.files, {
        isCaseSensitive: true,
      }),
      vscode.commands.registerCommand(USE_EXAMPLE_COMMAND, async (resource: unknown) => {
        await this.use(resource);
      }),
      vscode.languages.registerCodeLensProvider({ scheme: EXAMPLE_SCHEME }, this),
      vscode.workspace.onDidChangeTextDocument(({ document }) => {
        this.saveNow(document);
      }),
      vscode.window.onDidChangeActiveTextEditor(() => {
        const uri = vscode.window.activeTextEditor?.document.uri.toString();
        const example = uri === undefined ? undefined : this.examples.get(uri);
        if (example !== undefined) {
          this.saveNow(example.document);
        }
      }),
      vscode.workspace.onDidCloseTextDocument((document) => {
        const uri = document.uri.toString();
        // A language change closes the document and opens it again at once: wait for that.
        void Promise.resolve().then(() => {
          if (!vscode.workspace.textDocuments.some((open) => open.uri.toString() === uri)) {
            this.drop(uri, document.uri);
          }
        });
      }),
      vscode.workspace.onDidOpenTextDocument((document) => {
        const example = this.examples.get(document.uri.toString());
        if (example !== undefined) {
          example.document = document;
        }
      }),
    ];
    const stale = tabsOf((uri) => uri.scheme === EXAMPLE_SCHEME);
    if (stale.length > 0) {
      vscode.window.tabGroups.close(stale).then(undefined, (error: unknown) => {
        this.log.warn(`Could not close the examples of the window before: ${String(error)}`);
      });
    }
  }

  /**
   * Opens the editor of an example of `value` beside the active one, the cursor below its comment;
   * `inside` for a value below the root of its object.
   */
  async open(value: string, inside: boolean, use: PasteUse): Promise<void> {
    const uri = this.newUri(value);
    this.files.writeFile(uri, new TextEncoder().encode(exampleComment(value, inside)));
    const document = await vscode.workspace.openTextDocument(uri);
    this.examples.set(uri.toString(), {
      document,
      use,
      closed: new AbortController(),
      running: false,
    });
    await vscode.window.showTextDocument(document, {
      viewColumn: vscode.ViewColumn.Beside,
      selection: new vscode.Range(EXAMPLE_CURSOR_LINE, 0, EXAMPLE_CURSOR_LINE, 0),
      preserveFocus: false,
      preview: false,
    });
  }

  provideCodeLenses(document: Pick<vscode.TextDocument, 'uri'>): vscode.CodeLens[] {
    const uri = document.uri.toString();
    return this.examples.has(uri)
      ? [
          new vscode.CodeLens(new vscode.Range(0, 0, 0, 0), {
            title: USE_EXAMPLE_TITLE,
            command: USE_EXAMPLE_COMMAND,
            arguments: [uri],
          }),
        ]
      : [];
  }

  dispose(): void {
    for (const registration of this.registrations) {
      registration.dispose();
    }
    this.examples.clear();
  }

  /**
   * Saves a changed example to memory now, with Save without Formatting, which saves the active
   * editor: the example's own, checked in this same turn; one that is not active waits until it
   * is again.
   */
  private saveNow(document: vscode.TextDocument): void {
    const uri = document.uri.toString();
    if (
      document.uri.scheme !== EXAMPLE_SCHEME ||
      !document.isDirty ||
      vscode.window.activeTextEditor?.document.uri.toString() !== uri
    ) {
      return;
    }
    vscode.commands.executeCommand(SAVE_WITHOUT_FORMATTING).then(undefined, (error: unknown) => {
      this.log.warn(`Could not keep ${uri}: ${String(error)}`);
    });
  }

  /** An example whose document closed for good: a use still running writes nothing, and its file goes. */
  private drop(uri: string, resource: vscode.Uri): void {
    this.examples.get(uri)?.closed.abort();
    this.examples.delete(uri);
    if (resource.scheme === EXAMPLE_SCHEME) {
      this.files.delete(resource);
    }
  }

  /** The URI of a new example of `value`, named for its tab: `Example of params.yaml`, then `(2)`. */
  private newUri(value: string): vscode.Uri {
    for (let count = 1; ; count += 1) {
      const name = `Example of ${value}${count === 1 ? '' : ` (${String(count)})`}.yaml`;
      const uri = vscode.Uri.parse(`${EXAMPLE_SCHEME}:/${encodeURIComponent(name)}`);
      if (!this.examples.has(uri.toString())) {
        return uri;
      }
    }
  }

  /**
   * `cel-lens.useExample`: the code lens gives the example's URI, the editor title its Uri, and the
   * Command Palette nothing, for the active editor's. A second use while one runs does nothing.
   */
  private async use(resource: unknown): Promise<void> {
    const uri =
      typeof resource === 'string'
        ? resource
        : isUri(resource)
          ? resource.toString()
          : vscode.window.activeTextEditor?.document.uri.toString();
    const example = uri === undefined ? undefined : this.examples.get(uri);
    if (example === undefined) {
      this.log.warn(`${USE_EXAMPLE_COMMAND} ran on ${uri ?? 'no editor'}, which holds no example.`);
      return;
    }
    if (example.running) {
      return;
    }
    example.running = true;
    try {
      await example.use(example.document.getText(), {
        closed: example.closed.signal,
        close: () => this.close(example),
      });
    } finally {
      example.running = false;
    }
  }

  /**
   * Closes the example's editor, so nothing asks: its tabs, or, with a change not yet saved, its
   * editor reverted and closed, as its text was used; an editor the person closed meanwhile is not
   * opened again. A failure is logged: the type is written, and the flow goes on.
   */
  private async close({ document, closed }: Example): Promise<void> {
    if (closed.signal.aborted) {
      return;
    }
    const uri = document.uri.toString();
    this.examples.delete(uri);
    try {
      if (document.isDirty) {
        const shown = vscode.window.visibleTextEditors.find(
          (editor) => editor.document === document,
        );
        await vscode.window.showTextDocument(document, {
          ...(shown?.viewColumn !== undefined && { viewColumn: shown.viewColumn }),
          preserveFocus: false,
        });
        await vscode.commands.executeCommand(REVERT_AND_CLOSE);
      } else {
        await vscode.window.tabGroups.close(tabsOf((each) => each.toString() === uri));
      }
    } catch (error) {
      this.log.warn(`Could not close ${uri}: ${String(error)}`);
    }
  }
}

/**
 * The notes of `cel-lens.inferType` on what an example left open, as information diagnostics at
 * the keys of `.cellens` they are about: quiet, in place, and in the Problems view. An edit of a
 * note's key or block addresses it, and the note goes; the others move with the edits, and all
 * go when the document closes.
 */
export class ExampleNotes implements vscode.Disposable {
  private readonly collection = vscode.languages.createDiagnosticCollection('cel-lens examples');
  private readonly notes = new Map<string, readonly NotePlace[]>();
  private readonly listeners = [
    vscode.workspace.onDidChangeTextDocument((event) => {
      const uri = event.document.uri.toString();
      const notes = this.notes.get(uri);
      if (notes !== undefined && event.contentChanges.length > 0) {
        this.set(
          event.document.uri,
          moveNotes(
            notes,
            event.contentChanges.map((change) => ({
              startLine: change.range.start.line,
              endLine: change.range.end.line,
              text: change.text,
            })),
          ),
        );
      }
    }),
    vscode.workspace.onDidCloseTextDocument((document) => {
      this.set(document.uri, []);
    }),
  ];

  /** Adds the notes of an example to those of `cellens`. */
  show(cellens: string, notes: readonly NotePlace[]): void {
    const uri = vscode.Uri.parse(cellens);
    this.set(uri, [...(this.notes.get(uri.toString()) ?? []), ...notes]);
  }

  dispose(): void {
    for (const listener of this.listeners) {
      listener.dispose();
    }
    this.collection.dispose();
  }

  private set(uri: vscode.Uri, notes: readonly NotePlace[]): void {
    if (notes.length === 0) {
      this.notes.delete(uri.toString());
      this.collection.delete(uri);
      return;
    }
    this.notes.set(uri.toString(), notes);
    this.collection.set(
      uri,
      notes.map((note) => {
        const diagnostic = new vscode.Diagnostic(
          new vscode.Range(note.line, note.start, note.line, note.end),
          note.message,
          vscode.DiagnosticSeverity.Information,
        );
        diagnostic.source = 'cel-lens example';
        return diagnostic;
      }),
    );
  }
}
