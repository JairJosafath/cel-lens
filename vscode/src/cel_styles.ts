import type { CelTokenKind } from './cel_lexer';

export type CelStyle = 'cel' | 'classic' | 'off';

/** The theme kinds VS Code has: light, dark and the two high contrast ones. */
export type ThemeKind = 'light' | 'dark' | 'hcDark' | 'hcLight';

export type PartColors = Record<CelTokenKind, string>;

type ThemeColors = Record<ThemeKind, PartColors>;

// cel: the hues of the CEL logo's plum #674049 (346 degrees) and lavender
// #958CC1 (250 degrees), with the lightness moved until every color reads on
// its theme: darker steps on light themes, lighter ones on dark themes.
const CEL: ThemeColors = {
  light: {
    variable: '#7E2A3E',
    field: '#4F3EA5',
    function: '#9D274F',
    number: '#6839D5',
    string: '#924B55',
    operator: '#635EA8',
    keyword: '#AD1F3B',
    punctuation: '#756BA6',
  },
  dark: {
    variable: '#E5B1BD',
    field: '#B8B0E1',
    function: '#E495AF',
    number: '#AF96E9',
    string: '#C69097',
    operator: '#9390C3',
    keyword: '#EB8C9F',
    punctuation: '#8B83B4',
  },
  hcDark: {
    variable: '#EBC2CB',
    field: '#C5BEE6',
    function: '#E9A7BD',
    number: '#BCA7EC',
    string: '#CFA2A8',
    operator: '#A3A1CC',
    keyword: '#EEA0AF',
    punctuation: '#9B94BF',
  },
  hcLight: {
    variable: '#60202F',
    field: '#3C2F7C',
    function: '#781E3C',
    number: '#4E25AF',
    string: '#6F3940',
    operator: '#4B4785',
    keyword: '#84172D',
    punctuation: '#584F84',
  },
};

// classic: the warm, brownish raw-text feel of the string color VS Code gives a
// YAML scalar, in steps of brown, amber and tan.
const CLASSIC: ThemeColors = {
  light: {
    variable: '#703B1C',
    field: '#674922',
    function: '#804817',
    number: '#75560D',
    string: '#7C5C3C',
    operator: '#98563A',
    keyword: '#765419',
    punctuation: '#8C6C54',
  },
  dark: {
    variable: '#E6B599',
    field: '#D5B081',
    function: '#E39C5F',
    number: '#D39B17',
    string: '#BA9570',
    operator: '#C7876B',
    keyword: '#D59C39',
    punctuation: '#A48268',
  },
  hcDark: {
    variable: '#EBC4AD',
    field: '#DDBF98',
    function: '#E8AE7B',
    number: '#E6A819',
    string: '#C5A687',
    operator: '#CF9981',
    keyword: '#DCAD5B',
    punctuation: '#B1937C',
  },
  hcLight: {
    variable: '#542C15',
    field: '#4D361A',
    function: '#623711',
    number: '#5A420A',
    string: '#5F462E',
    operator: '#73412B',
    keyword: '#583F13',
    punctuation: '#69513F',
  },
};

const STYLES = { cel: CEL, classic: CLASSIC } as const satisfies Record<
  Exclude<CelStyle, 'off'>,
  ThemeColors
>;

/** The color of each part of a CEL expression, or none for `off`. */
export function celColors(style: CelStyle, theme: ThemeKind): Partial<PartColors> {
  switch (style) {
    case 'off':
      return {};
    case 'cel':
    case 'classic':
      return STYLES[style][theme];
  }
}
