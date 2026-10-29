import { scanCel, type CelTokenKind } from './cel_lexer';
import {
  findCel,
  offsetMap,
  type CelFeatures,
  type DocumentPosition,
  type LineWindow,
} from './cel_ranges';

export interface CelToken {
  kind: CelTokenKind;
  start: DocumentPosition;
  end: DocumentPosition;
}

/** Offsets of the first character of each line. */
function lineStarts(text: string): number[] {
  const starts = [0];
  for (let i = text.indexOf('\n'); i >= 0; i = text.indexOf('\n', i + 1)) {
    starts.push(i + 1);
  }
  return starts;
}

/**
 * Converts offsets to positions. Tokens come in document order, so it keeps the
 * line it found last and its bounds, and moves on only when an offset leaves them.
 */
function locator(starts: number[]): (offset: number) => DocumentPosition {
  let line = 0;
  let lineStart = 0;
  let nextStart = starts[1] ?? Infinity;
  return (offset) => {
    if (offset < lineStart || offset >= nextStart) {
      while (line > 0 && (starts[line] ?? 0) > offset) {
        line--;
      }
      while (line + 1 < starts.length && (starts[line + 1] ?? 0) <= offset) {
        line++;
      }
      lineStart = starts[line] ?? 0;
      nextStart = starts[line + 1] ?? Infinity;
    }
    return { line, character: offset - lineStart };
  };
}

/**
 * Calls `visit` for every token of every CEL expression in a YAML document, in
 * document order and document positions, without collecting them. With a
 * `window`, only the expressions that overlap its lines are tokenized. `features` tells which
 * experimental features count.
 */
export function forEachCelToken(
  text: string,
  visit: (kind: CelTokenKind, start: DocumentPosition, end: DocumentPosition) => void,
  window?: LineWindow,
  features?: CelFeatures,
): void {
  const positionAt = locator(lineStarts(text));
  for (const range of findCel(text, window, features)) {
    const [only] = range.segments;
    if (only?.copied === true && range.segments.length === 1) {
      // One copied run sits on one line, so a token's column is its index plus the run's.
      const { line, character } = positionAt(only.start);
      scanCel(range.source, (kind, first, last) => {
        visit(kind, { line, character: character + first }, { line, character: character + last });
      });
      continue;
    }
    const offsets = offsetMap(range);
    scanCel(range.source, (kind, first, last) => {
      const start = offsets.start(first);
      const end = offsets.end(last - 1);
      if (start !== undefined && end !== undefined) {
        visit(kind, positionAt(start), positionAt(end));
      }
    });
  }
}

/** Every token of every CEL expression in a YAML document, in document positions. */
export function tokenizeCel(text: string): CelToken[] {
  const tokens: CelToken[] = [];
  forEachCelToken(text, (kind, start, end) => {
    tokens.push({ kind, start, end });
  });
  return tokens;
}
