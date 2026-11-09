import { describe, expect, it } from 'vitest';

import {
  BACK,
  INLINE_INFO_OFF_MESSAGE,
  NO_VALUES_MESSAGE,
  TypeFlow,
  type PickRequest,
  type Picked,
  type TypeUi,
} from './type_flow';
import {
  GO_TYPE_PLACEHOLDER,
  INT_OR_STRING_PLACEHOLDER,
  KIND_PLACEHOLDER,
  type PickEntry,
  type PickLine,
  type TextRange,
} from './value_types';

const CELLENS = 'file:///ws/.cellens';

/** The argument of the line's parts for `params.spec.config` of a policy's params. */
const CONFIG = {
  cellens: CELLENS,
  file: 'policies/limits.yaml',
  document: 'ValidatingAdmissionPolicy limits',
  variable: 'params',
  path: 'spec.config',
  value: 'params.spec.config',
};

/** The argument of the line's parts for an `x-kubernetes-int-or-string` value of a CRD. */
const SURGE = {
  cellens: CELLENS,
  file: 'deploy/crd.yaml',
  document: 'CustomResourceDefinition widgets.example.com',
  version: 'v1',
  path: 'spec.maxSurge',
  value: 'self.spec.maxSurge',
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
  crdsNote: undefined,
  goTypes: [
    { folder: 'file:///ws/api/v1', name: 'QuotaSpec', doc: 'QuotaSpec is the quota of a team.' },
    { folder: 'file:///ws/api/v1', name: 'TeamSpec', doc: '' },
  ],
};

/** Where the engine puts the cursor in `.cellens`: between the braces of `type: "{}"`. */
const SELECTION: TextRange = {
  start: { line: 9, character: 11 },
  end: { line: 9, character: 11 },
};

/** What the #17 engine, with setType but no snippet, refuses a request with no type with. */
const NO_TYPE =
  'cel-lens.setType: give the type, such as type: "{name: string}", the kind, such as kind: "apps/v1 Deployment", the schema, such as schema: {"type": "object", "properties": {...}}, or the Go type, such as go: "./api/v1#QuotaSpec"';

const WAITING =
  'an edit of .cellens is waiting for the editor: run the command again once it is applied';

const SCHEMA_LINE = '$(edit) Write a schema in .cellens…';
const GO_LINE = '$(symbol-struct) Use a Go type…';
const SHORT_LINE = '$(edit) Write a short type in .cellens…';
/** What FakeUi.next takes for a click on a picker's Back button. */
const PRESS_BACK = 'the Back button';

/** A ResponseError as the language client rejects with it. */
function responseError(code: number, message: string): Error {
  return Object.assign(new Error(message), { code });
}

interface Call {
  readonly command: string;
  readonly argument: unknown;
}

/**
 * The engine, faked: answers each command from `answers`, or rejects with `failures`; a setType
 * with a snippet answers where the cursor goes. The signal of each command is kept.
 */
function fakeEngine(): {
  calls: Call[];
  signals: (AbortSignal | undefined)[];
  answers: Map<string, unknown>;
  failures: Map<string, Error>;
  run: (command: string, argument?: unknown, signal?: AbortSignal) => Promise<unknown>;
} {
  const calls: Call[] = [];
  const signals: (AbortSignal | undefined)[] = [];
  const answers = new Map<string, unknown>([
    ['cel-lens.typeChoices', CHOICES],
    ['cel-lens.setType', { cellens: CELLENS, change: 'added' }],
    ['cel-lens.removeType', { cellens: CELLENS, change: 'removed' }],
  ]);
  const failures = new Map<string, Error>();
  return {
    calls,
    signals,
    answers,
    failures,
    run: (command, argument, signal) => {
      calls.push({ command, argument });
      signals.push(signal);
      const failure = failures.get(command);
      if (failure !== undefined) {
        return Promise.reject(failure);
      }
      const snippet = typeof argument === 'object' && argument !== null && 'snippet' in argument;
      return Promise.resolve(
        snippet && !answers.has('snippet')
          ? { cellens: CELLENS, change: 'added', selection: SELECTION }
          : answers.get(snippet ? 'snippet' : command),
      );
    },
  };
}

/** A picker as it was shown: its lines once they came, and the one it started on. */
interface ShownPick {
  readonly title: string;
  readonly placeholder: string;
  readonly filter: string | undefined;
  readonly back: true | undefined;
  readonly first: readonly PickLine<unknown>[] | undefined;
  readonly lines: readonly PickLine<unknown>[];
  readonly active: PickEntry<unknown> | undefined;
}

/** The quick picks, faked: each pick takes the line labelled `next`'s next, and types `filters`'s next. */
class FakeUi implements TypeUi {
  readonly picks: ShownPick[] = [];
  readonly opened: { cellens: string; selection: TextRange }[] = [];
  readonly infos: string[] = [];
  readonly errors: { message: string; offerLogs: boolean }[] = [];
  readonly next: (string | undefined)[] = [];
  /** What is typed in the filter of each picker before its line is picked. */
  readonly filters: string[] = [];
  openFailure: Error | undefined;
  settingsOpened = 0;
  serverPathOpened = 0;
  outdatedShown = 0;
  /** How many of the next pickers pick from their first lines, before the engine answers. */
  atOnce = 0;
  logsShown = 0;
  /** The workspace folders, by URI; a URI below none is outside every one. */
  folders = ['file:///ws'];

  async pick<T>(request: PickRequest<T>): Promise<Picked<T> | typeof BACK | undefined> {
    const early = this.atOnce > 0 && request.lines instanceof Promise;
    this.atOnce -= early ? 1 : 0;
    const lines = early
      ? (request.first ?? [])
      : request.lines instanceof Promise
        ? await request.lines
        : request.lines;
    const entries = lines.filter((line): line is PickEntry<T> => line.separator !== true);
    this.picks.push({
      title: request.title,
      placeholder: request.placeholder,
      filter: request.filter,
      back: request.back,
      first: request.first,
      lines,
      active: entries.find((line) => request.active?.(line.value) === true),
    });
    const label = this.next.shift();
    if (label === PRESS_BACK && request.back === true) {
      return BACK;
    }
    const line = entries.find((each) => each.label === label);
    return line === undefined
      ? undefined
      : { value: line.value, typed: this.filters.shift() ?? '' };
  }

  openCellens(cellens: string, selection: TextRange): Promise<void> {
    if (this.openFailure !== undefined) {
      return Promise.reject(this.openFailure);
    }
    this.opened.push({ cellens, selection });
    return Promise.resolve();
  }

  workspacePath(uri: string): string {
    return uri.replace('file:///ws/', '');
  }

  workspaceFolder(uri: string): string | undefined {
    return this.folders.find((folder) => uri.startsWith(`${folder}/`));
  }

  openServerPathSetting(): Promise<void> {
    this.serverPathOpened += 1;
    return Promise.resolve();
  }

  showOutdatedEngine(): Promise<void> {
    this.outdatedShown += 1;
    return Promise.resolve();
  }

  showLogs(): void {
    this.logsShown += 1;
  }

  inform(message: string): Promise<void> {
    this.infos.push(message);
    return Promise.resolve();
  }

  showError(message: string, offerLogs: boolean): Promise<void> {
    this.errors.push({ message, offerLogs });
    return Promise.resolve();
  }

  openExperimentalSetting(): Promise<void> {
    this.settingsOpened += 1;
    return Promise.resolve();
  }
}

function setUp(): {
  engine: ReturnType<typeof fakeEngine>;
  ui: FakeUi;
  logs: string[];
  flow: TypeFlow;
} {
  const engine = fakeEngine();
  const ui = new FakeUi();
  const logs: string[] = [];
  const log = {
    warn: (message: string) => logs.push(message),
    error: (message: string) => logs.push(message),
  };
  return { engine, ui, logs, flow: new TypeFlow(engine.run, ui, log) };
}

function labels(pick: ShownPick | undefined): string[] | undefined {
  return pick?.lines.map((line) => line.label);
}

describe('giveType', () => {
  it('opens the picker titled after the value: a schema, a Go type and a short type, in that order, then the built-in kinds and the workspace CRDs', async () => {
    const { flow, ui, engine } = setUp();

    await flow.giveType(CONFIG);

    expect(engine.calls).toEqual([{ command: 'cel-lens.typeChoices', argument: undefined }]);
    expect(ui.picks).toHaveLength(1);
    expect(ui.picks[0]).toMatchObject({
      title: 'Give params.spec.config a type',
      placeholder: KIND_PLACEHOLDER,
    });
    expect(labels(ui.picks[0])).toEqual([
      SCHEMA_LINE,
      GO_LINE,
      SHORT_LINE,
      'Built-in kinds · Kubernetes 1.37',
      '$(symbol-class) Deployment',
      'Workspace CRDs',
      '$(file-code) Widget',
    ]);
    expect(ui.picks[0]?.first?.map((line) => line.label)).toEqual([
      SCHEMA_LINE,
      GO_LINE,
      SHORT_LINE,
    ]);
    expect(ui.picks[0]?.active).toBeUndefined();
  });

  it('sets the kind picked at once, with no type', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push('$(symbol-class) Deployment');

    await flow.giveType(CONFIG);

    expect(engine.calls.at(-1)).toEqual({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, kind: 'apps/v1 Deployment' },
    });
    expect(ui.opened).toEqual([]);
    expect(ui.errors).toEqual([]);
  });

  it.each([
    ['a schema', SCHEMA_LINE, 'schema', CONFIG],
    ['a short type', SHORT_LINE, 'type', CONFIG],
  ])(
    'has the engine write the entry of %s as a snippet, then opens .cellens at its first placeholder',
    async (_name, line, snippet, value) => {
      const { flow, ui, engine } = setUp();
      ui.next.push(line);

      await flow.giveType(value);

      expect(engine.calls.at(-1)).toEqual({
        command: 'cel-lens.setType',
        argument: { ...value, snippet },
      });
      expect(ui.opened).toEqual([{ cellens: CELLENS, selection: SELECTION }]);
      expect(ui.errors).toEqual([]);
    },
  );

  it('opens the .cellens the engine names, which may not be the one the line named', async () => {
    const { flow, ui, engine } = setUp();
    const nearer = 'file:///ws/policies/.cellens';
    engine.answers.set('snippet', { cellens: nearer, change: 'created', selection: SELECTION });
    ui.next.push(SHORT_LINE);

    await flow.giveType(CONFIG);

    expect(ui.opened).toEqual([{ cellens: nearer, selection: SELECTION }]);
  });

  it('turns from Use a Go type… to the Go types of the workspace, filtered by what was typed, each with its folder and doc comment', async () => {
    const { flow, ui } = setUp();
    ui.next.push(GO_LINE);
    ui.filters.push('quota');

    await flow.giveType(CONFIG);

    expect(ui.picks[1]).toMatchObject({
      title: 'Give params.spec.config a type › Go type',
      placeholder: GO_TYPE_PLACEHOLDER,
      filter: 'quota',
      lines: [
        {
          label: '$(symbol-struct) QuotaSpec',
          description: 'api/v1',
          detail: 'QuotaSpec is the quota of a team.',
        },
        { label: '$(symbol-struct) TeamSpec', description: 'api/v1' },
      ],
    });
  });

  it('sets the Go type picked at once, its folder relative to the folder of the .cellens', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push(GO_LINE, '$(symbol-struct) QuotaSpec');

    await flow.giveType({ ...CONFIG, cellens: 'file:///ws/policies/.cellens' });

    expect(engine.calls).toEqual([
      { command: 'cel-lens.typeChoices', argument: undefined },
      {
        command: 'cel-lens.setType',
        argument: {
          ...CONFIG,
          cellens: 'file:///ws/policies/.cellens',
          go: '../api/v1#QuotaSpec',
        },
      },
    ]);
    expect(ui.opened).toEqual([]);
  });

  it('asks the engine for the kinds and Go types once, for both pickers', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push(GO_LINE);

    await flow.giveType(CONFIG);

    expect(ui.picks).toHaveLength(2);
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
  });

  it.each([
    ['the picker', []],
    ['the Go types picker', [GO_LINE]],
  ])('changes nothing when %s is closed with Escape', async (_name, picked) => {
    const { flow, ui, engine } = setUp();
    ui.next.push(...picked);

    await flow.giveType(CONFIG);

    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
    expect(ui.opened).toEqual([]);
  });

  it('cancels the list of kinds and Go types once the flow ends, so an engine still walking the Go code stops for it', async () => {
    const { flow, engine } = setUp();

    await flow.giveType(CONFIG);

    expect(engine.signals[0]?.aborted).toBe(true);
  });

  it('offers only int and string for a value of a CRD, without asking for the kinds, and sets the one picked', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push('$(symbol-numeric) int');

    await flow.giveType(SURGE);

    expect(ui.picks[0]).toMatchObject({
      title: 'Give self.spec.maxSurge a type',
      placeholder: INT_OR_STRING_PLACEHOLDER,
    });
    expect(labels(ui.picks[0])).toEqual(['$(symbol-numeric) int', '$(symbol-string) string']);
    expect(engine.calls).toEqual([
      { command: 'cel-lens.setType', argument: { ...SURGE, type: 'int' } },
    ]);
  });

  it('opens the experimental setting from the line that says why no CRD is listed', async () => {
    const { flow, ui, engine } = setUp();
    engine.answers.set('cel-lens.typeChoices', {
      ...CHOICES,
      crds: [],
      crdsNote: 'only with crd-typed-policies on',
    });
    ui.next.push('$(info) Workspace CRDs are not listed');

    await flow.giveType(CONFIG);

    expect(ui.settingsOpened).toBe(1);
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
  });

  it('changes nothing from the line that says there is no Go type', async () => {
    const { flow, ui, engine } = setUp();
    engine.answers.set('cel-lens.typeChoices', { ...CHOICES, goTypes: [] });
    ui.next.push(GO_LINE, '$(info) No Go types in the workspace');

    await flow.giveType(CONFIG);

    expect(labels(ui.picks[1])).toEqual(['$(info) No Go types in the workspace']);
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
  });

  it('offers the three first lines alone, and logs, when the kinds are not in the form the engine documents', async () => {
    const { flow, ui, engine, logs } = setUp();
    engine.answers.set('cel-lens.typeChoices', { kinds: [] });

    await flow.giveType(CONFIG);

    expect(labels(ui.picks[0])).toEqual([SCHEMA_LINE, GO_LINE, SHORT_LINE]);
    expect(logs).toEqual([
      'cel-lens.typeChoices gave no kinds; the engine may not match this extension.',
    ]);
  });

  it('logs, and shows nothing, when the argument names no value', async () => {
    const { flow, ui, engine, logs } = setUp();

    await flow.giveType({ value: 'params.spec.config' });

    expect(ui.picks).toEqual([]);
    expect(engine.calls).toEqual([]);
    expect(logs).toEqual([
      'cel-lens.giveType was given no value; the engine may not match this extension.',
    ]);
  });
});

describe('the review of #8', () => {
  /** Two workspace folders, each with a Go type. */
  const TWO_FOLDERS = {
    ...CHOICES,
    goTypes: [
      { folder: 'file:///ws/charts/api', name: 'ChartSpec', doc: '' },
      { folder: 'file:///ws/operator/api/v1', name: 'QuotaSpec', doc: '' },
    ],
  };

  it('lists only the Go types of the workspace folder of the .cellens, and counts only those', async () => {
    const { flow, ui, engine } = setUp();
    ui.folders = ['file:///ws/charts', 'file:///ws/operator'];
    engine.answers.set('cel-lens.typeChoices', TWO_FOLDERS);
    ui.next.push(GO_LINE);

    await flow.giveType({ ...CONFIG, cellens: 'file:///ws/charts/.cellens' });

    expect(ui.picks[0]?.lines[1]).toMatchObject({ label: GO_LINE, description: '1 type' });
    expect(labels(ui.picks[1])).toEqual(['$(symbol-struct) ChartSpec']);
  });

  it('lists only the Go types below the folder of a .cellens outside every workspace folder', async () => {
    const { flow, ui, engine } = setUp();
    ui.folders = [];
    engine.answers.set('cel-lens.typeChoices', TWO_FOLDERS);
    ui.next.push(GO_LINE);

    await flow.giveType({ ...CONFIG, cellens: 'file:///ws/operator/.cellens' });

    expect(labels(ui.picks[1])).toEqual(['$(symbol-struct) QuotaSpec']);
  });

  it('says in the Go types picker that they could not be listed, and shows the logs from that line', async () => {
    const { flow, ui, engine } = setUp();
    engine.failures.set('cel-lens.typeChoices', new Error('the cel-lens engine is not running'));
    ui.next.push(GO_LINE, '$(warning) The Go types could not be listed');

    await flow.giveType(CONFIG);

    expect(ui.picks[1]?.lines).toEqual([
      {
        label: '$(warning) The Go types could not be listed',
        description: 'pick to show the logs',
        value: { action: 'showLogs' },
      },
    ]);
    expect(ui.logsShown).toBe(1);
  });

  it.each([
    ['without ./', 'api/v1#QuotaSpec'],
    ['with a trailing slash', './api/v1/#QuotaSpec'],
  ])(
    'starts an edit on the Go type the value has, written %s, and writes nothing when it is picked again',
    async (_name, go) => {
      const { flow, ui, engine } = setUp();
      ui.next.push(GO_LINE, '$(symbol-struct) QuotaSpec');

      await flow.editType({ ...CONFIG, go });

      expect(ui.picks[1]?.active?.label).toBe('$(symbol-struct) QuotaSpec');
      expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
    },
  );

  it('says on the lines that write in .cellens that they open the entry a value has', async () => {
    const { flow, ui } = setUp();

    await flow.editType({ ...CONFIG, kind: 'apps/v1 Deployment' });

    expect(ui.picks[0]?.lines[0]).toMatchObject({
      label: SCHEMA_LINE,
      detail:
        'Properties, descriptions and formats, as in a CRD; opens .cellens beside the rule at the entry it has, to write a schema there',
    });
    expect(ui.picks[0]?.lines[2]).toMatchObject({
      label: SHORT_LINE,
      detail:
        'Such as {name: string, replicas: int}; opens .cellens beside the rule at the entry it has, to write a short type there',
    });
  });

  it('says the engine is too old, and which setting fixes it, when the engine refuses a snippet, not its own reason', async () => {
    const { flow, ui, engine, logs } = setUp();
    engine.failures.set('cel-lens.setType', responseError(-32602, NO_TYPE));
    ui.next.push(SCHEMA_LINE);

    await flow.giveType(CONFIG);

    expect(ui.outdatedShown).toBe(1);
    expect(ui.errors).toEqual([]);
    expect(logs).toEqual([
      `Could not give params.spec.config a type, as the engine is too old: ${NO_TYPE.replace('cel-lens.setType: ', '')}`,
    ]);
  });

  it("shows another refusal of a snippet as it is, even one that names snippet (#8's re-review)", async () => {
    const { flow, ui, engine } = setUp();
    engine.failures.set(
      'cel-lens.setType',
      responseError(
        -32602,
        'cel-lens.setType: .cellens: types is not a list: fix it, then add the snippet',
      ),
    );
    ui.next.push(SCHEMA_LINE);

    await flow.giveType(CONFIG);

    expect(ui.outdatedShown).toBe(0);
    expect(ui.errors).toEqual([
      {
        message:
          'cel-lens could not give params.spec.config a type: .cellens: types is not a list: fix it, then add the snippet.',
        offerLogs: false,
      },
    ]);
  });

  it("writes in .cellens at once, without waiting for the engine's walk of the Go code (#8's re-review)", async () => {
    const { ui, engine, logs } = setUp();
    let answer: (choices: unknown) => void = () => undefined;
    const run = (command: string, argument?: unknown, signal?: AbortSignal): Promise<unknown> =>
      command === 'cel-lens.typeChoices'
        ? new Promise((resolve) => {
            answer = resolve;
          })
        : engine.run(command, argument, signal);
    const flow = new TypeFlow(run, ui, {
      warn: (message: string) => logs.push(message),
      error: (message: string) => logs.push(message),
    });
    ui.atOnce = 1;
    ui.next.push(SHORT_LINE);

    await flow.giveType(CONFIG);

    expect(engine.calls).toEqual([
      { command: 'cel-lens.setType', argument: { ...CONFIG, snippet: 'type' } },
    ]);
    expect(ui.opened).toEqual([{ cellens: CELLENS, selection: SELECTION }]);
    answer(CHOICES);
  });

  it('says the engine is too old without asking it to write, when it lists no Go types at all', async () => {
    const { flow, ui, engine } = setUp();
    engine.answers.set('cel-lens.typeChoices', { ...CHOICES, goTypes: undefined });
    ui.next.push(SHORT_LINE);

    await flow.giveType(CONFIG);

    expect(ui.outdatedShown).toBe(1);
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
  });

  it("says on Use a Go type… that it needs a newer engine, and says why when it is picked, with no Go types picker (#8's re-review)", async () => {
    const { flow, ui, engine } = setUp();
    engine.answers.set('cel-lens.typeChoices', { ...CHOICES, goTypes: undefined });
    ui.next.push(GO_LINE);

    await flow.giveType(CONFIG);

    expect(ui.picks[0]?.lines[1]).toMatchObject({
      label: GO_LINE,
      description: 'needs a newer engine',
    });
    expect(ui.picks).toHaveLength(1);
    expect(ui.outdatedShown).toBe(1);
  });

  it('says in the Go types picker, picked before the engine answered, that it is too old to list them, and opens cel-lens.serverPath from that line', async () => {
    const { ui, engine, logs } = setUp();
    let answer: (choices: unknown) => void = () => undefined;
    const run = (command: string, argument?: unknown, signal?: AbortSignal): Promise<unknown> =>
      command === 'cel-lens.typeChoices'
        ? new Promise((resolve) => {
            answer = resolve;
          })
        : engine.run(command, argument, signal);
    const flow = new TypeFlow(run, ui, {
      warn: (message: string) => logs.push(message),
      error: (message: string) => logs.push(message),
    });
    ui.atOnce = 1;
    ui.next.push(GO_LINE, '$(warning) This cel-lens engine cannot list Go types');

    const giving = flow.giveType(CONFIG);
    await Promise.resolve();
    answer({ ...CHOICES, goTypes: undefined });
    await giving;

    expect(ui.picks[1]?.lines).toEqual([
      {
        label: '$(warning) This cel-lens engine cannot list Go types',
        description: 'pick to open the setting',
        detail:
          'It is older than this extension: set cel-lens.serverPath to a newer cel-lens-core, then run cel-lens: Restart Engine',
        value: { action: 'openServerPathSetting' },
      },
    ]);
    expect(ui.serverPathOpened).toBe(1);
  });

  it("goes back from the Go types to the first picker with what was typed in it (#8's re-review)", async () => {
    const { flow, ui } = setUp();
    ui.filters.push('quota');
    ui.next.push(GO_LINE, PRESS_BACK);

    await flow.giveType(CONFIG);

    expect(ui.picks.map((pick) => pick.filter)).toEqual(['', 'quota', 'quota']);
  });

  it('titles the Go types picker as a step of the first, and goes back to the first with its Back button', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push(GO_LINE, PRESS_BACK, '$(symbol-class) Deployment');

    await flow.giveType(CONFIG);

    expect(ui.picks.map((pick) => [pick.title, pick.back])).toEqual([
      ['Give params.spec.config a type', undefined],
      ['Give params.spec.config a type › Go type', true],
      ['Give params.spec.config a type', undefined],
    ]);
    expect(engine.calls.map((call) => call.command)).toEqual([
      'cel-lens.typeChoices',
      'cel-lens.setType',
    ]);
  });
});

describe('writing in .cellens', () => {
  it('shows once why the engine did not write the entry, and opens nothing', async () => {
    const { flow, ui, engine } = setUp();
    engine.failures.set('cel-lens.setType', responseError(-32803, WAITING));
    ui.next.push(SCHEMA_LINE);

    await flow.giveType(CONFIG);

    expect(ui.opened).toEqual([]);
    expect(ui.errors).toEqual([
      {
        message: `cel-lens could not give params.spec.config a type: ${WAITING}.`,
        offerLogs: false,
      },
    ]);
  });

  it('logs, and opens nothing, when the engine gives no place to write the type', async () => {
    const { flow, ui, engine, logs } = setUp();
    engine.answers.set('snippet', { cellens: CELLENS, change: 'added' });
    ui.next.push(SHORT_LINE);

    await flow.giveType(CONFIG);

    expect(ui.opened).toEqual([]);
    expect(logs).toEqual([
      'cel-lens.setType gave no place to write the type; the engine may not match this extension.',
    ]);
  });

  it('says once that .cellens could not be opened, with Show Logs, and logs why', async () => {
    const { flow, ui, logs } = setUp();
    ui.openFailure = new Error('cannot open file:///ws/.cellens');
    ui.next.push(SHORT_LINE);

    await flow.giveType(CONFIG);

    expect(ui.errors).toEqual([
      {
        message: 'cel-lens could not open .cellens. Show the logs for the reason.',
        offerLogs: true,
      },
    ]);
    expect(logs).toEqual([`Could not open ${CELLENS}: Error: cannot open file:///ws/.cellens`]);
  });
});

describe('editType', () => {
  it.each([
    ['a short type', { type: '{name: string}' }, SHORT_LINE],
    ['a schema', { schema: { type: 'object' } }, SCHEMA_LINE],
    ['a Go type', { go: './api/v1#QuotaSpec' }, GO_LINE],
    ['a kind', { kind: 'apps/v1 Deployment' }, '$(symbol-class) Deployment'],
  ])(
    'opens the picker titled after the edit, on what a value with %s has',
    async (_name, entry, active) => {
      const { flow, ui } = setUp();

      await flow.editType({ ...CONFIG, ...entry });

      expect(ui.picks[0]?.title).toBe('Edit the type of params.spec.config');
      expect(ui.picks[0]?.active?.label).toBe(active);
    },
  );

  it.each([
    ['a short type', { type: '{name: string}' }, SHORT_LINE, 'type'],
    ['a schema', { schema: { type: 'object' } }, SCHEMA_LINE, 'schema'],
    ['a kind', { kind: 'apps/v1 Deployment' }, SHORT_LINE, 'type'],
  ])(
    'opens .cellens at the entry of a value with %s, where the engine puts the cursor on its type',
    async (_name, entry, line, snippet) => {
      const { flow, ui, engine } = setUp();
      const atType = { start: { line: 4, character: 10 }, end: { line: 4, character: 24 } };
      engine.answers.set('snippet', { cellens: CELLENS, change: 'unchanged', selection: atType });
      ui.next.push(line);

      await flow.editType({ ...CONFIG, ...entry });

      expect(engine.calls.at(-1)).toEqual({
        command: 'cel-lens.setType',
        argument: { ...CONFIG, snippet },
      });
      expect(ui.opened).toEqual([{ cellens: CELLENS, selection: atType }]);
    },
  );

  it('starts the Go types picker on the Go type a value has, and changes nothing when it is picked again', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push(GO_LINE, '$(symbol-struct) QuotaSpec');

    await flow.editType({ ...CONFIG, go: './api/v1#QuotaSpec' });

    expect(ui.picks[1]?.active?.label).toBe('$(symbol-struct) QuotaSpec');
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
  });

  it('replaces a Go type with the kind picked, with no Go type', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push('$(symbol-class) Deployment');

    await flow.editType({ ...CONFIG, go: './api/v1#QuotaSpec' });

    expect(engine.calls.at(-1)).toEqual({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, kind: 'apps/v1 Deployment' },
    });
  });

  it('changes nothing when the kind the value has is picked', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push('$(symbol-class) Deployment');

    await flow.editType({ ...CONFIG, kind: 'apps/v1 Deployment' });

    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
  });

  it('opens the int and string picker on the type of a CRD value, and sets the other', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push('$(symbol-string) string');

    await flow.editType({ ...SURGE, type: 'int' });

    expect(ui.picks[0]?.active?.label).toBe('$(symbol-numeric) int');
    expect(engine.calls).toEqual([
      { command: 'cel-lens.setType', argument: { ...SURGE, type: 'string' } },
    ]);
  });
});

describe('removeType', () => {
  it("removes the entry at once, with the line's argument, and shows nothing", async () => {
    const { flow, ui, engine, logs } = setUp();
    const typed = { ...CONFIG, type: '{name: string}' };

    await flow.removeType(typed);

    expect(engine.calls).toEqual([{ command: 'cel-lens.removeType', argument: typed }]);
    expect(ui.picks).toEqual([]);
    expect(ui.errors).toEqual([]);
    expect(logs).toEqual([]);
  });

  it('logs an answer that is not the one the engine documents, and shows nothing', async () => {
    const { flow, ui, engine, logs } = setUp();
    engine.answers.set('cel-lens.removeType', null);

    await flow.removeType({ ...CONFIG, type: 'int' });

    expect(ui.errors).toEqual([]);
    expect(logs).toEqual([
      'cel-lens.removeType gave no answer it documents; the engine may not match this extension.',
    ]);
  });
});

describe('a failure shown once', () => {
  it.each([
    [
      'the reason, with no button, when the engine refuses to remove',
      'cel-lens.removeType',
      responseError(-32803, WAITING),
      {
        message: `cel-lens could not remove the type of params.spec.config: ${WAITING}.`,
        offerLogs: false,
      },
      `Could not remove the type of params.spec.config: ${WAITING}`,
    ],
    [
      'Show Logs for another failure',
      'cel-lens.removeType',
      new Error('the cel-lens engine is not running'),
      {
        message:
          'cel-lens could not remove the type of params.spec.config. Show the logs for the reason.',
        offerLogs: true,
      },
      'Could not remove the type of params.spec.config: the cel-lens engine is not running',
    ],
  ])('shows %s', async (_name, command, failure, error, logged) => {
    const { flow, ui, engine, logs } = setUp();
    engine.failures.set(command, failure);

    await flow.removeType({ ...CONFIG, type: 'int' });

    expect(ui.errors).toEqual([error]);
    expect(logs).toEqual([logged]);
  });

  it('shows the reason a kind is refused, and stays quiet when it is cancelled', async () => {
    const refused = setUp();
    refused.engine.failures.set(
      'cel-lens.setType',
      responseError(-32602, 'cel-lens.setType: an entry for params.spec.config would not apply: x'),
    );
    refused.ui.next.push('$(symbol-class) Deployment');
    const cancelled = setUp();
    cancelled.engine.failures.set('cel-lens.setType', responseError(-32800, 'cancelled'));
    cancelled.ui.next.push('$(symbol-class) Deployment');

    await refused.flow.giveType(CONFIG);
    await cancelled.flow.giveType(CONFIG);

    expect(refused.ui.errors).toEqual([
      {
        message:
          'cel-lens could not give params.spec.config a type: an entry for params.spec.config would not apply: x.',
        offerLogs: false,
      },
    ]);
    expect(cancelled.ui.errors).toEqual([]);
  });

  it('shows the failure of the list of kinds once, and keeps the three first lines in the picker', async () => {
    const { flow, ui, engine } = setUp();
    engine.failures.set('cel-lens.typeChoices', new Error('the cel-lens engine is not running'));
    ui.next.push(GO_LINE);

    await flow.giveType(CONFIG);

    expect(ui.picks.map(labels)).toEqual([
      [SCHEMA_LINE, GO_LINE, SHORT_LINE],
      ['$(warning) The Go types could not be listed'],
    ]);
    expect(ui.errors).toEqual([
      {
        message:
          'cel-lens could not list the kinds and Go types for params.spec.config. Show the logs for the reason.',
        offerLogs: true,
      },
    ]);
  });
});

describe('giveValueType', () => {
  const range = { start: { line: 15, character: 4 }, end: { line: 15, character: 4 } };
  const lens = (command: string, argument: unknown): unknown => ({
    range,
    command: { title: 'params.spec.config: dyn · Give it a type', command, arguments: [argument] },
  });

  it("lists the file's values, titled and with a placeholder, and gives the one picked a type", async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push('self.spec.maxSurge', '$(symbol-string) string');

    await flow.giveValueType(
      [lens('cel-lens.giveType', CONFIG), lens('cel-lens.giveType', SURGE)],
      true,
    );

    expect(ui.picks[0]).toMatchObject({
      title: 'Give a value a type',
      placeholder: 'Pick a value to give a type; dyn means it has none yet',
    });
    expect(ui.picks[0]?.lines.map((line) => line.label)).toEqual([
      'params.spec.config',
      'self.spec.maxSurge',
    ]);
    expect(ui.picks[1]?.title).toBe('Give self.spec.maxSurge a type');
    expect(engine.calls).toEqual([
      { command: 'cel-lens.setType', argument: { ...SURGE, type: 'string' } },
    ]);
  });

  it('edits the type of a value that has one', async () => {
    const { flow, ui } = setUp();
    ui.next.push('params.spec.config');

    await flow.giveValueType([lens('cel-lens.editType', { ...CONFIG, type: 'int' })], true);

    expect(ui.picks[1]).toMatchObject({
      title: 'Edit the type of params.spec.config',
      active: { label: SHORT_LINE },
    });
  });

  it.each([
    ['no value', true, NO_VALUES_MESSAGE],
    ['the line above each rule off', false, INLINE_INFO_OFF_MESSAGE],
  ])('says so in one quiet message with %s', async (_name, inlineInfo, message) => {
    const { flow, ui } = setUp();

    await flow.giveValueType([lens('cel-lens.showRule', {})], inlineInfo);

    expect(ui.infos).toEqual([message]);
    expect(ui.picks).toEqual([]);
  });
});
