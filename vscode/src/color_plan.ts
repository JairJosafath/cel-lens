import { forEachCelToken } from './cel_tokens';
import { celColors, type CelStyle, type ThemeKind } from './cel_styles';
import type { CelFeatures, DocumentPosition, LineWindow } from './cel_ranges';
import type { CelTokenKind } from './cel_lexer';

/** A span of the document to paint: `end` is excluded. */
export interface PaintRange {
  start: DocumentPosition;
  end: DocumentPosition;
}

/** Every span to paint in one color, in document order. */
export interface ColorPaint {
  color: string;
  ranges: PaintRange[];
}

/**
 * What to paint where: the CEL in a YAML document, grouped by color. Parts
 * that share a color in the style share one entry. `off` gives nothing. A Kyverno policy is planned only with `features.kyverno`. With a
 * `window`, only the expressions that start on its lines are planned.
 */
export function planColors(
  text: string,
  style: CelStyle,
  theme: ThemeKind,
  window?: LineWindow,
  features?: CelFeatures,
): ColorPaint[] {
  if (style === 'off') {
    return [];
  }
  const colors = celColors(style, theme);
  // Kinds that share a color share one list, so most tokens cost one lookup.
  const byColor = new Map<string, PaintRange[]>();
  const byKind = new Map<CelTokenKind, PaintRange[] | null>();
  forEachCelToken(
    text,
    (kind, start, end) => {
      let ranges = byKind.get(kind);
      if (ranges === undefined) {
        const color = colors[kind];
        if (color === undefined) {
          ranges = null;
        } else {
          ranges = byColor.get(color) ?? [];
          byColor.set(color, ranges);
        }
        byKind.set(kind, ranges);
      }
      ranges?.push({ start, end });
    },
    window,
    features,
  );
  return [...byColor].map(([color, ranges]) => ({ color, ranges }));
}
