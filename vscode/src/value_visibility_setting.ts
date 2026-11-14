import type * as vscode from 'vscode';

import { DEFAULT_VALUE_TYPES, VALUE_TYPES_SETTING } from './value_visibility';
import { readBooleanSetting, toggleBooleanSetting } from './toggle_setting';

/** Whether the values a rule reads show for the file at `resource`, its workspace folder's setting included; for the window without one. */
export function readValueTypes(resource?: vscode.Uri): boolean {
  return readBooleanSetting(VALUE_TYPES_SETTING, DEFAULT_VALUE_TYPES, resource);
}

/**
 * Hides the values when they show for the active editor's file, and shows them when they don't.
 * It writes the scope that sets the value in force there (its workspace folder, the workspace or
 * the user settings), or the user settings when none does. The engine is told nothing: it
 * always sends the values, and the client hides them.
 */
export async function toggleValueTypes(log: Pick<vscode.LogOutputChannel, 'error'>): Promise<void> {
  await toggleBooleanSetting(VALUE_TYPES_SETTING, DEFAULT_VALUE_TYPES, log);
}
