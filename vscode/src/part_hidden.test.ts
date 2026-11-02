import { runInNewContext } from 'node:vm';

import { describe, expect, it } from 'vitest';

import { partHidden } from './part_hidden.ts';

function part(width: number, height: number): { getBoundingClientRect: () => never } {
  return { getBoundingClientRect: () => ({ width, height }) as never };
}

describe('partHidden', () => {
  it.each([
    { name: 'true when the part is not in the page', element: null, hidden: true },
    { name: 'true when the box has no width', element: part(0, 600), hidden: true },
    { name: 'true when the box has no height', element: part(300, 0), hidden: true },
    { name: 'false when the box has a size', element: part(300, 600), hidden: false },
  ])('is $name', ({ element, hidden }) => {
    expect(partHidden(element)).toBe(hidden);
  });

  it('runs alone in a page, from its source text', () => {
    const run = runInNewContext(`(${partHidden.toString()})`) as typeof partHidden;
    expect(run(part(0, 0))).toBe(true);
    expect(run(part(1, 1))).toBe(false);
  });
});
