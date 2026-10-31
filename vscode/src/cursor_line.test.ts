import { describe, expect, it } from 'vitest';

import { cursorLineText, type EditorDocument, type LineElement } from './cursor_line.ts';

const LINE_HEIGHT = 19;
const TOTAL_LINES = 120;
const VIEWPORT_LINES = 30;

function element(top: number, text: string): LineElement {
  return { textContent: text, style: { top: `${String(top)}px` } };
}

/** A document of `TOTAL_LINES` lines, rules at the top and bottom, of which only a window is rendered. */
function documentScrolledTo(firstLine: number, cursorLine: number): EditorDocument {
  const text = (line: number): string =>
    line === 0 || line === TOTAL_LINES - 1 ? '    - rule: ' : `    - field${String(line)}`;
  const rendered: LineElement[] = [];
  for (let line = firstLine; line < firstLine + VIEWPORT_LINES; line += 1) {
    rendered.push(element(line * LINE_HEIGHT, text(line).replace(/ /g, '\u00a0')));
  }
  const cursor = element(cursorLine * LINE_HEIGHT, '');
  return {
    querySelector: (selector) => (selector.includes('.cursor') ? cursor : null),
    querySelectorAll: () => rendered,
  };
}

describe('cursorLineText', () => {
  it.each([
    { name: 'the top of a document taller than two viewports', first: 0, cursor: 0 },
    { name: 'the bottom of a document taller than two viewports', first: 90, cursor: 119 },
  ])('reads the cursor line at $name, whatever is scrolled out of view', ({ first, cursor }) => {
    expect(cursorLineText(documentScrolledTo(first, cursor))).toBe('    - rule: ');
  });

  it('gives nothing when the cursor line is scrolled out of view', () => {
    expect(cursorLineText(documentScrolledTo(0, 100))).toBeUndefined();
  });

  it('gives nothing without a cursor', () => {
    const doc: EditorDocument = { querySelector: () => null, querySelectorAll: () => [] };
    expect(cursorLineText(doc)).toBeUndefined();
  });
});
