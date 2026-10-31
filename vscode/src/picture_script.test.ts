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
  });

  it('builds every wait error in one helper that names the state and keeps the reason', () => {
    expect(SOURCE.match(/Gave up after/g)).toHaveLength(1);
    expect(withoutFunction(SOURCE, 'withTimeout')).not.toMatch(/\bcatch \(error\)/);
    const start = SOURCE.indexOf('async function withTimeout(');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    expect(body).toMatch(/Gave up after .* waiting for \$\{description\}: \$\{reason\}/);
    expect(body).toMatch(/split\('\\n'\)\[0\]/);
  });

  it('gives the page load and every screenshot a timeout and a name', () => {
    expect(SOURCE).not.toMatch(/await page\.goto\(/);
    expect(
      withoutFunction(withoutFunction(SOURCE, 'screenshotPage'), 'saveGaveUpPicture'),
    ).not.toMatch(/\.screenshot\(/);
    expect(SOURCE).toMatch(/goto\([\s\S]*?\{ timeout: ms \}/);
    expect(SOURCE).toMatch(/screenshot\(\{ type: 'png', timeout: ms \}\)/);
  });

  it('clicks a locator only through clickWhenReady, which names the state', () => {
    const rest = withoutFunction(SOURCE, 'clickWhenReady');
    expect(rest).not.toMatch(/(?<!mouse)\.(click|dblclick|hover|fill|check|tap)\(/);
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
      /waitForWidgetShown\(page, '\.suggest-widget\.visible'[\s\S]*?completionPicture = await screenshotPage/,
    );
  });

  it('closes the side bars only through hideSideBar, which waits for the command row or the bar hiding', () => {
    const rest = withoutFunction(SOURCE, 'hideSideBar');
    expect(rest.match(/View: Close Primary Side Bar/g)).toHaveLength(1);
    expect(rest.match(/View: Hide Secondary Side Bar/g)).toHaveLength(1);
    expect(rest).not.toMatch(/runCommand/);
    const start = SOURCE.indexOf('async function hideSideBar(');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    expect(body).toMatch(/the palette to open[\s\S]*?PALETTE_TIMEOUT_MS/);
    expect(body).toMatch(
      /the palette to list the command \$\{title\} or the \$\{barName\} to hide/,
    );
    expect(body).toMatch(/press\('Escape'\)[\s\S]*?hid by itself[\s\S]*?waitForQuickInputClosed/);
  });

  it('checks both side bars hidden at the end of prepare', () => {
    const start = SOURCE.indexOf('async function prepare(');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    expect(body).toMatch(
      /expectSideBarHidden\(page, '\.part\.sidebar', 'primary side bar'\);\s+await expectSideBarHidden\(page, '\.part\.auxiliarybar', 'secondary side bar'\);\s*$/,
    );
  });

  it('writes into images/ only through save(), and keeps the give-up picture under out/', () => {
    expect(withoutFunction(SOURCE, 'save')).not.toMatch(/join\(IMAGES/);
    expect(SOURCE.match(/join\(IMAGES, /g)).toHaveLength(1);
    expect(SOURCE).toMatch(/const GAVE_UP_PICTURE = 'out\/pictures\/gave-up\.png'/);
    const start = SOURCE.indexOf('async function saveGaveUpPicture(');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    expect(body).not.toMatch(/\bsave\(|IMAGES/);
    expect(body).toMatch(/fullPage: true,\s+timeout: GAVE_UP_TIMEOUT_MS/);
    expect(body).toMatch(/catch \{[\s\S]*?the screenshot failed/);
    expect(SOURCE).toMatch(
      /catch \(error: unknown\) \{\s+await saveGaveUpPicture\(page\);\s+throw error;/,
    );
  });
});
