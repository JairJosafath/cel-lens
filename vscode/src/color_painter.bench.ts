import { Bench } from 'tinybench';

import { FakeEditor, openDocument, visibleEditors } from '../test/vscode';
import { budgetLine } from './bench_budget';
import { generateCrd } from './bench_crd';
import { runAndReport } from './bench_report';
import { ColorPainter } from './color_painter';

// Run with `npm run bench`: one repaint of a CRD with 10000 rules (planColors, then the
// decorations) against the stand-in in test/vscode.ts, so VS Code's own cost is not counted.
// The editor shows 50 lines in the middle of the file, as after scrolling; the budget is 100 ms.
const RULES = 10000;
const BUDGET_MS = 100;
const document = { languageId: 'yaml', getText: () => text };
const text = generateCrd(RULES);
const editor = new FakeEditor(document);
editor.visibleRanges = [{ start: { line: RULES }, end: { line: RULES + 50 } }];
visibleEditors.push(editor);
const painter = new ColorPainter();

const bench = new Bench({ name: 'color painter', time: 1000 });
bench.add('ColorPainter repaint, CRD with 10000 rules', () => {
  openDocument(document);
});
await runAndReport(bench);
const repaint = bench.tasks[0]?.result;
if (repaint?.state === 'completed') {
  process.stdout.write(`${budgetLine(`${String(RULES)} rules`, repaint.latency.p50, BUDGET_MS)}\n`);
}
painter.dispose();
