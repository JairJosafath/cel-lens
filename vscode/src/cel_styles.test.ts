import { describe, expect, it } from 'vitest';

import { celColors, type CelStyle, type ThemeKind } from './cel_styles';

const THEMES: ThemeKind[] = ['light', 'dark', 'hcDark', 'hcLight'];
const BACKGROUNDS: Record<ThemeKind, { color: string; contrast: number }> = {
  light: { color: '#FFFFFF', contrast: 4.5 },
  dark: { color: '#1E1E1E', contrast: 4.5 },
  hcDark: { color: '#000000', contrast: 7 },
  hcLight: { color: '#FFFFFF', contrast: 7 },
};
const DISTINCT = ['variable', 'field', 'function', 'number', 'string', 'operator'] as const;
const PARTS = [
  'variable',
  'field',
  'function',
  'keyword',
  'number',
  'string',
  'operator',
  'punctuation',
];

function rgb(hex: string): [number, number, number] {
  return [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)) as [number, number, number];
}

function luminance(hex: string): number {
  const [r, g, b] = rgb(hex).map((value) => {
    const c = value / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  }) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [x, y] = [luminance(a), luminance(b)];
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

function hue(hex: string): number {
  const [r, g, b] = rgb(hex);
  const max = Math.max(r, g, b);
  const delta = max - Math.min(r, g, b);
  const sector =
    max === r ? ((g - b) / delta) % 6 : max === g ? (b - r) / delta + 2 : (r - g) / delta + 4;
  return (sector * 60 + 360) % 360;
}

function apart(a: number, b: number): number {
  const difference = Math.abs(a - b) % 360;
  return Math.min(difference, 360 - difference);
}

const COMBINATIONS = (['cel', 'classic'] as const).flatMap((style) =>
  THEMES.flatMap((theme) =>
    Object.entries(celColors(style, theme)).map(([part, color]) => ({ style, theme, part, color })),
  ),
);

describe('celColors', () => {
  it.each(['cel', 'classic'] as const)(
    'gives %s a color for every part in every theme',
    (style) => {
      for (const theme of THEMES) {
        expect(Object.keys(celColors(style, theme)).sort()).toEqual([...PARTS].sort());
      }
    },
  );

  it.each(THEMES)('gives off no colors in %s themes', (theme) => {
    const style: CelStyle = 'off';
    expect(celColors(style, theme)).toEqual({});
  });

  it.each(COMBINATIONS.filter((c) => c.style === 'cel'))(
    'keeps the logo hues for cel $part in $theme',
    ({ color }) => {
      const distance = Math.min(
        apart(hue(color), hue('#674049')),
        apart(hue(color), hue('#958CC1')),
      );
      expect(distance).toBeLessThanOrEqual(10);
    },
  );

  it.each(COMBINATIONS.filter((c) => c.style === 'classic'))(
    'makes classic $part in $theme a brown, amber or tan',
    ({ color }) => {
      expect(hue(color)).toBeGreaterThanOrEqual(15);
      expect(hue(color)).toBeLessThanOrEqual(50);
    },
  );

  it.each(COMBINATIONS)('reads $style $part in $theme on its background', ({ theme, color }) => {
    const background = BACKGROUNDS[theme];
    expect(contrast(color, background.color)).toBeGreaterThanOrEqual(background.contrast);
  });

  it.each([
    ...(['cel', 'classic'] as const).flatMap((style) => THEMES.map((theme) => ({ style, theme }))),
  ])('tells the main parts of $style apart in $theme', ({ style, theme }) => {
    const colors = celColors(style, theme);
    const used = DISTINCT.map((part) => colors[part]);
    expect(new Set(used).size).toBe(DISTINCT.length);
  });
});
