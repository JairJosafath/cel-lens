import { afterEach, describe, expect, it } from 'vitest';

import {
  addWorkspaceFolder,
  ConfigurationTarget,
  errorChoice,
  executedCommands,
  infoChoice,
  memento,
  reset,
  settingUpdates,
  settings,
  shownErrors,
  shownInfos,
  updateFailure,
  workspaceSettings,
} from '../test/vscode';
import { moveLegacyColorStyle, offerLegacyColorStyleMove } from './legacy_color_style';

function fakeLog(): { errors: string[]; error(message: string): void } {
  const errors: string[] = [];
  return {
    errors,
    error(message: string) {
      errors.push(message);
    },
  };
}

describe('offerLegacyColorStyleMove', () => {
  afterEach(reset);

  it('tells the user once how to move the old key, with a button that does it', async () => {
    settings.set('celLens.colorStyle', 'off');
    infoChoice.next = 'Move Setting';

    await offerLegacyColorStyleMove(memento(), fakeLog());

    expect(shownInfos).toHaveLength(1);
    expect(shownInfos[0]).toContain('cel-lens.colorStyle');
    expect(settingUpdates).toEqual([
      { name: 'cel-lens.colorStyle', value: 'off', target: ConfigurationTarget.Global },
      { name: 'celLens.colorStyle', value: undefined, target: ConfigurationTarget.Global },
    ]);
  });

  it('offers the move when the old key is set only at workspace-folder scope', async () => {
    addWorkspaceFolder('api', { 'celLens.colorStyle': 'off' });

    await offerLegacyColorStyleMove(memento(), fakeLog());

    expect(shownInfos).toHaveLength(1);
  });

  it('stays silent when a folder already sets the new key', async () => {
    addWorkspaceFolder('api', { 'celLens.colorStyle': 'off' });
    addWorkspaceFolder('web', { 'cel-lens.colorStyle': 'classic' });

    await offerLegacyColorStyleMove(memento(), fakeLog());

    expect(shownInfos).toEqual([]);
  });

  it('shows the hint once across starts that share the same memento', async () => {
    settings.set('celLens.colorStyle', 'off');
    const state = memento();

    await offerLegacyColorStyleMove(state, fakeLog());
    await offerLegacyColorStyleMove(state, fakeLog());

    expect(shownInfos).toHaveLength(1);
  });

  it('logs and shows one error with Open Settings when the move fails', async () => {
    settings.set('celLens.colorStyle', 'off');
    infoChoice.next = 'Move Setting';
    errorChoice.next = 'Open Settings';
    updateFailure.error = new Error('settings.json has a syntax error');
    const log = fakeLog();

    await offerLegacyColorStyleMove(memento(), log);

    expect(log.errors).toHaveLength(1);
    expect(log.errors[0]).toContain('syntax error');
    expect(shownErrors).toHaveLength(1);
    expect(shownErrors[0]?.buttons).toEqual(['Open Settings']);
    expect(executedCommands).toEqual([
      { command: 'workbench.action.openSettings', args: ['cel-lens.colorStyle'] },
    ]);
  });

  it('changes nothing when the user dismisses the message', async () => {
    settings.set('celLens.colorStyle', 'off');

    await offerLegacyColorStyleMove(memento(), fakeLog());

    expect(shownInfos).toHaveLength(1);
    expect(settingUpdates).toEqual([]);
  });

  it.each([
    ['neither key is set', []],
    ['only the new key is set', [['cel-lens.colorStyle', 'off']]],
    [
      'both keys are set',
      [
        ['celLens.colorStyle', 'off'],
        ['cel-lens.colorStyle', 'classic'],
      ],
    ],
  ] as const)('stays silent when %s', async (_name, entries) => {
    for (const [key, value] of entries) {
      settings.set(key, value);
    }

    await offerLegacyColorStyleMove(memento(), fakeLog());

    expect(shownInfos).toEqual([]);
  });
});

describe('moveLegacyColorStyle', () => {
  afterEach(reset);

  it.each([
    ['user', settings, ConfigurationTarget.Global],
    ['workspace', workspaceSettings, ConfigurationTarget.Workspace],
  ] as const)(
    'writes cel-lens.colorStyle and removes celLens.colorStyle at %s scope',
    async (_name, store, target) => {
      store.set('celLens.colorStyle', 'classic');

      await moveLegacyColorStyle();

      expect(settingUpdates).toEqual([
        { name: 'cel-lens.colorStyle', value: 'classic', target },
        { name: 'celLens.colorStyle', value: undefined, target },
      ]);
    },
  );

  it('writes cel-lens.colorStyle and removes celLens.colorStyle at WorkspaceFolder scope', async () => {
    addWorkspaceFolder('api', { 'celLens.colorStyle': 'classic' });

    await moveLegacyColorStyle();

    expect(settingUpdates).toEqual([
      {
        name: 'cel-lens.colorStyle',
        value: 'classic',
        target: ConfigurationTarget.WorkspaceFolder,
        folder: 'api',
      },
      {
        name: 'celLens.colorStyle',
        value: undefined,
        target: ConfigurationTarget.WorkspaceFolder,
        folder: 'api',
      },
    ]);
  });

  it('moves only the folder that sets the old key in a multi-root workspace', async () => {
    addWorkspaceFolder('api');
    addWorkspaceFolder('web', { 'celLens.colorStyle': 'off' });
    addWorkspaceFolder('docs', { 'cel-lens.colorStyle': 'classic' });

    await moveLegacyColorStyle();

    expect(settingUpdates.map((u) => [u.name, u.value, u.folder])).toEqual([
      ['cel-lens.colorStyle', 'off', 'web'],
      ['celLens.colorStyle', undefined, 'web'],
    ]);
  });

  it('moves both scopes when both are set', async () => {
    settings.set('celLens.colorStyle', 'off');
    workspaceSettings.set('celLens.colorStyle', 'classic');

    await moveLegacyColorStyle();

    expect(settingUpdates.map((u) => [u.name, u.value, u.target])).toEqual([
      ['cel-lens.colorStyle', 'off', ConfigurationTarget.Global],
      ['celLens.colorStyle', undefined, ConfigurationTarget.Global],
      ['cel-lens.colorStyle', 'classic', ConfigurationTarget.Workspace],
      ['celLens.colorStyle', undefined, ConfigurationTarget.Workspace],
    ]);
  });
});
