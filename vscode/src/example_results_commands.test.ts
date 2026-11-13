import { afterEach, describe, expect, it } from 'vitest';

import {
  FakeEditor,
  Range,
  Uri,
  ViewColumn,
  ConfigurationTarget,
  addWorkspaceFolder,
  infoChoice,
  quickPicks,
  reset,
  settingUpdates,
  settings,
  shownDocuments,
  shownErrors,
  shownInfoButtons,
  shownInfos,
  showFailure,
  visibleEditors,
  window,
} from '../test/vscode';
import { EXAMPLE_RESULTS_OFF_MESSAGE, NO_RESULTS_LINE } from './example_results';
import { resultsUi } from './example_results_commands';
import { ExampleResultsFlow } from './example_results_flow';

const RULE = {
  uri: 'file:///workspace/repo/crd.yaml',
  range: { start: { line: 15, character: 30 }, end: { line: 15, character: 52 } },
};

const SMALL = 'file:///workspace/repo/config/samples/small.yaml';
const BIG = 'file:///workspace/repo/config/samples/big.yaml';
const FINE = 'file:///workspace/repo/testdata/invalid/fine.yaml';

function verdict(
  uri: string,
  file: string,
  line: number,
  over: Record<string, unknown>,
): Record<string, unknown> {
  return {
    example: { uri, file, line, operation: 'create', expect: 'allowed' },
    result: 'allowed',
    expected: true,
    ...over,
  };
}

const ANSWER = {
  kubernetesVersion: '1.37',
  rules: [
    {
      location: RULE,
      results: [
        verdict(BIG, 'config/samples/big.yaml', 3, {
          result: 'refused',
          refusals: [
            { path: 'spec.replicas', message: 'spec.replicas: Invalid value: 20: at most 10' },
          ],
          expected: false,
          wrong: 'rule',
        }),
        verdict(SMALL, 'config/samples/small.yaml', 1, {}),
        verdict(FINE, 'testdata/invalid/fine.yaml', 4, {
          example: {
            uri: FINE,
            file: 'testdata/invalid/fine.yaml',
            line: 4,
            operation: 'create',
            expect: 'refused',
          },
          expected: false,
          wrong: 'example',
        }),
      ],
    },
  ],
};

function setUp(answer: () => Promise<unknown> = () => Promise.resolve(ANSWER)): {
  log: ReturnType<typeof window.createOutputChannel>;
  flow: ExampleResultsFlow;
  asked: { command: string; argument: unknown }[];
} {
  const log = window.createOutputChannel('cel-lens');
  const asked: { command: string; argument: unknown }[] = [];
  const flow = new ExampleResultsFlow(
    (command, argument) => {
      asked.push({ command, argument });
      return answer();
    },
    resultsUi(log),
    log,
  );
  return { log, flow, asked };
}

describe('the example results of a rule, against the VS Code stand-in', () => {
  afterEach(reset);

  it('shows the picker at once, busy and saying the results are being computed, titled after the rule and matching what is typed on the file and the message too', async () => {
    const { flow } = setUp();

    await flow.show(RULE);

    expect(quickPicks.created).toEqual([
      {
        title: 'Example results of the rule on line 16',
        placeholder: 'Pick an example to open it at its line',
        matchOnDescription: true,
        matchOnDetail: true,
        busy: true,
        buttons: [],
        value: '',
        labels: ['$(sync~spin) Computing the example results…'],
      },
    ]);
  });

  it('lists the wrong verdicts first, each with its verdict, its file and line, and the API server’s message', async () => {
    const { flow, asked } = setUp();

    await flow.show(RULE);

    expect(asked).toEqual([{ command: 'cel-lens.exampleResults', argument: RULE }]);
    expect(quickPicks.shown[0]).toEqual([
      { label: 'Not as expected', kind: -1 },
      {
        label: '$(warning) config/samples/big.yaml:3',
        description: 'refused · expected allowed',
        detail: 'spec.replicas: Invalid value: 20: at most 10',
        value: { action: 'open', uri: BIG, file: 'config/samples/big.yaml', line: 3 },
      },
      {
        label: '$(warning) testdata/invalid/fine.yaml:4',
        description: 'allowed · expected refused',
        value: { action: 'open', uri: FINE, file: 'testdata/invalid/fine.yaml', line: 4 },
      },
      { label: 'As expected', kind: -1 },
      {
        label: '$(pass) config/samples/small.yaml:1',
        description: 'allowed',
        value: { action: 'open', uri: SMALL, file: 'config/samples/small.yaml', line: 1 },
      },
    ]);
  });

  it('opens the example picked at its line, beside the rule, with the focus there', async () => {
    const { flow } = setUp();
    quickPicks.next = '$(warning) config/samples/big.yaml:3';

    await flow.show(RULE);

    expect(shownDocuments).toEqual([
      {
        uri: BIG,
        selection: new Range(2, 0, 2, 0),
        viewColumn: ViewColumn.Beside,
        preserveFocus: false,
        preview: false,
      },
    ]);
  });

  it('opens the example in the editor group that already shows it, so the rule stays in view and no third group opens', async () => {
    const { flow } = setUp();
    for (const [uri, column] of [
      [RULE.uri, ViewColumn.One],
      [BIG, ViewColumn.Two],
    ] as const) {
      const editor = new FakeEditor({ languageId: 'yaml', getText: () => '', uri: Uri.parse(uri) });
      editor.viewColumn = column;
      visibleEditors.push(editor);
    }
    quickPicks.next = '$(warning) config/samples/big.yaml:3';

    await flow.show(RULE);

    expect(shownDocuments).toMatchObject([{ uri: BIG, viewColumn: ViewColumn.Two }]);
  });

  it('says once that the example could not be opened, with a button to the logs, and logs why', async () => {
    const { flow, log } = setUp();
    quickPicks.next = '$(warning) config/samples/big.yaml:3';
    showFailure.error = new Error('file is gone');

    await flow.show(RULE);

    expect(shownErrors).toEqual([
      {
        message: 'cel-lens could not open config/samples/big.yaml. Show the logs for the reason.',
        buttons: ['Show Logs'],
      },
    ]);
    expect(log.lines).toEqual([`Could not open ${BIG}: Error: file is gone`]);
  });

  it('says the results are being computed when the engine has none for the rule yet', async () => {
    const { flow } = setUp(() =>
      Promise.resolve({ kubernetesVersion: '1.37', rules: [{ location: RULE, results: [] }] }),
    );

    await flow.show(RULE);

    expect(quickPicks.shown[0]).toMatchObject([
      {
        label: '$(info) No example results yet',
        detail: NO_RESULTS_LINE.detail,
      },
    ]);
  });

  it('says the rule changed, and to click its part again, when the engine has no rule where the part pointed (#13)', async () => {
    const { flow } = setUp(() => Promise.resolve({ kubernetesVersion: '1.37', rules: [] }));

    await flow.show(RULE);

    expect(quickPicks.shown[0]).toMatchObject([
      { label: '$(info) The rule changed', detail: 'Click its part above the rule again' },
    ]);
  });

  describe('with the setting off', () => {
    it('says so in one quiet message with a button that turns it on, and asks the engine nothing', async () => {
      settings.set('cel-lens.exampleResults', false);
      const { flow, asked } = setUp();

      await flow.show(RULE);

      expect(shownInfos).toEqual([EXAMPLE_RESULTS_OFF_MESSAGE]);
      expect(shownInfoButtons).toEqual([['Turn On']]);
      expect(asked).toEqual([]);
      expect(quickPicks.created).toEqual([]);
      expect(settingUpdates).toEqual([]);
    });

    it('turns the setting on, in the scope that set it, from the button', async () => {
      settings.set('cel-lens.exampleResults', false);
      infoChoice.next = 'Turn On';
      const { flow } = setUp();

      await flow.show(RULE);

      expect(settingUpdates).toEqual([
        { name: 'cel-lens.exampleResults', value: true, target: ConfigurationTarget.Global },
      ]);
    });

    it.each([
      ['a', true],
      ['b', false],
    ])(
      'follows the workspace folder of the rule: a rule in folder %s offers to turn them on: %s',
      async (folder, offered) => {
        addWorkspaceFolder('a', { 'cel-lens.exampleResults': false });
        addWorkspaceFolder('b');
        const { flow, asked } = setUp();

        await flow.show({ ...RULE, uri: `file:///workspace/${folder}/crd.yaml` });

        expect(shownInfos).toEqual(offered ? [EXAMPLE_RESULTS_OFF_MESSAGE] : []);
        expect(asked).toHaveLength(offered ? 0 : 1);
      },
    );
  });
});
