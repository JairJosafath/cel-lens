import { describe, expect, it } from 'vitest';

import { budgetLine, growthLine, pairedGrowthRatio } from './bench_budget';

describe('budgetLine', () => {
  it.each([
    [42.04, 'tokens: within the 100 ms budget (42.0 ms)'],
    [100, 'tokens: within the 100 ms budget (100.0 ms)'],
    [100.5, 'tokens: OVER the 100 ms budget (100.5 ms)'],
  ])('reports %f ms as "%s"', (medianMs, expected) => {
    expect(budgetLine('tokens', medianMs, 100)).toBe(expected);
  });
});

describe('growthLine', () => {
  it.each([
    [2, 'growth: within 2.3x per doubling (2.00x)'],
    [2.3, 'growth: within 2.3x per doubling (2.30x)'],
    [2.8, 'growth: OVER 2.3x per doubling (2.80x)'],
  ])('reports ratio %f as "%s"', (ratio, expected) => {
    expect(growthLine('growth', ratio, 2.3)).toBe(expected);
  });
});

describe('pairedGrowthRatio', () => {
  it.each([
    ['equal pairs', [10, 20, 30], [20, 40, 60], 2],
    ['one pair hit by a load spike', [10, 10, 10, 10, 10], [20, 20, 70, 20, 20], 2],
    ['a spike on the small run', [10, 10, 80, 10, 10], [20, 20, 20, 20, 20], 2],
    ['an even count', [10, 10], [20, 30], 2.5],
    ['unmatched extra small runs', [10, 10, 10], [20], 2],
  ])('gives the median of the pair ratios for %s', (_name, small, large, expected) => {
    expect(pairedGrowthRatio(small, large)).toBe(expected);
  });
});
