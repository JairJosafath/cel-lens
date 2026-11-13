import * as vscode from 'vscode';

import { SHOW_ACTIONS_COMMAND, SHOW_LOGS_COMMAND } from './actions_menu';
import { ColorPainter, chooseColorStyle } from './color_painter';
import { CHOOSE_COLOR_STYLE_COMMAND } from './color_style';
import { ExampleEditors, ExampleNotes } from './example_editor';
import {
  EXAMPLE_RESULTS_SETTING_NAME,
  SHOW_EXAMPLE_RESULTS_COMMAND,
  TOGGLE_EXAMPLE_RESULTS_COMMAND,
} from './example_results';
import { resultsUi } from './example_results_commands';
import { ExampleResultsFlow } from './example_results_flow';
import { toggleExampleResults } from './example_results_setting';
import { INLINE_INFO_SETTING_NAME, TOGGLE_INLINE_INFO_COMMAND } from './inline_info';
import { toggleInlineInfo } from './inline_info_setting';
import { offerLegacyColorStyleMove } from './legacy_color_style';
import { PICK_VERSION_COMMAND, RESTART_SERVER_COMMAND } from './kubernetes_version';
import { listValues, showRule } from './rule_commands';
import { LIST_VALUES_COMMAND, SHOW_RULE_COMMAND } from './rule_parts';
import { ServerController } from './server_controller';
import { giveValueType, typeUi } from './type_commands';
import { TypeFlow } from './type_flow';
import {
  EDIT_TYPE_COMMAND,
  GIVE_TYPE_COMMAND,
  GIVE_VALUE_TYPE_COMMAND,
  REMOVE_TYPE_COMMAND,
} from './value_types';
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
  const examples = new ExampleEditors(log);
  const notes = new ExampleNotes();
  const ui = typeUi(log, examples, notes);
  const types = new TypeFlow(
    (command, argument, signal) => server.runEngineCommand(command, argument, signal),
    ui,
    log,
  );
  const results = new ExampleResultsFlow(
    (command, argument, signal) => server.runEngineCommand(command, argument, signal),
    resultsUi(log),
    log,
  );
  context.subscriptions.push(
    log,
    server,
    status,
    examples,
    notes,
    new ColorPainter(),
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (
        event.affectsConfiguration(INLINE_INFO_SETTING_NAME) ||
        event.affectsConfiguration(EXAMPLE_RESULTS_SETTING_NAME)
      ) {
        void server.sendLineSettings();
      } else if (
        event.affectsConfiguration('cel-lens.kubernetesVersion') ||
        event.affectsConfiguration('cel-lens.experimental')
      ) {
        void server.sendSettings();
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
    vscode.commands.registerCommand(TOGGLE_INLINE_INFO_COMMAND, async () => {
      await toggleInlineInfo(log);
    }),
    vscode.commands.registerCommand(TOGGLE_EXAMPLE_RESULTS_COMMAND, async () => {
      await toggleExampleResults(log);
    }),
    // The parts of the line above each rule; the engine names them, so they have no palette entry.
    vscode.commands.registerCommand(SHOW_RULE_COMMAND, async (location: unknown) => {
      await showRule(location, log);
    }),
    vscode.commands.registerCommand(SHOW_EXAMPLE_RESULTS_COMMAND, async (location: unknown) => {
      await results.show(location);
    }),
    vscode.commands.registerCommand(LIST_VALUES_COMMAND, async (more: unknown) => {
      await listValues(more, log);
    }),
    vscode.commands.registerCommand(GIVE_TYPE_COMMAND, async (value: unknown) => {
      await types.giveType(value);
    }),
    vscode.commands.registerCommand(EDIT_TYPE_COMMAND, async (value: unknown) => {
      await types.editType(value);
    }),
    vscode.commands.registerCommand(REMOVE_TYPE_COMMAND, async (value: unknown) => {
      await types.removeType(value);
    }),
    // The palette's way to the same steps, for the values of the open file.
    vscode.commands.registerCommand(GIVE_VALUE_TYPE_COMMAND, async () => {
      await giveValueType(types, ui, log);
    }),
  );
  void server.start();
  void offerLegacyColorStyleMove(context.globalState, log);
}

/** Lets VS Code wait for the language server to stop. */
export function deactivate(): Promise<void> | undefined {
  return controller?.stop();
}
