/** A position in a document: zero-based line and UTF-16 column, as in LSP. */
export interface DocumentPosition {
  line: number;
  character: number;
}

/**
 * A stretch of `source` read from one place in the document. A `copied` stretch
 * sits in the document as it is, character by character, from offset `start`;
 * any other stretch (an escape, a folded line break) was read from `start` to
 * `end` as a whole.
 */
interface Segment {
  from: number;
  length: number;
  start: number;
  end: number;
  copied: boolean;
}

/**
 * A run of CEL inside a YAML document. `source` is the CEL after YAML decoding
 * (indentation removed, escapes resolved); `segments` tell where in the
 * document, in UTF-16 units, each part of `source` was read from.
 */
export interface CelRange {
  source: string;
  segments: Segment[];
}

/** Where in the document each character of a range's `source` was read from. */
export interface OffsetMap {
  /** The offset that `source[index]` was read from. */
  start(index: number): number | undefined;
  /** The offset just after what `source[index]` was read from. */
  end(index: number): number | undefined;
}

/**
 * Maps `source` indexes of `range` to document offsets. Lookups mostly move
 * forward, so it keeps the segment it found last and walks from there.
 */
export function offsetMap(range: CelRange): OffsetMap {
  const { segments } = range;
  let at = 0;
  const find = (index: number): Segment | undefined => {
    while (at > 0 && index < (segments[at]?.from ?? 0)) {
      at--;
    }
    for (;;) {
      const segment = segments[at];
      if (segment === undefined || index < segment.from) {
        return undefined;
      }
      if (index < segment.from + segment.length) {
        return segment;
      }
      at++;
    }
  };
  return {
    start(index) {
      const segment = find(index);
      return segment === undefined
        ? undefined
        : segment.start + (segment.copied ? index - segment.from : 0);
    },
    end(index) {
      const segment = find(index);
      return segment === undefined
        ? undefined
        : segment.copied
          ? segment.start + index - segment.from + 1
          : segment.end;
    },
  };
}

/**
 * Where CEL lives in YAML: the keys whose values are CEL, under the key that
 * holds the list they sit in. Admission policies and kubebuilder markers are
 * added here.
 */
const CEL_PLACES = [
  { listKey: 'x-kubernetes-validations', celKeys: ['rule', 'messageExpression'] },
] as const;

const LIST_KEY_PATTERN = new RegExp(
  `^(${CEL_PLACES.map((place) => place.listKey).join('|')})[ \\t]*:(?=[ \\t]|$)`,
);
const CEL_KEY_PATTERN = new RegExp(
  `^(${CEL_PLACES.flatMap((place) => place.celKeys).join('|')})[ \\t]*:(?=[ \\t]|$)`,
);
const BLOCK_HEADER = /^([|>])(?:[+-]?([1-9])?|([1-9])[+-])[ \t]*(?:#.*)?$/;
const ESCAPES: Record<string, string> = {
  '0': '\0',
  a: '\x07',
  b: '\b',
  t: '\t',
  '\t': '\t',
  n: '\n',
  v: '\v',
  f: '\f',
  r: '\r',
  e: '\x1B',
  ' ': ' ',
  '"': '"',
  '/': '/',
  '\\': '\\',
  N: '\u0085',
  _: ' ',
  L: ' ',
  P: ' ',
};
const HEX_ESCAPE_LENGTHS: Record<string, number> = { x: 2, u: 4, U: 8 };

interface Line {
  /** Offset of the line's first character in the document. */
  offset: number;
  text: string;
}

interface Builder {
  source: string;
  segments: Segment[];
}

function splitLines(text: string): Line[] {
  const lines: Line[] = [];
  let offset = 0;
  for (const raw of text.split('\n')) {
    lines.push({ offset, text: raw.endsWith('\r') ? raw.slice(0, -1) : raw });
    offset += raw.length + 1;
  }
  return lines;
}

function indentOf(text: string): number {
  return text.length - text.trimStart().length;
}

function isBlank(text: string): boolean {
  return text.trim() === '';
}

function isComment(text: string): boolean {
  return text.trimStart().startsWith('#');
}

function push(builder: Builder, text: string, start: number, end: number): void {
  if (text.length > 0) {
    builder.segments.push({
      from: builder.source.length,
      length: text.length,
      start,
      end,
      copied: false,
    });
    builder.source += text;
  }
}

/** Adds text that sits in the document as it is, from offset `start`. */
function pushRun(builder: Builder, text: string, start: number): void {
  if (text.length > 0) {
    builder.segments.push({
      from: builder.source.length,
      length: text.length,
      start,
      end: start + text.length,
      copied: true,
    });
    builder.source += text;
  }
}

/** Adds the characters of `line` from `at` up to the next match of `stop`; returns where it stopped. */
function pushPlainRun(builder: Builder, line: Line, at: number, stop: RegExp): number {
  stop.lastIndex = at;
  const found = stop.exec(line.text);
  const end = found === null ? line.text.length : found.index;
  const length = Math.max(end - at, 1);
  pushRun(builder, line.text.slice(at, at + length), line.offset + at);
  return at + length;
}

/** The text of a plain scalar line from `from`, without its trailing comment. */
function plainText(line: Line, from: number): string {
  const comment = /[ \t]#/.exec(line.text.slice(from));
  const end = comment === null ? line.text.length : from + comment.index;
  return line.text.slice(from, end).trimEnd();
}

/**
 * Reads a plain scalar that starts at `column` of `lines[index]`. Lines below it
 * that sit deeper than its key (`keyColumn`) continue it, folded into one space;
 * a comment ends it.
 */
function plainScalar(lines: Line[], index: number, column: number, keyColumn: number): CelRange {
  const builder: Builder = { source: '', segments: [] };
  const first = lines[index];
  if (first === undefined) {
    return builder;
  }
  pushRun(builder, plainText(first, column), first.offset + column);
  if (/[ \t]#/.test(first.text.slice(column))) {
    return builder;
  }
  let previous = first;
  let breaks = 0;
  for (let i = index + 1; i < lines.length; i++) {
    const line = lines[i];
    if (line === undefined) {
      break;
    }
    if (isBlank(line.text)) {
      breaks++;
      continue;
    }
    const indent = indentOf(line.text);
    if (indent <= keyColumn || isComment(line.text)) {
      break;
    }
    const breakOffset = previous.offset + previous.text.length;
    push(builder, breaks > 0 ? '\n'.repeat(breaks) : ' ', breakOffset, line.offset);
    pushRun(builder, plainText(line, indent), line.offset + indent);
    previous = line;
    breaks = 0;
    if (/[ \t]#/.test(line.text.slice(indent))) {
      break;
    }
  }
  return builder;
}

/** Reads a quoted scalar that opens at `column` of `lines[index]`, over as many lines as it takes. */
function quotedScalar(lines: Line[], index: number, column: number): CelRange {
  const builder: Builder = { source: '', segments: [] };
  const first = lines[index];
  const quote = first?.text.charAt(column);
  let lineIndex = index;
  let at = column + 1;
  while (lineIndex < lines.length) {
    const line = lines[lineIndex];
    if (line === undefined) {
      break;
    }
    if (at >= line.text.length) {
      // A line break inside quotes folds into one space; blank lines keep their break.
      let next = lineIndex + 1;
      let breaks = 0;
      while (next < lines.length && isBlank(lines[next]?.text ?? 'x')) {
        next++;
        breaks++;
      }
      const following = lines[next];
      if (following === undefined) {
        break;
      }
      push(
        builder,
        breaks > 0 ? '\n'.repeat(breaks) : ' ',
        line.offset + line.text.length,
        following.offset,
      );
      lineIndex = next;
      at = indentOf(following.text);
      continue;
    }
    const char = line.text.charAt(at);
    const offset = line.offset + at;
    if (quote === "'") {
      if (char === "'" && line.text.charAt(at + 1) === "'") {
        push(builder, "'", offset, offset + 2);
        at += 2;
      } else if (char === "'") {
        break;
      } else {
        at = pushPlainRun(builder, line, at, /'/g);
      }
    } else if (char === '"') {
      break;
    } else if (char === '\\') {
      const code = line.text.charAt(at + 1);
      const hexLength = HEX_ESCAPE_LENGTHS[code];
      if (hexLength !== undefined) {
        const digits = line.text.slice(at + 2, at + 2 + hexLength);
        const point =
          /^[0-9a-fA-F]+$/.test(digits) && digits.length === hexLength ? parseInt(digits, 16) : NaN;
        const decoded = point <= 0x10ffff ? String.fromCodePoint(point) : '';
        push(builder, decoded, offset, offset + 2 + digits.length);
        at += 2 + digits.length;
      } else if (code === '') {
        break; // an escaped line break joins the lines without a space
      } else {
        push(builder, ESCAPES[code] ?? code, offset, offset + 2);
        at += 2;
      }
    } else {
      at = pushPlainRun(builder, line, at, /["\\]/g);
    }
  }
  return builder;
}

/** Reads a block scalar whose header `|` or `>` sits on `lines[index]`; `parentIndent` is its key's column. */
function blockScalar(
  lines: Line[],
  index: number,
  header: RegExpExecArray,
  parentIndent: number,
): CelRange {
  const builder: Builder = { source: '', segments: [] };
  const folded = header[1] === '>';
  const explicit = header[2] ?? header[3];
  const content: Line[] = [];
  let blockIndent = explicit === undefined ? -1 : parentIndent + Number(explicit);
  for (let i = index + 1; i < lines.length; i++) {
    const line = lines[i];
    if (line === undefined) {
      break;
    }
    if (isBlank(line.text)) {
      content.push(line);
      continue;
    }
    const indent = indentOf(line.text);
    if (blockIndent < 0 && indent > parentIndent) {
      blockIndent = indent;
    }
    if (blockIndent < 0 || indent < blockIndent) {
      break;
    }
    content.push(line);
  }
  while (content.length > 0 && isBlank(content[content.length - 1]?.text ?? 'x')) {
    content.pop();
  }
  let previous: Line | undefined;
  let pendingBreaks = 0;
  for (const line of content) {
    const blank = isBlank(line.text);
    if (previous !== undefined) {
      const breakOffset = previous.offset + previous.text.length;
      if (blank) {
        pendingBreaks++;
        continue;
      }
      const join = folded && pendingBreaks === 0 && !/^[ \t]/.test(line.text.slice(blockIndent));
      push(builder, join ? ' ' : '\n'.repeat(pendingBreaks + 1), breakOffset, line.offset);
      pendingBreaks = 0;
    } else if (blank) {
      continue;
    }
    const text = line.text.slice(blockIndent);
    pushRun(builder, text, line.offset + blockIndent);
    previous = line;
  }
  return builder;
}

/** Whether the block scalar of the key at `column` of `lines[index]` runs on to line `target`. */
function blockReaches(lines: Line[], index: number, column: number, target: number): boolean {
  for (let i = index + 1; i <= target; i++) {
    const text = lines[i]?.text;
    if (text === undefined || (!isBlank(text) && indentOf(text) <= column)) {
      return false;
    }
  }
  return true;
}

/** Zero-based lines, both included: only CEL that overlaps them is read. */
export interface LineWindow {
  fromLine: number;
  toLine: number;
}

/**
 * The CEL fields of admission policies, by kind: the keys from `spec` down to
 * the key that holds the CEL, list levels left out. Only a document whose
 * `apiVersion` is in `admissionregistration.k8s.io` (or is Kyverno's
 * `policies.kyverno.io/v1`) and whose `kind` is here has them.
 */
const VALIDATING_PATHS: readonly string[] = [
  'spec.validations.expression',
  'spec.validations.messageExpression',
  'spec.matchConditions.expression',
  'spec.variables.expression',
  'spec.auditAnnotations.valueExpression',
];

const POLICY_PATHS: Record<string, readonly string[]> = {
  ValidatingAdmissionPolicy: VALIDATING_PATHS,
  MutatingAdmissionPolicy: [
    'spec.matchConditions.expression',
    'spec.variables.expression',
    'spec.mutations.applyConfiguration.expression',
    'spec.mutations.jsonPatch.expression',
  ],
};

const POLICY_GROUP = 'admissionregistration.k8s.io/';
const KYVERNO_API_VERSION = 'policies.kyverno.io/v1';
const DOCUMENT_BOUNDARY = /^(?:---|\.\.\.)(?:[ \t]|$)/;
const MAPPING_KEY = /^([A-Za-z0-9_.-]+)[ \t]*:(?=[ \t]|$)/;

/** A top-level scalar value of a document line, without quotes or a trailing comment. */
function topLevelValue(text: string, key: string): string | undefined {
  const match = new RegExp(
    `^${key}[ \\t]*:[ \\t]*(?:"([^"]*)"|'([^']*)'|([^\\s#]+))[ \\t]*(?:#.*)?$`,
  ).exec(text);
  return match?.[1] ?? match?.[2] ?? match?.[3];
}

/** The experimental features that change what is read as CEL. */
export interface CelFeatures {
  /** Kyverno `ValidatingPolicy` documents count (the `kyverno` experimental feature). */
  kyverno: boolean;
}

/** The CEL paths of the policy in `lines[from..to]`, if the document is one. */
function policyPaths(
  lines: Line[],
  from: number,
  to: number,
  kyverno: boolean,
): readonly string[] | undefined {
  let kind: string | undefined;
  let apiVersion: string | undefined;
  for (let i = from; i <= to; i++) {
    const text = lines[i]?.text ?? '';
    kind ??= topLevelValue(text, 'kind');
    apiVersion ??= topLevelValue(text, 'apiVersion');
  }
  if (kind === 'ValidatingPolicy' && apiVersion === KYVERNO_API_VERSION) {
    return kyverno ? VALIDATING_PATHS : undefined;
  }
  if (kind === undefined || !apiVersion?.startsWith(POLICY_GROUP)) {
    return undefined;
  }
  return Object.hasOwn(POLICY_PATHS, kind) ? POLICY_PATHS[kind] : undefined;
}

/** Reads the value that follows the key ending at `valueFrom` on `lines[index]`; `column` is the key's column. */
function readValue(
  lines: Line[],
  index: number,
  column: number,
  valueFrom: number,
  window: LineWindow | undefined,
): CelRange | undefined {
  const line = lines[index];
  if (line === undefined) {
    return undefined;
  }
  const rest = line.text.slice(valueFrom);
  const valueColumn = valueFrom + indentOf(rest);
  const value = line.text.slice(valueColumn);
  const header = BLOCK_HEADER.exec(value);
  const fromLine = window?.fromLine ?? 0;
  const above = index < fromLine;
  if (above && !blockReaches(lines, index, column, fromLine)) {
    return undefined;
  }
  const first = value.charAt(0);
  if (header !== null) {
    return blockScalar(lines, index, header, column);
  }
  let range: CelRange | undefined;
  if (first === '"' || first === "'") {
    range = quotedScalar(lines, index, valueColumn);
  } else if (value !== '' && first !== '#') {
    range = plainScalar(lines, index, valueColumn, column);
  }
  // A scalar that starts above the window counts only if it runs into it.
  const windowStart = lines[fromLine]?.offset ?? Infinity;
  const lastSegment = range?.segments[range.segments.length - 1];
  return above && (lastSegment === undefined || lastSegment.end <= windowStart) ? undefined : range;
}

/** Reads the CEL of `lines[from..last]` in the lists of `CEL_PLACES`. */
function scanLists(
  lines: Line[],
  from: number,
  last: number,
  window: LineWindow | undefined,
  found: CelRange[],
): void {
  /** The list being read: its key's column, the column its dashes sit at, the column of an item's keys. */
  let list:
    | { keyIndent: number; dashIndent: number | undefined; itemColumn: number | undefined }
    | undefined;
  for (let index = from; index <= last; index++) {
    const line = lines[index];
    if (line === undefined || isBlank(line.text) || isComment(line.text)) {
      continue;
    }
    const indent = indentOf(line.text);
    const body = line.text.slice(indent);
    const isDash = /^-(?:[ \t]|$)/.test(body);
    if (list !== undefined && (indent < list.keyIndent || (indent === list.keyIndent && !isDash))) {
      list = undefined;
    }
    let column = indent;
    let content = body;
    if (list !== undefined && isDash) {
      list.dashIndent ??= indent;
      if (indent === list.dashIndent) {
        const dash = /^-[ \t]*/.exec(body);
        column = indent + (dash?.[0].length ?? 1);
        content = body.slice(dash?.[0].length ?? 1);
        list.itemColumn = column;
      }
    }
    const celKey = column === list?.itemColumn ? CEL_KEY_PATTERN.exec(content) : null;
    if (celKey !== null) {
      const range = readValue(lines, index, column, column + celKey[0].length, window);
      if (range !== undefined) {
        found.push(range);
      }
      continue;
    }
    if (LIST_KEY_PATTERN.test(content) && (list === undefined || indent <= list.keyIndent)) {
      list = { keyIndent: column, dashIndent: undefined, itemColumn: undefined };
    }
  }
}

/** Reads the CEL of `lines[from..last]` at the `paths` of an admission policy. */
function scanPolicy(
  lines: Line[],
  from: number,
  last: number,
  paths: readonly string[],
  window: LineWindow | undefined,
  found: CelRange[],
): void {
  /** The mapping keys above the current line, each with its column; list levels are not kept. */
  const stack: { column: number; key: string }[] = [];
  for (let index = from; index <= last; index++) {
    const line = lines[index];
    if (line === undefined || isBlank(line.text) || isComment(line.text)) {
      continue;
    }
    const indent = indentOf(line.text);
    let column = indent;
    let content = line.text.slice(indent);
    if (/^-(?:[ \t]|$)/.test(content)) {
      const dash = /^-[ \t]*/.exec(content)?.[0].length ?? 1;
      while ((stack[stack.length - 1]?.column ?? -1) > indent) {
        stack.pop();
      }
      column = indent + dash;
      content = content.slice(dash);
    }
    const key = MAPPING_KEY.exec(content);
    if (key?.[1] === undefined) {
      continue;
    }
    while ((stack[stack.length - 1]?.column ?? -1) >= column) {
      stack.pop();
    }
    stack.push({ column, key: key[1] });
    if (paths.includes(stack.map((entry) => entry.key).join('.'))) {
      const range = readValue(lines, index, column, column + key[0].length, window);
      if (range !== undefined) {
        found.push(range);
      }
    }
  }
}

/**
 * The CEL of a YAML file: the values of the keys in `CEL_PLACES`, and the CEL
 * fields of the admission policies in it, in plain, quoted and block scalars.
 * It reads the text line by line, never throws, and leaves out comments and
 * keys outside their list or policy. With a `window`, it still reads every
 * line to know the lists, but decodes only the CEL that overlaps it. A Kyverno policy has
 * no CEL unless `features.kyverno` is set.
 */
export function findCel(
  text: string,
  window?: LineWindow,
  features: CelFeatures = { kyverno: false },
): CelRange[] {
  const lines = splitLines(text);
  const found: CelRange[] = [];
  const last = Math.min(lines.length - 1, window?.toLine ?? Infinity);
  let start = 0;
  for (let index = 0; index <= lines.length; index++) {
    const boundary = index === lines.length || DOCUMENT_BOUNDARY.test(lines[index]?.text ?? '');
    if (!boundary) {
      continue;
    }
    const end = Math.min(index - 1, last);
    if (start <= end) {
      const paths = policyPaths(lines, start, index - 1, features.kyverno);
      if (paths === undefined) {
        scanLists(lines, start, end, window, found);
      } else {
        scanPolicy(lines, start, end, paths, window, found);
      }
    }
    start = index + 1;
  }
  return found.filter((range) => range.source.trim() !== '');
}
