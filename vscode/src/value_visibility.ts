import { SHOW_EXAMPLE_RESULTS_COMMAND } from './example_results';

/** The `cel-lens.valueTypes` setting, as contributed in package.json. */
export const VALUE_TYPES_SETTING = 'valueTypes';
export const VALUE_TYPES_SETTING_NAME = `cel-lens.${VALUE_TYPES_SETTING}`;

/** The `cel-lens.toggleValueTypes` command, as contributed in package.json. */
export const TOGGLE_VALUE_TYPES_COMMAND = 'cel-lens.toggleValueTypes';

/** The values a rule reads show until a person hides them. */
export const DEFAULT_VALUE_TYPES = true;

/** The part of a code lens this module looks at; a `vscode.CodeLens` has it. */
interface PartOfLine {
  readonly range: { readonly start: { readonly line: number } };
  readonly command?: { readonly command: string };
}

/**
 * What the engine answers for a file, without the value parts of its lines: of the lenses that
 * share a start line it keeps the first, the state, and any that runs
 * `cel-lens.showExampleResults`, and drops the others (the values a rule reads, and `+N more`).
 * The engine orders each line's parts the same way, the state first, so the position says what a
 * part is; a value part the engine infers runs `cel-lens.showRule`, so its command does not.
 * An empty or `null` answer stays as it is.
 */
export function withoutValueParts<Lens extends PartOfLine>(
  lenses: Lens[] | null | undefined,
): Lens[] | null | undefined {
  if (lenses === null || lenses === undefined) {
    return lenses;
  }
  const seen = new Set<number>();
  return lenses.filter((lens) => {
    const line = lens.range.start.line;
    if (lens.command?.command === SHOW_EXAMPLE_RESULTS_COMMAND) {
      return true;
    }
    if (seen.has(line)) {
      return false;
    }
    seen.add(line);
    return true;
  });
}
