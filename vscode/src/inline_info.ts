import { settingInForce, type ScopedValues } from './boolean_setting';

/** The `cel-lens.inlineInfo` setting, as contributed in package.json. */
export const INLINE_INFO_SETTING = 'inlineInfo';
export const INLINE_INFO_SETTING_NAME = `cel-lens.${INLINE_INFO_SETTING}`;

/** The `cel-lens.toggleInlineInfo` command, as contributed in package.json. */
export const TOGGLE_INLINE_INFO_COMMAND = 'cel-lens.toggleInlineInfo';

/** The lines are on until a person turns them off. */
export const DEFAULT_INLINE_INFO = true;

/** Whether the lines show: the value of the narrowest scope that sets one, else on. */
export function inlineInfoInForce(values: ScopedValues | undefined): boolean {
  return settingInForce(values, DEFAULT_INLINE_INFO);
}
