/** The holds (ms) each README GIF is planned with; `test/pictures/take.ts` records with these same values. */

/** The delay between typed characters in `colors.gif`. */
export const GIF_TYPING_MS = 80;

/** The text `colors.gif` types. */
export const COLORS_TYPED_TEXT = "self.name.startsWith('w') && self.replicas > 1";

export const COLORS_HOLD_BEFORE_MS = 200;
export const COLORS_HOLD_AFTER_MS = 600;

/** The holds of `colors.gif` in order: a lead, one per typed character, a tail. */
export function colorsHolds(): number[] {
  return [
    COLORS_HOLD_BEFORE_MS,
    ...Array.from(COLORS_TYPED_TEXT, () => GIF_TYPING_MS),
    COLORS_HOLD_AFTER_MS,
  ];
}

/** The holds of `completion.gif`, named by the step they follow. */
export const COMPLETION_HOLDS_MS = {
  start: 300,
  afterSelf: 200,
  listShown: 700,
  firstPick: 500,
  secondPick: 500,
  afterTab: 800,
} as const;

/** The holds of `completion.gif` in the order the script plays them. */
export function completionHolds(): number[] {
  return Object.values(COMPLETION_HOLDS_MS);
}
