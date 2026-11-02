import { describe, expect, it } from 'vitest';

import { hideSideBarWithRetry, type SideBarOutcome, type SideBarPage } from './side_bar_retry.ts';

/** A fake page that gives `outcomes` in turn, then `last`; its clock moves by `step` per wait. */
function fakePage(
  outcomes: readonly SideBarOutcome[],
  last: SideBarOutcome,
  step: number,
  openStep = 0,
) {
  const log: string[] = [];
  let clock = 0;
  let waited = 0;
  const page: SideBarPage = {
    openPalette: (timeout) => {
      clock += openStep;
      log.push(`F1 ${String(timeout)}`);
      return Promise.resolve();
    },
    typeCommand: () => {
      log.push('type');
      return Promise.resolve();
    },
    waitForOutcome: (timeout) => {
      clock += step;
      log.push(`wait ${String(timeout)}`);
      const outcome = outcomes[waited] ?? last;
      waited += 1;
      return Promise.resolve(outcome);
    },
    runCommand: () => {
      log.push('Enter');
      return Promise.resolve();
    },
    dismiss: () => {
      log.push('Escape');
      return Promise.resolve();
    },
  };
  return { page, log, now: () => clock };
}

describe('hideSideBarWithRetry', () => {
  it('presses Enter and never reopens when the command is listed at once', async () => {
    const { page, log, now } = fakePage(['listed'], 'listed', 10);
    await expect(hideSideBarWithRetry(page, 'the step', 100, now)).resolves.toBe('command');
    expect(log).toEqual(['F1 100', 'type', 'wait 100', 'Enter']);
  });

  it('presses F1 and types again after the palette closed once, then Enter', async () => {
    const { page, log, now } = fakePage(['closed', 'listed'], 'listed', 10);
    await hideSideBarWithRetry(page, 'the step', 100, now);
    expect(log).toEqual(['F1 100', 'type', 'wait 100', 'F1 90', 'type', 'wait 90', 'Enter']);
  });

  it('presses Escape and never reopens when the bar hid by itself', async () => {
    const { page, log, now } = fakePage(['hidden'], 'hidden', 10);
    await expect(hideSideBarWithRetry(page, 'the step', 100, now)).resolves.toBe('by itself');
    expect(log).toEqual(['F1 100', 'type', 'wait 100', 'Escape']);
  });

  it('gives up at the deadline with the step name when the palette always closes', async () => {
    const { page, log, now } = fakePage([], 'closed', 40);
    await expect(hideSideBarWithRetry(page, 'the step', 100, now)).rejects.toThrow(
      /the step: the palette kept closing before it listed the command/,
    );
    expect(log.filter((entry) => entry.startsWith('F1'))).toHaveLength(3);
  });

  it('says the palette listed neither the command nor the bar hid when no outcome came', async () => {
    const { page, now } = fakePage([], 'none', 10);
    await expect(hideSideBarWithRetry(page, 'the step', 100, now)).rejects.toThrow(
      'the step: the palette listed neither the command nor the bar hid',
    );
  });

  it('gives up without a wait when the clock passes the deadline between open and wait', async () => {
    const { page, log, now } = fakePage([], 'closed', 10, 100);
    await expect(hideSideBarWithRetry(page, 'the step', 100, now)).rejects.toThrow(/the step: /);
    expect(log).toEqual(['F1 100', 'type']);
  });
});
