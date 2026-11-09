import * as vscode from 'vscode';

import { OPEN_ENGINE_SETTING_LABEL, SHOW_LOGS_LABEL } from './button_labels';
import type { ExampleEditors, ExampleNotes } from './example_editor';
import { EXPERIMENTAL_SETTING } from './experimental';
import { readInlineInfo } from './inline_info_setting';
import { openServerPathSetting } from './open_server_setting';
import {
  BACK,
  type FlowLog,
  type PickRequest,
  type Picked,
  type TypeFlow,
  type TypeUi,
} from './type_flow';
import { OUTDATED_ENGINE_MESSAGE, type PickLine, type TextRange } from './value_types';

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
 * Lines still to come show the first ones at once, busy until the others are there. The filter
 * starts with what the request gives, and a step after another has a Back button to it.
 */
function pick<T>(request: PickRequest<T>): Promise<Picked<T> | typeof BACK | undefined> {
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
    quickPick.value = request.filter ?? '';
    if (request.back === true) {
      quickPick.buttons = [vscode.QuickInputButtons.Back];
      quickPick.onDidTriggerButton((button) => {
        if (button === vscode.QuickInputButtons.Back) {
          resolve(BACK);
          quickPick.hide();
        }
      });
    }
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
 * Opens `.cellens` in the editor group that already shows it, else beside the active editor,
 * which is the rule's, so the rule stays in view; the focus goes to the selection, and the editor
 * is no preview, so it stays open while the person types.
 */
async function openCellens(cellens: string, selection: TextRange): Promise<void> {
  const uri = vscode.Uri.parse(cellens);
  const shown = vscode.window.visibleTextEditors.find(
    (editor) => editor.document.uri.toString() === uri.toString(),
  );
  await vscode.window.showTextDocument(uri, {
    viewColumn: shown?.viewColumn ?? vscode.ViewColumn.Beside,
    selection: new vscode.Range(
      selection.start.line,
      selection.start.character,
      selection.end.line,
      selection.end.character,
    ),
    preserveFocus: false,
    preview: false,
  });
}

/** The document of `uri` if it is open. */
function openDocument(uri: string): vscode.TextDocument | undefined {
  const parsed = vscode.Uri.parse(uri).toString();
  return vscode.workspace.textDocuments.find((document) => document.uri.toString() === parsed);
}

/** Shows the flow with VS Code's quick pick, editors, diagnostics and notifications. */
export function typeUi(
  log: Pick<vscode.LogOutputChannel, 'show'>,
  examples: Pick<ExampleEditors, 'open'>,
  notes: Pick<ExampleNotes, 'show'>,
): TypeUi {
  return {
    pick,
    openCellens,
    workspaceFolder(uri) {
      return vscode.workspace.getWorkspaceFolder(vscode.Uri.parse(uri))?.uri.toString();
    },
    async readText(uri) {
      return (await vscode.workspace.openTextDocument(vscode.Uri.parse(uri))).getText();
    },
    async pasteExample(value, inside, use) {
      await examples.open(value, inside, use);
    },
    isDirty(uri) {
      return openDocument(uri)?.isDirty === true;
    },
    async save(uri) {
      await openDocument(uri)?.save();
    },
    showNotes(cellens, placed) {
      notes.show(cellens, placed);
    },
    workspacePath(uri) {
      // VS Code writes a workspace folder itself as its full path: its name says it shorter.
      const folder = vscode.Uri.parse(uri);
      const root = vscode.workspace.getWorkspaceFolder(folder);
      return root?.uri.toString() === folder.toString()
        ? root.name
        : vscode.workspace.asRelativePath(folder);
    },
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
    openServerPathSetting,
    async showOutdatedEngine(message = OUTDATED_ENGINE_MESSAGE) {
      if (
        (await vscode.window.showErrorMessage(message, OPEN_ENGINE_SETTING_LABEL)) ===
        OPEN_ENGINE_SETTING_LABEL
      ) {
        await openServerPathSetting();
      }
    },
    showLogs() {
      log.show();
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
