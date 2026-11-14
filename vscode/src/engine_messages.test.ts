import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  channels,
  commands,
  executedCommands,
  reset,
  settle,
  shownErrors,
  shownInfos,
  settingUpdates,
  settings,
  shownWarnings,
  updateFailure,
  warningChoice,
  workspaceSettings,
  window,
} from '../test/vscode';
import { SHOW_LOGS_COMMAND } from './actions_menu';
import { EngineMessages } from './engine_messages';

const CAP =
  'cel-lens stopped scanning the workspace for CustomResourceDefinitions after 5000 YAML files; CRDs in the files left out are not known, so a params type may stay dyn';

const log = (): string[] => channels.get('cel-lens')?.lines ?? [];

function newMessages(): EngineMessages {
  return new EngineMessages(window.createOutputChannel('cel-lens'));
}

describe('EngineMessages', () => {
  beforeEach(() => {
    reset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('shows the file-cap warning with a Show Logs button', () => {
    newMessages().show({ type: 2, message: CAP });

    expect(shownWarnings).toEqual([{ message: CAP, buttons: ['Show Logs'] }]);
  });

  it('runs cel-lens.showLogs when the user presses Show Logs', async () => {
    warningChoice.next = 'Show Logs';

    newMessages().show({ type: 2, message: CAP });
    await settle();

    expect(executedCommands).toContainEqual({ command: SHOW_LOGS_COMMAND, args: [] });
  });

  it('logs that it could not follow the Show Logs button when the command rejects', async () => {
    warningChoice.next = 'Show Logs';
    vi.spyOn(commands, 'executeCommand').mockRejectedValue(new Error('no such command'));

    newMessages().show({ type: 2, message: CAP });
    await settle();

    expect(log()).toContain('Could not follow the message button: Error: no such command');
  });

  it('runs nothing when the user dismisses the warning', async () => {
    newMessages().show({ type: 2, message: CAP });
    await settle();

    expect(executedCommands).toEqual([]);
  });

  it('shows the same cap warning only once', () => {
    const messages = newMessages();

    messages.show({ type: 2, message: CAP });
    messages.show({ type: 2, message: CAP });

    expect(shownWarnings).toHaveLength(1);
  });

  it('writes the cap warning to the log once per shown warning', () => {
    const messages = newMessages();

    messages.show({ type: 2, message: CAP });
    messages.show({ type: 2, message: CAP });

    expect(log()).toEqual([CAP]);
  });

  it.each([
    { type: 1, shown: () => shownErrors.length },
    { type: 2, shown: () => shownWarnings.length },
    { type: 3, shown: () => shownInfos.length },
    { type: 4, shown: () => shownInfos.length },
  ])('shows another message of type $type as the client would, with no button', (row) => {
    newMessages().show({ type: row.type, message: 'Kubernetes versions: 1.30' });

    expect(row.shown()).toBe(1);
    expect(shownWarnings.flatMap((shown) => shown.buttons)).toEqual([]);
  });

  describe('a showMessageRequest with an action that turns on a feature', () => {
    const NOTICE = {
      type: 2,
      message:
        'cel-lens does not read deploy/.cellens: giving a value a type of your own in .cellens is the experimental feature cellens-types, which is off: add cellens-types to the experimental features (cel-lens.experimental in VS Code) to read it.',
      actions: [{ title: 'Turn on cellens-types', experimental: 'cellens-types' }],
    };

    it('shows the message with the action’s title as the button', async () => {
      await newMessages().request(NOTICE);

      expect(shownWarnings).toEqual([
        { message: NOTICE.message, buttons: ['Turn on cellens-types'] },
      ]);
    });

    it.each([
      ['no experimental feature is listed', undefined, undefined, ['cellens-types'], 1],
      ['the user scope lists others', ['kyverno'], undefined, ['kyverno', 'cellens-types'], 1],
      [
        'the workspace scope lists others',
        ['kyverno'],
        ['fitting-completion'],
        ['fitting-completion', 'cellens-types'],
        2,
      ],
    ])(
      'adds cellens-types to cel-lens.experimental at the scope that sets it when the button is pressed: %s',
      async (_what, user, workspace, written, target) => {
        if (user !== undefined) {
          settings.set('cel-lens.experimental', user);
        }
        if (workspace !== undefined) {
          workspaceSettings.set('cel-lens.experimental', workspace);
        }
        warningChoice.next = 'Turn on cellens-types';

        const answer = await newMessages().request(NOTICE);

        expect(answer).toEqual({ title: 'Turn on cellens-types' });
        expect(settingUpdates).toEqual([{ name: 'cel-lens.experimental', value: written, target }]);
      },
    );

    it.each([['kyverno'], ['crd-typed-policies'], ['fitting-completion']])(
      'turns on %s too, a name the setting offers',
      async (name) => {
        warningChoice.next = 'Turn it on';

        await newMessages().request({
          type: 2,
          message: 'x',
          actions: [{ title: 'Turn it on', experimental: name }],
        });

        expect(settingUpdates).toEqual([
          { name: 'cel-lens.experimental', value: [name], target: 1 },
        ]);
      },
    );

    it.each([
      ['a name the setting does not offer', 'telemetry'],
      ['a name with other casing', 'Cellens-Types'],
      ['a name with a space', 'cellens-types '],
      ['a path', '../../etc'],
    ])(
      'writes nothing for an action that names %s, and says so in the log',
      async (_what, name) => {
        warningChoice.next = 'Turn it on';

        const answer = await newMessages().request({
          type: 2,
          message: 'x',
          actions: [{ title: 'Turn it on', experimental: name }],
        });

        expect(answer).toEqual({ title: 'Turn it on' });
        expect(settingUpdates).toEqual([]);
        expect(log()).toEqual([
          `The engine asked to turn on "${name}", which cel-lens.experimental does not offer, so nothing was changed.`,
        ]);
      },
    );

    it('answers null and writes nothing when the message is dismissed', async () => {
      const answer = await newMessages().request(NOTICE);

      expect(answer).toBeNull();
      expect(settingUpdates).toEqual([]);
    });

    it('leaves a feature that is on alone: nothing is written twice', async () => {
      settings.set('cel-lens.experimental', ['cellens-types']);
      warningChoice.next = 'Turn on cellens-types';

      await newMessages().request(NOTICE);

      expect(settingUpdates).toEqual([]);
    });

    it('answers the action picked and changes no setting for an action that names no feature', async () => {
      warningChoice.next = 'Show it';

      const answer = await newMessages().request({
        type: 2,
        message: 'Something',
        actions: [{ title: 'Show it' }, { title: 'Other' }],
      });

      expect(answer).toEqual({ title: 'Show it' });
      expect(settingUpdates).toEqual([]);
    });

    it.each([
      ['no actions', undefined],
      ['a list that holds no action', ['x', 3, { experimental: 'a' }, { title: '' }]],
    ])('shows the message with no button for %s', async (_what, actions) => {
      const answer = await newMessages().request({ type: 2, message: 'Hello', actions });

      expect(shownWarnings).toEqual([{ message: 'Hello', buttons: [] }]);
      expect(answer).toBeNull();
    });

    it.each([
      { type: 1, shown: () => shownErrors.length },
      { type: 2, shown: () => shownWarnings.length },
      { type: 3, shown: () => shownInfos.length },
    ])('shows a message of type $type as the client would', async (row) => {
      await newMessages().request({ type: row.type, message: 'x', actions: [] });

      expect(row.shown()).toBe(1);
    });

    it('logs and shows how to do it by hand when the setting cannot be written', async () => {
      warningChoice.next = 'Turn on cellens-types';
      updateFailure.error = new Error('settings.json is not valid');

      const answer = await newMessages().request(NOTICE);

      expect(answer).toEqual({ title: 'Turn on cellens-types' });
      expect(log()).toEqual([
        'Could not turn on cellens-types in cel-lens.experimental: Error: settings.json is not valid',
      ]);
      expect(shownWarnings.map((shown) => shown.buttons)).toEqual([
        ['Turn on cellens-types'],
        ['Open Experimental Setting'],
      ]);
    });
  });
});
