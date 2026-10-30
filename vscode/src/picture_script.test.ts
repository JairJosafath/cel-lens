import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

const SOURCE = readFileSync(`${__dirname}/../test/pictures/take.ts`, 'utf8');

/** The source with the body of the named function cut out. */
function withoutFunction(source: string, name: string): string {
  const start = source.indexOf(`async function ${name}(`);
  expect(start).toBeGreaterThanOrEqual(0);
  const end = source.indexOf('\n}\n', start);
  return source.slice(0, start) + source.slice(end + 3);
}

describe('the picture script', () => {
  it('has no fixed pause outside the helper that paces the GIFs', () => {
    const rest = withoutFunction(SOURCE, 'holdFrame');
    expect(rest).not.toMatch(/waitForTimeout/);
    expect(rest).not.toMatch(/\bpause\(/);
  });

  it('paces GIFs only through holdFrame, called from the recorded steps and typeText', () => {
    const lines = SOURCE.split('\n');
    const callers = lines.filter((line) => line.includes('holdFrame(page'));
    expect(callers.length).toBeGreaterThan(0);
    expect(SOURCE).toMatch(/not a wait for the editor/);
  });

  it('waits on a state only through waitForState, which has a timeout and names the state', () => {
    const rest = withoutFunction(SOURCE, 'waitForState');
    expect(rest).not.toMatch(/\.waitFor\(/);
    expect(SOURCE).toMatch(/Gave up after .* waiting for \$\{description\}/);
  });
});
