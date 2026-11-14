import type { ChildProcess } from 'node:child_process';
import {
  OPEN_ENGINE_SETTING_LABEL,
  OPEN_EXPERIMENTAL_SETTING_LABEL,
  REPLACE_EXPERIMENTAL_LABEL,
  SHOW_LOGS_LABEL,
} from './button_labels';
import { access, chmod, constants, stat } from 'node:fs/promises';
import { platform } from 'node:process';

import * as vscode from 'vscode';
import {
  type CancellationToken,
  CloseAction,
  type CloseHandlerResult,
  ErrorAction,
  LanguageClient,
} from 'vscode-languageclient/node';

import { RELEASES_PAGE_URL } from './core_releases';
import { EngineErrorHandler } from './engine_error_handler';
import {
  EngineMessages,
  SHOW_MESSAGE_METHOD,
  SHOW_MESSAGE_REQUEST_METHOD,
  type ShowMessageParams,
  type ShowMessageRequestParams,
} from './engine_messages';
import {
  EXPERIMENTAL_SETTING,
  parseExperimental,
  replaceExperimental,
  suggestions,
  unknownExperimental,
  unknownExperimentalMessage,
} from './experimental';
import {
  KUBERNETES_VERSION_SETTING,
  configurationParams,
  parseExperimentalFeatures,
  parseVersionInfo,
  serverOptions,
  type ServerState,
  type VersionInfo,
} from './kubernetes_version';
import { SHOW_EXAMPLE_RESULTS_COMMAND } from './example_results';
import { engineExampleResults, readExampleResults } from './example_results_setting';
import { engineInlineInfo, readInlineInfo } from './inline_info_setting';
import { LIST_VALUES_COMMAND, listValuesArguments } from './rule_parts';
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

/** The files the engine reads the types of `dyn` values from, whatever language they open as. */
const CELLENS_PATTERN = '**/.cellens';

/** The LSP request that runs a command of the engine. */
const EXECUTE_COMMAND = 'workspace/executeCommand';

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

  /**
   * Leaves out the client's own handling of `workspace/executeCommand`, which registers each
   * command the engine runs as a VS Code command. The engine's `cel-lens.removeType` has the
   * name of the line's part, the extension's own command, and a second registration of a
   * command throws, which would fail the start. The extension runs the engine's commands itself
   * (`runEngineCommand`), so it can cancel a check and say once why one failed.
   */
  override registerFeature(feature: Parameters<LanguageClient['registerFeature']>[0]): void {
    if ('registrationType' in feature && feature.registrationType.method === EXECUTE_COMMAND) {
      return;
    }
    super.registerFeature(feature);
  }
}

/** A cancellation token of the language client that fires when `signal` does. */
function cancellationToken(signal: AbortSignal): CancellationToken {
  return {
    get isCancellationRequested() {
      return signal.aborted;
    },
    onCancellationRequested: (listener) => {
      const fire = (): void => {
        listener(undefined);
      };
      signal.addEventListener('abort', fire, { once: true });
      return {
        dispose: () => {
          signal.removeEventListener('abort', fire);
        },
      };
    },
  };
}

/**
 * Gives the `+N more` lens's command the line it is on, which a command never learns from VS
 * Code, so the list it opens can name the rule: `cel-lens.listValues` takes one argument, the
 * line and the engine's parts. A lens with no command, or another command, stays as it is.
 */
function withRuleLine(lens: vscode.CodeLens): vscode.CodeLens {
  if (lens.command?.command === LIST_VALUES_COMMAND) {
    lens.command = {
      ...lens.command,
      arguments: listValuesArguments(lens.range.start.line, lens.command.arguments),
    };
  }
  return lens;
}

/**
 * The lenses without the part that says what a rule does to its examples, when the example
 * results are off for the file: the engine has one switch for the window, on while any workspace
 * folder has them on.
 */
function withoutExampleResults(
  lenses: vscode.CodeLens[] | null | undefined,
  resource: vscode.Uri,
): vscode.CodeLens[] | null | undefined {
  return lenses !== null && lenses !== undefined && !readExampleResults(resource)
    ? lenses.filter((lens) => lens.command?.command !== SHOW_EXAMPLE_RESULTS_COMMAND)
    : lenses;
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
  /** The experimental names the running engine listed in its initialize result. */
  private knownFeatures: readonly string[] = [];
  /** The unknown names last warned about, as one string, so the same list is never shown twice. */
  private warnedUnknown = '';
  /** The handlers of the engine's notifications, by method; each new client gets them all. */
  private readonly notifications = new Map<string, (params: unknown) => void>();

  /**
   * `onStarted` gets the `initialize` result of each engine that starts, once it runs and has been
   * told the settings, so a client can ask it for what it lists.
   */
  constructor(
    private readonly log: vscode.LogOutputChannel,
    private readonly extensionPath: string,
    private readonly onState: (state: ServerState) => void = () => undefined,
    private readonly onStarted: (initializeResult: unknown) => void = () => undefined,
  ) {}

  /**
   * Has `handler` run for each notification of `method` an engine sends, from the engine's next
   * start on and after every restart. One handler for a method; a second replaces the first.
   */
  onNotification(method: string, handler: (params: unknown) => void): void {
    this.notifications.set(method, handler);
  }

  /**
   * Tells the running server the `cel-lens.kubernetesVersion`, `cel-lens.experimental`,
   * `cel-lens.inlineInfo` and `cel-lens.exampleResults` settings, without restarting it. Queued behind a start or restart in
   * progress, so a change made while the server starts still arrives. Never rejects.
   */
  sendSettings(): Promise<void> {
    return this.enqueue(() => this.sendSettingsNow());
  }

  /**
   * Tells the running server the settings, then has VS Code ask for the lines above the rules
   * again: the engine asks for them itself when its switch flips, but a workspace folder's own
   * `cel-lens.inlineInfo` or `cel-lens.exampleResults` shows or hides its lines, or the part of
   * them that says what a rule does to its examples, in the client alone. Never rejects.
   */
  sendLineSettings(): Promise<void> {
    return this.enqueue(async () => {
      await this.sendSettingsNow();
      this.refreshCodeLenses();
    });
  }

  /**
   * Runs one of the engine's commands, such as `cel-lens.setType`, with `workspace/executeCommand`
   * and its one argument; `signal` cancels it. Resolves to the engine's answer, and rejects with
   * the client's ResponseError when the engine refuses, or when no engine is running.
   */
  runEngineCommand(command: string, argument?: unknown, signal?: AbortSignal): Promise<unknown> {
    const client = this.client;
    if (client === undefined) {
      return Promise.reject(new Error('the cel-lens engine is not running'));
    }
    const params = { command, arguments: argument === undefined ? [] : [argument] };
    return signal === undefined
      ? client.sendRequest<unknown>(EXECUTE_COMMAND, params)
      : client.sendRequest<unknown>(EXECUTE_COMMAND, params, cancellationToken(signal));
  }

  /** Fires the change event of the code lens provider of each visible editor, once per provider. */
  private refreshCodeLenses(): void {
    const feature = this.client?.getFeature('textDocument/codeLens');
    if (feature === undefined) {
      return;
    }
    const emitters = new Set<vscode.EventEmitter<void>>();
    for (const editor of vscode.window.visibleTextEditors) {
      const provider = feature.getProvider(editor.document);
      if (provider !== undefined) {
        emitters.add(provider.onDidChangeCodeLensEmitter);
      }
    }
    for (const emitter of emitters) {
      emitter.fire();
    }
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
      const params = configurationParams(readVersionSetting(), info, experimental);
      await this.client.sendNotification('workspace/didChangeConfiguration', {
        settings: {
          ...params.settings,
          inlineInfo: engineInlineInfo(),
          exampleResults: engineExampleResults(),
        },
      });
      this.sentVersion = options.kubernetesVersion;
      this.sentExperimental = experimentalKey;
      this.onState({ kind: 'running', info });
      this.warnUnknownExperimental(experimental);
    } catch (error) {
      this.log.error(`Could not tell the engine the settings: ${String(error)}`);
    }
  }

  /**
   * Tells the user once which names of `cel-lens.experimental` the engine does not know. An engine
   * that lists no features (an older one) gives nothing to compare with, so it warns of nothing.
   */
  private warnUnknownExperimental(experimental: readonly string[]): void {
    if (this.knownFeatures.length === 0) {
      return;
    }
    const names = unknownExperimental(experimental, this.knownFeatures);
    const key = names.join(',');
    if (key === this.warnedUnknown) {
      return;
    }
    this.warnedUnknown = key;
    if (names.length === 0) {
      return;
    }
    // Not awaited: a notification stays pending while it is open, and must not block a restart.
    const fixes = suggestions(names, this.knownFeatures);
    const buttons =
      fixes === undefined
        ? [OPEN_EXPERIMENTAL_SETTING_LABEL]
        : [REPLACE_EXPERIMENTAL_LABEL, OPEN_EXPERIMENTAL_SETTING_LABEL];
    void vscode.window
      .showWarningMessage(unknownExperimentalMessage(names, this.knownFeatures), ...buttons)
      .then((button) =>
        button === REPLACE_EXPERIMENTAL_LABEL && fixes !== undefined
          ? this.replaceExperimentalNames(fixes)
          : this.handle(button),
      );
  }

  /** Rewrites the misspelt names in each scope of `cel-lens.experimental` that lists them. */
  private async replaceExperimentalNames(fixes: ReadonlyMap<string, string>): Promise<void> {
    try {
      const scopes = [
        { target: vscode.ConfigurationTarget.Global, key: 'globalValue' },
        { target: vscode.ConfigurationTarget.Workspace, key: 'workspaceValue' },
      ] as const;
      const configuration = vscode.workspace.getConfiguration('cel-lens');
      for (const { target, key } of scopes) {
        const replaced = replaceExperimental(
          configuration.inspect<unknown>(EXPERIMENTAL_SETTING)?.[key],
          fixes,
        );
        if (replaced !== undefined) {
          await configuration.update(EXPERIMENTAL_SETTING, replaced, target);
        }
      }
    } catch (error) {
      this.log.error(`Could not replace the experimental names: ${String(error)}`);
      // Not awaited: a notification stays pending while it is open.
      void vscode.window
        .showWarningMessage(
          'cel-lens could not change the cel-lens.experimental setting. Fix any errors in settings.json, then correct the names by hand.',
          OPEN_EXPERIMENTAL_SETTING_LABEL,
        )
        .then((button) => this.handle(button));
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
        // Files on disk only: untitled YAML buffers are not analysed. A .cellens opens as plain
        // text, but the engine reads it with the editor's text, so the type it wrote is used,
        // and its next edit made on that text, before the file is saved.
        documentSelector: [
          { scheme: 'file', language: 'yaml' },
          { scheme: 'file', pattern: CELLENS_PATTERN },
        ],
        outputChannel: this.log,
        initializationOptions: {
          ...sentOptions,
          inlineInfo: engineInlineInfo(),
          exampleResults: engineExampleResults(),
          // The engine keeps the list of the rules of the workspace for the cel-lens view.
          ruleList: true,
        },
        middleware: {
          // The engine has one switch for the window for each setting; a workspace folder that
          // turns the lines off loses them here, and one that turns the example results off loses
          // their part. The +N more lens learns its line.
          provideCodeLenses: async (document, token, next) =>
            readInlineInfo(document.uri)
              ? withoutExampleResults(await next(document, token), document.uri)?.map(withRuleLine)
              : [],
          // A lens the engine fills in later gets its line too.
          resolveCodeLens: async (lens, token, next) => {
            const resolved = await next(lens, token);
            return resolved === null || resolved === undefined ? resolved : withRuleLine(resolved);
          },
        },
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
    // Registered before start, so it replaces the client's own showMessage handler.
    const messages = new EngineMessages(this.log);
    client.onNotification(SHOW_MESSAGE_METHOD, (params: ShowMessageParams) => {
      messages.show(params);
    });
    for (const [method, handler] of this.notifications) {
      client.onNotification(method, handler);
    }
    // The same for the requests with actions, one of which turns on an experimental feature.
    client.onRequest(SHOW_MESSAGE_REQUEST_METHOD, (params: ShowMessageRequestParams) =>
      messages.request(params),
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
      this.knownFeatures = parseExperimentalFeatures(client.initializeResult);
      this.onState({ kind: 'running', info });
      this.warnUnknownExperimental(sentOptions.experimental);
      // The first start cannot know the supported versions, so it sends the raw setting;
      // once the server has reported them, correct it if the engine default replaces it.
      if (
        serverOptions(readVersionSetting(), info, sentOptions.experimental).kubernetesVersion !==
        sentOptions.kubernetesVersion
      ) {
        await this.sendSettingsNow();
      }
      // Not when the settings just restarted the engine: the new one says it itself.
      if (this.client === client) {
        this.onStarted(client.initializeResult);
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
      } else if (button === OPEN_EXPERIMENTAL_SETTING_LABEL) {
        await vscode.commands.executeCommand(
          'workbench.action.openSettings',
          `cel-lens.${EXPERIMENTAL_SETTING}`,
        );
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
