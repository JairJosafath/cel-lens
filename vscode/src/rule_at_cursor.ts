// Which rule of a file the cursor is in, from the list the engine gave: the rule whose span (its
// list item, from the line of its dash to the end of its last line) holds the position, else the
// document whose range holds it. It reads only what the list says, so after an edit that the list
// does not have yet it answers from the old lines, and the view asks again when the list changes.
// No `vscode` import.
import type { ListedDocument, ListedRule } from './rule_list';
import type { Range } from './rule_parts';

/** What the cursor is in: a document, and the rule of it whose item holds the position, if any. */
export interface CursorTarget {
  readonly document: ListedDocument;
  readonly rule?: ListedRule;
}

/** Whether a position is inside a range, its start and its end included: the cursor can stand at the end of a line. */
export function contains(range: Range, line: number, character: number): boolean {
  const { start, end } = range;
  const afterStart = line > start.line || (line === start.line && character >= start.character);
  const beforeEnd = line < end.line || (line === end.line && character <= end.character);
  return afterStart && beforeEnd;
}

/** How many lines a range takes: the smaller one is the more inner of two that both hold a position. */
function lines(range: Range): number {
  return range.end.line - range.start.line;
}

/**
 * The target for a position in a file's documents, `undefined` when no document holds it. Where
 * several spans hold the position the one of the fewest lines wins, and the same for documents,
 * so what is inside another is found before the other.
 */
export function ruleAtCursor(
  documents: readonly ListedDocument[],
  line: number,
  character: number,
): CursorTarget | undefined {
  let rule: { document: ListedDocument; rule: ListedRule } | undefined;
  for (const document of documents) {
    for (const each of document.rules) {
      if (each.span !== undefined && contains(each.span, line, character)) {
        if (rule === undefined || lines(each.span) < lines(rule.rule.span ?? each.span)) {
          rule = { document, rule: each };
        }
      }
    }
  }
  if (rule !== undefined) {
    return rule;
  }
  let found: ListedDocument | undefined;
  for (const document of documents) {
    if (
      document.range !== undefined &&
      contains(document.range, line, character) &&
      (found?.range === undefined || lines(document.range) < lines(found.range))
    ) {
      found = document;
    }
  }
  return found === undefined ? undefined : { document: found };
}

/**
 * What the context key `cel-lens.ruleAtCursor` says of a cursor: `ruleWithExamples` in a rule whose
 * row shows example words, `rule` in another rule, and `undefined` outside a rule (a document's
 * own lines, or no document). `showExamples` is whether example results are on for the file, as
 * the row's words show only then.
 */
export type CursorKey = 'rule' | 'ruleWithExamples';

export function cursorKey(
  documents: readonly ListedDocument[],
  line: number,
  character: number,
  showExamples: boolean,
): CursorKey | undefined {
  const rule = ruleAtCursor(documents, line, character)?.rule;
  if (rule === undefined) {
    return undefined;
  }
  return showExamples && rule.examples !== undefined ? 'ruleWithExamples' : 'rule';
}
