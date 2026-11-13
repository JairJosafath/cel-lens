import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { EXAMPLE_SCHEME, USE_EXAMPLE_COMMAND } from './example_editor';
import { SHOW_EXAMPLE_RESULTS_COMMAND, TOGGLE_EXAMPLE_RESULTS_COMMAND } from './example_results';

interface Command {
  command: string;
  title: string;
  category?: string;
}
interface Setting {
  type?: string;
  default?: unknown;
  scope?: string;
  order?: number;
  markdownDescription?: string;
  enum?: string[];
  enumDescriptions?: string[];
}
interface Manifest {
  contributes: {
    commands: Command[];
    menus: {
      commandPalette: { command: string; when: string }[];
      'editor/title'?: { command: string; when: string; group?: string }[];
    };
    configuration: { properties: Record<string, Setting> };
  };
}

const MANIFEST = JSON.parse(readFileSync('package.json', 'utf8')) as Manifest;
const { commands, menus, configuration } = MANIFEST.contributes;
const INTERNAL_COMMANDS = [
  'cel-lens.showRule',
  'cel-lens.showExampleResults',
  'cel-lens.listValues',
  'cel-lens.giveType',
  'cel-lens.editType',
  'cel-lens.removeType',
];
const SHOWN_WITHOUT_YAML = ['cel-lens.showLogs'];
/** Shown only on the editor of a pasted example, whatever its language. */
const SHOWN_ON_EXAMPLES = ['cel-lens.useExample'];
const SETTINGS = Object.entries(configuration.properties);
const TITLE_VERBS = [
  'Choose',
  'Edit',
  'Give',
  'Pick',
  'Remove',
  'Restart',
  'Show',
  'Toggle',
  'Use',
];

function startsWithVerb(title: string): boolean {
  return TITLE_VERBS.includes(title.split(' ')[0] ?? '');
}

describe('startsWithVerb', () => {
  it.each([
    ['Restart Engine', true],
    ['Show Logs', true],
    ['Engine Restart', false],
    ['Logs', false],
    ['restart Engine', false],
    ['', false],
  ])('%j gives %s', (title, expected) => {
    expect(startsWithVerb(title)).toBe(expected);
  });
});

describe('commands in package.json', () => {
  it.each(commands.map((c) => [c.command, c] as const))(
    '%s has the category cel-lens and a title that starts with a verb from the allow-list',
    (_name, c) => {
      expect(c.category).toBe('cel-lens');
      expect(startsWithVerb(c.title)).toBe(true);
    },
  );

  it.each(commands.map((c) => c.command))(
    '%s is in the command palette menu with a when clause',
    (name) => {
      const entry = menus.commandPalette.find((e) => e.command === name);
      expect(entry).toBeDefined();
      if (INTERNAL_COMMANDS.includes(name)) {
        expect(entry?.when).toBe('false');
      } else if (SHOWN_WITHOUT_YAML.includes(name)) {
        expect(entry?.when).toBe('true');
      } else if (SHOWN_ON_EXAMPLES.includes(name)) {
        expect(entry?.when).toBe('resourceScheme == cel-lens-example');
      } else {
        expect(entry?.when).toContain('editorLangId == yaml');
      }
    },
  );
});

describe('Example results in package.json', () => {
  it('has the command Toggle Example Results, in the palette on YAML files', () => {
    expect(commands.find((c) => c.command === TOGGLE_EXAMPLE_RESULTS_COMMAND)).toEqual({
      command: TOGGLE_EXAMPLE_RESULTS_COMMAND,
      title: 'Toggle Example Results',
      category: 'cel-lens',
    });
    expect(
      menus.commandPalette.find((e) => e.command === TOGGLE_EXAMPLE_RESULTS_COMMAND)?.when,
    ).toBe('editorLangId == yaml');
  });

  it('has the command of the part of the line above a rule, which the palette never lists: it needs a rule', () => {
    expect(commands.find((c) => c.command === SHOW_EXAMPLE_RESULTS_COMMAND)?.title).toBe(
      'Show Example Results',
    );
    expect(menus.commandPalette.find((e) => e.command === SHOW_EXAMPLE_RESULTS_COMMAND)?.when).toBe(
      'false',
    );
  });
});

describe('Use This Example as the Type', () => {
  const where = `resourceScheme == ${EXAMPLE_SCHEME}`;

  it('shows in the Command Palette only on the editor of a pasted example, by its scheme (#14)', () => {
    expect(menus.commandPalette.find((e) => e.command === USE_EXAMPLE_COMMAND)?.when).toBe(where);
  });

  it('is the one icon of the editor title, shown only on the editor of a pasted example, by its scheme (#14)', () => {
    expect(menus['editor/title']).toEqual([
      { command: USE_EXAMPLE_COMMAND, when: where, group: 'navigation' },
    ]);
    expect(commands.find((c) => c.command === USE_EXAMPLE_COMMAND)).toEqual({
      command: USE_EXAMPLE_COMMAND,
      title: 'Use This Example as the Type',
      category: 'cel-lens',
      icon: '$(check)',
    });
  });
});

describe('settings in package.json', () => {
  it('puts the color style first, the inline info second, the example results third and the Kubernetes version fourth', () => {
    const ordered = [...SETTINGS].sort((a, b) => (a[1].order ?? 0) - (b[1].order ?? 0));
    expect(ordered.slice(0, 4).map(([key]) => key)).toEqual([
      'cel-lens.colorStyle',
      'cel-lens.inlineInfo',
      'cel-lens.exampleResults',
      'cel-lens.kubernetesVersion',
    ]);
  });

  it('contributes cel-lens.exampleResults as a boolean that is on by default, with resource scope, as cel-lens.inlineInfo is', () => {
    const exampleResults = configuration.properties['cel-lens.exampleResults'];
    const inlineInfo = configuration.properties['cel-lens.inlineInfo'];
    expect(exampleResults).toMatchObject({ type: 'boolean', default: true, scope: 'resource' });
    expect(exampleResults?.scope).toBe(inlineInfo?.scope);
    expect(exampleResults?.markdownDescription).toContain('cel-lens: Toggle Example Results');
  });

  it('contributes no celLens.* setting', () => {
    expect(SETTINGS.map(([key]) => key).filter((key) => key.startsWith('celLens.'))).toEqual([]);
  });

  it('gives every setting a distinct order', () => {
    const orders = SETTINGS.map(([, s]) => s.order);
    expect(orders.every((o) => typeof o === 'number')).toBe(true);
    expect(new Set(orders).size).toBe(orders.length);
  });

  it.each(SETTINGS)('%s has a markdownDescription', (_key, s) => {
    expect(s.markdownDescription?.length ?? 0).toBeGreaterThan(0);
  });

  it.each(SETTINGS.filter(([, s]) => s.enum !== undefined))(
    '%s has one enumDescription per enum value',
    (_key, s) => {
      expect(s.enumDescriptions).toHaveLength(s.enum?.length ?? -1);
    },
  );
});
