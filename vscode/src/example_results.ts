import { settingInForce, type ScopedValues } from './boolean_setting';
import { isRecord } from './is_record';
import type { Position } from './rule_parts';
import type { PickEntry, PickLine } from './value_types';

/** The `cel-lens.exampleResults` setting, as contributed in package.json. */
export const EXAMPLE_RESULTS_SETTING = 'exampleResults';
export const EXAMPLE_RESULTS_SETTING_NAME = `cel-lens.${EXAMPLE_RESULTS_SETTING}`;

/** The results are on until a person turns them off. */
export const DEFAULT_EXAMPLE_RESULTS = true;

/** The `cel-lens.toggleExampleResults` command, as contributed in package.json. */
export const TOGGLE_EXAMPLE_RESULTS_COMMAND = 'cel-lens.toggleExampleResults';

/**
 * The command of the part of the line above a CRD's rule that says what the rule does to its
 * examples. The engine names it and gives it the rule's location, so it has no palette entry.
 */
export const SHOW_EXAMPLE_RESULTS_COMMAND = 'cel-lens.showExampleResults';

/** The engine's command that runs a rule on its examples and answers each verdict. */
export const ENGINE_EXAMPLE_RESULTS = 'cel-lens.exampleResults';

/** The label of the notification button that turns the example results on. */
export const TURN_ON_LABEL = 'Turn On';

/** What a rule's part says when the results are off for the file, with the button that turns them on. */
export const EXAMPLE_RESULTS_OFF_MESSAGE =
  'Example results are off for this file. Turn them on to see what each rule does to its examples.';

/** The placeholder of the picker of a rule's example results. */
export const RESULTS_PLACEHOLDER = 'Pick an example to open it at its line';

/** Whether the results show: the value of the narrowest scope that sets one, else on. */
export function exampleResultsInForce(values: ScopedValues | undefined): boolean {
  return settingInForce(values, DEFAULT_EXAMPLE_RESULTS);
}

/** The status bar menu entry, such as `Example results: on`. */
export function exampleResultsLabel(on: boolean): string {
  return `Example results: ${on ? 'on' : 'off'}`;
}

/** The picker's title, naming the rule by its line from 1, such as `Example results of the rule on line 16`. */
export function resultsTitle(line: number): string {
  return `Example results of the rule on line ${String(line)}`;
}

/** What a rule did to an example, as the engine says it. */
export type Verdict = 'allowed' | 'refused' | 'not run' | 'error';

/** One example's verdict from one rule: the example, what it expects and the API server's message. */
export interface ExampleResult {
  readonly uri: string;
  /** The example's file below its workspace folder, as the engine names it. */
  readonly file: string;
  /** The line the example's document starts at, from 1. */
  readonly line: number;
  readonly operation: 'create' | 'update';
  readonly expect: 'allowed' | 'refused';
  /** The part of the message an example that expects to be refused says it expects. */
  readonly expectMessage?: string;
  readonly result: Verdict;
  /** The API server's refusals, its messages with their paths. */
  readonly refusals: readonly string[];
  /** Why the rule did not run, or what stopped it. */
  readonly note?: string;
  /** Whether the verdict is not what the example expects. */
  readonly wrong: boolean;
}

const VERDICTS: readonly string[] = ['allowed', 'refused', 'not run', 'error'];

function isVerdict(value: unknown): value is Verdict {
  return typeof value === 'string' && VERDICTS.includes(value);
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

/** The last two parts of a URI, for an example whose file the engine did not name. */
function fileOf(uri: string): string {
  return uri.split('/').slice(-2).join('/');
}

function parseResult(value: unknown): ExampleResult | undefined {
  if (!isRecord(value) || !isRecord(value['example']) || !isVerdict(value['result'])) {
    return undefined;
  }
  const example = value['example'];
  const uri = text(example['uri']);
  if (uri === undefined) {
    return undefined;
  }
  const line = example['line'];
  const refusals = Array.isArray(value['refusals'])
    ? value['refusals'].flatMap((refusal) =>
        isRecord(refusal) && text(refusal['message']) !== undefined
          ? [String(refusal['message'])]
          : [],
      )
    : [];
  const note = text(value['error']) ?? text(value['reason']);
  const expectMessage = text(example['message']);
  return {
    uri,
    file: text(example['file']) ?? fileOf(uri),
    line: typeof line === 'number' && Number.isInteger(line) && line >= 1 ? line : 1,
    operation: example['operation'] === 'update' ? 'update' : 'create',
    expect: example['expect'] === 'refused' ? 'refused' : 'allowed',
    ...(expectMessage !== undefined && { expectMessage }),
    result: value['result'],
    refusals,
    ...(note !== undefined && { note }),
    wrong: value['expected'] === false || text(value['wrong']) !== undefined,
  };
}

function startOf(rule: Record<string, unknown>): Position | undefined {
  const location = rule['location'];
  const range = isRecord(location) ? location['range'] : undefined;
  const start = isRecord(range) ? range['start'] : undefined;
  return isRecord(start) &&
    typeof start['line'] === 'number' &&
    typeof start['character'] === 'number'
    ? { line: start['line'], character: start['character'] }
    : undefined;
}

/**
 * Reads the answer of `cel-lens.exampleResults` for the rule that starts at `at`: the verdict of
 * each of its examples, in the order the engine gives them. Never another rule's: when no rule of
 * the answer starts at `at`, as for a part whose location went stale after an edit, it is
 * `'changed'`, even when the answer holds one rule. `undefined` when the answer is no list of
 * rules, as an engine that does not match this extension would give.
 */
export function parseRuleResults(
  answer: unknown,
  at: Position,
): ExampleResult[] | 'changed' | undefined {
  if (!isRecord(answer) || !Array.isArray(answer['rules'])) {
    return undefined;
  }
  const rule = answer['rules'].filter(isRecord).find((each) => {
    const start = startOf(each);
    return start?.line === at.line && start.character === at.character;
  });
  if (rule === undefined) {
    return 'changed';
  }
  const results = rule['results'];
  return Array.isArray(results)
    ? results.map(parseResult).filter((result) => result !== undefined)
    : [];
}

/** What a line of the picker does when picked. */
export type ResultPick =
  | { readonly action: 'open'; readonly uri: string; readonly file: string; readonly line: number }
  | { readonly action: 'showLogs' }
  | { readonly action: 'none' };

/** The line of the picker while the engine runs the rule on its examples. */
export const COMPUTING_LINE: PickEntry<ResultPick> = {
  label: '$(sync~spin) Computing the example results…',
  detail: "The engine runs the rule on each example with the API server's own validator",
  value: { action: 'none' },
};

/** The line of the picker for a rule the engine has no result for. */
export const NO_RESULTS_LINE: PickEntry<ResultPick> = {
  label: '$(info) No example results yet',
  detail: 'They are being computed, or the workspace has no document of a version the CRD serves',
  value: { action: 'none' },
};

/** The line of the picker for a part whose rule moved or changed since the line was drawn. */
export const RULE_CHANGED_LINE: PickEntry<ResultPick> = {
  label: '$(info) The rule changed',
  detail: 'Click its part above the rule again',
  value: { action: 'none' },
};

/** The line of the picker when the results could not be had: the reason, or a way to the logs. */
export function failedLine(reason: string, offerLogs: boolean): PickEntry<ResultPick> {
  return {
    label: '$(error) Could not get the example results',
    detail: reason,
    value: offerLogs ? { action: 'showLogs' } : { action: 'none' },
  };
}

/** The headings of the picker's two groups: the verdicts that are not what the example expects, and the others. */
const WRONG_GROUP = 'Not as expected';
const RIGHT_GROUP = 'As expected';

function icon(result: ExampleResult): string {
  if (result.wrong) {
    return '$(warning)';
  }
  switch (result.result) {
    case 'error':
      return '$(error)';
    case 'not run':
      return '$(circle-slash)';
    case 'allowed':
    case 'refused':
      return '$(pass)';
  }
}

/** What the example expected, for a verdict that is not it, such as `expected refused with "at most 10"`. */
function expectation(result: ExampleResult): string {
  return result.expect === 'refused' && result.expectMessage !== undefined
    ? `expected refused with "${result.expectMessage}"`
    : `expected ${result.expect}`;
}

/** The verdict in words: `refused`, `refused · expected allowed`, `allowed · as an update`. */
function verdictText(result: ExampleResult): string {
  return [
    result.result,
    ...(result.wrong ? [expectation(result)] : []),
    ...(result.operation === 'update' ? ['as an update'] : []),
  ].join(' · ');
}

/** The API server's message: its refusals, or why the rule did not run or what stopped it. */
function messageText(result: ExampleResult): string {
  return [...result.refusals, ...(result.note === undefined ? [] : [result.note])].join('; ');
}

/** The line of one example: its file and line, its verdict, and the API server's message. */
export function resultLine(result: ExampleResult): PickEntry<ResultPick> {
  const message = messageText(result);
  return {
    label: `${icon(result)} ${result.file}:${String(result.line)}`,
    description: verdictText(result),
    ...(message !== '' && { detail: message }),
    value: { action: 'open', uri: result.uri, file: result.file, line: result.line },
  };
}

function rank(result: ExampleResult): number {
  return result.wrong ? 0 : result.result === 'error' ? 1 : 2;
}

/**
 * The picker's lines: the wrong verdicts first, then the errors, then the others, each group in
 * the order the engine gives. With both wrong and right verdicts, a heading names each group.
 */
export function resultLines(results: readonly ExampleResult[]): PickLine<ResultPick>[] {
  const ordered = results
    .map((result, at) => ({ result, at }))
    .sort((a, b) => rank(a.result) - rank(b.result) || a.at - b.at)
    .map(({ result }) => result);
  const wrong = ordered.filter((result) => result.wrong);
  const right = ordered.filter((result) => !result.wrong);
  if (wrong.length === 0 || right.length === 0) {
    return ordered.map(resultLine);
  }
  return [
    { separator: true, label: WRONG_GROUP },
    ...wrong.map(resultLine),
    { separator: true, label: RIGHT_GROUP },
    ...right.map(resultLine),
  ];
}
