import { describe, expect, it } from 'vitest';

import { askUntilFixListed, type QuickFixAnswer, type QuickFixEditor } from './quick_fix_retry.ts';

/** A fake editor that gives `answers` in turn, then `last`; its clock moves by `step` per wait. */
function fakeEditor(answers: readonly QuickFixAnswer[], last: QuickFixAnswer, step: number) {
  const log: string[] = [];
  let clock = 0;
  let asked = 0;
  const editor: QuickFixEditor = {
    askForFixes: () => {
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
    expect(log).toEqual(['Control+.', 'wait 100', 'Escape', 'Control+.', 'wait 90']);
  });

  it('fails after the timeout when the editor never lists a fix', async () => {
    const { editor, log, now } = fakeEditor([], 'empty', 40);
    await expect(askUntilFixListed(editor, 100, now)).rejects.toThrow(/no fix/);
    expect(log.filter((entry) => entry === 'Control+.')).toHaveLength(3);
  });

  it('fails when a wait runs out with neither answer', async () => {
    const { editor, now } = fakeEditor([], 'none', 10);
    await expect(askUntilFixListed(editor, 100, now)).rejects.toThrow(/no fix/);
  });
});
