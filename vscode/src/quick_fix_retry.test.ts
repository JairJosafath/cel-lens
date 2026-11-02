import { describe, expect, it } from 'vitest';

import { askUntilFixListed, type QuickFixAnswer, type QuickFixEditor } from './quick_fix_retry.ts';

/** A fake editor that gives `answers` in turn, then `last`; its clock moves by `step` per wait. */
function fakeEditor(
  answers: readonly QuickFixAnswer[],
  last: QuickFixAnswer,
  step: number,
  goes = true,
  askStep = 0,
) {
  const log: string[] = [];
  let clock = 0;
  let asked = 0;
  const editor: QuickFixEditor = {
    askForFixes: () => {
      clock += askStep;
      log.push('Control+.');
      return Promise.resolve();
    },
    waitForAnswer: (timeout) => {
      clock += step;
      log.push(`wait ${String(timeout)}`);
      const answer = answers[asked] ?? last;
      asked += 1;
      return Promise.resolve(answer);
    },
    dismiss: () => {
      log.push('Escape');
      return Promise.resolve();
    },
    waitForEmptyAnswerGone: (timeout) => {
      log.push(`gone ${String(timeout)}`);
      if (!goes) {
        clock += timeout;
      }
      return Promise.resolve(goes);
    },
  };
  return { editor, log, now: () => clock };
}

describe('askUntilFixListed', () => {
  it('presses Control+. once when the first answer lists a fix', async () => {
    const { editor, log, now } = fakeEditor(['fix'], 'fix', 10);
    await askUntilFixListed(editor, 100, now);
    expect(log).toEqual(['Control+.', 'wait 100']);
  });

  it('presses Escape once and Control+. again after one empty answer', async () => {
    const { editor, log, now } = fakeEditor(['empty', 'fix'], 'fix', 10);
    await askUntilFixListed(editor, 100, now);
    expect(log).toEqual(['Control+.', 'wait 100', 'Escape', 'gone 90', 'Control+.', 'wait 90']);
  });

  it('fails after the timeout when the editor never lists a fix', async () => {
    const { editor, log, now } = fakeEditor([], 'empty', 40);
    await expect(askUntilFixListed(editor, 100, now)).rejects.toThrow(/no fix/);
    expect(log.filter((entry) => entry === 'Control+.')).toHaveLength(3);
  });

  it('gives up without pressing again when the empty answer never goes away', async () => {
    const { editor, log, now } = fakeEditor([], 'empty', 10, false);
    await expect(askUntilFixListed(editor, 100, now)).rejects.toThrow(
      'the editor listed no fix: the empty answer stayed after Escape',
    );
    expect(log).toEqual(['Control+.', 'wait 100', 'Escape', 'gone 90']);
  });

  it('gives up without waiting for the empty answer when no time is left after Escape', async () => {
    const { editor, log, now } = fakeEditor([], 'empty', 100);
    await expect(askUntilFixListed(editor, 100, now)).rejects.toThrow(/empty answer stayed/);
    expect(log).toEqual(['Control+.', 'wait 100', 'Escape']);
  });

  it('gives up without a wait when the clock passes the deadline between ask and wait', async () => {
    const { editor, log, now } = fakeEditor([], 'fix', 10, true, 100);
    await expect(askUntilFixListed(editor, 100, now)).rejects.toThrow(/no fix/);
    expect(log).toEqual(['Control+.']);
  });

  it('fails when a wait runs out with neither answer', async () => {
    const { editor, now } = fakeEditor([], 'none', 10);
    await expect(askUntilFixListed(editor, 100, now)).rejects.toThrow(/no fix/);
  });
});
