import { afterEach, describe, expect, it } from 'vitest';

import { env, executedCommands, reset } from '../test/vscode';
import { openServerPathSetting } from './open_server_setting';

describe('openServerPathSetting', () => {
  afterEach(reset);

  const setting = 'cel-lens.serverPath';
  const remoteCommand = 'workbench.action.openRemoteSettings';

  it.each([
    { remoteName: undefined, command: 'workbench.action.openSettings', arg: setting },
    { remoteName: 'wsl', command: remoteCommand, arg: { query: setting } },
    { remoteName: 'ssh-remote', command: remoteCommand, arg: { query: setting } },
    { remoteName: 'dev-container', command: remoteCommand, arg: { query: setting } },
  ])('runs $command filtered to the setting when remoteName is $remoteName', async (row) => {
    env.remoteName = row.remoteName;

    await openServerPathSetting();

    expect(executedCommands).toEqual([{ command: row.command, args: [row.arg] }]);
  });
});
