import { Bench } from 'tinybench';

import { budgetLine, growthLine, pairedGrowthRatio } from './bench_budget';
import { runAndReport } from './bench_report';
import { cursorKey } from './rule_at_cursor';
import { RuleList, parseRuleListAnswer } from './rule_list';
import { rawAnswer, rawDocument, rawFiles, rawRule } from './rule_list_samples';
import { RuleTree } from './rule_tree';

// Run with `npm run bench`: the engine answers a change of one file of a workspace with 5000
// rules; the extension reads the answer, applies it to the list and builds the items of the
// document that changed, as the view does for each such answer. The budget is 5 ms. The 10000
// rule run shows how the time grows with the list: at most 1.3x per doubling.
const RULES_PER_FILE = 10;
const BUDGET_RULES = 5000;
const BUDGET_MS = 5;
const MAX_GROWTH = 1.3;
const GROWTH_ROUNDS = 100;

/** The list and tree of a workspace with `rules` rules, as the view holds them after its first answer. */
function workspace(
  rules: number,
  problemsOnly = false,
  extra: Record<string, unknown>[] = [],
): { list: RuleList; tree: RuleTree } {
  const list = new RuleList();
  const tree = new RuleTree(list, {
    folders: [{ uri: 'file:///repo', name: 'repo' }],
    normalize: (uri) => uri,
    showExamples: () => true,
    limits: {},
    problemsOnly,
  });
  const first = parseRuleListAnswer(
    rawAnswer([...rawFiles(rules / RULES_PER_FILE, RULES_PER_FILE), ...extra], { generation: 1 }),
  );
  if (first === undefined) {
    throw new Error('the sample answer was not read');
  }
  tree.update(list.apply(first, 'all'));
  return { list, tree };
}

/**
 * The answer to a change of the file in the middle of the workspace. Two states alternate, so
 * each answer really changes the file: a rule's compile state flips between ok and a warning.
 */
function change(files: number, keepProblem = false): (round: number) => Record<string, unknown> {
  const middle = Math.floor(files / 2);
  const uri = `file:///repo/config/crd${String(middle).padStart(4, '0')}.yaml`;
  const name = `things${String(middle)}.example.com`;
  const document = `CustomResourceDefinition ${name}`;
  const make = (compile: 'ok' | 'warning'): Record<string, unknown> =>
    rawAnswer(
      [
        rawDocument(
          uri,
          Array.from({ length: RULES_PER_FILE }, (_, index) =>
            rawRule(uri, document, index, {
              compile: index === 0 ? compile : keepProblem && index === 1 ? 'error' : 'ok',
            }),
          ),
          { name },
        ),
      ],
      {},
    );
  const answers = [make('warning'), make('ok')];
  return (round) => {
    const answer = answers[round % 2] ?? {};
    answer['generation'] = round + 2;
    return answer;
  };
}

function runner(rules: number): () => void {
  const { list, tree } = workspace(rules);
  const next = change(rules / RULES_PER_FILE);
  let round = 0;
  return () => {
    const answer = parseRuleListAnswer(next(round));
    round += 1;
    if (answer === undefined) {
      throw new Error('the change was not read');
    }
    const refresh = tree.update(list.apply(answer, 'since'));
    if (refresh.length !== 1) {
      throw new Error(`a change of one file refreshed ${String(refresh.length)} nodes`);
    }
  };
}

/** The most rules of one file the cursor is looked up in. */
const BIG_FILE_RULES = 500;
const BIG_URI = 'file:///repo/config/big.yaml';
const CURSOR_BUDGET_MS = 1;
const FILTER_BUDGET_MS = 5;

/** A file of 500 rules in one CRD, whose document range holds all their spans. */
function bigFile(): Record<string, unknown> {
  const document = 'CustomResourceDefinition big.example.com';
  return {
    ...rawDocument(
      BIG_URI,
      Array.from({ length: BIG_FILE_RULES }, (_, index) => rawRule(BIG_URI, document, index)),
      { name: 'big.example.com' },
    ),
    range: {
      start: { line: 0, character: 0 },
      end: { line: 10 + BIG_FILE_RULES * 4 + 10, character: 0 },
    },
  };
}

/** Finding the rule the cursor is in, in a file of 500 rules within a list of `rules`: the follow of the view. */
function cursorRunner(rules: number): () => void {
  const { tree } = workspace(rules - BIG_FILE_RULES, false, [bigFile()]);
  let round = 0;
  return () => {
    const rule = round % BIG_FILE_RULES;
    round += 1;
    const found = tree.at(BIG_URI, 10 + rule * 4 + 1, 5);
    if (found?.kind !== 'rule') {
      throw new Error(`no rule found at the cursor of rule ${String(rule)}`);
    }
  };
}

/**
 * The key `cel-lens.ruleAtCursor` after a selection change, in a file of 500 rules within a list of
 * `rules`: the list's documents of the file and the lookup of the rule at the cursor, which the
 * extension does at each selection change, with no pause.
 */
function keyRunner(rules: number): () => void {
  const { list } = workspace(rules - BIG_FILE_RULES, false, [bigFile()]);
  let round = 0;
  return () => {
    const rule = round % BIG_FILE_RULES;
    round += 1;
    const key = cursorKey(list.documentsOf(BIG_URI), 10 + rule * 4 + 1, 5, true);
    if (key !== 'rule') {
      throw new Error(`no rule key at the cursor of rule ${String(rule)}`);
    }
  };
}

/**
 * A one-file answer with the filter for problems on: read, applied, the filtered tree built for the
 * file, and the badge's count, as the view does for each answer.
 */
function filteredRunner(rules: number): () => void {
  const { list, tree } = workspace(rules, true);
  const next = change(rules / RULES_PER_FILE, true);
  let round = 0;
  return () => {
    const answer = parseRuleListAnswer(next(round));
    round += 1;
    if (answer === undefined) {
      throw new Error('the change was not read');
    }
    tree.update(list.apply(answer, 'since'));
    if (list.problems < 1 || tree.roots().length !== 1) {
      throw new Error('the badge or the filtered tree is not what the answer says');
    }
  };
}

const cursorTask = `find the rule at the cursor, a file of ${String(BIG_FILE_RULES)} rules in ${String(BUDGET_RULES)}`;
const keyTask = `key update after a selection change, a file of ${String(BIG_FILE_RULES)} rules in ${String(BUDGET_RULES)}`;
const filterTask = `badge and filtered tree after a one-file answer, ${String(BUDGET_RULES)} rules`;

const taskName = (rules: number): string => `apply a change of one file, ${String(rules)} rules`;

// `npm run bench` starts node with --expose-gc: collecting before each run keeps a GC pause
// caused by earlier runs out of the timed one.
const collectGarbage = (): void => {
  (globalThis as { gc?: () => void }).gc?.();
};

// A run is under a millisecond, so the time is set by the number of runs; the collection before
// each one is not part of the time tinybench adds up.
const bench = new Bench({ name: 'rule list', warmupIterations: 20, iterations: 200, time: 0 });
for (const rules of [2500, BUDGET_RULES, BUDGET_RULES * 2]) {
  bench.add(taskName(rules), runner(rules), { beforeEach: collectGarbage });
}
bench.add(cursorTask, cursorRunner(BUDGET_RULES), { beforeEach: collectGarbage });
bench.add(keyTask, keyRunner(BUDGET_RULES), { beforeEach: collectGarbage });
bench.add(filterTask, filteredRunner(BUDGET_RULES), { beforeEach: collectGarbage });
await runAndReport(bench);

const budgeted = bench.getTask(taskName(BUDGET_RULES));
if (budgeted?.result.state === 'completed') {
  process.stdout.write(
    `${budgetLine(`${String(BUDGET_RULES)} rules`, budgeted.result.latency.p50, BUDGET_MS)}\n`,
  );
}

for (const [name, budget] of [
  [cursorTask, CURSOR_BUDGET_MS],
  [keyTask, CURSOR_BUDGET_MS],
  [filterTask, FILTER_BUDGET_MS],
] as const) {
  const task = bench.getTask(name);
  if (task?.result.state === 'completed') {
    process.stdout.write(`${budgetLine(name, task.result.latency.p50, budget)}\n`);
  }
}

// The growth line times the 5000 and 10000 rule lists one right after the other, round after
// round, so load on the machine hits both alike; the ratio is the median over the rounds.
const small = runner(BUDGET_RULES);
const large = runner(BUDGET_RULES * 2);
const timeOnce = (run: () => void): number => {
  collectGarbage();
  const start = performance.now();
  run();
  return performance.now() - start;
};
for (let warm = 0; warm < 20; warm++) {
  small();
  large();
}
const smallTimes: number[] = [];
const largeTimes: number[] = [];
for (let round = 0; round < GROWTH_ROUNDS; round++) {
  smallTimes.push(timeOnce(small));
  largeTimes.push(timeOnce(large));
}
process.stdout.write(
  `${growthLine('10000 rules over 5000', pairedGrowthRatio(smallTimes, largeTimes), MAX_GROWTH)}\n`,
);
