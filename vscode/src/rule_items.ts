// The words, icons and tooltips of the rows of the cel-lens view. They say what the engine said, in
// the words of the line above a rule, and decide nothing else: no state is worked out here but
// which icon a state gets. No `vscode` import.
import type { ListedDocument, ListedRule } from './rule_list';

/** A product icon of VS Code's, by id, with the theme colour it is drawn in, if any. */
export interface Icon {
  readonly id: string;
  readonly color?: string;
}

/** The state of a rule as its icon says it, worst first. */
export type RuleIconState = 'error' | 'warning' | 'pass' | 'check';

const ICONS: Readonly<Record<RuleIconState, Icon>> = {
  error: { id: 'error', color: 'problemsErrorIcon.foreground' },
  warning: { id: 'warning', color: 'problemsWarningIcon.foreground' },
  pass: { id: 'pass', color: 'testing.iconPassed' },
  check: { id: 'check' },
};

/**
 * The state of a rule's icon, worst first: an error (the CEL does not compile), a warning (it
 * compiles with a warning, or an example is wrong or errors), passing examples, or a plain check
 * for a rule that compiles. `showExamples` is false for a folder that turns example results off:
 * such a rule shows no example state, as its lines show none.
 */
export function ruleIconState(rule: ListedRule, showExamples: boolean): RuleIconState {
  const examples = showExamples ? rule.examples?.state : undefined;
  if (rule.compile.state === 'error') {
    return 'error';
  }
  if (rule.compile.state === 'warning' || examples === 'wrong' || examples === 'error') {
    return 'warning';
  }
  return examples === 'pass' ? 'pass' : 'check';
}

export function ruleIcon(rule: ListedRule, showExamples: boolean): Icon {
  return ICONS[ruleIconState(rule, showExamples)];
}

/** The line's own words for a rule's CEL: `compiles`, or `1 problem`, `2 problems`. */
export function compileWords(rule: ListedRule): string {
  if (rule.compile.state === 'ok') {
    return 'compiles';
  }
  return `${String(rule.compile.problems)} problem${rule.compile.problems === 1 ? '' : 's'}`;
}

/** A rule's description: the line's words, then ` · ` and the example words when there are any. */
export function ruleDescription(rule: ListedRule, showExamples: boolean): string {
  const examples = showExamples ? rule.examples : undefined;
  return examples === undefined ? compileWords(rule) : `${compileWords(rule)} · ${examples.words}`;
}

/** The kinds written as people say them: the CRD short, Kyverno's with its project. */
const KIND_WORDS: Readonly<Record<string, string>> = {
  CustomResourceDefinition: 'CRD',
  ValidatingAdmissionPolicy: 'ValidatingAdmissionPolicy',
  MutatingAdmissionPolicy: 'MutatingAdmissionPolicy',
  ValidatingPolicy: 'Kyverno ValidatingPolicy',
};

/** The kind of a document in words: `CRD`, `ValidatingAdmissionPolicy`, `Kyverno ValidatingPolicy`. */
export function kindWords(kind: string): string {
  return KIND_WORDS[kind] ?? kind;
}

/** A document's description: its kind and file, such as `CRD · config/crd/bases/widgets.yaml`. */
export function documentDescription(document: ListedDocument): string {
  return `${kindWords(document.kind)} · ${document.file}`;
}

const KIND_ICONS: Readonly<Record<string, string>> = {
  CustomResourceDefinition: 'symbol-class',
  ValidatingAdmissionPolicy: 'shield',
  MutatingAdmissionPolicy: 'tools',
  ValidatingPolicy: 'law',
};

/** The product icon of a document's kind. */
export function documentIcon(kind: string): Icon {
  return { id: KIND_ICONS[kind] ?? 'file-code' };
}

/** What each kind of an expression in a policy is called in its place: `validations[0]`. */
const POLICY_LISTS: Readonly<Record<string, string>> = {
  validation: 'validations',
  matchCondition: 'matchConditions',
  variable: 'variables',
  auditAnnotation: 'auditAnnotations',
  mutation: 'mutations',
};

/** Where a rule is in its document: `v1 · spec.ports[] · rule 0` for a CRD's, `validations[0]` for a policy's. */
export function rulePlace(rule: ListedRule): string {
  if (rule.field !== undefined) {
    return `${POLICY_LISTS[rule.field] ?? rule.field}[${String(rule.index)}]`;
  }
  return [
    ...(rule.version === undefined ? [] : [rule.version]),
    ...(rule.path === undefined || rule.path === '' ? [] : [rule.path]),
    `rule ${String(rule.index)}`,
  ].join(' · ');
}

/** The kinds whose rules no example runs on, whatever the setting. */
const WITHOUT_EXAMPLE_RESULTS = new Set(['MutatingAdmissionPolicy', 'ValidatingPolicy']);

/** Whether example results cover the rules of a kind of document at all. */
export function coversExamples(kind: string): boolean {
  return !WITHOUT_EXAMPLE_RESULTS.has(kind);
}

/** How many of the first problems a tooltip lists. */
const TOOLTIP_PROBLEMS = 3;

/** Escapes what Markdown would read as markup in a line of text. */
export function escapeMarkdown(value: string): string {
  return value.replace(/([\\`*_{}[\]()#+!|<>~])/g, '\\$1').replace(/-(?=\s|$)/g, '\\-');
}

/** A fenced code block holding `code`, fenced with more backticks than any run in it. */
export function codeBlock(code: string): string {
  const longest = Math.max(0, ...(code.match(/`+/g) ?? []).map((run) => run.length));
  const fence = '`'.repeat(Math.max(3, longest + 1));
  return `${fence}\n${code}\n${fence}`;
}

/**
 * A rule's tooltip, in Markdown with no command link: its label, where it is, its CEL in a code
 * block, its first problems, its example words, and, for a kind example results do not cover,
 * that they do not.
 */
export function ruleTooltip(
  rule: ListedRule,
  kind: string,
  showExamples: boolean,
  expression: string | undefined = rule.expression,
): string {
  const parts = [`**${escapeMarkdown(rule.label)}**`, escapeMarkdown(rulePlace(rule))];
  if (expression !== undefined && expression !== '') {
    parts.push(codeBlock(expression));
  }
  const problems = rule.compile.messages
    .slice(0, TOOLTIP_PROBLEMS)
    .map((entry) => `- ${entry.severity}: ${escapeMarkdown(entry.message)}`);
  parts.push(problems.length === 0 ? escapeMarkdown(compileWords(rule)) : problems.join('\n'));
  if (!coversExamples(kind)) {
    parts.push(`Example results do not cover a ${escapeMarkdown(kindWords(kind))}.`);
  } else if (showExamples && rule.examples !== undefined) {
    parts.push(escapeMarkdown(rule.examples.words));
  }
  return parts.join('\n\n');
}

/**
 * What a screen reader says of a rule: its label, the state in a word where the icon alone would
 * tell it (`error`, `warning`), then the description and the first problem.
 */
export function ruleAccessibleLabel(rule: ListedRule, showExamples: boolean): string {
  const first = rule.compile.messages[0];
  const detail = first === undefined || rule.compile.state === 'ok' ? '' : `: ${first.message}`;
  const state = ruleIconState(rule, showExamples);
  const word = state === 'error' || state === 'warning' ? `${state}, ` : '';
  return `${rule.label}, ${word}${ruleDescription(rule, showExamples)}${detail}`;
}

/** A document's row label: its name, or, for one with no `metadata.name`, what it is. */
export function documentLabel(document: ListedDocument): string {
  return document.name === '' ? `(no name) ${kindWords(document.kind)}` : document.name;
}

/** What a screen reader says of a document: its name, its kind and how many rules it has. */
export function documentAccessibleLabel(document: ListedDocument, problems: number): string {
  const rules = `${String(document.rules.length)} rule${document.rules.length === 1 ? '' : 's'}`;
  const bad = problems === 0 ? '' : `, ${String(problems)} with a problem`;
  return `${documentLabel(document)}, ${kindWords(document.kind)}, ${rules}${bad}`;
}

/** A position as the editor counts it: a line, and UTF-16 code units into it. */
interface TextPosition {
  readonly line: number;
  readonly character: number;
}

/** The text between two positions, as VS Code's `getText(range)` cuts it: lines end at `\n`, `\r\n` or `\r`. */
export function textInRange(text: string, start: TextPosition, end: TextPosition): string {
  const lines = text.split(/\r\n|\r|\n/);
  if (start.line >= lines.length) {
    return '';
  }
  return lines
    .slice(start.line, end.line + 1)
    .map((line, at, kept) => {
      const to = at === kept.length - 1 ? end.character : line.length;
      return line.slice(at === 0 ? start.character : 0, to);
    })
    .join('\n');
}
