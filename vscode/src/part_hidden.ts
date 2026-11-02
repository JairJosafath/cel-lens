/** The part of an element that `partHidden` reads. */
export interface PartElement {
  getBoundingClientRect: () => { readonly width: number; readonly height: number };
}

/**
 * Whether a workbench part is off screen: it is not in the page, or its box has no width or no
 * height. It runs inside the page, so it uses nothing outside itself.
 */
export function partHidden(part: PartElement | null): boolean {
  if (part === null) {
    return true;
  }
  const box = part.getBoundingClientRect();
  return box.width === 0 || box.height === 0;
}
