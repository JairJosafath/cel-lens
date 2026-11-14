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

/**
 * The known name closest to an unknown one (ignoring case), or undefined when nothing is close: at
 * most one edit per three characters of the name, and at least one. The first of equally close
 * names wins.
 */
export function closestKnownName(name: string, known: readonly string[]): string | undefined {
  const limit = Math.max(1, Math.floor(name.length / 3));
  let best: string | undefined;
  let bestDistance = limit + 1;
  for (const candidate of known) {
    const distance = editDistance(name.toLowerCase(), candidate.toLowerCase());
    if (distance < bestDistance) {
      best = candidate;
      bestDistance = distance;
    }
  }
  return best;
}

/** The number of single-character edits (insert, delete, replace) between two strings. */
function editDistance(a: string, b: string): number {
  let previous = Array.from({ length: b.length + 1 }, (_, index) => index);
  for (let i = 1; i <= a.length; i++) {
    const row = [i];
    for (let j = 1; j <= b.length; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      row.push(
        Math.min((previous[j] ?? 0) + 1, (row[j - 1] ?? 0) + 1, (previous[j - 1] ?? 0) + cost),
      );
    }
    previous = row;
  }
  return previous[b.length] ?? 0;
}

/**
 * The warning for names the engine does not know, naming the closest known name of each where
 * there is one, and ending with what to do.
 */
export function unknownExperimentalMessage(
  names: readonly string[],
  known: readonly string[],
): string {
  const quoted = names
    .map((name) => {
      const suggestion = closestKnownName(name, known);
      return suggestion === undefined ? `"${name}"` : `"${name}" (did you mean "${suggestion}"?)`;
    })
    .join(', ');
  return `cel-lens.experimental lists ${quoted}, which the engine does not know, so ${names.length === 1 ? 'it is' : 'they are'} ignored. Remove or correct ${names.length === 1 ? 'it' : 'them'} in the setting.`;
}

/**
 * The suggestion for each unknown name, but only when every one has a suggestion: a replacement
 * that fixes some names and leaves others would still leave a warning.
 */
export function suggestions(
  names: readonly string[],
  known: readonly string[],
): Map<string, string> | undefined {
  const found = new Map<string, string>();
  for (const name of names) {
    const suggestion = closestKnownName(name, known);
    if (suggestion === undefined) {
      return undefined;
    }
    found.set(name, suggestion);
  }
  return found.size === 0 ? undefined : found;
}

/**
 * The setting value with each string item found in `replacements` (after trimming) replaced, and
 * duplicates dropped; other items stay as they are. Undefined when the value is not a list or
 * nothing in it is replaced.
 */
export function replaceExperimental(
  value: unknown,
  replacements: ReadonlyMap<string, string>,
): unknown[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  const original: readonly unknown[] = value;
  const items = original.map((item: unknown) =>
    typeof item === 'string' ? (replacements.get(item.trim()) ?? item) : item,
  );
  return items.some((item, index) => item !== original[index])
    ? items.filter((item, index) => items.indexOf(item) === index)
    : undefined;
}

/**
 * The names `cel-lens.experimental` offers, as package.json lists them. An action of the engine
 * that turns a feature on is followed only for one of these: the extension never writes a name the
 * engine supplies into a person's settings unless the setting itself offers it.
 */
export const KNOWN_EXPERIMENTAL: readonly string[] = [
  'kyverno',
  'crd-typed-policies',
  'fitting-completion',
  'cellens-types',
];

/** The experimental feature that reads the `.cellens` files giving a value a type of your own. */
export const CELLENS_TYPES_FEATURE = 'cellens-types';

/**
 * The setting value with `name` added at the end, the other items as they are; `undefined` when
 * the value is a list that has the name (after trimming) already. A value that is no list is
 * replaced, as the engine reads such a value as none.
 */
export function addExperimental(value: unknown, name: string): unknown[] | undefined {
  if (!Array.isArray(value)) {
    return [name];
  }
  const items: readonly unknown[] = value;
  return items.some((item) => typeof item === 'string' && item.trim() === name)
    ? undefined
    : [...items, name];
}
