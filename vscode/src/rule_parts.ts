import { isRecord } from './is_record';

/** The command of the first part of the line above a rule: the rule's problems, or its hover. */
export const SHOW_RULE_COMMAND = 'cel-lens.showRule';

/** The command of the `+N more` part: lists the parts the line leaves out. */
export const LIST_VALUES_COMMAND = 'cel-lens.listValues';

/** The `source` of the engine's diagnostics. */
const ENGINE_SOURCE = 'cel-lens';

/** `vscode.DiagnosticSeverity.Hint`: VS Code's next-problem navigation skips hints. */
const HINT_SEVERITY = 3;

/** What the engine writes between a part's value and its action, as in `self.spec.port: dyn · Give it a type`. */
const ACTION_SEPARATOR = ' · ';

/** An LSP position: zero-based line and UTF-16 character. */
export interface Position {
  readonly line: number;
  readonly character: number;
}

export interface Range {
  readonly start: Position;
  readonly end: Position;
}

/** The argument of `cel-lens.showRule`: where the rule's expression is. */
export interface RuleLocation {
  readonly uri: string;
  readonly range: Range;
}

/** A diagnostic as VS Code holds it; `severity` is a `vscode.DiagnosticSeverity`, 0 (error) to 3 (hint). */
export interface Problem {
  readonly range: Range;
  readonly severity: number;
  readonly source?: string;
}

/** How VS Code orders problems for its next-problem navigation: the `problems.sortOrder` setting. */
export type ProblemOrder = 'severity' | 'position';

/** Where `cel-lens.showRule` takes a person: the problems peek, or the hover, at `at`. */
export interface RuleTarget {
  readonly kind: 'peek' | 'hover';
  readonly at: Position;
}

/** One part of a line, as the engine sends it: an LSP Command. */
export interface Part {
  readonly title: string;
  /** Empty for a part that only says something. */
  readonly command: string;
  readonly arguments: readonly unknown[];
}

/** The argument of `cel-lens.listValues` once the client added the rule's line: from 1, as people count. */
export interface MoreValues {
  readonly line: number;
  readonly parts: readonly Part[];
}

/** One line of the quick pick of the parts left out: the value, and what picking it does. */
export interface ValueItem {
  readonly label: string;
  readonly description: string;
  readonly part: Part;
}

function isCount(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0;
}

function parsePosition(value: unknown): Position | undefined {
  if (!isRecord(value) || !isCount(value['line']) || !isCount(value['character'])) {
    return undefined;
  }
  return { line: value['line'], character: value['character'] };
}

function parseRange(value: unknown): Range | undefined {
  const start = isRecord(value) ? parsePosition(value['start']) : undefined;
  const end = isRecord(value) ? parsePosition(value['end']) : undefined;
  return start === undefined || end === undefined ? undefined : { start, end };
}

/**
 * Whether a location of `cel-lens.showRule` is in a `.cellens`, as the part of a value whose type
 * an entry gives points at that entry, rather than in the rule's own document.
 */
export function isCellensLocation(location: RuleLocation): boolean {
  return location.uri.replace(/[?#].*$/, '').endsWith('/.cellens');
}

/** Reads the argument of `cel-lens.showRule`, `{uri, range}`; anything else gives `undefined`. */
export function parseRuleLocation(value: unknown): RuleLocation | undefined {
  if (!isRecord(value) || typeof value['uri'] !== 'string' || value['uri'] === '') {
    return undefined;
  }
  const range = parseRange(value['range']);
  return range === undefined ? undefined : { uri: value['uri'], range };
}

function compare(a: Position, b: Position): number {
  return a.line - b.line || a.character - b.character;
}

function contains(range: Range, at: Position): boolean {
  return compare(range.start, at) <= 0 && compare(at, range.end) <= 0;
}

/** The order of VS Code's next-problem navigation within one file. */
function navigationOrder(order: ProblemOrder): (a: Problem, b: Problem) => number {
  const byPosition = (a: Problem, b: Problem): number =>
    compare(a.range.start, b.range.start) || compare(a.range.end, b.range.end);
  return order === 'position'
    ? (a, b) => byPosition(a, b) || a.severity - b.severity
    : (a, b) => a.severity - b.severity || byPosition(a, b);
}

function isNavigable(problem: Problem): boolean {
  return problem.severity !== HINT_SEVERITY;
}

/**
 * Where to show the rule in `rule`: the peek at its first problem (an engine diagnostic that
 * starts in it and is no hint, which VS Code's next-problem command skips, first in VS Code's
 * order), or its hover when it has none: at its first hint if it has one, else at its start. The
 * next-problem command, run from that problem's start, opens the first problem in its own order
 * that contains or follows that point; when that is no problem of the rule (in severity order, a
 * worse one later in the file), the hover at the problem's start shows it instead.
 */
export function ruleTarget(
  rule: Range,
  problems: readonly Problem[],
  order: ProblemOrder,
): RuleTarget {
  const byOrder = navigationOrder(order);
  const own = problems
    .filter((problem) => problem.source === ENGINE_SOURCE && contains(rule, problem.range.start))
    .sort(byOrder);
  const first = own.find(isNavigable);
  if (first === undefined) {
    return { kind: 'hover', at: own[0]?.range.start ?? rule.start };
  }
  const at = first.range.start;
  const reached = problems
    .filter(isNavigable)
    .sort(byOrder)
    .find((problem) => contains(problem.range, at) || compare(at, problem.range.start) <= 0);
  return { kind: reached !== undefined && own.includes(reached) ? 'peek' : 'hover', at };
}

function isMoreValuesShape(value: unknown): boolean {
  return isRecord(value) && isCount(value['line']) && Array.isArray(value['parts']);
}

/**
 * The arguments the client gives `cel-lens.listValues`: one, the lens's line from 1 and the
 * engine's parts. Arguments already in that form stay as they are, so a lens wrapped when listed
 * and again when resolved keeps one line.
 */
export function listValuesArguments(
  lensLine: number,
  args: readonly unknown[] | undefined,
): unknown[] {
  const parts = args ?? [];
  if (parts.length === 1 && isMoreValuesShape(parts[0])) {
    return [...parts];
  }
  return [{ line: lensLine + 1, parts }];
}

function parsePart(value: unknown): Part | undefined {
  if (!isRecord(value) || typeof value['title'] !== 'string') {
    return undefined;
  }
  const command = value['command'] ?? '';
  const args = value['arguments'] ?? [];
  if (typeof command !== 'string' || !Array.isArray(args)) {
    return undefined;
  }
  return { title: value['title'], command, arguments: args };
}

/**
 * Reads the argument of `cel-lens.listValues`: the rule's line and its parts. Parts that are no
 * Command are dropped; no line, or no part left, gives `undefined`.
 */
export function parseMoreValues(value: unknown): MoreValues | undefined {
  if (!isRecord(value) || !isCount(value['line']) || !Array.isArray(value['parts'])) {
    return undefined;
  }
  const parts = value['parts'].map(parsePart).filter((part): part is Part => part !== undefined);
  return parts.length === 0 ? undefined : { line: value['line'], parts };
}

/** The quick pick's title, such as `More of the rule on line 16`. */
export function moreValuesTitle(line: number): string {
  return `More of the rule on line ${String(line)}`;
}

/** Each part as a quick pick line: the value before the last ` · ` as the label, the action after it. */
export function valueItems(parts: readonly Part[]): ValueItem[] {
  return parts.map((part) => {
    const cut = part.title.lastIndexOf(ACTION_SEPARATOR);
    return cut === -1
      ? { label: part.title, description: '', part }
      : {
          label: part.title.slice(0, cut),
          description: part.title.slice(cut + ACTION_SEPARATOR.length),
          part,
        };
  });
}
