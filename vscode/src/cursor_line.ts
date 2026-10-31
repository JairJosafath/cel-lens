/** The part of an element that `cursorLineText` reads. */
export interface LineElement {
  readonly textContent: string | null;
  readonly style: { readonly top: string };
}

/** The part of the document that `cursorLineText` reads. */
export interface EditorDocument {
  querySelector: (selector: string) => LineElement | null;
  querySelectorAll: (selector: string) => Iterable<LineElement>;
}

/**
 * The text of the line the cursor is on, or `undefined` when the cursor or its line is not
 * rendered. Monaco renders only the visible lines, so the cursor's line is found by its
 * position, never by counting lines. It runs inside the page, so it uses nothing outside itself.
 */
export function cursorLineText(doc: EditorDocument): string | undefined {
  const cursor = doc.querySelector('.monaco-editor .cursors-layer .cursor');
  if (cursor === null) {
    return undefined;
  }
  const top = parseFloat(cursor.style.top);
  for (const line of doc.querySelectorAll('.monaco-editor .view-line')) {
    if (parseFloat(line.style.top) === top) {
      return (line.textContent ?? '').replace(/\u00a0/g, ' ');
    }
  }
  return undefined;
}
