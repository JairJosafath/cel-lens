import * as vscode from 'vscode';

import type { CelStyle } from './cel_styles';
import { readStyle } from './color_painter';
import {
  COLOR_STYLE_SETTING,
  COLOR_STYLE_SETTING_NAME,
  COLOR_STYLE_SETTING_SECTION,
  DEFAULT_COLOR_STYLE,
  LEGACY_COLOR_STYLE_SETTING_SECTION,
  parseColorStyle,
} from './color_style';
import type { SettingScope } from './boolean_setting';
import { configurationTarget, reportFailedToggle } from './toggle_setting';

/** The key in `globalState` under which the style the toggle last turned off is kept. */
export const LAST_COLOR_STYLE_KEY = 'cel-lens.colorStyleBeforeOff';

/** The values a style setting has at each scope, as `WorkspaceConfiguration.inspect` gives them. */
interface Scoped {
  readonly globalValue?: unknown;
  readonly workspaceValue?: unknown;
  readonly workspaceFolderValue?: unknown;
}

/** The narrowest scope that sets a value, else `undefined`. */
function narrowest(values: Scoped | undefined): SettingScope | undefined {
  if (values?.workspaceFolderValue !== undefined) {
    return 'folder';
  }
  if (values?.workspaceValue !== undefined) {
    return 'workspace';
  }
  return values?.globalValue === undefined ? undefined : 'user';
}

/**
 * The scope to write: where the value in force is set. `cel-lens.colorStyle` counts first, and the
 * old `celLens.colorStyle` only where the new key is set nowhere, as the painter reads them; the
 * user settings when neither is set.
 */
function scopeToWrite(resource: vscode.Uri | undefined): SettingScope {
  const inspect = (section: string): Scoped | undefined =>
    vscode.workspace.getConfiguration(section, resource).inspect<unknown>(COLOR_STYLE_SETTING);
  return (
    narrowest(inspect(COLOR_STYLE_SETTING_SECTION)) ??
    narrowest(inspect(LEGACY_COLOR_STYLE_SETTING_SECTION)) ??
    'user'
  );
}

/** What the toggle writes for the style in force: `off`, or, turning on, the style it last turned off, else `cel`. */
export function toggledStyle(current: CelStyle, kept: unknown): CelStyle {
  if (current !== 'off') {
    return 'off';
  }
  const last = parseColorStyle(kept);
  return kept !== undefined && last !== 'off' ? last : DEFAULT_COLOR_STYLE;
}

/**
 * Turns the colors off when the active file has them, and back to the style they had when it has
 * none. The style it turns off is kept in `state`, so back is the style the person picked. It
 * writes the scope that sets the value in force (the workspace folder, the workspace or the user
 * settings), or the user settings when none does; the painter repaints at once, as for any change
 * of the setting. A write that fails is logged and shown once, with a button to the setting.
 */
export async function toggleColors(
  state: Pick<vscode.Memento, 'get' | 'update'>,
  log: Pick<vscode.LogOutputChannel, 'error'>,
): Promise<void> {
  const resource = vscode.window.activeTextEditor?.document.uri;
  const current = readStyle(resource);
  const next = toggledStyle(current, state.get<unknown>(LAST_COLOR_STYLE_KEY));
  try {
    if (current !== 'off') {
      await state.update(LAST_COLOR_STYLE_KEY, current);
    }
    await vscode.workspace
      .getConfiguration(COLOR_STYLE_SETTING_SECTION, resource)
      .update(COLOR_STYLE_SETTING, next, configurationTarget(scopeToWrite(resource)));
  } catch (error) {
    await reportFailedToggle(COLOR_STYLE_SETTING_NAME, error, log);
  }
}
