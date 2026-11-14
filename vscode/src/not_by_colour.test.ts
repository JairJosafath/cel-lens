// Nothing the extension draws tells a state by color alone (the request of cel-lens#3, step 6):
// each element says it in words or in a shape as well. What a test can see is held here, at the
// lowest tier that sees it: the menu's values, the walkthrough's pictures and what the extension
// does to the engine's lines. The rule view's icons and words are in `rule_items.test.ts`, the
// status bar item's accessible name in `version_status.test.ts`, every color of both styles in
// `cel_styles.test.ts`, and the lines the engine sends in `test/e2e/engine.test.ts`.
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { actionItems, type ActionItem, type MenuState } from './actions_menu';
import { withoutValueParts } from './value_visibility';

const ALL: MenuState = {
  style: 'cel',
  version: '1.37',
  engineDown: false,
  inlineInfo: true,
  exampleResults: true,
  valueTypes: true,
  cellensTypes: true,
};

function entries(over: Partial<MenuState> = {}): ActionItem[] {
  return actionItems({ ...ALL, ...over }).flatMap((line) => ('separator' in line ? [] : [line]));
}

describe('the status bar menu', () => {
  it.each<[string, Partial<MenuState>]>([
    ['on', {}],
    ['off', { inlineInfo: false, exampleResults: false, valueTypes: false, style: 'off' }],
  ])(
    'tells the value of each toggle in a word, %s, and gives a toggle no icon to tell it by',
    (word, over) => {
      const toggles = entries(over).filter((entry) =>
        ['Colors', 'Inline info', 'Example results', 'Value types'].includes(entry.label),
      );

      expect(toggles.map((entry) => entry.description)).toEqual([word, word, word, word]);
      expect(toggles.every((entry) => entry.icon === undefined)).toBe(true);
    },
  );

  it('tells the color style by its name, and the version by its number, in words', () => {
    const description = (label: string): string | undefined =>
      entries().find((entry) => entry.label === label)?.description;

    expect(description('Color style')).toBe('CEL');
    expect(description('Kubernetes version')).toBe('1.37');
  });

  it('has an icon on the two engine entries only, and each also has its words', () => {
    const withIcon = entries().filter((entry) => entry.icon !== undefined);

    expect(withIcon.map((entry) => entry.label)).toEqual(['Show Logs', 'Restart Engine']);
  });
});

describe('the line above a rule', () => {
  it('leaves the engine’s lenses as they come: the client drops some and gives none a color or a style of its own', () => {
    const lens = (command: string, title: string): object => ({
      range: { start: { line: 3 } },
      command: { command, title },
    });
    const lenses = [
      lens('cel-lens.showRule', 'self: Widget.spec · compiles'),
      lens('cel-lens.giveType', 'params: dyn · Give it a type'),
      lens('cel-lens.showExampleResults', 'refuses 1 example it should allow'),
    ];

    const kept = withoutValueParts(lenses as Parameters<typeof withoutValueParts>[0]);

    expect(kept).toEqual([lenses[0], lenses[2]]);
    for (const each of kept ?? []) {
      expect(Object.keys(each).sort()).toEqual(['command', 'range']);
    }
  });
});

describe('the walkthrough’s pictures', () => {
  const FILES = ['openSample', 'fixProblem', 'pickVersion', 'chooseColors'].map((name) =>
    readFileSync(`images/walkthrough/${name}.svg`, 'utf8'),
  );

  it('are drawn with theme variables only: step 5’s test, src/walkthrough_svg.test.ts, holds every property that takes a color; no literal color is in any of them', () => {
    const step5 = readFileSync('src/walkthrough_svg.test.ts', 'utf8');

    expect(step5).toContain('THEME_VARIABLE');
    for (const svg of FILES) {
      expect(svg).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
      expect(svg).not.toMatch(/\b(?:rgb|rgba|hsl|hsla)\(/);
    }
  });
});
