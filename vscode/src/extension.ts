import * as vscode from 'vscode';

import { SHOW_ACTIONS_COMMAND, SHOW_LOGS_COMMAND } from './actions_menu';
import { ColorPainter, chooseColorStyle } from './color_painter';
import { CHOOSE_COLOR_STYLE_COMMAND } from './color_style';
import { offerLegacyColorStyleMove } from './legacy_color_style';
import { PICK_VERSION_COMMAND, RESTART_SERVER_COMMAND } from './kubernetes_version';
import { ServerController } from './server_controller';
import { VersionStatus } from './version_status';

let controller: ServerController | undefined;

/**
 * Activates the extension. VS Code calls it once, on the first activation
 * event. Everything it registers goes into `context.subscriptions`, so VS Code
 * disposes it when the extension deactivates. The server starts in the
 * background, so activation returns at once.
 */
export function activate(
  context: Pick<vscode.ExtensionContext, 'subscriptions' | 'extensionPath'> & {
    readonly globalState: Pick<vscode.Memento, 'get' | 'update'>;
  },
): void {
  const log = vscode.window.createOutputChannel('cel-lens', { log: true });
  const status = new VersionStatus(log);
  const server = new ServerController(log, context.extensionPath, (state) => {
    status.setState(state);
  });
  controller = server;
  context.subscriptions.push(
    log,
    server,
    status,
    new ColorPainter(),
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (event.affectsConfiguration('cel-lens.kubernetesVersion')) {
        void server.sendVersionSetting();
      }
    }),
    vscode.commands.registerCommand(PICK_VERSION_COMMAND, async () => {
      await status.pick();
    }),
    vscode.commands.registerCommand(SHOW_ACTIONS_COMMAND, async () => {
      await status.showActions();
    }),
    vscode.commands.registerCommand(SHOW_LOGS_COMMAND, () => {
      log.show();
    }),
    vscode.commands.registerCommand(CHOOSE_COLOR_STYLE_COMMAND, async () => {
      await chooseColorStyle();
    }),
    vscode.commands.registerCommand(RESTART_SERVER_COMMAND, async () => {
      await server.restart();
    }),
  );
  void server.start();
  void offerLegacyColorStyleMove(context.globalState, log);
}

/** Lets VS Code wait for the language server to stop. */
export function deactivate(): Promise<void> | undefined {
  return controller?.stop();
}
