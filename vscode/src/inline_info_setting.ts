import * as vscode from 'vscode';

import {
  INLINE_INFO_SETTING,
  INLINE_INFO_SETTING_NAME,
  inlineInfoInForce,
  toggleScope,
  type InlineInfoScope,
} from './inline_info';

const SECTION = 'cel-lens';

const OPEN_SETTINGS_BUTTON = 'Open Settings';

const FAILURE_MESSAGE = `Could not change ${INLINE_INFO_SETTING_NAME}. Fix any errors in settings.json, then toggle it again.`;

/** Whether the lines show for the file at `resource`, its workspace folder's setting included; for the window without one. */
export function readInlineInfo(resource?: vscode.Uri): boolean {
  return inlineInfoInForce(
    vscode.workspace.getConfiguration(SECTION, resource).inspect<unknown>(INLINE_INFO_SETTING),
  );
}

/**
 * What the engine is told. It has one switch for the window, so it is on while the window or
 * any workspace folder shows the lines; the client hides the lines of a folder that turns them off.
 */
export function engineInlineInfo(): boolean {
  return (
    readInlineInfo() ||
    (vscode.workspace.workspaceFolders ?? []).some((folder) => readInlineInfo(folder.uri))
  );
}

function target(scope: InlineInfoScope): vscode.ConfigurationTarget {
  switch (scope) {
    case 'user':
      return vscode.ConfigurationTarget.Global;
    case 'workspace':
      return vscode.ConfigurationTarget.Workspace;
    case 'folder':
      return vscode.ConfigurationTarget.WorkspaceFolder;
  }
}

/**
 * Turns the lines off when they show for the active editor's file, and on when they don't. It
 * writes the scope that sets the value in force there (its workspace folder, the workspace or the
 * user settings), or the user settings when none does.
 */
export async function toggleInlineInfo(log: Pick<vscode.LogOutputChannel, 'error'>): Promise<void> {
  const configuration = vscode.workspace.getConfiguration(
    SECTION,
    vscode.window.activeTextEditor?.document.uri,
  );
  const values = configuration.inspect<unknown>(INLINE_INFO_SETTING);
  try {
    await configuration.update(
      INLINE_INFO_SETTING,
      !inlineInfoInForce(values),
      target(toggleScope(values)),
    );
  } catch (error) {
    log.error(`Could not change ${INLINE_INFO_SETTING_NAME}: ${String(error)}`);
    const button = await vscode.window.showErrorMessage(FAILURE_MESSAGE, OPEN_SETTINGS_BUTTON);
    if (button === OPEN_SETTINGS_BUTTON) {
      await vscode.commands.executeCommand(
        'workbench.action.openSettings',
        INLINE_INFO_SETTING_NAME,
      );
    }
  }
}
