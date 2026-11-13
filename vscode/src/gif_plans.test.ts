import { describe, expect, it } from 'vitest';

import { checkPlannedLength } from './gif_timeline.ts';
import { colorsHolds, completionHolds, giveTypeHolds } from './gif_plans.ts';

describe('the planned GIF lengths in docs/project-notes.md', () => {
  it.each([
    { name: 'colors.gif', holds: colorsHolds(), expected: 4480 },
    { name: 'completion.gif', holds: completionHolds(), expected: 3000 },
    { name: 'give-type.gif', holds: giveTypeHolds(), expected: 6000 },
  ])('$name plays $expected ms in total', ({ name, holds, expected }) => {
    expect(checkPlannedLength(name, holds)).toBe(expected);
  });
});
