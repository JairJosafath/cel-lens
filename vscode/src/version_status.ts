import * as vscode from 'vscode';
import { OPEN_ENGINE_SETTING_LABEL, RESTART_ENGINE_LABEL, SHOW_LOGS_LABEL } from './button_labels';

import {
  SHOW_ACTIONS_COMMAND,
  actionItems,
  versionDescription,
  type ActionItem,
} from './actions_menu';
import { RELEASES_PAGE_URL } from './core_releases';
import { currentColorStyle } from './color_painter';
import {
  ENGINE_DOWN_TEXT,
  KUBERNETES_VERSION_SETTING,
  engineDownTooltip,
  RESTART_SERVER_COMMAND,
  effectiveVersion,
  pickItems,
  statusText,
  statusTooltip,
  unsupportedVersionMessage,
  type ServerState,
  type VersionPickItem,
} from './kubernetes_version';
import { openServerPathSetting } from './open_server_setting';

const OPEN_RELEASES = 'Open Releases Page';

const STARTING_MESSAGE =
  'The cel-lens engine is still starting. Pick the Kubernetes version again in a moment.';
const STOPPED_MESSAGE =
  'Cannot pick a Kubernetes version: the cel-lens engine is not running. Restart the engine.';
const FAILED_MESSAGE =
  'Cannot pick a Kubernetes version: the cel-lens engine failed to start. Check the logs for the reason, fix it, then restart the engine.';
const CRASHED_MESSAGE =
  'Cannot pick a Kubernetes version: the cel-lens engine stopped after crashing. Check the logs for the reason, then restart the engine.';
const TOO_OLD_MESSAGE =
  'Cannot pick a Kubernetes version: the cel-lens engine reports no Kubernetes versions, so it is too old. Download a current engine from github.com/JairJosafath/cel-lens/releases (engine releases are tagged core-vX.Y.Z), then point cel-lens.serverPath to it, or clear that setting to use the bundled one, then restart the engine.';

function readSetting(): string {
  return vscode.workspace.getConfiguration('cel-lens').get<string>(KUBERNETES_VERSION_SETTING, '');
}

/** The status bar item that shows the Kubernetes version results match, on YAML files. */
export class VersionStatus implements vscode.Disposable {
  private state: ServerState = { kind: 'stopped' };
  private readonly item: vscode.StatusBarItem;
  private readonly editorListener: vscode.Disposable;
  private warned: string | undefined;
  private explaining = false;

  constructor(private readonly log?: vscode.LogOutputChannel) {
    this.item = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 0);
    this.item.name = 'cel-lens';
    this.item.command = SHOW_ACTIONS_COMMAND;
    this.editorListener = vscode.window.onDidChangeActiveTextEditor(() => {
      this.refresh();
    });
  }

  /** Sets what the server is doing; the item shows while it runs and reports versions, or is down. */
  setState(state: ServerState): void {
    this.state = state;
    this.refresh();
  }

  /** Shows the item for the current setting and editor, or hides it. */
  refresh(): void {
    if (this.state.kind === 'failed' || this.state.kind === 'crashed') {
      this.item.text = ENGINE_DOWN_TEXT;
      this.item.tooltip = engineDownTooltip(this.state.kind);
      this.item.show();
      return;
    }
    const info = this.state.kind === 'running' ? this.state.info : undefined;
    if (info === undefined || vscode.window.activeTextEditor?.document.languageId !== 'yaml') {
      this.item.hide();
      return;
    }
    const setting = readSetting();
    const problem = unsupportedVersionMessage(setting, info);
    if (problem !== undefined && this.warned !== setting) {
      this.log?.warn(problem);
    }
    this.warned = problem === undefined ? undefined : setting;
    const version = effectiveVersion(setting, info);
    this.item.text = statusText(version);
    this.item.tooltip = statusTooltip(version);
    this.item.show();
  }

  /** Opens the menu of the main actions, and runs the command of the entry picked. */
  async showActions(): Promise<void> {
    const choice = await vscode.window.showQuickPick<ActionItem>(
      actionItems(
        currentColorStyle(),
        versionDescription(this.state, readSetting()),
        this.state.kind === 'failed' || this.state.kind === 'crashed',
      ),
      { title: 'cel-lens', placeHolder: 'Pick an action' },
    );
    if (choice !== undefined) {
      await vscode.commands.executeCommand(choice.command);
    }
  }

  /** Asks which version to use, and writes the choice to the workspace setting. */
  async pick(): Promise<void> {
    const info = this.state.kind === 'running' ? this.state.info : undefined;
    if (info === undefined) {
      await this.explainNoVersions(this.state);
      return;
    }
    const choice = await vscode.window.showQuickPick<VersionPickItem>(
      pickItems(info, readSetting()),
      {
        title: 'Kubernetes version for cel-lens',
        placeHolder: 'Pick the version your cluster runs',
      },
    );
    if (choice === undefined) {
      return;
    }
    await vscode.workspace
      .getConfiguration('cel-lens')
      .update(
        KUBERNETES_VERSION_SETTING,
        choice.version === '' ? undefined : choice.version,
        vscode.ConfigurationTarget.Workspace,
      );
  }

  /** Says why no version can be picked; one message at a time, while it is open. */
  private async explainNoVersions(state: ServerState): Promise<void> {
    if (this.explaining) {
      return;
    }
    this.explaining = true;
    try {
      switch (state.kind) {
        case 'starting':
          this.log?.info(STARTING_MESSAGE);
          await vscode.window.showInformationMessage(STARTING_MESSAGE);
          return;
        case 'stopped':
          await this.offer(STOPPED_MESSAGE, [RESTART_ENGINE_LABEL]);
          return;
        case 'crashed':
          await this.offer(CRASHED_MESSAGE, [SHOW_LOGS_LABEL, RESTART_ENGINE_LABEL]);
          return;
        case 'failed':
          await this.offer(FAILED_MESSAGE, [
            SHOW_LOGS_LABEL,
            RESTART_ENGINE_LABEL,
            OPEN_ENGINE_SETTING_LABEL,
          ]);
          return;
        case 'running':
          await this.offer(TOO_OLD_MESSAGE, [
            OPEN_RELEASES,
            RESTART_ENGINE_LABEL,
            OPEN_ENGINE_SETTING_LABEL,
          ]);
          return;
      }
    } finally {
      this.explaining = false;
    }
  }

  private async offer(message: string, buttons: readonly string[]): Promise<void> {
    this.log?.warn(message);
    const button = await vscode.window.showErrorMessage(message, ...buttons);
    if (button === OPEN_RELEASES) {
      await vscode.env.openExternal(vscode.Uri.parse(RELEASES_PAGE_URL));
    } else if (button === RESTART_ENGINE_LABEL) {
      await vscode.commands.executeCommand(RESTART_SERVER_COMMAND);
    } else if (button === SHOW_LOGS_LABEL) {
      this.log?.show();
    } else if (button === OPEN_ENGINE_SETTING_LABEL) {
      await openServerPathSetting();
    }
  }

  dispose(): void {
    this.editorListener.dispose();
    this.item.dispose();
  }
}
