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
