import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  activeEditor,
  commandFailure,
  commandResults,
  errorChoice,
  executedCommands,
  inputBoxes,
  quickPicks,
  reset,
  settings,
  settle,
  shownErrors,
  shownInfos,
  ThemeIcon,
  window,
} from '../test/vscode';
import { OPEN_YAML_MESSAGE, giveValueType, typeUi } from './type_commands';
import { INLINE_INFO_OFF_MESSAGE, NO_VALUES_MESSAGE, TypeFlow } from './type_flow';
import { CHECK_DELAY_MS } from './type_input';
import { TYPE_PROMPT } from './value_types';

const CELLENS = 'file:///ws/.cellens';

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
};

const WAITING =
  'an edit of .cellens is waiting for the editor: run the command again once it is applied';

const MISSPELT = { message: 'unknown type "strin": use string, int, ...', column: 8 };

/** The engine, faked: `cel-lens.checkType` finds the misspelt `strin`, and every edit is applied. */
function fakeEngine(): {
  calls: { command: string; argument: unknown; signal: AbortSignal | undefined }[];
  run: (command: string, argument?: unknown, signal?: AbortSignal) => Promise<unknown>;
} {
  const calls: { command: string; argument: unknown; signal: AbortSignal | undefined }[] = [];
  return {
    calls,
    run: (command, argument, signal) => {
      calls.push({ command, argument, signal });
      switch (command) {
        case 'cel-lens.typeChoices':
          return Promise.resolve(CHOICES);
        case 'cel-lens.checkType':
          return Promise.resolve(JSON.stringify(argument).includes('strin}') ? MISSPELT : null);
        default:
          return Promise.resolve({ cellens: CELLENS, change: 'added' });
      }
    },
  };
}

function setUp(): {
  engine: ReturnType<typeof fakeEngine>;
  log: ReturnType<typeof window.createOutputChannel>;
  flow: TypeFlow;
  ui: ReturnType<typeof typeUi>;
} {
  const engine = fakeEngine();
  const log = window.createOutputChannel('cel-lens');
  const ui = typeUi(log);
  return { engine, log, ui, flow: new TypeFlow(engine.run, ui, log) };
}

/** Lets the chained promises of a step run: the engine's answer, then what is shown with it. */
async function flush(): Promise<void> {
  await settle();
  await settle();
}

describe('Give it a type, against the VS Code stand-in', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    reset();
  });

  it('shows the picker titled after the value, the kinds told apart by icon, with group and version as the description, found by what is typed in descriptions and details too', async () => {
    const { flow } = setUp();

    await flow.giveType(CONFIG);

    expect(quickPicks.created).toEqual([
      {
        title: 'Give params.spec.config a type',
        placeholder: 'Write a struct, or type to find a kind such as Deployment',
        matchOnDescription: true,
        matchOnDetail: true,
        busy: true,
        labels: ['$(edit) Write a struct…'],
      },
    ]);
    expect(quickPicks.shown[0]).toEqual([
      {
        label: '$(edit) Write a struct…',
        detail:
          'Its fields and their types, such as {name: string, replicas: int}, or any CEL type',
        alwaysShow: true,
        value: { action: 'write' },
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

  it('shows the picker at once, busy with Write a struct…, before the engine lists the kinds', async () => {
    const engine = fakeEngine();
    let answer: (choices: unknown) => void = () => undefined;
    const run = (command: string, argument?: unknown, signal?: AbortSignal): Promise<unknown> =>
      command === 'cel-lens.typeChoices'
        ? new Promise((resolve) => {
            answer = resolve;
          })
        : engine.run(command, argument, signal);
    const log = window.createOutputChannel('cel-lens');
    quickPicks.next = '$(symbol-class) Deployment';

    const giving = new TypeFlow(run, typeUi(log), log).giveType(CONFIG);
    await flush();

    expect(quickPicks.created).toMatchObject([
      { title: 'Give params.spec.config a type', busy: true, labels: ['$(edit) Write a struct…'] },
    ]);
    expect(quickPicks.shown).toEqual([]);

    answer(CHOICES);
    await giving;

    expect(quickPicks.shown[0]).toHaveLength(6);
    expect(engine.calls.at(-1)).toMatchObject({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, kind: 'apps/v1 Deployment' },
    });
  });

  it('starts an edit on the kind the value has', async () => {
    const { flow } = setUp();

    await flow.editType({ ...CONFIG, kind: 'example.com/v1 Widget' });

    expect(quickPicks.active[0]).toMatchObject([{ label: '$(file-code) Widget' }]);
  });

  it('writes a struct: the input box checks it as it is typed, and Enter on a type that is fine sets it', async () => {
    const { flow, engine } = setUp();
    quickPicks.next = '$(edit) Write a struct…';

    const giving = flow.giveType(CONFIG);
    await flush();
    const box = inputBoxes[0];
    expect(box).toMatchObject({
      title: 'Give params.spec.config a type',
      placeholder: '{name: string, replicas: int}',
      prompt: TYPE_PROMPT,
      value: '',
      visible: true,
    });

    box?.type('{name: strin}');
    await flush();
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
    vi.advanceTimersByTime(CHECK_DELAY_MS);
    await flush();
    expect(engine.calls.at(-1)).toMatchObject({
      command: 'cel-lens.checkType',
      argument: { ...CONFIG, type: '{name: strin}' },
    });
    expect(box?.validationMessage).toBe('unknown type "strin": use string, int, ... (character 8)');

    box?.accept();
    await flush();
    expect(engine.calls.map((call) => call.command)).not.toContain('cel-lens.setType');
    expect(box?.visible).toBe(true);

    box?.type('{name: string}');
    vi.advanceTimersByTime(CHECK_DELAY_MS);
    await flush();
    expect(box?.validationMessage).toBeUndefined();
    box?.accept();
    await giving;

    expect(engine.calls.at(-1)).toEqual({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, type: '{name: string}' },
      signal: undefined,
    });
    expect(box).toMatchObject({ visible: false, disposed: true });
  });

  it('starts the input box with a struct typed in the filter, where Write a struct… stays the line Enter picks', async () => {
    const { flow } = setUp();
    quickPicks.typed = '{name: string}';
    quickPicks.next = '$(edit) Write a struct…';

    const giving = flow.giveType(CONFIG);
    await flush();

    expect(inputBoxes[0]?.value).toBe('{name: string}');
    inputBoxes[0]?.hide();
    await giving;
  });

  it('turns from the input box to the picker with its Pick a kind… button, so a struct can become a kind', async () => {
    const { flow, engine } = setUp();
    quickPicks.next = '$(symbol-class) Deployment';

    const editing = flow.editType({ ...CONFIG, type: '{name: string}' });
    await flush();
    const box = inputBoxes[0];
    expect(box?.buttons).toEqual([
      { iconPath: new ThemeIcon('symbol-class'), tooltip: 'Pick a kind…' },
    ]);
    box?.press('Pick a kind…');
    await editing;

    expect(box?.visible).toBe(false);
    expect(quickPicks.created.map((pick) => pick.title)).toEqual([
      'Edit the type of params.spec.config',
    ]);
    expect(engine.calls.at(-1)).toMatchObject({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, kind: 'apps/v1 Deployment' },
    });
  });

  it('cancels the check of what was typed before, as the person types on', async () => {
    const { flow, engine } = setUp();
    quickPicks.next = '$(edit) Write a struct…';
    const giving = flow.giveType(CONFIG);
    await flush();
    const box = inputBoxes[0];

    box?.type('{name: strin}');
    vi.advanceTimersByTime(CHECK_DELAY_MS);
    const first = engine.calls.at(-1)?.signal;
    box?.type('{name: string}');

    expect(first?.aborted).toBe(true);
    box?.hide();
    await giving;
  });

  it('says once why a type was not set when the box was closed while the engine set it', async () => {
    const engine = fakeEngine();
    let refuse: (error: Error) => void = () => undefined;
    const run = (command: string, argument?: unknown, signal?: AbortSignal): Promise<unknown> =>
      command === 'cel-lens.setType'
        ? new Promise((_resolve, reject) => {
            refuse = reject;
          })
        : engine.run(command, argument, signal);
    const log = window.createOutputChannel('cel-lens');
    const flow = new TypeFlow(run, typeUi(log), log);
    quickPicks.next = '$(edit) Write a struct…';
    const giving = flow.giveType(CONFIG);
    await flush();
    const box = inputBoxes[0];
    box?.type('{name: string}');
    vi.advanceTimersByTime(CHECK_DELAY_MS);
    await flush();

    box?.accept();
    await flush();
    box?.hide();
    await flush();
    refuse(Object.assign(new Error(WAITING), { code: -32803 }));
    await giving;

    expect(shownErrors).toEqual([
      { message: `cel-lens could not give params.spec.config a type: ${WAITING}.`, buttons: [] },
    ]);
  });

  it('changes nothing on Escape in the input box', async () => {
    const { flow, engine } = setUp();
    quickPicks.next = '$(edit) Write a struct…';
    const giving = flow.giveType(CONFIG);
    await flush();

    inputBoxes[0]?.type('{name: string}');
    inputBoxes[0]?.hide();
    vi.advanceTimersByTime(CHECK_DELAY_MS);
    await giving;

    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
    expect(inputBoxes[0]?.disposed).toBe(true);
  });

  it('opens an edit of a type in the input box filled with it, and checks it', async () => {
    const { flow, engine } = setUp();

    const editing = flow.editType({ ...CONFIG, type: '{name: strin}' });
    await flush();
    vi.advanceTimersByTime(CHECK_DELAY_MS);
    await flush();

    expect(inputBoxes[0]).toMatchObject({
      title: 'Edit the type of params.spec.config',
      value: '{name: strin}',
      validationMessage: 'unknown type "strin": use string, int, ... (character 8)',
    });
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.checkType']);
    inputBoxes[0]?.hide();
    await editing;
  });
});

describe('typeUi', () => {
  afterEach(reset);

  it('shows an error with Show Logs, and opens the logs from it', async () => {
    const log = window.createOutputChannel('cel-lens');
    errorChoice.next = 'Show Logs';

    await typeUi(log).showError('cel-lens could not remove the type.', true);

    expect(shownErrors).toEqual([
      { message: 'cel-lens could not remove the type.', buttons: ['Show Logs'] },
    ]);
    expect(log.shown).toBe(true);
  });

  it('shows the reason a refusal gives with no button', async () => {
    await typeUi(window.createOutputChannel('cel-lens')).showError('Run it again.', false);

    expect(shownErrors).toEqual([{ message: 'Run it again.', buttons: [] }]);
  });

  it('opens the cel-lens.experimental setting', async () => {
    await typeUi(window.createOutputChannel('cel-lens')).openExperimentalSetting();

    expect(executedCommands).toEqual([
      { command: 'workbench.action.openSettings', args: ['cel-lens.experimental'] },
    ]);
  });
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
