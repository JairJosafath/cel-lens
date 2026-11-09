import { isRecord } from './is_record';

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

/** The line of the editor of a pasted example the cursor starts on: the one below the comment. */
export const EXAMPLE_CURSOR_LINE = 3;

/** Whether a parsed JSON value is an object with an `apiVersion` and a `kind`. */
function isResource(value: unknown): boolean {
  return isRecord(value) && 'apiVersion' in value && 'kind' in value;
}

/**
 * Whether a pasted example is a whole resource, with `apiVersion` and `kind` at its root, rather
 * than the value itself: JSON, an object or a list of them, or YAML whose documents have both
 * keys at their root, at the start of a line. Comment lines are left out.
 */
export function isWholeResource(text: string): boolean {
  const lines = text.split(/\r?\n/).filter((line) => !line.trimStart().startsWith('#'));
  try {
    const parsed: unknown = JSON.parse(lines.join('\n'));
    return Array.isArray(parsed) ? parsed.some(isResource) : isResource(parsed);
  } catch {
    return (
      lines.some((line) => /^apiVersion\s*:/.test(line)) &&
      lines.some((line) => /^kind\s*:/.test(line))
    );
  }
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
