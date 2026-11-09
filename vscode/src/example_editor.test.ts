import { afterEach, describe, expect, it } from 'vitest';

import {
  CodeLens,
  Diagnostic,
  DiagnosticSeverity,
  FakeEditor,
  Range,
  Uri,
  ViewColumn,
  activeEditor,
  changeDocument,
  closeDocument,
  codeLensProviders,
  commands,
  contextKeys,
  diagnosticCollections,
  executedCommands,
  reset,
  runCommand,
  settle,
  shownDocuments,
  untitledDocuments,
  visibleEditors,
  window,
  type FakeTextDocument,
} from '../test/vscode';
import {
  EXAMPLE_DOCUMENTS_CONTEXT,
  ExampleEditors,
  ExampleNotes,
  USE_EXAMPLE_COMMAND,
} from './example_editor';
import { exampleComment, type NotePlace } from './example_schema';
import type { PastedEditor } from './type_flow';

const CELLENS = 'file:///workspace/repo/.cellens';

/** What using an example got: its text, and its editor. */
interface Used {
  readonly text: string;
  readonly editor: PastedEditor;
}

/** Opens the editor of an example of `params.spec.config`; `used` lists each use of it. */
async function openExample(): Promise<{
  editors: ExampleEditors;
  document: FakeTextDocument;
  used: Used[];
  log: ReturnType<typeof window.createOutputChannel>;
}> {
  const log = window.createOutputChannel('cel-lens');
  const editors = new ExampleEditors(log);
  const used: Used[] = [];
  await editors.open('params.spec.config', true, (text, editor) => {
    used.push({ text, editor });
    return Promise.resolve();
  });
  const document = untitledDocuments[0];
  if (document === undefined) {
    throw new Error('no untitled document was opened');
  }
  return { editors, document, used, log };
}

/** The value the extension gave the context key of the editors of examples. */
function exampleDocuments(): unknown {
  return contextKeys.get(EXAMPLE_DOCUMENTS_CONTEXT);
}

describe('ExampleEditors', () => {
  afterEach(reset);

  it('opens an untitled YAML editor beside the rule, starting with what to paste, the cursor below it', async () => {
    const { document } = await openExample();

    expect(document).toMatchObject({
      uri: Uri.parse('untitled:Untitled-1'),
      languageId: 'yaml',
      text: exampleComment('params.spec.config', true),
    });
    expect(shownDocuments).toEqual([
      {
        uri: 'untitled:Untitled-1',
        selection: new Range(3, 0, 3, 0),
        viewColumn: ViewColumn.Beside,
        preserveFocus: false,
        preview: false,
      },
    ]);
    expect(exampleDocuments()).toEqual(['untitled:Untitled-1']);
  });

  it('gives its editor, and no other untitled YAML, the code lens Use this example as the type', async () => {
    const { editors, document } = await openExample();

    expect(codeLensProviders).toEqual([
      { selector: { scheme: 'untitled', language: 'yaml' }, provider: editors },
    ]);
    expect(editors.provideCodeLenses(document)).toEqual([
      new CodeLens(new Range(0, 0, 0, 0), {
        title: 'Use this example as the type',
        command: USE_EXAMPLE_COMMAND,
        arguments: ['untitled:Untitled-1'],
      }),
    ]);
    expect(editors.provideCodeLenses({ uri: Uri.parse('untitled:Untitled-9') })).toEqual([]);
  });

  it.each([
    ['the code lens, with its URI', () => ['untitled:Untitled-1']],
    ['the editor title, with its Uri', () => [Uri.parse('untitled:Untitled-1')]],
    [
      'the Command Palette, on the active editor',
      () => {
        activeEditor.languageId = 'yaml';
        activeEditor.uri = 'untitled:Untitled-1';
        return [];
      },
    ],
  ])('uses the pasted text when run from %s', async (_name, args) => {
    const { document, used } = await openExample();
    document.text += 'teams: []\n';

    await runCommand(USE_EXAMPLE_COMMAND, ...args());

    expect(used.map((use) => use.text)).toEqual([
      `${exampleComment('params.spec.config', true)}teams: []\n`,
    ]);
  });

  it('closes the editor without asking to save: shows it in its group, reverts and closes it, and drops it from the when clauses', async () => {
    const { document, used } = await openExample();
    const editor = new FakeEditor(document);
    editor.viewColumn = ViewColumn.Two;
    visibleEditors.push(editor);
    await runCommand(USE_EXAMPLE_COMMAND, 'untitled:Untitled-1');

    await used[0]?.editor.close();

    expect(shownDocuments.at(-1)).toEqual({
      uri: 'untitled:Untitled-1',
      selection: undefined,
      viewColumn: ViewColumn.Two,
      preserveFocus: false,
    });
    expect(executedCommands.at(-1)).toEqual({
      command: 'workbench.action.revertAndCloseActiveEditor',
      args: [],
    });
    expect(exampleDocuments()).toEqual([]);
  });

  it('writes nothing once its editor is closed, and logs the stray use', async () => {
    const { document, used, log } = await openExample();

    closeDocument(document);
    await settle();
    await runCommand(USE_EXAMPLE_COMMAND, 'untitled:Untitled-1');

    expect(used).toEqual([]);
    expect(exampleDocuments()).toEqual([]);
    expect(log.lines).toEqual([
      'cel-lens.useExample ran on untitled:Untitled-1, which holds no example.',
    ]);
  });

  it('uses an example once at a time, while the engine works on it', async () => {
    const log = window.createOutputChannel('cel-lens');
    const editors = new ExampleEditors(log);
    let uses = 0;
    let finish: () => void = () => undefined;
    await editors.open('params', false, () => {
      uses += 1;
      return new Promise((resolve) => {
        finish = resolve;
      });
    });

    const first = runCommand(USE_EXAMPLE_COMMAND, 'untitled:Untitled-1');
    await runCommand(USE_EXAMPLE_COMMAND, 'untitled:Untitled-1');
    expect(uses).toBe(1);
    finish();
    await first;
    const again = runCommand(USE_EXAMPLE_COMMAND, 'untitled:Untitled-1');
    await settle();

    expect(uses).toBe(2);
    finish();
    await again;
  });

  it("tells a use that its editor closed, and does not open it again to close it (#9's review)", async () => {
    const { document, used } = await openExample();
    await runCommand(USE_EXAMPLE_COMMAND, 'untitled:Untitled-1');
    const shown = shownDocuments.length;

    closeDocument(document);
    await used[0]?.editor.close();

    expect(used[0]?.editor.closed.aborted).toBe(true);
    expect(shownDocuments).toHaveLength(shown);
    expect(executedCommands.map((each) => each.command)).not.toContain(
      'workbench.action.revertAndCloseActiveEditor',
    );
  });

  it("empties the context key of the examples when an instance starts, as after a restart of the extension host, and on dispose (#9's review)", async () => {
    const { editors } = await openExample();
    expect(exampleDocuments()).toEqual(['untitled:Untitled-1']);

    editors.dispose();
    expect(exampleDocuments()).toEqual([]);

    contextKeys.set(EXAMPLE_DOCUMENTS_CONTEXT, ['untitled:Untitled-1']);
    const next = new ExampleEditors(window.createOutputChannel('cel-lens'));
    await settle();
    expect(exampleDocuments()).toEqual([]);
    next.dispose();
  });

  it('frees its command and its code lens provider on dispose', async () => {
    const { editors } = await openExample();

    editors.dispose();

    expect(await commands.getCommands()).toEqual([]);
    expect(codeLensProviders).toEqual([]);
  });
});

describe('ExampleNotes', () => {
  afterEach(reset);

  const teams: NotePlace = {
    line: 7,
    start: 8,
    end: 13,
    blockEnd: 10,
    message: 'teams: an empty list says nothing about its items',
  };
  const cellens = { uri: Uri.parse(CELLENS), languageId: 'plaintext', getText: () => '' };

  function shown(): readonly Diagnostic[] | undefined {
    return diagnosticCollections[0]?.entries.get(CELLENS);
  }

  it('shows each note as an information diagnostic at its key, from cel-lens example', () => {
    new ExampleNotes().show(CELLENS, [teams]);

    const expected = new Diagnostic(
      new Range(7, 8, 7, 13),
      'teams: an empty list says nothing about its items',
      DiagnosticSeverity.Information,
    );
    expected.source = 'cel-lens example';
    expect(diagnosticCollections[0]?.name).toBe('cel-lens examples');
    expect(shown()).toEqual([expected]);
  });

  it('adds the notes of a second example to those of the same .cellens', () => {
    const notes = new ExampleNotes();

    notes.show(CELLENS, [teams]);
    notes.show(CELLENS, [{ ...teams, line: 20, blockEnd: 22, message: 'other' }]);

    expect(shown()?.map((note) => note.message)).toEqual([teams.message, 'other']);
  });

  it('moves the notes with lines added above them, and drops one once its block is edited', () => {
    new ExampleNotes().show(CELLENS, [teams]);

    changeDocument(cellens, [{ range: { start: { line: 1 }, end: { line: 1 } }, text: 'a\n' }]);
    expect(shown()?.map((note) => note.range)).toEqual([new Range(8, 8, 8, 13)]);

    changeDocument(cellens, [{ range: { start: { line: 10 }, end: { line: 10 } }, text: 'x' }]);
    expect(shown()).toBeUndefined();
  });

  it('leaves the notes of .cellens as they are for a change of another document', () => {
    new ExampleNotes().show(CELLENS, [teams]);

    changeDocument({ ...cellens, uri: Uri.parse('file:///workspace/repo/a.yaml') }, [
      { range: { start: { line: 7 }, end: { line: 7 } }, text: 'x' },
    ]);

    expect(shown()).toHaveLength(1);
  });

  it('drops the notes of a .cellens once it is closed, and frees its collection on dispose', () => {
    const notes = new ExampleNotes();
    notes.show(CELLENS, [teams]);

    closeDocument(cellens);
    notes.dispose();

    expect(shown()).toBeUndefined();
    expect(diagnosticCollections[0]?.disposed).toBe(true);
  });
});
