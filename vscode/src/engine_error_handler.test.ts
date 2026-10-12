import { beforeEach, describe, expect, it, vi } from 'vitest';

import { errorChoice, executedCommands, reset, shownErrors } from '../test/vscode';
import { SHOW_LOGS_COMMAND } from './actions_menu';
import { EngineErrorHandler, connectionErrorMessage, giveUpMessage } from './engine_error_handler';
import { RESTART_SERVER_COMMAND } from './kubernetes_version';

vi.mock('vscode-languageclient/node', () => ({
  CloseAction: { DoNotRestart: 1, Restart: 2 },
  ErrorAction: { Continue: 1, Shutdown: 2 },
}));

const RESTART = 2;
const DO_NOT_RESTART = 1;
const MINUTE = 60_000;

function newHandler(clock: { now: number }): EngineErrorHandler {
  const log = { error: vi.fn() } as unknown as ConstructorParameters<typeof EngineErrorHandler>[0];
  return new EngineErrorHandler(log, () => clock.now);
}

describe('EngineErrorHandler', () => {
  beforeEach(() => {
    reset();
  });

  it.each([
    { crashes: 1, actions: [RESTART] },
    { crashes: 4, actions: [RESTART, RESTART, RESTART, RESTART] },
    { crashes: 5, actions: [RESTART, RESTART, RESTART, RESTART, DO_NOT_RESTART] },
  ])('after $crashes quick crashes it answers $actions', ({ crashes, actions }) => {
    const handler = newHandler({ now: 0 });

    const results = Array.from({ length: crashes }, () => handler.closed().action);

    expect(results).toEqual(actions);
  });

  it('keeps restarting when the crashes are spread over more than 3 minutes', () => {
    const clock = { now: 0 };
    const handler = newHandler(clock);

    const results = Array.from({ length: 8 }, () => {
      clock.now += MINUTE;
      return handler.closed().action;
    });

    expect(results).toEqual(Array.from({ length: 8 }, () => RESTART));
  });

  it('shows no message before it gives up', () => {
    const handler = newHandler({ now: 0 });

    for (let i = 0; i < 4; i++) {
      handler.closed();
    }

    expect(shownErrors).toEqual([]);
  });

  it('shows one message that says engine, never server, with both buttons, on the 5th crash', () => {
    const handler = newHandler({ now: 0 });

    const result = Array.from({ length: 5 }, () => handler.closed()).at(-1);

    expect(result).toEqual({ action: DO_NOT_RESTART, handled: true });
    expect(shownErrors).toEqual([
      {
        message:
          'The cel-lens engine crashed 5 times in the last 3 minutes and will not be restarted.',
        buttons: ['Restart Engine', 'Show Logs'],
      },
    ]);
    expect(giveUpMessage()).not.toMatch(/server/i);
  });

  it.each([
    { button: 'Restart Engine', command: RESTART_SERVER_COMMAND },
    { button: 'Show Logs', command: SHOW_LOGS_COMMAND },
  ])('runs $command when $button is chosen', async ({ button, command }) => {
    errorChoice.next = button;
    const handler = newHandler({ now: 0 });

    for (let i = 0; i < 5; i++) {
      handler.closed();
    }

    await vi.waitFor(() => {
      expect(executedCommands).toEqual([{ command, args: [] }]);
    });
  });

  it('does nothing when the message is dismissed', async () => {
    const handler = newHandler({ now: 0 });

    for (let i = 0; i < 5; i++) {
      handler.closed();
    }
    await Promise.resolve();

    expect(executedCommands).toEqual([]);
  });

  it.each([
    { count: 1, action: 1 },
    { count: 3, action: 1 },
    { count: 4, action: 2 },
    { count: undefined, action: 2 },
  ])('answers $action to a message error with count $count', ({ count, action }) => {
    expect(newHandler({ now: 0 }).error(new Error('x'), undefined, count).action).toBe(action);
  });

  it('shows no message while message errors are within the limit', () => {
    const handler = newHandler({ now: 0 });

    for (const count of [1, 2, 3]) {
      handler.error(new Error('x'), undefined, count);
    }

    expect(shownErrors).toEqual([]);
  });

  it('shows once a message that says engine, never server, after more than 3 message errors', () => {
    const handler = newHandler({ now: 0 });

    const results = [4, 5, 6].map((count) => handler.error(new Error('x'), undefined, count));

    expect(results).toEqual(Array.from({ length: 3 }, () => ({ action: 2, handled: true })));
    expect(shownErrors).toEqual([
      { message: connectionErrorMessage(), buttons: ['Restart Engine', 'Show Logs'] },
    ]);
    expect(connectionErrorMessage()).not.toMatch(/server/i);
  });
});
