// Tooling for test/pictures/take.ts; it lives in src/ only so vitest's include finds its test.
// The clock of a GIF is the script's pacing, not the wall clock: a virtual clock
// advances only while a pacing hold runs, so a slow machine cannot make a GIF longer.

/** A frame stamped with the virtual time it arrived at. */
export interface StampedFrame<Picture> {
  readonly picture: Picture;
  readonly at: number;
}

/** A virtual clock that advances only during a hold. Times are in milliseconds. */
export class VirtualClock {
  private time = 0;
  private hold: { readonly ms: number; readonly startedAt: number } | undefined;

  /** The virtual time now; during a hold, in proportion to how far the hold has come. */
  stamp(wallNow: number): number {
    if (this.hold === undefined) {
      return this.time;
    }
    const { ms, startedAt } = this.hold;
    const done = ms === 0 ? 1 : Math.min(1, Math.max(0, (wallNow - startedAt) / ms));
    return this.time + ms * done;
  }

  /** A hold of `ms` begins at wall-clock time `wallNow`. */
  beginHold(ms: number, wallNow: number): void {
    this.hold = { ms, startedAt: wallNow };
  }

  /** The running hold is over: the clock stands at its end. */
  endHold(): void {
    if (this.hold !== undefined) {
      this.time += this.hold.ms;
      this.hold = undefined;
    }
  }

  /** The virtual time the holds so far add up to. */
  get total(): number {
    return this.time;
  }
}

/**
 * The index into `frames` shown at each tick of `holds`' total length: one per `tickMs`,
 * the last frame that arrived at or before the tick (the last to arrive wins when
 * several stand at one instant), the first frame when none has arrived yet.
 * Empty when there are no frames.
 */
export function pickFrames(
  frames: readonly { readonly at: number }[],
  holds: readonly number[],
  tickMs: number,
): number[] {
  if (frames.length === 0) {
    return [];
  }
  const total = holds.reduce((sum, ms) => sum + ms, 0);
  const picked: number[] = [];
  let shown = 0;
  for (let tick = 0; tick < total; tick += tickMs) {
    while (shown + 1 < frames.length && (frames[shown + 1]?.at ?? Infinity) <= tick) {
      shown += 1;
    }
    picked.push(shown);
  }
  return picked;
}
