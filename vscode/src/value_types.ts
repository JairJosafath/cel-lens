import { RESTART_ENGINE_LABEL } from './button_labels';
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
export const ENGINE_INFER_TYPE = 'cel-lens.inferType';
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

/** The keys of an entry's type: the engine takes exactly one of them, or a `snippet` instead. */
const TYPE_KEYS: readonly string[] = ['type', 'kind', 'schema', 'go'];

/** A value the line above a rule names: the argument of its Give it a type, Edit and Remove parts. */
export interface TypeTarget {
  /** The engine's argument as it came: the engine gets it back, with the type or kind chosen. */
  readonly argument: Readonly<Record<string, unknown>>;
  /** The URI of the `.cellens` the value's entry is in or would go in. */
  readonly cellens: string;
  /** The value's CEL, such as `self.spec.config`, for titles and messages. */
  readonly value: string;
  /** The document the value is in, such as `CustomResourceDefinition widgets.example.com`. */
  readonly document: string;
  /** A CustomResourceDefinition's version, such as `v1`. */
  readonly version?: string;
  /** Where the value is below the root of its object or variable, such as `spec.config`. */
  readonly path?: string;
  /** The type an entry gives the value now, such as `{name: string}`. */
  readonly type?: string;
  /** The kind an entry gives the value now, such as `apps/v1 Deployment`. */
  readonly kind?: string;
  /** The Go type an entry gives the value now, as `go:` writes it, such as `./api/v1#QuotaSpec`. */
  readonly go?: string;
  /** Whether an entry gives the value an OpenAPI schema now. */
  readonly schema?: true;
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

/**
 * Reads the argument of `cel-lens.giveType`, `cel-lens.editType` and `cel-lens.removeType`:
 * `{cellens, file, document, version or variable, path, value, type, kind, schema or go}`.
 * Without a `cellens`, `file` or `document` it names no value and gives `undefined`.
 */
export function parseTypeTarget(argument: unknown): TypeTarget | undefined {
  if (!isRecord(argument)) {
    return undefined;
  }
  const cellens = text(argument['cellens']);
  const document = text(argument['document']);
  if (cellens === undefined || text(argument['file']) === undefined) {
    return undefined;
  }
  if (document === undefined) {
    return undefined;
  }
  const version = text(argument['version']);
  const path = text(argument['path']);
  const type = text(argument['type']);
  const kind = text(argument['kind']);
  const go = text(argument['go']);
  return {
    argument,
    cellens,
    value: text(argument['value']) ?? 'this value',
    document,
    ...(version !== undefined && { version }),
    ...(path !== undefined && { path }),
    ...(type !== undefined && { type }),
    ...(kind !== undefined && { kind }),
    ...(go !== undefined && { go }),
    ...(isRecord(argument['schema']) && { schema: true }),
  };
}

/** Whether the value is in a CustomResourceDefinition, where only `int` and `string` fit. */
export function inCrd(target: TypeTarget): boolean {
  return target.document.startsWith(CRD_DOCUMENT_PREFIX);
}

/** The engine's argument that names the value, without the type it has. */
export function withoutType(target: TypeTarget): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(target.argument).filter(([key]) => !TYPE_KEYS.includes(key)),
  );
}

/** The engine's argument with `type`, and no other type the value has: the engine takes exactly one. */
export function withType(target: TypeTarget, type: string): Record<string, unknown> {
  return { ...withoutType(target), type };
}

/** The engine's argument with `kind`, and no other type the value has. */
export function withKind(target: TypeTarget, kind: string): Record<string, unknown> {
  return { ...withoutType(target), kind };
}

/** The engine's argument with `go`, such as `./api/v1#QuotaSpec`, and no other type the value has. */
export function withGo(target: TypeTarget, go: string): Record<string, unknown> {
  return { ...withoutType(target), go };
}

/** The engine's argument with an OpenAPI `schema`, and no other type the value has. */
export function withSchema(
  target: TypeTarget,
  schema: Readonly<Record<string, unknown>>,
): Record<string, unknown> {
  return { ...withoutType(target), schema };
}

/** The form of an entry the engine writes for the person to fill in: a short type or a schema. */
export type SnippetForm = 'type' | 'schema';

/**
 * The engine's argument with `snippet` in place of a type: `cel-lens.setType` then writes a new
 * entry to fill in, or keeps the one the value has, and answers where the cursor goes.
 */
export function withSnippet(target: TypeTarget, snippet: SnippetForm): Record<string, unknown> {
  return { ...withoutType(target), snippet };
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

/** A struct type of the workspace's Go code a `go:` entry can name, as `cel-lens.typeChoices` lists it. */
export interface GoTypeChoice {
  /** The file URI of its package's folder, such as `file:///repo/api/v1`. */
  readonly folder: string;
  readonly name: string;
  /** The first sentence of its doc comment, `''` without one. */
  readonly doc: string;
}

/** A document of the workspace of the value's kind, which the engine can work out a schema from. */
export interface ExampleChoice {
  /** Its file's URI. */
  readonly uri: string;
  /** Its file below the workspace folder, such as `config/samples/limit.yaml`. */
  readonly file: string;
  /** The line the document starts at, from 1. */
  readonly line: number;
  readonly apiVersion: string;
  readonly kind: string;
}

/** The answer of `cel-lens.typeChoices`. */
export interface TypeChoices {
  readonly kubernetesVersion: string;
  readonly builtIn: readonly KindChoice[];
  readonly crds: readonly KindChoice[];
  /** Why `crds` is empty, when the engine does not read the workspace's CRDs. */
  readonly crdsNote?: string;
  readonly goTypes: readonly GoTypeChoice[];
  /** Why not every Go type is listed, when the workspace has more than the engine lists. */
  readonly goTypesNote?: string;
  /**
   * The answer has no `goTypes` key: the engine is older than what this extension needs. A `null`
   * list, as a workspace without Go code may give, is none, not an old engine.
   */
  readonly outdated?: true;
  /** The documents of the workspace of the value's kind. */
  readonly examples: readonly ExampleChoice[];
  /**
   * The answer of a value's line has no `examples` key: the engine cannot work out a schema from
   * an example (`cel-lens.inferType`), which every engine that lists examples can.
   */
  readonly cannotInfer?: true;
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

function parseGoTypeChoice(value: unknown): GoTypeChoice | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const folder = text(value['folder']);
  const name = text(value['name']);
  return folder === undefined || name === undefined
    ? undefined
    : { folder, name, doc: text(value['doc']) ?? '' };
}

function parseExampleChoice(value: unknown): ExampleChoice | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const uri = text(value['uri']);
  const file = text(value['file']);
  const line = value['line'];
  const apiVersion = text(value['apiVersion']);
  const kind = text(value['kind']);
  return uri === undefined ||
    file === undefined ||
    typeof line !== 'number' ||
    !Number.isInteger(line) ||
    line < 1 ||
    apiVersion === undefined ||
    kind === undefined
    ? undefined
    : { uri, file, line, apiVersion, kind };
}

/** Reads the answer of `cel-lens.typeChoices`; one without a `builtIn` list gives `undefined`. */
export function parseTypeChoices(value: unknown): TypeChoices | undefined {
  if (!isRecord(value) || !Array.isArray(value['builtIn'])) {
    return undefined;
  }
  const crdsNote = text(value['crdsNote']);
  const goTypes = value['goTypes'];
  const goTypesNote = text(value['goTypesNote']);
  const examples = value['examples'];
  return {
    kubernetesVersion: text(value['kubernetesVersion']) ?? '',
    builtIn: parseKindChoices(value['builtIn']),
    crds: parseKindChoices(value['crds']),
    ...(crdsNote !== undefined && { crdsNote }),
    goTypes: Array.isArray(goTypes)
      ? goTypes.map(parseGoTypeChoice).filter((choice) => choice !== undefined)
      : [],
    ...(goTypesNote !== undefined && { goTypesNote }),
    ...(goTypes === undefined && { outdated: true }),
    examples: Array.isArray(examples)
      ? examples.map(parseExampleChoice).filter((choice) => choice !== undefined)
      : [],
    ...(examples === undefined && { cannotInfer: true }),
  };
}

/** A kind as a `kind:` entry names it, such as `apps/v1 Deployment`. */
export function kindName(choice: KindChoice): string {
  return `${choice.apiVersion} ${choice.kind}`;
}

/** What picking a line of the type picker does. */
export type TypeChoice =
  | { readonly action: 'write'; readonly form: SnippetForm }
  | { readonly action: 'pickGo' }
  | { readonly action: 'go'; readonly go: string }
  | { readonly action: 'example'; readonly uri: string; readonly line: number }
  | { readonly action: 'paste' }
  | { readonly action: 'type'; readonly type: string }
  | { readonly action: 'kind'; readonly kind: string }
  | { readonly action: 'openCrdSetting' }
  | { readonly action: 'openServerPathSetting' }
  | { readonly action: 'showLogs' }
  | { readonly action: 'none' };

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
  /** Shown whatever is typed in the filter. */
  readonly alwaysShow?: true;
  readonly value: T;
}

/** One line of a quick pick, without VS Code's types. */
export type PickLine<T> = PickSeparator | PickEntry<T>;

/** Whether an entry gives the value a type now, which `Edit` changes. */
export function hasEntry(target: TypeTarget): boolean {
  return (
    target.type !== undefined ||
    target.kind !== undefined ||
    target.go !== undefined ||
    target.schema === true
  );
}

/**
 * The line of the type picker that writes a schema or a short type in `.cellens` beside the rule:
 * at a new entry, or, for a value with an entry, at the one it has.
 */
export function writeLine(form: SnippetForm, entry = false): PickEntry<TypeChoice> {
  const what = form === 'schema' ? 'a schema' : 'a short type';
  const opens = entry
    ? `opens .cellens beside the rule at the entry it has, to write ${what} there`
    : 'opens .cellens beside the rule';
  return form === 'schema'
    ? {
        label: '$(edit) Write a schema in .cellens…',
        description: 'OpenAPI',
        detail: `Properties, descriptions and formats, as in a CRD; ${opens}`,
        value: { action: 'write', form },
      }
    : {
        label: '$(edit) Write a short type in .cellens…',
        description: 'CEL',
        detail: `Such as {name: string, replicas: int}; ${opens}`,
        value: { action: 'write', form },
      };
}

/** The first line of the type picker: an OpenAPI schema, written in `.cellens` beside the rule. */
export const WRITE_SCHEMA_LINE = writeLine('schema');

/** The line of the type picker that writes a short type in `.cellens` beside the rule. */
export const WRITE_SHORT_TYPE_LINE = writeLine('type');

const GO_TYPE_LABEL = '$(symbol-struct) Use a Go type…';
const GO_TYPE_DETAIL = "A struct of the workspace's Go code, such as a kubebuilder API type";

/**
 * The line of the type picker that turns to the picker of the workspace's Go types: with how
 * many there are once the engine has listed them. It stays in the list whatever is typed, and
 * the Go types picker starts with what was typed.
 */
export function goTypeLine(count?: number, outdated = false): PickEntry<TypeChoice> {
  const description = outdated
    ? 'needs a newer engine'
    : count === undefined
      ? undefined
      : count === 0
        ? 'none in the workspace'
        : `${String(count)} ${count === 1 ? 'type' : 'types'}`;
  return {
    label: GO_TYPE_LABEL,
    ...(description !== undefined && { description }),
    detail: GO_TYPE_DETAIL,
    alwaysShow: true,
    value: { action: 'pickGo' },
  };
}

/** The line of the type picker that opens an editor beside the rule to paste an example into. */
export const PASTE_EXAMPLE_LINE: PickEntry<TypeChoice> = {
  label: '$(clippy) Paste an example…',
  detail: 'YAML or JSON, in an editor beside the rule; a schema is worked out from it',
  value: { action: 'paste' },
};

/** `Paste an example…` once the engine said it cannot work out a schema from one. */
const PASTE_NEEDS_ENGINE_LINE: PickEntry<TypeChoice> = {
  ...PASTE_EXAMPLE_LINE,
  description: 'needs a newer engine',
};

/**
 * The lines the type picker of a policy's value starts with, before the engine lists the kinds;
 * `entry` for a value with an entry, whose write lines open it.
 */
export function firstLines(entry = false): PickLine<TypeChoice>[] {
  return [writeLine('schema', entry), goTypeLine(), writeLine('type', entry), PASTE_EXAMPLE_LINE];
}

/** The placeholder of the type picker of a policy's value. */
export const KIND_PLACEHOLDER =
  'Pick how to write the type, or type to find a kind such as Deployment';

/** The placeholder of the type picker of a CRD's value. */
export const INT_OR_STRING_PLACEHOLDER =
  'An x-kubernetes-int-or-string value is an int or a string to a rule';

/** The placeholder of the picker of the workspace's Go types. */
export const GO_TYPE_PLACEHOLDER = 'Type to find a Go type by its name, folder or doc comment';

/** The icons that tell a built-in kind from a kind a CRD of the workspace defines. */
const BUILT_IN_ICON = '$(symbol-class)';
const CRD_ICON = '$(file-code)';

/**
 * The lines of the type picker of a CRD's value: `int` and `string`, the two types an
 * `x-kubernetes-int-or-string` value takes. A schema or a short type written in `.cellens` would
 * only come back to one of them, and a Go type or a kind does not fit it.
 */
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
 * The documents of the workspace of the value's kind, under their separator: each by its file,
 * with its kind as the description and the line it starts at as the detail.
 */
function exampleLines(choices: TypeChoices): PickLine<TypeChoice>[] {
  if (choices.examples.length === 0) {
    return [];
  }
  return [
    { separator: true, label: 'Examples in the workspace' },
    ...choices.examples.map((example): PickEntry<TypeChoice> => ({
      label: `$(file) ${example.file}`,
      description: `${example.apiVersion} ${example.kind}`,
      detail: `Document at line ${String(example.line)}`,
      value: { action: 'example', uri: example.uri, line: example.line },
    })),
  ];
}

/**
 * The lines of the type picker of a policy's value: `Write a schema in .cellens…`,
 * `Use a Go type…` and `Write a short type in .cellens…`, then the examples of the workspace and
 * `Paste an example…`, then the built-in kinds and the kinds of the workspace's CRDs, each group
 * under its separator. A kind's description is its group and version, marked `beta` or `alpha`
 * when it is not GA. `entry` for a value with an entry, whose write lines open it.
 */
export function typeLines(choices: TypeChoices, entry = false): PickLine<TypeChoice>[] {
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
  return [
    writeLine('schema', entry),
    goTypeLine(choices.goTypes.length, choices.outdated === true),
    writeLine('type', entry),
    ...exampleLines(choices),
    choices.cannotInfer === true ? PASTE_NEEDS_ENGINE_LINE : PASTE_EXAMPLE_LINE,
    ...builtIn,
    ...crdLines(choices),
  ];
}

/**
 * A URI's scheme and authority, such as `file://`, and its path's segments, decoded; `fold` for a
 * Windows path (a drive letter first), whose segments compare whatever their case.
 */
interface UriParts {
  readonly origin: string;
  readonly segments: readonly string[];
  readonly fold: boolean;
}

function uriParts(uri: string): UriParts | undefined {
  const found = /^([a-z][a-z0-9+.-]*:\/\/[^/?#]*)([^?#]*)$/i.exec(uri);
  if (found === null) {
    return undefined;
  }
  const decode = (segment: string): string => {
    try {
      return decodeURIComponent(segment);
    } catch {
      return segment;
    }
  };
  const segments = (found[2] ?? '')
    .split('/')
    .filter((segment) => segment !== '')
    .map(decode);
  return {
    origin: (found[1] ?? '').toLowerCase(),
    segments,
    fold: /^[a-z]:$/i.test(segments[0] ?? ''),
  };
}

/** How many segments `a` and `b` start with in common. */
function commonLength(a: UriParts, b: UriParts): number {
  const fold = a.fold || b.fold;
  const key = (segment: string | undefined): string | undefined =>
    fold ? segment?.toLowerCase() : segment;
  let common = 0;
  while (
    common < a.segments.length &&
    common < b.segments.length &&
    key(a.segments[common]) === key(b.segments[common])
  ) {
    common += 1;
  }
  return common;
}

/** The folder of a file's URI, such as `file:///repo` for `file:///repo/.cellens`. */
export function parentFolder(uri: string): string {
  return uri.replace(/\/[^/]*$/, '');
}

/** Whether the folder `folder` is `root` or below it, on the same host. */
export function isAtOrBelow(folder: string, root: string): boolean {
  const inner = uriParts(folder);
  const outer = uriParts(root);
  return (
    inner !== undefined &&
    outer?.origin === inner.origin &&
    commonLength(inner, outer) === outer.segments.length
  );
}

/**
 * The choices with only the Go types a `go:` entry of a `.cellens` in `root` can name: those of
 * `root`, its workspace folder, or for a `.cellens` outside every workspace folder its own folder.
 */
export function goTypesIn(choices: TypeChoices, root: string): TypeChoices {
  return { ...choices, goTypes: choices.goTypes.filter(({ folder }) => isAtOrBelow(folder, root)) };
}

/**
 * A Go type as a `go:` entry of `cellens` names it: its package's folder relative to the folder of
 * `cellens`, then `#` and its name, such as `./api/v1#QuotaSpec`, `../api/v1#QuotaSpec` or
 * `.#QuotaSpec`. `undefined` when the folder is not on the same host as `cellens`.
 */
export function goReference(cellens: string, folder: string, name: string): string | undefined {
  const from = uriParts(cellens);
  const to = uriParts(folder);
  if (from === undefined || to?.origin !== from.origin) {
    return undefined;
  }
  const base = { ...from, segments: from.segments.slice(0, -1) };
  const common = commonLength(base, to);
  const down = to.segments.slice(common);
  const up = base.segments.slice(common).map(() => '..');
  return `${(up.length === 0 ? ['.', ...down] : [...up, ...down]).join('/')}#${name}`;
}

/** A `go:` reference with its folder written one way: `api/v1#X` and `./api/v1/#X` are `api/v1#X`. */
function plainGo(go: string): string {
  const hash = go.lastIndexOf('#');
  const folder = (hash === -1 ? go : go.slice(0, hash))
    .split('/')
    .filter((segment) => segment !== '' && segment !== '.')
    .join('/');
  return `${folder}#${hash === -1 ? '' : go.slice(hash + 1)}`;
}

/** Whether two `go:` references name the same Go type from the same `.cellens`. */
export function sameGo(a: string, b: string): boolean {
  return plainGo(a) === plainGo(b);
}

/** The line of the Go types picker when the engine could not list them; picking it shows the logs. */
export const GO_TYPES_FAILED_LINE: PickEntry<TypeChoice> = {
  label: '$(warning) The Go types could not be listed',
  description: 'pick to show the logs',
  value: { action: 'showLogs' },
};

/** What to do about an engine too old for what the Go types and `.cellens` lines need. */
const OUTDATED_FIX = `set cel-lens.serverPath to a newer cel-lens-core, then run cel-lens: ${RESTART_ENGINE_LABEL}`;

/** What the flow says when the engine is too old to write a type in `.cellens` or list Go types. */
export const OUTDATED_ENGINE_MESSAGE = `This cel-lens engine is too old to write a type in .cellens or list Go types: ${OUTDATED_FIX}.`;

/** What the flow says when the engine is too old to work out a schema from an example. */
export const OUTDATED_EXAMPLE_MESSAGE = `This cel-lens engine is too old to work out a type from an example: ${OUTDATED_FIX}.`;

/** The line of the Go types picker when the engine is too old to list them; picking it opens the setting. */
export const OUTDATED_ENGINE_LINE: PickEntry<TypeChoice> = {
  label: '$(warning) This cel-lens engine cannot list Go types',
  description: 'pick to open the setting',
  detail: `It is older than this extension: ${OUTDATED_FIX}`,
  value: { action: 'openServerPathSetting' },
};

/**
 * The start of what an engine with `cel-lens.setType` but no `snippet` refuses a request without a
 * type with, as `InvalidParams`: `cel-lens.setType: give the type, such as type: "{name: string}",
 * the kind, …`.
 */
const NO_TYPE_REFUSAL = `${ENGINE_SET_TYPE}: give the type, such as`;

/**
 * Why the engine refused a `snippet` it does not know, as an engine older than this extension's
 * `Write … in .cellens…` lines does: it reads the request as one without a type. `undefined` for
 * another failure, which shows as it is.
 */
export function snippetRefusal(error: unknown): string | undefined {
  const failure = engineFailure(error);
  return isRecord(error) &&
    error['code'] === INVALID_PARAMS &&
    typeof error['message'] === 'string' &&
    error['message'].startsWith(NO_TYPE_REFUSAL) &&
    failure.kind === 'refused'
    ? failure.reason
    : undefined;
}

/** Whether an engine command was refused with `InvalidParams` and a message that starts with `start`. */
function refusedWith(error: unknown, start: string): boolean {
  return (
    isRecord(error) &&
    error['code'] === INVALID_PARAMS &&
    typeof error['message'] === 'string' &&
    error['message'].startsWith(start)
  );
}

/**
 * Whether the engine does not know `cel-lens.inferType`, as one older than this extension's
 * examples answers: `unknown command "cel-lens.inferType": cel-lens runs …`.
 */
export function unknownInferType(error: unknown): boolean {
  return refusedWith(error, `unknown command "${ENGINE_INFER_TYPE}"`);
}

/** The line of the Go types picker when the engine lists none. */
export const NO_GO_TYPES_LINE: PickEntry<TypeChoice> = {
  label: '$(info) No Go types in the workspace',
  detail:
    'cel-lens lists the exported struct types with a json tag that the .go files of the workspace declare, but _test.go files',
  value: { action: 'none' },
};

/**
 * The lines of the picker of the workspace's Go types: each type's name, its folder as
 * `folderName` writes it, and the first sentence of its doc comment; picking one gives it as
 * `go:` names it from `cellens`. With none, a line says so; when the engine lists only some, a
 * last line says why.
 */
export function goTypeLines(
  choices: TypeChoices,
  cellens: string,
  folderName: (folder: string) => string,
): PickLine<TypeChoice>[] {
  const lines = choices.goTypes.flatMap((choice): PickLine<TypeChoice>[] => {
    const go = goReference(cellens, choice.folder, choice.name);
    return go === undefined
      ? []
      : [
          {
            label: `$(symbol-struct) ${choice.name}`,
            description: folderName(choice.folder),
            ...(choice.doc !== '' && { detail: choice.doc }),
            value: { action: 'go', go },
          },
        ];
  });
  if (lines.length === 0) {
    lines.push(NO_GO_TYPES_LINE);
  }
  if (choices.goTypesNote !== undefined) {
    lines.push({
      label: '$(info) Not every Go type is listed',
      detail: choices.goTypesNote,
      value: { action: 'none' },
    });
  }
  return lines;
}

/**
 * Whether a choice is what the value has now, for an edit to start on; none is for `dyn`: its
 * kind, a CRD value's `int` or `string`, `Use a Go type…` and then the Go type for a Go type, and
 * the line that writes a schema or a short type for one the person wrote.
 */
export function isCurrentChoice(target: TypeTarget): (choice: TypeChoice) => boolean {
  const pickedType = inCrd(target) && (target.type === 'int' || target.type === 'string');
  return (choice) => {
    switch (choice.action) {
      case 'kind':
        return choice.kind === target.kind;
      case 'type':
        return choice.type === target.type;
      case 'pickGo':
        return target.go !== undefined;
      case 'go':
        return target.go !== undefined && sameGo(choice.go, target.go);
      case 'write':
        return choice.form === 'schema'
          ? target.schema === true
          : target.type !== undefined && !pickedType;
      case 'example':
      case 'paste':
      case 'openCrdSetting':
      case 'openServerPathSetting':
      case 'showLogs':
      case 'none':
        return false;
    }
  };
}

/** A range of `.cellens` as the engine gives it: zero-based lines and UTF-16 characters. */
export interface TextRange {
  readonly start: { readonly line: number; readonly character: number };
  readonly end: { readonly line: number; readonly character: number };
}

function position(value: unknown): { line: number; character: number } | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const line = value['line'];
  const character = value['character'];
  return typeof line === 'number' &&
    Number.isInteger(line) &&
    line >= 0 &&
    typeof character === 'number' &&
    Number.isInteger(character) &&
    character >= 0
    ? { line, character }
    : undefined;
}

/** Where the person writes the type: the `.cellens` and the range to select in it. */
export interface EntryPlace {
  readonly cellens: string;
  readonly selection: TextRange;
}

/**
 * Reads the answer of `cel-lens.setType` with a `snippet`:
 * `{cellens, change, selection: {start, end}}`. Anything else gives `undefined`.
 */
export function parseEntryPlace(value: unknown): EntryPlace | undefined {
  if (!isRecord(value) || !isChangeAnswer(value)) {
    return undefined;
  }
  const cellens = text(value['cellens']);
  const selection = value['selection'];
  const start = isRecord(selection) ? position(selection['start']) : undefined;
  const end = isRecord(selection) ? position(selection['end']) : undefined;
  return cellens === undefined || start === undefined || end === undefined
    ? undefined
    : { cellens, selection: { start, end } };
}

/** The answer of `cel-lens.setType` or `cel-lens.removeType`: the `.cellens` it changed, and how. */
export interface ChangeAnswer {
  readonly cellens: string;
  /** `added`, `replaced`, `created`, `removed`, `deleted`, or `unchanged` for a snippet. */
  readonly change: string;
}

/** Reads the answer of `cel-lens.setType` or `cel-lens.removeType`; anything else gives `undefined`. */
export function parseChangeAnswer(value: unknown): ChangeAnswer | undefined {
  const cellens = isRecord(value) ? text(value['cellens']) : undefined;
  const change = isRecord(value) ? text(value['change']) : undefined;
  return cellens === undefined || change === undefined ? undefined : { cellens, change };
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
    description:
      value.target.type ??
      value.target.kind ??
      value.target.go ??
      (value.target.schema === true ? 'schema' : 'dyn'),
    detail: `${[value.target.document, value.target.version].filter((part) => part !== undefined).join(' ')} · line ${String(value.line)}`,
    value,
  }));
}
