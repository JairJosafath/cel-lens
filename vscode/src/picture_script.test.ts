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
    const callers = lines.filter((line) => line.includes('holdFrame(recording'));
    expect(callers.length).toBeGreaterThan(0);
    expect(SOURCE).toMatch(/not a wait for the editor/);
  });

  it('waits on a state only through waitForState, which has a timeout and names the state', () => {
    const rest = withoutFunction(SOURCE, 'waitForState');
    expect(rest).not.toMatch(/\.waitFor\(/);
    expect(SOURCE).toMatch(/Gave up after .* waiting for \$\{description\}/);
  });

  it('clicks a locator only through clickWhenReady, which names the state', () => {
    const rest = withoutFunction(SOURCE, 'clickWhenReady');
    expect(rest).not.toMatch(/(?<!mouse)\.(click|dblclick|hover|fill|check|tap)\(/);
    const start = SOURCE.indexOf('async function clickWhenReady(');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    expect(body).toMatch(/Gave up after .* waiting for \$\{description\}/);
  });

  it('reads a locator only after waitForState has waited for it', () => {
    const rest = withoutFunction(SOURCE, 'pointAt');
    expect(rest).not.toMatch(/\.(boundingBox|textContent|innerText|getAttribute)\(/);
    const start = SOURCE.indexOf('async function pointAt(');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    expect(body.indexOf('await waitForState(row')).toBeGreaterThanOrEqual(0);
    expect(body.indexOf('await waitForState(row')).toBeLessThan(body.indexOf('.boundingBox('));
  });

  it('waits for the quick input to close after the go-to-line Enter, before the engine', () => {
    expect(SOURCE).toMatch(
      /type\('31'\);\s+await page\.keyboard\.press\('Enter'\);\s+await waitForQuickInputClosed\(page\);\s+await waitForEngine\(page\);/,
    );
  });

  it('counts no editor lines, which depend on the scroll position', () => {
    expect(SOURCE).not.toMatch(/waitForEditorLineCount|countEditorLines/);
  });

  it('waits after the undos for the last rule by the cursor line, not by a count', () => {
    expect(SOURCE).toMatch(
      /Control\+z[\s\S]*?moveToLastRule\(page\);\s+await waitForCursorLineIs\(page, lastRuleBefore\);/,
    );
  });

  it('waits until a hover or a list is fully shown, with an error naming the widget', () => {
    expect(SOURCE).toMatch(/\$\{widgetShown\.toString\(\)\}/);
    expect(SOURCE).toMatch(/to be fully shown/);
    expect(SOURCE).toMatch(
      /async function waitForHover[\s\S]*?waitForWidgetShown\(page, '\.monaco-hover:not\(\.hidden\)'/,
    );
    expect(SOURCE).toMatch(
      /waitForWidgetShown\(page, '\.action-widget'[^;]*;\s+await save\('quick-fix\.png'/,
    );
    expect(SOURCE).toMatch(
      /waitForWidgetShown\(page, '\.suggest-widget\.visible'[\s\S]*?completionPicture = await page\.screenshot/,
    );
  });
});
