import { afterEach, describe, expect, it } from 'vitest';

import {
  ConfigurationTarget,
  Uri,
  addWorkspaceFolder,
  reset,
  settingUpdates,
  settings,
  window,
  workspaceSettings,
} from '../test/vscode';
import { readValueTypes, toggleValueTypes } from './value_visibility_setting';

const NAME = 'cel-lens.valueTypes';

function log(): ReturnType<typeof window.createOutputChannel> {
  return window.createOutputChannel('cel-lens');
}

describe('readValueTypes', () => {
  afterEach(reset);

  it.each([
    ['set nowhere', undefined, true],
    ['off in the user settings', false, false],
  ])('is on unless turned off: %s', (_name, user, expected) => {
    if (user !== undefined) {
      settings.set(NAME, user);
    }

    expect(readValueTypes()).toBe(expected);
  });

  it("reads a file's workspace folder setting over the workspace's", () => {
    workspaceSettings.set(NAME, true);
    addWorkspaceFolder('a', { [NAME]: false });
    addWorkspaceFolder('b');

    expect(readValueTypes(Uri.parse('file:///workspace/a/policy.yaml'))).toBe(false);
    expect(readValueTypes(Uri.parse('file:///workspace/b/policy.yaml'))).toBe(true);
  });
});

describe('toggleValueTypes', () => {
  afterEach(reset);

  it('hides the values in the user settings when no scope sets them', async () => {
    await toggleValueTypes(log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: false, target: ConfigurationTarget.Global },
    ]);
  });

  it('shows the values again at the workspace that hid them', async () => {
    workspaceSettings.set(NAME, false);

    await toggleValueTypes(log());

    expect(settingUpdates).toEqual([
      { name: NAME, value: true, target: ConfigurationTarget.Workspace },
    ]);
  });
});
