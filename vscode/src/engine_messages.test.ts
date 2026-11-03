import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  executedCommands,
  reset,
  shownErrors,
  shownInfos,
  shownWarnings,
  warningChoice,
} from '../test/vscode';
import { SHOW_LOGS_COMMAND } from './actions_menu';
import { EngineMessages } from './engine_messages';

const CAP =
  'cel-lens stopped scanning the workspace for CustomResourceDefinitions after 5000 YAML files; CRDs in the files left out are not known, so a params type may stay dyn';

function newMessages(): EngineMessages {
  const log = { error: vi.fn() } as unknown as ConstructorParameters<typeof EngineMessages>[0];
  return new EngineMessages(log);
}

async function settle(): Promise<void> {
  for (let turn = 0; turn < 10; turn += 1) {
    await Promise.resolve();
  }
}

describe('EngineMessages', () => {
  beforeEach(() => {
    reset();
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
});
