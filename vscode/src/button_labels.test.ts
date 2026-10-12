import { readdirSync, readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { OPEN_ENGINE_SETTING_LABEL, RESTART_ENGINE_LABEL, SHOW_LOGS_LABEL } from './button_labels';

describe('the shared button labels', () => {
  it.each([
    ['Restart Engine', RESTART_ENGINE_LABEL],
    ['Open Engine Setting', OPEN_ENGINE_SETTING_LABEL],
    ['Show Logs', SHOW_LOGS_LABEL],
  ])('spell %s in exactly one non-test file under src', (text, label) => {
    expect(label).toBe(text);
    const spelled = readdirSync(__dirname)
      .filter((name) => name.endsWith('.ts') && !name.endsWith('.test.ts'))
      .filter((name) => readFileSync(`${__dirname}/${name}`, 'utf8').includes(text));
    expect(spelled).toEqual(['button_labels.ts']);
  });
});
