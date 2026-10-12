import { describe, expect, it } from 'vitest';

import { celColors, type CelStyle, type ThemeKind } from './cel_styles';
import { planColors } from './color_plan';

const CRD = [
  'x-kubernetes-validations:',
  '  - rule: "self.a < 3"',
  '    message: "self.a must be small"',
  '  - rule: "self.b"',
  '',
].join('\n');

const STYLES = ['cel', 'classic'] as const satisfies CelStyle[];
const THEMES = ['light', 'dark', 'hcDark', 'hcLight'] as const satisfies ThemeKind[];
const CASES = STYLES.flatMap((style) => THEMES.map((theme) => [style, theme] as const));

describe('planColors', () => {
  it.each(CASES)('paints the two rules in %s on %s, and not the message', (style, theme) => {
    const plan = planColors(CRD, style, theme);
    const colors = celColors(style, theme);

    const painted = plan.flatMap((paint) =>
      paint.ranges.map((range) => [paint.color, range.start.line] as const),
    );
    expect(new Set(painted.map(([, line]) => line))).toEqual(new Set([1, 3]));
    expect(painted).toHaveLength(8);
    expect(plan.find((paint) => paint.color === colors.variable)?.ranges).toEqual([
      { start: { line: 1, character: 11 }, end: { line: 1, character: 15 } },
      { start: { line: 3, character: 11 }, end: { line: 3, character: 15 } },
    ]);
    expect(plan.every((paint) => Object.values(colors).includes(paint.color))).toBe(true);
  });

  it.each(THEMES)('paints nothing for off on %s', (theme) => {
    expect(planColors(CRD, 'off', theme)).toEqual([]);
  });

  it.each([
    ['empty', ''],
    ['without CEL', 'a: 1\n'],
    ['broken', 'x-kubernetes-validations:\n  - rule: "self.a\n  : ['],
  ])('does not throw on a document %s', (_name, text) => {
    expect(() => planColors(text, 'cel', 'dark')).not.toThrow();
  });

  it.each(THEMES)('paints nothing for off in a policy on %s', (theme) => {
    const policy =
      'apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: object.a < 3\n';
    expect(planColors(policy, 'off', theme)).toEqual([]);
    expect(planColors(policy, 'cel', theme)).not.toEqual([]);
  });
});
