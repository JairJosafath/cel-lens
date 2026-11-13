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
import {
  engineExampleResults,
  readExampleResults,
  toggleExampleResults,
} from './example_results_setting';

const NAME = 'cel-lens.exampleResults';

function log(): ReturnType<typeof window.createOutputChannel> {
  return window.createOutputChannel('cel-lens');
}

describe('readExampleResults', () => {
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

    expect(readExampleResults()).toBe(expected);
  });

  it("reads a file's workspace folder setting over the workspace's", () => {
    workspaceSettings.set(NAME, true);
    addWorkspaceFolder('a', { [NAME]: false });
    addWorkspaceFolder('b');

    expect(readExampleResults(Uri.parse('file:///workspace/a/crd.yaml'))).toBe(false);
    expect(readExampleResults(Uri.parse('file:///workspace/b/crd.yaml'))).toBe(true);
    expect(readExampleResults()).toBe(true);
  });
});

describe('engineExampleResults', () => {
  afterEach(reset);

  it.each<[string, { user?: boolean; workspace?: boolean }, Record<string, unknown>[], boolean]>([
    ['nothing set', {}, [], true],
    ['the user off, with no folder', { user: false }, [], false],
    ['the user off and every folder silent', { user: false }, [{}, {}], false],
    ['the user off and one folder on', { user: false }, [{}, { [NAME]: true }], true],
    ['the workspace off over the user on', { user: true, workspace: false }, [{}, {}], false],
  ])(
    'is on while the window or a workspace folder shows the results: %s',
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

      expect(engineExampleResults()).toBe(expected);
    },
  );
});

describe('toggleExampleResults', () => {
  afterEach(reset);

  it('turns the results off in the user settings when no scope sets them', async () => {
    await toggleExampleResults(log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: false, target: ConfigurationTarget.Global },
    ]);
  });

  it('turns the results back on in the user settings that turned them off', async () => {
    settings.set(NAME, false);

    await toggleExampleResults(log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: true, target: ConfigurationTarget.Global },
    ]);
  });

  it('flips the workspace setting when the workspace sets it, and leaves the user one', async () => {
    settings.set(NAME, true);
    workspaceSettings.set(NAME, false);

    await toggleExampleResults(log());

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

    await toggleExampleResults(log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: false, target: ConfigurationTarget.WorkspaceFolder, folder: 'a' },
    ]);
  });

  it('flips the example results alone, never the inline info', async () => {
    settings.set('cel-lens.inlineInfo', false);

    await toggleExampleResults(log());

    expect(settingUpdates.map((update) => update.name)).toEqual([NAME]);
  });

  it('says once that the setting could not be written, logs why, and opens it from the button', async () => {
    const channel = log();
    updateFailure.error = new Error('Unable to write into user settings');
    errorChoice.next = 'Open Settings';

    await toggleExampleResults(channel);

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
