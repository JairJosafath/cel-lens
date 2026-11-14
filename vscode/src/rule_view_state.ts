// What the cel-lens view says when it has no row: which of its welcome texts shows. The state is
// worked out from facts alone, so each one is tested without VS Code, and the extension sets it as
// a context key that `viewsWelcome` reads. No `vscode` import.

/** The context key that holds the view's state; `viewsWelcome` entries are chosen by it. */
export const RULE_VIEW_STATE_KEY = 'cel-lens.ruleView.state';

/** The context key that is true while the `kyverno` experimental feature is off. */
export const KYVERNO_OFF_KEY = 'cel-lens.ruleView.kyvernoOff';

/** The context key that is true while the view shows only the rules with a problem. */
export const PROBLEMS_ONLY_KEY = 'cel-lens.ruleView.problemsOnly';

/** The context key that is true while the view follows the editor. */
export const FOLLOW_EDITOR_KEY = 'cel-lens.ruleView.followEditor';

/** The editor submenu's *Reveal in Rule View*: the rule at the cursor, else its document, selected in the view. */
export const REVEAL_RULE_COMMAND = 'cel-lens.rules.reveal';

/** The context key that is true while the active editor's file is one the engine lists rules for. */
export const EDITOR_HAS_RULES_KEY = 'cel-lens.editorHasRules';

/** The context key that says what the cursor is in: `rule`, `ruleWithExamples`, or unset outside a rule. */
export const RULE_AT_CURSOR_KEY = 'cel-lens.ruleAtCursor';

/** The id of the view: `view` in a `when` clause, and what `createTreeView` takes. */
export const RULE_VIEW_ID = 'cel-lens.rules';

/** The id of the view container in the activity bar. */
export const RULE_VIEW_CONTAINER_ID = 'cel-lens';

/** Whether the engine runs, is on its way up, or is not there. */
export type EngineStatus = 'starting' | 'running' | 'down';

/**
 * What the view shows: `tree`, rows; or, with none, why: no folder is open, the engine is not
 * running, the engine cannot list rules, the engine is still looking, or it found none.
 */
export type RuleViewState =
  'tree' | 'noFolder' | 'engineDown' | 'unsupported' | 'scanning' | 'noProblems' | 'empty';

/** Every state, in the order the manifest's welcome texts list them. */
export const RULE_VIEW_STATES: readonly RuleViewState[] = [
  'noFolder',
  'engineDown',
  'unsupported',
  'scanning',
  'noProblems',
  'empty',
];

export interface ViewFacts {
  readonly engine: EngineStatus;
  /** The engine refused `cel-lens.ruleList` and has never answered it. */
  readonly unsupported: boolean;
  /** How many workspace folders are open. */
  readonly folders: number;
  /** An answer of the list was applied since the engine started. */
  readonly answered: boolean;
  /** The engine says it is still walking the workspace. */
  readonly scanning: boolean;
  /** The last ask failed, and no answer has come: nothing is being waited for. */
  readonly failed: boolean;
  /** The view shows only the rules with a problem. */
  readonly problemsOnly: boolean;
  /** The list holds a file with rules, whatever the filter shows of it. */
  readonly listed: boolean;
  /** The tree has a row, a warning row included. */
  readonly rows: boolean;
}

/** The state of the view: the first of these that holds. */
export function ruleViewState(facts: ViewFacts): RuleViewState {
  if (facts.rows) {
    return 'tree';
  }
  if (facts.engine === 'down') {
    return 'engineDown';
  }
  if (facts.unsupported) {
    return 'unsupported';
  }
  if (facts.folders === 0) {
    return 'noFolder';
  }
  if (facts.problemsOnly && facts.listed) {
    return 'noProblems';
  }
  if (facts.engine === 'starting' || facts.scanning || !facts.answered) {
    return 'scanning';
  }
  return 'empty';
}

/**
 * Whether the view shows its progress bar: while the engine starts, looks for the first answer or
 * reports a scan it has not finished, and there is a folder to look in.
 */
export function isLooking(facts: ViewFacts): boolean {
  if (facts.engine === 'down' || facts.unsupported || facts.failed || facts.folders === 0) {
    return false;
  }
  return facts.engine === 'starting' || facts.scanning || !facts.answered;
}
