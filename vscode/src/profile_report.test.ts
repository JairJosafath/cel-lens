import { describe, expect, it } from 'vitest';
import { formatProfileSummary, median } from './profile_report';

describe('median', () => {
  it.each([
    { values: [], expected: 0 },
    { values: [7], expected: 7 },
    { values: [3, 1, 2], expected: 2 },
    { values: [4, 1, 3, 2], expected: 2.5 },
  ])('is $expected for $values', ({ values, expected }) => {
    expect(median(values)).toBe(expected);
  });
});

describe('formatProfileSummary', () => {
  it('prints the median, the warm-up and profiled run counts and the directory', () => {
    expect(
      formatProfileSummary({
        rules: 5000,
        warmupRuns: 10,
        profiledRuns: 3,
        runMilliseconds: [59.5, 58, 61],
        directory: 'out/profile/',
      }),
    ).toBe(
      'Profiled 5000 rules: median 59.5 ms per run (10 warm-up runs, 3 profiled runs).\n' +
        'CPU profile written to out/profile/\n',
    );
  });
});
