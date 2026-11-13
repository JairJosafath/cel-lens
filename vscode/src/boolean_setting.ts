/** The values a setting has at each scope, as `WorkspaceConfiguration.inspect` gives them. */
export interface ScopedValues {
  readonly globalValue?: unknown;
  readonly workspaceValue?: unknown;
  readonly workspaceFolderValue?: unknown;
}

/** A scope a toggle writes: the user settings, the workspace's or a workspace folder's. */
export type SettingScope = 'user' | 'workspace' | 'folder';

/** The values from the narrowest scope out; anything that is not a boolean counts as unset. */
function byScope(values: ScopedValues | undefined): [SettingScope, unknown][] {
  return [
    ['folder', values?.workspaceFolderValue],
    ['workspace', values?.workspaceValue],
    ['user', values?.globalValue],
  ];
}

/** A boolean setting's value in force: the narrowest scope that sets one, else `fallback`. */
export function settingInForce(values: ScopedValues | undefined, fallback: boolean): boolean {
  const found = byScope(values).find(([, value]) => typeof value === 'boolean');
  return found === undefined ? fallback : found[1] === true;
}

/** The scope a toggle writes: the narrowest that sets a value, so the flip takes effect, else the user's. */
export function toggleScope(values: ScopedValues | undefined): SettingScope {
  return byScope(values).find(([, value]) => typeof value === 'boolean')?.[0] ?? 'user';
}
