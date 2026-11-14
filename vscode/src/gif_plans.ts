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

/** What `give-type.gif` types in the picker: it leaves the workspace's example alone in the list. */
export const GIVE_TYPE_FILTER = 'limit.yaml';

/** The holds of `give-type.gif`, named by the step they follow. */
export const GIVE_TYPE_HOLDS_MS = {
  start: 600,
  pickerShown: 1400,
  exampleHighlighted: 1000,
  afterWrite: 2200,
} as const;

/** The holds of `give-type.gif` in the order the script plays them: the typing of the filter comes after `pickerShown`. */
export function giveTypeHolds(): number[] {
  const { start, pickerShown, exampleHighlighted, afterWrite } = GIVE_TYPE_HOLDS_MS;
  return [
    start,
    pickerShown,
    ...Array.from(GIVE_TYPE_FILTER, () => GIF_TYPING_MS),
    exampleHighlighted,
    afterWrite,
  ];
}

/** The delay between typed characters in `cellens-types.gif`. */
export const CELLENS_TYPES_TYPING_MS = 50;

/** What `cellens-types.gif` types in the schema before the completion list opens, what it types in the list to find the type, and after the type the list gives. */
export const CELLENS_TYPES_TYPED_BEFORE = 'maxReplicas: {type: ';
export const CELLENS_TYPES_FILTER = 'int';
export const CELLENS_TYPES_TYPED_AFTER = '}';

/** The holds of `cellens-types.gif`, named by the step they follow. */
export const CELLENS_TYPES_HOLDS_MS = {
  start: 500,
  pickerShown: 1100,
  cellensOpen: 700,
  listShown: 800,
  typePicked: 400,
  afterWrite: 1600,
} as const;

/** The holds of `cellens-types.gif` in the order the script plays them. */
export function cellensTypesHolds(): number[] {
  const { start, pickerShown, cellensOpen, listShown, typePicked, afterWrite } =
    CELLENS_TYPES_HOLDS_MS;
  return [
    start,
    pickerShown,
    cellensOpen,
    ...Array.from(CELLENS_TYPES_TYPED_BEFORE, () => CELLENS_TYPES_TYPING_MS),
    listShown,
    ...Array.from(CELLENS_TYPES_FILTER, () => CELLENS_TYPES_TYPING_MS),
    typePicked,
    ...Array.from(CELLENS_TYPES_TYPED_AFTER, () => CELLENS_TYPES_TYPING_MS),
    afterWrite,
  ];
}
