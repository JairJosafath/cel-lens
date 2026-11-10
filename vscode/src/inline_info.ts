/** The `cel-lens.inlineInfo` setting, as contributed in package.json. */
export const INLINE_INFO_SETTING = 'inlineInfo';
export const INLINE_INFO_SETTING_NAME = `cel-lens.${INLINE_INFO_SETTING}`;

/** The `cel-lens.toggleInlineInfo` command, as contributed in package.json. */
export const TOGGLE_INLINE_INFO_COMMAND = 'cel-lens.toggleInlineInfo';

/** The lines are on until a person turns them off. */
const DEFAULT_INLINE_INFO = true;

/** The values a setting has at each scope, as `WorkspaceConfiguration.inspect` gives them. */
export interface ScopedValues {
  readonly globalValue?: unknown;
  readonly workspaceValue?: unknown;
  readonly workspaceFolderValue?: unknown;
}

/** A scope the toggle writes: the user settings, the workspace's or a workspace folder's. */
export type InlineInfoScope = 'user' | 'workspace' | 'folder';

/** The values from the narrowest scope out; anything that is not a boolean counts as unset. */
function byScope(values: ScopedValues | undefined): [InlineInfoScope, unknown][] {
  return [
    ['folder', values?.workspaceFolderValue],
    ['workspace', values?.workspaceValue],
    ['user', values?.globalValue],
  ];
}

/** Whether the lines show: the value of the narrowest scope that sets one, else on. */
export function inlineInfoInForce(values: ScopedValues | undefined): boolean {
  const found = byScope(values).find(([, value]) => typeof value === 'boolean');
  return found === undefined ? DEFAULT_INLINE_INFO : found[1] === true;
}

/** The scope a toggle writes: the narrowest that sets a value, so the flip takes effect, else the user's. */
export function toggleScope(values: ScopedValues | undefined): InlineInfoScope {
  return byScope(values).find(([, value]) => typeof value === 'boolean')?.[0] ?? 'user';
}

/** The status bar menu entry, such as `Inline info: on`. */
export function inlineInfoLabel(on: boolean): string {
  return `Inline info: ${on ? 'on' : 'off'}`;
}
