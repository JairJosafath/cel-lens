/** How long the input box waits after a keystroke before it asks the engine to check the type. */
export const CHECK_DELAY_MS = 150;

/** What Enter on an empty input box says. */
export const EMPTY_TYPE_PROBLEM = 'Write a type, such as {name: string, replicas: int}';

/** The parts of an input box `TypeInput` drives; `vscode.InputBox` has each of them. */
export interface InputView {
  readonly value: string;
  setProblem(message: string | undefined): void;
  setBusy(busy: boolean): void;
  setEnabled(enabled: boolean): void;
  close(): void;
}

/** What to do with the text of the input box. */
export interface InputActions {
  /** What is wrong with `text`, or `undefined` when nothing is; `signal` fires when the text changed since. */
  check(text: string, signal: AbortSignal): Promise<string | undefined>;
  /** Sets the type: `undefined` once it is set, else what to show in the box. */
  accept(text: string): Promise<string | undefined>;
}

/** A check of one text, running or answered. */
interface Check {
  readonly text: string;
  readonly controller: AbortController;
  readonly problem: Promise<string | undefined>;
}

/**
 * Checks the type in an input box as the person types: `CHECK_DELAY_MS` after the last
 * keystroke, cancelling the check of an older text, and shows a problem only while the box still
 * holds the text it is about. Enter sets a type the check finds nothing wrong with and closes the
 * box; a problem, or a type the engine refuses to set, keeps the box open with the text.
 */
export class TypeInput {
  private timer: ReturnType<typeof setTimeout> | undefined;
  private last: Check | undefined;
  private accepting = false;
  /** The box closed: Escape changes nothing, even while Enter waits for its check. */
  private disposed = false;

  constructor(
    private readonly view: InputView,
    private readonly actions: InputActions,
  ) {}

  /** The text changed: the check of the old one is cancelled, and the new one is checked after a pause. */
  changed(text: string): void {
    this.cancel();
    if (text.trim() === '') {
      this.view.setProblem(undefined);
      return;
    }
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void this.check(text); // Shows its own answer; nothing waits for it here.
    }, CHECK_DELAY_MS);
  }

  /** Enter: checks the text now, unless it is already checked, and sets it when nothing is wrong. */
  async accepted(): Promise<void> {
    const text = this.view.value;
    if (this.accepting) {
      return;
    }
    if (text.trim() === '') {
      this.view.setProblem(EMPTY_TYPE_PROBLEM);
      return;
    }
    this.accepting = true;
    try {
      const problem = await this.check(text);
      if (this.disposed || problem !== undefined || this.view.value !== text) {
        return;
      }
      this.view.setBusy(true);
      this.view.setEnabled(false);
      const refused = await this.actions.accept(text);
      if (refused === undefined) {
        this.view.close();
        return;
      }
      this.view.setBusy(false);
      this.view.setEnabled(true);
      this.view.setProblem(refused);
    } finally {
      this.accepting = false;
    }
  }

  /** The box closed: nothing more is checked, and nothing is set. */
  dispose(): void {
    this.disposed = true;
    this.cancel();
  }

  private cancel(): void {
    clearTimeout(this.timer);
    this.timer = undefined;
    if (this.last !== undefined) {
      this.last.controller.abort();
      this.last = undefined;
      this.view.setBusy(false);
    }
  }

  /** The problem of `text`, from the check of it already made or running, else from a new one. */
  private check(text: string): Promise<string | undefined> {
    if (this.last?.text === text) {
      return this.last.problem;
    }
    this.cancel();
    const controller = new AbortController();
    this.view.setBusy(true);
    const problem = this.actions.check(text, controller.signal).then(
      (found) => found,
      () => undefined,
    );
    this.last = { text, controller, problem };
    void problem.then((found) => {
      if (!controller.signal.aborted) {
        this.view.setBusy(false);
        this.view.setProblem(found);
      }
    }); // Never rejects: a failed check counts as no problem, and Enter's set reports it.
    return problem;
  }
}
