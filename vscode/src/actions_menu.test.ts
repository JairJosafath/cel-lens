import { describe, expect, it } from 'vitest';

import {
  EDITOR_SEPARATOR,
  ENGINE_SEPARATOR,
  MENU_ENTRIES,
  SHOW_LOGS_COMMAND,
  actionItems,
  versionDescription,
  type ActionLine,
  type MenuState,
} from './actions_menu';
import { CHOOSE_COLOR_STYLE_COMMAND, TOGGLE_COLORS_COMMAND } from './color_style';
import { TOGGLE_EXAMPLE_RESULTS_COMMAND } from './example_results';
import { TOGGLE_INLINE_INFO_COMMAND } from './inline_info';
import { PICK_VERSION_COMMAND, RESTART_SERVER_COMMAND } from './kubernetes_version';
import { TOGGLE_VALUE_TYPES_COMMAND } from './value_visibility';

const ALL: MenuState = {
  style: 'cel',
  version: '1.37',
  engineDown: false,
  inlineInfo: true,
  exampleResults: true,
  valueTypes: true,
  cellensTypes: true,
};

/** A line as a short word: an entry as its label and description, a heading as itself in brackets. */
function shown(lines: ActionLine[]): string[] {
  return lines.map((line) =>
    'separator' in line ? `[${line.separator}]` : `${line.label}: ${line.description}`,
  );
}

describe('actionItems', () => {
  it('lists the version, the color style, the group In the editor and the group Engine, in order', () => {
    expect(shown(actionItems({ ...ALL, style: 'classic', version: '1.36' }))).toEqual([
      'Kubernetes version: 1.36',
      'Color style: Classic',
      '[In the editor]',
      'Colors: on',
      'Inline info: on',
      'Example results: on',
      'Value types: on',
      '[Engine]',
      'Show Logs: ',
      'Restart Engine: ',
    ]);
  });

  it('leaves Value types out while cellens-types is off, whatever valueTypes says', () => {
    const labels = shown(actionItems({ ...ALL, cellensTypes: false, valueTypes: false }));

    expect(labels.some((line) => line.startsWith('Value types'))).toBe(false);
    expect(labels).toHaveLength(9);
  });

  it.each([
    ['cel', 'CEL', 'on'],
    ['classic', 'Classic', 'on'],
    ['off', 'Off', 'off'],
  ] as const)('says the style %s as %s, and Colors as %s', (style, label, colors) => {
    const lines = shown(actionItems({ ...ALL, style }));

    expect(lines).toContain(`Color style: ${label}`);
    expect(lines).toContain(`Colors: ${colors}`);
  });

  it.each([
    ['inline info', { inlineInfo: false }, 'Inline info: off'],
    ['example results', { exampleResults: false }, 'Example results: off'],
    ['value types', { valueTypes: false }, 'Value types: off'],
  ] as const)('says %s off in its description', (_name, change, line) => {
    expect(shown(actionItems({ ...ALL, ...change }))).toContain(line);
  });

  it('runs the command of each entry', () => {
    const commands = actionItems(ALL).flatMap((line) =>
      'separator' in line ? [] : [[line.label, line.command]],
    );

    expect(commands).toEqual([
      ['Kubernetes version', PICK_VERSION_COMMAND],
      ['Color style', CHOOSE_COLOR_STYLE_COMMAND],
      ['Colors', TOGGLE_COLORS_COMMAND],
      ['Inline info', TOGGLE_INLINE_INFO_COMMAND],
      ['Example results', TOGGLE_EXAMPLE_RESULTS_COMMAND],
      ['Value types', TOGGLE_VALUE_TYPES_COMMAND],
      ['Show Logs', SHOW_LOGS_COMMAND],
      ['Restart Engine', RESTART_SERVER_COMMAND],
    ]);
  });

  it('gives an icon to the two engine entries and to no other', () => {
    const icons = actionItems(ALL).flatMap((line) =>
      'separator' in line ? [] : [[line.label, line.icon]],
    );

    expect(icons).toEqual([
      ['Kubernetes version', undefined],
      ['Color style', undefined],
      ['Colors', undefined],
      ['Inline info', undefined],
      ['Example results', undefined],
      ['Value types', undefined],
      ['Show Logs', '$(output)'],
      ['Restart Engine', '$(debug-restart)'],
    ]);
  });

  it('never puts a value in a label', () => {
    for (const line of actionItems(ALL)) {
      if (!('separator' in line)) {
        expect(line.label).not.toMatch(/:|\bon\b|\boff\b/i);
      }
    }
  });

  it('puts the group Engine and its heading first when the engine is down, and keeps the rest in order', () => {
    expect(
      shown(actionItems({ ...ALL, engineDown: true, version: 'Engine failed to start' })),
    ).toEqual([
      '[Engine]',
      'Show Logs: ',
      'Restart Engine: ',
      '[]',
      'Kubernetes version: Engine failed to start',
      'Color style: CEL',
      '[In the editor]',
      'Colors: on',
      'Inline info: on',
      'Example results: on',
      'Value types: on',
    ]);
  });

  it.each([true, false])(
    'puts a separator between every group, so no line sits under a heading it is not in (engine down: %s)',
    (engineDown) => {
      const lines = actionItems({ ...ALL, engineDown });
      const engine = lines.findIndex((line) => 'separator' in line && line.separator === 'Engine');
      const after = lines.slice(engine + 1);
      const group = after.findIndex((line) => 'separator' in line);
      // The Engine heading holds only the two engine entries, up to the next separator or the end.
      const inEngine = group === -1 ? after : after.slice(0, group);

      expect(inEngine.map((line) => ('separator' in line ? '' : line.label))).toEqual([
        'Show Logs',
        'Restart Engine',
      ]);
    },
  );

  it('keeps its two headings as the words the menu shows', () => {
    expect([EDITOR_SEPARATOR, ENGINE_SEPARATOR]).toEqual(['In the editor', 'Engine']);
  });

  it('has one exported list of the entries, in the menu order, that the menu is built from', () => {
    expect(MENU_ENTRIES.map((entry) => entry.label)).toEqual([
      'Kubernetes version',
      'Color style',
      'Colors',
      'Inline info',
      'Example results',
      'Value types',
      'Show Logs',
      'Restart Engine',
    ]);
  });
});

describe('versionDescription', () => {
  it.each([
    [{ kind: 'starting' }, 'Engine starting'],
    [{ kind: 'stopped' }, 'Engine stopped'],
    [{ kind: 'failed' }, 'Engine failed to start'],
    [{ kind: 'crashed' }, 'Engine stopped after crashing'],
    [{ kind: 'running', info: undefined }, 'Engine reports no versions'],
    [
      { kind: 'running', info: { inUse: '1.37', defaultVersion: '1.37', supported: ['1.37'] } },
      '1.37',
    ],
  ] as const)('says %j as %s', (state, expected) => {
    expect(versionDescription(state, '')).toBe(expected);
  });
});
