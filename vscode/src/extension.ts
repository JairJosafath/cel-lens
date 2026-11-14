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
import { FOLLOW_EDITOR_SETTING } from './rule_follow';
import { RULE_LIST_CHANGED_NOTIFICATION } from './rule_list';
import { listValues, showRule } from './rule_commands';
import { LIST_VALUES_COMMAND, SHOW_RULE_COMMAND } from './rule_parts';
import {
  FOLLOW_EDITOR_COMMAND,
  RuleView,
  SHOW_ALL_RULES_COMMAND,
  SHOW_PROBLEMS_ONLY_COMMAND,
  SHOW_RULE_EXAMPLES_COMMAND,
  SHOW_RULE_PROBLEMS_COMMAND,
  STOP_FOLLOWING_COMMAND,
} from './rule_view';
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
    readonly workspaceState: Pick<vscode.Memento, 'get' | 'update'>;
  },
): void {
  const log = vscode.window.createOutputChannel('cel-lens', { log: true });
  const status = new VersionStatus(log);
  // The server and the view need each other: the server says when the engine starts and stops,
  // the view asks it for the list. Both are made before the engine starts, so neither is early.
  const server = new ServerController(
    log,
    context.extensionPath,
    (state) => {
      status.setState(state);
      view.engineState(state);
    },
    (initializeResult) => {
      view.engineStarted(initializeResult);
    },
  );
  controller = server;
  const view = new RuleView(
    (command, argument) => server.runEngineCommand(command, argument),
    log,
    {
      state: context.workspaceState,
    },
  );
  // The engine tells a client that asked for the list (`ruleList: true`) when it changed.
  server.onNotification(RULE_LIST_CHANGED_NOTIFICATION, (params) => {
    view.onRuleListChanged(params);
  });
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
    view,
    examples,
    notes,
    new ColorPainter(),
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (event.affectsConfiguration(`cel-lens.${FOLLOW_EDITOR_SETTING}`)) {
        view.followEditorChanged();
      }
      if (
        event.affectsConfiguration(INLINE_INFO_SETTING_NAME) ||
        event.affectsConfiguration(EXAMPLE_RESULTS_SETTING_NAME)
      ) {
        // The rows show or hide their example words by folder, and the engine's states follow its switch.
        void server.sendLineSettings().then(() => {
          view.redraw();
          return view.refresh();
        });
      } else if (
        event.affectsConfiguration('cel-lens.kubernetesVersion') ||
        event.affectsConfiguration('cel-lens.experimental')
      ) {
        void server.sendSettings().then(() => {
          view.redraw();
          return view.refresh();
        });
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
    // The context menu of a rule in the view: the line's own commands, run for the rule it was opened on.
    vscode.commands.registerCommand(SHOW_RULE_PROBLEMS_COMMAND, async (node: unknown) => {
      await view.showProblems(node);
    }),
    vscode.commands.registerCommand(SHOW_RULE_EXAMPLES_COMMAND, async (node: unknown) => {
      await view.showExampleResults(node);
    }),
    // The view's title menu: the filter, and following the editor.
    vscode.commands.registerCommand(SHOW_PROBLEMS_ONLY_COMMAND, () => {
      view.setProblemsOnly(true);
    }),
    vscode.commands.registerCommand(SHOW_ALL_RULES_COMMAND, () => {
      view.setProblemsOnly(false);
    }),
    vscode.commands.registerCommand(FOLLOW_EDITOR_COMMAND, async () => {
      await view.setFollowEditor(true);
    }),
    vscode.commands.registerCommand(STOP_FOLLOWING_COMMAND, async () => {
      await view.setFollowEditor(false);
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
