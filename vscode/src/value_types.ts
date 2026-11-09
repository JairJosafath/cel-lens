import { isRecord } from './is_record';
import { LIST_VALUES_COMMAND, listValuesArguments, parseMoreValues } from './rule_parts';

/** The commands of the parts of the line above a rule that give a `dyn` value a type; the engine names them. */
export const GIVE_TYPE_COMMAND = 'cel-lens.giveType';
export const EDIT_TYPE_COMMAND = 'cel-lens.editType';
export const REMOVE_TYPE_COMMAND = 'cel-lens.removeType';

/** The palette command that lists the values of the open file a type can be given to. */
export const GIVE_VALUE_TYPE_COMMAND = 'cel-lens.giveValueType';

/**
 * The engine's commands, which the extension runs with `workspace/executeCommand`. The engine's
 * `cel-lens.removeType` has the name of the line's part, and takes the same argument.
 */
export const ENGINE_TYPE_CHOICES = 'cel-lens.typeChoices';
export const ENGINE_CHECK_TYPE = 'cel-lens.checkType';
export const ENGINE_SET_TYPE = 'cel-lens.setType';
export const ENGINE_REMOVE_TYPE = 'cel-lens.removeType';

/** The LSP error codes the engine refuses a command with, and the one a cancelled request gets. */
const INVALID_PARAMS = -32602;
const REQUEST_FAILED = -32803;
const REQUEST_CANCELLED = -32800;

/** The kind of a document whose values are `x-kubernetes-int-or-string`: they take an int or a string. */
const CRD_DOCUMENT_PREFIX = 'CustomResourceDefinition ';

/** The keys that name a value as a `.cellens` entry does, in the engine's order. */
const VALUE_KEYS = ['cellens', 'file', 'document', 'version', 'variable', 'path'] as const;

/** A value the line above a rule names: the argument of its Give it a type, Edit and Remove parts. */
export interface TypeTarget {
  /** The engine's argument as it came: the engine gets it back, with the type or kind chosen. */
  readonly argument: Readonly<Record<string, unknown>>;
  /** The value's CEL, such as `self.spec.config`, for titles and messages. */
  readonly value: string;
  /** The document the value is in, such as `CustomResourceDefinition widgets.example.com`. */
  readonly document: string;
  /** A CustomResourceDefinition's version, such as `v1`. */
  readonly version?: string;
  /** The type an entry gives the value now, such as `{name: string}`. */
  readonly type?: string;
  /** The kind an entry gives the value now, such as `apps/v1 Deployment`. */
  readonly kind?: string;
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

/**
 * Reads the argument of `cel-lens.giveType`, `cel-lens.editType` and `cel-lens.removeType`:
 * `{cellens, file, document, version or variable, path, value, type or kind}`. Without a
 * `cellens`, `file` or `document` it names no value and gives `undefined`.
 */
export function parseTypeTarget(argument: unknown): TypeTarget | undefined {
  if (!isRecord(argument)) {
    return undefined;
  }
  const document = text(argument['document']);
  if (text(argument['cellens']) === undefined || text(argument['file']) === undefined) {
    return undefined;
  }
  if (document === undefined) {
    return undefined;
  }
  const version = text(argument['version']);
  const type = text(argument['type']);
  const kind = text(argument['kind']);
  return {
    argument,
    value: text(argument['value']) ?? 'this value',
    document,
    ...(version !== undefined && { version }),
    ...(type !== undefined && { type }),
    ...(kind !== undefined && { kind }),
  };
}

/** Whether the value is in a CustomResourceDefinition, where only `int` and `string` fit. */
export function inCrd(target: TypeTarget): boolean {
  return target.document.startsWith(CRD_DOCUMENT_PREFIX);
}

function withoutTypeOrKind(target: TypeTarget): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(target.argument).filter(([key]) => key !== 'type' && key !== 'kind'),
  );
}

/** The engine's argument with `type`, and no `kind`: the engine takes exactly one. */
export function withType(target: TypeTarget, type: string): Record<string, unknown> {
  return { ...withoutTypeOrKind(target), type };
}

/** The engine's argument with `kind`, and no `type`: the engine takes exactly one. */
export function withKind(target: TypeTarget, kind: string): Record<string, unknown> {
  return { ...withoutTypeOrKind(target), kind };
}

/** The title of the steps that give a value its first type, such as `Give self.spec.config a type`. */
export function giveTypeTitle(value: string): string {
  return `Give ${value} a type`;
}

/** The title of the steps that change a value's type, such as `Edit the type of self.spec.config`. */
export function editTypeTitle(value: string): string {
  return `Edit the type of ${value}`;
}

/** Whether the Kubernetes API version of a built-in kind is generally available, beta or alpha. */
export type Stability = 'ga' | 'beta' | 'alpha';

/** A kind a `kind:` entry can name, as `cel-lens.typeChoices` lists it. */
export interface KindChoice {
  /** Its group and version, such as `apps/v1`, or `v1` for the core group. */
  readonly apiVersion: string;
  readonly kind: string;
  /** One line about the kind. */
  readonly description: string;
  readonly stability: Stability;
}

/** The answer of `cel-lens.typeChoices`. */
export interface TypeChoices {
  readonly kubernetesVersion: string;
  readonly builtIn: readonly KindChoice[];
  readonly crds: readonly KindChoice[];
  /** Why `crds` is empty, when the engine does not read the workspace's CRDs. */
  readonly crdsNote?: string;
}

function parseKindChoice(value: unknown): KindChoice | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const apiVersion = text(value['apiVersion']);
  const kind = text(value['kind']);
  if (apiVersion === undefined || kind === undefined) {
    return undefined;
  }
  const stability = value['stability'];
  return {
    apiVersion,
    kind,
    description: text(value['description']) ?? '',
    stability: stability === 'alpha' || stability === 'beta' ? stability : 'ga',
  };
}

function parseKindChoices(value: unknown): KindChoice[] {
  return Array.isArray(value)
    ? value.map(parseKindChoice).filter((choice) => choice !== undefined)
    : [];
}

/** Reads the answer of `cel-lens.typeChoices`; one without a `builtIn` list gives `undefined`. */
export function parseTypeChoices(value: unknown): TypeChoices | undefined {
  if (!isRecord(value) || !Array.isArray(value['builtIn'])) {
    return undefined;
  }
  const crdsNote = text(value['crdsNote']);
  return {
    kubernetesVersion: text(value['kubernetesVersion']) ?? '',
    builtIn: parseKindChoices(value['builtIn']),
    crds: parseKindChoices(value['crds']),
    ...(crdsNote !== undefined && { crdsNote }),
  };
}

/** A kind as a `kind:` entry names it, such as `apps/v1 Deployment`. */
export function kindName(choice: KindChoice): string {
  return `${choice.apiVersion} ${choice.kind}`;
}

/** What picking a line of the type picker does. */
export type TypeChoice =
  | { readonly action: 'write' }
  | { readonly action: 'type'; readonly type: string }
  | { readonly action: 'kind'; readonly kind: string }
  | { readonly action: 'openCrdSetting' };

/** A line that names a group of the lines below it. */
export interface PickSeparator {
  readonly separator: true;
  readonly label: string;
}

/** A line a person can pick, and what it stands for. */
export interface PickEntry<T> {
  readonly separator?: false;
  readonly label: string;
  readonly description?: string;
  readonly detail?: string;
  /** Shown whatever is typed in the filter, which it then takes. */
  readonly alwaysShow?: true;
  readonly value: T;
}

/** One line of a quick pick, without VS Code's types. */
export type PickLine<T> = PickSeparator | PickEntry<T>;

/**
 * The first line of the type picker: write the type, a struct most often. It stays in the list
 * whatever is typed, so a struct typed in the filter is the line Enter picks, and the input box
 * starts with it.
 */
export const WRITE_STRUCT_LINE: PickEntry<TypeChoice> = {
  label: '$(edit) Write a struct…',
  detail: 'Its fields and their types, such as {name: string, replicas: int}, or any CEL type',
  alwaysShow: true,
  value: { action: 'write' },
};

/** The CEL type names a type can be, as `.cellens` writes them. */
const TYPE_NAMES = ['string', 'int', 'uint', 'double', 'bool', 'bytes', 'timestamp', 'duration'];

/**
 * What was typed in the type picker's filter, when it reads as a type for the input box to start
 * with, such as `{name: string}`, `list(int)` or `string`; `undefined` for the name of a kind.
 */
export function typedType(filter: string): string | undefined {
  const typed = filter.trim();
  return /^(\{|list\(|map\()/.test(typed) || TYPE_NAMES.includes(typed) ? typed : undefined;
}

/** The placeholder of the type picker of a policy's value. */
export const KIND_PLACEHOLDER = 'Write a struct, or type to find a kind such as Deployment';

/** The placeholder of the type picker of a CRD's value. */
export const INT_OR_STRING_PLACEHOLDER =
  'An x-kubernetes-int-or-string value is an int or a string to a rule';

/** The tooltip of the input box's button that turns to the picker, so a struct can become a kind. */
export const PICK_KIND_TOOLTIP = 'Pick a kind…';

/** The placeholder of the input box: a struct, the type people write most. */
export const STRUCT_PLACEHOLDER = '{name: string, replicas: int}';

/** The prompt under the input box: the types it takes. */
export const TYPE_PROMPT =
  'A CEL type: string, int, bool, double, bytes, timestamp, duration, list(T), map(string, T) or {field: T, ...}';

/** The icons that tell a built-in kind from a kind a CRD of the workspace defines. */
const BUILT_IN_ICON = '$(symbol-class)';
const CRD_ICON = '$(file-code)';

/** The two lines of the type picker of a CRD's value: an `x-kubernetes-int-or-string` value is an int or a string. */
export const INT_OR_STRING_LINES: readonly PickLine<TypeChoice>[] = [
  {
    label: '$(symbol-numeric) int',
    detail: 'A whole number, such as 3',
    value: { action: 'type', type: 'int' },
  },
  {
    label: '$(symbol-string) string',
    detail: 'A string, such as "25%"',
    value: { action: 'type', type: 'string' },
  },
];

function kindLine(choice: KindChoice, icon: string): PickEntry<TypeChoice> {
  return {
    label: `${icon} ${choice.kind}`,
    description:
      choice.stability === 'ga' ? choice.apiVersion : `${choice.apiVersion} · ${choice.stability}`,
    ...(choice.description !== '' && { detail: choice.description }),
    value: { action: 'kind', kind: kindName(choice) },
  };
}

function crdLines(choices: TypeChoices): PickLine<TypeChoice>[] {
  const separator: PickSeparator = { separator: true, label: 'Workspace CRDs' };
  if (choices.crds.length > 0) {
    return [separator, ...choices.crds.map((choice) => kindLine(choice, CRD_ICON))];
  }
  if (choices.crdsNote === undefined) {
    return [];
  }
  return [
    separator,
    {
      label: '$(info) Workspace CRDs are not listed',
      description: 'pick to open the setting',
      detail: choices.crdsNote,
      value: { action: 'openCrdSetting' },
    },
  ];
}

/**
 * The lines of the type picker of a policy's value: `Write a struct…` first, then the built-in
 * kinds and the kinds of the workspace's CRDs, each group under its separator. A kind's
 * description is its group and version, marked `beta` or `alpha` when it is not GA.
 */
export function typeLines(choices: TypeChoices): PickLine<TypeChoice>[] {
  const builtIn: PickLine<TypeChoice>[] =
    choices.builtIn.length === 0
      ? []
      : [
          {
            separator: true,
            label:
              choices.kubernetesVersion === ''
                ? 'Built-in kinds'
                : `Built-in kinds · Kubernetes ${choices.kubernetesVersion}`,
          },
          ...choices.builtIn.map((choice) => kindLine(choice, BUILT_IN_ICON)),
        ];
  return [WRITE_STRUCT_LINE, ...builtIn, ...crdLines(choices)];
}

/** Whether a choice is the value's type or kind now, for an edit to start on; none is for `dyn`. */
export function isCurrentChoice(target: TypeTarget): (choice: TypeChoice) => boolean {
  return (choice) =>
    (choice.action === 'kind' && choice.kind === target.kind) ||
    (choice.action === 'type' && choice.type === target.type);
}

/** What `cel-lens.checkType` finds wrong with a type: `column` counts characters from 1. */
export interface TypeProblem {
  readonly message: string;
  readonly column?: number;
}

/**
 * Reads the answer of `cel-lens.checkType`: `null` for a type that is fine, else its problem.
 * Anything else gives `undefined`.
 */
export function parseTypeProblem(value: unknown): TypeProblem | null | undefined {
  if (value === null) {
    return null;
  }
  const message = isRecord(value) ? text(value['message']) : undefined;
  if (!isRecord(value) || message === undefined) {
    return undefined;
  }
  const column = value['column'];
  return typeof column === 'number' && Number.isInteger(column) && column > 0
    ? { message, column }
    : { message };
}

/** The problem as the input box shows it, such as `unknown type "strin": use string, int, ... (character 8)`. */
export function problemText(problem: TypeProblem): string {
  return problem.column === undefined
    ? problem.message
    : `${problem.message} (character ${String(problem.column)})`;
}

/** Whether a value is the answer of `cel-lens.setType` or `cel-lens.removeType`: `{cellens, change}`. */
export function isChangeAnswer(value: unknown): boolean {
  return (
    isRecord(value) && text(value['cellens']) !== undefined && text(value['change']) !== undefined
  );
}

/** Why an engine command failed: refused with a reason a person can act on, cancelled, or another error. */
export type EngineFailure =
  | { readonly kind: 'refused'; readonly reason: string }
  | { readonly kind: 'cancelled' }
  | { readonly kind: 'other'; readonly reason: string };

/** The engine's reason without the command it starts with, as in `cel-lens.setType: give type or kind`. */
function plainReason(message: string): string {
  return message.replace(/^cel-lens\.[A-Za-z]+: /, '').replace(/\.$/, '');
}

/**
 * Reads what the language client rejected an engine command with: a ResponseError has a code and
 * a message. The reason of another error is for the log: its message, and its code if it has one.
 */
export function engineFailure(error: unknown): EngineFailure {
  const code = isRecord(error) && typeof error['code'] === 'number' ? error['code'] : undefined;
  const message = isRecord(error) ? text(error['message']) : undefined;
  if (code === REQUEST_CANCELLED) {
    return { kind: 'cancelled' };
  }
  if ((code === REQUEST_FAILED || code === INVALID_PARAMS) && message !== undefined) {
    return { kind: 'refused', reason: plainReason(message) };
  }
  const reason = message ?? String(error);
  return {
    kind: 'other',
    reason: code === undefined ? reason : `${reason} (error ${String(code)})`,
  };
}

/** A value the lines above the rules of a file name, with the part that runs on it and the line from 1. */
export interface LineValue {
  readonly target: TypeTarget;
  readonly command: typeof GIVE_TYPE_COMMAND | typeof EDIT_TYPE_COMMAND;
  readonly line: number;
}

/** The parts of one code lens that give a value a type or edit it, with their argument. */
function typeParts(lens: Record<string, unknown>, line: number): [string, unknown][] {
  const command = lens['command'];
  if (!isRecord(command) || typeof command['command'] !== 'string') {
    return [];
  }
  const args = Array.isArray(command['arguments']) ? command['arguments'] : [];
  if (command['command'] === LIST_VALUES_COMMAND) {
    const more = parseMoreValues(listValuesArguments(line - 1, args)[0]);
    return (more?.parts ?? []).map((part) => [part.command, part.arguments[0]]);
  }
  return [[command['command'], args[0]]];
}

function lensLine(lens: Record<string, unknown>): number | undefined {
  const range = lens['range'];
  const start = isRecord(range) ? range['start'] : undefined;
  const line = isRecord(start) ? start['line'] : undefined;
  return typeof line === 'number' && Number.isInteger(line) && line >= 0 ? line + 1 : undefined;
}

/**
 * The values the lines above the rules of a file name, from its code lenses as
 * `vscode.executeCodeLensProvider` gives them: each `Give it a type` and `Edit` part, also those
 * `+N more` holds, each value once, in the order of the lines.
 */
export function lineValues(lenses: unknown): LineValue[] {
  const values = new Map<string, LineValue>();
  for (const lens of Array.isArray(lenses) ? lenses : []) {
    const line = isRecord(lens) ? lensLine(lens) : undefined;
    if (!isRecord(lens) || line === undefined) {
      continue;
    }
    for (const [command, argument] of typeParts(lens, line)) {
      const target = parseTypeTarget(argument);
      if (
        target === undefined ||
        (command !== GIVE_TYPE_COMMAND && command !== EDIT_TYPE_COMMAND)
      ) {
        continue;
      }
      const key = JSON.stringify(VALUE_KEYS.map((name) => target.argument[name] ?? ''));
      if (!values.has(key)) {
        values.set(key, { target, command, line });
      }
    }
  }
  return [...values.values()];
}

/** The title of the palette's list of values. */
export const VALUE_PICK_TITLE = 'Give a value a type';

/** The placeholder of the palette's list of values. */
export const VALUE_PICK_PLACEHOLDER = 'Pick a value to give a type; dyn means it has none yet';

/** Each value as a line of the palette's list: its CEL, its type now, and where it is. */
export function valueLines(values: readonly LineValue[]): PickEntry<LineValue>[] {
  return values.map((value) => ({
    label: value.target.value,
    description: value.target.type ?? value.target.kind ?? 'dyn',
    detail: `${[value.target.document, value.target.version].filter((part) => part !== undefined).join(' ')} · line ${String(value.line)}`,
    value,
  }));
}
