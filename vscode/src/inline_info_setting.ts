import type * as vscode from 'vscode';

import { DEFAULT_INLINE_INFO, INLINE_INFO_SETTING } from './inline_info';
import { engineBooleanSetting, readBooleanSetting, toggleBooleanSetting } from './toggle_setting';

/** Whether the lines show for the file at `resource`, its workspace folder's setting included; for the window without one. */
export function readInlineInfo(resource?: vscode.Uri): boolean {
  return readBooleanSetting(INLINE_INFO_SETTING, DEFAULT_INLINE_INFO, resource);
}

/**
 * What the engine is told. It has one switch for the window, so it is on while the window or
 * any workspace folder shows the lines; the client hides the lines of a folder that turns them off.
 */
export function engineInlineInfo(): boolean {
  return engineBooleanSetting(INLINE_INFO_SETTING, DEFAULT_INLINE_INFO);
}

/**
 * Turns the lines off when they show for the active editor's file, and on when they don't. It
 * writes the scope that sets the value in force there (its workspace folder, the workspace or the
 * user settings), or the user settings when none does.
 */
export async function toggleInlineInfo(log: Pick<vscode.LogOutputChannel, 'error'>): Promise<void> {
  await toggleBooleanSetting(INLINE_INFO_SETTING, DEFAULT_INLINE_INFO, log);
}
