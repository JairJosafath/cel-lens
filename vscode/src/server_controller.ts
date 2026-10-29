import type { ChildProcess } from 'node:child_process';
import { OPEN_ENGINE_SETTING_LABEL, SHOW_LOGS_LABEL } from './button_labels';
import { access, chmod, constants, stat } from 'node:fs/promises';
import { platform } from 'node:process';

import * as vscode from 'vscode';
import {
  CloseAction,
  type CloseHandlerResult,
  ErrorAction,
  LanguageClient,
} from 'vscode-languageclient/node';

import { RELEASES_PAGE_URL } from './core_releases';
import { EngineErrorHandler } from './engine_error_handler';
import { EXPERIMENTAL_SETTING, parseExperimental } from './experimental';
import {
  KUBERNETES_VERSION_SETTING,
  configurationParams,
  parseVersionInfo,
  serverOptions,
  type ServerState,
  type VersionInfo,
} from './kubernetes_version';
import { SERVER_PATH_SETTING, openServerPathSetting } from './open_server_setting';
import { StartTrace, startFailureMessage } from './server_failure';
import { spawnEngine } from './spawn_engine';
import { resolveServer, type ServerSource } from './server_path';

function readVersionSetting(): string {
  return vscode.workspace.getConfiguration('cel-lens').get<string>(KUBERNETES_VERSION_SETTING, '');
}

function readExperimentalSetting(): string[] {
  return parseExperimental(
    vscode.workspace.getConfiguration('cel-lens').get<unknown>(EXPERIMENTAL_SETTING, []),
  );
}

async function isFile(path: string): Promise<boolean> {
  try {
    return (await stat(path)).isFile();
  } catch {
    return false;
  }
}

/** Makes the bundled binary executable: a `.vsix` unpacked without the mode bits loses them. */
async function ensureExecutable(path: string): Promise<void> {
  try {
    await access(path, constants.X_OK);
  } catch {
    await chmod(path, 0o755);
  }
}

/**
 * A client that stays quiet until it has started: when `start()` fails, the base class always
 * pops up its own "couldn't create connection" notification, and the controller already
 * reports the failure once, with the server's error output.
 */
class CelLensClient extends LanguageClient {
  quietUntilStarted = true;

  override error(message: string, data?: unknown, showNotification?: boolean | 'force'): void {
    super.error(message, data, this.quietUntilStarted ? false : showNotification);
  }
}

export const END_WAIT_MS = 2000;

/** Starts, stops and restarts the `cel-lens-core lsp` language client. */
export class ServerController implements vscode.Disposable {
  private client: LanguageClient | undefined;
  private queue: Promise<void> = Promise.resolve();
  /** The versions the last server reported; a restart uses them before the new server answers. */
  private info: VersionInfo | undefined;
  /** The version the running server was last told; empty means its default. */
  private sentVersion = '';
  /** The experimental list the running server was last told, as one string. */
  private sentExperimental = '';

  constructor(
    private readonly log: vscode.LogOutputChannel,
    private readonly extensionPath: string,
    private readonly onState: (state: ServerState) => void = () => undefined,
  ) {}

  /**
   * Tells the running server the `cel-lens.kubernetesVersion` and `cel-lens.experimental`
   * settings, without restarting it. Queued behind a start or restart in progress, so a change
   * made while the server starts still arrives. Never rejects.
   */
  sendSettings(): Promise<void> {
    return this.enqueue(() => this.sendSettingsNow());
  }

  private async sendSettingsNow(): Promise<void> {
    const info = this.info;
    const experimental = readExperimentalSetting();
    const options = serverOptions(readVersionSetting(), info, experimental);
    const experimentalKey = experimental.join(',');
    try {
      if (this.client === undefined) {
        return;
      }
      if (info?.supported.length === 0) {
        // The server does not say which versions it accepts, only the one it uses after
        // `initialize`, so a change restarts it and reads that.
        if (
          options.kubernetesVersion !== this.sentVersion ||
          experimentalKey !== this.sentExperimental
        ) {
          await this.stopNow();
          await this.startNow();
        }
        return;
      }
      await this.client.sendNotification(
        'workspace/didChangeConfiguration',
        configurationParams(readVersionSetting(), info, experimental),
      );
      this.sentVersion = options.kubernetesVersion;
      this.sentExperimental = experimentalKey;
      this.onState({ kind: 'running', info });
    } catch (error) {
      this.log.error(`Could not tell the engine the settings: ${String(error)}`);
    }
  }

  /** Starts the server unless it is running. Never rejects. */
  start(): Promise<void> {
    return this.enqueue(() => this.startNow());
  }

  /** Stops the server, if it is running. Never rejects. */
  stop(): Promise<void> {
    return this.enqueue(() => this.stopNow());
  }

  /** Stops the server and starts it again, finding the binary anew. */
  restart(): Promise<void> {
    return this.enqueue(async () => {
      await this.stopNow();
      await this.startNow();
    });
  }

  dispose(): void {
    void this.stop();
  }

  /** Runs operations one after the other, so quick restarts never overlap. */
  private enqueue(operation: () => Promise<void>): Promise<void> {
    this.queue = this.queue.then(operation, operation);
    return this.queue;
  }

  private async startNow(): Promise<void> {
    if (this.client !== undefined) {
      return;
    }
    this.onState({ kind: 'starting' });
    const setting = vscode.workspace.getConfiguration('cel-lens').get<string>('serverPath', '');
    const server = await resolveServer({
      setting,
      extensionPath: this.extensionPath,
      platform,
      isFile,
    });
    if (!server.found) {
      this.onState({ kind: 'failed' });
      this.log.error(server.message);
      // Not awaited: a notification stays pending while it is open, and must not block a restart.
      void vscode.window
        .showErrorMessage(
          server.message,
          server.source === 'setting' ? OPEN_ENGINE_SETTING_LABEL : SHOW_LOGS_LABEL,
        )
        .then((button) => this.handle(button));
      return;
    }

    try {
      if (server.source === 'bundled' && platform !== 'win32') {
        await ensureExecutable(server.command);
      }
    } catch (error) {
      this.onState({ kind: 'failed' });
      this.reportStartFailure(
        error instanceof Error ? error.message : String(error),
        server.source,
      );
      return;
    }
    this.log.info(
      `Starting ${server.command} ${server.args.join(' ')} (from the ${server.source})`,
    );
    const sentOptions = serverOptions(readVersionSetting(), this.info, readExperimentalSetting());
    this.sentVersion = sentOptions.kubernetesVersion;
    this.sentExperimental = sentOptions.experimental.join(',');
    const trace = new StartTrace(server.command);
    let started = false;
    let shutdownReported = false;
    // One handler per client, so its count of crashes survives from one crash to the next.
    const lateHandler = new EngineErrorHandler(this.log);
    const client: CelLensClient = new CelLensClient(
      'cel-lens',
      'cel-lens',
      // Spawned here, not by the client, to keep the server's stderr for the failure message.
      () => Promise.resolve(this.spawnServer(server.command, server.args, trace)),
      {
        // Files on disk only: untitled YAML buffers are not analysed.
        documentSelector: [{ scheme: 'file', language: 'yaml' }],
        outputChannel: this.log,
        initializationOptions: sentOptions,
        // A server that dies while starting is reported once below, never restarted until the
        // client gives up; one that dies later gets the client's usual restarts.
        errorHandler: {
          error: (error, message, count) => {
            if (!started) {
              return { action: ErrorAction.Shutdown, handled: true };
            }
            const result = lateHandler.error(error, message, count);
            if (result.action === ErrorAction.Shutdown && !shutdownReported) {
              shutdownReported = true;
              this.onState({ kind: 'crashed' });
            }
            return result;
          },
          closed: () => {
            if (!started) {
              return { action: CloseAction.DoNotRestart, handled: true };
            }
            const result = lateHandler.closed();
            const report = (final: CloseHandlerResult): CloseHandlerResult => {
              if (final.action === CloseAction.DoNotRestart) {
                this.onState({ kind: 'crashed' });
              }
              return final;
            };
            return result instanceof Promise ? result.then(report) : report(result);
          },
        },
        // The failure is reported below with the server's stderr, not by the client.
        initializationFailedHandler: () => false,
      },
    );
    try {
      await client.start();
      started = true;
      client.quietUntilStarted = false;
      this.client = client;
      const info = parseVersionInfo(client.initializeResult, sentOptions.kubernetesVersion);
      if (info === undefined) {
        this.log.warn(
          `The engine reports no Kubernetes versions, so the version cannot be shown or picked. Check that ${SERVER_PATH_SETTING} points to cel-lens-core v0.1.0 or newer, or clear it. Download a current engine from ${RELEASES_PAGE_URL} (engine releases are tagged core-vX.Y.Z).`,
        );
      }
      this.info = info;
      this.onState({ kind: 'running', info });
      // The first start cannot know the supported versions, so it sends the raw setting;
      // once the server has reported them, correct it if the engine default replaces it.
      if (
        serverOptions(readVersionSetting(), info, sentOptions.experimental).kubernetesVersion !==
        sentOptions.kubernetesVersion
      ) {
        await this.sendSettingsNow();
      }
    } catch (error) {
      this.onState({ kind: 'failed' });
      // The client rejects as soon as its write fails, often before the engine's stderr and exit arrive.
      await waitForEnd(trace.ended);
      this.reportStartFailure(
        error instanceof Error ? error.message : String(error),
        server.source,
        trace,
      );
      await this.disposeFailed(client);
    }
  }

  /** Runs the server over stdio and keeps its stderr, spawn error and exit code in `trace`; the client logs the output. */
  private spawnServer(command: string, args: readonly string[], trace: StartTrace): ChildProcess {
    const child = spawnEngine(command, args);
    child.stderr.setEncoding('utf8');
    child.stderr.on('data', (chunk: string) => {
      trace.stderr.add(chunk);
    });
    child.on('exit', (code) => {
      trace.exitCode = code ?? undefined;
    });
    trace.ended = new Promise<void>((resolve) => {
      child.once('close', () => {
        resolve();
      });
      child.once('error', () => {
        resolve();
      });
    });
    // The client never logs a spawn error (EACCES, ENOEXEC), so the log gets it here.
    child.on('error', (error: NodeJS.ErrnoException) => {
      this.log.error(error.message);
      trace.spawnError ??= { code: error.code, message: error.message };
    });
    return child;
  }

  /** Logs why the server could not start and tells the user once, with a button to the fix. */
  private reportStartFailure(
    reason: string,
    source: ServerSource,
    trace = new StartTrace(''),
  ): void {
    // The engine's output and any spawn error are already in the log, so it carries only the reason.
    this.log.error(`The cel-lens engine failed to start: ${reason}`);
    const message = startFailureMessage(reason, trace, source);
    // Not awaited: a notification stays pending while it is open, and must not block a restart.
    void vscode.window
      .showErrorMessage(message, source === 'setting' ? OPEN_ENGINE_SETTING_LABEL : SHOW_LOGS_LABEL)
      .then((button) => this.handle(button));
  }

  /** A client that failed to start cannot be stopped, so its dispose rejects; that is expected. */
  private async disposeFailed(client: LanguageClient): Promise<void> {
    try {
      await client.dispose();
    } catch (error) {
      this.log.debug(`Disposing the client that failed to start: ${String(error)}`);
    }
  }

  private async stopNow(): Promise<void> {
    const client = this.client;
    this.client = undefined;
    this.onState({ kind: 'stopped' });
    try {
      await client?.stop();
    } catch (error) {
      this.log.error(`The engine did not stop cleanly: ${String(error)}`);
    }
  }

  private async handle(button: string | undefined): Promise<void> {
    try {
      if (button === SHOW_LOGS_LABEL) {
        this.log.show();
      } else if (button === OPEN_ENGINE_SETTING_LABEL) {
        await openServerPathSetting();
      }
    } catch (error) {
      this.log.error(`Could not follow the message button: ${String(error)}`);
    }
  }
}

/** Waits for a process to end, but not forever: a hung engine must not hold back the message. */
async function waitForEnd(ended: Promise<void> | undefined): Promise<void> {
  if (ended === undefined) {
    return;
  }
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<void>((resolve) => {
    timer = setTimeout(resolve, END_WAIT_MS);
  });
  await Promise.race([ended, timeout]);
  clearTimeout(timer);
}
