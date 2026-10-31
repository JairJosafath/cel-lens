// Takes the pictures of the README: `npm run pictures`.
//
// Builds the extension, installs it in openvscode-server (a VS Code served to a
// browser), opens the two fixtures of this folder in headless Chromium driven by
// Playwright, and writes five PNGs and two GIFs into images/. Not part of
// `make check`: it fetches the server and Chromium, so it needs a network, and it
// is run on purpose. The engine is the pinned one: `npm run engine` puts it in bin/.
import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { stdout } from 'node:process';

import gifenc from 'gifenc';
import { chromium, type CDPSession, type Locator, type Page } from 'playwright';
import jpeg from 'jpeg-js';

import { cursorLineText } from '../../src/cursor_line.ts';
import { widgetShown } from '../../src/widget_shown.ts';
import { indexFrames, type RawFrame } from '../../src/gif_frames.ts';
import { pickFrames, VirtualClock, type StampedFrame } from '../../src/gif_timeline.ts';

const { GIFEncoder } = gifenc;

const ROOT = resolve(import.meta.dirname, '../..');
const FIXTURES = import.meta.dirname;
const IMAGES = join(ROOT, 'images');
const CACHE = join(ROOT, 'node_modules/.cache/cel-lens-pictures');

const VIEWPORT = { width: 1100, height: 640 };
const GIF_COLORS = 48;
const SCREENCAST_QUALITY = 90;
const GIF_TICK_MS = 100;
const GIF_TYPING_MS = 80;
/** A GIF whose planned length (the sum of its holds) reaches this is an error: the README GIFs stay short. */
const GIF_MAX_PLANNED_MS = 8000;
/** How long a state the script waits for may take, on a slow machine. */
const STATE_TIMEOUT_MS = 30_000;

const CRD = 'widget_crd.yaml';
const POLICY = 'pod_policy.yaml';

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
  'telemetry.telemetryLevel': 'off',
  'update.mode': 'none',
  'extensions.autoUpdate': false,
  'editor.minimap.enabled': false,
  'editor.fontSize': 15,
  'editor.lineHeight': 24,
  'editor.autoClosingBrackets': 'never',
  'editor.autoClosingQuotes': 'never',
  'editor.autoIndent': 'keep',
  'editor.quickSuggestions': QUICK_SUGGESTIONS_OFF,
  'editor.wordWrap': 'off',
  'editor.scrollBeyondLastLine': false,
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

/** The pixels of a JPEG as RGBA bytes. */
function decode(picture: Buffer): { width: number; height: number; data: Uint8Array } {
  const image = jpeg.decode(picture, { useTArray: true });
  return { width: image.width, height: image.height, data: image.data };
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
      format: 'jpeg',
      quality: SCREENCAST_QUALITY,
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
    const planned = this.holds.reduce((sum, ms) => sum + ms, 0);
    if (planned >= GIF_MAX_PLANNED_MS) {
      throw new Error(
        `${this.name} is planned to play ${String(planned)} ms, ${String(GIF_MAX_PLANNED_MS)} ms or more: shorten its holds.`,
      );
    }
    const raw: RawFrame[] = pickFrames(this.shots, this.holds, GIF_TICK_MS).map((shown) =>
      decode(this.shots[shown]?.picture ?? Buffer.alloc(0)),
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

/**
 * Waits until `locator` is in `state`; fails with an error that names `description`,
 * the state waited for, when it is not there after `timeout` ms.
 */
async function waitForState(
  locator: Locator,
  description: string,
  state: 'visible' | 'hidden' = 'visible',
  timeout = STATE_TIMEOUT_MS,
): Promise<void> {
  try {
    await locator.waitFor({ state, timeout });
  } catch (error) {
    const reason =
      error instanceof Error ? (error.message.split('\n')[0] ?? error.message) : String(error);
    throw new Error(`Gave up after ${String(timeout)} ms waiting for ${description}: ${reason}`, {
      cause: error,
    });
  }
}

/**
 * Clicks `locator` once it is on screen; fails with an error that names `description`, the state
 * waited for, when it is not clickable after `timeout` ms.
 */
async function clickWhenReady(
  locator: Locator,
  description: string,
  timeout = STATE_TIMEOUT_MS,
): Promise<void> {
  try {
    await locator.click({ timeout });
  } catch (error) {
    const reason =
      error instanceof Error ? (error.message.split('\n')[0] ?? error.message) : String(error);
    throw new Error(`Gave up after ${String(timeout)} ms waiting for ${description}: ${reason}`, {
      cause: error,
    });
  }
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
  try {
    await page.waitForFunction(
      `(${cursorLineText.toString()})(document)?.includes(${JSON.stringify(text)}) === true`,
      undefined,
      { timeout: STATE_TIMEOUT_MS },
    );
  } catch (error) {
    throw new Error(
      `Gave up after ${String(STATE_TIMEOUT_MS)} ms waiting for the cursor line to hold ${text}`,
      { cause: error },
    );
  }
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
  try {
    await page.waitForFunction(
      `(${cursorLineText.toString()})(document) === ${JSON.stringify(text)}`,
      undefined,
      { timeout: STATE_TIMEOUT_MS },
    );
  } catch (error) {
    throw new Error(
      `Gave up after ${String(STATE_TIMEOUT_MS)} ms waiting for the cursor line to be ${text}`,
      { cause: error },
    );
  }
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
  try {
    await page.waitForFunction(
      `(() => { const elements = Array.from(document.querySelectorAll(${JSON.stringify(selector)})); return elements.length > 0 && elements.every((${widgetShown.toString()})); })()`,
      undefined,
      { timeout: STATE_TIMEOUT_MS },
    );
  } catch (error) {
    throw new Error(
      `Gave up after ${String(STATE_TIMEOUT_MS)} ms waiting for ${description} to be fully shown`,
      { cause: error },
    );
  }
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
    page.locator('.quick-input-list .monaco-list-row', { hasText: name }).first(),
    `the quick-open list to have a row for ${name}`,
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
  // The palette hides a close command whose bar is already hidden, so run each only when its part is on screen.
  if (await page.locator('.part.sidebar').isVisible()) {
    await runCommand(page, 'View: Close Primary Side Bar');
  }
  if (await page.locator('.part.auxiliarybar').isVisible()) {
    await runCommand(page, 'View: Hide Secondary Side Bar');
  }
  await expectSecondarySideBarHidden(page);
}

/** Runs a command through the command palette, by its title. */
async function runCommand(page: Page, title: string): Promise<void> {
  await page.keyboard.press('F1');
  await page.keyboard.type(title);
  await waitForState(
    page.locator('.quick-input-list .monaco-list-row', { hasText: title }).first(),
    `the palette to list the command ${title}`,
  );
  await page.keyboard.press('Enter');
  await waitForQuickInputClosed(page);
}

/** Fails when the secondary side bar (the CHAT panel) is still on screen: no picture may show it. */
async function expectSecondarySideBarHidden(page: Page): Promise<void> {
  await waitForState(
    page.locator('.part.auxiliarybar'),
    'the secondary side bar to be off screen after workbench.action.closeAuxiliaryBar (no picture may show it)',
    'hidden',
  );
}

async function takePictures(page: Page): Promise<void> {
  await prepare(page);
  await openFile(page, CRD);
  await page.keyboard.press('Control+g');
  await page.keyboard.type('31');
  await page.keyboard.press('Enter');
  await waitForQuickInputClosed(page);
  await waitForEngine(page);
  await waitForState(
    page.locator('.monaco-editor .squiggly-error').first(),
    'the editor to underline the wrong field',
    'visible',
    60_000,
  );

  // problems.png: the reason of the wrong field, on the wrong field.
  const wrong = await pointAt(page, 'self.replica > 0', 'replica ');
  await page.mouse.move(wrong.x - 20, wrong.y - 60);
  await page.mouse.move(wrong.x, wrong.y, { steps: 6 });
  await waitForHover(page);
  await save('problems.png', await page.screenshot({ type: 'png' }));

  // quick-fix.png: the fix offered on that problem.
  await page.mouse.move(wrong.x, wrong.y + 120);
  await waitForNoHover(page);
  await page.mouse.click(wrong.x, wrong.y);
  await page.keyboard.press('Control+.');
  await waitForState(
    page.locator('.action-widget .monaco-list-row').first(),
    'the quick fix list to show a fix',
  );
  await waitForWidgetShown(page, '.action-widget', 'the quick fix list');
  await save('quick-fix.png', await page.screenshot({ type: 'png' }));
  await page.keyboard.press('Escape');
  await page.keyboard.press('Control+End');

  // hover.png: the type and description of a field.
  const field = await pointAt(page, 'self.replicas >= self.minReplicas', 'minReplicas');
  await page.mouse.move(field.x - 20, field.y + 80);
  await page.mouse.move(field.x, field.y, { steps: 6 });
  await waitForHover(page);
  await save('hover.png', await page.screenshot({ type: 'png' }));
  await page.mouse.move(field.x, field.y + 200);

  // completion.png and completion.gif: the list after self., one item picked.
  const lastRuleBefore = await readLastRuleLine();
  await moveToLastRule(page);
  await waitForCursorLineIs(page, lastRuleBefore);
  await startNewRule(page);
  await page.keyboard.type('- rule: ');
  await waitForCursorLine(page, '- rule: ');
  let completionPicture: Uint8Array | undefined;
  const completion = await record(page, 'completion.gif', true, async (recording) => {
    await holdFrame(recording, 300);
    await page.keyboard.type('self');
    await holdFrame(recording, 200);
    await page.keyboard.type('.');
    await page.keyboard.press('Control+Space');
    await waitForState(
      page.locator('.suggest-widget.visible .monaco-list-row').first(),
      'the completion list to show an item after self.',
    );
    await waitForWidgetShown(page, '.suggest-widget.visible', 'the completion list');
    await holdFrame(recording, 700);
    await page.keyboard.press('ArrowDown');
    await holdFrame(recording, 500);
    completionPicture = await page.screenshot({ type: 'png' });
    await page.keyboard.press('ArrowDown');
    await holdFrame(recording, 500);
    await page.keyboard.press('Tab');
    await waitForState(
      page.locator('.suggest-widget').first(),
      'the completion list to close after the pick',
      'hidden',
    );
    await holdFrame(recording, 800);
  });
  await save('completion.gif', completion);
  if (completionPicture === undefined) {
    throw new Error('The completion list was never on screen.');
  }
  await save('completion.png', completionPicture);

  // Back to the file as it was.
  for (let step = 0; step < 40; step += 1) {
    await page.keyboard.press('Control+z');
  }
  await moveToLastRule(page);
  await waitForCursorLineIs(page, lastRuleBefore);

  // colors.gif: a rule colored as it is typed, with no completion list in the way.
  await startNewRule(page);
  await page.keyboard.type('- rule: ');
  await waitForCursorLine(page, '- rule: ');
  const colors = await record(page, 'colors.gif', false, async (recording) => {
    await holdFrame(recording, 200);
    await typeText(
      page,
      recording,
      "self.name.startsWith('w') && self.replicas > 1",
      GIF_TYPING_MS,
    );
    await holdFrame(recording, 600);
  });
  await save('colors.gif', colors);

  // colors.png: a whole policy in color.
  await openFile(page, POLICY);
  await waitForEditorText(page, 'no-latest-tag');
  await page.keyboard.press('Control+End');
  await page.mouse.move(VIEWPORT.width - 5, VIEWPORT.height - 5);
  await save('colors.png', await page.screenshot({ type: 'png' }));
}

async function main(): Promise<void> {
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
    for (const name of [CRD, POLICY]) {
      await cp(join(FIXTURES, name), join(workspace, name));
    }
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
    const browser = await chromium.launch({ headless: true });
    try {
      const page = await browser.newPage({ viewport: VIEWPORT, deviceScaleFactor: 1 });
      await page.goto(`${url}?folder=${encodeURIComponent(workspace)}`);
      await waitForState(
        page.locator('.monaco-workbench'),
        'the workbench to load',
        'visible',
        60_000,
      );
      await takePictures(page);
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
