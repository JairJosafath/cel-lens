import * as vscode from 'vscode';

import { openBesideRule } from './cellens_editor';
import { SHOW_LOGS_LABEL } from './button_labels';
import { EXAMPLE_RESULTS_OFF_MESSAGE, TURN_ON_LABEL } from './example_results';
import type { ResultsUi } from './example_results_flow';
import { readExampleResults, toggleExampleResults } from './example_results_setting';
import { pick } from './type_commands';

/** Shows the example results flow with VS Code's quick pick, editors and notifications. */
export function resultsUi(log: Pick<vscode.LogOutputChannel, 'error' | 'show'>): ResultsUi {
  return {
    pick,
    isOn(uri) {
      return readExampleResults(vscode.Uri.parse(uri));
    },
    async offerTurnOn() {
      const button = await vscode.window.showInformationMessage(
        EXAMPLE_RESULTS_OFF_MESSAGE,
        TURN_ON_LABEL,
      );
      if (button === TURN_ON_LABEL) {
        await toggleExampleResults(log);
      }
    },
    async openExample(uri, line) {
      const start = { line: line - 1, character: 0 };
      await openBesideRule(uri, { start, end: start });
    },
    async showError(message, offerLogs) {
      const button = offerLogs
        ? await vscode.window.showErrorMessage(message, SHOW_LOGS_LABEL)
        : await vscode.window.showErrorMessage(message);
      if (button === SHOW_LOGS_LABEL) {
        log.show();
      }
    },
    showLogs() {
      log.show();
    },
  };
}
