import { parseAllDocuments } from 'yaml';

import { isRecord } from './is_record';
import type { PickEntry } from './value_types';

/** The answer of `cel-lens.inferType`: the schema worked out from examples, and what they leave open. */
export interface InferredSchema {
  readonly schema: Readonly<Record<string, unknown>>;
  /** Each as the engine words it, the value first, such as `spec.teams: an empty list says nothing about its items`. */
  readonly notes: readonly string[];
}

/** Reads the answer of `cel-lens.inferType`: `{schema, notes}`. Anything else gives `undefined`. */
export function parseInferredSchema(value: unknown): InferredSchema | undefined {
  if (!isRecord(value) || !isRecord(value['schema'])) {
    return undefined;
  }
  const notes = value['notes'];
  return {
    schema: value['schema'],
    notes: Array.isArray(notes)
      ? notes.filter((note): note is string => typeof note === 'string' && note !== '')
      : [],
  };
}

/** The example `cel-lens.inferType` works out a schema from: a text, its document at `line`, the value at `path`. */
export interface ExampleArgument {
  readonly text: string;
  readonly line?: number;
  readonly path?: string;
}

/**
 * The comment the editor of a pasted example starts with: a short first line, which names the
 * editor's tab, then what to paste and how to use it. `inside` for a value below the root of its
 * object, which a whole resource holds.
 */
export function exampleComment(value: string, inside: boolean): string {
  return [
    `# An example of ${value}`,
    inside
      ? '# Paste it below, or a whole resource that holds it, as YAML or JSON: one document or several.'
      : '# Paste it below, as YAML or JSON: one document or several.',
    '# Then click "Use this example as the type" above. Closing this editor writes nothing.',
    '',
  ].join('\n');
}

/** What the code lens above a pasted example says, and the title of the question about it. */
export const USE_EXAMPLE_TITLE = 'Use this example as the type';

/** The line of the editor of a pasted example the cursor starts on: the one below the comment. */
export const EXAMPLE_CURSOR_LINE = 3;

/**
 * The kind of a parsed value that is a resource, with an `apiVersion` and a `kind`; the kinds of
 * its items for a `List`, whose items the engine takes as the examples.
 */
function resourceKind(value: unknown): string[] {
  if (
    !isRecord(value) ||
    typeof value['apiVersion'] !== 'string' ||
    typeof value['kind'] !== 'string'
  ) {
    return [];
  }
  const items = value['items'];
  return value['kind'] === 'List' && Array.isArray(items)
    ? items.flatMap(resourceKind)
    : [value['kind']];
}

/**
 * The kinds of the resources a pasted example is, at its root: each YAML or JSON document a
 * resource, or a list of them, in any style YAML writes them (block or flow, any indentation);
 * `[]` for an example of a value alone, whose root has no `apiVersion` and `kind`, and for a
 * document YAML cannot read, which the engine then says what is wrong with.
 */
export function pastedKinds(text: string): string[] {
  return parseAllDocuments(text).flatMap((document) => {
    if (document.errors.length > 0) {
      return [];
    }
    let root: unknown;
    try {
      root = document.toJS();
    } catch {
      return []; // Such as too many aliases: the engine says what is wrong with it.
    }
    return Array.isArray(root) ? root.flatMap(resourceKind) : resourceKind(root);
  });
}

/**
 * How `cel-lens.inferType` takes a pasted example for a value: `none`, as the value itself, for a
 * value at the root of its object or an example with no resource at its root; `send`, with the
 * value's path, for resources of a kind the value is in (`kinds`, from the examples of the
 * workspace); `none` for resources of another kind, such as an object reference the value is;
 * and `ask` when the value's kinds are not known, as the resource may be either.
 */
export function pastedPath(
  text: string,
  path: string | undefined,
  kinds: readonly string[],
): 'none' | 'send' | 'ask' {
  const pasted = pastedKinds(text);
  if (path === undefined || pasted.length === 0) {
    return 'none';
  }
  if (kinds.length === 0) {
    return 'ask';
  }
  return pasted.some((kind) => kinds.includes(kind)) ? 'send' : 'none';
}

/** The answer of the question `pastedPath` asks: the value at its path in the resource, or the resource itself. */
export type PastedAs = 'whole' | 'value';

/** The lines of the question whether a pasted resource holds the value or is it, the first likelier. */
export function pastedAsLines(kind: string, path: string): PickEntry<PastedAs>[] {
  return [
    {
      label: `$(symbol-class) Use the whole ${kind}'s ${path}`,
      detail: `The value is the ${path} of each pasted ${kind}`,
      value: 'whole',
    },
    {
      label: '$(symbol-field) Use it as the value itself',
      detail: `The pasted ${kind} is an example of the value, such as an object reference`,
      value: 'value',
    },
  ];
}

/** A note of `cel-lens.inferType` at the line of `.cellens` it is about. */
export interface NotePlace {
  /** The line of the key the note is about, from 0, and the key's characters on it. */
  readonly line: number;
  readonly start: number;
  readonly end: number;
  /** The last line of the key's block: an edit from `line` to here addresses the note. */
  readonly blockEnd: number;
  readonly message: string;
}

/** A key of a block mapping on a line: where it starts and ends, its indentation and its name. */
interface YamlKey {
  readonly indent: number;
  readonly end: number;
  readonly name: string;
}

/** A plain, double-quoted or single-quoted key, then `:` and a space or the end of the line. */
const KEY = /^( *)(?!- )("(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|[^\s"'#][^#]*?)\s*:(?:\s|$)/;

/** A key as YAML reads it: a quoted one without its quotes. */
function unquote(raw: string): string {
  if (raw.startsWith("'")) {
    return raw.slice(1, -1).replaceAll("''", "'");
  }
  if (raw.startsWith('"')) {
    try {
      const parsed: unknown = JSON.parse(raw);
      return typeof parsed === 'string' ? parsed : raw;
    } catch {
      return raw.slice(1, -1);
    }
  }
  return raw;
}

function keyOf(line: string): YamlKey | undefined {
  const found = KEY.exec(line);
  const raw = found?.[2];
  if (found === null || raw === undefined) {
    return undefined;
  }
  const indent = found[1]?.length ?? 0;
  return { indent, end: indent + raw.length, name: unquote(raw) };
}

function indentOf(line: string): number {
  return line.length - line.trimStart().length;
}

function isBlank(line: string): boolean {
  const trimmed = line.trim();
  return trimmed === '' || trimmed.startsWith('#');
}

/** The last line of the block below the line `at`, whose indentation is `indent`. */
function blockEnd(lines: readonly string[], at: number, indent: number): number {
  let end = at;
  for (let line = at + 1; line < lines.length; line += 1) {
    const text = lines[line] ?? '';
    if (isBlank(text)) {
      continue;
    }
    if (indentOf(text) <= indent) {
      break;
    }
    end = line;
  }
  return end;
}

/** The key `name` of the mapping below the key at `parent`, on the first indentation below it. */
function childKey(lines: readonly string[], parent: number, name: string): number | undefined {
  const parentIndent = indentOf(lines[parent] ?? '');
  let childIndent: number | undefined;
  for (let line = parent + 1; line < lines.length; line += 1) {
    const text = lines[line] ?? '';
    if (isBlank(text)) {
      continue;
    }
    const indent = indentOf(text);
    if (indent <= parentIndent) {
      return undefined;
    }
    childIndent ??= indent;
    if (indent === childIndent && keyOf(text)?.name === name) {
      return line;
    }
  }
  return undefined;
}

/**
 * The keys of a schema a `.cellens` path goes through: `properties` and the field for a name,
 * `items` for `[]` and `additionalProperties` for `[*]`, as in `spec.ports[].name`.
 */
function schemaKeys(path: string): string[] {
  return path.split('.').flatMap((segment) => {
    const found = /^(.*?)((?:\[\]|\[\*\])*)$/.exec(segment);
    const name = found?.[1] ?? segment;
    const brackets = found?.[2]?.match(/\[\*?\]/g) ?? [];
    return [
      ...(name === '' ? [] : ['properties', name]),
      ...brackets.map((bracket) => (bracket === '[]' ? 'items' : 'additionalProperties')),
    ];
  });
}

/**
 * The line of the deepest key of `keys` found below `from`, and whether every one was; a
 * `properties` key counts as its field's, so a missing field leaves the note at its parent.
 */
function follow(
  lines: readonly string[],
  from: number,
  keys: readonly string[],
): { line: number; complete: boolean } {
  let line = from;
  let deepest = from;
  for (const key of keys) {
    const next = childKey(lines, line, key);
    if (next === undefined) {
      return { line: deepest, complete: false };
    }
    line = next;
    if (key !== 'properties') {
      deepest = next;
    }
  }
  return { line, complete: true };
}

/** Whether a line starts an entry of `types:`, which no search for a key of another may cross. */
function startsEntry(line: string): boolean {
  return /^\s*- /.test(line);
}

/** The line of the `schema:` key of the entry the line `at` is in, else `at`. */
function schemaLine(lines: readonly string[], at: number): number {
  for (let line = at; line >= 0; line -= 1) {
    const text = lines[line] ?? '';
    if (keyOf(text)?.name === 'schema') {
      return line;
    }
    if (startsEntry(text)) {
      break;
    }
  }
  for (let line = at + 1; line < lines.length && !startsEntry(lines[line] ?? ''); line += 1) {
    if (keyOf(lines[line] ?? '')?.name === 'schema') {
      return line;
    }
  }
  return at;
}

/** The note at the key on `line`, or at the text of the line when it has no key. */
function placeAt(lines: readonly string[], line: number, message: string): NotePlace {
  const text = lines[line] ?? '';
  const key = keyOf(text);
  const indent = key?.indent ?? indentOf(text);
  return {
    line,
    start: indent,
    end: key?.end ?? text.trimEnd().length,
    blockEnd: blockEnd(lines, line, indent),
    message,
  };
}

/**
 * Where each note of `cel-lens.inferType` goes in the text of `.cellens`, once its schema is an
 * entry there and `at` is a line of that entry, such as where the engine puts the cursor on its
 * type: at the key of the value the note names (`spec.teams` is `properties: spec: properties:
 * teams:` below the entry's `schema:`), else at the deepest key of it there is, else at `schema:`.
 * `path`, the value's own path, is left out of a note that starts with it.
 */
export function notePlaces(
  text: string,
  at: number,
  notes: readonly string[],
  path?: string,
): NotePlace[] {
  const lines = text.split(/\r?\n/);
  const schema = schemaLine(lines, at);
  return notes.map((note) => {
    const named = /^([^\s:]+): /.exec(note)?.[1];
    if (named === undefined) {
      return placeAt(lines, schema, note);
    }
    const whole = follow(lines, schema, schemaKeys(named));
    const inner =
      path !== undefined && named.startsWith(`${path}.`)
        ? follow(lines, schema, schemaKeys(named.slice(path.length + 1)))
        : undefined;
    const found = whole.complete || inner?.complete !== true ? whole : inner;
    return placeAt(lines, found.line, note);
  });
}

/** A change of a document: the lines it replaced, from 0, and the text it put there. */
export interface LineChange {
  readonly startLine: number;
  readonly endLine: number;
  readonly text: string;
}

/**
 * The notes once the document changed, the changes taken in order: a change that touches a
 * note's key or block addresses it, and the note goes; the notes below a change move with it.
 */
export function moveNotes(
  notes: readonly NotePlace[],
  changes: readonly LineChange[],
): NotePlace[] {
  let moved = [...notes];
  for (const change of changes) {
    const shift = (change.text.match(/\n/g)?.length ?? 0) - (change.endLine - change.startLine);
    moved = moved
      .filter((note) => note.blockEnd < change.startLine || note.line > change.endLine)
      .map((note) =>
        note.line > change.endLine
          ? { ...note, line: note.line + shift, blockEnd: note.blockEnd + shift }
          : note,
      );
  }
  return moved;
}
