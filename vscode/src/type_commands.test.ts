import { afterEach, describe, expect, it } from 'vitest';

import {
  FakeEditor,
  Range,
  ViewColumn,
  Uri,
  Diagnostic,
  PRESS_BACK,
  activeEditor,
  addWorkspaceFolder,
  closeDocument,
  commandFailure,
  commandResults,
  diagnosticCollections,
  errorChoice,
  executedCommands,
  fileTexts,
  quickPicks,
  reset,
  runCommand,
  settings,
  settle,
  showFailure,
  shownDocuments,
  shownErrors,
  shownInfos,
  savedDocuments,
  closedTabs,
  openDocuments,
  visibleEditors,
  window,
  workspace,
} from '../test/vscode';
import { ExampleEditors, ExampleNotes } from './example_editor';
import { OPEN_YAML_MESSAGE, giveValueType, typeUi } from './type_commands';
import { INLINE_INFO_OFF_MESSAGE, NO_VALUES_MESSAGE, TypeFlow } from './type_flow';
import { GO_TYPE_PLACEHOLDER } from './value_types';

const CELLENS = 'file:///workspace/repo/.cellens';

const CONFIG = {
  cellens: CELLENS,
  file: 'policies/limits.yaml',
  document: 'ValidatingAdmissionPolicy limits',
  variable: 'params',
  path: 'spec.config',
  value: 'params.spec.config',
};

const CHOICES = {
  kubernetesVersion: '1.37',
  builtIn: [
    {
      apiVersion: 'apps/v1',
      group: 'apps',
      version: 'v1',
      kind: 'Deployment',
      description: 'Deployment enables declarative updates for Pods and ReplicaSets.',
      stability: 'ga',
    },
    {
      apiVersion: 'storage.k8s.io/v1beta1',
      group: 'storage.k8s.io',
      version: 'v1beta1',
      kind: 'VolumeAttributesClass',
      description: 'VolumeAttributesClass represents a specification of mutable volume attributes.',
      stability: 'beta',
    },
  ],
  crds: [
    {
      apiVersion: 'example.com/v1',
      group: 'example.com',
      version: 'v1',
      kind: 'Widget',
      description: 'A widget.',
    },
  ],
  goTypes: [
    {
      folder: 'file:///workspace/repo/api/v1',
      name: 'QuotaSpec',
      doc: 'QuotaSpec is the quota of a team.',
    },
    { folder: 'file:///workspace/repo/internal/limits', name: 'Limit', doc: '' },
  ],
  examples: [],
};

/** Where the engine puts the cursor in `.cellens`: between the braces of `type: "{}"`. */
const SELECTION = { start: { line: 9, character: 11 }, end: { line: 9, character: 11 } };

const SCHEMA_LINE = '$(edit) Write a schema in .cellens…';
const GO_LINE = '$(symbol-struct) Use a Go type…';
const SHORT_LINE = '$(edit) Write a short type in .cellens…';
const PASTE_LINE = '$(clippy) Paste an example…';

const SAMPLES = 'file:///workspace/repo/config/samples/limit.yaml';

/** The in-memory file of the first example of `params.spec.config`, named for its tab. */
const EXAMPLE = 'cel-lens-example:/Example%20of%20params.spec.config.yaml';
const SAMPLE =
  'apiVersion: rules.example.com/v1\nkind: ReplicaLimit\nspec:\n  config:\n    teams: []\n';

/** The schema the engine works out from an example, and what it leaves open. */
const INFERRED = {
  schema: {
    type: 'object',
    properties: {
      teams: { type: 'array', items: { 'x-kubernetes-preserve-unknown-fields': true } },
    },
  },
  notes: ['teams: an empty list says nothing about its items'],
};

/** `.cellens` once the engine wrote the schema, the cursor on line 9 (`SELECTION`) below its `schema:`. */
const WRITTEN = [
  'types:',
  '  - file: policies/limits.yaml',
  '    document: ValidatingAdmissionPolicy limits',
  '    variable: params',
  '    path: spec.config',
  '    schema:',
  '      properties:',
  '        teams:',
  '          items:',
  '            x-kubernetes-preserve-unknown-fields: true',
  '          type: array',
  '      type: object',
].join('\n');

type Run = (command: string, argument?: unknown, signal?: AbortSignal) => Promise<unknown>;

/**
 * The engine, faked: it lists `CHOICES`, and applies every edit before it answers, as the real
 * one does; a setType with a snippet answers where the cursor goes. `hold` keeps a command's
 * answer until the test gives it.
 */
function fakeEngine(): {
  calls: { command: string; argument: unknown }[];
  hold: (command: string) => (answer?: unknown) => void;
  run: Run;
} {
  const calls: { command: string; argument: unknown }[] = [];
  const held = new Map<string, Promise<unknown>>();
  const answer = (command: string, argument: unknown): unknown => {
    if (command === 'cel-lens.typeChoices') {
      return CHOICES;
    }
    if (command === 'cel-lens.inferType') {
      return INFERRED;
    }
    const snippet = typeof argument === 'object' && argument !== null && 'snippet' in argument;
    return snippet
      ? { cellens: CELLENS, change: 'added', selection: SELECTION }
      : { cellens: CELLENS, change: 'added' };
  };
  return {
    calls,
    hold: (command) => {
      let give: (answer?: unknown) => void = () => undefined;
      held.set(
        command,
        new Promise((resolve) => {
          give = resolve;
        }),
      );
      return (given) => {
        give(given);
      };
    },
    run: async (command, argument) => {
      calls.push({ command, argument });
      const waiting = held.get(command);
      const given = waiting === undefined ? undefined : await waiting;
      const answered = given ?? answer(command, argument);
      // The engine's edit of .cellens, when it is open: one more version, not saved.
      const edited = openDocuments.find((document) => document.uri.toString() === CELLENS);
      if (edited !== undefined && parseChange(answered)) {
        edited.version += 1;
        edited.isDirty = true;
      }
      return answered;
    },
  };
}

/** The UI of the flow, with the editors of pasted examples and the notes on them. */
function uiOf(log: ReturnType<typeof window.createOutputChannel>): ReturnType<typeof typeUi> {
  return typeUi(log, new ExampleEditors(log), new ExampleNotes());
}

/** Whether an answer is of an edit the engine made: added, replaced, created, removed. */
function parseChange(answer: unknown): boolean {
  return (
    typeof answer === 'object' &&
    answer !== null &&
    'change' in answer &&
    answer.change !== 'unchanged' &&
    !('selection' in answer)
  );
}

function setUp(): {
  engine: ReturnType<typeof fakeEngine>;
  log: ReturnType<typeof window.createOutputChannel>;
  flow: TypeFlow;
  ui: ReturnType<typeof typeUi>;
} {
  const engine = fakeEngine();
  const log = window.createOutputChannel('cel-lens');
  const ui = uiOf(log);
  return { engine, log, ui, flow: new TypeFlow(engine.run, ui, log) };
}

/** An editor of the group `viewColumn` that shows the document at `uri`. */
function editorOf(uri: string, viewColumn: number): FakeEditor {
  const editor = new FakeEditor({ languageId: 'yaml', getText: () => '', uri: Uri.parse(uri) });
  editor.viewColumn = viewColumn;
  return editor;
}

describe('Give it a type, against the VS Code stand-in', () => {
  afterEach(reset);

  it('shows the picker at once, titled after the value and busy with a schema, a Go type and a short type, then the kinds told apart by icon, found by what is typed in descriptions and details too', async () => {
    const { flow } = setUp();

    await flow.giveType(CONFIG);

    expect(quickPicks.created).toEqual([
      {
        title: 'Give params.spec.config a type',
        placeholder: 'Pick how to write the type, or type to find a kind such as Deployment',
        matchOnDescription: true,
        matchOnDetail: true,
        busy: true,
        buttons: [],
        value: '',
        labels: [SCHEMA_LINE, GO_LINE, SHORT_LINE, PASTE_LINE],
      },
    ]);
    expect(quickPicks.shown[0]).toEqual([
      {
        label: SCHEMA_LINE,
        description: 'OpenAPI',
        detail: 'Properties, descriptions and formats, as in a CRD; opens .cellens beside the rule',
        value: { action: 'write', form: 'schema' },
      },
      {
        label: GO_LINE,
        description: '2 types',
        detail: "A struct of the workspace's Go code, such as a kubebuilder API type",
        alwaysShow: true,
        value: { action: 'pickGo' },
      },
      {
        label: SHORT_LINE,
        description: 'CEL',
        detail: 'Such as {name: string, replicas: int}; opens .cellens beside the rule',
        value: { action: 'write', form: 'type' },
      },
      {
        label: PASTE_LINE,
        detail: 'YAML or JSON, in an editor beside the rule; a schema is worked out from it',
        value: { action: 'paste' },
      },
      { label: 'Built-in kinds · Kubernetes 1.37', kind: -1 },
      {
        label: '$(symbol-class) Deployment',
        description: 'apps/v1',
        detail: 'Deployment enables declarative updates for Pods and ReplicaSets.',
        value: { action: 'kind', kind: 'apps/v1 Deployment' },
      },
      {
        label: '$(symbol-class) VolumeAttributesClass',
        description: 'storage.k8s.io/v1beta1 · beta',
        detail: 'VolumeAttributesClass represents a specification of mutable volume attributes.',
        value: { action: 'kind', kind: 'storage.k8s.io/v1beta1 VolumeAttributesClass' },
      },
      { label: 'Workspace CRDs', kind: -1 },
      {
        label: '$(file-code) Widget',
        description: 'example.com/v1',
        detail: 'A widget.',
        value: { action: 'kind', kind: 'example.com/v1 Widget' },
      },
    ]);
  });

  it('lets a kind be picked from the busy picker after the engine lists them, and sets it at once', async () => {
    const { flow, engine } = setUp();
    const listed = engine.hold('cel-lens.typeChoices');
    quickPicks.next = '$(symbol-class) Deployment';

    const giving = flow.giveType(CONFIG);
    await settle();

    expect(quickPicks.created).toMatchObject([
      { busy: true, labels: [SCHEMA_LINE, GO_LINE, SHORT_LINE, PASTE_LINE] },
    ]);
    expect(quickPicks.shown).toEqual([]);

    listed(CHOICES);
    await giving;

    expect(quickPicks.shown[0]).toHaveLength(9);
    expect(engine.calls.at(-1)).toEqual({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, kind: 'apps/v1 Deployment' },
    });
    expect(shownDocuments).toEqual([]);
  });

  it('starts an edit on the kind the value has', async () => {
    const { flow } = setUp();

    await flow.editType({ ...CONFIG, kind: 'example.com/v1 Widget' });

    expect(quickPicks.active[0]).toMatchObject([{ label: '$(file-code) Widget' }]);
  });

  it.each([
    ['a schema', SCHEMA_LINE, 'schema'],
    ['a short type', SHORT_LINE, 'type'],
  ])(
    'writes %s in .cellens: opens it beside the rule once the engine applied its entry, focused on its first placeholder, and no preview',
    async (_name, line, snippet) => {
      const { flow, engine } = setUp();
      const written = engine.hold('cel-lens.setType');
      quickPicks.next = line;

      const giving = flow.giveType(CONFIG);
      await settle();
      expect(engine.calls.at(-1)).toEqual({
        command: 'cel-lens.setType',
        argument: { ...CONFIG, snippet },
      });
      expect(shownDocuments).toEqual([]);

      written();
      await giving;

      expect(shownDocuments).toEqual([
        {
          uri: CELLENS,
          selection: new Range(9, 11, 9, 11),
          viewColumn: ViewColumn.Beside,
          preserveFocus: false,
          preview: false,
        },
      ]);
    },
  );

  it('opens .cellens in the editor group that already shows it, so the rule stays in view and no third group opens', async () => {
    const { flow } = setUp();
    visibleEditors.push(
      editorOf('file:///workspace/repo/policies/limits.yaml', ViewColumn.One),
      editorOf(CELLENS, ViewColumn.Two),
    );
    quickPicks.next = SHORT_LINE;

    await flow.giveType(CONFIG);

    expect(shownDocuments).toMatchObject([{ uri: CELLENS, viewColumn: ViewColumn.Two }]);
  });

  it('opens an edit at the entry the value has, where the engine puts the cursor on its type', async () => {
    const { flow, engine } = setUp();
    const atType = { start: { line: 4, character: 10 }, end: { line: 4, character: 24 } };
    const found = engine.hold('cel-lens.setType');
    quickPicks.next = SHORT_LINE;

    const editing = flow.editType({ ...CONFIG, type: '{name: string}' });
    await settle();
    found({ cellens: CELLENS, change: 'unchanged', selection: atType });
    await editing;

    expect(quickPicks.created.map((pick) => pick.title)).toEqual([
      'Edit the type of params.spec.config',
    ]);
    expect(quickPicks.active[0]).toMatchObject([{ label: SHORT_LINE }]);
    expect(engine.calls.at(-1)).toEqual({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, snippet: 'type' },
    });
    expect(shownDocuments).toMatchObject([{ uri: CELLENS, selection: new Range(4, 10, 4, 24) }]);
  });

  it('turns to the Go types, which start with what was typed in the first picker, each with its folder in the workspace and its doc comment, and sets the one picked at once', async () => {
    const { flow, engine } = setUp();
    addWorkspaceFolder('repo');
    quickPicks.typed = 'quota';
    quickPicks.queue.push(GO_LINE, '$(symbol-struct) QuotaSpec');

    await flow.giveType(CONFIG);

    expect(quickPicks.created[1]).toEqual({
      title: 'Give params.spec.config a type › Go type',
      placeholder: GO_TYPE_PLACEHOLDER,
      matchOnDescription: true,
      matchOnDetail: true,
      busy: true,
      buttons: ['Back'],
      value: 'quota',
      labels: [],
    });
    expect(quickPicks.shown[1]).toEqual([
      {
        label: '$(symbol-struct) QuotaSpec',
        description: 'api/v1',
        detail: 'QuotaSpec is the quota of a team.',
        value: { action: 'go', go: './api/v1#QuotaSpec' },
      },
      {
        label: '$(symbol-struct) Limit',
        description: 'internal/limits',
        value: { action: 'go', go: './internal/limits#Limit' },
      },
    ]);
    expect(engine.calls.at(-1)).toEqual({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, go: './api/v1#QuotaSpec' },
    });
    expect(shownDocuments).toEqual([]);
  });

  it('gives a value the type of a document of the workspace: reads its file, and opens .cellens beside the rule with the notes at their keys, as information', async () => {
    const { flow, engine } = setUp();
    addWorkspaceFolder('repo');
    fileTexts.set(SAMPLES, SAMPLE);
    fileTexts.set(CELLENS, WRITTEN);
    const listed = engine.hold('cel-lens.typeChoices');
    quickPicks.next = '$(file) config/samples/limit.yaml';

    const giving = flow.giveType(CONFIG);
    await settle();
    listed({
      ...CHOICES,
      examples: [
        {
          uri: SAMPLES,
          file: 'config/samples/limit.yaml',
          line: 1,
          apiVersion: 'rules.example.com/v1',
          kind: 'ReplicaLimit',
        },
      ],
    });
    await giving;

    expect(engine.calls.slice(1)).toEqual([
      { command: 'cel-lens.inferType', argument: { text: SAMPLE, line: 1, path: 'spec.config' } },
      { command: 'cel-lens.setType', argument: { ...CONFIG, schema: INFERRED.schema } },
      { command: 'cel-lens.setType', argument: { ...CONFIG, snippet: 'schema' } },
    ]);
    expect(shownDocuments).toEqual([
      {
        uri: CELLENS,
        selection: new Range(9, 11, 9, 11),
        viewColumn: ViewColumn.Beside,
        preserveFocus: false,
        preview: false,
      },
    ]);
    const note = new Diagnostic(
      new Range(7, 8, 7, 13),
      'teams: an empty list says nothing about its items',
      2,
    );
    note.source = 'cel-lens example';
    expect(diagnosticCollections[0]?.entries.get(CELLENS)).toEqual([note]);
    expect(shownErrors).toEqual([]);
  });

  it('pastes an example: opens it beside the rule in memory, and Use this example as the type writes its schema, closes its tab without asking, then opens .cellens beside the rule', async () => {
    const { flow, engine } = setUp();
    fileTexts.set(CELLENS, WRITTEN);
    quickPicks.next = PASTE_LINE;

    await flow.giveType(CONFIG);
    expect(shownDocuments).toMatchObject([{ uri: EXAMPLE, viewColumn: ViewColumn.Beside }]);
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);

    const [document] = openDocuments;
    if (document !== undefined) {
      document.text += 'teams: []\n';
    }
    await runCommand('cel-lens.useExample', EXAMPLE);

    expect(engine.calls.slice(1).map((call) => call.argument)).toEqual([
      { text: document?.text },
      { ...CONFIG, schema: INFERRED.schema },
      { ...CONFIG, snippet: 'schema' },
    ]);
    expect(closedTabs).toEqual([EXAMPLE]);
    expect(shownDocuments.map((shown) => shown.uri)).toEqual([EXAMPLE, CELLENS]);
    expect(diagnosticCollections[0]?.entries.get(CELLENS)).toHaveLength(1);
  });

  it('leaves .cellens unsaved when the person typed in it while the engine wrote (#14)', async () => {
    const { flow, engine } = setUp();
    fileTexts.set(CELLENS, WRITTEN);
    const cellens = await workspace.openTextDocument(Uri.parse(CELLENS));
    const written = engine.hold('cel-lens.setType');
    quickPicks.next = '$(symbol-class) Deployment';

    const giving = flow.giveType(CONFIG);
    await settle();
    cellens.version += 1;
    written();
    await giving;

    expect(savedDocuments).toEqual([]);
  });

  it("saves .cellens once the engine wrote a kind, when it had no unsaved edits, and leaves it unsaved when it had (#9's review)", async () => {
    const { flow } = setUp();
    fileTexts.set(CELLENS, WRITTEN);
    const cellens = await workspace.openTextDocument(Uri.parse(CELLENS));
    quickPicks.next = '$(symbol-class) Deployment';

    await flow.giveType(CONFIG);
    expect(savedDocuments).toEqual([CELLENS]);

    cellens.isDirty = true;
    await flow.giveType(CONFIG);
    expect(savedDocuments).toEqual([CELLENS]);
  });

  it('writes nothing when the editor of a pasted example is closed', async () => {
    const { flow, engine } = setUp();
    quickPicks.next = PASTE_LINE;
    await flow.giveType(CONFIG);

    closeDocument({ uri: Uri.parse(EXAMPLE) });
    await settle();
    await runCommand('cel-lens.useExample', EXAMPLE);

    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
    expect(shownDocuments).toHaveLength(1);
  });

  it("goes back from the Go types to the first picker with the Back button (#8's review)", async () => {
    const { flow, engine } = setUp();
    quickPicks.queue.push(GO_LINE, PRESS_BACK, '$(symbol-class) Deployment');

    await flow.giveType(CONFIG);

    expect(quickPicks.created.map((pick) => pick.title)).toEqual([
      'Give params.spec.config a type',
      'Give params.spec.config a type › Go type',
      'Give params.spec.config a type',
    ]);
    expect(engine.calls.at(-1)).toEqual({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, kind: 'apps/v1 Deployment' },
    });
  });

  it("lists only the Go types of the workspace folder of the .cellens (#8's review)", async () => {
    const engine = fakeEngine();
    const listed = engine.hold('cel-lens.typeChoices');
    const log = window.createOutputChannel('cel-lens');
    addWorkspaceFolder('charts');
    addWorkspaceFolder('operator');
    quickPicks.queue.push(GO_LINE, '$(symbol-struct) QuotaSpec');

    const giving = new TypeFlow(engine.run, uiOf(log), log).giveType({
      ...CONFIG,
      cellens: 'file:///workspace/charts/.cellens',
    });
    await settle();
    listed({
      ...CHOICES,
      goTypes: [
        { folder: 'file:///workspace/charts/api', name: 'ChartSpec', doc: '' },
        { folder: 'file:///workspace/operator/api/v1', name: 'QuotaSpec', doc: '' },
      ],
    });
    await giving;

    expect(quickPicks.shown[1]).toMatchObject([{ label: '$(symbol-struct) ChartSpec' }]);
    expect(quickPicks.shown[1]).toHaveLength(1);
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
  });

  it("says once that the engine is too old to write in .cellens, with a button to cel-lens.serverPath (#8's review)", async () => {
    const engine = fakeEngine();
    const log = window.createOutputChannel('cel-lens');
    const run: Run = (command, argument, signal) =>
      command === 'cel-lens.setType'
        ? Promise.reject(
            Object.assign(
              new Error(
                'cel-lens.setType: give the type, such as type: "{name: string}", the kind, such as kind: "apps/v1 Deployment", the schema, such as schema: {"type": "object", "properties": {...}}, or the Go type, such as go: "./api/v1#QuotaSpec"',
              ),
              {
                code: -32602,
              },
            ),
          )
        : engine.run(command, argument, signal);
    errorChoice.next = 'Open Engine Setting';
    quickPicks.next = SCHEMA_LINE;

    await new TypeFlow(run, uiOf(log), log).giveType(CONFIG);

    expect(shownErrors).toEqual([
      {
        message:
          'This cel-lens engine is too old to write a type in .cellens or list Go types: set cel-lens.serverPath to a newer cel-lens-core, then run cel-lens: Restart Engine.',
        buttons: ['Open Engine Setting'],
      },
    ]);
    expect(executedCommands).toEqual([
      { command: 'workbench.action.openSettings', args: ['cel-lens.serverPath'] },
    ]);
    expect(shownDocuments).toEqual([]);
  });

  it('says once that .cellens could not be opened, with Show Logs', async () => {
    const { flow, log } = setUp();
    showFailure.error = new Error('cannot open');
    quickPicks.next = SCHEMA_LINE;

    await flow.giveType(CONFIG);

    expect(shownErrors).toEqual([
      {
        message: 'cel-lens could not open .cellens. Show the logs for the reason.',
        buttons: ['Show Logs'],
      },
    ]);
    expect(log.lines).toEqual([`Could not open ${CELLENS}: Error: cannot open`]);
  });
});

describe('typeUi', () => {
  afterEach(reset);

  it('shows an error with Show Logs, and opens the logs from it', async () => {
    const log = window.createOutputChannel('cel-lens');
    errorChoice.next = 'Show Logs';

    await uiOf(log).showError('cel-lens could not remove the type.', true);

    expect(shownErrors).toEqual([
      { message: 'cel-lens could not remove the type.', buttons: ['Show Logs'] },
    ]);
    expect(log.shown).toBe(true);
  });

  it('shows the reason a refusal gives with no button', async () => {
    await uiOf(window.createOutputChannel('cel-lens')).showError('Run it again.', false);

    expect(shownErrors).toEqual([{ message: 'Run it again.', buttons: [] }]);
  });

  it('opens the cel-lens.experimental setting', async () => {
    await uiOf(window.createOutputChannel('cel-lens')).openExperimentalSetting();

    expect(executedCommands).toEqual([
      { command: 'workbench.action.openSettings', args: ['cel-lens.experimental'] },
    ]);
  });

  it.each([
    ['one workspace folder', ['repo'], 'file:///workspace/repo/api/v1', 'api/v1'],
    [
      'several, with the name of its own',
      ['repo', 'other'],
      'file:///workspace/repo/api/v1',
      'repo/api/v1',
    ],
    ['the workspace folder itself, by its name', ['repo'], 'file:///workspace/repo', 'repo'],
  ])(
    'writes a folder relative to its workspace folder with %s',
    (_name, folders, uri, expected) => {
      for (const folder of folders) {
        addWorkspaceFolder(folder);
      }

      expect(uiOf(window.createOutputChannel('cel-lens')).workspacePath(uri)).toBe(expected);
    },
  );
});

describe('giveValueType', () => {
  afterEach(reset);

  const lens = {
    range: { start: { line: 15, character: 4 }, end: { line: 15, character: 4 } },
    command: {
      title: 'params.spec.config: dyn · Give it a type',
      command: 'cel-lens.giveType',
      arguments: [CONFIG],
    },
  };

  it('lists the values of the active YAML file from its code lenses, and continues with the one picked', async () => {
    const { flow, ui, log, engine } = setUp();
    activeEditor.languageId = 'yaml';
    commandResults.set('vscode.executeCodeLensProvider', [lens]);
    quickPicks.next = 'params.spec.config';

    await giveValueType(flow, ui, log);

    expect(executedCommands).toEqual([
      { command: 'vscode.executeCodeLensProvider', args: [expect.anything()] },
    ]);
    expect(String(executedCommands[0]?.args[0])).toBe(activeEditor.uri);
    expect(quickPicks.created.map((pick) => pick.title)).toEqual([
      'Give a value a type',
      'Give params.spec.config a type',
    ]);
    expect(quickPicks.shown[0]).toMatchObject([
      {
        label: 'params.spec.config',
        description: 'dyn',
        detail: 'ValidatingAdmissionPolicy limits · line 16',
      },
    ]);
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
  });

  it.each([
    ['no value', true, NO_VALUES_MESSAGE],
    ['the line above each rule off', false, INLINE_INFO_OFF_MESSAGE],
  ])('says so in one quiet message with %s', async (_name, inlineInfo, message) => {
    const { flow, ui, log } = setUp();
    activeEditor.languageId = 'yaml';
    settings.set('cel-lens.inlineInfo', inlineInfo);
    commandResults.set('vscode.executeCodeLensProvider', []);

    await giveValueType(flow, ui, log);

    expect(shownInfos).toEqual([message]);
    expect(quickPicks.shown).toEqual([]);
  });

  it.each([
    ['a JSON file', 'json', 'file:///workspace/a.json'],
    ['a .cellens, which opens as plain text', 'plaintext', 'file:///workspace/.cellens'],
  ])('asks for a YAML file when %s is active', async (_name, languageId, uri) => {
    const { flow, ui, log } = setUp();
    activeEditor.languageId = languageId;
    activeEditor.uri = uri;

    await giveValueType(flow, ui, log);

    expect(shownInfos).toEqual([OPEN_YAML_MESSAGE]);
    expect(executedCommands).toEqual([]);
  });

  it('says once that the values could not be listed, and logs why', async () => {
    const { flow, ui, log } = setUp();
    activeEditor.languageId = 'yaml';
    commandFailure.error = new Error('no provider');

    await giveValueType(flow, ui, log);

    expect(shownErrors).toEqual([
      {
        message: 'cel-lens could not list the values of this file. Show the logs for the reason.',
        buttons: ['Show Logs'],
      },
    ]);
    expect(log.lines).toEqual([
      `Could not read the lines above the rules of ${activeEditor.uri}: Error: no provider`,
    ]);
  });
});
