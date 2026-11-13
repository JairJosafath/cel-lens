import type * as vscode from 'vscode';

import { DEFAULT_EXAMPLE_RESULTS, EXAMPLE_RESULTS_SETTING } from './example_results';
import { engineBooleanSetting, readBooleanSetting, toggleBooleanSetting } from './toggle_setting';

/** Whether example results show for the file at `resource`, its workspace folder's setting included; for the window without one. */
export function readExampleResults(resource?: vscode.Uri): boolean {
  return readBooleanSetting(EXAMPLE_RESULTS_SETTING, DEFAULT_EXAMPLE_RESULTS, resource);
}

/**
 * What the engine is told. It has one switch for the window, so it is on while the window or any
 * workspace folder shows the results; the client hides the part of the line above a rule in a
 * folder that turns them off, and the command there says they are off.
 */
export function engineExampleResults(): boolean {
  return engineBooleanSetting(EXAMPLE_RESULTS_SETTING, DEFAULT_EXAMPLE_RESULTS);
}

/**
 * Turns example results off when they show for the active editor's file, and on when they don't.
 * It writes the scope that sets the value in force there (its workspace folder, the workspace or
 * the user settings), or the user settings when none does.
 */
export async function toggleExampleResults(
  log: Pick<vscode.LogOutputChannel, 'error'>,
): Promise<void> {
  await toggleBooleanSetting(EXAMPLE_RESULTS_SETTING, DEFAULT_EXAMPLE_RESULTS, log);
}
