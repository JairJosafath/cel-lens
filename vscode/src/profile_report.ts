export const median = (values: readonly number[]): number => {
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  const high = sorted[middle] ?? 0;
  const low = sorted[middle - 1] ?? high;
  return sorted.length % 2 === 0 ? (low + high) / 2 : high;
};

export interface ProfileSummary {
  readonly rules: number;
  readonly warmupRuns: number;
  readonly profiledRuns: number;
  readonly runMilliseconds: readonly number[];
  readonly directory: string;
}

export const formatProfileSummary = (summary: ProfileSummary): string =>
  `Profiled ${String(summary.rules)} rules: median ${median(summary.runMilliseconds).toFixed(1)} ms per run ` +
  `(${String(summary.warmupRuns)} warm-up runs, ${String(summary.profiledRuns)} profiled runs).\n` +
  `CPU profile written to ${summary.directory}\n`;
