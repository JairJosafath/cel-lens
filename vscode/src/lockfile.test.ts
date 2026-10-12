import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

function readJson(name: string): unknown {
  return JSON.parse(readFileSync(`${__dirname}/../${name}`, 'utf8'));
}

function field(value: unknown, key: string): unknown {
  return typeof value === 'object' && value !== null && key in value
    ? Reflect.get(value, key)
    : undefined;
}

function lockRoot(lock: unknown): unknown {
  return field(field(lock, 'packages'), '');
}

describe('package-lock.json', () => {
  it.each(['name', 'version', 'license'])(
    'records the same %s as package.json for the root package',
    (key) => {
      const expected = field(readJson('package.json'), key);
      expect(typeof expected).toBe('string');
      expect(field(lockRoot(readJson('package-lock.json')), key)).toBe(expected);
    },
  );
});
