import {
  EDIT_TYPE_COMMAND,
  ENGINE_REMOVE_TYPE,
  ENGINE_SET_TYPE,
  ENGINE_TYPE_CHOICES,
  GIVE_TYPE_COMMAND,
  GO_TYPES_FAILED_LINE,
  GO_TYPE_PLACEHOLDER,
  INT_OR_STRING_LINES,
  INT_OR_STRING_PLACEHOLDER,
  KIND_PLACEHOLDER,
  OUTDATED_ENGINE_LINE,
  REMOVE_TYPE_COMMAND,
  VALUE_PICK_PLACEHOLDER,
  VALUE_PICK_TITLE,
  editTypeTitle,
  engineFailure,
  firstLines,
  giveTypeTitle,
  goTypeLines,
  goTypesIn,
  hasEntry,
  inCrd,
  isChangeAnswer,
  isCurrentChoice,
  lineValues,
  parentFolder,
  parseEntryPlace,
  parseTypeChoices,
  parseTypeTarget,
  snippetRefusal,
  sameGo,
  typeLines,
  valueLines,
  withGo,
  withKind,
  withSnippet,
  withType,
  type PickLine,
  type SnippetForm,
  type TextRange,
  type TypeChoice,
  type TypeChoices,
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
  /** What the filter starts with. */
  readonly filter?: string;
  /** Whether the picker is a step after another, with a Back button to it. */
  readonly back?: true;
}

/** What the Back button of a picker gives. */
export const BACK = 'back';

/** The line picked, and what was typed in the filter. */
export interface Picked<T> {
  readonly value: T;
  readonly typed: string;
}

/** What the flow shows a person; the extension shows it with VS Code. */
export interface TypeUi {
  /** Shows the quick pick; the line picked, `BACK` for its Back button, or `undefined` on Escape. */
  pick<T>(request: PickRequest<T>): Promise<Picked<T> | typeof BACK | undefined>;
  /**
   * Opens `cellens` beside the rule's editor, or in the editor group that already shows it, with
   * `selection` selected and the focus there, so the person types the type at once.
   */
  openCellens(cellens: string, selection: TextRange): Promise<void>;
  /** A folder's path relative to its workspace folder, such as `api/v1`. */
  workspacePath(uri: string): string;
  /** The URI of the workspace folder `uri` is in, if any. */
  workspaceFolder(uri: string): string | undefined;
  inform(message: string): Promise<void>;
  /** Shows an error once; `offerLogs` adds the button that opens the logs. */
  showError(message: string, offerLogs: boolean): Promise<void>;
  openExperimentalSetting(): Promise<void>;
  /** Opens the setting `cel-lens.serverPath`, which picks the engine. */
  openServerPathSetting(): Promise<void>;
  /** Says once that the engine is too old for this, with a button to `cel-lens.serverPath`. */
  showOutdatedEngine(): Promise<void>;
  showLogs(): void;
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
 * `Give it a type` and `Edit` show a picker. `Write a schema in .cellens…` and
 * `Write a short type in .cellens…` have the engine write an entry to fill in, or find the one
 * the value has, and open `.cellens` beside the rule there; a Go type, a kind, or a CRD value's
 * `int` or `string` is set at once. `Remove` removes the entry at once. The engine edits
 * `.cellens` through the editor, so Undo there takes a change back, and it refreshes the lines
 * above the rules itself, also as the person types in `.cellens`.
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

  /** `cel-lens.editType`: the same picker, on what the value has now. */
  async editType(argument: unknown): Promise<void> {
    const target = this.target(EDIT_TYPE_COMMAND, argument);
    if (target !== undefined) {
      await this.choose(target, editTypeTitle(target.value), `edit the type of ${target.value}`);
    }
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
    if (picked === undefined || picked === BACK) {
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
   * The picker, on what the value has now when it has a type. A policy's value asks the engine
   * for its kinds and Go types, which the `Use a Go type…` step shares, keeping the Go types of
   * the workspace folder of its `.cellens`, as a `go:` entry names no other; once the flow ends, a
   * list still to come is cancelled. Nothing waits for that list but the pickers that show it:
   * the engine answers once its walk of the Go code ends. Picking what the value has already
   * changes nothing. Back from the Go types returns to this picker with what was typed in it.
   */
  private async choose(target: TypeTarget, title: string, action: string): Promise<void> {
    const listing = new AbortController();
    const root = this.ui.workspaceFolder(target.cellens) ?? parentFolder(target.cellens);
    let listed: TypeChoices | undefined;
    const choices = inCrd(target)
      ? undefined
      : this.typeChoices(target, listing.signal).then((found) => {
          listed = found === undefined ? undefined : goTypesIn(found, root);
          return listed;
        });
    const outdated = (): boolean => listed?.outdated === true;
    const entry = hasEntry(target);
    let filter = '';
    try {
      for (;;) {
        const picked = await this.ui.pick<TypeChoice>({
          title,
          placeholder: choices === undefined ? INT_OR_STRING_PLACEHOLDER : KIND_PLACEHOLDER,
          lines:
            choices === undefined
              ? INT_OR_STRING_LINES
              : choices.then((found) =>
                  found === undefined ? firstLines(entry) : typeLines(found, entry),
                ),
          first: firstLines(entry),
          active: isCurrentChoice(target),
          filter,
        });
        if (picked === undefined || picked === BACK) {
          return;
        }
        if (picked.value.action === 'pickGo' && choices !== undefined && !outdated()) {
          const step = await this.pickGoType(
            target,
            title,
            action,
            choices,
            picked.typed,
            outdated,
          );
          if (step === BACK) {
            filter = picked.typed;
            continue;
          }
          return;
        }
        await this.apply(target, picked.value, action, outdated);
        return;
      }
    } finally {
      listing.abort();
    }
  }

  /**
   * The second step of `Use a Go type…`: the Go types, filtered by what was typed, or why there
   * are none to list; its Back button returns to the first.
   */
  private async pickGoType(
    target: TypeTarget,
    title: string,
    action: string,
    choices: Promise<TypeChoices | undefined>,
    typed: string,
    outdated: () => boolean,
  ): Promise<typeof BACK | undefined> {
    const picked = await this.ui.pick<TypeChoice>({
      title: `${title} › Go type`,
      placeholder: GO_TYPE_PLACEHOLDER,
      lines: choices.then((found) =>
        found === undefined
          ? [GO_TYPES_FAILED_LINE]
          : found.outdated === true
            ? [OUTDATED_ENGINE_LINE]
            : goTypeLines(found, target.cellens, (folder) => this.ui.workspacePath(folder)),
      ),
      first: [],
      active: isCurrentChoice(target),
      filter: typed,
      back: true,
    });
    if (picked === BACK) {
      return BACK;
    }
    if (picked !== undefined) {
      await this.apply(target, picked.value, action, outdated);
    }
    return undefined;
  }

  /**
   * Does what the line picked says, at once. `outdated` says whether the engine has already
   * answered that it is too old for the lines that write in `.cellens` and the Go types; one that
   * has not answered yet refuses a `snippet` itself.
   */
  private async apply(
    target: TypeTarget,
    choice: TypeChoice,
    action: string,
    outdated: () => boolean,
  ): Promise<void> {
    switch (choice.action) {
      case 'write':
        if (outdated()) {
          await this.ui.showOutdatedEngine();
          return;
        }
        await this.writeInCellens(target, choice.form, action);
        return;
      case 'go':
        if (target.go === undefined || !sameGo(choice.go, target.go)) {
          await this.change(ENGINE_SET_TYPE, withGo(target, choice.go), action);
        }
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
      case 'openServerPathSetting':
        await this.ui.openServerPathSetting();
        return;
      case 'showLogs':
        this.ui.showLogs();
        return;
      case 'pickGo':
        if (outdated()) {
          await this.ui.showOutdatedEngine();
        }
        return;
      case 'none':
        return;
    }
  }

  /**
   * The kinds and Go types from `cel-lens.typeChoices`, `undefined` when the engine cannot list
   * them: the picker then keeps its first lines, and the failure is shown once, beside it.
   */
  private async typeChoices(
    target: TypeTarget,
    signal: AbortSignal,
  ): Promise<TypeChoices | undefined> {
    let answer: unknown;
    try {
      answer = await this.engine(ENGINE_TYPE_CHOICES, undefined, signal);
    } catch (error) {
      // Not awaited: a notification stays pending while it is open, and the picker is open.
      void this.report(`list the kinds and Go types for ${target.value}`, error);
      return undefined;
    }
    const choices = parseTypeChoices(answer);
    if (choices === undefined) {
      this.log.warn(
        `${ENGINE_TYPE_CHOICES} gave no kinds; the engine may not match this extension.`,
      );
    }
    return choices;
  }

  /**
   * Has the engine write an entry to fill in, a short type or a schema, or find the entry the
   * value has, and opens `.cellens` there once the edit is applied: the engine sends the edit
   * before it answers.
   */
  private async writeInCellens(
    target: TypeTarget,
    form: SnippetForm,
    action: string,
  ): Promise<void> {
    let answer: unknown;
    try {
      answer = await this.engine(ENGINE_SET_TYPE, withSnippet(target, form));
    } catch (error) {
      const refusal = snippetRefusal(error);
      if (refusal !== undefined) {
        this.log.warn(`Could not ${action}, as the engine is too old: ${refusal}`);
        await this.ui.showOutdatedEngine();
        return;
      }
      await this.report(action, error);
      return;
    }
    const place = parseEntryPlace(answer);
    if (place === undefined) {
      this.log.warn(
        `${ENGINE_SET_TYPE} gave no place to write the type; the engine may not match this extension.`,
      );
      return;
    }
    try {
      await this.ui.openCellens(place.cellens, place.selection);
    } catch (error) {
      this.log.error(`Could not open ${place.cellens}: ${String(error)}`);
      await this.ui.showError(
        'cel-lens could not open .cellens. Show the logs for the reason.',
        true,
      );
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
