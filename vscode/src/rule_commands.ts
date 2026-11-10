import * as vscode from 'vscode';

import { SHOW_LOGS_LABEL } from './button_labels';
import { openCellens } from './cellens_editor';
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
} from './rule_parts';

/** The log a command writes to when the engine's argument is not what it expects, or a step fails. */
type CommandLog = Pick<vscode.LogOutputChannel, 'warn' | 'error' | 'show'>;

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
      await openCellens(rule.uri, rule.range);
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
