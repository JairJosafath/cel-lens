// Takes the pictures of the README: `npm run pictures`.
//
// Builds the extension, installs it in openvscode-server (a VS Code served to a
// browser), opens the fixtures of this folder in headless Chromium driven by
// Playwright, and writes the PNGs and GIFs of the README into images/. Not part of
// `make check`: it fetches the server and Chromium, so it needs a network, and it
// is run on purpose. The engine is the pinned one: `npm run engine` puts it in bin/.
// `npm run pictures -- cellens-types` takes only that GIF, `npm run pictures -- menus` only
// actions-menu.png and editor-menu.png. `npm run pictures -- themes` takes six scenes
// in each of the four default themes into out/pictures/themes/, never images/. `CEL_LENS_CHROME` names a Chrome or
// Chromium to run in place of the one Playwright downloads.
import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { stdout } from 'node:process';

import gifenc from 'gifenc';
import { chromium, errors, type CDPSession, type Locator, type Page } from 'playwright';

import { cursorLineText } from '../../src/cursor_line.ts';
import { askUntilFixListed } from '../../src/quick_fix_retry.ts';
import {
  CELLENS_TYPES_FILTER,
  CELLENS_TYPES_HOLDS_MS,
  CELLENS_TYPES_TYPED_AFTER,
  CELLENS_TYPES_TYPED_BEFORE,
  CELLENS_TYPES_TYPING_MS,
  COLORS_HOLD_AFTER_MS,
  COLORS_HOLD_BEFORE_MS,
  COLORS_TYPED_TEXT,
  COMPLETION_HOLDS_MS,
  GIF_TYPING_MS,
  GIVE_TYPE_FILTER,
  GIVE_TYPE_HOLDS_MS,
} from '../../src/gif_plans.ts';
import { hideSideBarWithRetry } from '../../src/side_bar_retry.ts';
import { partHidden } from '../../src/part_hidden.ts';
import { widgetShown } from '../../src/widget_shown.ts';
import { indexFrames, type RawFrame } from '../../src/gif_frames.ts';
import { decodePng } from '../../src/png_frame.ts';
import {
  checkPlannedLength,
  pickFrames,
  VirtualClock,
  type StampedFrame,
} from '../../src/gif_timeline.ts';

const { GIFEncoder } = gifenc;

const ROOT = resolve(import.meta.dirname, '../..');
const FIXTURES = import.meta.dirname;
const IMAGES = join(ROOT, 'images');
const CACHE = join(ROOT, 'node_modules/.cache/cel-lens-pictures');

const VIEWPORT = { width: 1100, height: 640 };
/** Less than this many pixels of a line at the edge of the editor is the rest of a line scrolled out, not a line cut in half. */
const SLIVER_PX = 4;
/** The height of the status bar, which covers the bottom of the window. */
const STATUS_BAR_HEIGHT = 22;
const GIF_COLORS = 48;
const GIF_TICK_MS = 100;
/** How long a state the script waits for may take, on a slow machine. */
const STATE_TIMEOUT_MS = 30_000;
const WORKBENCH_TIMEOUT_MS = 60_000;
const PALETTE_TIMEOUT_MS = 30_000;
const GAVE_UP_TIMEOUT_MS = 5_000;
/** Where the page is kept when the script stops; outside images/ and ignored by git. */
const GAVE_UP_PICTURE = 'out/pictures/gave-up.png';

/** The pictures asked for on the command line, such as `cellens-types`; none means all of them. */
const ONLY = process.argv.slice(2);
/** The names `npm run pictures --` takes: the pictures that can be taken alone. */
const TAKEN_ALONE = ['cellens-types', 'rule-view', 'menus', 'themes'];

const CRD = 'widget_crd.yaml';
const POLICY = 'pod_policy.yaml';
/** A policy whose `params` is a custom kind, so the line above its rule offers Give it a type. */
const PARAMS_POLICY = 'container_policy.yaml';
/** A ValidatingAdmissionPolicy that reads `params.maxReplicas` of a custom kind, which a schema in `.cellens` types. */
const REPLICA_POLICY = 'replica_policy.yaml';
/** A document of that kind: the workspace's example, at the path a person keeps their samples. */
const SAMPLE = 'limit_sample.yaml';
const SAMPLE_IN_WORKSPACE = 'config/samples/limit.yaml';
/** A CRD whose rule is run on the examples of `config/samples/`: two that the rule allows, and one, `big.yaml`, that it refuses though every example expects to be allowed. */
const LIMIT_CRD = 'limit_crd.yaml';
const LIMIT_EXAMPLES: readonly (readonly [string, string])[] = [
  ['small', 'small.yaml'],
  ['fine', 'fine.yaml'],
  ['big', 'big.yaml'],
];
const EXAMPLES_FOLDER = 'config/samples';
/** A Pod the policy of POLICY allows, in the workspace only for the theme pictures' rule view: its rules then show the passing state. */
const POD_SAMPLE: readonly [string, string] = ['pod_sample.yaml', 'pod.yaml'];
/** A ValidatingAdmissionPolicy, which is in the workspace from the start (quick open does not find a file made later), and its bindings, its param and the Deployments it is run on, which are there only for its picture. */
const DEPLOY_POLICY = 'deploy_policy.yaml';
const DEPLOY_FILES: readonly (readonly [string, string])[] = [
  ['deploy_bindings.yaml', 'bindings.yaml'],
  ['deploy_limits.yaml', 'config/params/limits.yaml'],
  ['deploy_small.yaml', 'config/deployments/small.yaml'],
  ['deploy_big.yaml', 'config/deployments/big.yaml'],
];
/** The warnings the policy's wrong verdict makes in the status bar: one on the policy and one on the Deployment. */
const DEPLOY_WARNINGS = 2;
/** The line of the rule in limit_crd.yaml, and the warnings its wrong verdict makes in the status bar: one on the rule and one on the example. */
const EXAMPLE_RULE_LINE = 22;
/** The line of the rule that does not compile in the CRD of `rule-view.png`, which the cursor is on, and the label of its row. */
const RULE_VIEW_LINE = 28;
/** Where the right edge of the side bar is at the viewport's size (its activity bar is 48 px), and how much wider it is made for `rule-view.png`. */
const RULE_VIEW_SIDE_BAR_EDGE_X = 322;
const RULE_VIEW_WIDER_PX = 260;
const RULE_VIEW_SELECTED = 'a widget runs at least one copy';
/** The documents of the view that are opened for `rule-view.png`, besides the CRD the cursor is in. */
const RULE_VIEW_OPEN = ['limits.example.com', 'no-latest-tag'];

/** The four default themes of `npm run pictures -- themes`: the name in a file, the theme's id, and the class its workbench has. */
const THEMES = [
  { name: 'light', id: 'Default Light Modern', workbench: 'vs' },
  { name: 'dark', id: 'Default Dark Modern', workbench: 'vs-dark' },
  { name: 'hc-dark', id: 'Default High Contrast', workbench: 'hc-black' },
  { name: 'hc-light', id: 'Default High Contrast Light', workbench: 'hc-light' },
] as const;
type ThemeRun = (typeof THEMES)[number];
/** Where the theme pictures are written: outside images/, and ignored by git. */
const THEMES_OUT = 'out/pictures/themes';
const EXAMPLE_WARNINGS = 2;

const QUICK_SUGGESTIONS_OFF = { other: 'off', comments: 'off', strings: 'off' };

/**
 * YAML sets its own quick suggestions, which beat the general setting: switch them off for YAML.
 * Trigger characters are off too, so a completion list opens only when a script asks (Control+Space).
 */
const YAML_NO_WORD_SUGGESTIONS = {
  'editor.quickSuggestions': QUICK_SUGGESTIONS_OFF,
  'editor.suggestOnTriggerCharacters': false,
};

/** Settings that make the editor look the same on every run. */
const SETTINGS = {
  'workbench.colorTheme': 'Default Dark Modern',
  'workbench.startupEditor': 'none',
  'workbench.tips.enabled': false,
  'workbench.activityBar.location': 'hidden',
  'workbench.secondarySideBar.defaultVisibility': 'hidden',
  'workbench.welcomePage.walkthroughs.openOnInstall': false,
  'chat.disableAIFeatures': true,
  'window.commandCenter': false,
  'security.workspace.trust.enabled': false,
  // Giving a value a type of your own is experimental: the pictures that show it need it on.
  'cel-lens.experimental': ['cellens-types'],
  'telemetry.telemetryLevel': 'off',
  'update.mode': 'none',
  'extensions.autoUpdate': false,
  'editor.minimap.enabled': false,
  'editor.stickyScroll.enabled': false,
  // No word of a picture is marked because the cursor was on it a moment ago.
  'editor.occurrencesHighlight': 'off',
  'editor.selectionHighlight': false,
  'editor.fontSize': 15,
  'editor.lineHeight': 24,
  'editor.autoClosingBrackets': 'never',
  'editor.autoClosingQuotes': 'never',
  'editor.autoIndent': 'keep',
  'editor.quickSuggestions': QUICK_SUGGESTIONS_OFF,
  'editor.wordWrap': 'off',
  'editor.scrollBeyondLastLine': true,
  'editor.renderWhitespace': 'none',
  'files.autoSave': 'off',
  'breadcrumbs.enabled': false,
  '[yaml]': YAML_NO_WORD_SUGGESTIONS,
};

interface PinnedVersions {
  readonly openvscodeServer: string;
}

function log(message: string): void {
  stdout.write(`pictures: ${message}\n`);
}

function run(command: string, args: readonly string[], cwd = ROOT): void {
  execFileSync(command, args, { cwd, stdio: 'inherit' });
}

async function pinnedVersions(): Promise<PinnedVersions> {
  const manifest: unknown = JSON.parse(await readFile(join(ROOT, 'package.json'), 'utf8'));
  const pictures = (manifest as { pictures?: { openvscodeServer?: unknown } }).pictures;
  if (typeof pictures?.openvscodeServer !== 'string') {
    throw new Error('package.json needs "pictures": { "openvscodeServer": "<version>" }.');
  }
  return { openvscodeServer: pictures.openvscodeServer };
}

function serverPlatform(): string {
  const platforms: Record<string, string | undefined> = { linux: 'linux', darwin: 'darwin' };
  const architectures: Record<string, string | undefined> = { x64: 'x64', arm64: 'arm64' };
  const platform = platforms[process.platform];
  const arch = architectures[process.arch];
  if (platform === undefined || arch === undefined) {
    throw new Error(`openvscode-server has no build for ${process.platform} ${process.arch}.`);
  }
  return `${platform}-${arch}`;
}

/** Downloads the pinned openvscode-server into the cache unless it is there; gives its folder. */
async function fetchServer(version: string): Promise<string> {
  const name = `openvscode-server-v${version}-${serverPlatform()}`;
  const folder = join(CACHE, name);
  try {
    await readFile(join(folder, 'bin/openvscode-server'));
    return folder;
  } catch {
    log(`fetching openvscode-server ${version}`);
  }
  const url = `https://github.com/gitpod-io/openvscode-server/releases/download/openvscode-server-v${version}/${name}.tar.gz`;
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`Download of ${url} failed: HTTP ${String(response.status)}.`);
  }
  await mkdir(CACHE, { recursive: true });
  const archive = join(CACHE, `${name}.tar.gz`);
  await writeFile(archive, Buffer.from(await response.arrayBuffer()));
  run('tar', ['-xzf', archive, '-C', CACHE]);
  await rm(archive);
  return folder;
}

function freePort(): Promise<number> {
  return new Promise((done, fail) => {
    const probe = createServer();
    probe.once('error', fail);
    probe.listen(0, '127.0.0.1', () => {
      const address = probe.address();
      probe.close(() => {
        if (typeof address === 'object' && address !== null) {
          done(address.port);
        } else {
          fail(new Error('No free port.'));
        }
      });
    });
  });
}

/** Stops the server and the processes it started: they share its process group. */
function stopServer(server: ChildProcess | undefined): void {
  if (server?.pid !== undefined) {
    try {
      process.kill(-server.pid);
    } catch {
      // It has already stopped.
    }
  }
}

async function waitForServer(url: string, child: ChildProcess): Promise<void> {
  for (let attempt = 0; attempt < 150; attempt += 1) {
    if (child.exitCode !== null) {
      throw new Error(`openvscode-server stopped with code ${String(child.exitCode)}.`);
    }
    try {
      if ((await fetch(url)).ok) {
        return;
      }
    } catch {
      // Not listening yet.
    }
    await new Promise((done) => setTimeout(done, 200));
  }
  throw new Error(`openvscode-server did not answer at ${url}.`);
}

/** Page script (the project has no DOM types): notes whether a completion list shows from now on. */
const WATCH_FOR_LIST = `(() => {
  const seen = { suggest: false };
  const observer = new MutationObserver(() => {
    if (document.querySelector('.suggest-widget.visible') !== null) {
      seen.suggest = true;
    }
  });
  observer.observe(document.body, { subtree: true, attributes: true, childList: true });
  window.celLensWatch = { seen, observer };
})()`;

/** Page script: stops the watch and gives whether a completion list showed. */
const STOP_WATCHING_FOR_LIST = `(() => {
  window.celLensWatch.observer.disconnect();
  return window.celLensWatch.seen.suggest;
})()`;

/**
 * Watches a page as a GIF. The browser pushes a frame whenever the page repaints
 * (CDP screencast), so capturing never waits on the page while it types. The frames
 * are stamped with the virtual time they arrived at (the clock advances only while
 * `hold` runs, so the GIF plays the script's pacing whatever the machine took), held raw
 * until `stop()`, then sampled every `GIF_TICK_MS` and encoded.
 */
class Recording {
  private readonly shots: StampedFrame<Buffer>[] = [];
  private readonly clock = new VirtualClock();
  private readonly holds: number[] = [];

  private readonly session: CDPSession;
  readonly page: Page;
  private readonly name: string;

  private constructor(session: CDPSession, page: Page, name: string) {
    this.name = name;
    this.session = session;
    this.page = page;
    session.on('Page.screencastFrame', (frame) => {
      this.shots.push({
        picture: Buffer.from(frame.data, 'base64'),
        at: this.clock.stamp(Date.now()),
      });
      void session.send('Page.screencastFrameAck', { sessionId: frame.sessionId }).catch(() => {
        // The recording has stopped.
      });
    });
  }

  static async start(page: Page, name: string): Promise<Recording> {
    const session = await page.context().newCDPSession(page);
    const recording = new Recording(session, page, name);
    await page.evaluate(WATCH_FOR_LIST);
    await session.send('Page.startScreencast', {
      format: 'png',
      everyNthFrame: 1,
    });
    return recording;
  }

  /** A pacing hold of `ms` begins: the GIF's clock advances by `ms` until `endHold`. */
  beginHold(ms: number): void {
    this.holds.push(ms);
    this.clock.beginHold(ms, Date.now());
  }

  endHold(): void {
    this.clock.endHold();
  }

  /** Stops, and gives the GIF and whether a completion list was on screen meanwhile. */
  async stop(): Promise<{ gif: Uint8Array; listSeen: boolean }> {
    await this.session.send('Page.stopScreencast');
    const listSeen: unknown = await this.page.evaluate(STOP_WATCHING_FOR_LIST);
    await this.session.detach();
    return { gif: this.encode(), listSeen: listSeen === true };
  }

  /**
   * One frame per tick, all quantized with one palette. A frame writes only the pixels
   * whose palette colour changed since the one before; the rest is transparent and left in place, which keeps the file small. All frames are held in memory until encoded.
   */
  private encode(): Uint8Array {
    checkPlannedLength(this.name, this.holds);
    const raw: RawFrame[] = pickFrames(this.shots, this.holds, GIF_TICK_MS).map((shown) =>
      decodePng(this.shots[shown]?.picture ?? Buffer.alloc(0)),
    );
    if (raw.length === 0) {
      throw new Error('The recording has no frames: the page showed nothing to turn into a GIF.');
    }
    const { palette, transparentIndex, frames } = indexFrames(raw, GIF_COLORS, gifenc);
    const encoder = GIFEncoder();
    for (const [number, { index, width, height }] of frames.entries()) {
      // Only the first frame carries the palette (as the global table); later frames reuse it.
      encoder.writeFrame(
        index,
        width,
        height,
        number === 0
          ? { palette, delay: GIF_TICK_MS }
          : { delay: GIF_TICK_MS, transparent: true, dispose: 1, transparentIndex },
      );
    }
    encoder.finish();
    return encoder.bytes();
  }
}

/** Records `action` as the GIF `name`; `listAllowed` false makes a completion list on screen an error. */
async function record(
  page: Page,
  name: string,
  listAllowed: boolean,
  action: (recording: Recording) => Promise<void>,
): Promise<Uint8Array> {
  const recording = await Recording.start(page, name);
  await action(recording);
  const { gif, listSeen } = await recording.stop();
  if (listSeen && !listAllowed) {
    throw new Error('A completion list appeared while a recording that must not show one ran.');
  }
  return gif;
}

/**
 * Holds the page for `ms` so a GIF keeps a frame on screen for that long. It paces
 * what the GIF shows and is not a wait for the editor: states are waited for with `waitForState`.
 */
async function holdFrame(recording: Recording, ms: number): Promise<void> {
  recording.beginHold(ms);
  try {
    await recording.page.waitForTimeout(ms);
  } finally {
    recording.endHold();
  }
}

/** The first line of an error's message, else the thrown value as text: the reason in a give-up message. */
function firstMessageLine(error: unknown): string {
  return error instanceof Error ? (error.message.split('\n')[0] ?? error.message) : String(error);
}

/**
 * Runs `action` with a `timeout` in ms; when it fails, throws an error that names `description`,
 * the state waited for, and keeps Playwright's first-line reason; it rejects a `timeout` of 0 or
 * less before calling Playwright, which reads 0 as no timeout. Every wait and action of the
 * script goes through here.
 */
async function withTimeout<T>(
  description: string,
  timeout: number,
  action: (timeout: number) => Promise<T>,
): Promise<T> {
  // A timeout of 0 means no timeout in Playwright, so a spent deadline never reaches it.
  if (timeout <= 0) {
    throw new Error(`Gave up waiting for ${description}: no time was left (${String(timeout)} ms)`);
  }
  try {
    return await action(timeout);
  } catch (error) {
    const reason = firstMessageLine(error);
    throw new Error(`Gave up after ${String(timeout)} ms waiting for ${description}: ${reason}`, {
      cause: error,
    });
  }
}

/** Waits until `locator` is in `state`, or fails naming `description`. */
async function waitForState(
  locator: Locator,
  description: string,
  state: 'visible' | 'hidden' = 'visible',
  timeout = STATE_TIMEOUT_MS,
): Promise<void> {
  await withTimeout(description, timeout, (ms) => locator.waitFor({ state, timeout: ms }));
}

/** Clicks `locator` once it is on screen, or fails naming `description`. */
async function clickWhenReady(
  locator: Locator,
  description: string,
  timeout = STATE_TIMEOUT_MS,
): Promise<void> {
  await withTimeout(description, timeout, (ms) => locator.click({ timeout: ms }));
}

/** Waits until the page function `expression` is true, or fails naming `description`. */
async function waitForExpression(
  page: Page,
  expression: string,
  description: string,
): Promise<void> {
  await withTimeout(description, STATE_TIMEOUT_MS, async (ms) => {
    await page.waitForFunction(expression, undefined, { timeout: ms });
  });
}

/** Takes a PNG of the page, or fails naming the picture `file`, such as `problems.png`. */
async function screenshotPage(page: Page, file: string): Promise<Buffer> {
  return withTimeout(`${file} to be taken`, STATE_TIMEOUT_MS, (ms) =>
    page.screenshot({ type: 'png', timeout: ms }),
  );
}

/** Waits until a line of the active editor holds `text`. */
async function waitForEditorText(page: Page, text: string): Promise<void> {
  await waitForState(
    page.locator('.monaco-editor .view-line', { hasText: text }).first(),
    `the editor to show a line with ${text}`,
  );
}

/** Waits until the line the cursor is on holds `text`, wherever the editor is scrolled. */
async function waitForCursorLine(page: Page, text: string): Promise<void> {
  await waitForExpression(
    page,
    `(${cursorLineText.toString()})(document)?.includes(${JSON.stringify(text)}) === true`,
    `the cursor line to hold ${text}`,
  );
}

/** The line before the last of the CRD fixture: the line the cursor is on after moveToLastRule. */
async function readLastRuleLine(): Promise<string> {
  const lines = (await readFile(join(FIXTURES, CRD), 'utf8')).split('\n');
  const line = lines.at(-2);
  if (line === undefined) {
    throw new Error(`${CRD} has no line before its last.`);
  }
  return line;
}

/** Moves the cursor to the end of the last line before the final one, the spot a new rule starts from. */
async function moveToLastRule(page: Page): Promise<void> {
  await page.keyboard.press('Control+End');
  await page.keyboard.press('ArrowUp');
  await page.keyboard.press('End');
}

/** Waits until the line the cursor is on is exactly `text`, wherever the editor is scrolled. */
async function waitForCursorLineIs(page: Page, text: string): Promise<void> {
  await waitForExpression(
    page,
    `(${cursorLineText.toString()})(document) === ${JSON.stringify(text)}`,
    `the cursor line to be ${text}`,
  );
}

/** Waits until the quick-open or palette list has gone after a choice. */
async function waitForQuickInputClosed(page: Page): Promise<void> {
  await waitForState(
    page.locator('.quick-input-widget'),
    'the quick-open or palette list to close',
    'hidden',
  );
}

/** Waits until every element matching `selector` is fully shown, as `widgetShown` says. */
async function waitForWidgetShown(
  page: Page,
  selector: string,
  description: string,
): Promise<void> {
  await waitForExpression(
    page,
    `(() => { const elements = Array.from(document.querySelectorAll(${JSON.stringify(selector)})); return elements.length > 0 && elements.every((${widgetShown.toString()})); })()`,
    `${description} to be fully shown`,
  );
}

/** Waits until the light bulb is fully shown on the cursor's line, so a quick fix request finds the fix. */
async function waitForLightBulbOnCursorLine(page: Page): Promise<void> {
  await waitForExpression(
    page,
    `(() => { const shown = ${widgetShown.toString()}; const cursor = document.querySelector('.monaco-editor .cursors-layer .cursor')?.getBoundingClientRect(); return cursor !== undefined && Array.from(document.querySelectorAll('.monaco-editor .lightBulbWidget')).some((bulb) => { const box = bulb.getBoundingClientRect(); const middle = box.top + box.height / 2; return shown(bulb) && middle >= cursor.top && middle <= cursor.bottom; }); })()`,
    'the light bulb to be fully shown on the cursor line',
  );
}

/**
 * Presses Ctrl+. and waits for a fully shown quick fix row; when the editor answers "No code
 * actions available", presses Escape, waits for the message to leave the page and asks again. All asks share the step's one timeout.
 */
async function askForQuickFix(page: Page): Promise<void> {
  const answer = `(() => { if (Array.from(document.querySelectorAll('.action-widget .monaco-list-row')).length > 0) { return 'fix'; } return Array.from(document.querySelectorAll('.monaco-editor .monaco-editor-overlaymessage:not(.fadeOut)')).some((message) => message.textContent?.includes('No code actions available') === true) ? 'empty' : false; })()`;
  const gone = `Array.from(document.querySelectorAll('.monaco-editor .monaco-editor-overlaymessage')).every((message) => message.textContent?.includes('No code actions available') !== true)`;
  await withTimeout('the quick fix list to show a fix', STATE_TIMEOUT_MS, (ms) =>
    askUntilFixListed(
      {
        askForFixes: () => page.keyboard.press('Control+.'),
        waitForAnswer: async (timeout) => {
          const found = await page
            .waitForFunction(answer, undefined, { timeout })
            .then((handle) => handle.jsonValue())
            .catch((error: unknown) => {
              if (error instanceof errors.TimeoutError) {
                return 'none';
              }
              throw error;
            });
          return found === 'fix' || found === 'empty' ? found : 'none';
        },
        dismiss: () => page.keyboard.press('Escape'),
        waitForEmptyAnswerGone: (timeout) =>
          page
            .waitForFunction(gone, undefined, { timeout })
            .then(() => true)
            .catch((error: unknown) => {
              if (error instanceof errors.TimeoutError) {
                return false;
              }
              throw error;
            }),
      },
      ms,
      Date.now,
    ),
  );
}

/** Waits until the hover widget is on screen, holds some text and is fully shown. */
async function waitForHover(page: Page): Promise<void> {
  await waitForState(
    page.locator('.monaco-hover:not(.hidden)', { hasText: /\S/ }).first(),
    'the hover widget to show its text',
  );
  await waitForWidgetShown(page, '.monaco-hover:not(.hidden)', 'the hover widget');
}

/** Waits until no hover widget is on screen. */
async function waitForNoHover(page: Page): Promise<void> {
  await waitForState(
    page.locator('.monaco-hover:not(.hidden)').first(),
    'the hover widget to go away',
    'hidden',
  );
}

async function openFile(page: Page, name: string): Promise<void> {
  await page.keyboard.press('Control+p');
  await page.keyboard.type(name);
  await waitForState(
    page.locator('.quick-input-list .monaco-list-row.focused', { hasText: name }).first(),
    `the quick-open list to have ${name} highlighted`,
  );
  await page.keyboard.press('Enter');
  await waitForQuickInputClosed(page);
  await waitForState(
    page.locator('.tab.active', { hasText: name }).first(),
    `the tab of ${name} to be active`,
  );
  await waitForState(
    page.locator('.monaco-editor.focused .view-line').first(),
    `the editor of ${name} to have focus and show its content`,
  );
}

/**
 * Types text into the focused editor, `delay` ms between characters. The delay paces
 * the typing a GIF shows; it is not a wait for the editor.
 */
async function typeText(
  page: Page,
  recording: Recording,
  text: string,
  delay: number,
): Promise<void> {
  for (const character of text) {
    await page.keyboard.type(character);
    await holdFrame(recording, delay);
  }
}

/** Puts the cursor on a new line after the last rule, at the indent of the rules. */
async function startNewRule(page: Page): Promise<void> {
  await clickWhenReady(
    page.locator('.monaco-editor .view-lines').first(),
    'the editor text to be clickable',
  );
  await moveToLastRule(page);
  await page.keyboard.press('Enter');
}

/** The spot in the editor where `word` is on the line holding `line`. */
async function pointAt(page: Page, line: string, word: string): Promise<{ x: number; y: number }> {
  const row = page.locator('.monaco-editor .view-line', { hasText: line }).first();
  await waitForState(row, `the line with ${line} to be on screen`);
  const box = await row.locator('span').first().boundingBox();
  if (box === null) {
    throw new Error(`No line with ${line} is on screen.`);
  }
  const text = (await row.textContent()) ?? '';
  const column = text.replaceAll('\u00a0', ' ').indexOf(word);
  if (column < 0) {
    throw new Error(`${word} is not on the line ${line}.`);
  }
  const middle = (column + word.length / 2) / text.length;
  return { x: box.x + box.width * middle, y: box.y + box.height / 2 };
}

/**
 * Waits until the line above a rule shows `text`, such as `1 problem`. The engine's lines come
 * after the first diagnostics and push the rules below them down, so a spot measured before they
 * show is the wrong one by the time the mouse gets there.
 */
async function waitForLensLine(page: Page, text: string): Promise<void> {
  await waitForState(
    page.locator('.codelens-decoration', { hasText: text }).first(),
    `the line above a rule to show ${text}`,
    'visible',
    60_000,
  );
}

async function waitForEngine(page: Page): Promise<void> {
  await waitForState(
    page.locator('.statusbar-item', { hasText: /K8s \d/ }).first(),
    'the status bar to show the Kubernetes version of the engine',
    'visible',
    90_000,
  );
}

async function save(name: string, bytes: Uint8Array): Promise<void> {
  await writeFile(join(IMAGES, name), bytes);
  log(`wrote images/${name} (${String(bytes.length)} bytes)`);
}

/** Writes a theme picture, `<scene>-<theme>.png`, to out/pictures/themes/. */
async function saveTheme(scene: string, theme: ThemeRun, bytes: Uint8Array): Promise<void> {
  const file = `${scene}-${theme.name}.png`;
  await mkdir(join(ROOT, THEMES_OUT), { recursive: true });
  await writeFile(join(ROOT, THEMES_OUT, file), bytes);
  log(`wrote ${THEMES_OUT}/${file} (${String(bytes.length)} bytes)`);
}

/**
 * Keeps a picture of the page when the script stops, and says where. Never writes through
 * `save()` and never throws: the error that stopped the script stays the one reported.
 */
async function saveGaveUpPicture(page: Page): Promise<void> {
  try {
    const bytes = await page.screenshot({
      type: 'png',
      fullPage: true,
      timeout: GAVE_UP_TIMEOUT_MS,
    });
    const target = join(ROOT, GAVE_UP_PICTURE);
    await mkdir(dirname(target), { recursive: true });
    await writeFile(target, bytes);
    process.stderr.write(`pictures: the page when the script stopped: ${GAVE_UP_PICTURE}\n`);
  } catch (failure: unknown) {
    const reason = firstMessageLine(failure);
    process.stderr.write(
      `pictures: no picture of the page when the script stopped: the screenshot failed: ${reason}\n`,
    );
  }
}

/** Trusts the workspace, so its settings apply, and clears the screen down to the editor. */
async function prepare(page: Page): Promise<void> {
  await clickWhenReady(
    page.getByRole('button', { name: 'Yes, I trust the authors' }),
    'the trust button to be clickable',
    60_000,
  );
  await waitForState(page.locator('.monaco-dialog-box'), 'the trust dialog to close', 'hidden');
  await page.keyboard.press('Control+k');
  await page.keyboard.press('Control+w');
  await hidePrimarySideBar(page);
  await hideSideBar(
    page,
    '.part.auxiliarybar',
    'View: Hide Secondary Side Bar',
    'secondary side bar',
  );
  await expectSideBarHidden(page, '.part.sidebar', 'primary side bar');
  await expectSideBarHidden(page, '.part.auxiliarybar', 'secondary side bar');
}

/** Hides the primary side bar, as prepare() does: the theme pictures hide it again after the rule view. */
async function hidePrimarySideBar(page: Page): Promise<void> {
  await hideSideBar(page, '.part.sidebar', 'View: Close Primary Side Bar', 'primary side bar');
}

/** The page expression that is true when the side bar in `part` is off screen. */
function partHiddenExpression(part: string): string {
  return `(${partHidden.toString()})(document.querySelector(${JSON.stringify(part)}))`;
}

/**
 * Hides the side bar in `part` with the palette command `title`. Does nothing when the bar is off
 * screen. On a busy machine the bar can hide by itself (its empty view container resolves) while the
 * palette opens, and the palette then no longer lists the command; so it waits for the row or for the
 * bar to go, whichever comes first.
 */
async function hideSideBar(
  page: Page,
  part: string,
  title: string,
  barName: string,
): Promise<void> {
  const alreadyHidden: unknown = await page.evaluate(partHiddenExpression(part));
  if (alreadyHidden === true) {
    return;
  }
  const palette = page.locator('.quick-input-widget');
  const outcome = `(() => { const hidden = ${partHiddenExpression(part)}; if (hidden) { return 'hidden'; } if (Array.from(document.querySelectorAll('.quick-input-list .monaco-list-row')).some((row) => row.textContent?.includes(${JSON.stringify(title)}) === true)) { return 'listed'; } return document.querySelector('.quick-input-widget')?.checkVisibility() === true ? false : 'closed'; })()`;
  const result = await withTimeout(
    `the palette to list the command ${title} or the ${barName} to hide`,
    STATE_TIMEOUT_MS,
    (ms) =>
      hideSideBarWithRetry(
        {
          openPalette: async (timeout) => {
            await page.keyboard.press('F1');
            await waitForState(
              palette,
              'the palette to open',
              'visible',
              Math.min(PALETTE_TIMEOUT_MS, timeout),
            );
          },
          typeCommand: () => page.keyboard.type(title),
          waitForOutcome: async (timeout) => {
            const found = await page
              .waitForFunction(outcome, undefined, { timeout })
              .then((handle) => handle.jsonValue())
              .catch((error: unknown) => {
                if (error instanceof errors.TimeoutError) {
                  return 'none';
                }
                throw error;
              });
            return found === 'listed' || found === 'hidden' || found === 'closed' ? found : 'none';
          },
          runCommand: () => page.keyboard.press('Enter'),
          dismiss: () => page.keyboard.press('Escape'),
        },
        'hiding the ' + barName,
        ms,
        Date.now,
      ),
  );
  if (result === 'by itself') {
    log(`the ${barName} hid by itself before the palette listed ${title}`);
  }
  await waitForQuickInputClosed(page);
}

/** Fails when the side bar in `part` is still on screen: no picture may show it. */
async function expectSideBarHidden(page: Page, part: string, barName: string): Promise<void> {
  await waitForExpression(
    page,
    partHiddenExpression(part),
    `the ${barName} to be off screen (no picture may show it)`,
  );
}

/** Closes every editor, and discards what is unsaved in them: no picture keeps another's changes. */
async function closeAllEditors(page: Page): Promise<void> {
  const closed = `document.querySelectorAll('.tab').length === 0`;
  const asks = `document.querySelector('.monaco-dialog-box') !== null`;
  await page.keyboard.press('Control+k');
  await page.keyboard.press('Control+w');
  for (let asked = 0; asked < 3; asked += 1) {
    await waitForExpression(
      page,
      `${closed} || ${asks}`,
      'the editors to close, or to ask what to do with what is unsaved',
    );
    if ((await page.locator('.monaco-dialog-box').count()) === 0) {
      return;
    }
    await clickWhenReady(
      page.getByRole('button', { name: "Don't Save" }),
      "the Don't Save button of the dialog about unsaved changes to be clickable",
    );
    await waitForState(page.locator('.monaco-dialog-box'), 'the dialog to close', 'hidden');
  }
  await waitForExpression(page, closed, 'every editor to close');
}

/** What a picture starts from: no editor but its own file, whatever the pictures before it did. */
async function startPicture(page: Page, file: string): Promise<void> {
  await closeAllEditors(page);
  await openFile(page, file);
}

/** The page expression that is true when the status bar counts `problems` problems, errors and warnings together. */
function problemCountExpression(problems: number): string {
  return `(() => { const counts = (document.getElementById('status.problems')?.textContent ?? '').match(/\\d+/g) ?? []; return counts.reduce((sum, count) => sum + Number(count), 0) === ${String(problems)}; })()`;
}

/**
 * Fails, naming the picture, unless it starts clean: one tab and nothing unsaved in it, the
 * `problems` the picture should show in the status bar (none that another file leaves behind),
 * no notification on screen, and the line above a rule that says the engine has answered for
 * the file (`ready`, such as `compiles`).
 */
async function expectCleanStart(
  page: Page,
  picture: string,
  { problems, ready }: { problems: number; ready: string },
): Promise<void> {
  await waitForLensLine(page, ready);
  await waitForExpression(
    page,
    `document.querySelectorAll('.tab').length === 1 && document.querySelectorAll('.tab.dirty').length === 0 && document.querySelector('.notifications-toasts.visible') === null && ${problemCountExpression(problems)}`,
    `${picture} to start from a clean editor: one tab, nothing unsaved, ${String(problems)} problems in the status bar and no notification`,
  );
}

/**
 * Moves the cursor to the end of the file and waits until it is fully on screen. Done once the
 * lines above the rules show: they push the end down, so a scroll made before them leaves the
 * last lines below the window, and the line above them cut by its edge.
 */
async function scrollToEnd(page: Page, picture: string): Promise<void> {
  await page.keyboard.press('Control+End');
  await waitForExpression(
    page,
    `(() => { const cursor = document.querySelector('.monaco-editor .cursors-layer .cursor')?.getBoundingClientRect(); return cursor !== undefined && cursor.top > 0 && cursor.bottom < ${String(VIEWPORT.height - STATUS_BAR_HEIGHT)}; })()`,
    `the end of the file of ${picture} to be on screen`,
  );
  await showWholeLines(page, picture);
}

/**
 * The page function that gives how much of the editor's first and last visible lines (and of the
 * lines above the rules) is cut by its edge: the first and then the last such cut, in pixels, 0
 * when none is. A scroll lands anywhere, as the lines above the rules have a height of their own.
 */
const CUT_LINES = `(() => {
  const SLIVER_PX = ${String(SLIVER_PX)};
  const area = document.querySelector('.monaco-editor .overflow-guard')?.getBoundingClientRect();
  if (area === undefined) { return undefined; }
  let top = 0;
  let bottom = 0;
  for (const element of document.querySelectorAll('.monaco-editor .view-lines .view-line, .monaco-editor .codelens-decoration')) {
    const box = element.getBoundingClientRect();
    // A line whose last pixels or first pixels show at the edge is out of view, not cut in half.
    const shown = Math.min(box.bottom, area.bottom) - Math.max(box.top, area.top);
    if (box.height === 0 || shown < SLIVER_PX) { continue; }
    top = Math.max(top, area.top - box.top);
    bottom = Math.max(bottom, box.bottom - area.bottom);
  }
  return { top, bottom };
})()`;

/** Page function: true when no line of the editor is cut by its top or bottom edge. */
const NO_CUT_LINES = `(() => { const cut = ${CUT_LINES}; return cut !== undefined && cut.top < 1 && cut.bottom < 1; })()`;

/**
 * Waits until no line is cut by the top or the bottom edge of the editor, and fails naming the
 * picture when one is: a picture is never taken with half a line at its edge. The fixtures are
 * short enough to show whole, so nothing has to be scrolled by a few pixels.
 */
async function showWholeLines(page: Page, picture: string): Promise<void> {
  await waitForExpression(
    page,
    NO_CUT_LINES,
    `no line of ${picture} to be cut by the top or the bottom of the editor`,
  );
}

/** Moves the mouse where it covers no text and no widget shows a hover: the empty right of the editor. */
async function parkMouse(page: Page): Promise<void> {
  // Not a corner of the window: the status bar's bell shows a tooltip there.
  await page.mouse.move(VIEWPORT.width - 100, VIEWPORT.height / 2);
  await waitForNoHover(page);
}

/** inline-info.png: the line above the rules of a policy, with a `dyn` value to give a type. */
async function takeInlineInfo(page: Page): Promise<void> {
  await startPicture(page, PARAMS_POLICY);
  await waitForEditorText(page, 'container-limit');
  await expectCleanStart(page, 'inline-info.png', { problems: 0, ready: 'Give it a type' });
  await scrollToEnd(page, 'inline-info.png');
  await parkMouse(page);
  await save('inline-info.png', await screenshotPage(page, 'inline-info.png'));
}

/**
 * give-type.gif: Give it a type clicked on the line above the rule, the workspace's example
 * picked, `.cellens` opening beside the rule, and the line now showing the type with Edit.
 * Starts from the policy alone. Runs last: it writes a `.cellens` into the workspace and opens it
 * beside the policy.
 */
async function takeGiveType(page: Page): Promise<void> {
  await startPicture(page, PARAMS_POLICY);
  await expectCleanStart(page, 'give-type.gif', { problems: 0, ready: 'Give it a type' });
  await scrollToEnd(page, 'give-type.gif');
  await parkMouse(page);
  const givingType = await record(page, 'give-type.gif', false, async (recording) => {
    await holdFrame(recording, GIVE_TYPE_HOLDS_MS.start);
    await clickWhenReady(
      page.locator('.codelens-decoration a', { hasText: 'Give it a type' }).first(),
      'the line above the rule to offer Give it a type',
    );
    await waitForState(
      page.locator('.quick-input-widget .quick-input-title', { hasText: 'Give params a type' }),
      'the picker titled Give params a type',
    );
    const example = page.locator('.quick-input-list .monaco-list-row', {
      hasText: SAMPLE_IN_WORKSPACE,
    });
    await waitForState(example.first(), `the picker to list ${SAMPLE_IN_WORKSPACE} as an example`);
    await holdFrame(recording, GIVE_TYPE_HOLDS_MS.pickerShown);
    // The filter leaves the example alone in the list, highlighted, before it is clicked.
    await typeText(page, recording, GIVE_TYPE_FILTER, GIF_TYPING_MS);
    await waitForState(
      page.locator('.quick-input-list .monaco-list-row.focused', {
        hasText: SAMPLE_IN_WORKSPACE,
      }),
      `the example ${SAMPLE_IN_WORKSPACE} to be highlighted in the picker`,
    );
    await holdFrame(recording, GIVE_TYPE_HOLDS_MS.exampleHighlighted);
    await clickWhenReady(example.first(), `the example ${SAMPLE_IN_WORKSPACE} to be clickable`);
    await waitForQuickInputClosed(page);
    await waitForState(
      page.locator('.tab.active', { hasText: '.cellens' }).first(),
      'the tab of .cellens to open beside the rule',
    );
    await waitForState(
      page.locator('.codelens-decoration a', { hasText: 'Edit' }).first(),
      'the line above the rule to offer Edit once the value has a type',
    );
    await holdFrame(recording, GIVE_TYPE_HOLDS_MS.afterWrite);
  });
  await save('give-type.gif', givingType);
}

/**
 * cellens-types.gif: the whole loop of giving a value a type in `.cellens`, with the experimental
 * feature on: `params: dyn · Give it a type` above a rule, **Write a schema in .cellens…** picked,
 * `.cellens` opening beside the rule, the schema typed with completion, and the line above the
 * rule following to `params: {maxReplicas} · Edit`. It starts from a workspace with no `.cellens`
 * and leaves none.
 */
async function takeCellensTypes(page: Page, workspace: string): Promise<void> {
  await rm(join(workspace, '.cellens'), { force: true });
  await startPicture(page, REPLICA_POLICY);
  await expectCleanStart(page, 'cellens-types.gif', { problems: 0, ready: 'Give it a type' });
  await scrollToEnd(page, 'cellens-types.gif');
  await parkMouse(page);
  const writing = await record(page, 'cellens-types.gif', true, async (recording) => {
    await holdFrame(recording, CELLENS_TYPES_HOLDS_MS.start);
    await clickWhenReady(
      page.locator('.codelens-decoration a', { hasText: 'Give it a type' }).first(),
      'the line above the rule to offer Give it a type',
    );
    await waitForState(
      page.locator('.quick-input-widget .quick-input-title', { hasText: 'Give params a type' }),
      'the picker titled Give params a type',
    );
    const schema = page.locator('.quick-input-list .monaco-list-row', {
      hasText: 'Write a schema in .cellens…',
    });
    await waitForState(schema.first(), 'the picker to list Write a schema in .cellens…');
    await holdFrame(recording, CELLENS_TYPES_HOLDS_MS.pickerShown);
    await clickWhenReady(schema.first(), 'Write a schema in .cellens… to be clickable');
    await waitForQuickInputClosed(page);
    await waitForState(
      page.locator('.tab.active', { hasText: '.cellens' }).first(),
      'the tab of .cellens to open beside the rule',
    );
    await waitForState(
      page.locator('.monaco-editor .view-line', { hasText: 'properties:' }).first(),
      'the new entry to show its properties',
    );
    await holdFrame(recording, CELLENS_TYPES_HOLDS_MS.cellensOpen);
    await typeText(page, recording, CELLENS_TYPES_TYPED_BEFORE, CELLENS_TYPES_TYPING_MS);
    await page.keyboard.press('Control+Space');
    await waitForState(
      page.locator('.suggest-widget.visible .monaco-list-row').first(),
      'the completion list to show the types of the schema',
    );
    await waitForWidgetShown(page, '.suggest-widget.visible', 'the completion list');
    await holdFrame(recording, CELLENS_TYPES_HOLDS_MS.listShown);
    // The list narrows to the type as it is typed, and Tab takes it.
    await typeText(page, recording, CELLENS_TYPES_FILTER, CELLENS_TYPES_TYPING_MS);
    await waitForState(
      page.locator('.suggest-widget.visible .monaco-list-row.focused', { hasText: 'integer' }),
      'the completion list to highlight integer',
    );
    await page.keyboard.press('Tab');
    await waitForState(
      page.locator('.suggest-widget').first(),
      'the completion list to close after the pick',
      'hidden',
    );
    await holdFrame(recording, CELLENS_TYPES_HOLDS_MS.typePicked);
    await typeText(page, recording, CELLENS_TYPES_TYPED_AFTER, CELLENS_TYPES_TYPING_MS);
    await waitForState(
      page.locator('.codelens-decoration a', { hasText: 'Edit' }).first(),
      'the line above the rule to offer Edit once the value has a type',
    );
    await waitForState(
      page.locator('.codelens-decoration a', { hasText: 'params: {maxReplicas}' }).first(),
      'the line above the rule to show params: {maxReplicas}',
    );
    await holdFrame(recording, CELLENS_TYPES_HOLDS_MS.afterWrite);
  });
  await save('cellens-types.gif', writing);
}

/**
 * rule-view.png: the cel-lens view open beside a CRD, with the badge on its icon in the activity
 * bar: the documents of the workspace with their rules, one rule that does not compile, one whose
 * example is wrong, rules that pass, and the rule the cursor is in selected by following the
 * editor. The activity bar is the one picture that shows it; the examples come and go with it, as
 * with the example results pictures. With a theme, for the theme pictures, a Pod the policy allows
 * comes too, so a rule shows each of the four states: error, warning, pass and check.
 */
async function takeRuleView(page: Page, workspace: string, theme?: ThemeRun): Promise<void> {
  await mkdir(join(workspace, EXAMPLES_FOLDER), { recursive: true });
  for (const [fixture, file] of LIMIT_EXAMPLES) {
    await mkdir(dirname(join(workspace, EXAMPLES_FOLDER, file)), { recursive: true });
    await cp(join(FIXTURES, `limit_${fixture}.yaml`), join(workspace, EXAMPLES_FOLDER, file));
  }
  if (theme !== undefined) {
    await cp(join(FIXTURES, POD_SAMPLE[0]), join(workspace, EXAMPLES_FOLDER, POD_SAMPLE[1]));
  }
  const settings = join(workspace, '.vscode/settings.json');
  const themed = theme === undefined ? {} : { 'workbench.colorTheme': theme.id };
  await writeFile(
    settings,
    JSON.stringify(
      { ...SETTINGS, ...themed, 'workbench.activityBar.location': 'default' },
      null,
      2,
    ),
  );
  await startPicture(page, CRD);
  await page.keyboard.press('Control+g');
  await page.keyboard.type(String(RULE_VIEW_LINE));
  await page.keyboard.press('Enter');
  await waitForQuickInputClosed(page);
  await waitForCursorLine(page, '- rule: self.replica');
  await waitForLensLine(page, '1 problem');
  await clickWhenReady(
    // The item, not its link: the badge on the icon sits over the link and takes the click, which reaches the item.
    page.locator('.activitybar li.action-item[aria-label^="cel-lens"]').first(),
    'the cel-lens icon of the activity bar to be clickable',
  );
  // The side bar is widened by its edge, so the words of each row show whole.
  await page.mouse.move(RULE_VIEW_SIDE_BAR_EDGE_X, VIEWPORT.height / 2);
  await page.mouse.down();
  await page.mouse.move(RULE_VIEW_SIDE_BAR_EDGE_X + RULE_VIEW_WIDER_PX, VIEWPORT.height / 2, {
    steps: 8,
  });
  await page.mouse.up();
  const rows = page.locator('.pane-body .monaco-list-row');
  await waitForState(
    rows.filter({ hasText: 'widgets.example.com' }).first(),
    'the view to list the CRD of the editor',
  );
  await waitForState(
    page.locator('.activitybar .badge-content:visible').first(),
    'the badge on the icon of the cel-lens view',
  );
  // The documents that show the other states open; the rows between are left closed.
  // A row the view kept open from an earlier picture (the theme pictures take the view four times)
  // stays open: its twistie would close it.
  for (const name of RULE_VIEW_OPEN) {
    const row = rows.filter({ hasText: name }).first();
    await waitForState(row, `the view to list ${name}`);
    const closed = row.locator(':scope[aria-expanded="false"]');
    if ((await closed.count()) > 0) {
      await clickWhenReady(closed.locator('.monaco-tl-twistie'), `the row of ${name} to open`);
    }
  }
  await waitForState(
    rows.filter({ hasText: 'refuses 1 example it should allow' }).first(),
    'the view to list a rule with a wrong example',
  );
  if (theme !== undefined) {
    await waitForState(
      rows.filter({ hasText: /\d+ examples? pass/ }).first(),
      'the view to list a rule whose examples pass',
    );
  }
  // The cursor moves into the rule that does not compile, and the view follows it.
  const wrong = await pointAt(page, 'self.replica > 0', 'replica ');
  await page.mouse.click(wrong.x, wrong.y);
  await waitForState(
    rows.filter({ hasText: RULE_VIEW_SELECTED }).locator(':scope.selected, :scope.focused').first(),
    `the view to select ${RULE_VIEW_SELECTED} by following the editor`,
  );
  await parkMouse(page);
  const picture = await screenshotPage(page, 'rule-view.png');
  if (theme === undefined) {
    await save('rule-view.png', picture);
  } else {
    await saveTheme('rule-view', theme, picture);
  }
  await writeFile(settings, JSON.stringify({ ...SETTINGS, ...themed }, null, 2));
  for (const [, file] of LIMIT_EXAMPLES) {
    await rm(join(workspace, EXAMPLES_FOLDER, file));
  }
  await rm(join(workspace, EXAMPLES_FOLDER, POD_SAMPLE[1]), { force: true });
}

/**
 * example-results-line.png and example-results-picker.png: a CRD whose rule refuses one of the
 * three examples of the workspace, which expects to be allowed. The script puts the examples in
 * `config/samples/` for these two pictures and takes them out again. The first picture shows the
 * line above the rule with its part and the warning on the rule, with no hover (a hover with three
 * examples and the warning is taller than VS Code lets one be, and cuts its last row); the second,
 * the picker the part opens, the example that is not as expected first.
 */
async function takeExampleResults(page: Page, workspace: string): Promise<void> {
  // The examples come and go with these pictures: their wrong verdict is a warning the status bar
  // counts, and no other picture shows it.
  await mkdir(join(workspace, EXAMPLES_FOLDER), { recursive: true });
  for (const [fixture, file] of LIMIT_EXAMPLES) {
    await mkdir(dirname(join(workspace, EXAMPLES_FOLDER, file)), { recursive: true });
    await cp(join(FIXTURES, `limit_${fixture}.yaml`), join(workspace, EXAMPLES_FOLDER, file));
  }
  await startPicture(page, LIMIT_CRD);
  await page.keyboard.press('Control+g');
  await page.keyboard.type(String(EXAMPLE_RULE_LINE));
  await page.keyboard.press('Enter');
  await waitForQuickInputClosed(page);
  await waitForCursorLine(page, '- rule: self.replicas');
  await expectCleanStart(page, 'example-results-line.png', {
    problems: EXAMPLE_WARNINGS,
    ready: 'refuses 1 example it should allow',
  });
  await showWholeLines(page, 'example-results-line.png');

  // example-results-line.png: the part of the line above the rule, and the warning on the rule.
  await parkMouse(page);
  await save('example-results-line.png', await screenshotPage(page, 'example-results-line.png'));

  // example-results-picker.png: the picker the part opens, the wrong example first.
  await clickWhenReady(
    page
      .locator('.codelens-decoration a', { hasText: 'refuses 1 example it should allow' })
      .first(),
    'the line above the rule to offer its example results',
  );
  await waitForState(
    page.locator('.quick-input-widget .quick-input-title', {
      hasText: 'Example results of the rule',
    }),
    'the picker titled Example results of the rule',
  );
  await waitForState(
    page.locator('.quick-input-list .monaco-list-row', { hasText: 'big.yaml' }).first(),
    'the picker to list big.yaml as not as expected',
  );
  await waitForState(
    page.locator('.quick-input-list .monaco-list-row', { hasText: 'small.yaml' }).first(),
    'the picker to list small.yaml as as expected',
  );
  const picker = await screenshotPage(page, 'example-results-picker.png');
  await save('example-results-picker.png', picker);
  await page.keyboard.press('Escape');
  await waitForQuickInputClosed(page);
  for (const [, file] of LIMIT_EXAMPLES) {
    await rm(join(workspace, EXAMPLES_FOLDER, file));
  }
}

/**
 * example-results-policy.png: the picker opened from the part above a ValidatingAdmissionPolicy's
 * validation, which two bindings give different verdicts for the Deployment `big.yaml`: refused
 * under one, warned under the other. It shows what the CRD pictures do not: the binding and the
 * param each line ran under, the API server's answer, and the verdicts of a policy. The bindings,
 * the param and the Deployments come and go with this picture, as the examples of the CRD do.
 */
async function takePolicyResults(page: Page, workspace: string): Promise<void> {
  for (const [fixture, file] of DEPLOY_FILES) {
    await mkdir(dirname(join(workspace, file)), { recursive: true });
    await cp(join(FIXTURES, fixture), join(workspace, file));
  }
  await startPicture(page, DEPLOY_POLICY);
  await expectCleanStart(page, 'example-results-policy.png', {
    problems: DEPLOY_WARNINGS,
    ready: '1 wrong',
  });
  await showWholeLines(page, 'example-results-policy.png');
  await parkMouse(page);
  await clickWhenReady(
    page.locator('.codelens-decoration a', { hasText: '1 wrong' }).first(),
    'the line above the validation to offer its example results',
  );
  await waitForState(
    page.locator('.quick-input-widget .quick-input-title', {
      hasText: 'Example results of the rule',
    }),
    'the picker titled Example results of the rule',
  );
  await waitForState(
    page.locator('.quick-input-list .monaco-list-row', { hasText: 'deny-limits' }).first(),
    'the picker to list the example under the binding deny-limits',
  );
  await waitForState(
    page.locator('.quick-input-list .monaco-list-row', { hasText: 'warn-limits' }).first(),
    'the picker to list the example under the binding warn-limits',
  );
  const picker = await screenshotPage(page, 'example-results-policy.png');
  await save('example-results-policy.png', picker);
  await page.keyboard.press('Escape');
  await waitForQuickInputClosed(page);
  for (const [, file] of DEPLOY_FILES) {
    await rm(join(workspace, file));
  }
}

/** Waits until the workbench has the theme's class: the theme setting was applied. */
async function waitForTheme(page: Page, theme: ThemeRun): Promise<void> {
  await waitForExpression(
    page,
    `document.querySelector('.monaco-workbench')?.classList.contains(${JSON.stringify(theme.workbench)}) === true`,
    `the workbench to switch to ${theme.id}`,
  );
}

/** Runs a command from the palette by its title, such as `cel-lens: Get Started`. */
async function runFromPalette(page: Page, title: string): Promise<void> {
  await page.keyboard.press('F1');
  await page.keyboard.type(title);
  await waitForState(
    page.locator('.quick-input-list .monaco-list-row.focused', { hasText: title }).first(),
    `the palette to list ${title}`,
  );
  await page.keyboard.press('Enter');
  await waitForQuickInputClosed(page);
}

/** The line of the CRD that the theme pictures show at the top of the editor. */
const THEME_CRD_TOP_LINE = 17;

/**
 * Opens the CRD with its colors and the line above each rule, the cursor in the rule that has a
 * problem, as `picture` shows it. Going to a line centers it, and where the top line then falls depends on whether the lines above
 * the rules were drawn before the scroll or after: the theme after the first finds them drawn, and
 * its top line cut. A line the cursor moves up to from below the top of the editor is put whole
 * at its top instead; so the cursor goes to the last rule (which shows the lines above the rules),
 * up to THEME_CRD_TOP_LINE line by line, and down to the rule with the problem, and the same lines
 * show in every theme.
 */
async function showCrdRules(page: Page, picture: string): Promise<void> {
  const lines = (await readFile(join(FIXTURES, CRD), 'utf8')).split('\n');
  await startPicture(page, CRD);
  await page.keyboard.press('Control+g');
  await page.keyboard.type(String(lines.length - 1));
  await page.keyboard.press('Enter');
  await waitForQuickInputClosed(page);
  await waitForCursorLineIs(page, await readLastRuleLine());
  await waitForEngine(page);
  await waitForLensLine(page, '1 problem');
  for (let line = lines.length - 1; line > THEME_CRD_TOP_LINE; line -= 1) {
    await page.keyboard.press('ArrowUp');
  }
  await waitForCursorLineIs(page, lines[THEME_CRD_TOP_LINE - 1] ?? '');
  // Down, not with go-to-line, which centers the line even when it shows.
  for (let line = THEME_CRD_TOP_LINE; line < RULE_VIEW_LINE; line += 1) {
    await page.keyboard.press('ArrowDown');
  }
  await waitForCursorLine(page, '- rule: self.replica');
  await showWholeLines(page, picture);
  await parkMouse(page);
}

/** The CRD with its colors and the line above each rule, in a theme. */
async function takeThemeCrd(page: Page, theme: ThemeRun): Promise<void> {
  await showCrdRules(page, `crd-${theme.name}.png`);
  await saveTheme('crd', theme, await screenshotPage(page, `crd-${theme.name}.png`));
}

/** The reason of the wrong field, shown in its hover, with the squiggle under the field. */
async function takeThemeProblem(page: Page, theme: ThemeRun): Promise<void> {
  await waitForState(
    page.locator('.monaco-editor .squiggly-error').first(),
    'the editor to underline the wrong field',
    'visible',
    60_000,
  );
  const wrong = await pointAt(page, 'self.replica > 0', 'replica ');
  await page.mouse.move(wrong.x - 20, wrong.y - 60);
  await page.mouse.move(wrong.x, wrong.y, { steps: 6 });
  await waitForHover(page);
  await saveTheme(
    'problem-hover',
    theme,
    await screenshotPage(page, `problem-hover-${theme.name}.png`),
  );
  await parkMouse(page);
}

/** The status bar menu, opened by a click on the status bar item; gives the picture, the menu closed again. */
async function takeStatusMenu(page: Page, picture: string): Promise<Uint8Array> {
  await clickWhenReady(
    page.locator('.statusbar-item', { hasText: /K8s \d/ }).first(),
    'the status bar item with the Kubernetes version to be clickable',
  );
  await waitForState(
    page.locator('.quick-input-list .monaco-list-row', { hasText: 'Kubernetes version' }).first(),
    'the status bar menu to list Kubernetes version',
  );
  await waitForWidgetShown(page, '.quick-input-widget', 'the status bar menu');
  const bytes = await screenshotPage(page, picture);
  await page.keyboard.press('Escape');
  await waitForQuickInputClosed(page);
  return bytes;
}

/**
 * The editor's cel-lens submenu, opened by a right click on a rule. The engine's list of the file
 * sets the keys the submenu's entries wait for, so a menu that opens before it has them is closed
 * and opened again, a few times. Gives the picture, the menus closed again.
 */
async function takeEditorMenu(page: Page, picture: string): Promise<Uint8Array> {
  const rule = await pointAt(page, 'self.replicas >= self.minReplicas', 'replicas ');
  await page.mouse.click(rule.x, rule.y);
  for (let attempt = 0; ; attempt += 1) {
    await page.mouse.click(rule.x, rule.y, { button: 'right' });
    const entry = page.locator('.monaco-menu .action-label', { hasText: /^cel-lens$/ }).first();
    await waitForState(entry, 'the editor menu to have the cel-lens submenu');
    await clickWhenReady(entry, 'the cel-lens entry of the editor menu to open its submenu');
    const shown = page
      .locator('.monaco-submenu .action-label', { hasText: 'Show Problems' })
      .first();
    try {
      await waitForState(shown, 'the cel-lens submenu to list Show Problems', 'visible', 5_000);
      break;
    } catch (error: unknown) {
      if (attempt >= 5) {
        throw error;
      }
      await page.keyboard.press('Escape');
      await page.keyboard.press('Escape');
    }
  }
  const bytes = await screenshotPage(page, picture);
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  await waitForState(page.locator('.monaco-menu').first(), 'the menus to close', 'hidden');
  return bytes;
}

/**
 * actions-menu.png and editor-menu.png, the README's pictures of where to find everything: the
 * status bar menu open over the CRD, and the editor's cel-lens submenu open at a rule.
 */
async function takeMenus(page: Page): Promise<void> {
  await showCrdRules(page, 'actions-menu.png');
  await expectCleanStart(page, 'actions-menu.png', { problems: 1, ready: '1 problem' });
  await save('actions-menu.png', await takeStatusMenu(page, 'actions-menu.png'));
  await save('editor-menu.png', await takeEditorMenu(page, 'editor-menu.png'));
}

/** The first step of the walkthrough, opened by Get Started in the palette. */
async function takeThemeWalkthrough(page: Page, theme: ThemeRun): Promise<void> {
  await closeAllEditors(page);
  await runFromPalette(page, 'cel-lens: Get Started');
  await waitForState(
    page.locator('.gettingStartedContainer', { hasText: 'Open a sample CRD' }).first(),
    'the walkthrough to show its first step',
    'visible',
    60_000,
  );
  // The step's picture is an SVG drawn in a webview, which loads after the step shows.
  await waitForState(
    page
      .frameLocator('.getting-started-media iframe.webview')
      .frameLocator('#active-frame')
      .locator('svg')
      .first(),
    'the walkthrough to show the picture of its first step',
    'visible',
    60_000,
  );
  await parkMouse(page);
  await saveTheme(
    'walkthrough',
    theme,
    await screenshotPage(page, `walkthrough-${theme.name}.png`),
  );
  await closeAllEditors(page);
}

/**
 * `npm run pictures -- themes`: six scenes in each of the four default themes, written to
 * out/pictures/themes/<scene>-<theme>.png: a CRD with its colors and lines, a problem with its
 * hover, the rule view with a rule in each state, the status bar menu, the editor's submenu and the
 * walkthrough's first step. The theme is changed through the workspace's settings, as a person
 * changes it. Nothing goes to images/.
 */
async function takeThemes(page: Page, workspace: string): Promise<void> {
  const settings = join(workspace, '.vscode/settings.json');
  for (const theme of THEMES) {
    await writeFile(
      settings,
      JSON.stringify({ ...SETTINGS, 'workbench.colorTheme': theme.id }, null, 2),
    );
    await waitForTheme(page, theme);
    await takeThemeCrd(page, theme);
    await takeThemeProblem(page, theme);
    await saveTheme(
      'status-menu',
      theme,
      await takeStatusMenu(page, `status-menu-${theme.name}.png`),
    );
    await saveTheme('submenu', theme, await takeEditorMenu(page, `submenu-${theme.name}.png`));
    await takeRuleView(page, workspace, theme);
    await hidePrimarySideBar(page);
    await takeThemeWalkthrough(page, theme);
  }
  await writeFile(settings, JSON.stringify(SETTINGS, null, 2));
}

async function takePictures(page: Page, workspace: string): Promise<void> {
  await prepare(page);
  if (ONLY.length > 0) {
    // `npm run pictures -- cellens-types`: the pictures named, and no other, so the rest stay as they are.
    if (ONLY.includes('cellens-types')) {
      await takeCellensTypes(page, workspace);
      await rm(join(workspace, '.cellens'), { force: true });
    }
    if (ONLY.includes('rule-view')) {
      await takeRuleView(page, workspace);
    }
    if (ONLY.includes('menus')) {
      await takeMenus(page);
    }
    if (ONLY.includes('themes')) {
      await takeThemes(page, workspace);
    }
    return;
  }
  await takeInlineInfo(page);

  await startPicture(page, CRD);
  await page.keyboard.press('Control+g');
  await page.keyboard.type('28');
  await page.keyboard.press('Enter');
  await waitForQuickInputClosed(page);
  await waitForEngine(page);
  await waitForState(
    page.locator('.monaco-editor .squiggly-error').first(),
    'the editor to underline the wrong field',
    'visible',
    60_000,
  );
  await expectCleanStart(page, 'problems.png', { problems: 1, ready: '1 problem' });
  await showWholeLines(page, 'problems.png');

  // problems.png: the reason of the wrong field, on the wrong field.
  const wrong = await pointAt(page, 'self.replica > 0', 'replica ');
  await page.mouse.move(wrong.x - 20, wrong.y - 60);
  await page.mouse.move(wrong.x, wrong.y, { steps: 6 });
  await waitForHover(page);
  await save('problems.png', await screenshotPage(page, 'problems.png'));

  // quick-fix.png: the fix offered on that problem.
  await page.mouse.move(wrong.x, wrong.y + 120);
  await waitForNoHover(page);
  await page.mouse.click(wrong.x, wrong.y);
  await waitForLightBulbOnCursorLine(page);
  await askForQuickFix(page);
  await waitForWidgetShown(page, '.action-widget', 'the quick fix list');
  await save('quick-fix.png', await screenshotPage(page, 'quick-fix.png'));

  // The fix is taken and saved: the pictures after this one show the rules as they should be.
  await page.keyboard.press('Enter');
  await waitForState(
    page.locator('.monaco-editor .squiggly-error').first(),
    'the underline to go once the quick fix is applied',
    'hidden',
  );
  await page.keyboard.press('Control+s');
  await waitForExpression(
    page,
    `document.querySelectorAll('.tab.dirty').length === 0`,
    'the CRD with the fix applied to be saved',
  );
  await page.keyboard.press('Control+End');

  // hover.png: the type and description of a field.
  const field = await pointAt(page, 'self.replicas >= self.minReplicas', 'minReplicas');
  await page.mouse.move(field.x - 20, field.y + 80);
  await page.mouse.move(field.x, field.y, { steps: 6 });
  await waitForHover(page);
  await save('hover.png', await screenshotPage(page, 'hover.png'));
  await page.mouse.move(field.x, field.y + 200);

  // completion.png and completion.gif: the list after self., one item picked.
  const lastRuleBefore = await readLastRuleLine();
  await moveToLastRule(page);
  await waitForCursorLineIs(page, lastRuleBefore);
  await expectCleanStart(page, 'completion.gif', { problems: 0, ready: 'compiles' });
  await showWholeLines(page, 'completion.gif');
  await startNewRule(page);
  await page.keyboard.type('- rule: ');
  await waitForCursorLine(page, '- rule: ');
  let completionPicture: Uint8Array | undefined;
  const completion = await record(page, 'completion.gif', true, async (recording) => {
    await holdFrame(recording, COMPLETION_HOLDS_MS.start);
    await page.keyboard.type('self');
    await holdFrame(recording, COMPLETION_HOLDS_MS.afterSelf);
    await page.keyboard.type('.');
    await page.keyboard.press('Control+Space');
    await waitForState(
      page.locator('.suggest-widget.visible .monaco-list-row').first(),
      'the completion list to show an item after self.',
    );
    await waitForWidgetShown(page, '.suggest-widget.visible', 'the completion list');
    await holdFrame(recording, COMPLETION_HOLDS_MS.listShown);
    await page.keyboard.press('ArrowDown');
    await holdFrame(recording, COMPLETION_HOLDS_MS.firstPick);
    completionPicture = await screenshotPage(page, 'completion.png');
    await page.keyboard.press('ArrowDown');
    await holdFrame(recording, COMPLETION_HOLDS_MS.secondPick);
    await page.keyboard.press('Tab');
    await waitForState(
      page.locator('.suggest-widget').first(),
      'the completion list to close after the pick',
      'hidden',
    );
    await holdFrame(recording, COMPLETION_HOLDS_MS.afterTab);
  });
  await save('completion.gif', completion);
  if (completionPicture === undefined) {
    throw new Error('The completion list was never on screen.');
  }
  await save('completion.png', completionPicture);

  // Back to the file as it was saved: what the completion typed is discarded, not undone.
  await startPicture(page, CRD);
  await moveToLastRule(page);
  await waitForCursorLineIs(page, lastRuleBefore);
  await expectCleanStart(page, 'colors.gif', { problems: 0, ready: 'compiles' });
  await showWholeLines(page, 'colors.gif');

  // colors.gif: a rule colored as it is typed, with no completion list in the way.
  await startNewRule(page);
  await page.keyboard.type('- rule: ');
  await waitForCursorLine(page, '- rule: ');
  const colors = await record(page, 'colors.gif', false, async (recording) => {
    await holdFrame(recording, COLORS_HOLD_BEFORE_MS);
    await typeText(page, recording, COLORS_TYPED_TEXT, GIF_TYPING_MS);
    await holdFrame(recording, COLORS_HOLD_AFTER_MS);
  });
  await save('colors.gif', colors);

  // colors.png: a whole policy in color.
  await startPicture(page, POLICY);
  await waitForEditorText(page, 'no-latest-tag');
  await expectCleanStart(page, 'colors.png', { problems: 0, ready: 'compiles' });
  await scrollToEnd(page, 'colors.png');
  await parkMouse(page);
  await save('colors.png', await screenshotPage(page, 'colors.png'));

  await takeExampleResults(page, workspace);
  await takePolicyResults(page, workspace);
  await takeCellensTypes(page, workspace);
  // The engine's edit created an empty .cellens on disk, which the next picture must not find.
  await rm(join(workspace, '.cellens'), { force: true });
  await takeGiveType(page);
  await takeMenus(page);
  await takeRuleView(page, workspace);
}

async function main(): Promise<void> {
  const unknown = ONLY.filter((name) => !TAKEN_ALONE.includes(name));
  if (unknown.length > 0) {
    throw new Error(
      `Unknown picture ${unknown.join(', ')}: only ${TAKEN_ALONE.join(', ')} can be taken alone.`,
    );
  }
  const { openvscodeServer } = await pinnedVersions();
  log('building the extension');
  run('npm', ['run', 'build', '--silent']);
  run('npm', ['run', 'engine', '--silent']);

  const work = await mkdtemp(join(tmpdir(), 'cel-lens-pictures-'));
  let server: ChildProcess | undefined;
  try {
    const vsix = join(work, 'cel-lens.vsix');
    run(join(ROOT, 'node_modules/.bin/vsce'), [
      'package',
      '--no-dependencies',
      '--skip-license',
      '--allow-missing-repository',
      '--out',
      vsix,
    ]);

    const home = await fetchServer(openvscodeServer);
    const binary = join(home, 'bin/openvscode-server');
    const dirs = ['--user-data-dir', join(work, 'user'), '--extensions-dir', join(work, 'ext')];
    const serverData = ['--server-data-dir', join(work, 'server')];
    run(binary, [...dirs, ...serverData, '--install-extension', vsix]);

    const workspace = join(work, 'workspace');
    await mkdir(join(workspace, '.vscode'), { recursive: true });
    for (const name of [CRD, POLICY, PARAMS_POLICY, REPLICA_POLICY, LIMIT_CRD, DEPLOY_POLICY]) {
      await cp(join(FIXTURES, name), join(workspace, name));
    }
    await mkdir(join(workspace, dirname(SAMPLE_IN_WORKSPACE)), { recursive: true });
    await cp(join(FIXTURES, SAMPLE), join(workspace, SAMPLE_IN_WORKSPACE));
    await writeFile(join(workspace, '.vscode/settings.json'), JSON.stringify(SETTINGS, null, 2));
    await mkdir(join(work, 'server/data/Machine'), { recursive: true });
    await writeFile(
      join(work, 'server/data/Machine/settings.json'),
      JSON.stringify({ 'security.workspace.trust.enabled': false }),
    );

    const port = await freePort();
    server = spawn(
      binary,
      [
        ...dirs,
        ...serverData,
        '--host',
        '127.0.0.1',
        '--port',
        String(port),
        '--without-connection-token',
        '--disable-telemetry',
      ],
      { stdio: 'ignore', detached: true },
    );
    const url = `http://127.0.0.1:${String(port)}/`;
    await waitForServer(url, server);

    await mkdir(IMAGES, { recursive: true });
    const chrome = process.env['CEL_LENS_CHROME'];
    const browser = await chromium.launch({
      headless: true,
      ...(chrome !== undefined && chrome !== '' && { executablePath: chrome }),
    });
    try {
      const page = await browser.newPage({ viewport: VIEWPORT, deviceScaleFactor: 1 });
      await withTimeout('the workbench page to load', WORKBENCH_TIMEOUT_MS, (ms) =>
        page.goto(`${url}?folder=${encodeURIComponent(workspace)}`, { timeout: ms }),
      );
      try {
        await waitForState(
          page.locator('.monaco-workbench'),
          'the workbench to load',
          'visible',
          WORKBENCH_TIMEOUT_MS,
        );
        await takePictures(page, workspace);
      } catch (error: unknown) {
        await saveGaveUpPicture(page);
        throw error;
      }
    } finally {
      await browser.close();
    }
  } finally {
    stopServer(server);
    await rm(work, { recursive: true, force: true });
  }
}

main().catch((error: unknown) => {
  process.stderr.write(`pictures: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
});
