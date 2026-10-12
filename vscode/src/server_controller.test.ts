import type { ServerState } from './kubernetes_version';
import { ChildProcess } from 'node:child_process';
import { chmod, mkdir, mkdtemp, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execPath } from 'node:process';
import { PassThrough } from 'node:stream';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type LogChannel,
  channels,
  errorChoice,
  executedCommands,
  reset,
  settings,
  shownErrors,
  window,
} from '../test/vscode';
import { END_WAIT_MS, ServerController } from './server_controller';

const EXTENSION_PATH = '/nowhere/ext';

const client = vi.hoisted(() => ({
  created: [] as { id: string; server: unknown; options: unknown }[],
  started: 0,
  stopped: 0,
  disposed: 0,
  disposeError: undefined as Error | undefined,
  startError: undefined as Error | undefined,
  initializeResult: undefined as Record<string, unknown> | undefined,
  notifications: [] as { method: string; params: unknown }[],
  startGate: undefined as Promise<void> | undefined,
}));

const fsFault = vi.hoisted(() => ({ chmodError: undefined as Error | undefined }));

vi.mock('node:fs/promises', async (importOriginal) => {
  const original = await importOriginal<typeof import('node:fs/promises')>();
  return {
    ...original,
    chmod: (...args: Parameters<typeof original.chmod>) =>
      fsFault.chmodError === undefined
        ? original.chmod(...args)
        : Promise.reject(fsFault.chmodError),
  };
});

const engine = vi.hoisted(() => ({ hung: undefined as ChildProcess | undefined }));
const spawned = vi.hoisted(() => ({ calls: [] as { command: string; args: readonly string[] }[] }));

vi.mock('./spawn_engine', async (importOriginal) => {
  const original = await importOriginal<typeof import('./spawn_engine')>();
  return {
    ...original,
    spawnEngine: (command: string, args: readonly string[]) => {
      spawned.calls.push({ command, args });
      return engine.hung ?? original.spawnEngine(command, args);
    },
  };
});

vi.mock('vscode-languageclient/node', async () => {
  const { window: shown } = await import('vscode');
  return {
    CloseAction: { DoNotRestart: 1, Restart: 2 },
    ErrorAction: { Continue: 1, Shutdown: 2 },
    LanguageClient: class {
      constructor(id: string, _name: string, server: unknown, options: unknown) {
        client.created.push({ id, server, options });
      }

      get initializeResult(): Record<string, unknown> | undefined {
        return client.initializeResult;
      }

      sendNotification(method: string, params: unknown): Promise<void> {
        client.notifications.push({ method, params });
        return Promise.resolve();
      }

      /** Like the real client: shows a forced error as a notification, whatever the options say. */
      error(message: string, _data?: unknown, showNotification?: boolean | 'force'): void {
        if (showNotification === 'force') {
          void shown.showErrorMessage(message);
        }
      }

      /** Counts restarts like the real default handler: 4 are allowed, then it gives up. */
      createDefaultErrorHandler(): unknown {
        const restarts: number[] = [];
        return {
          error: () => ({ action: 1 }),
          closed: () => {
            restarts.push(restarts.length);
            return { action: restarts.length <= 4 ? 2 : 1 };
          },
        };
      }

      async start(): Promise<void> {
        client.started += 1;
        if (client.startGate !== undefined) {
          return client.startGate;
        }
        if (client.startError === undefined) {
          return;
        }
        // Like the real client: run the server, and fail when it exits before it initializes.
        const created = client.created.at(-1);
        const server = created?.server;
        if (typeof server === 'function') {
          const child = await (server as () => Promise<ChildProcess>)();
          // Like the real client: pipe the server's stderr into the output channel.
          child.stderr?.on('data', (chunk: string) => {
            (created?.options as { outputChannel: LogChannel }).outputChannel.append(chunk);
          });
          // Do not wait for the exit: the real client rejects as soon as its write fails.
        }
        // The real client shows this on every failed start, with 'force'.
        this.error(
          "cel-lens client: couldn't create connection to server.",
          client.startError,
          'force',
        );
        throw client.startError;
      }

      stop(): Promise<void> {
        client.stopped += 1;
        return Promise.resolve();
      }

      dispose(): Promise<void> {
        client.disposed += 1;
        return client.disposeError === undefined
          ? Promise.resolve()
          : Promise.reject(client.disposeError);
      }
    },
  };
});

async function flushMicrotasks(): Promise<void> {
  for (let turn = 0; turn < 10; turn += 1) {
    await Promise.resolve();
  }
}

function controllerReporting(states: unknown[]): ServerController {
  return new ServerController(window.createOutputChannel('cel-lens'), EXTENSION_PATH, (state) => {
    states.push(state);
  });
}

function newController(onState?: (state: ServerState) => void): ServerController {
  return new ServerController(window.createOutputChannel('cel-lens'), EXTENSION_PATH, onState);
}

const tempDirs: string[] = [];

async function makeTempDir(): Promise<string> {
  const directory = await mkdtemp(join(tmpdir(), 'cel-lens-'));
  tempDirs.push(directory);
  return directory;
}

describe('ServerController', () => {
  beforeEach(() => {
    client.created.length = 0;
    client.started = 0;
    client.stopped = 0;
    client.disposed = 0;
    client.disposeError = undefined;
    client.startError = undefined;
    client.initializeResult = undefined;
    client.notifications.length = 0;
    client.startGate = undefined;
    spawned.calls.length = 0;
    fsFault.chmodError = undefined;
    engine.hung = undefined;
    vi.stubEnv('PATH', '/nowhere');
  });
  afterEach(async () => {
    reset();
    vi.useRealTimers();
    vi.unstubAllEnvs();
    for (const directory of tempDirs.splice(0)) {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it('starts cel-lens-core lsp over stdio for YAML files on disk', async () => {
    settings.set('cel-lens.serverPath', execPath);

    await newController().start();

    expect(client.created).toMatchObject([
      {
        id: 'cel-lens',
        server: expect.any(Function) as unknown,
        options: { documentSelector: [{ scheme: 'file', language: 'yaml' }] },
      },
    ]);
    expect(client.started).toBe(1);
    expect(channels.get('cel-lens')?.lines[0]).toContain(`Starting ${execPath} lsp`);
  });

  it('runs the server through spawnEngine with the resolved command and args', async () => {
    settings.set('cel-lens.serverPath', execPath);
    await newController().start();

    const server = client.created[0]?.server as () => Promise<ChildProcess>;
    const child = await server();
    child.kill();

    expect(spawned.calls).toEqual([{ command: execPath, args: ['lsp'] }]);
  });

  it('starts the binary bundled in the extension folder by default, never one on the PATH', async () => {
    const directory = await makeTempDir();
    await mkdir(join(directory, 'bin'));
    await writeFile(join(directory, 'bin', 'cel-lens-core'), '');
    await chmod(join(directory, 'bin', 'cel-lens-core'), 0o644);
    vi.stubEnv('PATH', directory);

    await new ServerController(window.createOutputChannel('cel-lens'), directory).start();

    expect(client.created).toHaveLength(1);
    expect(channels.get('cel-lens')?.lines[0]).toContain(
      `Starting ${join(directory, 'bin', 'cel-lens-core')} lsp`,
    );
    expect(shownErrors).toEqual([]);
    // Made executable, as a .vsix unpacked without mode bits needs.
    expect((await stat(join(directory, 'bin', 'cel-lens-core'))).mode & 0o100).toBe(0o100);
  });

  it('shows one message with a Show Logs button when the bundled binary is missing, and starts nothing', async () => {
    const controller = newController();

    await controller.start();

    expect(client.created).toEqual([]);
    expect(shownErrors).toHaveLength(1);
    expect(shownErrors[0]?.message).toContain('missing from the extension');
    expect(shownErrors[0]?.buttons).toEqual(['Show Logs']);
    expect(channels.get('cel-lens')?.lines).toHaveLength(1);
  });

  it('reports starting, then failed, when the server binary is missing', async () => {
    const seen: unknown[] = [];

    await controllerReporting(seen).start();

    expect(seen).toEqual([{ kind: 'starting' }, { kind: 'failed' }]);
  });

  it('reports failed, shows one message with Show Logs, and starts nothing, when the bundled binary cannot be made executable', async () => {
    const directory = await makeTempDir();
    await mkdir(join(directory, 'bin'));
    await writeFile(join(directory, 'bin', 'cel-lens-core'), '');
    await chmod(join(directory, 'bin', 'cel-lens-core'), 0o644);
    fsFault.chmodError = new Error('operation not permitted');
    const seen: unknown[] = [];

    await new ServerController(window.createOutputChannel('cel-lens'), directory, (state) => {
      seen.push(state);
    }).start();

    expect(seen).toEqual([{ kind: 'starting' }, { kind: 'failed' }]);
    expect(client.created).toEqual([]);
    expect(shownErrors).toHaveLength(1);
    expect(shownErrors[0]?.message).toContain('operation not permitted');
    expect(shownErrors[0]?.message).toContain('reinstall the extension');
    expect(shownErrors[0]?.buttons).toEqual(['Show Logs']);
  });

  it('shows the logs from the message', async () => {
    errorChoice.next = 'Show Logs';

    await newController().start();

    await flushMicrotasks();
    expect(channels.get('cel-lens')?.shown).toBe(true);
  });

  it('opens the setting from the message', async () => {
    errorChoice.next = 'Open Engine Setting';
    settings.set('cel-lens.serverPath', '/missing/cel-lens-core');

    await newController().start();

    expect(shownErrors[0]?.buttons).toEqual(['Open Engine Setting']);
    await flushMicrotasks();
    expect(executedCommands).toEqual([
      { command: 'workbench.action.openSettings', args: ['cel-lens.serverPath'] },
    ]);
  });

  it('shows one message with the stderr of a server from the setting that exits at start and an Open Engine Setting button, disposes the client, and does not throw', async () => {
    settings.set('cel-lens.serverPath', execPath);
    client.startError = new Error('Server process exited with code 1');
    const seen: unknown[] = [];

    await controllerReporting(seen).start();

    expect(seen).toEqual([{ kind: 'starting' }, { kind: 'failed' }]);
    expect(shownErrors).toHaveLength(1);
    expect(shownErrors[0]?.message).toContain('Server process exited with code 1');
    expect(shownErrors[0]?.message).toContain('MODULE_NOT_FOUND');
    expect(shownErrors[0]?.message).toContain('Fix the cel-lens.serverPath setting');
    expect(shownErrors[0]?.buttons).toEqual(['Open Engine Setting']);
    expect(client.disposed).toBe(1);
    expect(channels.get('cel-lens')?.lines.join('\n')).toContain('Cannot find module');
  });

  it('shows the whole message, exactly, for a cel-lens.serverPath that does not exist', async () => {
    settings.set('cel-lens.serverPath', '/missing/cel-lens-core');

    await newController().start();

    expect(shownErrors[0]?.message).toBe(
      'The cel-lens engine failed to start: the engine file was not found: /missing/cel-lens-core. Fix the cel-lens.serverPath setting, or clear it to use the bundled engine.',
    );
  });

  it('shows the whole message, exactly, for an engine that prints an error and exits at once', async () => {
    const dir = await makeTempDir();
    const engine = join(dir, 'engine');
    await writeFile(engine, '#!/bin/sh\necho "engine: bad configuration" >&2\nexit 3\n', {
      mode: 0o755,
    });
    settings.set('cel-lens.serverPath', engine);
    client.startError = new Error('write EPIPE');

    await newController().start();

    expect(shownErrors[0]?.message).toBe(
      'The cel-lens engine failed to start: "engine: bad configuration". Fix the cel-lens.serverPath setting, or clear it to use the bundled engine.',
    );
    expect(shownErrors[0]?.buttons).toEqual(['Open Engine Setting']);
  });

  it('shows the start failure after END_WAIT_MS when the engine never closes', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    settings.set('cel-lens.serverPath', execPath);
    client.startError = new Error('write EPIPE');
    // A stand-in that never emits close or error: no process is spawned.
    engine.hung = new ChildProcess();
    engine.hung.stderr = new PassThrough();

    const started = newController().start();
    // The binary lookup uses real file I/O; the wait begins once the engine has been spawned.
    while (vi.getTimerCount() === 0) {
      await new Promise<void>((resolve) => {
        setImmediate(resolve);
      });
    }
    await vi.advanceTimersByTimeAsync(END_WAIT_MS - 1);
    expect(shownErrors).toEqual([]);

    await vi.advanceTimersByTimeAsync(1);
    await started;

    expect(shownErrors).toHaveLength(1);
    expect(shownErrors[0]?.message).toContain('The cel-lens engine failed to start');
    expect(client.disposed).toBe(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('logs each line the server writes to stderr once', async () => {
    settings.set('cel-lens.serverPath', execPath);
    client.startError = new Error('Server process exited with code 1');

    await newController().start();

    const logged = channels.get('cel-lens')?.lines.join('\n') ?? '';
    expect(logged.split('Cannot find module')).toHaveLength(2);
    expect(logged).toContain('The cel-lens engine failed to start');
    expect(shownErrors[0]?.message).toContain('MODULE_NOT_FOUND');
  });

  it('leads with the last line a stand-in engine printed, without stream errors', async () => {
    const dir = await makeTempDir();
    const engine = join(dir, 'engine');
    await writeFile(engine, '#!/bin/sh\necho "engine: bad configuration" >&2\nexit 3\n', {
      mode: 0o755,
    });
    settings.set('cel-lens.serverPath', engine);
    client.startError = new Error('write EPIPE');

    await newController().start();

    const message = shownErrors[0]?.message ?? '';
    expect(message).toContain('"engine: bad configuration"');
    expect(message).not.toMatch(/EPIPE|stream was destroyed/);
    expect(shownErrors[0]?.buttons).toEqual(['Open Engine Setting']);
  });

  it('names the path of an engine file without run permission in plain words', async () => {
    const notExecutable = join(await makeTempDir(), 'engine');
    await writeFile(notExecutable, 'text', { mode: 0o644 });
    settings.set('cel-lens.serverPath', notExecutable);
    client.startError = new Error('Cannot call write after a stream was destroyed');

    await newController().start();

    const message = shownErrors[0]?.message ?? '';
    expect(
      message.startsWith(
        `The cel-lens engine failed to start: the engine file can't be run (permission denied): ${notExecutable}. `,
      ),
    ).toBe(true);
    expect(message).not.toContain('stream was destroyed');
    expect(shownErrors[0]?.buttons).toEqual(['Open Engine Setting']);
  });

  it('logs a spawn error once and quotes it in the message', async () => {
    const notExecutable = join(await makeTempDir(), 'engine');
    await writeFile(notExecutable, 'text', { mode: 0o644 });
    settings.set('cel-lens.serverPath', notExecutable);
    client.startError = new Error('Server process exited with code 1');

    await newController().start();

    const logged = channels.get('cel-lens')?.lines.join('\n') ?? '';
    expect(logged.split('EACCES')).toHaveLength(2);
    expect(shownErrors[0]?.message).toContain('permission denied');
  });

  it.each([
    { started: false, action: 1, handled: true },
    { started: true, action: 2, handled: undefined },
  ])(
    'answers the server closing with started=$started with action $action',
    async ({ started, action, handled }) => {
      settings.set('cel-lens.serverPath', execPath);
      let release = (): void => undefined;
      if (!started) {
        client.startGate = new Promise<void>((resolve) => {
          release = resolve;
        });
      }
      const starting = newController().start();
      await vi.waitFor(() => {
        expect(client.started).toBe(1);
      });
      const options = client.created[0]?.options as {
        errorHandler: { closed: () => { action: number; handled?: boolean } };
      };

      const result = options.errorHandler.closed();

      expect(result.action).toBe(action);
      expect(result.handled).toBe(handled);
      release();
      await starting;
    },
  );

  it("creates the client with the extension's own error handler, which says engine when it gives up", async () => {
    settings.set('cel-lens.serverPath', execPath);
    await newController().start();
    const options = client.created[0]?.options as {
      errorHandler: { closed: () => { action: number } };
    };

    const actions = Array.from({ length: 6 }, () => options.errorHandler.closed().action);

    expect(actions.slice(0, 4)).toEqual([2, 2, 2, 2]);
    expect(actions[4]).toBe(1);
    expect(shownErrors.map((shown) => shown.message)).toEqual([
      expect.stringContaining('cel-lens engine crashed 5 times'),
      expect.stringContaining('cel-lens engine crashed 5 times'),
    ]);
  });

  it('reports a crashed state once the client gives up restarting', async () => {
    settings.set('cel-lens.serverPath', execPath);
    const states: string[] = [];
    await newController((state) => states.push(state.kind)).start();
    const options = client.created[0]?.options as {
      errorHandler: { closed: () => { action: number } };
    };

    for (let crash = 0; crash < 5; crash += 1) {
      options.errorHandler.closed();
    }

    expect(states.filter((kind) => kind === 'crashed')).toHaveLength(1);
  });

  it('reports a crashed state once, when message errors make the handler shut the connection down', async () => {
    settings.set('cel-lens.serverPath', execPath);
    const states: string[] = [];
    await newController((state) => states.push(state.kind)).start();
    const options = client.created[0]?.options as {
      errorHandler: {
        error: (error: Error, message: unknown, count: number) => { action: number };
      };
    };

    const actions = [1, 2, 3, 4, 5].map(
      (count) => options.errorHandler.error(new Error('bad'), undefined, count).action,
    );

    expect(actions).toEqual([1, 1, 1, 2, 2]);
    expect(states.filter((kind) => kind === 'crashed')).toHaveLength(1);
    expect(states.at(-1)).toBe('crashed');
  });

  it('still resolves, on start and on restart, when disposing the failed client rejects like the real one', async () => {
    settings.set('cel-lens.serverPath', execPath);
    client.startError = new Error('spawn failed');
    client.disposeError = new Error("Client is not running and can't be stopped");
    const controller = newController();

    await expect(controller.start()).resolves.toBeUndefined();
    await expect(controller.restart()).resolves.toBeUndefined();

    expect(client.started).toBe(2);
    expect(shownErrors).toHaveLength(2);
    // Spawns a real process twice, which a busy machine can take longer than 5 s to do.
  }, 30_000);

  it('restarts while the missing-binary message is still open', async () => {
    errorChoice.never = true;
    const controller = newController();
    await controller.start();
    settings.set('cel-lens.serverPath', execPath);

    await controller.restart();

    expect(shownErrors).toHaveLength(1);
    expect(client.started).toBe(1);
  });

  it('restarts by stopping the running server and starting a new one', async () => {
    settings.set('cel-lens.serverPath', execPath);
    const controller = newController();
    await controller.start();

    await controller.restart();

    expect(client.stopped).toBe(1);
    expect(client.created).toHaveLength(2);
    expect(client.started).toBe(2);
  });

  it('runs overlapping restarts one after the other', async () => {
    settings.set('cel-lens.serverPath', execPath);
    const controller = newController();
    await controller.start();

    await Promise.all([controller.restart(), controller.restart()]);

    expect(client.created).toHaveLength(3);
    expect(client.stopped).toBe(2);
  });

  it('does not start a second server while one runs', async () => {
    settings.set('cel-lens.serverPath', execPath);
    const controller = newController();

    await controller.start();
    await controller.start();

    expect(client.created).toHaveLength(1);
  });

  it('stops the server when disposed', async () => {
    settings.set('cel-lens.serverPath', execPath);
    const controller = newController();
    await controller.start();

    controller.dispose();
    await controller.stop();

    expect(client.stopped).toBe(1);
  });

  it.each([
    ['', { kubernetesVersion: '' }],
    ['1.35', { kubernetesVersion: '1.35' }],
  ])('passes the setting %j in initializationOptions as %j', async (setting, options) => {
    settings.set('cel-lens.serverPath', execPath);
    settings.set('cel-lens.kubernetesVersion', setting);

    await newController().start();

    expect(client.created).toMatchObject([{ options: { initializationOptions: options } }]);
  });

  it('hands the versions in the initialize result to the listener, and clears them on stop', async () => {
    settings.set('cel-lens.serverPath', execPath);
    client.initializeResult = {
      capabilities: {
        experimental: {
          celLens: { kubernetesVersion: '1.37', supportedKubernetesVersions: ['1.37'] },
        },
      },
    };
    const seen: unknown[] = [];
    const controller = new ServerController(
      window.createOutputChannel('cel-lens'),
      EXTENSION_PATH,
      (state) => {
        seen.push(state);
      },
    );

    await controller.start();
    await controller.stop();

    expect(seen).toEqual([
      { kind: 'starting' },
      { kind: 'running', info: { inUse: '1.37', defaultVersion: '1.37', supported: ['1.37'] } },
      { kind: 'stopped' },
    ]);
  });

  it('logs that the server reports no versions, when an older server does not', async () => {
    settings.set('cel-lens.serverPath', execPath);
    const log = window.createOutputChannel('cel-lens-old');

    await new ServerController(log, EXTENSION_PATH).start();

    const text = log.lines.join('\n');
    expect(text).toContain('reports no Kubernetes versions');
    expect(text).toContain('https://github.com/JairJosafath/cel-lens/releases');
    expect(text).toContain('core-vX.Y.Z');
  });

  it('sends the changed setting with didChangeConfiguration and does not restart', async () => {
    settings.set('cel-lens.serverPath', execPath);
    const controller = newController();
    await controller.start();
    settings.set('cel-lens.kubernetesVersion', '1.36');

    await controller.sendVersionSetting();

    expect(client.notifications).toEqual([
      {
        method: 'workspace/didChangeConfiguration',
        params: { settings: { kubernetesVersion: '1.36' } },
      },
    ]);
    expect(client.created).toHaveLength(1);
    expect(client.stopped).toBe(0);
  });

  it.each([
    ['1.99', { kubernetesVersion: '' }],
    ['1.36', { kubernetesVersion: '1.36' }],
  ])(
    'with setting %j, sends %j after the server reports its versions',
    async (setting, options) => {
      settings.set('cel-lens.serverPath', execPath);
      settings.set('cel-lens.kubernetesVersion', setting);
      client.initializeResult = {
        capabilities: {
          experimental: {
            celLens: { kubernetesVersion: '1.37', supportedKubernetesVersions: ['1.37', '1.36'] },
          },
        },
      };
      const controller = newController();

      await controller.start();
      await controller.sendVersionSetting();
      await controller.restart();

      expect(client.created.map((c) => c.options)).toMatchObject([
        { initializationOptions: { kubernetesVersion: setting } },
        { initializationOptions: options },
      ]);
      expect(client.notifications.at(-1)).toEqual({
        method: 'workspace/didChangeConfiguration',
        params: { settings: options },
      });
    },
  );

  it.each([
    [
      '1.99',
      [
        {
          method: 'workspace/didChangeConfiguration',
          params: { settings: { kubernetesVersion: '' } },
        },
      ],
    ],
    ['1.36', []],
  ])('on start with setting %j, the notifications are %j', async (setting, notifications) => {
    settings.set('cel-lens.serverPath', execPath);
    settings.set('cel-lens.kubernetesVersion', setting);
    client.initializeResult = {
      capabilities: {
        experimental: {
          celLens: { kubernetesVersion: '1.37', supportedKubernetesVersions: ['1.37', '1.36'] },
        },
      },
    };

    await newController().start();

    expect(client.notifications).toEqual(notifications);
  });

  it('sends a version change made while the server is still starting, once it is up', async () => {
    settings.set('cel-lens.serverPath', execPath);
    let finishStart: () => void = () => undefined;
    client.startGate = new Promise<void>((resolve) => {
      finishStart = resolve;
    });
    const controller = newController();
    const started = controller.start();
    await flushMicrotasks();
    settings.set('cel-lens.kubernetesVersion', '1.36');

    const sent = controller.sendVersionSetting();
    await flushMicrotasks();
    expect(client.notifications).toEqual([]);
    finishStart();
    await Promise.all([started, sent]);

    expect(client.notifications).toEqual([
      {
        method: 'workspace/didChangeConfiguration',
        params: { settings: { kubernetesVersion: '1.36' } },
      },
    ]);
  });

  it('sends nothing when no server runs', async () => {
    await newController().sendVersionSetting();

    expect(client.notifications).toEqual([]);
  });

  describe('with a server that reports only the version in use', () => {
    function reportInUse(version: string): void {
      client.initializeResult = { kubernetesVersion: version };
    }

    it.each([
      ['1.34', ''],
      ['', '1.34'],
    ])('restarts and sends %j when the setting changes from %j', async (next, before) => {
      settings.set('cel-lens.serverPath', execPath);
      settings.set('cel-lens.kubernetesVersion', before);
      reportInUse('1.37');
      const controller = newController();
      await controller.start();
      settings.set('cel-lens.kubernetesVersion', next);

      await controller.sendVersionSetting();

      expect(client.created.map((c) => c.options)).toMatchObject([
        { initializationOptions: { kubernetesVersion: before } },
        { initializationOptions: { kubernetesVersion: next } },
      ]);
      expect(client.notifications).toEqual([]);
    });

    it('does not restart when the setting did not change', async () => {
      settings.set('cel-lens.serverPath', execPath);
      reportInUse('1.37');
      const controller = newController();
      await controller.start();

      await controller.sendVersionSetting();

      expect(client.created).toHaveLength(1);
    });

    it('hands the version in use, and no default, to the listener after a pinned start', async () => {
      settings.set('cel-lens.serverPath', execPath);
      settings.set('cel-lens.kubernetesVersion', '1.34');
      reportInUse('1.34');
      const seen: unknown[] = [];

      await new ServerController(
        window.createOutputChannel('cel-lens'),
        EXTENSION_PATH,
        (state) => {
          seen.push(state);
        },
      ).start();

      expect(seen).toEqual([
        { kind: 'starting' },
        { kind: 'running', info: { inUse: '1.34', defaultVersion: undefined, supported: [] } },
      ]);
    });
  });
});
