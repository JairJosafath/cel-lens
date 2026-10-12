import { RESTART_ENGINE_LABEL, SHOW_LOGS_LABEL } from './button_labels';
import type { CelStyle } from './cel_styles';
import { CHOOSE_COLOR_STYLE_COMMAND, styleLabel } from './color_style';
import {
  PICK_VERSION_COMMAND,
  RESTART_SERVER_COMMAND,
  effectiveVersion,
  type ServerState,
} from './kubernetes_version';

/** The `cel-lens.showActions` command, as contributed in package.json. */
export const SHOW_ACTIONS_COMMAND = 'cel-lens.showActions';

/** The `cel-lens.showLogs` command, as contributed in package.json. */
export const SHOW_LOGS_COMMAND = 'cel-lens.showLogs';

/** One line of the actions menu: what it says, and the command it runs. */
export interface ActionItem {
  readonly label: string;
  readonly description: string;
  readonly command: string;
}

/** The menu's entries in order; style and version are shown as descriptions. */
export function actionItems(style: CelStyle, version: string, engineDown = false): ActionItem[] {
  const restart = { label: RESTART_ENGINE_LABEL, description: '', command: RESTART_SERVER_COMMAND };
  const logs = { label: SHOW_LOGS_LABEL, description: '', command: SHOW_LOGS_COMMAND };
  if (engineDown) {
    return [
      restart,
      logs,
      { label: 'Color style', description: styleLabel(style), command: CHOOSE_COLOR_STYLE_COMMAND },
      { label: 'Kubernetes version', description: version, command: PICK_VERSION_COMMAND },
    ];
  }
  return [
    { label: 'Color style', description: styleLabel(style), command: CHOOSE_COLOR_STYLE_COMMAND },
    { label: 'Kubernetes version', description: version, command: PICK_VERSION_COMMAND },
    restart,
    logs,
  ];
}

/** What the version entry says: the version in use, or the engine's state when none is known. */
export function versionDescription(state: ServerState, setting: string): string {
  switch (state.kind) {
    case 'starting':
      return 'Engine starting';
    case 'stopped':
      return 'Engine stopped';
    case 'failed':
      return 'Engine failed to start';
    case 'crashed':
      return 'Engine stopped after crashing';
    case 'running':
      return state.info === undefined
        ? 'Engine reports no versions'
        : effectiveVersion(setting, state.info);
  }
}
