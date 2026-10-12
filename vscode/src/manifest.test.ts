import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

interface Command {
  command: string;
  title: string;
  category?: string;
}
interface Setting {
  order?: number;
  markdownDescription?: string;
  enum?: string[];
  enumDescriptions?: string[];
}
interface Manifest {
  contributes: {
    commands: Command[];
    menus: { commandPalette: { command: string; when: string }[] };
    configuration: { properties: Record<string, Setting> };
  };
}

const MANIFEST = JSON.parse(readFileSync('package.json', 'utf8')) as Manifest;
const { commands, menus, configuration } = MANIFEST.contributes;
const INTERNAL_COMMANDS: string[] = [];
const SHOWN_WITHOUT_YAML = ['cel-lens.showLogs'];
const SETTINGS = Object.entries(configuration.properties);
const TITLE_VERBS = ['Choose', 'Pick', 'Restart', 'Show'];

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
      } else {
        expect(entry?.when).toContain('editorLangId == yaml');
      }
    },
  );
});

describe('settings in package.json', () => {
  it('puts the color style first and the Kubernetes version second', () => {
    const ordered = [...SETTINGS].sort((a, b) => (a[1].order ?? 0) - (b[1].order ?? 0));
    expect(ordered.slice(0, 2).map(([key]) => key)).toEqual([
      'cel-lens.colorStyle',
      'cel-lens.kubernetesVersion',
    ]);
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
