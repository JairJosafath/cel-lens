import { afterEach, describe, expect, it } from 'vitest';

import {
  CodeLens,
  Diagnostic,
  DiagnosticSeverity,
  FakeEditor,
  Range,
  TabInputText,
  Uri,
  ViewColumn,
  activeEditor,
  changeActiveEditor,
  changeDocument,
  changeLanguage,
  closeDocument,
  closedTabs,
  codeLensProviders,
  commands,
  diagnosticCollections,
  executedCommands,
  fileSystemProviders,
  openDocuments,
  openTabs,
  reset,
  runCommand,
  savedDocuments,
  settle,
  showFailure,
  shownDocuments,
  visibleEditors,
  window,
  type FakeTextDocument,
} from '../test/vscode';
import {
  EXAMPLE_SCHEME,
  ExampleEditors,
  ExampleFiles,
  ExampleNotes,
  USE_EXAMPLE_COMMAND,
} from './example_editor';
import { exampleComment, type NotePlace } from './example_schema';
import type { PastedEditor } from './type_flow';

const CELLENS = 'file:///workspace/repo/.cellens';

/** The in-memory file of the first example of `params.spec.config`, named for its tab. */
const EXAMPLE = 'cel-lens-example:/Example%20of%20params.spec.config.yaml';

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
  const document = openDocuments[0];
  if (document === undefined) {
    throw new Error('no example was opened');
  }
  return { editors, document, used, log };
}

/** The text of an in-memory file of a pasted example. */
async function memory(uri: string): Promise<string> {
  const provider = fileSystemProviders.get(EXAMPLE_SCHEME);
  if (provider === undefined) {
    throw new Error('no file system provider for the examples');
  }
  return new TextDecoder().decode(await provider.readFile(Uri.parse(uri)));
}

describe('ExampleEditors', () => {
  afterEach(reset);

  it('opens an in-memory YAML file beside the rule, named after the value for its tab, starting with what to paste, the cursor below it (#14)', async () => {
    const { document } = await openExample();

    expect(document).toMatchObject({
      uri: Uri.parse(EXAMPLE),
      languageId: 'yaml',
      text: exampleComment('params.spec.config', true),
    });
    expect(await memory(EXAMPLE)).toBe(exampleComment('params.spec.config', true));
    expect(shownDocuments).toEqual([
      {
        uri: EXAMPLE,
        selection: new Range(3, 0, 3, 0),
        viewColumn: ViewColumn.Beside,
        preserveFocus: false,
        preview: false,
      },
    ]);
  });

  it('names a second example of the same value apart (#14)', async () => {
    const { editors } = await openExample();

    await editors.open('params.spec.config', true, () => Promise.resolve());

    expect(openDocuments.map((document) => document.uri.toString())).toEqual([
      EXAMPLE,
      'cel-lens-example:/Example%20of%20params.spec.config%20(2).yaml',
    ]);
  });

  it('saves a change of the active example in the same turn with Save without Formatting, never TextDocument.save(), so it is not dirty when closed, however soon (#16)', async () => {
    const { document } = await openExample();
    activeEditor.languageId = 'yaml';
    activeEditor.uri = EXAMPLE;
    const change = [{ range: { start: { line: 3 }, end: { line: 3 } }, text: 'teams: x' }];

    document.isDirty = true;
    changeDocument(document, change);

    expect(executedCommands).toEqual([
      { command: 'workbench.action.files.saveWithoutFormatting', args: [] },
    ]);
    expect(savedDocuments).toEqual([]);
  });

  it('saves an example changed while another editor was active only once it is active again, and never another file (#16)', async () => {
    const { document } = await openExample();
    const other = {
      uri: Uri.parse(CELLENS),
      languageId: 'plaintext',
      getText: () => '',
      isDirty: true,
    };
    activeEditor.languageId = 'yaml';
    activeEditor.uri = CELLENS;
    document.isDirty = true;

    changeDocument(document, [{ range: { start: { line: 3 }, end: { line: 3 } }, text: 'a' }]);
    changeDocument(other, [{ range: { start: { line: 0 }, end: { line: 0 } }, text: 'a' }]);
    changeActiveEditor('yaml');
    expect(executedCommands).toEqual([]);

    activeEditor.uri = EXAMPLE;
    changeActiveEditor('yaml');

    expect(executedCommands).toEqual([
      { command: 'workbench.action.files.saveWithoutFormatting', args: [] },
    ]);
    expect(savedDocuments).toEqual([]);
  });

  it("keeps an example across a change of its language, which closes it and opens it again (#14's review)", async () => {
    const { document, used } = await openExample();

    changeLanguage(document);
    await settle();
    await runCommand(USE_EXAMPLE_COMMAND, EXAMPLE);

    expect(used).toHaveLength(1);
    expect(used[0]?.editor.closed.aborted).toBe(false);
    expect(await memory(EXAMPLE)).toBe(exampleComment('params.spec.config', true));
  });

  it('gives its editor, in any language, and no other, the code lens Use this example as the type', async () => {
    const { editors, document } = await openExample();

    expect(codeLensProviders).toEqual([
      { selector: { scheme: EXAMPLE_SCHEME }, provider: editors },
    ]);
    expect(editors.provideCodeLenses(document)).toEqual([
      new CodeLens(new Range(0, 0, 0, 0), {
        title: 'Use this example as the type',
        command: USE_EXAMPLE_COMMAND,
        arguments: [EXAMPLE],
      }),
    ]);
    expect(editors.provideCodeLenses({ uri: Uri.parse('cel-lens-example:/Other.yaml') })).toEqual(
      [],
    );
  });

  it.each([
    ['the code lens, with its URI', () => [EXAMPLE]],
    ['the editor title, with its Uri', () => [Uri.parse(EXAMPLE)]],
    [
      'the Command Palette, on the active editor',
      () => {
        activeEditor.languageId = 'yaml';
        activeEditor.uri = EXAMPLE;
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

  it('closes its tab without asking, and forgets its file once it closed', async () => {
    const { document, used } = await openExample();
    await runCommand(USE_EXAMPLE_COMMAND, EXAMPLE);

    await used[0]?.editor.close();
    closeDocument(document);
    await settle();

    expect(closedTabs).toEqual([EXAMPLE]);
    expect(shownDocuments).toHaveLength(1);
    await expect(memory(EXAMPLE)).rejects.toThrow('file not found');
  });

  it("reverts and closes an example with a change not yet saved, in its group, with no save participant and no question (#14's review)", async () => {
    const { document, used } = await openExample();
    const editor = new FakeEditor(document);
    editor.viewColumn = ViewColumn.Two;
    visibleEditors.push(editor);
    await runCommand(USE_EXAMPLE_COMMAND, EXAMPLE);
    document.isDirty = true;

    await used[0]?.editor.close();

    expect(shownDocuments.at(-1)).toEqual({
      uri: EXAMPLE,
      selection: undefined,
      viewColumn: ViewColumn.Two,
      preserveFocus: false,
    });
    expect(executedCommands).toEqual([
      { command: 'workbench.action.revertAndCloseActiveEditor', args: [] },
    ]);
    expect(savedDocuments).toEqual([]);
  });

  it("logs, and lets the flow go on, when the example's editor cannot be closed (#14's review)", async () => {
    const { document, used, log } = await openExample();
    await runCommand(USE_EXAMPLE_COMMAND, EXAMPLE);
    document.isDirty = true;
    showFailure.error = new Error('the editor is gone');

    await expect(used[0]?.editor.close()).resolves.toBeUndefined();

    expect(log.lines).toEqual([`Could not close ${EXAMPLE}: Error: the editor is gone`]);
  });

  it('writes nothing once its editor is closed, and logs the stray use', async () => {
    const { document, used, log } = await openExample();

    closeDocument(document);
    await settle();
    await runCommand(USE_EXAMPLE_COMMAND, EXAMPLE);

    expect(used).toEqual([]);
    expect(log.lines).toEqual([`cel-lens.useExample ran on ${EXAMPLE}, which holds no example.`]);
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
    const uri = 'cel-lens-example:/Example%20of%20params.yaml';

    const first = runCommand(USE_EXAMPLE_COMMAND, uri);
    await runCommand(USE_EXAMPLE_COMMAND, uri);
    expect(uses).toBe(1);
    finish();
    await first;
    const again = runCommand(USE_EXAMPLE_COMMAND, uri);
    await settle();

    expect(uses).toBe(2);
    finish();
    await again;
  });

  it("tells a use that its editor closed, and does not open it again to close it (#9's review)", async () => {
    const { document, used } = await openExample();
    await runCommand(USE_EXAMPLE_COMMAND, EXAMPLE);

    closeDocument(document);
    await settle();
    await used[0]?.editor.close();

    expect(used[0]?.editor.closed.aborted).toBe(true);
    expect(shownDocuments).toHaveLength(1);
    expect(closedTabs).toEqual([]);
  });

  it('closes the editors of pasted examples a window reload restored, as their files are gone, and no other (#14)', () => {
    openTabs.push(
      { input: new TabInputText(Uri.parse(EXAMPLE)) },
      { input: new TabInputText(Uri.parse(CELLENS)) },
    );

    new ExampleEditors(window.createOutputChannel('cel-lens')).dispose();

    expect(closedTabs).toEqual([EXAMPLE]);
  });

  it('frees its command, its code lens provider and its files on dispose', async () => {
    const { editors } = await openExample();

    editors.dispose();

    expect(await commands.getCommands()).toEqual([]);
    expect(codeLensProviders).toEqual([]);
    expect(fileSystemProviders.size).toBe(0);
  });
});

describe('ExampleFiles', () => {
  const uri = Uri.parse(EXAMPLE);

  it('keeps a file in memory: written, stated, read and deleted', () => {
    const files = new ExampleFiles();

    files.writeFile(uri, new TextEncoder().encode('teams: []\n'));

    expect(files.stat(uri)).toMatchObject({ type: 1, size: 10 });
    expect(new TextDecoder().decode(files.readFile(uri))).toBe('teams: []\n');
    files.delete(uri);
    expect(() => files.readFile(uri)).toThrow('file not found');
  });

  it('has no file of the window before a reload, nor folders, nor renames', () => {
    const files = new ExampleFiles();

    expect(() => files.stat(uri)).toThrow('file not found');
    expect(files.readDirectory()).toEqual([]);
    expect(() => {
      files.createDirectory();
    }).toThrow('no permissions');
    expect(() => {
      files.rename();
    }).toThrow('no permissions');
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
