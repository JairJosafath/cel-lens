import * as vscode from 'vscode';

import { settingInForce, toggleScope, type SettingScope } from './boolean_setting';

const SECTION = 'cel-lens';

const OPEN_SETTINGS_BUTTON = 'Open Settings';

/** Whether the boolean setting `cel-lens.<key>` is on for `resource`, its workspace folder's value included; for the window without one. */
export function readBooleanSetting(key: string, fallback: boolean, resource?: vscode.Uri): boolean {
  return settingInForce(
    vscode.workspace.getConfiguration(SECTION, resource).inspect<unknown>(key),
    fallback,
  );
}

/**
 * What the engine is told of a boolean setting. It has one switch for the window, so it is on
 * while the window or any workspace folder has the setting on; the client hides what it can of a
 * folder that turns it off.
 */
export function engineBooleanSetting(key: string, fallback: boolean): boolean {
  return (
    readBooleanSetting(key, fallback) ||
    (vscode.workspace.workspaceFolders ?? []).some((folder) =>
      readBooleanSetting(key, fallback, folder.uri),
    )
  );
}

function target(scope: SettingScope): vscode.ConfigurationTarget {
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
 * Flips the boolean setting `cel-lens.<key>` for the active editor's file: off when it is on
 * there, and on when it is not. It writes the scope that sets the value in force there (its
 * workspace folder, the workspace or the user settings), or the user settings when none does. A
 * write that fails is logged and shown once, with a button to the setting.
 */
export async function toggleBooleanSetting(
  key: string,
  fallback: boolean,
  log: Pick<vscode.LogOutputChannel, 'error'>,
): Promise<void> {
  const configuration = vscode.workspace.getConfiguration(
    SECTION,
    vscode.window.activeTextEditor?.document.uri,
  );
  const values = configuration.inspect<unknown>(key);
  const name = `${SECTION}.${key}`;
  try {
    await configuration.update(key, !settingInForce(values, fallback), target(toggleScope(values)));
  } catch (error) {
    log.error(`Could not change ${name}: ${String(error)}`);
    const button = await vscode.window.showErrorMessage(
      `Could not change ${name}. Fix any errors in settings.json, then toggle it again.`,
      OPEN_SETTINGS_BUTTON,
    );
    if (button === OPEN_SETTINGS_BUTTON) {
      await vscode.commands.executeCommand('workbench.action.openSettings', name);
    }
  }
}
