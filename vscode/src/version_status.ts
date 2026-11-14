import * as vscode from 'vscode';
import { OPEN_ENGINE_SETTING_LABEL, RESTART_ENGINE_LABEL, SHOW_LOGS_LABEL } from './button_labels';

import { SHOW_ACTIONS_COMMAND, actionItems, versionDescription } from './actions_menu';
import { RELEASES_PAGE_URL } from './core_releases';
import { readStyle } from './color_painter';
import { readExampleResults } from './example_results_setting';
import { CELLENS_TYPES_FEATURE } from './experimental';
import { featureOn } from './experimental_feature';
import { readInlineInfo } from './inline_info_setting';
import { readValueTypes } from './value_visibility_setting';
import {
  ENGINE_DOWN_ACCESSIBLE_LABEL,
  ENGINE_DOWN_TEXT,
  KUBERNETES_VERSION_SETTING,
  engineDownTooltip,
  RESTART_SERVER_COMMAND,
  effectiveVersion,
  pickItems,
  statusAccessibleLabel,
  statusText,
  statusTooltip,
  unsupportedVersionMessage,
  type ServerState,
  type VersionPickItem,
} from './kubernetes_version';
import { openServerPathSetting } from './open_server_setting';
import { reportFailedToggle } from './toggle_setting';

/** A line of the quick pick: an entry with the command it runs, or a heading. */
type MenuPick = vscode.QuickPickItem & { readonly command?: string };

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

/**
 * Whether the window has workspace settings to write: a folder, or a workspace file (which may
 * hold no folder yet). An empty window has none, and VS Code refuses a write there.
 */
function hasWorkspace(): boolean {
  return (
    vscode.workspace.workspaceFile !== undefined ||
    (vscode.workspace.workspaceFolders?.length ?? 0) > 0
  );
}

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
      this.item.accessibilityInformation = { label: ENGINE_DOWN_ACCESSIBLE_LABEL, role: 'button' };
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
    this.item.accessibilityInformation = { label: statusAccessibleLabel(version), role: 'button' };
    this.item.show();
  }

  /**
   * Opens the menu of the settings, the toggles and the engine's actions, each with its value for
   * the active editor's file, and runs the command of the entry picked. A heading runs nothing.
   */
  async showActions(): Promise<void> {
    const resource = vscode.window.activeTextEditor?.document.uri;
    const style = readStyle(resource);
    const lines = actionItems({
      style,
      version: versionDescription(this.state, readSetting()),
      engineDown: this.state.kind === 'failed' || this.state.kind === 'crashed',
      inlineInfo: readInlineInfo(resource),
      exampleResults: readExampleResults(resource),
      valueTypes: readValueTypes(resource),
      cellensTypes: featureOn(CELLENS_TYPES_FEATURE),
    });
    const items = lines.map((line): MenuPick => {
      if ('separator' in line) {
        return { label: line.separator, kind: vscode.QuickPickItemKind.Separator };
      }
      return {
        label: line.icon === undefined ? line.label : `${line.icon} ${line.label}`,
        description: line.description,
        command: line.command,
      };
    });
    const choice = await vscode.window.showQuickPick<MenuPick>(items, {
      title: 'cel-lens',
      placeHolder: 'Pick a setting, a toggle or an action',
    });
    if (choice?.command !== undefined) {
      await vscode.commands.executeCommand(choice.command);
    }
  }

  /**
   * Asks which version to use, and writes the choice to the workspace setting, or to the user
   * setting in a window with no folder or workspace file open.
   */
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
    const name = `cel-lens.${KUBERNETES_VERSION_SETTING}`;
    try {
      await vscode.workspace
        .getConfiguration('cel-lens')
        .update(
          KUBERNETES_VERSION_SETTING,
          choice.version === '' ? undefined : choice.version,
          hasWorkspace() ? vscode.ConfigurationTarget.Workspace : vscode.ConfigurationTarget.Global,
        );
    } catch (error) {
      // As the toggles report a failed write: logged, and shown once with a button to the setting.
      await reportFailedToggle(
        name,
        error,
        this.log ?? { error: () => undefined },
        'pick the version again',
      );
    }
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
