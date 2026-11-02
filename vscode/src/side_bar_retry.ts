/**
 * What the page shows after the command was typed: the command row, the bar off screen, or the
 * palette closed with the bar still on screen. 'none' when no outcome came before the timeout.
 */
export type SideBarOutcome = 'listed' | 'hidden' | 'closed' | 'none';

/** How `hideSideBar` ended: the bar hid with the command, or by itself before the palette listed it. */
export type SideBarResult = 'command' | 'by itself';

/** The part of the page that `hideSideBarWithRetry` drives; the picture script backs it with Playwright. */
export interface SideBarPage {
  /** Presses F1 and waits up to `timeout` ms for the palette to open; throws when it did not. */
  openPalette: (timeout: number) => Promise<void>;
  /** Types the command title into the palette. */
  typeCommand: () => Promise<void>;
  /** Waits up to `timeout` ms for one of the three outcomes; 'none' when none came. */
  waitForOutcome: (timeout: number) => Promise<SideBarOutcome>;
  /** Presses Enter on the listed command. */
  runCommand: () => Promise<void>;
  /** Presses Escape. */
  dismiss: () => Promise<void>;
}

/**
 * Opens the palette and types the command; each time the palette closes while the bar still shows,
 * opens it and types the command again, until the row is listed (Enter) or the bar hid by itself
 * (Escape). All opens, types and waits share one `timeout` in ms, measured with `now`; it throws
 * naming `step` when no time is left: the palette kept closing, or listed neither the command
 * nor the bar hid.
 */
export async function hideSideBarWithRetry(
  page: SideBarPage,
  step: string,
  timeout: number,
  now: () => number,
): Promise<SideBarResult> {
  const deadline = now() + timeout;
  let closed = false;
  const giveUp = () =>
    new Error(
      closed
        ? `${step}: the palette kept closing before it listed the command`
        : `${step}: the palette listed neither the command nor the bar hid`,
    );
  for (;;) {
    const remaining = deadline - now();
    if (remaining <= 0) {
      throw giveUp();
    }
    await page.openPalette(remaining);
    await page.typeCommand();
    const left = deadline - now();
    // A timeout of 0 means no timeout in the Playwright backing, so never wait with it.
    if (left <= 0) {
      throw giveUp();
    }
    switch (await page.waitForOutcome(left)) {
      case 'listed':
        await page.runCommand();
        return 'command';
      case 'hidden':
        await page.dismiss();
        return 'by itself';
      case 'closed':
        closed = true;
        break;
      case 'none':
        throw giveUp();
    }
  }
}
