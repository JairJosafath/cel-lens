import { Bench } from 'tinybench';

import { generateCrd } from './bench_crd';
import { budgetLine, growthLine, pairedGrowthRatio } from './bench_budget';
import { runAndReport } from './bench_report';
import { tokenizeCel } from './cel_tokens';

// Run with `npm run bench`: finding and tokenizing a CRD with 5000 rules has a budget of 100 ms.
// The 2500 and 10000 rule runs show how the time grows with the rules.
const SIZES = [2500, 5000, 10000];
const BUDGET_RULES = 5000;
const BUDGET_MS = 100;
const MAX_GROWTH = 2.3;

const taskName = (rules: number): string => `tokenizeCel, CRD with ${String(rules)} rules`;

// `npm run bench` starts node with --expose-gc: collecting before each run keeps a GC pause
// caused by earlier runs out of the timed one.
const collectGarbage = (): void => {
  (globalThis as { gc?: () => void }).gc?.();
};

const bench = new Bench({
  name: 'cel tokens',
  // Warm up the JIT and take enough runs that a few GC pauses do not move the median.
  warmupIterations: 10,
  iterations: 50,
  time: 1000,
});
for (const rules of SIZES) {
  const crd = generateCrd(rules);
  bench.add(
    taskName(rules),
    () => {
      tokenizeCel(crd);
    },
    { beforeEach: collectGarbage },
  );
}
await runAndReport(bench);

const budgeted = bench.getTask(taskName(BUDGET_RULES));
if (budgeted?.result.state === 'completed') {
  process.stdout.write(
    `${budgetLine(`${String(BUDGET_RULES)} rules`, budgeted.result.latency.p50, BUDGET_MS)}\n`,
  );
}

// The growth line times the 5000 and 10000 rule sizes one right after the other, round after
// round, so load on the machine hits both alike; the ratio is the median over the rounds.
const GROWTH_ROUNDS = 40;
const smallCrd = generateCrd(BUDGET_RULES);
const largeCrd = generateCrd(BUDGET_RULES * 2);
const timeOnce = (crd: string): number => {
  collectGarbage();
  const start = performance.now();
  tokenizeCel(crd);
  return performance.now() - start;
};
const smallTimes: number[] = [];
const largeTimes: number[] = [];
for (let round = 0; round < GROWTH_ROUNDS; round++) {
  smallTimes.push(timeOnce(smallCrd));
  largeTimes.push(timeOnce(largeCrd));
}
process.stdout.write(
  `${growthLine('10000 rules over 5000', pairedGrowthRatio(smallTimes, largeTimes), MAX_GROWTH)}\n`,
);
