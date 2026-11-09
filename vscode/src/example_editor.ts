import * as vscode from 'vscode';

import { EXAMPLE_CURSOR_LINE, exampleComment, moveNotes, type NotePlace } from './example_schema';
import type { PasteUse } from './type_flow';

/** The command that uses the example pasted in an editor `Paste an example…` opened. */
export const USE_EXAMPLE_COMMAND = 'cel-lens.useExample';

/** What the code lens above a pasted example says. */
export const USE_EXAMPLE_TITLE = 'Use this example as the type';

/**
 * The context key that lists the URIs of the editors of pasted examples, for the `when` clauses
 * of the command in the editor title and the Command Palette: `resource in cel-lens.exampleDocuments`.
 */
export const EXAMPLE_DOCUMENTS_CONTEXT = 'cel-lens.exampleDocuments';

/** The command that reverts the active editor and closes it, so it asks nothing. */
const REVERT_AND_CLOSE = 'workbench.action.revertAndCloseActiveEditor';

/**
 * An editor of a pasted example: its document, what using it runs, whether that runs now, and
 * what fires when it closes.
 */
interface Example {
  readonly document: vscode.TextDocument;
  readonly use: PasteUse;
  readonly closed: AbortController;
  running: boolean;
}

/** Whether a command's argument is the URI the editor title gives it. */
function isUri(value: unknown): value is vscode.Uri {
  return value instanceof Object && 'scheme' in value && 'fsPath' in value;
}

/**
 * The untitled YAML editors `Paste an example…` opens beside the rule: each starts with a comment
 * that says what to paste, and has `Use this example as the type` as a code lens above it and in
 * its editor title. Using it hands its text to the flow, which closes it without asking to save
 * once the type is written; closing it otherwise writes nothing, even while it is being used.
 * The context key lives in the window, so a new instance, as after a restart of the extension
 * host, and `dispose()` empty it.
 */
export class ExampleEditors implements vscode.CodeLensProvider, vscode.Disposable {
  private readonly examples = new Map<string, Example>();
  private readonly registrations: vscode.Disposable[];

  constructor(private readonly log: Pick<vscode.LogOutputChannel, 'warn'>) {
    this.registrations = [
      vscode.commands.registerCommand(USE_EXAMPLE_COMMAND, async (resource: unknown) => {
        await this.use(resource);
      }),
      vscode.languages.registerCodeLensProvider({ scheme: 'untitled', language: 'yaml' }, this),
      vscode.workspace.onDidCloseTextDocument((document) => {
        const uri = document.uri.toString();
        this.examples.get(uri)?.closed.abort();
        this.forget(uri);
      }),
    ];
    void this.updateContext(); // Never rejects: setContext only stores the value.
  }

  /**
   * Opens the editor of an example of `value` beside the active one, the cursor below its comment;
   * `inside` for a value below the root of its object.
   */
  async open(value: string, inside: boolean, use: PasteUse): Promise<void> {
    const document = await vscode.workspace.openTextDocument({
      language: 'yaml',
      content: exampleComment(value, inside),
    });
    this.examples.set(document.uri.toString(), {
      document,
      use,
      closed: new AbortController(),
      running: false,
    });
    await this.updateContext();
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
    void this.updateContext(); // Never rejects: setContext only stores the value.
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
   * Reverts the example's editor and closes it, in the group that shows it, so it asks nothing;
   * an editor the person closed meanwhile is not opened again.
   */
  private async close({ document, closed }: Example): Promise<void> {
    if (closed.signal.aborted) {
      return;
    }
    this.forget(document.uri.toString());
    const shown = vscode.window.visibleTextEditors.find((editor) => editor.document === document);
    await vscode.window.showTextDocument(document, {
      ...(shown?.viewColumn !== undefined && { viewColumn: shown.viewColumn }),
      preserveFocus: false,
    });
    await vscode.commands.executeCommand(REVERT_AND_CLOSE);
  }

  private forget(uri: string): void {
    if (this.examples.delete(uri)) {
      void this.updateContext(); // Never rejects: setContext only stores the value.
    }
  }

  private async updateContext(): Promise<void> {
    await vscode.commands.executeCommand('setContext', EXAMPLE_DOCUMENTS_CONTEXT, [
      ...this.examples.keys(),
    ]);
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
