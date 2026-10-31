import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { experimentalRows } from './experimental_table';

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

const COLORS = /\bcolou?rs?\b/i;

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
});
