import { existsSync, readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

import {
  FOLLOW_EDITOR_KEY,
  KYVERNO_OFF_KEY,
  PROBLEMS_ONLY_KEY,
  RULE_VIEW_CONTAINER_ID,
  RULE_VIEW_ID,
  RULE_VIEW_STATES,
  RULE_VIEW_STATE_KEY,
} from './rule_view_state';

interface Welcome {
  view: string;
  contents: string;
  when: string;
}
interface Manifest {
  activationEvents: string[];
  contributes: {
    commands: { command: string; title: string; icon?: string }[];
    configuration: { properties: Record<string, Record<string, unknown>> };
    viewsContainers: { activitybar: { id: string; title: string; icon: string }[] };
    views: Record<string, { id: string; name: string }[]>;
    viewsWelcome: Welcome[];
    menus: {
      commandPalette: { command: string; when: string }[];
      'view/item/context': { command: string; when: string; group: string }[];
      'view/title': { command: string; when: string; group: string }[];
    };
  };
}

const MANIFEST = JSON.parse(readFileSync('package.json', 'utf8')) as Manifest;
const { viewsContainers, views, viewsWelcome, menus } = MANIFEST.contributes;
const CONTRIBUTED = MANIFEST.contributes.commands.map((each) => each.command);
/** The built-in commands the welcome texts link to. */
const BUILT_IN = ['workbench.action.files.openFolder', 'workbench.action.openSettings'];

function welcomeOf(state: string): Welcome[] {
  return viewsWelcome.filter((entry) =>
    entry.when.includes(`${RULE_VIEW_STATE_KEY} == '${state}'`),
  );
}

function linksOf(entries: readonly Welcome[]): { label: string; target: string }[] {
  return entries.flatMap((entry) =>
    [...entry.contents.matchAll(/\[([^\]]+)\]\(([^)]+)\)/g)].map((match) => ({
      label: match[1] ?? '',
      target: match[2] ?? '',
    })),
  );
}

describe('the view container', () => {
  it('is one entry of the activity bar, cel-lens, with its own icon', () => {
    expect(viewsContainers.activitybar).toEqual([
      { id: RULE_VIEW_CONTAINER_ID, title: 'cel-lens', icon: 'images/cel-lens.svg' },
    ]);
    expect(existsSync('images/cel-lens.svg')).toBe(true);
  });

  it('holds one tree view, cel-lens.rules, named Rules', () => {
    expect(Object.keys(views)).toEqual([RULE_VIEW_CONTAINER_ID]);
    expect(views[RULE_VIEW_CONTAINER_ID]).toMatchObject([{ id: RULE_VIEW_ID, name: 'Rules' }]);
    expect(views[RULE_VIEW_CONTAINER_ID]).toHaveLength(1);
  });

  it('adds no activation event: the view activates the extension', () => {
    expect(MANIFEST.activationEvents).toEqual(['onLanguage:yaml', 'onFileSystem:cel-lens-example']);
  });

  it('has an icon of one colour VS Code can tint: no colour of its own, no fill but none', () => {
    const svg = readFileSync('images/cel-lens.svg', 'utf8');

    expect(svg).toContain('stroke="currentColor"');
    expect(svg).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(svg).not.toMatch(/(?:fill|stroke)="(?!none|currentColor)/);
    expect(svg).toMatch(/viewBox="0 0 24 24"/);
  });
});

describe('the welcome texts', () => {
  it.each(RULE_VIEW_STATES)('has one for the state %s, for this view alone', (state) => {
    const entries = welcomeOf(state);

    expect(entries.length).toBeGreaterThan(0);
    expect(entries.every((entry) => entry.view === RULE_VIEW_ID)).toBe(true);
  });

  it('has none for a view with rows, and reads only the states it knows', () => {
    const states = viewsWelcome.map(
      (entry) => new RegExp(`${RULE_VIEW_STATE_KEY} == '(\\w+)'`).exec(entry.when)?.[1],
    );

    expect(states.every((state) => RULE_VIEW_STATES.some((each) => each === state))).toBe(true);
  });

  it('says to open a folder, with a link named Open Folder', () => {
    expect(welcomeOf('noFolder')[0]?.contents).toContain('Open a folder to list the CEL rules');
    expect(linksOf(welcomeOf('noFolder'))).toEqual([
      { label: 'Open Folder', target: 'command:workbench.action.files.openFolder' },
    ]);
  });

  it('says the engine is not running, with links to Show Logs and Restart Engine', () => {
    expect(welcomeOf('engineDown')[0]?.contents).toContain('The cel-lens engine is not running.');
    expect(linksOf(welcomeOf('engineDown'))).toEqual([
      { label: 'Show Logs', target: 'command:cel-lens.showLogs' },
      { label: 'Restart Engine', target: 'command:cel-lens.restartServer' },
    ]);
  });

  it('says the engine cannot list rules, with a link to the engine setting and one to the logs', () => {
    expect(welcomeOf('unsupported')[0]?.contents).toContain(
      'cannot list rules, which a newer one does',
    );
    expect(linksOf(welcomeOf('unsupported'))).toEqual([
      {
        label: 'Open the Engine Setting',
        target: 'command:workbench.action.openSettings?%5B%22cel-lens.serverPath%22%5D',
      },
      { label: 'Show Logs', target: 'command:cel-lens.showLogs' },
    ]);
  });

  it('says it is looking while it scans', () => {
    expect(welcomeOf('scanning')).toMatchObject([
      { contents: 'Looking for CEL rules in the workspace…' },
    ]);
  });

  const README = 'https://github.com/JairJosafath/cel-lens/tree/main/vscode#readme';
  const SETTING = 'command:workbench.action.openSettings?%5B%22cel-lens.experimental%22%5D';

  it('says nothing was found while kyverno is off, names the kinds listed, and links to the README and the experimental setting', () => {
    const [off] = welcomeOf('empty').filter((entry) =>
      entry.when.endsWith(` && ${KYVERNO_OFF_KEY}`),
    );

    expect(off?.contents).toContain('No CEL rules in this workspace yet.');
    for (const kind of [
      'CustomResourceDefinitions',
      'ValidatingAdmissionPolicies',
      'MutatingAdmissionPolicies',
    ]) {
      expect(off?.contents).toContain(kind);
    }
    expect(off?.contents).toContain('Kyverno ValidatingPolicies are listed once the kyverno');
    expect(linksOf(off === undefined ? [] : [off])).toEqual([
      { label: 'Read What cel-lens Lists', target: README },
      { label: 'Open the Experimental Setting', target: SETTING },
    ]);
  });

  it('says nothing was found while kyverno is on, and names Kyverno ValidatingPolicies among the kinds listed', () => {
    const [on] = welcomeOf('empty').filter((entry) =>
      entry.when.endsWith(` && !${KYVERNO_OFF_KEY}`),
    );

    expect(on?.contents).toContain('MutatingAdmissionPolicies and Kyverno ValidatingPolicies.');
    expect(on?.contents).not.toContain('once the kyverno');
    expect(linksOf(on === undefined ? [] : [on])).toEqual([
      { label: 'Read What cel-lens Lists', target: README },
    ]);
  });

  it('has exactly one text for each state of kyverno when nothing was found', () => {
    expect(welcomeOf('empty')).toHaveLength(2);
  });

  it('links only to commands that exist, and every link’s label says where it goes', () => {
    for (const { label, target } of linksOf(viewsWelcome)) {
      expect(label).toMatch(/^[A-Z]/);
      if (target.startsWith('command:')) {
        const command = target.slice('command:'.length).split('?')[0] ?? '';
        expect([...CONTRIBUTED, ...BUILT_IN]).toContain(command);
      } else {
        expect(target).toMatch(/^https:\/\//);
      }
    }
  });
});

describe('the context menu of a rule', () => {
  const MENU = menus['view/item/context'];

  it('has Show Problems for every rule, and Show Example Results only for one with example words', () => {
    expect(MENU).toEqual([
      {
        command: 'cel-lens.rules.showProblems',
        when: 'view == cel-lens.rules && viewItem =~ /^rule/',
        group: 'navigation@1',
      },
      {
        command: 'cel-lens.rules.showExampleResults',
        when: 'view == cel-lens.rules && viewItem == ruleWithExamples',
        group: 'navigation@2',
      },
    ]);
  });

  it('keeps both commands out of the palette: they take the row the menu was opened on', () => {
    for (const { command } of MENU) {
      expect(menus.commandPalette.find((entry) => entry.command === command)?.when).toBe('false');
    }
  });

  it('is shown for the contexts the tree sets, and for no other', () => {
    const shown = (when: string, value: string): boolean =>
      when.includes('viewItem =~ /^rule/')
        ? value.startsWith('rule')
        : when.endsWith(`== ${value}`);

    expect(
      MENU.map((entry) =>
        ['rule', 'ruleWithExamples', 'document', 'folder', 'openFiles', 'warning'].map((value) =>
          shown(entry.when, value),
        ),
      ),
    ).toEqual([
      [true, true, false, false, false, false],
      [false, true, false, false, false, false],
    ]);
  });
});

describe('the title menu of the view', () => {
  const TITLE = menus['view/title'];
  const VIEW = 'view == cel-lens.rules';

  it('has one icon for the filter at a time, picked by the context key', () => {
    expect(TITLE.filter((entry) => entry.group === 'navigation@1')).toEqual([
      {
        command: 'cel-lens.rules.showProblemsOnly',
        when: `${VIEW} && !${PROBLEMS_ONLY_KEY}`,
        group: 'navigation@1',
      },
      {
        command: 'cel-lens.rules.showAll',
        when: `${VIEW} && ${PROBLEMS_ONLY_KEY}`,
        group: 'navigation@1',
      },
    ]);
  });

  it('gives the two commands of the filter an icon each, and none to the other two', () => {
    const icon = (name: string): string | undefined =>
      MANIFEST.contributes.commands.find((entry) => entry.command === name)?.icon;

    expect(icon('cel-lens.rules.showProblemsOnly')).toBe('$(filter)');
    expect(icon('cel-lens.rules.showAll')).toBe('$(filter-filled)');
    expect(icon('cel-lens.rules.followEditor')).toBeUndefined();
    expect(icon('cel-lens.rules.stopFollowing')).toBeUndefined();
  });

  it('has Follow Editor and Stop Following in the overflow menu, one at a time, picked by the context key', () => {
    expect(TITLE.filter((entry) => entry.group === '1_follow@1')).toEqual([
      {
        command: 'cel-lens.rules.followEditor',
        when: `${VIEW} && !${FOLLOW_EDITOR_KEY}`,
        group: '1_follow@1',
      },
      {
        command: 'cel-lens.rules.stopFollowing',
        when: `${VIEW} && ${FOLLOW_EDITOR_KEY}`,
        group: '1_follow@1',
      },
    ]);
  });

  it('titles the four commands as the issue names them, and has each in the palette by its key (#37)', () => {
    const titles = Object.fromEntries(
      MANIFEST.contributes.commands.map((entry) => [entry.command, entry.title]),
    );

    expect(titles['cel-lens.rules.showProblemsOnly']).toBe('Show Problems Only');
    expect(titles['cel-lens.rules.showAll']).toBe('Show All Rules');
    expect(titles['cel-lens.rules.followEditor']).toBe('Follow Editor');
    expect(titles['cel-lens.rules.stopFollowing']).toBe('Stop Following');
    const when = (command: string): string | undefined =>
      menus.commandPalette.find((entry) => entry.command === command)?.when;
    expect(when('cel-lens.rules.showProblemsOnly')).toBe('!cel-lens.ruleView.problemsOnly');
    expect(when('cel-lens.rules.showAll')).toBe('cel-lens.ruleView.problemsOnly');
    expect(when('cel-lens.rules.followEditor')).toBe('!cel-lens.ruleView.followEditor');
    expect(when('cel-lens.rules.stopFollowing')).toBe('cel-lens.ruleView.followEditor');
  });
});

describe('the setting cel-lens.ruleView.followEditor', () => {
  const setting = MANIFEST.contributes.configuration.properties['cel-lens.ruleView.followEditor'];

  it('is a boolean, on by default, with window scope', () => {
    expect(setting).toMatchObject({ type: 'boolean', default: true, scope: 'window' });
    expect(typeof setting?.['markdownDescription']).toBe('string');
  });
});

describe('the welcome text for a filter that shows nothing', () => {
  it('says no rule has a problem, with a link named Show All Rules that switches the filter off', () => {
    const [entry] = welcomeOf('noProblems');

    expect(entry?.contents).toContain('No rule has a problem.');
    expect(linksOf(entry === undefined ? [] : [entry])).toEqual([
      { label: 'Show All Rules', target: 'command:cel-lens.rules.showAll' },
    ]);
  });
});
