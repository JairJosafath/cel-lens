// Fails when the engine calls code with a known vulnerability that its osv-scanner.toml does
// not leave aside.
//
//   node .github/scripts/govulncheck-left.mjs engine/osv-scanner.toml govulncheck.json
//
// govulncheck has no ignore list, and an advisory the Go vulnerability database gives no symbols
// marks every call into its module. So CI has govulncheck write what it finds as JSON, which
// never fails, and this script reads it: an advisory the code calls must be one of the
// [[IgnoredVulns]] of the osv-scanner.toml, where each has its reason. An advisory of a module or
// package that is only imported is printed and fails nothing, as in govulncheck's own text output.
import { readFileSync, realpathSync } from 'node:fs';
import process, { argv, stderr, stdout } from 'node:process';
import { pathToFileURL } from 'node:url';

/**
 * The ids of the [[IgnoredVulns]] entries of an osv-scanner.toml.
 *
 * @param {string} toml
 * @returns {string[]}
 */
// eslint-disable-next-line @typescript-eslint/explicit-module-boundary-types -- plain JavaScript has no type syntax: the JSDoc above gives the types
export function ignoredIds(toml) {
  return toml
    .split(/^\s*(?=\[)/m)
    .filter((table) => /^\[\[IgnoredVulns\]\]/.test(table))
    .flatMap((table) => {
      const id = /^\s*id\s*=\s*"([^"]+)"/m.exec(table)?.[1];
      return id === undefined ? [] : [id];
    });
}

/**
 * The values of a text of JSON objects written one after the other, as govulncheck writes them:
 * each over as many lines as it takes.
 *
 * @param {string} text
 * @returns {unknown[]}
 */
// eslint-disable-next-line @typescript-eslint/explicit-module-boundary-types -- plain JavaScript has no type syntax: the JSDoc above gives the types
export function parseStream(text) {
  const values = [];
  let depth = 0;
  let start = 0;
  let inString = false;
  let escaped = false;
  for (let index = 0; index < text.length; index++) {
    const character = text[index];
    if (inString) {
      if (escaped) {
        escaped = false;
      } else if (character === '\\') {
        escaped = true;
      } else if (character === '"') {
        inString = false;
      }
    } else if (character === '"') {
      inString = true;
    } else if (character === '{') {
      if (depth === 0) {
        start = index;
      }
      depth++;
    } else if (character === '}') {
      depth--;
      if (depth < 0) {
        throw new Error(`a } closes nothing at character ${String(index)}`);
      }
      if (depth === 0) {
        values.push(JSON.parse(text.slice(start, index + 1)));
      }
    }
  }
  if (depth > 0 || inString) {
    throw new Error('it ends inside an object');
  }
  return values;
}

/**
 * @param {unknown} value
 * @param {string} key
 * @returns {unknown}
 */
function field(value, key) {
  return typeof value === 'object' && value !== null && key in value
    ? Reflect.get(value, key)
    : undefined;
}

/**
 * Where a finding's vulnerable code is, such as "example.com/module@v1.2.3 (fixed in v1.2.4)".
 *
 * @param {unknown} finding
 * @returns {string}
 */
function placeOf(finding) {
  const frame = field(field(finding, 'trace'), '0');
  const version = field(frame, 'version');
  const fixed = field(finding, 'fixed_version');
  const module = `${String(field(frame, 'module'))}${typeof version === 'string' ? `@${version}` : ''}`;
  return `${module} (${typeof fixed === 'string' ? `fixed in ${fixed}` : 'no fixed version'})`;
}

/**
 * Reads govulncheck's JSON output against an osv-scanner.toml. The lines say what was found; the
 * exit code is 1 when the code calls an advisory the toml does not leave aside, or when the
 * output is not a whole one.
 *
 * @param {string} toml The text of the osv-scanner.toml.
 * @param {string} output The text govulncheck wrote with -format json.
 * @param {string} tomlName The toml's path, for the lines.
 * @returns {{ lines: string[], exitCode: number }}
 */
// eslint-disable-next-line @typescript-eslint/explicit-module-boundary-types -- plain JavaScript has no type syntax: the JSDoc above gives the types
export function check(toml, output, tomlName) {
  let messages;
  try {
    messages = parseStream(output);
  } catch (error) {
    const why = error instanceof Error ? error.message : String(error);
    return { lines: [`govulncheck: the output cannot be read: ${why}.`], exitCode: 1 };
  }
  if (!messages.some((message) => field(message, 'config') !== undefined)) {
    return {
      lines: ['govulncheck: the output has no config message: it is empty or cut off.'],
      exitCode: 1,
    };
  }

  const ignored = new Set(ignoredIds(toml));
  /** @type {Map<string, string>} */
  const summaries = new Map();
  /** @type {Map<string, { called: Set<string>, imported: Set<string> }>} */
  const advisories = new Map();
  for (const message of messages) {
    const id = field(field(message, 'osv'), 'id');
    const summary = field(field(message, 'osv'), 'summary');
    if (typeof id === 'string' && typeof summary === 'string') {
      summaries.set(id, summary);
    }
    const finding = field(message, 'finding');
    const found = field(finding, 'osv');
    if (typeof found === 'string') {
      const places = advisories.get(found) ?? { called: new Set(), imported: new Set() };
      // The first frame is the vulnerable code: with a function, the code calls it.
      const isCalled = typeof field(field(field(finding, 'trace'), '0'), 'function') === 'string';
      (isCalled ? places.called : places.imported).add(placeOf(finding));
      advisories.set(found, places);
    }
  }

  const lines = [];
  const left = [];
  let called = 0;
  for (const [id, places] of [...advisories].sort(([a], [b]) => a.localeCompare(b))) {
    const isCalled = places.called.size > 0;
    const where = [...(isCalled ? places.called : places.imported)].join(' and ');
    const summary = summaries.has(id) ? `, ${String(summaries.get(id))}` : '';
    if (!isCalled) {
      lines.push(`${id} in ${where}${summary}: imported, not called (information only)`);
    } else if (ignored.has(id)) {
      called++;
      lines.push(`${id} in ${where}${summary}: left aside by ${tomlName}`);
    } else {
      called++;
      left.push(id);
      lines.push(`${id} in ${where}${summary}: NOT left aside`);
    }
  }
  const count = `${String(called)} ${called === 1 ? 'advisory' : 'advisories'} called`;
  if (left.length > 0) {
    lines.push(
      `govulncheck: ${count}, ${String(left.length)} NOT left aside by ${tomlName}: ${left.join(', ')}.`,
    );
    return { lines, exitCode: 1 };
  }
  lines.push(`govulncheck: ${count}, ${String(called)} left aside by ${tomlName}.`);
  return { lines, exitCode: 0 };
}

/**
 * @param {string} path
 * @returns {string | undefined} The file's text, or undefined when there is no such file.
 */
function readIfThere(path) {
  try {
    return readFileSync(path, 'utf8');
  } catch (error) {
    if (field(error, 'code') === 'ENOENT') {
      return undefined;
    }
    throw error;
  }
}

/**
 * @param {string[]} paths The osv-scanner.toml and govulncheck's JSON output.
 * @returns {number} The exit code.
 */
function main(paths) {
  const [tomlPath, outputPath] = paths;
  if (tomlPath === undefined || outputPath === undefined || paths.length !== 2) {
    stderr.write('Usage: node govulncheck-left.mjs <osv-scanner.toml> <govulncheck.json>\n');
    return 2;
  }
  const toml = readIfThere(tomlPath);
  const output = readIfThere(outputPath);
  if (output === undefined) {
    stdout.write(`govulncheck: ${outputPath} does not exist: govulncheck wrote no output.\n`);
    return 1;
  }
  const { lines, exitCode } = check(toml ?? '', output, tomlPath);
  if (toml === undefined) {
    lines.unshift(`govulncheck: ${tomlPath} does not exist, so no advisory is left aside.`);
  }
  stdout.write(`${lines.join('\n')}\n`);
  return exitCode;
}

if (argv[1] !== undefined && import.meta.url === pathToFileURL(realpathSync(argv[1])).href) {
  process.exitCode = main(argv.slice(2));
}
