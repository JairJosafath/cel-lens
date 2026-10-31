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
}

const NO_FIX_MESSAGE = 'the editor listed no fix before the timeout';

/**
 * Asks for the quick fixes and, each time the editor answers that there are none, dismisses the
 * answer and asks again, until a fix row is listed. All asks share one `timeout` in ms, measured
 * with `now`; it throws when no fix row is listed within it.
 */
export async function askUntilFixListed(
  editor: QuickFixEditor,
  timeout: number,
  now: () => number,
): Promise<void> {
  const deadline = now() + timeout;
  for (;;) {
    const remaining = deadline - now();
    if (remaining <= 0) {
      throw new Error(NO_FIX_MESSAGE);
    }
    await editor.askForFixes();
    switch (await editor.waitForAnswer(remaining)) {
      case 'fix':
        return;
      case 'empty':
        await editor.dismiss();
        break;
      case 'none':
        throw new Error(NO_FIX_MESSAGE);
    }
  }
}
