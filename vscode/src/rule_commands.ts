import * as vscode from 'vscode';

import { SHOW_LOGS_LABEL } from './button_labels';
import { openBesideRule } from './cellens_editor';
import { isRecord } from './is_record';
import {
  LIST_VALUES_COMMAND,
  SHOW_RULE_COMMAND,
  isCellensLocation,
  moreValuesTitle,
  parseMoreValues,
  parseRuleLocation,
  ruleTarget,
  valueItems,
  type ProblemOrder,
  type RuleLocation,
} from './rule_parts';

/** The log a command writes to when the engine's argument is not what it expects, or a step fails. */
type CommandLog = Pick<vscode.LogOutputChannel, 'warn' | 'error' | 'show'>;

/** What the status bar says when a menu or the palette ran a rule's command with the cursor in no rule. */
export const NO_RULE_MESSAGE = 'cel-lens: the cursor is in no rule';

/** How long the status bar says it, in milliseconds. */
export const NO_RULE_MESSAGE_MS = 3000;

/** Whether the argument is a URI, which VS Code gives a command run from an editor's menu. */
function isUri(argument: unknown): boolean {
  return (
    isRecord(argument) &&
    typeof argument['scheme'] === 'string' &&
    typeof argument['path'] === 'string'
  );
}

/**
 * The argument a rule's command (`cel-lens.showRule`, `cel-lens.showExampleResults`) runs with.
 * From the line above a rule or from the view it is a location, which is left as it is. Run from
 * the editor's menu (the URI of its editor) or the palette (none) it is the location of the rule at
 * the cursor of that editor (the active one for none), looked up now, so a key that lags never runs the wrong rule; with no rule there
 * the status bar says so for three seconds, and the result is `undefined`: do nothing else.
 */
export function ruleArgument(
  argument: unknown,
  atCursor: (uri?: string) => RuleLocation | undefined,
): unknown {
  if (argument !== undefined && !isUri(argument)) {
    return argument;
  }
  // The URI is the editor the menu was opened in, which is not always the active one: in a split
  // editor the title menu of another group gives its own.
  const location = atCursor(isUri(argument) ? String(argument) : undefined);
  if (location === undefined) {
    vscode.window.setStatusBarMessage(NO_RULE_MESSAGE, NO_RULE_MESSAGE_MS);
  }
  return location;
}

function problemOrder(): ProblemOrder {
  return vscode.workspace.getConfiguration('problems').get<unknown>('sortOrder') === 'position'
    ? 'position'
    : 'severity';
}

/**
 * Runs `cel-lens.showRule`, the first part of the line above a rule: opens the problems peek at
 * the rule's first problem, or shows the hover there when the peek cannot reach it, or the rule's
 * hover at its start when it has no problem. A location in a `.cellens`, the entry a value's type
 * comes from, opens beside the rule with the entry selected, as Give it a type opens it.
 */
export async function showRule(location: unknown, log: CommandLog): Promise<void> {
  const rule = parseRuleLocation(location);
  if (rule === undefined) {
    log.warn(
      `${SHOW_RULE_COMMAND} was given no rule location; the engine may not match this extension.`,
    );
    return;
  }
  if (isCellensLocation(rule)) {
    try {
      await openBesideRule(rule.uri, rule.range);
    } catch (error) {
      log.error(`Could not show the entry in ${rule.uri}: ${String(error)}`);
    }
    return;
  }
  const uri = vscode.Uri.parse(rule.uri);
  const { kind, at } = ruleTarget(rule.range, vscode.languages.getDiagnostics(uri), problemOrder());
  try {
    // A peek left open on this problem would make the next-problem command move on to the next
    // one, so it closes first and the command starts afresh from the cursor.
    await vscode.commands.executeCommand('closeMarkersNavigation');
    await vscode.window.showTextDocument(uri, {
      selection: new vscode.Range(at.line, at.character, at.line, at.character),
    });
    await vscode.commands.executeCommand(
      kind === 'peek' ? 'editor.action.marker.next' : 'editor.action.showHover',
    );
  } catch (error) {
    log.error(`Could not show the rule in ${rule.uri}: ${String(error)}`);
  }
}

/**
 * Runs `cel-lens.listValues`, the `+N more` part: a quick pick of the parts the line leaves out,
 * titled after the rule's line; picking one runs its command with its arguments.
 */
export async function listValues(more: unknown, log: CommandLog): Promise<void> {
  const values = parseMoreValues(more);
  if (values === undefined) {
    log.warn(
      `${LIST_VALUES_COMMAND} was given no values; the engine may not match this extension.`,
    );
    return;
  }
  const picked = await vscode.window.showQuickPick(valueItems(values.parts), {
    title: moreValuesTitle(values.line),
    placeHolder: 'Pick a value and what to do with it',
  });
  if (picked === undefined || picked.part.command === '') {
    return;
  }
  try {
    await vscode.commands.executeCommand(picked.part.command, ...picked.part.arguments);
  } catch (error) {
    log.error(`Could not run ${picked.part.command}: ${String(error)}`);
    const button = await vscode.window.showErrorMessage(
      `cel-lens could not run "${picked.part.title}". Show the logs for the reason.`,
      SHOW_LOGS_LABEL,
    );
    if (button === SHOW_LOGS_LABEL) {
      log.show();
    }
  }
}
