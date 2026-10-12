import * as vscode from 'vscode';
import { RESTART_ENGINE_LABEL, SHOW_LOGS_LABEL } from './button_labels';
import {
  CloseAction,
  ErrorAction,
  type CloseHandlerResult,
  type ErrorHandler,
  type ErrorHandlerResult,
} from 'vscode-languageclient/node';

import { SHOW_LOGS_COMMAND } from './actions_menu';
import { RESTART_SERVER_COMMAND } from './kubernetes_version';

/** A crash is the engine's last allowed one when this many fall inside the window. */
export const MAX_CRASHES = 5;
/** The window in which `MAX_CRASHES` crashes make the extension give up, in milliseconds. */
export const CRASH_WINDOW_MS = 3 * 60 * 1000;
/** Message errors the engine survives before the connection is shut down. */
const MAX_MESSAGE_ERRORS = 3;

/** The message shown once the extension stops restarting a crashing engine. */
export function giveUpMessage(): string {
  return `The cel-lens engine crashed ${String(MAX_CRASHES)} times in the last ${String(CRASH_WINDOW_MS / 60_000)} minutes and will not be restarted.`;
}

/** The message shown once the connection to the engine keeps erroring and is shut down. */
export function connectionErrorMessage(): string {
  return 'The connection to the cel-lens engine keeps erroring, so the engine was shut down.';
}

/**
 * Restarts a crashed engine like the client's default handler, but gives up with a message
 * that says "engine" and offers the same actions as the actions menu.
 */
export class EngineErrorHandler implements ErrorHandler {
  private readonly crashes: number[] = [];
  private connectionErrorShown = false;

  constructor(
    private readonly log: vscode.LogOutputChannel,
    private readonly now: () => number = Date.now,
  ) {}

  error(_error: Error, _message: unknown, count: number | undefined): ErrorHandlerResult {
    if (count !== undefined && count <= MAX_MESSAGE_ERRORS) {
      return { action: ErrorAction.Continue };
    }
    if (!this.connectionErrorShown) {
      this.connectionErrorShown = true;
      this.notify(connectionErrorMessage());
    }
    return { action: ErrorAction.Shutdown, handled: true };
  }

  closed(): CloseHandlerResult {
    this.crashes.push(this.now());
    if (this.crashes.length < MAX_CRASHES) {
      return { action: CloseAction.Restart };
    }
    const first = this.crashes[0] ?? 0;
    if (this.now() - first > CRASH_WINDOW_MS) {
      this.crashes.shift();
      return { action: CloseAction.Restart };
    }
    this.notify(giveUpMessage());
    return { action: CloseAction.DoNotRestart, handled: true };
  }

  private notify(message: string): void {
    this.log.error(message);
    // Not awaited: a notification stays pending while it is open.
    void vscode.window
      .showErrorMessage(message, RESTART_ENGINE_LABEL, SHOW_LOGS_LABEL)
      .then((button) => this.follow(button));
  }

  private async follow(button: string | undefined): Promise<void> {
    try {
      if (button === RESTART_ENGINE_LABEL) {
        await vscode.commands.executeCommand(RESTART_SERVER_COMMAND);
      } else if (button === SHOW_LOGS_LABEL) {
        await vscode.commands.executeCommand(SHOW_LOGS_COMMAND);
      }
    } catch (error) {
      this.log.error(`Could not follow the message button: ${String(error)}`);
    }
  }
}
