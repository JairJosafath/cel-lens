/** The rows of the "Experimental" table of a README: feature name to the cell at `column`. */
function experimentalColumn(readme: string, column: 1 | 2): Map<string, string> {
  const rows = new Map<string, string>();
  let inSection = false;
  for (const line of readme.split('\n')) {
    if (/^#{1,6}\s/.test(line)) {
      inSection = false;
    }
    if (/^Experimental:\s*$/.test(line)) {
      inSection = true;
    }
    if (!inSection || !line.startsWith('|')) {
      continue;
    }
    const cells = line
      .replace(/^\||\|\s*$/g, '')
      .split('|')
      .map((cell) => cell.trim());
    const name = cells[0];
    const cell = cells[column];
    if (name === undefined || cell === undefined) {
      continue;
    }
    if (name === 'Name' || /^:?-+:?$/.test(name)) {
      continue;
    }
    rows.set(name.replace(/`/g, ''), cell);
  }
  return rows;
}

/** The rows of the "Experimental" table of a README: feature name to its "What it does" cell. */
export function experimentalRows(readme: string): Map<string, string> {
  return experimentalColumn(readme, 1);
}

/** The rows of the "Experimental" table of a README: feature name to its "Why it is experimental" cell. */
export function experimentalReasons(readme: string): Map<string, string> {
  return experimentalColumn(readme, 2);
}

const REASON_MARKER = 'experimental because ';

/** The reason a setting description gives: the text after "experimental because". */
export function descriptionReason(description: string): string | undefined {
  const at = description.indexOf(REASON_MARKER);
  return at < 0 ? undefined : description.slice(at + REASON_MARKER.length);
}

function normalizeReason(text: string): string {
  return text
    .replace(/`/g, '')
    .replace(/\.\s*$/, '')
    .trim()
    .toLowerCase();
}

/** Whether two reasons are the same words, allowing only case, backticks and a trailing period to differ. */
export function sameReason(description: string, row: string): boolean {
  return normalizeReason(description) === normalizeReason(row);
}

/** A word about colors, in any form the READMEs and the setting use. */
export const COLOR_WORD = /\bcolou?r(?:s|ed|ing)?\b|\bcolorStyle\b/i;

/** Words for what the engine does; a note about colors only does not speak of them. */
const ENGINE_WORD =
  /\b(?:hover|completions?|completes?|diagnostics?|errors?|checks?|checked|fix(?:es)?)\b/i;

/**
 * Coloring is the extension's work and the engine never colors, so the engine README cannot say
 * what a feature does to colors. A row of the extension may therefore add one sentence about
 * colors only (it may name a setting such as `cel-lens.colorStyle`) to the engine's text; any other difference in words is still a difference.
 */
function addsOnlyColorNote(extensionText: string, engineText: string): boolean {
  if (!extensionText.includes(engineText)) {
    return false;
  }
  const added = extensionText.replace(engineText, '').replace(/^[\s.,;]+|[\s.,;]+$/g, '');
  return (
    added === '' || (!/[.]\s/.test(added) && COLOR_WORD.test(added) && !ENGINE_WORD.test(added))
  );
}

/** What differs between two tables, one line per feature missing from a side or worded differently. */
export function compareExperimentalRows(
  extension: Map<string, string>,
  engine: Map<string, string>,
): string[] {
  const problems: string[] = [];
  for (const [name, text] of extension) {
    const engineText = engine.get(name);
    if (engineText === undefined) {
      problems.push(`${name}: the engine README lacks this feature`);
    } else if (!addsOnlyColorNote(text, engineText)) {
      problems.push(`${name}: differs\n  extension: ${text}\n  engine:    ${engineText}`);
    }
  }
  for (const name of engine.keys()) {
    if (!extension.has(name)) {
      problems.push(`${name}: the extension README lacks this feature`);
    }
  }
  return problems;
}

/** What a hand-written list of feature names gets wrong against the engine's names: one line per extra or missing name. */
export function featureNameProblems(
  list: string,
  listed: readonly string[],
  engine: readonly string[],
): string[] {
  return [
    ...listed
      .filter((name) => !engine.includes(name))
      .map((name) => `${list} lists "${name}", which the engine does not know (extra)`),
    ...engine
      .filter((name) => !listed.includes(name))
      .map((name) => `${list} lacks "${name}", which the engine knows (missing)`),
  ];
}
