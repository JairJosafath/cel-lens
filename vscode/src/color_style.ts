import type { CelStyle } from './cel_styles';

/** The `cel-lens.colorStyle` setting, as contributed in package.json. */
export const COLOR_STYLE_SETTING_SECTION = 'cel-lens';
export const COLOR_STYLE_SETTING = 'colorStyle';
export const COLOR_STYLE_SETTING_NAME = `${COLOR_STYLE_SETTING_SECTION}.${COLOR_STYLE_SETTING}`;

/** The key before 0.3.0, still read when `cel-lens.colorStyle` is not set. */
export const LEGACY_COLOR_STYLE_SETTING_SECTION = 'celLens';
export const LEGACY_COLOR_STYLE_SETTING_NAME = `${LEGACY_COLOR_STYLE_SETTING_SECTION}.${COLOR_STYLE_SETTING}`;

/** The `cel-lens.chooseColorStyle` command, as contributed in package.json. */
export const CHOOSE_COLOR_STYLE_COMMAND = 'cel-lens.chooseColorStyle';

/** The style used when the setting is missing or not one of the three. */
export const DEFAULT_COLOR_STYLE: CelStyle = 'cel';

const STYLE_DESCRIPTIONS = {
  cel: 'Plum and lavender, the hues of the cel-lens logo (the default)',
  classic: 'Warm browns and ambers, close to a plain YAML string',
  off: 'No cel-lens colors; the theme alone colors the YAML',
} as const satisfies Record<CelStyle, string>;

const STYLE_LABELS = {
  cel: 'CEL',
  classic: 'Classic',
  off: 'Off',
} as const satisfies Record<CelStyle, string>;

const STYLES: readonly CelStyle[] = ['cel', 'classic', 'off'];

/** Reads the setting's value; anything that is not a style gives the default. */
export function parseColorStyle(value: unknown): CelStyle {
  return STYLES.find((style) => style === value) ?? DEFAULT_COLOR_STYLE;
}

/** The name the user sees for a style, in the quick pick and the actions menu. */
export function styleLabel(style: CelStyle): string {
  return STYLE_LABELS[style];
}

/** One line of the color style quick pick. */
export interface StyleItem {
  readonly label: string;
  readonly description: string;
  readonly detail: string;
  readonly style: CelStyle;
}

/** The three styles for the quick pick, the current one marked. */
export function styleItems(current: CelStyle): StyleItem[] {
  return STYLES.map((style) => ({
    label: styleLabel(style),
    description: style === current ? '$(check) current' : '',
    detail: STYLE_DESCRIPTIONS[style],
    style,
  }));
}
