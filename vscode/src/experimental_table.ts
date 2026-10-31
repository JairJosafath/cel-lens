/** The rows of the "Experimental" table of a README: feature name to its "What it does" cell. */
export function experimentalRows(readme: string): Map<string, string> {
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
    const [name, whatItDoes] = cells;
    if (name === undefined || whatItDoes === undefined) {
      continue;
    }
    if (name === 'Name' || /^:?-+:?$/.test(name)) {
      continue;
    }
    rows.set(name.replace(/`/g, ''), whatItDoes);
  }
  return rows;
}

/** A word about colors, in any form the READMEs and the setting use. */
export const COLOR_WORD = /\bcolou?r(?:s|ed|ing)?\b/i;

/**
 * Coloring is the extension's work and the engine never colors, so the engine README cannot say
 * what a feature does to colors. A row of the extension may therefore add one sentence about
 * colors to the engine's text; any other difference in words is still a difference.
 */
function addsOnlyColorNote(extensionText: string, engineText: string): boolean {
  if (!extensionText.includes(engineText)) {
    return false;
  }
  const added = extensionText.replace(engineText, '').replace(/^[\s.,;]+|[\s.,;]+$/g, '');
  return added === '' || (/^[^.]*$/.test(added) && COLOR_WORD.test(added));
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
