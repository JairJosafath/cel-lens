import * as vscode from 'vscode';

import {
  COLOR_STYLE_SETTING,
  COLOR_STYLE_SETTING_NAME,
  COLOR_STYLE_SETTING_SECTION,
  LEGACY_COLOR_STYLE_SETTING_NAME,
  LEGACY_COLOR_STYLE_SETTING_SECTION,
} from './color_style';

const MOVE_BUTTON = 'Move Setting';
const OPEN_SETTINGS_BUTTON = 'Open Settings';

const MOVE_MESSAGE =
  `The setting ${LEGACY_COLOR_STYLE_SETTING_NAME} is now ${COLOR_STYLE_SETTING_NAME}. ` +
  'The old name still works, but VS Code flags it as unknown and a later release will drop it.';

interface Scope {
  readonly legacy: unknown;
  readonly target: vscode.ConfigurationTarget;
  /** The folder a workspace-folder value belongs to; VS Code reads and writes it only through the folder's resource. */
  readonly folder?: vscode.Uri;
}

function inspect(section: string, folder?: vscode.Uri) {
  return vscode.workspace.getConfiguration(section, folder).inspect<unknown>(COLOR_STYLE_SETTING);
}

/** Every scope where either key is set: user, workspace, and each workspace folder. */
function scopes(): (Scope & { readonly current: unknown })[] {
  const legacy = inspect(LEGACY_COLOR_STYLE_SETTING_SECTION);
  const current = inspect(COLOR_STYLE_SETTING_SECTION);
  const folders = (vscode.workspace.workspaceFolders ?? []).map((folder) => ({
    legacy: inspect(LEGACY_COLOR_STYLE_SETTING_SECTION, folder.uri)?.workspaceFolderValue,
    current: inspect(COLOR_STYLE_SETTING_SECTION, folder.uri)?.workspaceFolderValue,
    target: vscode.ConfigurationTarget.WorkspaceFolder,
    folder: folder.uri,
  }));
  return [
    {
      legacy: legacy?.globalValue,
      current: current?.globalValue,
      target: vscode.ConfigurationTarget.Global,
    },
    {
      legacy: legacy?.workspaceValue,
      current: current?.workspaceValue,
      target: vscode.ConfigurationTarget.Workspace,
    },
    ...folders,
  ];
}

/**
 * Writes each scope's `celLens.colorStyle` value to `cel-lens.colorStyle` at
 * the same scope, then removes the old key there. A workspace-folder value is
 * moved through that folder's own configuration.
 */
export async function moveLegacyColorStyle(): Promise<void> {
  for (const scope of scopes()) {
    if (scope.legacy !== undefined) {
      const current = vscode.workspace.getConfiguration(COLOR_STYLE_SETTING_SECTION, scope.folder);
      const legacy = vscode.workspace.getConfiguration(
        LEGACY_COLOR_STYLE_SETTING_SECTION,
        scope.folder,
      );
      await current.update(COLOR_STYLE_SETTING, scope.legacy, scope.target);
      await legacy.update(COLOR_STYLE_SETTING, undefined, scope.target);
    }
  }
}

const HINT_SHOWN_KEY = 'legacyColorStyleHintShown';

const FAILURE_MESSAGE =
  `Could not move ${LEGACY_COLOR_STYLE_SETTING_NAME} to ${COLOR_STYLE_SETTING_NAME}. ` +
  'Fix any errors in settings.json, then set the new name by hand.';

async function moveOrReport(log: Pick<vscode.LogOutputChannel, 'error'>): Promise<void> {
  try {
    await moveLegacyColorStyle();
  } catch (error) {
    log.error(`Could not move ${LEGACY_COLOR_STYLE_SETTING_NAME}: ${String(error)}`);
    const button = await vscode.window.showErrorMessage(FAILURE_MESSAGE, OPEN_SETTINGS_BUTTON);
    if (button === OPEN_SETTINGS_BUTTON) {
      await vscode.commands.executeCommand(
        'workbench.action.openSettings',
        COLOR_STYLE_SETTING_NAME,
      );
    }
  }
}

/**
 * Tells the user once, when only the old `celLens.colorStyle` key is set, how
 * to move it to `cel-lens.colorStyle`, with a button that does it. `state`
 * remembers that the hint was shown, so it never comes back.
 */
export async function offerLegacyColorStyleMove(
  state: Pick<vscode.Memento, 'get' | 'update'>,
  log: Pick<vscode.LogOutputChannel, 'error'>,
): Promise<void> {
  const all = scopes();
  if (
    state.get(HINT_SHOWN_KEY) === true ||
    !all.some((scope) => scope.legacy !== undefined) ||
    all.some((scope) => scope.current !== undefined)
  ) {
    return;
  }
  await state.update(HINT_SHOWN_KEY, true);
  const button = await vscode.window.showInformationMessage(MOVE_MESSAGE, MOVE_BUTTON);
  if (button === MOVE_BUTTON) {
    await moveOrReport(log);
  }
}
