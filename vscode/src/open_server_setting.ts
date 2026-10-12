import * as vscode from 'vscode';

export const SERVER_PATH_SETTING = 'cel-lens.serverPath';

/**
 * Opens `cel-lens.serverPath` on the Settings tab that can set it: the Remote
 * tab in a remote window, because the machine-scoped setting is not shown on
 * the User tab there.
 */
export async function openServerPathSetting(): Promise<void> {
  if (vscode.env.remoteName === undefined) {
    await vscode.commands.executeCommand('workbench.action.openSettings', SERVER_PATH_SETTING);
    return;
  }
  // The Remote command ignores a bare string argument; only an object carries the query.
  await vscode.commands.executeCommand('workbench.action.openRemoteSettings', {
    query: SERVER_PATH_SETTING,
  });
}
