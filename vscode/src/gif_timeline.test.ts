import { describe, expect, it } from 'vitest';

import { pickFrames, VirtualClock } from './gif_timeline.ts';

const TICK = 100;

/** Runs steps of (hold ms, frames arriving during the hold at wall-clock fractions) with `slow` times the wall time. */
function record(slow: number): { at: number[]; holds: number[] } {
  const clock = new VirtualClock();
  const at: number[] = [];
  const holds: number[] = [];
  let wall = 0;
  for (const [ms, fractions] of [
    [300, [0.5]],
    [200, [0.25, 1]],
    [500, []],
  ] as const) {
    wall += 50 * slow; // a step between holds: the clock stands still
    at.push(clock.stamp(wall));
    clock.beginHold(ms, wall);
    for (const fraction of fractions) {
      at.push(clock.stamp(wall + ms * fraction));
    }
    wall += ms;
    clock.endHold();
    holds.push(ms);
  }
  return { at, holds };
}

describe('VirtualClock', () => {
  it.each([
    { name: 'before any hold', wall: 5, expected: 0 },
    { name: 'a quarter into a hold', wall: 1025, expected: 25 },
    { name: 'past the end of a hold', wall: 9000, expected: 100 },
  ])('stamps a frame $name at $expected', ({ wall, expected }) => {
    const clock = new VirtualClock();
    if (wall > 5) {
      clock.beginHold(100, 1000);
    }
    expect(clock.stamp(wall)).toBe(expected);
  });

  it('stands still between holds and totals the holds only', () => {
    const clock = new VirtualClock();
    clock.beginHold(300, 0);
    clock.endHold();
    expect(clock.stamp(99_999)).toBe(300);
    clock.beginHold(200, 100_000);
    clock.endHold();
    expect(clock.total).toBe(500);
  });
});

describe('pickFrames', () => {
  it('gives the same frames for ten times the wall-clock time between steps', () => {
    const fast = record(1);
    const slow = record(10);
    expect(slow.at).toEqual(fast.at);
    const frames = fast.at.map((at) => ({ at }));
    expect(pickFrames(frames, slow.holds, TICK)).toEqual(pickFrames(frames, fast.holds, TICK));
    expect(pickFrames(frames, slow.holds, TICK)).toHaveLength(10);
  });

  it('shows the last frame to arrive at an instant between two holds', () => {
    const frames = [{ at: 0 }, { at: 200 }, { at: 200 }, { at: 200 }];
    expect(pickFrames(frames, [200, 200], TICK)).toEqual([0, 0, 3, 3]);
  });

  it.each([
    { holds: [300, 200, 500], ticks: 10 },
    { holds: [250], ticks: 3 },
    { holds: [], ticks: 0 },
  ])('makes $ticks frames for holds $holds, whatever the frames', ({ holds, ticks }) => {
    const frames = [{ at: 0 }, { at: 1_000_000 }];
    expect(pickFrames(frames, holds, TICK)).toHaveLength(ticks);
  });

  it('shows the first frame until one has arrived, and nothing without frames', () => {
    expect(pickFrames([{ at: 250 }], [300], TICK)).toEqual([0, 0, 0]);
    expect(pickFrames([], [300], TICK)).toEqual([]);
  });
});
