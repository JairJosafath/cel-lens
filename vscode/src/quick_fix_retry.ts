/** What the editor shows after the quick fix keys: a fix row, or its "No code actions available". */
export type QuickFixAnswer = 'fix' | 'empty' | 'none';

/** The part of the editor that `askUntilFixListed` drives; the picture script backs it with Playwright. */
export interface QuickFixEditor {
  /** Presses Ctrl+. */
  askForFixes: () => Promise<void>;
  /** Waits up to `timeout` ms for a fix row or the empty answer; 'none' when neither came. */
  waitForAnswer: (timeout: number) => Promise<QuickFixAnswer>;
  /** Presses Escape. */
  dismiss: () => Promise<void>;
  /** Waits up to `timeout` ms for the empty answer to leave the page; false when it stayed. */
  waitForEmptyAnswerGone: (timeout: number) => Promise<boolean>;
}

const NO_FIX_MESSAGE = 'the editor listed no fix before the timeout';
const ANSWER_STAYED_MESSAGE = 'the editor listed no fix: the empty answer stayed after Escape';

/**
 * Asks for the quick fixes and, each time the editor answers that there are none, dismisses the
 * answer, waits for it to go away and asks again, until a fix row is listed. All asks and waits
 * share one `timeout` in ms, measured with `now`; it throws when no fix row is listed within it,
 * or when the empty answer stays.
 */
export async function askUntilFixListed(
  editor: QuickFixEditor,
  timeout: number,
  now: () => number,
): Promise<void> {
  const deadline = now() + timeout;
  for (;;) {
    if (deadline - now() <= 0) {
      throw new Error(NO_FIX_MESSAGE);
    }
    await editor.askForFixes();
    const left = deadline - now();
    // A timeout of 0 means no timeout in the Playwright backing, so never wait with it.
    if (left <= 0) {
      throw new Error(NO_FIX_MESSAGE);
    }
    switch (await editor.waitForAnswer(left)) {
      case 'fix':
        return;
      case 'empty': {
        await editor.dismiss();
        const goneLeft = deadline - now();
        if (goneLeft <= 0 || !(await editor.waitForEmptyAnswerGone(goneLeft))) {
          throw new Error(ANSWER_STAYED_MESSAGE);
        }
        break;
      }
      case 'none':
        throw new Error(NO_FIX_MESSAGE);
    }
  }
}
