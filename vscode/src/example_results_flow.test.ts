import { describe, expect, it } from 'vitest';

import {
  COMPUTING_LINE,
  NO_RESULTS_LINE,
  RULE_CHANGED_LINE,
  type ResultPick,
} from './example_results';
import { ExampleResultsFlow, type ResultsUi } from './example_results_flow';
import { BACK, type PickRequest, type Picked } from './type_flow';
import type { PickEntry, PickLine } from './value_types';

const RULE = {
  uri: 'file:///ws/crd.yaml',
  range: { start: { line: 15, character: 30 }, end: { line: 15, character: 52 } },
};

function sample(over: Record<string, unknown>, verdict: Record<string, unknown>): unknown {
  return {
    example: {
      uri: 'file:///ws/config/samples/ok.yaml',
      file: 'config/samples/ok.yaml',
      line: 1,
      operation: 'create',
      expect: 'allowed',
      ...over,
    },
    result: 'allowed',
    expected: true,
    ...verdict,
  };
}

const OK = sample({}, {});
const BIG = sample(
  { uri: 'file:///ws/config/samples/big.yaml', file: 'config/samples/big.yaml', line: 3 },
  {
    result: 'refused',
    refusals: [{ path: 'spec.replicas', message: 'spec.replicas: Invalid value: 20: at most 10' }],
    expected: false,
    wrong: 'rule',
  },
);

function answerOf(...results: unknown[]): unknown {
  return { kubernetesVersion: '1.37', rules: [{ location: RULE, results }] };
}

interface ShownPick {
  readonly title: string;
  readonly placeholder: string;
  readonly first: readonly PickLine<ResultPick>[] | undefined;
  /** What the lines were once the engine answered. */
  readonly lines: readonly PickLine<ResultPick>[];
}

class FakeUi implements ResultsUi {
  readonly picks: ShownPick[] = [];
  readonly opened: { uri: string; line: number }[] = [];
  readonly errors: { message: string; offerLogs: boolean }[] = [];
  /** The labels the next pickers pick, in order; none is Escape. */
  readonly next: (string | undefined)[] = [];
  off = false;
  /** The person presses Escape before the engine answers. */
  escapeAtOnce = false;
  turnOnOffered = 0;
  logsShown = 0;
  openFailure: Error | undefined;

  async pick(
    request: PickRequest<ResultPick>,
  ): Promise<Picked<ResultPick> | typeof BACK | undefined> {
    const choice = this.next.shift();
    if (this.escapeAtOnce) {
      this.picks.push(shown(request, []));
      return undefined;
    }
    const lines = request.lines instanceof Promise ? await request.lines : request.lines;
    this.picks.push(shown(request, lines));
    if (choice === BACK) {
      return BACK;
    }
    const line = lines.find(
      (each): each is PickEntry<ResultPick> => each.separator !== true && each.label === choice,
    );
    return line === undefined ? undefined : { value: line.value, typed: '' };
  }

  isOn(): boolean {
    return !this.off;
  }

  offerTurnOn(): Promise<void> {
    this.turnOnOffered += 1;
    return Promise.resolve();
  }

  openExample(uri: string, line: number): Promise<void> {
    if (this.openFailure !== undefined) {
      return Promise.reject(this.openFailure);
    }
    this.opened.push({ uri, line });
    return Promise.resolve();
  }

  showError(message: string, offerLogs: boolean): Promise<void> {
    this.errors.push({ message, offerLogs });
    return Promise.resolve();
  }

  showLogs(): void {
    this.logsShown += 1;
  }
}

function shown(
  request: PickRequest<ResultPick>,
  lines: readonly PickLine<ResultPick>[],
): ShownPick {
  return {
    title: request.title,
    placeholder: request.placeholder,
    first: request.first,
    lines,
  };
}

/** An engine that answers what it is told to, and records each call with whether it was cancelled. */
function setUp(answer: () => Promise<unknown>): {
  ui: FakeUi;
  logs: string[];
  calls: { command: string; argument: unknown; signal: AbortSignal | undefined }[];
  flow: ExampleResultsFlow;
} {
  const calls: { command: string; argument: unknown; signal: AbortSignal | undefined }[] = [];
  const ui = new FakeUi();
  const logs: string[] = [];
  const log = { warn: (m: string) => logs.push(m), error: (m: string) => logs.push(m) };
  const flow = new ExampleResultsFlow(
    (command, argument, signal) => {
      calls.push({ command, argument, signal });
      return answer();
    },
    ui,
    log,
  );
  return { ui, logs, calls, flow };
}

function labels(pick: ShownPick | undefined): string[] {
  return (pick?.lines ?? []).map((line) => line.label);
}

describe('ExampleResultsFlow.show', () => {
  it('asks the engine cel-lens.exampleResults for the rule, with the location the engine gave, and shows the wrong verdicts first with their file, line and message', async () => {
    const { flow, ui, calls } = setUp(() => Promise.resolve(answerOf(OK, BIG)));

    await flow.show({ ...RULE });

    expect(calls).toMatchObject([{ command: 'cel-lens.exampleResults', argument: RULE }]);
    expect(ui.picks).toHaveLength(1);
    expect(ui.picks[0]).toMatchObject({
      title: 'Example results of the rule on line 16',
      placeholder: 'Pick an example to open it at its line',
    });
    expect(labels(ui.picks[0])).toEqual([
      'Not as expected',
      '$(warning) config/samples/big.yaml:3',
      'As expected',
      '$(pass) config/samples/ok.yaml:1',
    ]);
    expect(ui.picks[0]?.lines[1]).toMatchObject({
      description: 'refused · expected allowed',
      detail: 'spec.replicas: Invalid value: 20: at most 10',
    });
  });

  it('opens the picker at once, saying the results are being computed, before the engine answers', async () => {
    const { flow, ui } = setUp(() => Promise.resolve(answerOf(OK)));

    await flow.show(RULE);

    expect(ui.picks[0]?.first).toEqual([COMPUTING_LINE]);
    expect(COMPUTING_LINE.label).toContain('Computing the example results');
  });

  it('opens the example picked at its line', async () => {
    const { flow, ui } = setUp(() => Promise.resolve(answerOf(OK, BIG)));
    ui.next.push('$(warning) config/samples/big.yaml:3');

    await flow.show(RULE);

    expect(ui.opened).toEqual([{ uri: 'file:///ws/config/samples/big.yaml', line: 3 }]);
  });

  it.each([
    ['Escape', undefined],
    ['Back', BACK],
  ])('opens nothing on %s', async (_name, choice) => {
    const { flow, ui } = setUp(() => Promise.resolve(answerOf(OK)));
    ui.next.push(choice);

    await flow.show(RULE);

    expect(ui.opened).toEqual([]);
  });

  it('says once that the example could not be opened, with a button to the logs, and logs why', async () => {
    const { flow, ui, logs } = setUp(() => Promise.resolve(answerOf(OK)));
    ui.next.push('$(pass) config/samples/ok.yaml:1');
    ui.openFailure = new Error('file is gone');

    await flow.show(RULE);

    expect(ui.errors).toEqual([
      {
        message: 'cel-lens could not open config/samples/ok.yaml. Show the logs for the reason.',
        offerLogs: true,
      },
    ]);
    expect(logs).toEqual(['Could not open file:///ws/config/samples/ok.yaml: Error: file is gone']);
  });

  it('says no results are there yet, that they are being computed, when the engine has none for the rule', async () => {
    const { flow, ui } = setUp(() => Promise.resolve(answerOf()));

    await flow.show(RULE);

    expect(ui.picks[0]?.lines).toEqual([NO_RESULTS_LINE]);
    expect(NO_RESULTS_LINE.detail).toContain('They are being computed');
  });

  it('says the rule changed, and to click its part again, instead of showing the results of the only other rule the engine answers with (#13)', async () => {
    const stale = {
      ...RULE,
      range: { start: { line: 40, character: 30 }, end: { line: 40, character: 52 } },
    };
    const { flow, ui } = setUp(() => Promise.resolve(answerOf(OK, BIG)));

    await flow.show(stale);

    expect(ui.picks[0]?.lines).toEqual([RULE_CHANGED_LINE]);
    expect(RULE_CHANGED_LINE.detail).toBe('Click its part above the rule again');
  });

  it('cancels the engine’s work once the picker is closed', async () => {
    const { flow, calls } = setUp(() => Promise.resolve(answerOf(OK)));

    await flow.show(RULE);

    expect(calls[0]?.signal?.aborted).toBe(true);
  });

  it('cancels the engine’s work when Escape closes the picker before the engine answers', async () => {
    const { flow, ui, calls } = setUp(() => new Promise(() => undefined));
    ui.escapeAtOnce = true;

    await flow.show(RULE);

    expect(calls[0]?.signal?.aborted).toBe(true);
    expect(ui.opened).toEqual([]);
  });

  it('says the results are off, and offers to turn them on, without asking the engine', async () => {
    const { flow, ui, calls } = setUp(() => Promise.resolve(answerOf(OK)));
    ui.off = true;

    await flow.show(RULE);

    expect(ui.turnOnOffered).toBe(1);
    expect(calls).toEqual([]);
    expect(ui.picks).toEqual([]);
  });

  it.each([
    ['nothing', undefined],
    ['a rule with no range', { uri: 'file:///ws/crd.yaml' }],
    ['a rule with no URI', { range: RULE.range }],
  ])('logs once that it was given %s, and shows nothing', async (_name, argument) => {
    const { flow, ui, logs, calls } = setUp(() => Promise.resolve(answerOf(OK)));

    await flow.show(argument);

    expect(logs).toEqual([
      'cel-lens.showExampleResults was given no rule location; the engine may not match this extension.',
    ]);
    expect(calls).toEqual([]);
    expect(ui.picks).toEqual([]);
  });

  it('shows the engine’s reason when it refuses, as the setting turned off since the click, without the logs button', async () => {
    const refused = { code: -32803, message: 'cel-lens.exampleResults: example results are off' };
    const { flow, ui, logs } = setUp(() => Promise.reject(Object.assign(new Error('x'), refused)));

    await flow.show(RULE);

    expect(ui.picks[0]?.lines).toMatchObject([
      { label: '$(error) Could not get the example results', detail: 'example results are off' },
    ]);
    expect(ui.picks[0]?.lines[0]).toMatchObject({ value: { action: 'none' } });
    expect(logs).toEqual(['Could not get the example results: example results are off']);
  });

  it('shows a way to the logs for another failure, and logs it', async () => {
    const { flow, ui, logs } = setUp(() => Promise.reject(new Error('connection closed')));
    ui.next.push('$(error) Could not get the example results');

    await flow.show(RULE);

    expect(ui.picks[0]?.lines).toMatchObject([
      {
        label: '$(error) Could not get the example results',
        detail: 'Show the logs for the reason',
        value: { action: 'showLogs' },
      },
    ]);
    expect(ui.logsShown).toBe(1);
    expect(logs).toEqual(['Could not get the example results: connection closed']);
  });

  it('shows nothing and logs nothing for a request the engine cancelled', async () => {
    const cancelled = Object.assign(new Error('cancelled'), { code: -32800 });
    const { flow, ui, logs } = setUp(() => Promise.reject(cancelled));

    await flow.show(RULE);

    expect(ui.picks[0]?.lines).toEqual([]);
    expect(logs).toEqual([]);
  });

  it('warns that the engine may not match this extension when it answers no rules', async () => {
    const { flow, ui, logs } = setUp(() => Promise.resolve({ results: [] }));

    await flow.show(RULE);

    expect(logs).toEqual([
      'cel-lens.exampleResults gave no rules; the engine may not match this extension.',
    ]);
    expect(ui.picks[0]?.lines).toMatchObject([{ value: { action: 'showLogs' } }]);
  });
});
