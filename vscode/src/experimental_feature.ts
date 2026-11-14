import * as vscode from 'vscode';

import { OPEN_EXPERIMENTAL_SETTING_LABEL } from './button_labels';
import {
  CELLENS_TYPES_FEATURE,
  EXPERIMENTAL_SETTING,
  addExperimental,
  parseExperimental,
} from './experimental';

/** The experimental features the setting `cel-lens.experimental` lists now. */
export function readExperimental(): string[] {
  return parseExperimental(
    vscode.workspace.getConfiguration('cel-lens').get<unknown>(EXPERIMENTAL_SETTING, []),
  );
}

/** Whether the experimental feature `name` is on. */
export function featureOn(name: string): boolean {
  return readExperimental().includes(name);
}

/** The log a failed write goes to. */
type SettingLog = Pick<vscode.LogOutputChannel, 'error'>;

/**
 * Adds `name` to `cel-lens.experimental` at the scope that sets it: the workspace's when it does,
 * else the user's, which is the value in force (the setting has window scope, so no folder has
 * one); the user's when none does. A name already listed is left. A write that fails is logged
 * and shown once, with a button to the setting. Resolves to whether the feature is listed now.
 */
export async function turnOnExperimental(name: string, log: SettingLog): Promise<boolean> {
  const configuration = vscode.workspace.getConfiguration('cel-lens');
  const inspected = configuration.inspect<unknown>(EXPERIMENTAL_SETTING);
  const inWorkspace = inspected?.workspaceValue !== undefined;
  const value = inWorkspace ? inspected.workspaceValue : inspected?.globalValue;
  const next = addExperimental(value, name);
  if (next === undefined) {
    return true;
  }
  try {
    await configuration.update(
      EXPERIMENTAL_SETTING,
      next,
      inWorkspace ? vscode.ConfigurationTarget.Workspace : vscode.ConfigurationTarget.Global,
    );
    return true;
  } catch (error) {
    log.error(`Could not turn on ${name} in cel-lens.experimental: ${String(error)}`);
    // Not awaited: a notification stays pending while it is open.
    void vscode.window
      .showWarningMessage(
        `cel-lens could not change the cel-lens.experimental setting. Fix any errors in settings.json, then add "${name}" to it by hand.`,
        OPEN_EXPERIMENTAL_SETTING_LABEL,
      )
      .then((button) =>
        button === OPEN_EXPERIMENTAL_SETTING_LABEL
          ? vscode.commands.executeCommand(
              'workbench.action.openSettings',
              `cel-lens.${EXPERIMENTAL_SETTING}`,
            )
          : undefined,
      );
    return false;
  }
}

/** Turns on `cellens-types`. */
export function turnOnCellensTypes(log: SettingLog): Promise<boolean> {
  return turnOnExperimental(CELLENS_TYPES_FEATURE, log);
}
