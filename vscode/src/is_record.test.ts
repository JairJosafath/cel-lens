import { describe, expect, it } from 'vitest';
import { isRecord } from './is_record';

describe('isRecord', () => {
  it.each([
    ['null', null, false],
    ['undefined', undefined, false],
    ['an empty array', [], false],
    ['an array of objects', [{}], false],
    ['a string', 'text', false],
    ['a number', 1, false],
    ['a boolean', true, false],
    ['a function', () => 1, false],
    ['an empty object', {}, true],
    ['an object with keys', { a: 1 }, true],
    ['an object without a prototype', Object.create(null), true],
  ])('%s gives %s', (_name, value, expected) => {
    expect(isRecord(value)).toBe(expected);
  });
});
