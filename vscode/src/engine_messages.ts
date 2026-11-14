import * as vscode from 'vscode';

import { SHOW_LOGS_COMMAND } from './actions_menu';
import { SHOW_LOGS_LABEL } from './button_labels';
import { KNOWN_EXPERIMENTAL } from './experimental';
import { turnOnExperimental } from './experimental_feature';
import { isRecord } from './is_record';

/** The LSP method of a message the engine asks the client to show. */
export const SHOW_MESSAGE_METHOD = 'window/showMessage';

/** The LSP method of a message the engine asks the client to show, with actions to pick from. */
export const SHOW_MESSAGE_REQUEST_METHOD = 'window/showMessageRequest';

/** The start of the warning the engine sends when it stops scanning the workspace at its file cap. */
const SCAN_CAP_PREFIX = 'cel-lens stopped scanning the workspace';

const MESSAGE_TYPE_ERROR = 1;
const MESSAGE_TYPE_WARNING = 2;

/** The parameters of a `window/showMessage` notification, as far as the extension reads them. */
export interface ShowMessageParams {
  type: number;
  message: string;
}

/** The action of a `window/showMessageRequest` as the extension reads it: its title, and the experimental feature it turns on, if it names one. */
interface MessageAction {
  readonly title: string;
  readonly experimental?: string;
}

function readActions(value: unknown): MessageAction[] {
  return Array.isArray(value)
    ? (value as unknown[]).flatMap((item) =>
        isRecord(item) && typeof item['title'] === 'string' && item['title'] !== ''
          ? [
              {
                title: item['title'],
                ...(typeof item['experimental'] === 'string' &&
                  item['experimental'] !== '' && { experimental: item['experimental'] }),
              },
            ]
          : [],
      )
    : [];
}

/** The parameters of a `window/showMessageRequest`, as far as the extension reads them. */
export interface ShowMessageRequestParams extends ShowMessageParams {
  actions?: unknown;
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

  /**
   * Shows a `window/showMessageRequest` like the language client does, and answers the action
   * picked, or `null`. An action that names an experimental feature (`{"title": "Turn on
   * cellens-types", "experimental": "cellens-types"}`) adds it to `cel-lens.experimental`, at the
   * scope that sets the setting: the engine cannot change an editor setting, and ignores the answer.
   */
  async request(params: ShowMessageRequestParams): Promise<{ title: string } | null> {
    const actions = readActions(params.actions);
    const titles = actions.map((action) => action.title);
    const picked =
      params.type === MESSAGE_TYPE_ERROR
        ? await vscode.window.showErrorMessage(params.message, ...titles)
        : params.type === MESSAGE_TYPE_WARNING
          ? await vscode.window.showWarningMessage(params.message, ...titles)
          : await vscode.window.showInformationMessage(params.message, ...titles);
    const action = actions.find((each) => each.title === picked);
    if (action === undefined) {
      return null;
    }
    if (action.experimental !== undefined) {
      if (KNOWN_EXPERIMENTAL.includes(action.experimental)) {
        await turnOnExperimental(action.experimental, this.log);
      } else {
        this.log.warn(
          `The engine asked to turn on "${action.experimental}", which cel-lens.experimental does not offer, so nothing was changed.`,
        );
      }
    }
    return { title: action.title };
  }

  private showScanCap(message: string): void {
    if (this.shown.has(message)) {
      return;
    }
    this.shown.add(message);
    this.log.warn(message);
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
