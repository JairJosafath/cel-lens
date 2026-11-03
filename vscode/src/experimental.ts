/** The `cel-lens.experimental` setting, as contributed in package.json. */
export const EXPERIMENTAL_SETTING = 'experimental';

/** The experimental feature that colors Kyverno `ValidatingPolicy` documents. */
export const KYVERNO_FEATURE = 'kyverno';

/**
 * The list the engine is told: the strings of the setting, trimmed and without duplicates, in the
 * order given. Anything else (a malformed settings.json) is left out; the engine ignores names it
 * does not know and says so once.
 */
export function parseExperimental(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  const names = value.flatMap((item: unknown) => (typeof item === 'string' ? [item.trim()] : []));
  return [...new Set(names.filter((name) => name !== ''))];
}

/** The names in the setting that the engine does not list among its experimental features. */
export function unknownExperimental(
  setting: readonly string[],
  known: readonly string[],
): string[] {
  return setting.filter((name) => !known.includes(name));
}

/** The warning for names the engine does not know, ending with what to do. */
export function unknownExperimentalMessage(names: readonly string[]): string {
  const quoted = names.map((name) => `"${name}"`).join(', ');
  return `cel-lens.experimental lists ${quoted}, which the engine does not know, so ${names.length === 1 ? 'it is' : 'they are'} ignored. Remove or correct ${names.length === 1 ? 'it' : 'them'} in the setting.`;
}
