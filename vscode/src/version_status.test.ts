import { afterEach, describe, expect, it } from 'vitest';

import { RELEASES_PAGE_URL } from './core_releases';
import {
  ConfigurationTarget,
  Uri,
  activeEditor,
  addWorkspaceFolder,
  emptyWindow,
  updateFailure,
  workspaceFileUri,
  changeActiveEditor,
  errorChoice,
  executedCommands,
  QuickPickItemKind,
  openedUrls,
  quickPicks,
  reset,
  settingUpdates,
  settings,
  shownErrors,
  shownInfos,
  statusBarItems,
  window,
} from '../test/vscode';
import { PICK_VERSION_COMMAND, RESTART_SERVER_COMMAND } from './kubernetes_version';
import { SHOW_ACTIONS_COMMAND, SHOW_LOGS_COMMAND } from './actions_menu';
import { CHOOSE_COLOR_STYLE_COMMAND, TOGGLE_COLORS_COMMAND } from './color_style';
import { TOGGLE_EXAMPLE_RESULTS_COMMAND } from './example_results';
import { TOGGLE_INLINE_INFO_COMMAND } from './inline_info';
import { TOGGLE_VALUE_TYPES_COMMAND } from './value_visibility';
import { VersionStatus } from './version_status';

const INFO = { inUse: '1.37', defaultVersion: '1.37', supported: ['1.37', '1.36'] };
const RUNNING = { kind: 'running', info: INFO } as const;

function item(): (typeof statusBarItems)[number] {
  const [first] = statusBarItems;
  if (first === undefined) {
    throw new Error('no status bar item was created');
  }
  return first;
}

describe('VersionStatus', () => {
  afterEach(reset);

  it('shows the default version on a YAML file, with a tooltip and the pick command', () => {
    const status = new VersionStatus();
    changeActiveEditor('yaml');

    status.setState(RUNNING);

    expect(item()).toMatchObject({
      text: 'CEL · K8s 1.37',
      visible: true,
      command: SHOW_ACTIONS_COMMAND,
    });
    expect(item().tooltip).toBe('cel-lens: Kubernetes 1.37 · click for actions');
  });

  it('shows the setting instead of the default, once refreshed', () => {
    const status = new VersionStatus();
    changeActiveEditor('yaml');
    status.setState(RUNNING);
    settings.set('cel-lens.kubernetesVersion', '1.36');

    status.refresh();

    expect(item().text).toBe('CEL · K8s 1.36');
  });

  it.each([
    ['not YAML', 'json'],
    ['no editor', undefined],
  ])('hides on %s, and shows when a YAML file opens', (_name, languageId) => {
    const status = new VersionStatus();
    status.setState(RUNNING);

    changeActiveEditor(languageId);
    expect(item().visible).toBe(false);

    changeActiveEditor('yaml');
    expect(item().visible).toBe(true);
  });

  it.each([
    ['failed to start', { kind: 'failed' }, 'cel-lens: the engine failed to start'],
    ['crashed', { kind: 'crashed' }, 'cel-lens: the engine stopped after crashing'],
  ] as const)('stays visible and says the engine is down after it %s', (_name, state, tooltip) => {
    const status = new VersionStatus();
    changeActiveEditor('yaml');
    status.setState(RUNNING);

    status.setState(state);

    expect(item()).toMatchObject({
      text: '$(error) CEL · Engine down',
      visible: true,
      command: SHOW_ACTIONS_COMMAND,
    });
    expect(item().tooltip).toContain(tooltip);
  });

  it('shows the version again after a restart succeeds', () => {
    const status = new VersionStatus();
    changeActiveEditor('yaml');
    status.setState({ kind: 'failed' });

    status.setState({ kind: 'starting' });
    status.setState(RUNNING);

    expect(item()).toMatchObject({ text: 'CEL · K8s 1.37', visible: true });
  });

  it('hides when the server reports no version', () => {
    const status = new VersionStatus();
    changeActiveEditor('yaml');
    status.setState(RUNNING);

    status.setState({ kind: 'running', info: undefined });

    expect(item().visible).toBe(false);
  });

  it('writes the picked version to the workspace setting with a folder open', async () => {
    addWorkspaceFolder('workspace');
    const status = new VersionStatus();
    status.setState(RUNNING);
    quickPicks.next = '1.36';

    await status.pick();

    expect(quickPicks.shown).toHaveLength(1);
    expect(settingUpdates).toEqual([
      { name: 'cel-lens.kubernetesVersion', value: '1.36', target: ConfigurationTarget.Workspace },
    ]);
  });

  it('writes the picked version to the workspace setting with a workspace file open, even one with no folder (#47)', async () => {
    workspaceFileUri.value = Uri.parse('file:///home/me/project.code-workspace');
    const status = new VersionStatus();
    status.setState(RUNNING);
    quickPicks.next = '1.36';

    await status.pick();

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.kubernetesVersion', value: '1.36', target: ConfigurationTarget.Workspace },
    ]);
  });

  it('writes the picked version to the user setting in a window with no folder open, where the workspace setting cannot be written (#47)', async () => {
    emptyWindow.value = true;
    const status = new VersionStatus();
    status.setState(RUNNING);
    quickPicks.next = '1.36';

    await status.pick();

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.kubernetesVersion', value: '1.36', target: ConfigurationTarget.Global },
    ]);
    expect(shownErrors).toEqual([]);
  });

  it('says once that the version could not be written, with Open Settings, and logs why (#47)', async () => {
    addWorkspaceFolder('workspace');
    updateFailure.error = new Error('settings.json has errors');
    errorChoice.next = 'Open Settings';
    const log = window.createOutputChannel('cel-lens');
    const status = new VersionStatus(log);
    status.setState(RUNNING);
    quickPicks.next = '1.36';

    await status.pick();

    expect(shownErrors).toEqual([
      {
        message:
          'Could not change cel-lens.kubernetesVersion. Fix any errors in settings.json, then pick the version again.',
        buttons: ['Open Settings'],
      },
    ]);
    expect(executedCommands).toEqual([
      { command: 'workbench.action.openSettings', args: ['cel-lens.kubernetesVersion'] },
    ]);
    expect(log.lines.join('\n')).toContain('settings.json has errors');
    expect(settingUpdates).toEqual([]);
  });

  it('clears the workspace setting when the default is picked', async () => {
    addWorkspaceFolder('workspace');
    const status = new VersionStatus();
    status.setState(RUNNING);
    quickPicks.next = 'Default (1.37)';

    await status.pick();

    expect(settingUpdates).toEqual([
      { name: 'cel-lens.kubernetesVersion', value: undefined, target: 2 },
    ]);
  });

  it('changes nothing when the pick is dismissed', async () => {
    const status = new VersionStatus();
    status.setState(RUNNING);

    await status.pick();

    expect(settingUpdates).toEqual([]);
  });

  it('says the server is starting, not that none runs, while it starts', async () => {
    const status = new VersionStatus();
    status.setState({ kind: 'starting' });

    await status.pick();

    expect(quickPicks.shown).toEqual([]);
    expect(shownInfos).toHaveLength(1);
    expect(shownInfos[0]).toContain('still starting');
    expect(shownInfos[0]).toContain('engine');
    expect(shownInfos[0]).not.toContain('language server');
    expect(shownErrors).toEqual([]);
  });

  it('offers logs, a restart and the server setting after the server failed to start', async () => {
    const status = new VersionStatus();
    status.setState({ kind: 'failed' });
    errorChoice.next = 'Restart Engine';

    await status.pick();

    expect(shownErrors).toHaveLength(1);
    expect(shownErrors[0]?.message).toContain('failed to start');
    expect(shownErrors[0]?.message).toContain('engine');
    expect(shownErrors[0]?.message).not.toContain('language server');
    expect(shownErrors[0]?.message).toContain('restart the engine');
    expect(shownErrors[0]?.buttons).toEqual(['Show Logs', 'Restart Engine', 'Open Engine Setting']);
    expect(executedCommands).toEqual([{ command: RESTART_SERVER_COMMAND, args: [] }]);
  });

  it('offers a restart when the server is not running', async () => {
    const status = new VersionStatus();

    await status.pick();

    expect(shownErrors[0]?.message).toContain('not running');
    expect(shownErrors[0]?.message).toContain('engine');
    expect(shownErrors[0]?.message).not.toContain('language server');
    expect(shownErrors[0]?.message).toContain('Restart the engine.');
    expect(shownErrors[0]?.buttons).toEqual(['Restart Engine']);
  });

  it('offers the releases page, a restart and the server setting when the server reports no versions', async () => {
    const status = new VersionStatus();
    status.setState({ kind: 'running', info: undefined });
    errorChoice.next = 'Open Releases Page';

    await status.pick();

    expect(quickPicks.shown).toEqual([]);
    expect(shownErrors[0]?.message).toContain('too old');
    expect(shownErrors[0]?.message).toContain('engine');
    expect(shownErrors[0]?.message).not.toContain('language server');
    expect(shownErrors[0]?.message).toContain('restart the engine');
    expect(shownErrors[0]?.message).toContain('core-vX.Y.Z');
    expect(shownErrors[0]?.message).toContain('github.com/JairJosafath/cel-lens');
    expect(shownErrors[0]?.buttons).toEqual([
      'Open Releases Page',
      'Restart Engine',
      'Open Engine Setting',
    ]);
    expect(openedUrls).toEqual(['https://github.com/JairJosafath/cel-lens/releases']);
    expect(openedUrls).toEqual([RELEASES_PAGE_URL]);
  });

  it('opens the server setting from the failed message', async () => {
    const status = new VersionStatus();
    status.setState({ kind: 'failed' });
    errorChoice.next = 'Open Engine Setting';

    await status.pick();

    expect(executedCommands).toEqual([
      { command: 'workbench.action.openSettings', args: ['cel-lens.serverPath'] },
    ]);
  });

  it('shows no second message while the first is open', async () => {
    const status = new VersionStatus();
    status.setState({ kind: 'failed' });
    errorChoice.never = true;

    void status.pick();
    await status.pick();

    expect(shownErrors).toHaveLength(1);
  });

  it('disposes the item and the listener', () => {
    const status = new VersionStatus();

    status.dispose();

    expect(item().disposed).toBe(true);
  });

  it('shows the default and warns once when the setting is not a supported version', () => {
    const log = window.createOutputChannel('cel-lens-status');
    const status = new VersionStatus(log);
    changeActiveEditor('yaml');
    status.setState(RUNNING);
    settings.set('cel-lens.kubernetesVersion', '1.99');

    status.refresh();
    status.refresh();

    expect(item().text).toBe('CEL · K8s 1.37');
    expect(log.lines.filter((line) => line.includes('"1.99"'))).toHaveLength(1);
  });

  describe('its accessible name', () => {
    it('says the name, the Kubernetes version and what a click does while the engine runs', () => {
      const status = new VersionStatus();
      changeActiveEditor('yaml');
      settings.set('cel-lens.kubernetesVersion', '1.36');

      status.setState(RUNNING);

      expect(item().accessibilityInformation).toEqual({
        label: 'cel-lens, Kubernetes 1.36, click for actions',
        role: 'button',
      });
    });

    it('follows the version when the setting changes', () => {
      const status = new VersionStatus();
      changeActiveEditor('yaml');
      status.setState(RUNNING);
      expect(item().accessibilityInformation?.label).toBe(
        'cel-lens, Kubernetes 1.37, click for actions',
      );

      settings.set('cel-lens.kubernetesVersion', '1.36');
      status.refresh();

      expect(item().accessibilityInformation?.label).toBe(
        'cel-lens, Kubernetes 1.36, click for actions',
      );
    });

    it.each([
      ['failed', { kind: 'failed' }],
      ['crashed', { kind: 'crashed' }],
    ] as const)(
      'says the engine is down in words, and what a click does, when it %s',
      (_name, state) => {
        const status = new VersionStatus();

        status.setState(state);

        expect(item().accessibilityInformation).toEqual({
          label: 'cel-lens engine down, click to restart it or show the logs',
          role: 'button',
        });
      },
    );
  });

  describe('showActions', () => {
    /** The lines of the menu last shown, as the quick pick got them. */
    function lines(): { label: string; description?: string; kind?: number; command?: string }[] {
      return quickPicks.shown[0] as { label: string; description?: string; kind?: number }[];
    }

    /** One line as a short word: a heading in brackets, an entry as its label and description. */
    function words(): string[] {
      return lines().map((line) =>
        line.kind === QuickPickItemKind.Separator
          ? `[${line.label}]`
          : `${line.label}: ${line.description ?? ''}`,
      );
    }

    it('lists the settings, the toggles and the engine actions in order, each with its value, with the headings as separators', async () => {
      const status = new VersionStatus();
      status.setState(RUNNING);
      settings.set('cel-lens.colorStyle', 'classic');
      settings.set('cel-lens.kubernetesVersion', '1.36');

      await status.showActions();

      expect(quickPicks.shown).toHaveLength(1);
      expect(words()).toEqual([
        'Kubernetes version: 1.36',
        'Color style: Classic',
        '[In the editor]',
        'Colors: on',
        'Inline info: on',
        'Example results: on',
        '[Engine]',
        '$(output) Show Logs: ',
        '$(debug-restart) Restart Engine: ',
      ]);
    });

    it('asks under the title cel-lens with the placeholder that names a setting, a toggle and an action', async () => {
      const status = new VersionStatus();
      status.setState(RUNNING);

      await status.showActions();

      expect(quickPicks.options).toEqual([
        { title: 'cel-lens', placeHolder: 'Pick a setting, a toggle or an action' },
      ]);
    });

    it('has Value types only while cellens-types is in cel-lens.experimental, with its value', async () => {
      const status = new VersionStatus();
      status.setState(RUNNING);
      settings.set('cel-lens.experimental', ['cellens-types']);
      settings.set('cel-lens.valueTypes', false);

      await status.showActions();

      expect(words()).toContain('Value types: off');
      expect(words().indexOf('Value types: off')).toBe(words().indexOf('Example results: on') + 1);
    });

    it('says Colors off, and Color style Off, when the style is off', async () => {
      const status = new VersionStatus();
      status.setState(RUNNING);
      settings.set('cel-lens.colorStyle', 'off');

      await status.showActions();

      expect(words()).toContain('Colors: off');
      expect(words()).toContain('Color style: Off');
    });

    it('shows the values of the window when no editor is active', async () => {
      const status = new VersionStatus();
      status.setState(RUNNING);
      settings.set('cel-lens.inlineInfo', false);

      await status.showActions();

      expect(words()).toContain('Inline info: off');
    });

    it.each([
      ['inlineInfo', 'a', 'Inline info: off'],
      ['inlineInfo', 'b', 'Inline info: on'],
      ['exampleResults', 'a', 'Example results: off'],
      ['exampleResults', 'b', 'Example results: on'],
      ['valueTypes', 'a', 'Value types: off'],
      ['valueTypes', 'b', 'Value types: on'],
    ])(
      'shows %s for the active editor folder %s of a two-folder workspace, which turns it off in folder a: %s',
      async (key, folder, line) => {
        const status = new VersionStatus();
        status.setState(RUNNING);
        settings.set('cel-lens.experimental', ['cellens-types']);
        addWorkspaceFolder('a', { [`cel-lens.${key}`]: false });
        addWorkspaceFolder('b');
        activeEditor.uri = `file:///workspace/${folder}/crd.yaml`;
        changeActiveEditor('yaml');

        await status.showActions();

        expect(words()).toContain(line);
      },
    );

    it.each([
      ['a', 'Classic', 'on'],
      ['b', 'CEL', 'on'],
      ['c', 'Off', 'off'],
    ])(
      'shows the style of the active editor folder %s in a workspace of three folders: %s, and Colors %s',
      async (folder, label, colors) => {
        const status = new VersionStatus();
        status.setState(RUNNING);
        addWorkspaceFolder('a', { 'cel-lens.colorStyle': 'classic' });
        addWorkspaceFolder('b');
        addWorkspaceFolder('c', { 'cel-lens.colorStyle': 'off' });
        activeEditor.uri = `file:///workspace/${folder}/crd.yaml`;
        changeActiveEditor('yaml');

        await status.showActions();

        expect(words()).toContain(`Color style: ${label}`);
        expect(words()).toContain(`Colors: ${colors}`);
      },
    );

    it.each([
      ['starting', { kind: 'starting' }, 'Engine starting'],
      ['stopped', { kind: 'stopped' }, 'Engine stopped'],
      ['failed', { kind: 'failed' }, 'Engine failed to start'],
      ['crashed', { kind: 'crashed' }, 'Engine stopped after crashing'],
      [
        'running without versions',
        { kind: 'running', info: undefined },
        'Engine reports no versions',
      ],
    ] as const)(
      'names the engine state in the version entry when it is %s',
      async (_name, state, description) => {
        const status = new VersionStatus();
        status.setState(state);

        await status.showActions();

        expect(words()).toContain(`Kubernetes version: ${description}`);
      },
    );

    it.each([
      ['failed', { kind: 'failed' }],
      ['crashed', { kind: 'crashed' }],
    ] as const)(
      'puts the Engine group and its heading first when the engine %s',
      async (_n, state) => {
        const status = new VersionStatus();
        status.setState(state);

        await status.showActions();

        expect(words().map((word) => word.split(':')[0])).toEqual([
          '[Engine]',
          '$(output) Show Logs',
          '$(debug-restart) Restart Engine',
          '[]',
          'Kubernetes version',
          'Color style',
          '[In the editor]',
          'Colors',
          'Inline info',
          'Example results',
        ]);
      },
    );

    it.each([
      ['failed', { kind: 'failed' }],
      ['crashed', { kind: 'crashed' }],
    ] as const)(
      'has no line under the Engine heading but the two engine entries when the engine %s (#45)',
      async (_n, state) => {
        const status = new VersionStatus();
        status.setState(state);

        await status.showActions();

        const all = lines();
        const heading = all.findIndex((line) => line.label === 'Engine');
        const rest = all.slice(heading + 1);
        const next = rest.findIndex((line) => line.kind === QuickPickItemKind.Separator);
        expect(rest.slice(0, next).map((line) => line.label)).toEqual([
          '$(output) Show Logs',
          '$(debug-restart) Restart Engine',
        ]);
        expect(rest[next]?.label).toBe('');
      },
    );

    it('runs Restart Engine from the menu of a crashed engine', async () => {
      const status = new VersionStatus();
      status.setState({ kind: 'crashed' });
      quickPicks.next = '$(debug-restart) Restart Engine';

      await status.showActions();

      expect(executedCommands).toEqual([{ command: RESTART_SERVER_COMMAND, args: [] }]);
    });

    it('offers logs and a restart when picking a version after a crash', async () => {
      const status = new VersionStatus();
      status.setState({ kind: 'crashed' });

      await status.pick();

      expect(shownErrors[0]?.message).toContain('crashing');
      expect(shownErrors[0]?.buttons).toEqual(['Show Logs', 'Restart Engine']);
    });

    it.each([
      ['Kubernetes version', PICK_VERSION_COMMAND],
      ['Color style', CHOOSE_COLOR_STYLE_COMMAND],
      ['Colors', TOGGLE_COLORS_COMMAND],
      ['Inline info', TOGGLE_INLINE_INFO_COMMAND],
      ['Example results', TOGGLE_EXAMPLE_RESULTS_COMMAND],
      ['Value types', TOGGLE_VALUE_TYPES_COMMAND],
      ['$(output) Show Logs', SHOW_LOGS_COMMAND],
      ['$(debug-restart) Restart Engine', RESTART_SERVER_COMMAND],
    ])('runs the command of the %s entry, and only it', async (label, command) => {
      const status = new VersionStatus();
      status.setState(RUNNING);
      settings.set('cel-lens.experimental', ['cellens-types']);
      quickPicks.next = label;

      await status.showActions();

      expect(executedCommands).toEqual([{ command, args: [] }]);
      expect(quickPicks.shown).toHaveLength(1);
    });

    it.each(['In the editor', 'Engine'])('runs nothing for the heading %s', async (heading) => {
      const status = new VersionStatus();
      status.setState(RUNNING);
      quickPicks.next = heading;

      await status.showActions();

      expect(executedCommands).toEqual([]);
    });

    it('runs nothing when the menu is closed with Escape', async () => {
      const status = new VersionStatus();
      status.setState(RUNNING);
      quickPicks.next = undefined;

      await status.showActions();

      expect(quickPicks.shown).toHaveLength(1);
      expect(executedCommands).toEqual([]);
    });

    it('marks the headings as separators and no other line, and gives them no command', async () => {
      const status = new VersionStatus();
      status.setState(RUNNING);

      await status.showActions();

      const separators = lines().filter((line) => line.kind === QuickPickItemKind.Separator);
      expect(separators.map((line) => line.label)).toEqual(['In the editor', 'Engine']);
      expect(separators.every((line) => line.command === undefined)).toBe(true);
    });
  });
});
