import { afterEach, describe, expect, it } from 'vitest';

import { RELEASES_PAGE_URL } from './core_releases';
import {
  activeEditor,
  addWorkspaceFolder,
  changeActiveEditor,
  errorChoice,
  executedCommands,
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
import { CHOOSE_COLOR_STYLE_COMMAND } from './color_style';
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

  it('writes the picked version to the workspace setting', async () => {
    const status = new VersionStatus();
    status.setState(RUNNING);
    quickPicks.next = '1.36';

    await status.pick();

    expect(quickPicks.shown).toHaveLength(1);
    expect(settingUpdates).toEqual([
      { name: 'cel-lens.kubernetesVersion', value: '1.36', target: 2 },
    ]);
  });

  it('clears the workspace setting when the default is picked', async () => {
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

  describe('showActions', () => {
    it('lists the four actions in order, with the current style and version', async () => {
      const status = new VersionStatus();
      status.setState(RUNNING);
      settings.set('cel-lens.colorStyle', 'classic');
      settings.set('cel-lens.kubernetesVersion', '1.36');

      await status.showActions();

      expect(quickPicks.shown).toHaveLength(1);
      expect(quickPicks.shown[0]).toMatchObject([
        { label: 'Color style', description: 'Classic' },
        { label: 'Kubernetes version', description: '1.36' },
        { label: 'Restart Engine' },
        { label: 'Show Logs' },
      ]);
    });

    it.each([
      ['a', 'Classic'],
      ['b', 'CEL'],
    ])(
      'shows the style of the active editor folder %s in a two-folder workspace: %s',
      async (folder, label) => {
        const status = new VersionStatus();
        status.setState(RUNNING);
        addWorkspaceFolder('a', { 'cel-lens.colorStyle': 'classic' });
        addWorkspaceFolder('b');
        activeEditor.uri = `file:///workspace/${folder}/crd.yaml`;
        changeActiveEditor('yaml');

        await status.showActions();

        expect(quickPicks.shown[0]?.[0]).toMatchObject({
          label: 'Color style',
          description: label,
        });
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

        expect(quickPicks.shown[0]).toContainEqual(
          expect.objectContaining({ label: 'Kubernetes version', description }),
        );
      },
    );

    it.each([
      ['failed', { kind: 'failed' }],
      ['crashed', { kind: 'crashed' }],
    ] as const)('puts Restart Engine and Show Logs first when the engine %s', async (_n, state) => {
      const status = new VersionStatus();
      status.setState(state);

      await status.showActions();

      expect(quickPicks.shown[0]).toMatchObject([
        { label: 'Restart Engine' },
        { label: 'Show Logs' },
        { label: 'Color style' },
        { label: 'Kubernetes version' },
      ]);
    });

    it('runs Restart Engine from the menu of a crashed engine', async () => {
      const status = new VersionStatus();
      status.setState({ kind: 'crashed' });
      quickPicks.next = 'Restart Engine';

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
      ['Color style', CHOOSE_COLOR_STYLE_COMMAND],
      ['Kubernetes version', PICK_VERSION_COMMAND],
      ['Restart Engine', RESTART_SERVER_COMMAND],
      ['Show Logs', SHOW_LOGS_COMMAND],
    ])('runs the command of the %s entry', async (label, command) => {
      const status = new VersionStatus();
      status.setState(RUNNING);
      quickPicks.next = label;

      await status.showActions();

      expect(executedCommands).toEqual([{ command, args: [] }]);
    });

    it('runs nothing when the menu is closed with Escape', async () => {
      const status = new VersionStatus();
      status.setState(RUNNING);
      quickPicks.next = undefined;

      await status.showActions();

      expect(quickPicks.shown).toHaveLength(1);
      expect(executedCommands).toEqual([]);
    });
  });
});
