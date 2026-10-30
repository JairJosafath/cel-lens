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
    } else if (engineText !== text) {
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
