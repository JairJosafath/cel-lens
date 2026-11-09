import { describe, expect, it } from 'vitest';

import {
  INLINE_INFO_OFF_MESSAGE,
  NO_VALUES_MESSAGE,
  TypeFlow,
  type PickRequest,
  type Picked,
  type TypeUi,
  type WriteEnd,
  type WriteRequest,
} from './type_flow';
import {
  INT_OR_STRING_PLACEHOLDER,
  KIND_PLACEHOLDER,
  STRUCT_PLACEHOLDER,
  type PickEntry,
  type PickLine,
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
};

const WAITING =
  'an edit of .cellens is waiting for the editor: run the command again once it is applied';

/** A ResponseError as the language client rejects with it. */
function responseError(code: number, message: string): Error {
  return Object.assign(new Error(message), { code });
}

interface Call {
  readonly command: string;
  readonly argument: unknown;
}

/** The engine, faked: answers each command from `answers`, or rejects with `failures`. */
function fakeEngine(): {
  calls: Call[];
  answers: Map<string, unknown>;
  failures: Map<string, Error>;
  run: (command: string, argument?: unknown, signal?: AbortSignal) => Promise<unknown>;
} {
  const calls: Call[] = [];
  const answers = new Map<string, unknown>([
    ['cel-lens.typeChoices', CHOICES],
    ['cel-lens.checkType', null],
    ['cel-lens.setType', { cellens: CELLENS, change: 'added' }],
    ['cel-lens.removeType', { cellens: CELLENS, change: 'removed' }],
  ]);
  const failures = new Map<string, Error>();
  return {
    calls,
    answers,
    failures,
    run: (command, argument) => {
      calls.push({ command, argument });
      const failure = failures.get(command);
      return failure === undefined
        ? Promise.resolve(answers.get(command))
        : Promise.reject(failure);
    },
  };
}

/** The quick picks and input boxes, faked: each pick takes the line labelled `picks`'s next. */
/** What FakeUi.typed takes for a click on the input box's Pick a kind… button. */
const PICK_KIND = 'the Pick a kind… button';

/** A picker as it was shown: its lines once they came, and the one it started on. */
interface ShownPick {
  readonly title: string;
  readonly placeholder: string;
  readonly first: readonly PickLine<unknown>[] | undefined;
  readonly lines: readonly PickLine<unknown>[];
  readonly active: PickEntry<unknown> | undefined;
}

class FakeUi implements TypeUi {
  readonly picks: ShownPick[] = [];
  readonly writes: WriteRequest[] = [];
  readonly infos: string[] = [];
  readonly errors: { message: string; offerLogs: boolean }[] = [];
  readonly next: (string | undefined)[] = [];
  /** What is typed in the filter of each picker before its line is picked. */
  readonly filters: string[] = [];
  /** What each input box does once open: the text it sets, undefined for Escape, or PICK_KIND. */
  readonly typed: (string | undefined)[] = [];
  /** What the box shows after Enter: undefined once the type is set. */
  readonly shown: (string | undefined)[] = [];
  settingsOpened = 0;

  async pick<T>(request: PickRequest<T>): Promise<Picked<T> | undefined> {
    const lines = request.lines instanceof Promise ? await request.lines : request.lines;
    const entries = lines.filter((line): line is PickEntry<T> => line.separator !== true);
    this.picks.push({
      title: request.title,
      placeholder: request.placeholder,
      first: request.first,
      lines,
      active: entries.find((line) => request.active?.(line.value) === true),
    });
    const label = this.next.shift();
    const line = entries.find((each) => each.label === label);
    return line === undefined
      ? undefined
      : { value: line.value, typed: this.filters.shift() ?? '' };
  }

  async write(request: WriteRequest): Promise<WriteEnd> {
    this.writes.push(request);
    const text = this.typed.shift();
    if (text === PICK_KIND) {
      return 'pickKind';
    }
    if (text !== undefined) {
      this.shown.push(await request.accept(text));
    }
    return 'closed';
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

describe('giveType', () => {
  it('opens the picker titled after the value, Write a struct… first, then the built-in kinds and the workspace CRDs', async () => {
    const { flow, ui, engine } = setUp();

    await flow.giveType(CONFIG);

    expect(engine.calls).toEqual([{ command: 'cel-lens.typeChoices', argument: undefined }]);
    expect(ui.picks).toHaveLength(1);
    expect(ui.picks[0]).toMatchObject({
      title: 'Give params.spec.config a type',
      placeholder: KIND_PLACEHOLDER,
    });
    expect(ui.picks[0]?.lines.map((line) => line.label)).toEqual([
      '$(edit) Write a struct…',
      'Built-in kinds · Kubernetes 1.37',
      '$(symbol-class) Deployment',
      'Workspace CRDs',
      '$(file-code) Widget',
    ]);
    expect(ui.picks[0]?.active).toBeUndefined();
  });

  it('sets the kind picked, with no type', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push('$(symbol-class) Deployment');

    await flow.giveType(CONFIG);

    expect(engine.calls.at(-1)).toEqual({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, kind: 'apps/v1 Deployment' },
    });
    expect(ui.errors).toEqual([]);
  });

  it('opens the input box from Write a struct…, with the struct placeholder and nothing in it, and sets the type written', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push('$(edit) Write a struct…');
    ui.typed.push('{name: string, replicas: int}');

    await flow.giveType(CONFIG);

    expect(ui.writes).toHaveLength(1);
    expect(ui.writes[0]).toMatchObject({
      title: 'Give params.spec.config a type',
      placeholder: STRUCT_PLACEHOLDER,
      value: '',
    });
    expect(STRUCT_PLACEHOLDER).toBe('{name: string, replicas: int}');
    expect(engine.calls.at(-1)).toEqual({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, type: '{name: string, replicas: int}' },
    });
    expect(ui.shown).toEqual([undefined]);
  });

  it.each([
    ['a struct', '{name: string}', '{name: string}'],
    ["a kind's name", 'deploy', ''],
  ])(
    'starts the input box with %s typed in the filter as what it reads as a type',
    async (_name, typed, value) => {
      const { flow, ui } = setUp();
      ui.next.push('$(edit) Write a struct…');
      ui.filters.push(typed);
      ui.typed.push(undefined);

      await flow.giveType(CONFIG);

      expect(ui.writes[0]?.value).toBe(value);
    },
  );

  it('changes nothing when the picker is closed with Escape', async () => {
    const { flow, ui, engine } = setUp();

    await flow.giveType(CONFIG);

    expect(ui.writes).toEqual([]);
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
  });

  it('changes nothing when the input box is closed with Escape', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push('$(edit) Write a struct…');
    ui.typed.push(undefined);

    await flow.giveType(CONFIG);

    expect(ui.writes).toHaveLength(1);
    expect(engine.calls.map((call) => call.command)).toEqual(['cel-lens.typeChoices']);
  });

  it('offers only int and string for a value of a CRD, without asking for the kinds, and sets the one picked', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push('$(symbol-numeric) int');

    await flow.giveType(SURGE);

    expect(ui.picks[0]).toMatchObject({
      title: 'Give self.spec.maxSurge a type',
      placeholder: INT_OR_STRING_PLACEHOLDER,
    });
    expect(ui.picks[0]?.lines.map((line) => line.label)).toEqual([
      '$(symbol-numeric) int',
      '$(symbol-string) string',
    ]);
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

  it('offers Write a struct… alone, and logs, when the kinds are not in the form the engine documents', async () => {
    const { flow, ui, engine, logs } = setUp();
    engine.answers.set('cel-lens.typeChoices', { kinds: [] });

    await flow.giveType(CONFIG);

    expect(ui.picks[0]?.lines.map((line) => line.label)).toEqual(['$(edit) Write a struct…']);
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

describe('the check as the person types', () => {
  async function checkOf(
    answer: unknown,
    failure?: Error,
  ): Promise<{ shown: string | undefined; calls: Call[]; logs: string[] }> {
    const { flow, ui, engine, logs } = setUp();
    engine.answers.set('cel-lens.checkType', answer);
    if (failure !== undefined) {
      engine.failures.set('cel-lens.checkType', failure);
    }
    ui.next.push('$(edit) Write a struct…');
    ui.typed.push(undefined);
    await flow.giveType(CONFIG);
    const request = ui.writes[0];
    if (request === undefined) {
      throw new Error('no input box was shown');
    }
    const shown = await request.check('{name: strin}', new AbortController().signal);
    return { shown, calls: engine.calls.slice(1), logs };
  }

  it("asks cel-lens.checkType about the value's argument with the type", async () => {
    const { calls } = await checkOf(null);

    expect(calls).toEqual([
      { command: 'cel-lens.checkType', argument: { ...CONFIG, type: '{name: strin}' } },
    ]);
  });

  it.each([
    ['a type that is fine', null, undefined],
    [
      'the problem at its character',
      { message: 'unknown type "strin": use string, int, ...', column: 8 },
      'unknown type "strin": use string, int, ... (character 8)',
    ],
    [
      'the problem of the value as a whole',
      { message: 'spec.config is already typed as string: remove this entry' },
      'spec.config is already typed as string: remove this entry',
    ],
  ])('shows %s', async (_name, answer, expected) => {
    expect((await checkOf(answer)).shown).toBe(expected);
  });

  it.each([
    ['the reason the engine refuses', responseError(-32602, 'cel-lens.checkType: bad'), 'bad', []],
    [
      'nothing for a check cancelled',
      responseError(-32800, 'Request got cancelled'),
      undefined,
      [],
    ],
    [
      'nothing, and logs, for another failure',
      new Error('the cel-lens engine is not running'),
      undefined,
      ['Could not check the type of params.spec.config: the cel-lens engine is not running'],
    ],
  ])('shows %s', async (_name, failure, expected, logged) => {
    const { shown, logs } = await checkOf(null, failure);

    expect(shown).toBe(expected);
    expect(logs).toEqual(logged);
  });
});

describe('setting the type written', () => {
  it.each([
    [
      'the reason the engine refuses, keeping the box open',
      responseError(-32803, WAITING),
      `${WAITING}.`,
    ],
    [
      'where to look for another failure',
      new Error('the connection closed'),
      'cel-lens could not give params.spec.config a type. Run cel-lens: Show Logs for the reason.',
    ],
  ])('shows %s', async (_name, failure, expected) => {
    const { flow, ui, engine } = setUp();
    engine.failures.set('cel-lens.setType', failure);
    ui.next.push('$(edit) Write a struct…');
    ui.typed.push('{name: string}');

    await flow.giveType(CONFIG);

    expect(ui.shown).toEqual([expected]);
    expect(ui.errors).toEqual([]);
  });
});

describe('editType', () => {
  it('opens the input box filled with the type a value has, titled after the edit', async () => {
    const { flow, ui, engine } = setUp();
    ui.typed.push('{name: string, replicas: int}');

    await flow.editType({ ...CONFIG, type: '{name: string}' });

    expect(ui.picks).toEqual([]);
    expect(ui.writes[0]).toMatchObject({
      title: 'Edit the type of params.spec.config',
      placeholder: STRUCT_PLACEHOLDER,
      value: '{name: string}',
    });
    expect(engine.calls).toEqual([
      {
        command: 'cel-lens.setType',
        argument: { ...CONFIG, type: '{name: string, replicas: int}' },
      },
    ]);
  });

  it('turns from the box of a struct to the picker with Pick a kind…, and sets the kind picked, with no type', async () => {
    const { flow, ui, engine } = setUp();
    ui.typed.push(PICK_KIND);
    ui.next.push('$(symbol-class) Deployment');

    await flow.editType({ ...CONFIG, type: '{name: string}' });

    expect(ui.picks.map((pick) => pick.title)).toEqual(['Edit the type of params.spec.config']);
    expect(engine.calls.at(-1)).toEqual({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, kind: 'apps/v1 Deployment' },
    });
  });

  it('comes back to the box filled with the struct from Write a struct… after Pick a kind…', async () => {
    const { flow, ui } = setUp();
    ui.typed.push(PICK_KIND, undefined);
    ui.next.push('$(edit) Write a struct…');

    await flow.editType({ ...CONFIG, type: '{name: string}' });

    expect(ui.writes.map((write) => write.value)).toEqual(['{name: string}', '{name: string}']);
  });

  it('closes the box and changes nothing on Enter on the type it has', async () => {
    const { flow, ui, engine } = setUp();
    ui.typed.push('{name: string}');

    await flow.editType({ ...CONFIG, type: '{name: string}' });

    expect(ui.shown).toEqual([undefined]);
    expect(engine.calls).toEqual([]);
  });

  it('opens the picker on the kind a value has, and sets the type written instead, with no kind', async () => {
    const { flow, ui, engine } = setUp();
    ui.next.push('$(edit) Write a struct…');
    ui.typed.push('{name: string}');

    await flow.editType({ ...CONFIG, kind: 'apps/v1 Deployment' });

    expect(ui.picks[0]?.title).toBe('Edit the type of params.spec.config');
    expect(ui.picks[0]?.active?.label).toBe('$(symbol-class) Deployment');
    expect(ui.writes[0]?.value).toBe('');
    expect(engine.calls.at(-1)).toEqual({
      command: 'cel-lens.setType',
      argument: { ...CONFIG, type: '{name: string}' },
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

    expect(ui.writes).toEqual([]);
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

  it('shows the failure of the list of kinds once, and keeps Write a struct… in the picker', async () => {
    const { flow, ui, engine } = setUp();
    engine.failures.set('cel-lens.typeChoices', new Error('the cel-lens engine is not running'));

    await flow.giveType(CONFIG);

    expect(ui.picks.map((pick) => pick.lines.map((line) => line.label))).toEqual([
      ['$(edit) Write a struct…'],
    ]);
    expect(ui.errors).toEqual([
      {
        message:
          'cel-lens could not list the kinds for params.spec.config. Show the logs for the reason.',
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

    expect(ui.writes[0]).toMatchObject({
      title: 'Edit the type of params.spec.config',
      value: 'int',
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
