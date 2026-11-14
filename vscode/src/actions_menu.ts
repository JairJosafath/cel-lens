import { RESTART_ENGINE_LABEL, SHOW_LOGS_LABEL } from './button_labels';
import type { CelStyle } from './cel_styles';
import { CHOOSE_COLOR_STYLE_COMMAND, TOGGLE_COLORS_COMMAND, styleLabel } from './color_style';
import { TOGGLE_EXAMPLE_RESULTS_COMMAND } from './example_results';
import { TOGGLE_INLINE_INFO_COMMAND } from './inline_info';
import {
  PICK_VERSION_COMMAND,
  RESTART_SERVER_COMMAND,
  effectiveVersion,
  type ServerState,
} from './kubernetes_version';
import { REVEAL_RULE_COMMAND } from './rule_view_state';
import { TOGGLE_VALUE_TYPES_COMMAND } from './value_visibility';

/** The `cel-lens.showActions` command, as contributed in package.json. */
export const SHOW_ACTIONS_COMMAND = 'cel-lens.showActions';

/** The `cel-lens.showLogs` command, as contributed in package.json. */
export const SHOW_LOGS_COMMAND = 'cel-lens.showLogs';

/** One entry of the actions menu: what it says, its current value in words, and the command it runs. */
export interface ActionItem {
  readonly label: string;
  readonly description: string;
  readonly command: string;
  /** A codicon, such as `$(output)`; only the two engine entries have one. */
  readonly icon?: string;
}

/** A heading between the groups of the menu; it runs nothing. */
export interface ActionSeparator {
  readonly separator: string;
}

/** A line of the menu: an entry, or the heading of a group. */
export type ActionLine = ActionItem | ActionSeparator;

/** The menu's two headings. */
export const EDITOR_SEPARATOR = 'In the editor';
export const ENGINE_SEPARATOR = 'Engine';

/** The labels of the entries that name a setting or a toggle; a label never holds a value. */
export const VERSION_LABEL = 'Kubernetes version';
export const COLOR_STYLE_LABEL = 'Color style';
export const COLORS_LABEL = 'Colors';
export const INLINE_INFO_LABEL = 'Inline info';
export const EXAMPLE_RESULTS_LABEL = 'Example results';
export const VALUE_TYPES_LABEL = 'Value types';

/**
 * The entries of the menu in order, with the command each runs: the one list the README's
 * tables and the settings' `order` follow. Value types shows only while `cellens-types` is on.
 */
export const MENU_ENTRIES = [
  { label: VERSION_LABEL, command: PICK_VERSION_COMMAND },
  { label: COLOR_STYLE_LABEL, command: CHOOSE_COLOR_STYLE_COMMAND },
  { label: COLORS_LABEL, command: TOGGLE_COLORS_COMMAND },
  { label: INLINE_INFO_LABEL, command: TOGGLE_INLINE_INFO_COMMAND },
  { label: EXAMPLE_RESULTS_LABEL, command: TOGGLE_EXAMPLE_RESULTS_COMMAND },
  { label: VALUE_TYPES_LABEL, command: TOGGLE_VALUE_TYPES_COMMAND },
  { label: SHOW_LOGS_LABEL, command: SHOW_LOGS_COMMAND },
  { label: RESTART_ENGINE_LABEL, command: RESTART_SERVER_COMMAND },
] as const;

/** The engine's two entries, the menu's last group. */
const ENGINE_COMMANDS: readonly string[] = [SHOW_LOGS_COMMAND, RESTART_SERVER_COMMAND];

/**
 * Every command of the Command Palette in the order a person meets it, which the README's
 * Commands table follows: the walkthrough's Get Started, the menu itself, the menu's settings and
 * toggles, the rule view's commands, giving a value a type, and the menu's engine entries last.
 */
export const COMMAND_ORDER: readonly string[] = [
  'cel-lens.getStarted',
  SHOW_ACTIONS_COMMAND,
  ...MENU_ENTRIES.map((entry) => entry.command).filter(
    (command) => !ENGINE_COMMANDS.includes(command),
  ),
  REVEAL_RULE_COMMAND,
  'cel-lens.rules.showProblemsOnly',
  'cel-lens.rules.showAll',
  'cel-lens.rules.followEditor',
  'cel-lens.rules.stopFollowing',
  'cel-lens.giveValueType',
  'cel-lens.useExample',
  ...ENGINE_COMMANDS,
];

/**
 * Every setting in the same order: the menu's (the version, the color style and the toggles'
 * settings), then the rule view's, then the engine's. The README's Settings table and the
 * settings' `order` in package.json, so the Settings editor, follow it.
 */
export const SETTING_ORDER: readonly string[] = [
  'cel-lens.kubernetesVersion',
  'cel-lens.colorStyle',
  'cel-lens.inlineInfo',
  'cel-lens.exampleResults',
  'cel-lens.valueTypes',
  'cel-lens.ruleView.followEditor',
  'cel-lens.experimental',
  'cel-lens.trace.server',
  'cel-lens.serverPath',
];

/** What the menu shows, as it is in force for the active editor's file; the window's without one. */
export interface MenuState {
  /** The color style in force: the Color style entry names it, and Colors is off when it is `off`. */
  readonly style: CelStyle;
  /** The version entry's description: `versionDescription` says it. */
  readonly version: string;
  /** The engine failed or crashed: its group comes first. */
  readonly engineDown: boolean;
  readonly inlineInfo: boolean;
  readonly exampleResults: boolean;
  readonly valueTypes: boolean;
  /** `cellens-types` is on: only then the menu has Value types. */
  readonly cellensTypes: boolean;
}

function commandOf(label: string): string {
  const entry = MENU_ENTRIES.find((each) => each.label === label);
  if (entry === undefined) {
    throw new Error(`The menu has no entry ${label}.`);
  }
  return entry.command;
}

function onOff(on: boolean): string {
  return on ? 'on' : 'off';
}

/**
 * The menu's lines in order: the version and the color style; the group In the editor, with the
 * toggles; the group Engine, with the logs and the restart, which comes first when the engine
 * failed or crashed, and then a separator with no label. Each entry shows its current value as its description, and only the two
 * engine entries have an icon.
 */
export function actionItems(state: MenuState): ActionLine[] {
  const entry = (label: string, description: string, icon?: string): ActionItem => ({
    label,
    description,
    command: commandOf(label),
    ...(icon !== undefined && { icon }),
  });
  const top = [
    entry(VERSION_LABEL, state.version),
    entry(COLOR_STYLE_LABEL, styleLabel(state.style)),
  ];
  const editor: ActionLine[] = [
    { separator: EDITOR_SEPARATOR },
    entry(COLORS_LABEL, onOff(state.style !== 'off')),
    entry(INLINE_INFO_LABEL, onOff(state.inlineInfo)),
    entry(EXAMPLE_RESULTS_LABEL, onOff(state.exampleResults)),
    ...(state.cellensTypes ? [entry(VALUE_TYPES_LABEL, onOff(state.valueTypes))] : []),
  ];
  const engine: ActionLine[] = [
    { separator: ENGINE_SEPARATOR },
    entry(SHOW_LOGS_LABEL, '', '$(output)'),
    entry(RESTART_ENGINE_LABEL, '', '$(debug-restart)'),
  ];
  // A separator labels every line up to the next one, so the lines that follow the Engine group
  // when it comes first need a separator of their own, with no label.
  return state.engineDown
    ? [...engine, { separator: '' }, ...top, ...editor]
    : [...top, ...editor, ...engine];
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
