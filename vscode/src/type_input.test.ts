import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { CHECK_DELAY_MS, EMPTY_TYPE_PROBLEM, TypeInput, type InputView } from './type_input';

/** An input box that remembers what it shows. */
class View implements InputView {
  value = '';
  problem: string | undefined;
  busy = false;
  enabled = true;
  closed = false;

  setProblem(message: string | undefined): void {
    this.problem = message;
  }

  setBusy(busy: boolean): void {
    this.busy = busy;
  }

  setEnabled(enabled: boolean): void {
    this.enabled = enabled;
  }

  close(): void {
    this.closed = true;
  }
}

/** A check that answers when the test says, so a late answer can come after the text changed. */
interface PendingCheck {
  readonly text: string;
  readonly signal: AbortSignal;
  answer(problem: string | undefined): void;
}

function setUp(): {
  view: View;
  input: TypeInput;
  checks: PendingCheck[];
  accepted: string[];
  refuse: { next: string | undefined };
} {
  const view = new View();
  const checks: PendingCheck[] = [];
  const accepted: string[] = [];
  const refuse: { next: string | undefined } = { next: undefined };
  const input = new TypeInput(view, {
    check: (text, signal) =>
      new Promise((resolve) => {
        checks.push({ text, signal, answer: resolve });
      }),
    accept: (text) => {
      accepted.push(text);
      return Promise.resolve(refuse.next);
    },
  });
  return { view, input, checks, accepted, refuse };
}

function type(view: View, input: TypeInput, text: string): void {
  view.value = text;
  input.changed(text);
}

/** Lets the promises chained on a check's answer run. */
async function settle(): Promise<void> {
  for (let turn = 0; turn < 10; turn += 1) {
    await Promise.resolve();
  }
}

describe('TypeInput', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('checks the text once the person pauses, not at every keystroke', () => {
    const { view, input, checks } = setUp();

    type(view, input, '{name');
    vi.advanceTimersByTime(CHECK_DELAY_MS - 1);
    type(view, input, '{name: str');
    vi.advanceTimersByTime(CHECK_DELAY_MS - 1);
    expect(checks).toEqual([]);

    vi.advanceTimersByTime(1);
    expect(checks.map((check) => check.text)).toEqual(['{name: str']);
    expect(view.busy).toBe(true);
  });

  it("shows the check's problem, and clears it once a later text is fine", async () => {
    const { view, input, checks } = setUp();

    type(view, input, '{name: strin}');
    vi.advanceTimersByTime(CHECK_DELAY_MS);
    checks[0]?.answer('unknown type "strin" (character 8)');
    await settle();
    expect(view.problem).toBe('unknown type "strin" (character 8)');
    expect(view.busy).toBe(false);

    type(view, input, '{name: string}');
    vi.advanceTimersByTime(CHECK_DELAY_MS);
    checks[1]?.answer(undefined);
    await settle();
    expect(view.problem).toBeUndefined();
  });

  it('cancels the check of an older text, and never shows its late answer', async () => {
    const { view, input, checks } = setUp();

    type(view, input, '{name: strin}');
    vi.advanceTimersByTime(CHECK_DELAY_MS);
    type(view, input, '{name: string}');
    expect(checks[0]?.signal.aborted).toBe(true);
    expect(view.busy).toBe(false);

    checks[0]?.answer('unknown type "strin" (character 8)');
    await settle();
    expect(view.problem).toBeUndefined();

    vi.advanceTimersByTime(CHECK_DELAY_MS);
    checks[1]?.answer(undefined);
    await settle();
    expect(checks.map((check) => check.text)).toEqual(['{name: strin}', '{name: string}']);
    expect(view.problem).toBeUndefined();
  });

  it('clears the problem of an emptied box without asking the engine', () => {
    const { view, input, checks } = setUp();
    view.problem = 'unknown type "strin" (character 8)';

    type(view, input, '  ');
    vi.advanceTimersByTime(CHECK_DELAY_MS);

    expect(checks).toEqual([]);
    expect(view.problem).toBeUndefined();
  });

  it('sets a type that is fine on Enter and closes the box', async () => {
    const { view, input, checks, accepted } = setUp();
    type(view, input, '{name: string}');
    vi.advanceTimersByTime(CHECK_DELAY_MS);
    checks[0]?.answer(undefined);

    await input.accepted();

    expect(checks).toHaveLength(1);
    expect(accepted).toEqual(['{name: string}']);
    expect(view.closed).toBe(true);
  });

  it('checks at once on an Enter that comes before the pause, then sets the type', async () => {
    const { view, input, checks, accepted } = setUp();
    type(view, input, '{name: string}');

    const enter = input.accepted();
    expect(checks.map((check) => check.text)).toEqual(['{name: string}']);
    checks[0]?.answer(undefined);
    await enter;

    vi.advanceTimersByTime(CHECK_DELAY_MS);
    expect(checks).toHaveLength(1);
    expect(accepted).toEqual(['{name: string}']);
  });

  it('keeps the box open with the problem on Enter on a type that is wrong', async () => {
    const { view, input, checks, accepted } = setUp();
    type(view, input, '{name: strin}');

    const enter = input.accepted();
    checks[0]?.answer('unknown type "strin" (character 8)');
    await enter;

    expect(accepted).toEqual([]);
    expect(view.closed).toBe(false);
    expect(view.problem).toBe('unknown type "strin" (character 8)');
  });

  it('asks for a type on Enter in an empty box', async () => {
    const { view, input, checks, accepted } = setUp();

    await input.accepted();

    expect(checks).toEqual([]);
    expect(accepted).toEqual([]);
    expect(view.problem).toBe(EMPTY_TYPE_PROBLEM);
  });

  it('keeps the box open with the text and the reason when the engine refuses to set it', async () => {
    const { view, input, checks, refuse } = setUp();
    refuse.next =
      'An edit of .cellens is waiting for the editor: run the command again once it is applied.';
    type(view, input, '{name: string}');

    const enter = input.accepted();
    checks[0]?.answer(undefined);
    await enter;

    expect(view.closed).toBe(false);
    expect(view.value).toBe('{name: string}');
    expect(view.problem).toBe(refuse.next);
    expect(view).toMatchObject({ busy: false, enabled: true });
  });

  it('sets the type once when Enter is pressed again while it is set', async () => {
    const { view, input, checks, accepted } = setUp();
    type(view, input, '{name: string}');
    vi.advanceTimersByTime(CHECK_DELAY_MS);
    checks[0]?.answer(undefined);
    await settle();

    const first = input.accepted();
    const second = input.accepted();
    await Promise.all([first, second]);

    expect(accepted).toEqual(['{name: string}']);
  });

  it('sets nothing when the text changed while Enter waited for its check', async () => {
    const { view, input, checks, accepted } = setUp();
    type(view, input, '{name: string}');

    const enter = input.accepted();
    type(view, input, '{name: string, replicas: int}');
    checks[0]?.answer(undefined);
    await enter;

    expect(accepted).toEqual([]);
    expect(view.closed).toBe(false);
  });

  it('sets nothing when the box closes while Enter waits for its check', async () => {
    const { view, input, checks, accepted } = setUp();
    type(view, input, '{name: string}');

    const enter = input.accepted();
    input.dispose();
    checks[0]?.answer(undefined);
    await enter;

    expect(checks[0]?.signal.aborted).toBe(true);
    expect(accepted).toEqual([]);
  });

  it('checks nothing once the box is closed', () => {
    const { view, input, checks } = setUp();
    type(view, input, '{name: string}');

    input.dispose();
    vi.advanceTimersByTime(CHECK_DELAY_MS);

    expect(checks).toEqual([]);
  });

  it('counts a check that fails as no problem, so Enter can set the type and say why it fails', async () => {
    const view = new View();
    const accepted: string[] = [];
    const input = new TypeInput(view, {
      check: () => Promise.reject(new Error('the cel-lens engine is not running')),
      accept: (text) => {
        accepted.push(text);
        return Promise.resolve('cel-lens could not give self.spec.config a type.');
      },
    });
    type(view, input, '{name: string}');

    await input.accepted();

    expect(accepted).toEqual(['{name: string}']);
    expect(view.problem).toBe('cel-lens could not give self.spec.config a type.');
  });
});
