/** The part of a computed style that `widgetShown` reads. */
export interface WidgetStyle {
  readonly display: string;
  readonly visibility: string;
  readonly opacity: string;
}

/** The part of an element that `widgetShown` reads. */
export interface WidgetElement {
  readonly ownerDocument: {
    readonly defaultView: { getComputedStyle: (element: WidgetElement) => WidgetStyle } | null;
  };
  getAnimations: (options: { subtree: boolean }) => readonly { readonly playState: string }[];
}

/**
 * Whether a widget is fully shown: rendered (not `display: none` or `visibility: hidden`), its
 * computed opacity is 1, and none of its animations or transitions, nor those of its subtree, is
 * still running. It runs inside the page, so it uses nothing outside itself.
 */
export function widgetShown(element: WidgetElement): boolean {
  const view = element.ownerDocument.defaultView;
  if (view === null) {
    return false;
  }
  const style = view.getComputedStyle(element);
  if (style.display === 'none' || style.visibility === 'hidden' || style.opacity !== '1') {
    return false;
  }
  return !element
    .getAnimations({ subtree: true })
    .some((animation) => animation.playState === 'running');
}
