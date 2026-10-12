/** Says whether a measured median time is within its budget, for the bench output. */
export function budgetLine(name: string, medianMs: number, budgetMs: number): string {
  const verdict = medianMs <= budgetMs ? 'within' : 'OVER';
  return `${name}: ${verdict} the ${budgetMs.toFixed(0)} ms budget (${medianMs.toFixed(1)} ms)`;
}

/** The median of a non-empty list of numbers. */
function median(values: readonly number[]): number {
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  const high = sorted[middle] ?? 0;
  const low = sorted[middle - 1] ?? high;
  return sorted.length % 2 === 0 ? (low + high) / 2 : high;
}

/**
 * The growth from a small to a large input as the median of the ratios of runs taken one
 * right after the other, so machine load that changes over time hits both sizes alike.
 */
export function pairedGrowthRatio(
  smallTimesMs: readonly number[],
  largeTimesMs: readonly number[],
): number {
  const ratios = smallTimesMs.flatMap((small, index) => {
    const large = largeTimesMs[index];
    return large === undefined ? [] : [large / small];
  });
  return median(ratios);
}

/** Says how much a doubling of the input grew the time, against the allowed growth. */
export function growthLine(name: string, ratio: number, allowedRatio: number): string {
  const verdict = ratio <= allowedRatio ? 'within' : 'OVER';
  return `${name}: ${verdict} ${allowedRatio.toFixed(1)}x per doubling (${ratio.toFixed(2)}x)`;
}
