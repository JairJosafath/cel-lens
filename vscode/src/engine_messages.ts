import * as vscode from 'vscode';

import { SHOW_LOGS_COMMAND } from './actions_menu';
import { SHOW_LOGS_LABEL } from './button_labels';

/** The LSP method of a message the engine asks the client to show. */
export const SHOW_MESSAGE_METHOD = 'window/showMessage';

/** The start of the warning the engine sends when it stops scanning the workspace at its file cap. */
const SCAN_CAP_PREFIX = 'cel-lens stopped scanning the workspace';

const MESSAGE_TYPE_ERROR = 1;
const MESSAGE_TYPE_WARNING = 2;

/** The parameters of a `window/showMessage` notification, as far as the extension reads them. */
export interface ShowMessageParams {
  type: number;
  message: string;
}

/**
 * Shows the messages the engine sends like the language client does, except that the file-cap
 * warning gets a button to the log, where the skipped files are listed, and appears once per
 * engine start.
 */
export class EngineMessages {
  private readonly shown = new Set<string>();

  constructor(private readonly log: vscode.LogOutputChannel) {}

  show(params: ShowMessageParams): void {
    if (params.type === MESSAGE_TYPE_WARNING && params.message.startsWith(SCAN_CAP_PREFIX)) {
      this.showScanCap(params.message);
    } else if (params.type === MESSAGE_TYPE_ERROR) {
      void vscode.window.showErrorMessage(params.message);
    } else if (params.type === MESSAGE_TYPE_WARNING) {
      void vscode.window.showWarningMessage(params.message);
    } else {
      void vscode.window.showInformationMessage(params.message);
    }
  }

  private showScanCap(message: string): void {
    if (this.shown.has(message)) {
      return;
    }
    this.shown.add(message);
    // Not awaited: a notification stays pending while it is open.
    void vscode.window.showWarningMessage(message, SHOW_LOGS_LABEL).then(async (button) => {
      if (button !== SHOW_LOGS_LABEL) {
        return;
      }
      try {
        await vscode.commands.executeCommand(SHOW_LOGS_COMMAND);
      } catch (error) {
        this.log.error(`Could not follow the message button: ${String(error)}`);
      }
    });
  }
}
