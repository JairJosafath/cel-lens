import { SHOW_LOGS_LABEL } from './button_labels';
import {
  EDIT_TYPE_COMMAND,
  ENGINE_CHECK_TYPE,
  ENGINE_REMOVE_TYPE,
  ENGINE_SET_TYPE,
  ENGINE_TYPE_CHOICES,
  GIVE_TYPE_COMMAND,
  INT_OR_STRING_LINES,
  INT_OR_STRING_PLACEHOLDER,
  KIND_PLACEHOLDER,
  REMOVE_TYPE_COMMAND,
  STRUCT_PLACEHOLDER,
  TYPE_PROMPT,
  VALUE_PICK_PLACEHOLDER,
  VALUE_PICK_TITLE,
  WRITE_STRUCT_LINE,
  editTypeTitle,
  engineFailure,
  giveTypeTitle,
  inCrd,
  isChangeAnswer,
  isCurrentChoice,
  lineValues,
  parseTypeChoices,
  parseTypeProblem,
  parseTypeTarget,
  problemText,
  typeLines,
  typedType,
  valueLines,
  withKind,
  withType,
  type PickLine,
  type TypeChoice,
  type TypeTarget,
} from './value_types';

/** Runs one of the engine's commands with `workspace/executeCommand`; `signal` cancels it. */
export type EngineCommand = (
  command: string,
  argument?: unknown,
  signal?: AbortSignal,
) => Promise<unknown>;

/** A quick pick to show: its lines, and the one to start on. */
export interface PickRequest<T> {
  readonly title: string;
  readonly placeholder: string;
  /** The lines, or the promise of them: the picker shows at once, with `first`, busy until they come. */
  readonly lines: readonly PickLine<T>[] | Promise<readonly PickLine<T>[]>;
  readonly first?: readonly PickLine<T>[];
  /** Whether a line stands for what the value has now, for the picker to start on. */
  readonly active?: (value: T) => boolean;
}

/** An input box to show, which checks its text as the person types and sets it on Enter. */
export interface WriteRequest {
  readonly title: string;
  readonly placeholder: string;
  readonly prompt: string;
  readonly value: string;
  check(text: string, signal: AbortSignal): Promise<string | undefined>;
  accept(text: string): Promise<string | undefined>;
}

/** The line picked, and what was typed in the filter. */
export interface Picked<T> {
  readonly value: T;
  readonly typed: string;
}

/** How the input box closed: by Enter or Escape, or by its Pick a kind… button. */
export type WriteEnd = 'closed' | 'pickKind';

/** What the flow shows a person; the extension shows it with VS Code. */
export interface TypeUi {
  /** Shows the quick pick; the line picked, or `undefined` on Escape. */
  pick<T>(request: PickRequest<T>): Promise<Picked<T> | undefined>;
  /**
   * Shows the input box until it is closed: by Enter on a type that was set, by Escape, or by
   * its Pick a kind… button.
   */
  write(request: WriteRequest): Promise<WriteEnd>;
  inform(message: string): Promise<void>;
  /** Shows an error once; `offerLogs` adds the button that opens the logs. */
  showError(message: string, offerLogs: boolean): Promise<void>;
  openExperimentalSetting(): Promise<void>;
}

export interface FlowLog {
  warn(message: string): void;
  error(message: string): void;
}

/** What the palette says when no rule of the file reads a value a type can be given to. */
export const NO_VALUES_MESSAGE = 'No CEL rule in this file reads a value that can be given a type.';

/** What the palette says when the lines the values come from are off for the file. */
export const INLINE_INFO_OFF_MESSAGE =
  'The values come from the line above each CEL rule, which is off for this file: turn it on with cel-lens: Toggle Inline Info.';

function sentence(text: string): string {
  return /[.!?]$/.test(text) ? text : `${text}.`;
}

/**
 * Asks a person what type a `dyn` value has, and has the engine write it into `.cellens`:
 * `Give it a type` and `Edit` show a picker of a struct to write and the kinds there are, or an
 * input box that checks the type as it is typed; `Remove` removes the entry at once. The engine
 * edits `.cellens` through the editor, so Undo there takes a change back, and it refreshes the
 * lines above the rules itself.
 */
export class TypeFlow {
  constructor(
    private readonly engine: EngineCommand,
    private readonly ui: TypeUi,
    private readonly log: FlowLog,
  ) {}

  /** `cel-lens.giveType`: the picker, titled `Give self.spec.config a type`. */
  async giveType(argument: unknown): Promise<void> {
    const target = this.target(GIVE_TYPE_COMMAND, argument);
    if (target !== undefined) {
      await this.choose(target, giveTypeTitle(target.value), `give ${target.value} a type`);
    }
  }

  /**
   * `cel-lens.editType`: the input box filled with the type a policy's value has, or the picker
   * on the kind it has, or on the int or string of a CRD's value.
   */
  async editType(argument: unknown): Promise<void> {
    const target = this.target(EDIT_TYPE_COMMAND, argument);
    if (target === undefined) {
      return;
    }
    const title = editTypeTitle(target.value);
    const action = `edit the type of ${target.value}`;
    if (target.type !== undefined && !inCrd(target)) {
      await this.writeOrPick(target, title, action, target.type);
      return;
    }
    await this.choose(target, title, action);
  }

  /** `cel-lens.removeType`: removes the entry at once. */
  async removeType(argument: unknown): Promise<void> {
    const target = this.target(REMOVE_TYPE_COMMAND, argument);
    if (target !== undefined) {
      await this.change(ENGINE_REMOVE_TYPE, target.argument, `remove the type of ${target.value}`);
    }
  }

  /**
   * The palette's `cel-lens: Give a Value a Type…`: lists the values the lines above the rules of
   * the file name, from its code lenses, and gives the one picked a type, or edits it.
   */
  async giveValueType(lenses: unknown, inlineInfo: boolean): Promise<void> {
    const values = lineValues(lenses);
    if (values.length === 0) {
      await this.ui.inform(inlineInfo ? NO_VALUES_MESSAGE : INLINE_INFO_OFF_MESSAGE);
      return;
    }
    const picked = await this.ui.pick({
      title: VALUE_PICK_TITLE,
      placeholder: VALUE_PICK_PLACEHOLDER,
      lines: valueLines(values),
    });
    if (picked === undefined) {
      return;
    }
    const { command, target } = picked.value;
    await (command === EDIT_TYPE_COMMAND
      ? this.editType(target.argument)
      : this.giveType(target.argument));
  }

  private target(command: string, argument: unknown): TypeTarget | undefined {
    const target = parseTypeTarget(argument);
    if (target === undefined) {
      this.log.warn(`${command} was given no value; the engine may not match this extension.`);
    }
    return target;
  }

  /**
   * The picker, on the value's type or kind when it has one: the type or kind picked is set, and
   * `Write a struct…` opens the input box. Picking what the value has already changes nothing.
   */
  private async choose(target: TypeTarget, title: string, action: string): Promise<void> {
    const picked = await this.ui.pick<TypeChoice>({
      title,
      placeholder: inCrd(target) ? INT_OR_STRING_PLACEHOLDER : KIND_PLACEHOLDER,
      lines: inCrd(target) ? INT_OR_STRING_LINES : this.kindLines(target),
      first: [WRITE_STRUCT_LINE],
      active: isCurrentChoice(target),
    });
    const choice = picked?.value;
    switch (choice?.action) {
      case undefined:
        return;
      case 'write':
        await this.writeOrPick(
          target,
          title,
          action,
          typedType(picked?.typed ?? '') ?? target.type ?? '',
        );
        return;
      case 'type':
        if (choice.type !== target.type) {
          await this.change(ENGINE_SET_TYPE, withType(target, choice.type), action);
        }
        return;
      case 'kind':
        if (choice.kind !== target.kind) {
          await this.change(ENGINE_SET_TYPE, withKind(target, choice.kind), action);
        }
        return;
      case 'openCrdSetting':
        await this.ui.openExperimentalSetting();
        return;
    }
  }

  /**
   * The picker's lines from `cel-lens.typeChoices`. When the engine cannot list the kinds, the
   * picker keeps `Write a struct…` and the failure is shown once, beside it.
   */
  private async kindLines(target: TypeTarget): Promise<readonly PickLine<TypeChoice>[]> {
    let answer: unknown;
    try {
      answer = await this.engine(ENGINE_TYPE_CHOICES);
    } catch (error) {
      // Not awaited: a notification stays pending while it is open, and the picker is open.
      void this.report(`list the kinds for ${target.value}`, error);
      return [WRITE_STRUCT_LINE];
    }
    const choices = parseTypeChoices(answer);
    if (choices === undefined) {
      this.log.warn(
        `${ENGINE_TYPE_CHOICES} gave no kinds; the engine may not match this extension.`,
      );
      return [WRITE_STRUCT_LINE];
    }
    return typeLines(choices);
  }

  /** The input box, then the picker when its Pick a kind… button closed it. */
  private async writeOrPick(
    target: TypeTarget,
    title: string,
    action: string,
    value: string,
  ): Promise<void> {
    if ((await this.write(target, title, action, value)) === 'pickKind') {
      await this.choose(target, title, action);
    }
  }

  /**
   * The input box; it resolves once the box is closed and a type Enter set is set, so a failure
   * of a box closed while the engine set its type is shown as a notification.
   */
  private async write(
    target: TypeTarget,
    title: string,
    action: string,
    value: string,
  ): Promise<WriteEnd> {
    const box = { open: true };
    let setting: Promise<unknown> = Promise.resolve();
    const end = await this.ui.write({
      title,
      placeholder: STRUCT_PLACEHOLDER,
      prompt: TYPE_PROMPT,
      value,
      check: (text, signal) => this.check(target, text, signal),
      accept: (text) => {
        if (text === target.type) {
          return Promise.resolve(undefined);
        }
        const set = this.setWritten(target, text, action, box);
        setting = set;
        return set;
      },
    });
    box.open = false;
    await setting;
    return end;
  }

  /** What `cel-lens.checkType` finds wrong with `text` for the value; a failed check shows nothing. */
  private async check(
    target: TypeTarget,
    text: string,
    signal: AbortSignal,
  ): Promise<string | undefined> {
    let answer: unknown;
    try {
      answer = await this.engine(ENGINE_CHECK_TYPE, withType(target, text), signal);
    } catch (error) {
      const failure = engineFailure(error);
      switch (failure.kind) {
        case 'cancelled':
          return undefined;
        case 'refused':
          return failure.reason;
        case 'other':
          this.log.error(`Could not check the type of ${target.value}: ${failure.reason}`);
          return undefined;
      }
    }
    const problem = parseTypeProblem(answer);
    if (problem === undefined) {
      this.log.warn(
        `${ENGINE_CHECK_TYPE} gave no answer it documents; the engine may not match this extension.`,
      );
    }
    return problem === null || problem === undefined ? undefined : problemText(problem);
  }

  /**
   * Sets the type written in the input box: `undefined` once set, else what the box shows; once
   * the box is closed, a notification shows it instead.
   */
  private async setWritten(
    target: TypeTarget,
    text: string,
    action: string,
    box: { readonly open: boolean },
  ): Promise<string | undefined> {
    try {
      this.checkChange(ENGINE_SET_TYPE, await this.engine(ENGINE_SET_TYPE, withType(target, text)));
      return undefined;
    } catch (error) {
      if (!box.open) {
        await this.report(action, error);
        return undefined;
      }
      const failure = engineFailure(error);
      switch (failure.kind) {
        case 'cancelled':
          return undefined;
        case 'refused':
          this.log.warn(`Could not ${action}: ${failure.reason}`);
          return sentence(failure.reason);
        case 'other':
          this.log.error(`Could not ${action}: ${failure.reason}`);
          return `cel-lens could not ${action}. Run cel-lens: ${SHOW_LOGS_LABEL} for the reason.`;
      }
    }
  }

  /** Sets or removes the entry; a failure is shown once. */
  private async change(command: string, argument: unknown, action: string): Promise<void> {
    try {
      this.checkChange(command, await this.engine(command, argument));
    } catch (error) {
      await this.report(action, error);
    }
  }

  private checkChange(command: string, answer: unknown): void {
    if (!isChangeAnswer(answer)) {
      this.log.warn(
        `${command} gave no answer it documents; the engine may not match this extension.`,
      );
    }
  }

  /** Says once that `action` failed: the engine's reason when it refused, else a button to the logs. */
  private async report(action: string, error: unknown): Promise<void> {
    const failure = engineFailure(error);
    switch (failure.kind) {
      case 'cancelled':
        return;
      case 'refused':
        this.log.warn(`Could not ${action}: ${failure.reason}`);
        await this.ui.showError(sentence(`cel-lens could not ${action}: ${failure.reason}`), false);
        return;
      case 'other':
        this.log.error(`Could not ${action}: ${failure.reason}`);
        await this.ui.showError(
          `cel-lens could not ${action}. Show the logs for the reason.`,
          true,
        );
        return;
    }
  }
}
