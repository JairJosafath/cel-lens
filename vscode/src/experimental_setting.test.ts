import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { KNOWN_EXPERIMENTAL } from './experimental';
import {
  COLOR_WORD,
  descriptionReason,
  experimentalReasons,
  experimentalRows,
  sameReason,
} from './experimental_table';

interface ExperimentalItems {
  enum: string[];
  enumDescriptions: string[];
}

function settingItems(): ExperimentalItems {
  const manifest: unknown = JSON.parse(readFileSync(join(__dirname, '..', 'package.json'), 'utf8'));
  const items = (
    manifest as {
      contributes: {
        configuration: { properties: Record<string, { items: ExperimentalItems }> };
      };
    }
  ).contributes.configuration.properties['cel-lens.experimental']?.items;
  if (items === undefined) {
    throw new Error('package.json has no cel-lens.experimental setting');
  }
  return items;
}

function readmeRows(): Map<string, string> {
  return experimentalRows(readFileSync(join(__dirname, '..', 'README.md'), 'utf8'));
}

const COLORS = COLOR_WORD;

const CAPABILITIES = {
  checks: /\bchecks?\b|\bdiagnostics?\b/i,
  hover: /\bhovers?\b/i,
  completion: /\bcomplet(?:e|es|ion)\b/i,
  typing: /\btypes\b|\btyping\b|\btyped\b/i,
};

function capabilities(text: string): string[] {
  return Object.entries(CAPABILITIES)
    .filter(([, pattern]) => pattern.test(text))
    .map(([capability]) => capability);
}

/** The features the README says gate coloring: "... are colored only with `name` in ...". */
function coloredFeatures(): Set<string> {
  const readme = readFileSync(join(__dirname, '..', 'README.md'), 'utf8');
  return new Set(
    [...readme.matchAll(/colored only with `([a-z-]+)`/g)].map((match) => match[1] ?? ''),
  );
}

describe('the cel-lens.experimental setting against the README', () => {
  it('lists the same features as the README Experimental table', () => {
    expect([...settingItems().enum].sort()).toEqual([...readmeRows().keys()].sort());
  });

  it('has one description per feature', () => {
    const items = settingItems();
    expect(items.enumDescriptions).toHaveLength(items.enum.length);
  });

  it.each(settingItems().enum.map((name, index) => [name, index] as const))(
    'says about %s what the README says on colors',
    (name, index) => {
      const description = settingItems().enumDescriptions[index] ?? '';
      expect(COLORS.test(description)).toBe(coloredFeatures().has(name));
    },
  );

  it.each(settingItems().enum.map((name, index) => [name, index] as const))(
    'names for %s the same capabilities as the README',
    (name, index) => {
      // What the feature does, not why it is experimental: a reason may say "checks" of what it lacks.
      const description = (settingItems().enumDescriptions[index] ?? '').split(
        'experimental because',
      )[0];
      expect(capabilities(description ?? '')).toEqual(capabilities(readmeRows().get(name) ?? ''));
    },
  );

  it.each([...coloredFeatures()])(
    'says in the README that %s off means no colors, in both tables',
    (name) => {
      const readme = readFileSync(join(__dirname, '..', 'README.md'), 'utf8');
      const colorsRow = readme.split('\n').find((line) => /^\| Colors\s/.test(line)) ?? '';
      expect(readmeRows().get(name) ?? '').toMatch(COLORS);
      expect(colorsRow).toContain(`\`${name}\``);
    },
  );

  it.each(settingItems().enum.map((name, index) => [name, index] as const))(
    'gives for %s the reason the README row gives',
    (name, index) => {
      const readme = readFileSync(join(__dirname, '..', 'README.md'), 'utf8');
      const reason = descriptionReason(settingItems().enumDescriptions[index] ?? '');
      expect(reason).toBeDefined();
      const row = experimentalReasons(readme).get(name);
      expect(row, `the README Experimental row for ${name} has no Why cell`).toBeTruthy();
      expect(sameReason(reason ?? '', row ?? '')).toBe(true);
    },
  );

  it.each([
    [
      'the v0.27.0 crd-typed-policies wording',
      "the workspace scan blocks the engine's start while it runs.",
      'The workspace scan runs only while the feature is on, at initialize or when the setting turns it on',
      false,
    ],
    [
      'the README wording',
      'The workspace scan runs only while the feature is on, at initialize or when the setting turns it on',
      'The workspace scan runs only while the feature is on, at initialize or when the setting turns it on',
      true,
    ],
    [
      'case, backticks and a final period',
      'only `ValidatingPolicy` is read.',
      'Only ValidatingPolicy is read',
      true,
    ],
  ])('compares reasons: %s', (_label, description, row, expected) => {
    expect(sameReason(description, row)).toBe(expected);
  });

  it.each([
    ['Shows diagnostics in the editor', ['checks']],
    ['Shows a diagnostic in the editor', ['checks']],
    ['Gives typed variables', ['typing']],
    ['Offers fields', []],
    ['Offers completion after a dot', ['completion']],
  ])('reads "%s" as the capabilities %j', (text, expected) => {
    expect(capabilities(text)).toEqual(expected);
  });

  it.each([
    ['colored', true],
    ['coloring', true],
    ['colors', true],
    ['colour', true],
    ['collar', false],
  ])('reads "%s" as a color mention: %s', (word, expected) => {
    expect(COLORS.test(`Gives ${word} to documents`)).toBe(expected);
  });

  it('has the names of the setting as the list an engine action is checked against', () => {
    expect([...KNOWN_EXPERIMENTAL]).toEqual(settingItems().enum);
  });
});
