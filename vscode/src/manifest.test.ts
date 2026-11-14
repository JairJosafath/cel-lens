import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { TOGGLE_COLORS_COMMAND } from './color_style';
import { EXAMPLE_SCHEME, USE_EXAMPLE_COMMAND } from './example_editor';
import { SHOW_EXAMPLE_RESULTS_COMMAND, TOGGLE_EXAMPLE_RESULTS_COMMAND } from './example_results';
import { TOGGLE_VALUE_TYPES_COMMAND, VALUE_TYPES_SETTING_NAME } from './value_visibility';
import { SHOW_LOGS_COMMAND } from './actions_menu';
import { CHOOSE_COLOR_STYLE_COMMAND } from './color_style';
import { TOGGLE_INLINE_INFO_COMMAND } from './inline_info';
import { PICK_VERSION_COMMAND, RESTART_SERVER_COMMAND } from './kubernetes_version';
import { COMMAND_ORDER, MENU_ENTRIES, SETTING_ORDER, SHOW_ACTIONS_COMMAND } from './actions_menu';
import { whenImplies } from './key_clash';
import { REVEAL_RULE_COMMAND } from './rule_view_state';
import {
  FOLLOW_EDITOR_COMMAND,
  SHOW_ALL_RULES_COMMAND,
  SHOW_PROBLEMS_ONLY_COMMAND,
  STOP_FOLLOWING_COMMAND,
} from './rule_view';
import { GIVE_VALUE_TYPE_COMMAND } from './value_types';
import {
  FIX_SAMPLE_PROBLEM_COMMAND,
  GET_STARTED_COMMAND,
  OPEN_SAMPLE_COMMAND,
  SAMPLE_FIXED_CONTEXT,
  WALKTHROUGH_ID,
} from './get_started';

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
/** An entry of a menu: a command or a submenu, with its `when` and its group. */
interface MenuEntry {
  command?: string;
  submenu?: string;
  when?: string;
  group?: string;
}
interface WalkthroughStep {
  id: string;
  title: string;
  description: string;
  media: { svg?: string; altText?: string };
  completionEvents?: string[];
  when?: string;
}
interface Walkthrough {
  id: string;
  title: string;
  description: string;
  when?: string;
  steps: WalkthroughStep[];
}
interface Manifest {
  name: string;
  publisher: string;
  contributes: {
    walkthroughs?: Walkthrough[];
    commands: Command[];
    keybindings?: { command: string; key: string; mac?: string; when?: string }[];
    submenus?: { id: string; label: string }[];
    menus: {
      commandPalette: { command: string; when: string }[];
      'editor/title'?: MenuEntry[];
      'editor/context'?: MenuEntry[];
      'cel-lens.editor'?: MenuEntry[];
    };
    configuration: { properties: Record<string, Setting> };
  };
}

const MANIFEST = JSON.parse(readFileSync('package.json', 'utf8')) as Manifest;
const { commands, menus, configuration, walkthroughs } = MANIFEST.contributes;
const INTERNAL_COMMANDS = [
  'cel-lens.showRule',
  'cel-lens.showExampleResults',
  'cel-lens.listValues',
  'cel-lens.giveType',
  'cel-lens.editType',
  'cel-lens.removeType',
  'cel-lens.rules.showProblems',
  'cel-lens.rules.showExampleResults',
  'cel-lens.openSample',
  'cel-lens.fixSampleProblem',
];
/** In the palette on any file, by the context keys the view's title bar uses. */
const SHOWN_BY_KEY: Record<string, string> = {
  'cel-lens.rules.showProblemsOnly': '!cel-lens.ruleView.problemsOnly',
  'cel-lens.rules.showAll': 'cel-lens.ruleView.problemsOnly',
  'cel-lens.rules.followEditor': '!cel-lens.ruleView.followEditor',
  'cel-lens.rules.stopFollowing': 'cel-lens.ruleView.followEditor',
};
const SHOWN_WITHOUT_YAML = ['cel-lens.showLogs', 'cel-lens.getStarted'];
/** Shown only on the editor of a pasted example, whatever its language. */
const SHOWN_ON_EXAMPLES = ['cel-lens.useExample'];
const SETTINGS = Object.entries(configuration.properties);
const TITLE_VERBS = [
  'Choose',
  'Edit',
  'Fix',
  'Follow',
  'Get',
  'Give',
  'Open',
  'Pick',
  'Remove',
  'Restart',
  'Reveal',
  'Show',
  'Stop',
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
      } else if (name in SHOWN_BY_KEY) {
        expect(entry?.when).toBe(SHOWN_BY_KEY[name]);
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

describe('Toggle Colors and Toggle Value Types in package.json', () => {
  it('has the command Toggle Colors, in the palette on YAML files', () => {
    expect(commands.find((c) => c.command === TOGGLE_COLORS_COMMAND)).toEqual({
      command: TOGGLE_COLORS_COMMAND,
      title: 'Toggle Colors',
      category: 'cel-lens',
    });
    expect(menus.commandPalette.find((e) => e.command === TOGGLE_COLORS_COMMAND)?.when).toBe(
      'editorLangId == yaml',
    );
  });

  it('has the command Toggle Value Types, in the palette on YAML files while cellens-types is on', () => {
    expect(commands.find((c) => c.command === TOGGLE_VALUE_TYPES_COMMAND)).toEqual({
      command: TOGGLE_VALUE_TYPES_COMMAND,
      title: 'Toggle Value Types',
      category: 'cel-lens',
    });
    expect(menus.commandPalette.find((e) => e.command === TOGGLE_VALUE_TYPES_COMMAND)?.when).toBe(
      'editorLangId == yaml && cel-lens.cellensTypes',
    );
  });

  it('contributes cel-lens.valueTypes as a boolean that is on by default, with resource scope', () => {
    const valueTypes = configuration.properties[VALUE_TYPES_SETTING_NAME];
    expect(valueTypes).toMatchObject({ type: 'boolean', default: true, scope: 'resource' });
    expect(typeof valueTypes?.order).toBe('number');
  });

  it('says in the description of cel-lens.valueTypes what it hides, that the types still apply, what it needs and how to flip it', () => {
    const text = configuration.properties[VALUE_TYPES_SETTING_NAME]?.markdownDescription ?? '';
    for (const part of ['Give it a type', 'Edit', 'Remove', '`+N more`']) {
      expect(text).toContain(part);
    }
    expect(text).toContain('the types you gave still apply');
    expect(text).toContain('`cellens-types`');
    expect(text).toContain('**cel-lens: Toggle Value Types**');
    expect(text).toContain('status bar menu');
  });
});

describe('the walkthrough and the menus agree (the join of the walkthrough with the toggles and the menus)', () => {
  const text = (walkthroughs ?? [])
    .flatMap((each) => each.steps)
    .map((step) => step.description)
    .join('\n');

  it('names Toggle Colors, which is a command, and the status bar menu, which turns the colors off and on', () => {
    expect(text).toContain('**Toggle Colors** and the status bar menu turn them off and on again');
    expect(commands.find((c) => c.command === TOGGLE_COLORS_COMMAND)?.title).toBe('Toggle Colors');
    expect(MENU_ENTRIES.map((entry) => entry.command)).toContain(TOGGLE_COLORS_COMMAND);
  });

  it('says the status bar menu has every setting and toggle, and it has a command for each toggle and each setting picker', () => {
    expect(text).toContain('which has every setting and toggle of cel-lens');
    const inTheMenu = MENU_ENTRIES.map((entry) => entry.command);
    for (const command of commands.filter((each) => each.title.startsWith('Toggle '))) {
      expect(inTheMenu).toContain(command.command);
    }
    expect(inTheMenu).toContain('cel-lens.pickKubernetesVersion');
    expect(inTheMenu).toContain('cel-lens.chooseColorStyle');
  });
});

describe('the order a person meets the commands in (#41)', () => {
  const palette = menus.commandPalette
    .filter((entry) => entry.when !== 'false')
    .map((entry) => entry.command);

  it('lists every command of the Command Palette once, and no other', () => {
    expect([...COMMAND_ORDER].sort()).toEqual([...palette].sort());
    expect(new Set(COMMAND_ORDER).size).toBe(COMMAND_ORDER.length);
  });

  it('starts with Get Started and the menu, follows the menu’s entries, and ends with its engine entries', () => {
    expect(COMMAND_ORDER.slice(0, 2)).toEqual([GET_STARTED_COMMAND, SHOW_ACTIONS_COMMAND]);
    const inTheMenu: readonly string[] = MENU_ENTRIES.map((entry) => entry.command);
    expect(COMMAND_ORDER.filter((command) => inTheMenu.includes(command))).toEqual(inTheMenu);
    expect(COMMAND_ORDER.slice(-2)).toEqual([SHOW_LOGS_COMMAND, RESTART_SERVER_COMMAND]);
  });

  it('names the rule view’s commands and giving a type by the ids the extension registers', () => {
    expect(COMMAND_ORDER.slice(8, 15)).toEqual([
      REVEAL_RULE_COMMAND,
      SHOW_PROBLEMS_ONLY_COMMAND,
      SHOW_ALL_RULES_COMMAND,
      FOLLOW_EDITOR_COMMAND,
      STOP_FOLLOWING_COMMAND,
      GIVE_VALUE_TYPE_COMMAND,
      USE_EXAMPLE_COMMAND,
    ]);
  });
});

describe('the keybindings in package.json (#38)', () => {
  const keybindings = MANIFEST.contributes.keybindings ?? [];

  it('contributes one: Ctrl+K L, Cmd+K L on macOS, for Toggle Inline Info while typing in YAML', () => {
    expect(keybindings).toEqual([
      {
        command: TOGGLE_INLINE_INFO_COMMAND,
        key: 'ctrl+k l',
        mac: 'cmd+k l',
        when: 'editorTextFocus && editorLangId == yaml',
      },
    ]);
  });

  it.each(keybindings.map((binding) => [binding.command, binding] as const))(
    'has the command %s of its keybinding contributed, and in the palette with a when its own when implies',
    (command, binding) => {
      expect(commands.map((each) => each.command)).toContain(command);
      const palette = menus.commandPalette.find((each) => each.command === command);

      expect(palette).toBeDefined();
      expect(whenImplies(binding.when, palette?.when ?? 'false')).toBe(true);
    },
  );
});

describe('the editor submenu in package.json (#37)', () => {
  const HERE = 'editorLangId == yaml && cel-lens.editorHasRules';
  const submenu = menus['cel-lens.editor'] ?? [];

  /** The entries of one group of the submenu, as `[command, when]`, in their order. */
  function group(name: string): [string | undefined, string | undefined][] {
    return submenu
      .filter((entry) => entry.group?.split('@')[0] === name)
      .sort((a, b) => Number(a.group?.split('@')[1]) - Number(b.group?.split('@')[1]))
      .map((entry) => [entry.command, entry.when]);
  }

  it('contributes the submenu cel-lens.editor with the label cel-lens', () => {
    expect(MANIFEST.contributes.submenus).toEqual([{ id: 'cel-lens.editor', label: 'cel-lens' }]);
  });

  it('shows it in the editor context menu, in the group 1_cel-lens, only in YAML the engine lists rules for', () => {
    expect(menus['editor/context']).toEqual([
      { submenu: 'cel-lens.editor', when: HERE, group: '1_cel-lens' },
    ]);
  });

  it('shows it in the overflow of the editor title, in a group that is not navigation, so it has no icon', () => {
    const entry = menus['editor/title']?.find((each) => each.submenu === 'cel-lens.editor');

    expect(entry).toEqual({ submenu: 'cel-lens.editor', when: HERE, group: '1_cel-lens' });
    expect(entry?.group).not.toBe('navigation');
  });

  it('holds the rule actions in 1_rule, each by what the cursor is in', () => {
    expect(group('1_rule')).toEqual([
      ['cel-lens.showRule', 'cel-lens.ruleAtCursor'],
      [REVEAL_RULE_COMMAND, 'cel-lens.ruleAtCursor'],
      [SHOW_EXAMPLE_RESULTS_COMMAND, 'cel-lens.ruleAtCursor == ruleWithExamples'],
      ['cel-lens.giveValueType', 'cel-lens.cellensTypes'],
    ]);
  });

  it('holds the four toggles in 2_toggles, Value Types only while cellens-types is on', () => {
    expect(group('2_toggles')).toEqual([
      [TOGGLE_COLORS_COMMAND, undefined],
      [TOGGLE_INLINE_INFO_COMMAND, undefined],
      [TOGGLE_EXAMPLE_RESULTS_COMMAND, undefined],
      [TOGGLE_VALUE_TYPES_COMMAND, 'cel-lens.cellensTypes'],
    ]);
  });

  it('holds the Kubernetes version and the color style in 3_settings', () => {
    expect(group('3_settings')).toEqual([
      [PICK_VERSION_COMMAND, undefined],
      [CHOOSE_COLOR_STYLE_COMMAND, undefined],
    ]);
  });

  it('holds nothing else, and neither Show Logs nor Restart Engine, which are about the engine', () => {
    const commands = submenu.map((entry) => entry.command);

    expect(submenu).toHaveLength(10);
    expect(commands).not.toContain(SHOW_LOGS_COMMAND);
    expect(commands).not.toContain(RESTART_SERVER_COMMAND);
  });

  it('retitles Show Rule as Show Problems, the word of the view, and titles Reveal in Rule View', () => {
    const title = (name: string): string | undefined =>
      commands.find((c) => c.command === name)?.title;

    expect(title('cel-lens.showRule')).toBe('Show Problems');
    expect(title(REVEAL_RULE_COMMAND)).toBe('Reveal in Rule View');
  });

  it('puts Reveal in Rule View in the palette for YAML the engine lists rules for', () => {
    expect(menus.commandPalette.find((e) => e.command === REVEAL_RULE_COMMAND)?.when).toBe(HERE);
  });

  it('keeps the commands that need a rule or a row as their argument out of the palette', () => {
    for (const name of INTERNAL_COMMANDS) {
      expect(menus.commandPalette.find((e) => e.command === name)?.when).toBe('false');
    }
  });
});

describe('Use This Example as the Type', () => {
  const where = `resourceScheme == ${EXAMPLE_SCHEME}`;

  it('shows in the Command Palette only on the editor of a pasted example, by its scheme (#14)', () => {
    expect(menus.commandPalette.find((e) => e.command === USE_EXAMPLE_COMMAND)?.when).toBe(where);
  });

  it('is the one icon of the editor title, shown only on the editor of a pasted example, by its scheme (#14)', () => {
    expect(menus['editor/title']?.filter((entry) => entry.group === 'navigation')).toEqual([
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
  it('gives the settings the order 0 to 8 of SETTING_ORDER, the menu’s, which the README’s Settings table follows too (#41)', () => {
    expect(SETTINGS.map(([key]) => key).sort()).toEqual([...SETTING_ORDER].sort());
    expect(SETTING_ORDER.map((key) => configuration.properties[key]?.order)).toEqual(
      SETTING_ORDER.map((_key, index) => index),
    );
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

/** The buttons of a step's description: each `[label](command:id)` on its own line. */
function buttonsOf(description: string): { label: string; command: string }[] {
  return [...description.matchAll(/^\[([^\]]+)\]\(command:([\w.-]+)\)$/gm)].map((match) => ({
    label: match[1] ?? '',
    command: match[2] ?? '',
  }));
}

/** The commands a step names: its buttons' and its completion events'. */
function commandsOf(step: WalkthroughStep): string[] {
  return [
    ...[...step.description.matchAll(/command:([\w.-]+)/g)].map((match) => match[1] ?? ''),
    ...(step.completionEvents ?? [])
      .filter((event) => event.startsWith('onCommand:'))
      .map((event) => event.slice('onCommand:'.length)),
  ];
}

describe('the Get started with cel-lens walkthrough', () => {
  const [walkthrough] = walkthroughs ?? [];
  const steps = walkthrough?.steps ?? [];

  it('is the one walkthrough, getStarted, shown with no when clause', () => {
    expect(walkthroughs).toHaveLength(1);
    expect(walkthrough).toMatchObject({
      id: 'getStarted',
      title: 'Get started with cel-lens',
      description: 'Check the CEL rules of your Kubernetes YAML as you type.',
    });
    expect(walkthrough?.when).toBeUndefined();
    expect(`${MANIFEST.publisher}.${MANIFEST.name}#${walkthrough?.id ?? ''}`).toBe(WALKTHROUGH_ID);
  });

  it('has four steps, in this order, with their titles, buttons and completion events', () => {
    expect(
      steps.map((step) => ({
        id: step.id,
        title: step.title,
        buttons: buttonsOf(step.description),
        completionEvents: step.completionEvents,
      })),
    ).toEqual([
      {
        id: 'openSample',
        title: 'Open a sample CRD',
        buttons: [{ label: 'Open the Sample CRD', command: OPEN_SAMPLE_COMMAND }],
        completionEvents: [`onCommand:${OPEN_SAMPLE_COMMAND}`],
      },
      {
        id: 'fixProblem',
        title: 'See a problem and fix it',
        buttons: [{ label: 'Fix the Problem', command: FIX_SAMPLE_PROBLEM_COMMAND }],
        completionEvents: [`onContext:${SAMPLE_FIXED_CONTEXT}`],
      },
      {
        id: 'pickVersion',
        title: 'Pick the Kubernetes version',
        buttons: [{ label: 'Pick Kubernetes Version', command: 'cel-lens.pickKubernetesVersion' }],
        completionEvents: ['onCommand:cel-lens.pickKubernetesVersion'],
      },
      {
        id: 'chooseColors',
        title: 'Choose the colors',
        buttons: [{ label: 'Choose Color Style', command: 'cel-lens.chooseColorStyle' }],
        completionEvents: ['onCommand:cel-lens.chooseColorStyle'],
      },
    ]);
  });

  it.each(steps.map((step) => [step.id, step] as const))(
    '%s has one button, whose label starts with a verb, and no when clause',
    (_id, step) => {
      const buttons = buttonsOf(step.description);
      expect(buttons).toHaveLength(1);
      expect(startsWithVerb(buttons[0]?.label ?? '')).toBe(true);
      expect(step.description.match(/\]\(command:/g)).toHaveLength(1);
      expect(step.when).toBeUndefined();
    },
  );

  it.each(steps.map((step) => [step.id, step] as const))(
    '%s has its picture, images/walkthrough/<step>.svg, with alt text',
    (id, step) => {
      expect(step.media.svg).toBe(`images/walkthrough/${id}.svg`);
      expect(step.media.altText?.length ?? 0).toBeGreaterThan(0);
    },
  );

  it.each(steps.flatMap((step) => commandsOf(step).map((command) => [step.id, command] as const)))(
    '%s names %s, which package.json contributes',
    (_id, command) => {
      expect(commands.map((c) => c.command)).toContain(command);
    },
  );

  it('says in the version step that the status bar item shows the version and its menu has every setting and toggle', () => {
    const step = steps.find((each) => each.id === 'pickVersion');
    expect(step?.description).toContain('status bar item shows');
    expect(step?.description).toContain('every setting and toggle');
  });

  it('names Toggle Colors in the colors step', () => {
    expect(steps.find((each) => each.id === 'chooseColors')?.description).toContain(
      '**Toggle Colors**',
    );
  });
});

describe('Get Started and the walkthrough’s commands in package.json', () => {
  it('has Get Started in the palette on any file', () => {
    expect(commands.find((c) => c.command === GET_STARTED_COMMAND)).toEqual({
      command: GET_STARTED_COMMAND,
      title: 'Get Started',
      category: 'cel-lens',
    });
    expect(menus.commandPalette.find((e) => e.command === GET_STARTED_COMMAND)?.when).toBe('true');
  });

  it.each([OPEN_SAMPLE_COMMAND, FIX_SAMPLE_PROBLEM_COMMAND])(
    'hides %s from the palette: it is a step’s button',
    (command) => {
      expect(menus.commandPalette.find((e) => e.command === command)?.when).toBe('false');
    },
  );
});
