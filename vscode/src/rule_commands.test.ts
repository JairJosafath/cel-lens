import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  type FakeDiagnostic,
  FakeEditor,
  Range,
  Uri,
  ViewColumn,
  commandFailure,
  commands,
  diagnostics,
  errorChoice,
  executedCommands,
  quickPicks,
  reset,
  settings,
  shownDocuments,
  shownErrors,
  statusBarMessages,
  visibleEditors,
  window,
} from '../test/vscode';
import { listValues, ruleArgument, showRule } from './rule_commands';

const URI = 'file:///workspace/crd.yaml';
const CELLENS = 'file:///workspace/.cellens';

/** Closes an open problems peek first, so the next-problem command starts afresh from the cursor. */
const CLOSE_PEEK = { command: 'closeMarkersNavigation', args: [] };

/** The rule `self.replicas >= self.minReplicas` on line 16 (15 from 0), after `- rule: `. */
const RULE = { start: { line: 15, character: 10 }, end: { line: 15, character: 44 } };

function log(): ReturnType<typeof window.createOutputChannel> {
  return window.createOutputChannel('cel-lens');
}

function problem(line: number, character: number, severity: number): FakeDiagnostic {
  return {
    range: { start: { line, character }, end: { line, character: character + 4 } },
    severity,
    source: 'cel-lens',
    message: 'no such key: replica',
  };
}

describe('showRule', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    reset();
  });

  it("opens the .cellens entry a value's type comes from beside the rule, the entry selected, with no problems peek and no hover (#16)", async () => {
    const entry = { start: { line: 4, character: 4 }, end: { line: 8, character: 20 } };

    await showRule({ uri: CELLENS, range: entry }, log());

    expect(shownDocuments).toEqual([
      {
        uri: CELLENS,
        selection: new Range(4, 4, 8, 20),
        viewColumn: ViewColumn.Beside,
        preserveFocus: false,
        preview: false,
      },
    ]);
    expect(executedCommands).toEqual([]);
  });

  it('opens that .cellens in the editor group that already shows it (#16)', async () => {
    const shown = new FakeEditor({
      languageId: 'plaintext',
      getText: () => '',
      uri: Uri.parse(CELLENS),
    });
    shown.viewColumn = ViewColumn.Three;
    visibleEditors.push(shown);

    await showRule({ uri: CELLENS, range: RULE }, log());

    expect(shownDocuments).toMatchObject([{ uri: CELLENS, viewColumn: ViewColumn.Three }]);
  });

  it("opens the problems peek at the rule's first problem", async () => {
    diagnostics.set(URI, [problem(15, 30, 0), problem(15, 15, 0)]);

    await showRule({ uri: URI, range: RULE }, log());

    expect(shownDocuments).toEqual([{ uri: URI, selection: new Range(15, 15, 15, 15) }]);
    expect(executedCommands).toEqual([
      CLOSE_PEEK,
      { command: 'editor.action.marker.next', args: [] },
    ]);
  });

  it('closes a problems peek left open before it moves the cursor, so a second click opens the same problem', async () => {
    diagnostics.set(URI, [problem(15, 15, 0), problem(30, 10, 0)]);
    const show = vi.spyOn(window, 'showTextDocument');
    const execute = vi.spyOn(commands, 'executeCommand');

    await showRule({ uri: URI, range: RULE }, log());
    await showRule({ uri: URI, range: RULE }, log());

    expect(execute.mock.calls.map(([command]) => command)).toEqual([
      'closeMarkersNavigation',
      'editor.action.marker.next',
      'closeMarkersNavigation',
      'editor.action.marker.next',
    ]);
    const [firstClose, , secondClose] = execute.mock.invocationCallOrder;
    const [firstShow, secondShow] = show.mock.invocationCallOrder;
    expect(firstClose).toBeLessThan(firstShow ?? 0);
    expect(secondClose).toBeLessThan(secondShow ?? 0);
    expect(shownDocuments).toEqual([
      { uri: URI, selection: new Range(15, 15, 15, 15) },
      { uri: URI, selection: new Range(15, 15, 15, 15) },
    ]);
  });

  it('shows the hover at the start of a rule that has no problem', async () => {
    diagnostics.set(URI, [problem(30, 10, 0)]);

    await showRule({ uri: URI, range: RULE }, log());

    expect(shownDocuments).toEqual([{ uri: URI, selection: new Range(15, 10, 15, 10) }]);
    expect(executedCommands).toEqual([
      CLOSE_PEEK,
      { command: 'editor.action.showHover', args: [] },
    ]);
  });

  it.each([
    ['severity', 'editor.action.showHover'],
    ['position', 'editor.action.marker.next'],
  ])(
    'with problems.sortOrder %s, a warning of the rule before an error further down runs %s at the warning',
    async (order, command) => {
      settings.set('problems.sortOrder', order);
      diagnostics.set(URI, [problem(15, 12, 1), problem(30, 10, 0)]);

      await showRule({ uri: URI, range: RULE }, log());

      expect(shownDocuments).toEqual([{ uri: URI, selection: new Range(15, 12, 15, 12) }]);
      expect(executedCommands).toEqual([CLOSE_PEEK, { command, args: [] }]);
    },
  );

  it('does nothing but log a warning when the argument is not a rule location', async () => {
    const channel = log();

    await showRule({ uri: URI }, channel);

    expect(shownDocuments).toEqual([]);
    expect(executedCommands).toEqual([]);
    expect(channel.lines).toEqual([
      'cel-lens.showRule was given no rule location; the engine may not match this extension.',
    ]);
  });

  it('logs, and does not throw, when the editor command fails', async () => {
    const channel = log();
    commandFailure.error = new Error('no editor');

    await showRule({ uri: URI, range: RULE }, channel);

    expect(channel.lines).toEqual([`Could not show the rule in ${URI}: Error: no editor`]);
  });
});

describe('listValues', () => {
  afterEach(reset);

  const port = { path: 'spec.port', value: 'self.spec.port' };
  const config = { path: 'spec.config', value: 'self.spec.config', type: '{name: string}' };
  const parts = [
    {
      title: 'self.spec.port: dyn · Give it a type',
      command: 'cel-lens.giveType',
      arguments: [port],
    },
    { title: 'self.spec.config: {name} · Edit', command: 'cel-lens.editType', arguments: [config] },
    {
      title: 'self.spec.config: {name} · Remove',
      command: 'cel-lens.removeType',
      arguments: [config],
    },
  ];

  it('lists the parts left out, titled after the rule, each value as the label and its action as the description', async () => {
    await listValues({ line: 16, parts }, log());

    expect(quickPicks.options).toEqual([
      { title: 'More of the rule on line 16', placeHolder: 'Pick a value and what to do with it' },
    ]);
    expect(quickPicks.shown[0]).toMatchObject([
      { label: 'self.spec.port: dyn', description: 'Give it a type' },
      { label: 'self.spec.config: {name}', description: 'Edit' },
      { label: 'self.spec.config: {name}', description: 'Remove' },
    ]);
    expect(executedCommands).toEqual([]);
  });

  it('runs the command of the part picked, with its arguments', async () => {
    quickPicks.next = 'self.spec.port: dyn';

    await listValues({ line: 16, parts }, log());

    expect(executedCommands).toEqual([{ command: 'cel-lens.giveType', args: [port] }]);
  });

  it('runs nothing when the quick pick is closed with Escape', async () => {
    await listValues({ line: 16, parts }, log());

    expect(quickPicks.shown).toHaveLength(1);
    expect(executedCommands).toEqual([]);
  });

  it('runs nothing for a part that only says something', async () => {
    quickPicks.next = 'only words';

    await listValues({ line: 16, parts: [{ title: 'only words', command: '' }] }, log());

    expect(executedCommands).toEqual([]);
  });

  it('shows nothing and logs a warning when the argument holds no parts', async () => {
    const channel = log();

    await listValues(parts, channel);

    expect(quickPicks.shown).toEqual([]);
    expect(channel.lines).toEqual([
      'cel-lens.listValues was given no values; the engine may not match this extension.',
    ]);
  });

  it('says once that the picked command failed, logs why, and shows the logs from the button', async () => {
    const channel = log();
    quickPicks.next = 'self.spec.port: dyn';
    commandFailure.error = new Error("command 'cel-lens.giveType' not found");
    errorChoice.next = 'Show Logs';

    await listValues({ line: 16, parts }, channel);

    expect(shownErrors).toEqual([
      {
        message:
          'cel-lens could not run "self.spec.port: dyn · Give it a type". Show the logs for the reason.',
        buttons: ['Show Logs'],
      },
    ]);
    expect(channel.lines).toEqual([
      "Could not run cel-lens.giveType: Error: command 'cel-lens.giveType' not found",
    ]);
    expect(channel.shown).toBe(true);
  });
});

describe('ruleArgument', () => {
  afterEach(reset);

  const LOCATION = { uri: URI, range: RULE };

  it('leaves a location as it is, from the line or the view, and looks nothing up', () => {
    const atCursor = vi.fn(() => undefined);

    expect(ruleArgument(LOCATION, atCursor)).toBe(LOCATION);
    expect(atCursor).not.toHaveBeenCalled();
    expect(statusBarMessages).toEqual([]);
  });

  it('looks up the cursor of the editor of the URI it is given, not the active one (review of #48)', () => {
    const asked: (string | undefined)[] = [];

    ruleArgument(Uri.parse(URI), (uri) => {
      asked.push(uri);
      return LOCATION;
    });
    ruleArgument(undefined, (uri) => {
      asked.push(uri);
      return LOCATION;
    });

    expect(asked).toEqual([URI, undefined]);
  });

  it.each([
    ['an editor URI, from a menu', Uri.parse(URI)],
    ['nothing, from the palette', undefined],
  ])('looks up the rule at the cursor when it is given %s', (_name, argument) => {
    expect(ruleArgument(argument, () => LOCATION)).toBe(LOCATION);
    expect(statusBarMessages).toEqual([]);
  });

  it.each([
    ['an editor URI', Uri.parse(URI)],
    ['nothing', undefined],
  ])(
    'says in the status bar for 3 seconds that the cursor is in no rule, given %s',
    (_name, argument) => {
      expect(ruleArgument(argument, () => undefined)).toBeUndefined();
      expect(statusBarMessages).toEqual([
        { text: 'cel-lens: the cursor is in no rule', hideAfterMs: 3000 },
      ]);
    },
  );
});
