import { tmpdir, userInfo } from 'node:os';

import * as vscode from 'vscode';

import { SHOW_LOGS_COMMAND } from './actions_menu';
import { RESTART_ENGINE_LABEL, SHOW_LOGS_LABEL } from './button_labels';
import { RESTART_SERVER_COMMAND, type ServerState } from './kubernetes_version';
import { SAMPLE_CRD, SAMPLE_PATH, SAMPLE_RULES_LINE } from './sample_crd';
import { TempSample, userTempFolder } from './sample_folder';

/** The `cel-lens.getStarted` command, as contributed in package.json: opens the walkthrough. */
export const GET_STARTED_COMMAND = 'cel-lens.getStarted';

/** The `cel-lens.openSample` command, the first step's button. */
export const OPEN_SAMPLE_COMMAND = 'cel-lens.openSample';

/** The `cel-lens.fixSampleProblem` command, the second step's button. */
export const FIX_SAMPLE_PROBLEM_COMMAND = 'cel-lens.fixSampleProblem';

/** The context key that completes the second step: the open sample had an error and has none. */
export const SAMPLE_FIXED_CONTEXT = 'cel-lens.sampleFixed';

/** The walkthrough, by the extension's id (`publisher.name`) and its own id. */
export const WALKTHROUGH_ID = 'cel-lens.cel-lens-vscode#getStarted';

/** How long *Fix the Problem* waits for the engine to report the sample's problem. */
export const SAMPLE_PROBLEM_WAIT_MS = 10_000;

/** VS Code's command that opens the quick fix list at the cursor. */
const QUICK_FIX_COMMAND = 'editor.action.quickFix';

type GetStartedLog = Pick<vscode.LogOutputChannel, 'info' | 'error'>;

interface FoundError {
  readonly line: number;
  readonly character: number;
}

/** The user id files are made with (the effective one), where the system has ids; Windows has none. */
function currentUid(): number | undefined {
  return process.geteuid?.();
}

/**
 * The folder of the system's temporary folder for this user's sample, when the extension's
 * storage is not on disk (the engine checks only `file:`): named for the user id, or the user
 * name where the system has no ids.
 */
export function tempFolder(): string {
  return userTempFolder(tmpdir(), tempUser());
}

function tempUser(): number | string {
  return currentUid() ?? userInfo().username;
}

/** Opens the *Get started with cel-lens* walkthrough on the Welcome page. */
export async function openWalkthrough(): Promise<void> {
  await vscode.commands.executeCommand('workbench.action.openWalkthrough', WALKTHROUGH_ID);
}

/**
 * The walkthrough's sample CRD: writes it to the extension's storage, shows it, finds its problem
 * for the quick fix, and sets `cel-lens.sampleFixed` once the problem is gone. Nothing here runs
 * until a person presses a step's button.
 */
export class GetStarted implements vscode.Disposable {
  /** The sample's place in the temporary folder, when the extension's storage is not on disk. */
  private readonly temp: TempSample | undefined;
  /** The sample; in the temporary folder it moves once, when the named folder is not the user's. */
  private sample: vscode.Uri;
  private readonly subscriptions: vscode.Disposable[];
  /** The version of the sample's document when it last had an error, while it is open. */
  private errorVersion: number | undefined;
  private fixed = false;
  private fixing = false;

  constructor(
    private readonly storage: vscode.Uri,
    private readonly engineState: () => ServerState,
    private readonly log: GetStartedLog,
  ) {
    this.temp =
      storage.scheme === 'file' ? undefined : new TempSample(tmpdir(), tempUser(), currentUid());
    this.sample =
      this.temp === undefined
        ? vscode.Uri.joinPath(storage, ...SAMPLE_PATH)
        : vscode.Uri.file(this.temp.path);
    this.subscriptions = [
      vscode.languages.onDidChangeDiagnostics((event) => {
        if (event.uris.some((uri) => this.isSample(uri))) {
          this.diagnosticsChanged();
        }
      }),
      vscode.workspace.onDidCloseTextDocument((document) => {
        if (this.isSample(document.uri)) {
          this.errorVersion = undefined;
        }
      }),
    ];
  }

  /**
   * *Open the Sample CRD*: writes a fresh copy unless the sample is open in an editor, so the
   * problem is back on a second run, then shows it in an editor that stays open, a fresh copy at
   * its rules. Gives the URI, or undefined when the sample could not be written or shown, which
   * it says once.
   */
  async openSample(): Promise<vscode.Uri | undefined> {
    try {
      if (this.isOpen()) {
        await vscode.window.showTextDocument(this.sample, { preview: false });
        return this.sample;
      }
      if (!(await this.write())) {
        return undefined;
      }
      this.log.info(`Wrote the sample CRD to ${this.sample.toString()}.`);
      this.errorVersion = undefined;
      await this.setFixed(false);
      // The cursor at the rules, so a short window shows them rather than the top of the file.
      const rules = new vscode.Range(SAMPLE_RULES_LINE, 0, SAMPLE_RULES_LINE, 0);
      await vscode.window.showTextDocument(this.sample, { preview: false, selection: rules });
      return this.sample;
    } catch (error) {
      this.log.error(
        `Could not open the sample CRD at ${this.sample.toString()}: ${String(error)}`,
      );
      const choice = await vscode.window.showErrorMessage(
        `cel-lens could not open the sample CRD at ${this.sample.toString()}: ${errorText(error)}`,
        SHOW_LOGS_LABEL,
      );
      if (choice === SHOW_LOGS_LABEL) {
        await vscode.commands.executeCommand(SHOW_LOGS_COMMAND);
      }
      return undefined;
    }
  }

  /**
   * *Fix the Problem*: shows the sample as *Open the Sample CRD* does, waits for the engine's
   * error on it, puts the cursor there and opens the quick fix list. With no error in time, it
   * says why: the engine is not running, or the sample has no problem left.
   */
  async fixProblem(): Promise<void> {
    if (this.fixing) {
      return;
    }
    this.fixing = true;
    try {
      const uri = await this.openSample();
      if (uri === undefined) {
        return;
      }
      const error = await this.firstError();
      if (error === undefined) {
        await this.sayWhyNoProblem();
        return;
      }
      const at = new vscode.Range(error.line, error.character, error.line, error.character);
      await vscode.window.showTextDocument(uri, { preview: false, selection: at });
      await vscode.commands.executeCommand(QUICK_FIX_COMMAND);
    } finally {
      this.fixing = false;
    }
  }

  /**
   * Writes a fresh sample: with `workspace.fs` into the extension's storage, or into this user's
   * own temporary folder (a private one when another user holds the named one), never through a
   * symbolic link nor into a folder another user owns.
   * Says once why it refused, and gives false then.
   */
  private async write(): Promise<boolean> {
    if (this.temp === undefined) {
      await vscode.workspace.fs.createDirectory(vscode.Uri.joinPath(this.storage, SAMPLE_PATH[0]));
      await vscode.workspace.fs.writeFile(this.sample, new TextEncoder().encode(SAMPLE_CRD));
      return true;
    }
    const outcome = await this.temp.write(SAMPLE_CRD);
    if (outcome.written) {
      this.sample = vscode.Uri.file(outcome.path);
      return true;
    }
    this.log.error(`Did not write the sample CRD: ${outcome.reason}.`);
    // "Remove it" only where the person can: something in their own folder.
    await vscode.window.showErrorMessage(
      outcome.fixable
        ? `cel-lens did not write the sample CRD, because ${outcome.reason}. Remove it, then open the sample again.`
        : `cel-lens cannot write the sample CRD safely, because ${outcome.reason}.`,
    );
    return false;
  }

  dispose(): void {
    for (const subscription of this.subscriptions) {
      subscription.dispose();
    }
  }

  private isSample(uri: vscode.Uri): boolean {
    return uri.toString() === this.sample.toString();
  }

  /** Whether a tab shows the sample, edited or not. */
  private isOpen(): boolean {
    return vscode.window.tabGroups.all.some((group) =>
      group.tabs.some(
        (tab) => tab.input instanceof vscode.TabInputText && this.isSample(tab.input.uri),
      ),
    );
  }

  /** The sample's errors, the first in the text first. */
  private errors(): FoundError[] {
    return vscode.languages
      .getDiagnostics(this.sample)
      .filter((diagnostic) => diagnostic.severity === vscode.DiagnosticSeverity.Error)
      .map(({ range }) => ({ line: range.start.line, character: range.start.character }))
      .sort((a, b) => a.line - b.line || a.character - b.character);
  }

  private engineDown(): boolean {
    const { kind } = this.engineState();
    return kind === 'failed' || kind === 'crashed' || kind === 'stopped';
  }

  /** The sample's first error, waiting up to `SAMPLE_PROBLEM_WAIT_MS` for one while the engine runs. */
  private firstError(): Promise<FoundError | undefined> {
    const now = this.errors()[0];
    if (now !== undefined || this.engineDown()) {
      return Promise.resolve(now);
    }
    return new Promise((resolve) => {
      const done = (found: FoundError | undefined): void => {
        clearTimeout(timer);
        listener.dispose();
        resolve(found);
      };
      const timer = setTimeout(() => {
        done(undefined);
      }, SAMPLE_PROBLEM_WAIT_MS);
      const listener = vscode.languages.onDidChangeDiagnostics((event) => {
        const found = event.uris.some((uri) => this.isSample(uri)) ? this.errors()[0] : undefined;
        if (found !== undefined) {
          done(found);
        }
      });
    });
  }

  private async sayWhyNoProblem(): Promise<void> {
    if (this.engineState().kind === 'running') {
      await vscode.window.showInformationMessage(
        'The sample CRD has no problem left to fix. Close it, and Open the Sample CRD writes a fresh one.',
      );
      return;
    }
    const choice = await vscode.window.showErrorMessage(
      'The cel-lens engine is not running, so it cannot check the sample CRD.',
      SHOW_LOGS_LABEL,
      RESTART_ENGINE_LABEL,
    );
    if (choice === SHOW_LOGS_LABEL) {
      await vscode.commands.executeCommand(SHOW_LOGS_COMMAND);
    } else if (choice === RESTART_ENGINE_LABEL) {
      await vscode.commands.executeCommand(RESTART_SERVER_COMMAND);
    }
  }

  /**
   * Sets `cel-lens.sampleFixed` once the open sample, having had an error, has none. The
   * document must have changed since the error, so diagnostics the client clears (a restart of
   * the engine) do not count as a fix.
   */
  private diagnosticsChanged(): void {
    const document = vscode.workspace.textDocuments.find((each) => this.isSample(each.uri));
    if (document === undefined) {
      return;
    }
    // The first errors can come before the sample's tab does: the document is open, so they count.
    if (this.errors().length > 0) {
      this.errorVersion = document.version;
      return;
    }
    if (this.errorVersion !== undefined && document.version > this.errorVersion && this.isOpen()) {
      this.errorVersion = undefined;
      void this.setFixed(true);
    }
  }

  private async setFixed(fixed: boolean): Promise<void> {
    if (this.fixed === fixed) {
      return;
    }
    this.fixed = fixed;
    await vscode.commands.executeCommand('setContext', SAMPLE_FIXED_CONTEXT, fixed);
  }
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
