import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { COLOR_WORD, experimentalRows } from './experimental_table';

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
  checks: /\bchecks?\b/i,
  hover: /\bhovers?\b/i,
  completion: /\bcomplet(?:e|es|ion)\b|\boffers\b/i,
  typing: /\btypes\b|\btyping\b/i,
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
      const description = settingItems().enumDescriptions[index] ?? '';
      expect(capabilities(description)).toEqual(capabilities(readmeRows().get(name) ?? ''));
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

  it.each([
    ['colored', true],
    ['coloring', true],
    ['colors', true],
    ['colour', true],
    ['collar', false],
  ])('reads "%s" as a color mention: %s', (word, expected) => {
    expect(COLORS.test(`Gives ${word} to documents`)).toBe(expected);
  });
});
