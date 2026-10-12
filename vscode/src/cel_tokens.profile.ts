import { generateCrd } from './bench_crd';
import { tokenizeCel } from './cel_tokens';
import { formatProfileSummary } from './profile_report';

// Run with `npm run profile -- <rules>`: tokenizes a generated CRD with that many rules, after
// warm-up runs, with a forced GC before each timed run. See cel_tokens.profile.md.
const DEFAULT_RULES = 5000;
const WARMUP_RUNS = 10;
const PROFILED_RUNS = 60;
const PROFILE_DIRECTORY = 'out/profile/';

const rules = Number(process.argv[2] ?? DEFAULT_RULES);
if (!Number.isInteger(rules) || rules < 1) {
  process.stderr.write(`Usage: npm run profile -- <rules>  (a positive whole number)\n`);
  process.exit(1);
}

const collectGarbage = (): void => {
  (globalThis as { gc?: () => void }).gc?.();
};

const crd = generateCrd(rules);
const runMilliseconds: number[] = [];
for (let run = 0; run < WARMUP_RUNS + PROFILED_RUNS; run++) {
  collectGarbage();
  const start = performance.now();
  tokenizeCel(crd);
  if (run >= WARMUP_RUNS) {
    runMilliseconds.push(performance.now() - start);
  }
}
process.stdout.write(
  formatProfileSummary({
    rules,
    warmupRuns: WARMUP_RUNS,
    profiledRuns: PROFILED_RUNS,
    runMilliseconds,
    directory: PROFILE_DIRECTORY,
  }),
);
