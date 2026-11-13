import { readFileSync } from 'node:fs';

import ts from 'typescript';
import { describe, expect, it } from 'vitest';

const SOURCE = readFileSync(`${__dirname}/../test/pictures/take.ts`, 'utf8');

/** The source with the body of the named function cut out. */
function withoutFunction(source: string, name: string): string {
  const start = source.search(new RegExp(`async function ${name}(<T>)?\\(`));
  expect(start).toBeGreaterThanOrEqual(0);
  const end = source.indexOf('\n}\n', start);
  return source.slice(0, start) + source.slice(end + 3);
}

/** The hold argument of a call: the second of `holdFrame`, the fourth of `typeText`. */
const HOLD_ARGUMENT = new Map([
  ['holdFrame', 1],
  ['typeText', 3],
]);

/** The hold arguments of `holdFrame` and `typeText` calls that are not a plain name from gif_plans. */
function inlineHolds(source: string): string[] {
  const file = ts.createSourceFile('take.ts', source, ts.ScriptTarget.Latest, true);
  const found: string[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)) {
      const argument = node.arguments[HOLD_ARGUMENT.get(node.expression.text) ?? -1];
      if (
        argument !== undefined &&
        !ts.isIdentifier(argument) &&
        !ts.isPropertyAccessExpression(argument)
      ) {
        found.push(argument.getText(file));
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(file);
  return found;
}

/** The names `source` imports from `../../src/gif_plans.ts`. */
function plannedNames(file: ts.SourceFile): Set<string> {
  const names = new Set<string>();
  for (const statement of file.statements) {
    if (
      ts.isImportDeclaration(statement) &&
      ts.isStringLiteral(statement.moduleSpecifier) &&
      statement.moduleSpecifier.text === '../../src/gif_plans.ts' &&
      statement.importClause?.namedBindings !== undefined &&
      ts.isNamedImports(statement.importClause.namedBindings)
    ) {
      for (const element of statement.importClause.namedBindings.elements) {
        names.add(element.name.text);
      }
    }
  }
  return names;
}

/** The name at the root of an identifier or a property access, or undefined for any other expression. */
function rootName(expression: ts.Expression): string | undefined {
  if (ts.isIdentifier(expression)) {
    return expression.text;
  }
  return ts.isPropertyAccessExpression(expression) ? rootName(expression.expression) : undefined;
}

/** Whether `name` is a parameter of a function around `node`, which its callers must then fill from gif_plans. */
function isEnclosingParameter(node: ts.Node, name: string): boolean {
  return (
    ts.findAncestor(
      node,
      (ancestor) =>
        ts.isFunctionLike(ancestor) &&
        ancestor.parameters.some((parameter) => parameter.name.getText() === name),
    ) !== undefined
  );
}

/** The hold names of `holdFrame` and `typeText` calls that are neither imported from gif_plans nor a parameter. */
function unplannedHolds(source: string): string[] {
  const file = ts.createSourceFile('take.ts', source, ts.ScriptTarget.Latest, true);
  const planned = plannedNames(file);
  const found: string[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)) {
      const argument = node.arguments[HOLD_ARGUMENT.get(node.expression.text) ?? -1];
      const name = argument === undefined ? undefined : rootName(argument);
      if (name !== undefined && !planned.has(name) && !isEnclosingParameter(node, name)) {
        found.push(name);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(file);
  return found;
}

/** The `Math.max(…, 0)` calls in the source: a floor of 0 turns a spent deadline into "no timeout" in Playwright. */
function zeroFloors(source: string): string[] {
  const file = ts.createSourceFile('take.ts', source, ts.ScriptTarget.Latest, true);
  const found: string[] = [];
  const visit = (node: ts.Node): void => {
    if (
      ts.isCallExpression(node) &&
      node.expression.getText(file) === 'Math.max' &&
      node.arguments.some((argument) => argument.getText(file) === '0')
    ) {
      found.push(node.getText(file));
    }
    ts.forEachChild(node, visit);
  };
  visit(file);
  return found;
}

describe('the picture script', () => {
  it.each([
    [
      'a floored timeout in waitForFunction',
      'page.waitForFunction(e, undefined, { timeout: Math.max(deadline - now(), 0) });',
    ],
    [
      'a floored timeout in waitForState',
      'await waitForState(locator, "x", "visible", Math.max(0, left));',
    ],
  ])('finds %s', (_name, source) => {
    expect(zeroFloors(source)).toHaveLength(1);
  });

  it('passes no Math.max(…, 0) value as a Playwright timeout', () => {
    expect(zeroFloors(SOURCE)).toEqual([]);
  });

  it('rejects a timeout of 0 or less in withTimeout before calling Playwright', () => {
    const start = SOURCE.indexOf('async function withTimeout');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    const guard = body.indexOf('timeout <= 0');
    expect(guard).toBeGreaterThanOrEqual(0);
    expect(guard).toBeLessThan(body.indexOf('action(timeout)'));
  });

  it.each([
    ['holdFrame(recording, 400)', 1],
    ['holdFrame(recording, 1_000)', 1],
    ['holdFrame(recording, GIF_TYPING_MS * 2)', 1],
    ['typeText(page, recording, TEXT, 50)', 1],
    ["typeText(page, recording, 'a, b', 50)", 1],
    ['typeText(\n page,\n recording,\n TEXT,\n 50,\n)', 1],
    ['typeText(page, recording, TEXT, GIF_TYPING_MS + 1)', 1],
    ['holdFrame(recording, COLORS_HOLD_AFTER_MS)', 0],
    ['holdFrame(recording, COMPLETION_HOLDS_MS.start)', 0],
    ['typeText(page, recording, TEXT, GIF_TYPING_MS)', 0],
    ["typeText(page, recording, 'a, b', GIF_TYPING_MS)", 0],
  ])('finds the inline holds in %j: %i', (source, count) => {
    expect(inlineHolds(source)).toHaveLength(count);
  });

  it('takes every hold from src/gif_plans.ts, never from a number in the script', () => {
    expect(inlineHolds(SOURCE)).toEqual([]);
  });

  it.each([
    ["import { SLOW_MS } from '../../src/gif_plans.ts';\nholdFrame(recording, SLOW_MS);", 0],
    ["import { HOLDS } from '../../src/gif_plans.ts';\nholdFrame(recording, HOLDS.start);", 0],
    ['const SLOW = 500;\nholdFrame(recording, SLOW);', 1],
    [
      "import { A } from '../../src/gif_plans.ts';\nconst SLOW = 5;\ntypeText(page, recording, A, SLOW);",
      1,
    ],
    ['const HOLDS = { a: 1 };\nholdFrame(recording, HOLDS.a);', 1],
    ["import { SLOW } from './other.ts';\nholdFrame(recording, SLOW);", 1],
    ['async function typeText(a, b, c, delay) {\n await holdFrame(b, delay);\n}', 0],
  ])('finds the holds not from gif_plans in %j: %i', (source, count) => {
    expect(unplannedHolds(source)).toHaveLength(count);
  });

  it('imports every hold name from src/gif_plans.ts, never declares one in the script', () => {
    expect(unplannedHolds(SOURCE)).toEqual([]);
  });

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

  it('waits on a page function only through waitForExpression, which has a timeout and names it', () => {
    const rest = withoutFunction(
      withoutFunction(withoutFunction(SOURCE, 'waitForExpression'), 'askForQuickFix'),
      'hideSideBar',
    );
    expect(rest).not.toMatch(/\.waitForFunction\(/);
  });

  it('names each screenshot by its picture file and has no second error for a missing picture', () => {
    expect(SOURCE).not.toMatch(/No picture of/);
    expect(SOURCE).not.toMatch(/the page to be captured/);
    const files = [...SOURCE.matchAll(/screenshotPage\(page, '([^']+)'\)/g)].map((m) => m[1]);
    expect(files).toEqual([
      'inline-info.png',
      'example-results-line.png',
      'example-results-picker.png',
      'problems.png',
      'quick-fix.png',
      'hover.png',
      'completion.png',
      'colors.png',
    ]);
  });

  it('builds every wait error in one helper that names the state and keeps the reason', () => {
    expect(SOURCE.match(/Gave up after/g)).toHaveLength(1);
    expect(withoutFunction(SOURCE, 'withTimeout')).not.toMatch(/\bcatch \(error\)/);
    const start = SOURCE.indexOf('async function withTimeout<T>(');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    expect(body).toMatch(/Gave up after .* waiting for \$\{description\}: \$\{reason\}/);
    expect(body).toMatch(/const reason = firstMessageLine\(error\);/);
  });

  it('formats the first line of an error message in one function, used by both give-up reasons', () => {
    expect(SOURCE.match(/split\('\\n'\)\[0\]/g)).toHaveLength(1);
    const start = SOURCE.indexOf('function firstMessageLine(');
    expect(start).toBeGreaterThanOrEqual(0);
    expect(SOURCE.slice(start, SOURCE.indexOf('\n}\n', start))).toMatch(/split\('\\n'\)\[0\]/);
    expect(SOURCE.match(/const reason = firstMessageLine\(\w+\);/g)).toHaveLength(2);
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
      /type\('28'\);\s+await page\.keyboard\.press\('Enter'\);\s+await waitForQuickInputClosed\(page\);\s+await waitForEngine\(page\);/,
    );
  });

  it('counts no editor lines, which depend on the scroll position', () => {
    expect(SOURCE).not.toMatch(/waitForEditorLineCount|countEditorLines/);
  });

  it('waits after discarding what the completion typed for the last rule by the cursor line, not by a count', () => {
    expect(SOURCE).not.toMatch(/Control\+z/);
    expect(SOURCE).toMatch(
      /startPicture\(page, CRD\);\s+await moveToLastRule\(page\);\s+await waitForCursorLineIs\(page, lastRuleBefore\);/,
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
      /the palette to list the command \$\{title\} or the \$\{barName\} to hide[\s\S]*?hideSideBarWithRetry/,
    );
    expect(body).toMatch(/press\('Escape'\)[\s\S]*?hid by itself[\s\S]*?waitForQuickInputClosed/);
  });

  it('decides that a side bar is off screen in one function, partHidden, for both waits', () => {
    expect(SOURCE).toMatch(/import \{ partHidden \} from '\.\.\/\.\.\/src\/part_hidden\.ts'/);
    const hide = SOURCE.slice(
      SOURCE.indexOf('async function hideSideBar('),
      SOURCE.indexOf('async function expectSideBarHidden('),
    );
    const expectHidden = SOURCE.slice(
      SOURCE.indexOf('async function expectSideBarHidden('),
      SOURCE.indexOf('async function takePictures('),
    );
    expect(SOURCE.match(/\$\{partHidden\.toString\(\)\}/g)).toHaveLength(1);
    expect(SOURCE).toMatch(/function partHiddenExpression\(part: string\): string \{/);
    expect(hide).toMatch(/page\.evaluate\(partHiddenExpression\(part\)\)/);
    expect(hide).toMatch(/const hidden = \$\{partHiddenExpression\(part\)\};/);
    expect(expectHidden).toMatch(/partHiddenExpression\(part\)/);
    expect(SOURCE).not.toMatch(/box\.width === 0/);
    expect(SOURCE).not.toMatch(/\.isVisible\(\)/);
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
    expect(body).toMatch(
      /catch \(failure: unknown\) \{\s+const reason = firstMessageLine\(failure\);[\s\S]*?the screenshot failed: \$\{reason\}/,
    );
    expect(SOURCE).toMatch(
      /catch \(error: unknown\) \{\s+await saveGaveUpPicture\(page\);\s+throw error;/,
    );
  });

  it('waits for the light bulb, then asks for the quick fix within the one named timeout', () => {
    expect(SOURCE).toMatch(
      /page\.mouse\.click\(wrong\.x, wrong\.y\);\s+await waitForLightBulbOnCursorLine\(page\);\s+await askForQuickFix\(page\);/,
    );
    expect(SOURCE).toMatch(
      /withTimeout\('the quick fix list to show a fix', STATE_TIMEOUT_MS, \(ms\) =>\s+askUntilFixListed\(/,
    );
    expect(SOURCE).toMatch(/askForFixes: \(\) => page\.keyboard\.press\('Control\+\.'\)/);
    expect(SOURCE).toMatch(/dismiss: \(\) => page\.keyboard\.press\('Escape'\)/);
    const start = SOURCE.indexOf('async function waitForLightBulbOnCursorLine(');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    expect(body).toMatch(/widgetShown\.toString\(\)/);
    expect(body).toMatch(/\.lightBulbWidget/);
    expect(body).toMatch(/the light bulb to be fully shown on the cursor line/);
  });
});

describe('the settings of the pictures', () => {
  it.each([
    [
      'sticky scroll off, so no line of a picture is cut in half under its header',
      'editor.stickyScroll.enabled',
    ],
    ['the highlight of the word the cursor was on off', 'editor.occurrencesHighlight'],
  ])('turn %s: %s', (_name, setting) => {
    const settings = SOURCE.slice(
      SOURCE.indexOf('const SETTINGS = {'),
      SOURCE.indexOf('\n};', SOURCE.indexOf('const SETTINGS = {')),
    );
    expect(settings).toMatch(new RegExp(`'${setting.replaceAll('.', '\\.')}': (false|'off')`));
  });
});

describe('the pictures start clean', () => {
  it.each([
    'inline-info.png',
    'example-results-line.png',
    'problems.png',
    'completion.gif',
    'colors.gif',
    'colors.png',
    'give-type.gif',
  ])('checks that %s starts from a clean editor, and fails naming it when it does not', (name) => {
    expect(SOURCE).toContain(`expectCleanStart(page, '${name}', {`);
    const start = SOURCE.indexOf('async function expectCleanStart(');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    expect(body).toMatch(/`\$\{picture\} to start from a clean editor: /);
  });

  it('checks one tab, nothing unsaved, the problem count of the status bar and no notification', () => {
    const start = SOURCE.indexOf('async function expectCleanStart(');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    expect(body).toContain(".tab').length === 1");
    expect(body).toContain(".tab.dirty').length === 0");
    expect(body).toContain('.notifications-toasts.visible');
    expect(body).toContain('problemCountExpression(problems)');
    expect(SOURCE).toContain("getElementById('status.problems')");
  });

  it.each([
    'inline-info.png',
    'give-type.gif',
    'colors.png',
    'problems.png',
    'completion.gif',
    'colors.gif',
  ])(
    'checks that no line of %s is cut by an edge of the editor, and fails naming it when one is',
    (name) => {
      expect(SOURCE).toMatch(
        new RegExp(`(scrollToEnd|showWholeLines)\\(page, '${name.replace('.', '\\.')}'\\)`),
      );
    },
  );

  it('measures the cut of the first and last visible lines, and of the lines above the rules', () => {
    const start = SOURCE.indexOf('const CUT_LINES = ');
    const body = SOURCE.slice(start, SOURCE.indexOf('})()`;', start));
    expect(body).toContain('.view-line');
    expect(body).toContain('.codelens-decoration');
    expect(body).toContain('area.top - box.top');
    expect(body).toContain('box.bottom - area.bottom');
  });

  it('starts each picture group from startPicture, which closes every editor and discards what is unsaved', () => {
    const start = SOURCE.indexOf('async function closeAllEditors(');
    const body = SOURCE.slice(start, SOURCE.indexOf('\n}\n', start));
    expect(body).toContain("Don't Save");
    expect(SOURCE.match(/startPicture\(page, /g)?.length).toBeGreaterThanOrEqual(5);
    expect(SOURCE).not.toMatch(/await openFile\(page, (CRD|POLICY|PARAMS_POLICY)\)/);
  });
});

describe('the picture script docs', () => {
  it('puts withTimeout directly under its JSDoc, with firstMessageLine and its one-line comment above', () => {
    expect(SOURCE).toMatch(/\*\/\nasync function withTimeout<T>\(/);
    expect(SOURCE).toMatch(
      /\n\n\/\*\* The first line of an error's message[^\n]*\*\/\nfunction firstMessageLine\(/,
    );
  });
});
