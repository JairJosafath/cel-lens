import { describe, expect, it } from 'vitest';
import { experimentalSettingEnum } from './manifest_setting';

function manifestWith(setting: unknown): unknown {
  return { contributes: { configuration: { properties: { 'cel-lens.experimental': setting } } } };
}

describe('experimentalSettingEnum', () => {
  it.each([
    ['the listed names', manifestWith({ items: { enum: ['a', 'b'] } }), ['a', 'b']],
    ['no manifest', undefined, []],
    ['a string', 'x', []],
    ['an array', [], []],
    ['null', null, []],
    ['no contributes', {}, []],
    ['contributes not an object', { contributes: 3 }, []],
    ['no configuration', { contributes: {} }, []],
    ['configuration as an array', { contributes: { configuration: [] } }, []],
    ['no properties', { contributes: { configuration: {} } }, []],
    ['no such setting', { contributes: { configuration: { properties: {} } } }, []],
    ['no items', manifestWith({}), []],
    ['no enum', manifestWith({ items: {} }), []],
    ['enum not an array', manifestWith({ items: { enum: 'a' } }), []],
    ['enum with a non-string', manifestWith({ items: { enum: ['a', 1] } }), []],
  ])('reads %s as %j', (_name, manifest, expected) => {
    expect(experimentalSettingEnum(manifest)).toEqual(expected);
  });
});
