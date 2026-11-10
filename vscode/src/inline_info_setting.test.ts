import { afterEach, describe, expect, it } from 'vitest';

import {
  ConfigurationTarget,
  Uri,
  activeEditor,
  addWorkspaceFolder,
  changeActiveEditor,
  errorChoice,
  executedCommands,
  reset,
  settingUpdates,
  settings,
  shownErrors,
  updateFailure,
  window,
  workspaceSettings,
} from '../test/vscode';
import { engineInlineInfo, readInlineInfo, toggleInlineInfo } from './inline_info_setting';

const NAME = 'cel-lens.inlineInfo';

function log(): ReturnType<typeof window.createOutputChannel> {
  return window.createOutputChannel('cel-lens');
}

describe('readInlineInfo', () => {
  afterEach(reset);

  it.each([
    ['set nowhere', undefined, undefined, true],
    ['off in the user settings', false, undefined, false],
    ['off in the user settings and on in the workspace', false, true, true],
  ])('is on unless turned off: %s', (_name, user, workspace, expected) => {
    if (user !== undefined) {
      settings.set(NAME, user);
    }
    if (workspace !== undefined) {
      workspaceSettings.set(NAME, workspace);
    }

    expect(readInlineInfo()).toBe(expected);
  });

  it("reads a file's workspace folder setting over the workspace's", () => {
    workspaceSettings.set(NAME, true);
    addWorkspaceFolder('a', { [NAME]: false });
    addWorkspaceFolder('b');

    expect(readInlineInfo(Uri.parse('file:///workspace/a/crd.yaml'))).toBe(false);
    expect(readInlineInfo(Uri.parse('file:///workspace/b/crd.yaml'))).toBe(true);
    expect(readInlineInfo()).toBe(true);
  });
});

describe('engineInlineInfo', () => {
  afterEach(reset);

  it.each<[string, { user?: boolean; workspace?: boolean }, Record<string, unknown>[], boolean]>([
    ['nothing set', {}, [], true],
    ['the user off, with no folder', { user: false }, [], false],
    ['the user off and every folder silent', { user: false }, [{}, {}], false],
    ['the user off and one folder on', { user: false }, [{}, { [NAME]: true }], true],
    ['the workspace off over the user on', { user: true, workspace: false }, [{}, {}], false],
    [
      'the workspace on and both folders off, for a file outside them',
      { workspace: true },
      [{ [NAME]: false }, { [NAME]: false }],
      true,
    ],
  ])(
    'is on while the window or a workspace folder shows the lines: %s',
    (_name, scopes, folders, expected) => {
      if (scopes.user !== undefined) {
        settings.set(NAME, scopes.user);
      }
      if (scopes.workspace !== undefined) {
        workspaceSettings.set(NAME, scopes.workspace);
      }
      for (const [index, folderSettings] of folders.entries()) {
        addWorkspaceFolder(`f${String(index)}`, folderSettings);
      }

      expect(engineInlineInfo()).toBe(expected);
    },
  );
});

describe('toggleInlineInfo', () => {
  afterEach(reset);

  it('turns the lines off in the user settings when no scope sets them', async () => {
    await toggleInlineInfo(log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: false, target: ConfigurationTarget.Global },
    ]);
  });

  it('turns the lines back on in the user settings that turned them off', async () => {
    settings.set(NAME, false);

    await toggleInlineInfo(log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: true, target: ConfigurationTarget.Global },
    ]);
  });

  it('flips the workspace setting when the workspace sets it, and leaves the user one', async () => {
    settings.set(NAME, true);
    workspaceSettings.set(NAME, false);

    await toggleInlineInfo(log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: true, target: ConfigurationTarget.Workspace },
    ]);
  });

  it("flips the setting of the active file's workspace folder when that folder sets it", async () => {
    workspaceSettings.set(NAME, false);
    addWorkspaceFolder('a', { [NAME]: true });
    addWorkspaceFolder('b');
    activeEditor.uri = 'file:///workspace/a/crd.yaml';
    changeActiveEditor('yaml');

    await toggleInlineInfo(log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: false, target: ConfigurationTarget.WorkspaceFolder, folder: 'a' },
    ]);
  });

  it('flips the workspace setting from a file in a folder that does not set it', async () => {
    workspaceSettings.set(NAME, false);
    addWorkspaceFolder('a', { [NAME]: true });
    addWorkspaceFolder('b');
    activeEditor.uri = 'file:///workspace/b/crd.yaml';
    changeActiveEditor('yaml');

    await toggleInlineInfo(log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: true, target: ConfigurationTarget.Workspace, folder: 'b' },
    ]);
  });

  it('says once that the setting could not be written, logs why, and opens it from the button', async () => {
    const channel = log();
    updateFailure.error = new Error('Unable to write into user settings');
    errorChoice.next = 'Open Settings';

    await toggleInlineInfo(channel);

    expect(shownErrors).toEqual([
      {
        message: `Could not change ${NAME}. Fix any errors in settings.json, then toggle it again.`,
        buttons: ['Open Settings'],
      },
    ]);
    expect(channel.lines).toEqual([
      `Could not change ${NAME}: Error: Unable to write into user settings`,
    ]);
    expect(executedCommands).toEqual([{ command: 'workbench.action.openSettings', args: [NAME] }]);
  });
});
