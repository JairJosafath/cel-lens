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

describe('cursorLineText as the wait after the undos', () => {
  const lastRule = '    - rule: ';

  it.each([
    { name: 'the top rule of a tall document', first: 0, cursor: 0, done: true },
    {
      name: 'the bottom rule, one line scrolled past the last',
      first: 91,
      cursor: 119,
      done: true,
    },
    { name: 'a line that is not the rule', first: 90, cursor: 118, done: false },
  ])('is $done for $name', ({ first, cursor, done }) => {
    expect(cursorLineText(documentScrolledTo(first, cursor)) === lastRule).toBe(done);
  });
});

describe('cursorLineText serialised for the page', () => {
  it('reads the cursor line when evaluated with only a document', () => {
    // eslint-disable-next-line @typescript-eslint/no-implied-eval -- the page receives the function as source, so the test evaluates it the same way
    const inPage = new Function('document', `return (${cursorLineText.toString()})(document);`);
    const text: unknown = Reflect.apply(inPage, undefined, [documentScrolledTo(90, 119)]);
    expect(text).toBe('    - rule: ');
  });
});
