import { afterEach, describe, expect, it } from 'vitest';

import {
  ConfigurationTarget,
  activeEditor,
  addWorkspaceFolder,
  changeActiveEditor,
  errorChoice,
  executedCommands,
  memento,
  reset,
  settingUpdates,
  settings,
  shownErrors,
  updateFailure,
  window,
  workspaceSettings,
} from '../test/vscode';
import { LAST_COLOR_STYLE_KEY, toggleColors, toggledStyle } from './toggle_colors';

const NAME = 'cel-lens.colorStyle';
const LEGACY = 'celLens.colorStyle';

function log(): ReturnType<typeof window.createOutputChannel> {
  return window.createOutputChannel('cel-lens');
}

describe('toggledStyle', () => {
  it.each([
    ['cel', undefined, 'off'],
    ['classic', undefined, 'off'],
    ['off', 'classic', 'classic'],
    ['off', 'cel', 'cel'],
    ['off', undefined, 'cel'],
    ['off', 'off', 'cel'],
    ['off', 'rainbow', 'cel'],
  ] as const)('with %s in force and %s kept gives %s', (current, kept, expected) => {
    expect(toggledStyle(current, kept)).toBe(expected);
  });
});

describe('toggleColors', () => {
  afterEach(reset);

  it('turns the colors off in the user settings when no scope sets a style, and keeps cel', async () => {
    const state = memento();

    await toggleColors(state, log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: 'off', target: ConfigurationTarget.Global },
    ]);
    expect(state.get(LAST_COLOR_STYLE_KEY)).toBe('cel');
  });

  it('turns classic off, and back to classic, not to cel', async () => {
    const state = memento();
    settings.set(NAME, 'classic');

    await toggleColors(state, log());
    settings.set(NAME, 'off');
    await toggleColors(state, log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: 'off', target: ConfigurationTarget.Global },
      { name: NAME, value: 'classic', target: ConfigurationTarget.Global },
    ]);
  });

  it('turns the colors back on to cel when it kept no style', async () => {
    settings.set(NAME, 'off');

    await toggleColors(memento(), log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: 'cel', target: ConfigurationTarget.Global },
    ]);
  });

  it('writes the workspace setting when the workspace sets the style in force', async () => {
    settings.set(NAME, 'cel');
    workspaceSettings.set(NAME, 'classic');

    await toggleColors(memento(), log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: 'off', target: ConfigurationTarget.Workspace },
    ]);
  });

  it("writes the setting of the active file's folder when that folder sets it", async () => {
    workspaceSettings.set(NAME, 'cel');
    addWorkspaceFolder('a', { [NAME]: 'classic' });
    addWorkspaceFolder('b');
    activeEditor.uri = 'file:///workspace/a/crd.yaml';
    changeActiveEditor('yaml');
    const state = memento();

    await toggleColors(state, log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: 'off', target: ConfigurationTarget.WorkspaceFolder, folder: 'a' },
    ]);
    expect(state.get(LAST_COLOR_STYLE_KEY)).toBe('classic');
  });

  it('reads a style set only under the old celLens.colorStyle and writes the new key at its scope', async () => {
    const state = memento();
    workspaceSettings.set(LEGACY, 'classic');

    await toggleColors(state, log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: 'off', target: ConfigurationTarget.Workspace },
    ]);
    expect(state.get(LAST_COLOR_STYLE_KEY)).toBe('classic');
  });

  it('reads an old celLens.colorStyle of off as off, and turns the colors on', async () => {
    settings.set(LEGACY, 'off');

    await toggleColors(memento(), log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: 'cel', target: ConfigurationTarget.Global },
    ]);
  });

  it('says once that the setting could not be written, logs why, and opens it from the button', async () => {
    const channel = log();
    updateFailure.error = new Error('Unable to write into user settings');
    errorChoice.next = 'Open Settings';

    await toggleColors(memento(), channel);

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
