import * as vscode from 'vscode';

import { SHOW_LOGS_LABEL } from './button_labels';
import { EXPERIMENTAL_SETTING } from './experimental';
import { readInlineInfo } from './inline_info_setting';
import type {
  FlowLog,
  PickRequest,
  Picked,
  TypeFlow,
  TypeUi,
  WriteEnd,
  WriteRequest,
} from './type_flow';
import { TypeInput } from './type_input';
import { PICK_KIND_TOOLTIP, type PickLine } from './value_types';

/** What `cel-lens: Give a Value a Type…` says without a YAML file open. */
export const OPEN_YAML_MESSAGE = 'Open a Kubernetes YAML file to give one of its values a type.';

/** A quick pick line with the value it stands for; a separator has none. */
interface ShownItem<T> extends vscode.QuickPickItem {
  readonly value?: T;
}

function shownItem<T>(line: PickLine<T>): ShownItem<T> {
  if (line.separator === true) {
    return { label: line.label, kind: vscode.QuickPickItemKind.Separator };
  }
  return {
    label: line.label,
    ...(line.description !== undefined && { description: line.description }),
    ...(line.detail !== undefined && { detail: line.detail }),
    ...(line.alwaysShow === true && { alwaysShow: true }),
    value: line.value,
  };
}

/**
 * Shows the lines, matching what is typed on their descriptions and details too, as GitLens does.
 * Lines still to come show the first ones at once, busy until the others are there.
 */
function pick<T>(request: PickRequest<T>): Promise<Picked<T> | undefined> {
  return new Promise((resolve) => {
    const quickPick = vscode.window.createQuickPick<ShownItem<T>>();
    const isActive = request.active;
    let hidden = false;
    const showLines = (lines: readonly PickLine<T>[]): void => {
      const items = lines.map(shownItem);
      quickPick.items = items;
      const active = items.find((item) => item.value !== undefined && isActive?.(item.value));
      if (active !== undefined) {
        quickPick.activeItems = [active];
      }
    };
    quickPick.title = request.title;
    quickPick.placeholder = request.placeholder;
    quickPick.matchOnDescription = true;
    quickPick.matchOnDetail = true;
    if (request.lines instanceof Promise) {
      showLines(request.first ?? []);
      quickPick.busy = true;
      void request.lines.then(
        (lines) => {
          if (!hidden) {
            showLines(lines);
            quickPick.busy = false;
          }
        },
        () => {
          quickPick.busy = false;
        },
      ); // Never rejects: a failure leaves the first lines, not busy.
    } else {
      showLines(request.lines);
    }
    quickPick.onDidAccept(() => {
      const value = quickPick.selectedItems[0]?.value;
      resolve(value === undefined ? undefined : { value, typed: quickPick.value });
      quickPick.hide();
    });
    quickPick.onDidHide(() => {
      hidden = true;
      resolve(undefined);
      quickPick.dispose();
    });
    quickPick.show();
  });
}

/**
 * Shows the input box until Enter sets the type, Escape closes it or its Pick a kind… button
 * turns to the picker; it checks the type as it is typed.
 */
function write(request: WriteRequest): Promise<WriteEnd> {
  return new Promise((resolve) => {
    const box = vscode.window.createInputBox();
    const pickKind: vscode.QuickInputButton = {
      iconPath: new vscode.ThemeIcon('symbol-class'),
      tooltip: PICK_KIND_TOOLTIP,
    };
    box.title = request.title;
    box.placeholder = request.placeholder;
    box.prompt = request.prompt;
    box.value = request.value;
    box.buttons = [pickKind];
    let closed = false;
    let end: WriteEnd = 'closed';
    const input = new TypeInput(
      {
        get value() {
          return box.value;
        },
        setProblem: (message) => {
          if (!closed) {
            box.validationMessage = message;
          }
        },
        setBusy: (busy) => {
          if (!closed) {
            box.busy = busy;
          }
        },
        setEnabled: (enabled) => {
          if (!closed) {
            box.enabled = enabled;
          }
        },
        close: () => {
          box.hide();
        },
      },
      request,
    );
    const listeners = [
      box.onDidChangeValue((text) => {
        input.changed(text);
      }),
      box.onDidAccept(() => {
        void input.accepted(); // Never rejects: it shows what goes wrong in the box.
      }),
      box.onDidTriggerButton((button) => {
        if (button === pickKind) {
          end = 'pickKind';
          box.hide();
        }
      }),
      box.onDidHide(() => {
        closed = true;
        input.dispose();
        for (const listener of listeners) {
          listener.dispose();
        }
        box.dispose();
        resolve(end);
      }),
    ];
    box.show();
    input.changed(box.value);
  });
}

/** Shows the flow with VS Code's quick pick, input box and notifications. */
export function typeUi(log: Pick<vscode.LogOutputChannel, 'show'>): TypeUi {
  return {
    pick,
    write,
    async inform(message) {
      await vscode.window.showInformationMessage(message);
    },
    async showError(message, offerLogs) {
      const button = offerLogs
        ? await vscode.window.showErrorMessage(message, SHOW_LOGS_LABEL)
        : await vscode.window.showErrorMessage(message);
      if (button === SHOW_LOGS_LABEL) {
        log.show();
      }
    },
    async openExperimentalSetting() {
      await vscode.commands.executeCommand(
        'workbench.action.openSettings',
        `cel-lens.${EXPERIMENTAL_SETTING}`,
      );
    },
  };
}

/**
 * `cel-lens: Give a Value a Type…`: the values come from the line above each rule of the active
 * YAML file, its code lenses, so the list names what the lines name.
 */
export async function giveValueType(flow: TypeFlow, ui: TypeUi, log: FlowLog): Promise<void> {
  const document = vscode.window.activeTextEditor?.document;
  if (document?.languageId !== 'yaml') {
    await ui.inform(OPEN_YAML_MESSAGE);
    return;
  }
  let lenses: unknown;
  try {
    lenses = await vscode.commands.executeCommand('vscode.executeCodeLensProvider', document.uri);
  } catch (error) {
    log.error(
      `Could not read the lines above the rules of ${document.uri.toString()}: ${String(error)}`,
    );
    await ui.showError(
      'cel-lens could not list the values of this file. Show the logs for the reason.',
      true,
    );
    return;
  }
  await flow.giveValueType(lenses, readInlineInfo(document.uri));
}
