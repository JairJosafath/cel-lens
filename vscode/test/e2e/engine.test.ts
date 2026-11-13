// Starts the real cel-lens-core in bin/ (put there by `npm run engine`; no network here) with the command and arguments the
// extension uses, and talks to it as a language client over stdio. No VS Code, no display, no
// stand-in. A test that gets no answer fails when vitest's time limit passes
// (vitest.e2e.config.mts); an engine that exits fails it at once, with its error output.
import type { ChildProcess } from 'node:child_process';
import { copyFile, mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { platform, tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import {
  DidChangeConfigurationNotification,
  DidChangeTextDocumentNotification,
  DidOpenTextDocumentNotification,
  ExitNotification,
  InitializeRequest,
  InitializedNotification,
  DidChangeWatchedFilesNotification,
  FileChangeType,
  PublishDiagnosticsNotification,
  RegistrationRequest,
  ShutdownRequest,
  StreamMessageReader,
  StreamMessageWriter,
  createProtocolConnection,
  type CompletionItem,
  type CompletionList,
  type Diagnostic,
  type CancellationToken,
  type CodeLens,
  type Registration,
  type Hover,
  type WorkspaceEdit,
  type ProtocolConnection,
} from 'vscode-languageserver-protocol/node';

import {
  parseExperimentalFeatures,
  parseVersionInfo,
  serverOptions,
} from '../../src/kubernetes_version';
import {
  readmeDefaultKubernetesVersion,
  readmeKubernetesVersions,
  readmeSection,
} from '../../src/readme';
import { featureNameProblems, experimentalRows } from '../../src/experimental_table';
import { experimentalSettingEnum } from '../../src/manifest_setting';
import { ExampleResultsFlow, type ResultsUi } from '../../src/example_results_flow';
import { requireEngineFile } from '../../src/engine_file';
import { isRecord } from '../../src/is_record';
import { resolveServer } from '../../src/server_path';
import { spawnEngine } from '../../src/spawn_engine';

/**
 * An engine to start in place of the one in bin/, as the extension's `cel-lens.serverPath` names
 * one: to try an engine build before it is pinned.
 */
const ENGINE_PATH = process.env['CEL_LENS_SERVER_PATH'] ?? '';
const FIXTURES = fileURLToPath(new URL('fixtures/', import.meta.url));
const EXTENSION_PATH = fileURLToPath(new URL('../../', import.meta.url));
const ERROR = 1;
const WARNING = 2;
/** The watch kinds Create and Delete of LSP 3.17 together (Create 1, Change 2, Delete 4). */
const CREATE_DELETE_WATCH_KIND = 5;
const DOCS_LINK =
  /^https:\/\/(kubernetes\.io\/docs|github\.com\/google\/cel-spec|pkg\.go\.dev\/github\.com\/google\/cel-go)([/#]|$)/;

interface Engine {
  readonly process: ChildProcess;
  readonly connection: ProtocolConnection;
  /** Rejects when the engine exits. */
  readonly exited: Promise<never>;
  /** The diagnostics last published per document URI. */
  readonly published: Map<string, Diagnostic[]>;
  /** The versions the engine says it supports, from its `initialize` result. */
  readonly supportedVersions: readonly string[];
  /** The version the engine uses when none is asked for, from its `initialize` result. */
  readonly defaultVersion: string | undefined;
  /** The experimental feature names the engine knows, from its `initialize` result. */
  readonly experimentalFeatures: readonly string[];
  /** The registrations the engine asked the client for with `client/registerCapability`. */
  readonly registrations: Registration[];
  /** The commands the engine lists in `executeCommandProvider`, from its `initialize` result. */
  readonly commands: readonly string[];
  /** How many times the engine asked the client to ask again for the lines above the rules. */
  readonly refreshes: { count: number };
  /** Resolves on the next publishDiagnostics. */
  nextPublish: () => Promise<void>;
  /** Resolves once the engine has asked for the lines again after `seen` refreshes. */
  refreshedAfter: (seen: number) => Promise<void>;
}

let engine: Engine | undefined;

function running(): Engine {
  if (engine === undefined) {
    throw new Error('The engine did not start.');
  }
  return engine;
}

/** The folder the extension is installed in: the repository, whose bin/ holds the engine. */
async function checkedExtensionPath(): Promise<string> {
  await requireEngineFile(EXTENSION_PATH, (path) =>
    stat(path).then(
      (found) => found.isFile(),
      () => false,
    ),
  );
  return EXTENSION_PATH;
}

interface SessionOptions {
  /** A workspace folder on disk, scanned by the engine. */
  readonly folder?: string;
  /** Tells the engine the client registers file watchers. */
  readonly dynamicRegistration?: boolean;
  readonly experimental?: readonly string[];
  /** Whether the engine draws the line above each rule: the extension's `cel-lens.inlineInfo`. */
  readonly inlineInfo?: boolean;
  /** Tells the engine the client applies the edits that write `.cellens`, and applies them to disk. */
  readonly writesCellens?: boolean;
  /** Whether the engine shows what each rule does to its examples: the extension's `cel-lens.exampleResults`. */
  readonly exampleResults?: boolean;
}

/** The offset in `text` of an LSP position (characters count UTF-16 code units, as JavaScript's do). */
function offsetOf(text: string, position: { line: number; character: number }): number {
  let offset = 0;
  for (let line = 0; line < position.line; line += 1) {
    offset = text.indexOf('\n', offset) + 1;
  }
  return offset + position.character;
}

/**
 * Applies what the engine asks of the editor to write `.cellens`: create the file, and change
 * its text. A test never changes two ranges that touch, so applying them last to first is enough.
 */
async function applyEditToDisk(edit: WorkspaceEdit): Promise<void> {
  for (const change of edit.documentChanges ?? []) {
    if ('kind' in change) {
      if (change.kind !== 'create') {
        throw new Error(`The engine asked for a ${change.kind}, which the test does not apply.`);
      }
      await writeFile(fileURLToPath(change.uri), '');
      continue;
    }
    const path = fileURLToPath(change.textDocument.uri);
    let text = await readFile(path, 'utf8');
    const edits = change.edits.flatMap((each) => ('newText' in each ? [each] : []));
    for (const { range, newText } of edits.sort(
      (a, b) => offsetOf(text, b.range.start) - offsetOf(text, a.range.start),
    )) {
      text =
        text.slice(0, offsetOf(text, range.start)) +
        newText +
        text.slice(offsetOf(text, range.end));
    }
    await writeFile(path, text);
  }
}

/** Starts the engine as the extension does and initializes a session. */
async function startEngine(extensionPath: string, options: SessionOptions = {}): Promise<Engine> {
  const server = await resolveServer({
    setting: ENGINE_PATH,
    extensionPath,
    platform: platform(),
    isFile: () => Promise.resolve(true),
  });
  if (!server.found) {
    throw new Error(server.message);
  }
  const child = spawnEngine(server.command, server.args);
  let stderr = '';
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (chunk: string) => {
    stderr += chunk;
  });
  const exited = new Promise<never>((_resolve, reject) => {
    child.on('exit', (code) => {
      reject(new Error(`The engine exited with code ${String(code)}: ${stderr}`));
    });
    child.on('error', reject);
  });
  exited.catch(() => undefined);

  const connection = createProtocolConnection(
    new StreamMessageReader(child.stdout),
    new StreamMessageWriter(child.stdin),
  );
  const published = new Map<string, Diagnostic[]>();
  const waiting: (() => void)[] = [];
  connection.onNotification(PublishDiagnosticsNotification.type, (params) => {
    published.set(params.uri, params.diagnostics);
    for (const wake of waiting.splice(0)) {
      wake();
    }
  });
  const refreshes = { count: 0 };
  const refreshWaiting: (() => void)[] = [];
  connection.onRequest('workspace/codeLens/refresh', () => {
    refreshes.count += 1;
    for (const wake of refreshWaiting.splice(0)) {
      wake();
    }
    return null;
  });
  const registrations: Registration[] = [];
  connection.onRequest(RegistrationRequest.type, (params) => {
    registrations.push(...params.registrations);
  });
  if (options.writesCellens) {
    connection.onRequest('workspace/applyEdit', async (params: { edit: WorkspaceEdit }) => {
      await applyEditToDisk(params.edit);
      return { applied: true };
    });
  }
  connection.listen();
  const initialized = await Promise.race([
    connection.sendRequest(InitializeRequest.type, {
      processId: process.pid,
      // eslint-disable-next-line @typescript-eslint/no-deprecated -- LSP still requires rootUri, deprecated as it is: the type has no way to leave it out, and the extension's client sends it too
      rootUri: null,
      workspaceFolders:
        options.folder === undefined
          ? null
          : [{ uri: pathToFileURL(options.folder).href, name: 'workspace' }],
      capabilities: {
        workspace: {
          codeLens: { refreshSupport: true },
          ...(options.dynamicRegistration && {
            didChangeWatchedFiles: { dynamicRegistration: true },
          }),
          ...(options.writesCellens && {
            applyEdit: true,
            workspaceEdit: { documentChanges: true, resourceOperations: ['create'] },
          }),
        },
      },
      initializationOptions: {
        ...serverOptions('', undefined, options.experimental),
        inlineInfo: options.inlineInfo ?? true,
        exampleResults: options.exampleResults ?? true,
      },
    }),
    exited,
  ]);
  await connection.sendNotification(InitializedNotification.type, {});
  return {
    process: child,
    connection,
    exited,
    supportedVersions: parseVersionInfo(initialized, '')?.supported ?? [],
    defaultVersion: parseVersionInfo(initialized, '')?.defaultVersion,
    experimentalFeatures: parseExperimentalFeatures(initialized),
    published,
    registrations,
    commands: listedCommands(initialized),
    refreshes,
    nextPublish: () =>
      new Promise<void>((resolve) => {
        waiting.push(resolve);
      }),
    refreshedAfter: (seen) =>
      refreshes.count > seen
        ? Promise.resolve()
        : new Promise<void>((resolve) => {
            refreshWaiting.push(resolve);
          }),
  };
}

/** The commands the engine lists in its `initialize` result, for the client to run with `workspace/executeCommand`. */
function listedCommands(initialized: unknown): string[] {
  const capabilities = isRecord(initialized) ? initialized['capabilities'] : undefined;
  const provider = isRecord(capabilities) ? capabilities['executeCommandProvider'] : undefined;
  const commands = isRecord(provider) ? provider['commands'] : undefined;
  return Array.isArray(commands)
    ? commands.filter((command): command is string => typeof command === 'string')
    : [];
}

/** Waits until the diagnostics for `uri` satisfy `accept`, or the engine exits. */
async function diagnosticsFor(
  uri: string,
  accept: (found: Diagnostic[]) => boolean,
): Promise<Diagnostic[]> {
  const { published, nextPublish, exited } = running();
  for (;;) {
    const found = published.get(uri);
    if (found !== undefined && accept(found)) {
      return found;
    }
    await Promise.race([nextPublish(), exited]);
  }
}

function lineOf(text: string, needle: string): number {
  return text.split('\n').findIndex((line) => line.includes(needle));
}

/** Opens a fixture as a YAML document; a `.txt` fixture is YAML that Prettier cannot parse. */
async function open(name: string): Promise<{ uri: string; text: string }> {
  const text = await readFile(join(FIXTURES, name), 'utf8');
  const uri = `file:///workspace/${name.replace(/\.txt$/, '.yaml')}`;
  await running().connection.sendNotification(DidOpenTextDocumentNotification.type, {
    textDocument: { uri, languageId: 'yaml', version: 1, text },
  });
  return { uri, text };
}

/** The lines above the rules of an open document, as VS Code shows them: the title and the command of each. */
async function lensesOf(uri: string): Promise<{ title: string; command: string }[]> {
  const found = await running().connection.sendRequest<CodeLens[] | null>('textDocument/codeLens', {
    textDocument: { uri },
  });
  return (found ?? []).flatMap((lens) =>
    lens.command === undefined
      ? []
      : [{ title: lens.command.title, command: lens.command.command }],
  );
}

/** What the engine answers `cel-lens.<name>` with, for the argument of a line above a rule. */
async function runCommand(command: string, argument: unknown): Promise<unknown> {
  return running().connection.sendRequest('workspace/executeCommand', {
    command,
    arguments: [argument],
  });
}

/** The argument of the part of the line above a rule that runs `command`, such as `cel-lens.giveType`. */
async function lensArgument(uri: string, command: string): Promise<Record<string, unknown>> {
  const found = await running().connection.sendRequest<CodeLens[] | null>('textDocument/codeLens', {
    textDocument: { uri },
  });
  const argument: unknown = found?.find((lens) => lens.command?.command === command)?.command
    ?.arguments?.[0];
  if (typeof argument !== 'object' || argument === null) {
    throw new Error(`No line above a rule has a part that runs ${command}.`);
  }
  return { ...argument };
}

/** Asks for the lines above the rules of an open document until `accept` takes them, as VS Code does when the engine asks it to. */
async function lensesWhen(
  uri: string,
  accept: (lenses: { title: string; command: string }[]) => boolean,
): Promise<{ title: string; command: string }[]> {
  const { refreshes, refreshedAfter, exited } = running();
  for (;;) {
    const seen = refreshes.count;
    const found = await lensesOf(uri);
    if (accept(found)) {
      return found;
    }
    await Promise.race([refreshedAfter(seen), exited]);
  }
}

/** The lines of a picker the flow showed, once the engine answered. */
interface PickedLines {
  readonly title: string;
  readonly lines: { label: string; description?: string; detail?: string }[];
}

/** A picker that records its lines and picks the one labelled `choice`. */
function pickerOf(
  picked: PickedLines[],
  opened: { uri: string; line: number }[],
  choice: string,
): ResultsUi {
  return {
    async pick(request) {
      const lines = request.lines instanceof Promise ? await request.lines : request.lines;
      picked.push({
        title: request.title,
        lines: lines.map((line) => ({
          label: line.label,
          ...(line.separator !== true &&
            line.description !== undefined && {
              description: line.description,
            }),
          ...(line.separator !== true && line.detail !== undefined && { detail: line.detail }),
        })),
      });
      const line = lines.find((each) => each.separator !== true && each.label === choice);
      return line === undefined || line.separator === true
        ? undefined
        : { value: line.value, typed: '' };
    },
    isOn: () => true,
    offerTurnOn: () => Promise.reject(new Error('the results are on')),
    openExample(uri, line) {
      opened.push({ uri, line });
      return Promise.resolve();
    },
    showError: (message) => Promise.reject(new Error(message)),
    showLogs: fail,
  };
}

/** A cancellation token that fires when `signal` does, for a request of the protocol connection. */
function cancellationOf(signal: AbortSignal): CancellationToken {
  return {
    get isCancellationRequested() {
      return signal.aborted;
    },
    onCancellationRequested: (listener) => {
      signal.addEventListener(
        'abort',
        () => {
          listener(undefined);
        },
        { once: true },
      );
      return { dispose: () => undefined };
    },
  };
}

/** Fails the test: the flow logged a problem, which it does only for an engine it does not understand. */
function fail(message?: string): never {
  throw new Error(message ?? 'the flow showed something it should not');
}

beforeAll(async () => {
  engine = await startEngine(await checkedExtensionPath());
});

async function stopEngine(stopping: Engine): Promise<void> {
  try {
    await Promise.race([stopping.connection.sendRequest(ShutdownRequest.type), stopping.exited]);
    await stopping.connection.sendNotification(ExitNotification.type);
  } catch {
    // The engine already exited; the kill below is then a no-op.
  }
  stopping.connection.dispose();
  stopping.process.kill();
}

afterAll(async () => {
  if (engine !== undefined) {
    await stopEngine(engine);
  }
});

describe('the bundled cel-lens-core, started with the extension’s command', () => {
  it('supports the Kubernetes versions the README lists', async () => {
    const readme = await readFile(`${EXTENSION_PATH}README.md`, 'utf8');
    const listed = readmeKubernetesVersions(
      readmeSection(readme, 'Supported Kubernetes versions') ?? '',
    );

    expect(running().supportedVersions.length).toBeGreaterThan(0);
    expect([...listed].sort()).toEqual([...running().supportedVersions].sort());
  });

  it('lists in package.json and the README the experimental feature names the engine knows', async () => {
    const inManifest = experimentalSettingEnum(
      JSON.parse(await readFile(`${EXTENSION_PATH}package.json`, 'utf8')),
    );
    const readme = await readFile(`${EXTENSION_PATH}README.md`, 'utf8');
    const known = running().experimentalFeatures;

    expect(known.length).toBeGreaterThan(0);
    expect([
      ...featureNameProblems('package.json', inManifest, known),
      ...featureNameProblems('README.md', [...experimentalRows(readme).keys()], known),
    ]).toEqual([]);
  });

  it('states the engine’s default Kubernetes version in the README', async () => {
    const readme = await readFile(`${EXTENSION_PATH}README.md`, 'utf8');
    const stated = readmeDefaultKubernetesVersion(
      readmeSection(readme, 'Supported Kubernetes versions') ?? '',
    );

    expect(running().defaultVersion).toBeDefined();
    expect(stated).toBe(running().defaultVersion);
  });

  it('draws the line above each rule: what self is, and that the rule compiles', async () => {
    const { uri } = await open('valid_crd.yaml');
    await diagnosticsFor(uri, () => true);

    expect(await lensesOf(uri)).toEqual([
      { title: 'self: Widget.spec · compiles', command: 'cel-lens.showRule' },
    ]);
  });

  it('draws, above a rule that reads a value of a custom paramKind, a Give it a type for the dyn value', async () => {
    const { uri } = await open('replica_policy.yaml');
    await diagnosticsFor(uri, () => true);

    expect(await lensesOf(uri)).toEqual([
      { title: 'object: apps/v1 Deployment · compiles', command: 'cel-lens.showRule' },
      { title: 'params: dyn · Give it a type', command: 'cel-lens.giveType' },
    ]);
  });

  it('draws no line above a rule while inlineInfo is off, and draws them again when it is on', async () => {
    const quiet = await startEngine(await checkedExtensionPath(), { inlineInfo: false });
    const shared = engine;
    engine = quiet;
    try {
      const { uri } = await open('replica_policy.yaml');
      await diagnosticsFor(uri, () => true);
      expect(await lensesOf(uri)).toEqual([]);

      await quiet.connection.sendNotification(DidChangeConfigurationNotification.type, {
        settings: { ...serverOptions('', undefined, []), inlineInfo: true },
      });

      expect(await lensesOf(uri)).toHaveLength(2);
    } finally {
      await stopEngine(quiet);
      engine = shared;
    }
  });

  it('shows a cel-lens error on a broken x-kubernetes-validations rule', async () => {
    const { uri, text } = await open('broken_crd.yaml');

    const found = await diagnosticsFor(uri, (list) => list.length > 0);

    expect(found).toMatchObject([{ source: 'cel-lens', severity: ERROR }]);
    expect(found[0]?.range.start.line).toBe(lineOf(text, '- rule:'));
  });

  it('answers hover and completion on a valid rule', async () => {
    const { uri, text } = await open('valid_crd.yaml');
    const line = lineOf(text, '- rule:');
    const character = (text.split('\n')[line] ?? '').indexOf('replicas');
    const { connection } = running();

    const hover = await connection.sendRequest<Hover | null>('textDocument/hover', {
      textDocument: { uri },
      position: { line, character: character + 2 },
    });
    const completion = await connection.sendRequest<CompletionList | CompletionItem[] | null>(
      'textDocument/completion',
      { textDocument: { uri }, position: { line, character } },
    );

    expect(JSON.stringify(hover?.contents)).toContain('replicas');
    const items = Array.isArray(completion) ? completion : (completion?.items ?? []);
    expect(items.map((item) => item.label)).toContain('replicas');
  });

  it('answers hover on a CEL function with a link to its docs', async () => {
    const { uri, text } = await open('function_crd.yaml');
    const line = lineOf(text, '- rule:');
    const character = (text.split('\n')[line] ?? '').indexOf('size');

    const hover = await running().connection.sendRequest<Hover | null>('textDocument/hover', {
      textDocument: { uri },
      position: { line, character: character + 1 },
    });

    expect(hover).not.toBeNull();
    const links = JSON.stringify(hover?.contents).match(/https:\/\/[^\s"')\]]+/g) ?? [];
    expect(links).not.toHaveLength(0);
    for (const link of links) {
      expect(link).toMatch(DOCS_LINK);
    }
  });

  it('shows a Kyverno diagnostic only when kyverno is in the experimental list', async () => {
    const { uri, text } = await open('kyverno_policy.yaml');
    const { connection } = running();

    expect(await diagnosticsFor(uri, () => true)).toEqual([]);

    await connection.sendNotification(DidChangeConfigurationNotification.type, {
      settings: { ...serverOptions(''), experimental: ['kyverno'] },
    });
    const found = await diagnosticsFor(uri, (list) => list.length > 0);

    expect(found).toMatchObject([{ source: 'cel-lens', severity: ERROR }]);
    expect(found[0]?.range.start.line).toBe(lineOf(text, '- expression:'));

    await connection.sendNotification(DidChangeConfigurationNotification.type, {
      settings: { ...serverOptions(''), experimental: [] },
    });

    expect(await diagnosticsFor(uri, (list) => list.length === 0)).toEqual([]);
  });

  it('shows one warning per matched kind, starting with the kind, on a policy matching two kinds', async () => {
    const { uri, text } = await open('typecheck_policy.yaml');

    const found = await diagnosticsFor(uri, (list) => list.length > 0);

    const warnings = found.filter((d) => d.severity === WARNING);
    expect(warnings).toHaveLength(2);
    expect(warnings[0]?.message).toMatch(/^apps\/v1 Deployment: /);
    expect(warnings[1]?.message).toMatch(/^apps\/v1 StatefulSet: /);
    for (const warning of warnings) {
      expect(warning).toMatchObject({ source: 'cel-lens' });
      expect(warning.range.start.line).toBe(lineOf(text, '- expression:'));
    }
  });

  it('types object from each matched kind: a Deployment-only field warns only for the StatefulSet', async () => {
    const { uri, text } = await open('per_kind_object_policy.yaml');

    const found = await diagnosticsFor(uri, (list) => list.length > 0);

    const warnings = found.filter((d) => d.severity === WARNING);
    expect(warnings).toHaveLength(1);
    expect(warnings[0]?.message).toMatch(/^apps\/v1 StatefulSet: /);
    expect(warnings[0]).toMatchObject({ source: 'cel-lens' });
    expect(warnings[0]?.range.start.line).toBe(lineOf(text, '- expression:'));
    expect(found.map((d) => d.message)).not.toContainEqual(
      expect.stringMatching(/^apps\/v1 Deployment: /),
    );
  });

  it('reports broken YAML where it breaks and clears the error once the text is fixed', async () => {
    const { uri, text } = await open('broken_yaml.txt');

    const found = await diagnosticsFor(uri, (list) => list.length > 0);

    expect(found.some((d) => d.source === 'cel-lens' && d.severity === ERROR)).toBe(true);
    expect(found.map((d) => d.range.start.line)).toContain(lineOf(text, 'names:'));

    await running().connection.sendNotification(DidChangeTextDocumentNotification.type, {
      textDocument: { uri, version: 2 },
      contentChanges: [{ text: text.replace('Widget\n', 'Widget}\n') }],
    });

    expect(await diagnosticsFor(uri, (list) => list.length === 0)).toEqual([]);
  });
});

describe('the bundled cel-lens-core, with a workspace folder on disk', () => {
  it('forgets the CRDs below a deleted folder, through the create-and-delete ** watcher', async () => {
    const root = await mkdtemp(join(tmpdir(), 'cel-lens-e2e-'));
    const crds = join(root, 'crds');
    await mkdir(crds);
    await copyFile(join(FIXTURES, 'widget_crd.yaml'), join(crds, 'widget_crd.yaml'));
    const session = await startEngine(await checkedExtensionPath(), {
      folder: root,
      dynamicRegistration: true,
      experimental: ['crd-typed-policies'],
    });
    const shared = engine;
    engine = session;
    try {
      const { uri, text } = await open('widget_policy.yaml');

      const warned = await diagnosticsFor(uri, (list) => list.length > 0);

      expect(warned).toMatchObject([{ source: 'cel-lens', severity: WARNING }]);
      expect(warned[0]?.range.start.line).toBe(lineOf(text, '- expression:'));
      const watched = session.registrations.find(
        (registration) => registration.method === 'workspace/didChangeWatchedFiles',
      );
      expect(watched?.registerOptions).toMatchObject({
        watchers: [
          { globPattern: '**/*.{yaml,yml}' },
          { globPattern: '**/.cellens' },
          { globPattern: '**/*.go' },
          { globPattern: '**', kind: CREATE_DELETE_WATCH_KIND },
        ],
      });

      await rm(crds, { recursive: true });
      await session.connection.sendNotification(DidChangeWatchedFilesNotification.type, {
        changes: [{ uri: pathToFileURL(crds).href, type: FileChangeType.Deleted }],
      });

      expect(await diagnosticsFor(uri, (list) => list.length === 0)).toEqual([]);
    } finally {
      await stopEngine(session);
      engine = shared;
      await rm(root, { recursive: true, force: true });
    }
  });

  it('gives the dyn value a type from an example of the workspace, and the line above the rule shows it', async () => {
    const root = await mkdtemp(join(tmpdir(), 'cel-lens-e2e-'));
    await mkdir(join(root, 'config/samples'), { recursive: true });
    await copyFile(join(FIXTURES, 'replica_policy.yaml'), join(root, 'policy.yaml'));
    await copyFile(
      join(FIXTURES, 'replica_limit_sample.yaml'),
      join(root, 'config/samples/limit.yaml'),
    );
    const session = await startEngine(await checkedExtensionPath(), {
      folder: root,
      dynamicRegistration: true,
      writesCellens: true,
    });
    const shared = engine;
    engine = session;
    try {
      const uri = pathToFileURL(join(root, 'policy.yaml')).href;
      const text = await readFile(join(root, 'policy.yaml'), 'utf8');
      await session.connection.sendNotification(DidOpenTextDocumentNotification.type, {
        textDocument: { uri, languageId: 'yaml', version: 1, text },
      });
      await diagnosticsFor(uri, () => true);
      const value = await lensArgument(uri, 'cel-lens.giveType');
      expect(value).toMatchObject({ variable: 'params', file: 'policy.yaml' });

      // The picker lists the example under the value's kind ...
      const choices = await runCommand('cel-lens.typeChoices', value);
      expect(choices).toMatchObject({
        examples: [
          {
            file: 'config/samples/limit.yaml',
            line: 1,
            apiVersion: 'rules.example.com/v1',
            kind: 'ReplicaLimit',
          },
        ],
      });
      expect(choices).toHaveProperty('goTypes');
      expect(choices).toHaveProperty('builtIn');

      // ... the engine works a schema out of it ...
      const sample = await readFile(join(root, 'config/samples/limit.yaml'), 'utf8');
      const inferred = await runCommand('cel-lens.inferType', { text: sample, line: 1 });
      expect(inferred).toMatchObject({
        schema: { type: 'object', properties: { maxReplicas: { type: 'integer' } } },
      });

      // ... and writes it as the value's entry in a new .cellens.
      const schema = (inferred as { schema: unknown }).schema;
      const written = await runCommand('cel-lens.setType', { ...value, schema });
      expect(written).toMatchObject({ change: 'created' });
      expect(await readFile(join(root, '.cellens'), 'utf8')).toContain('maxReplicas:');
      await session.connection.sendNotification(DidChangeWatchedFilesNotification.type, {
        changes: [
          { uri: pathToFileURL(join(root, '.cellens')).href, type: FileChangeType.Created },
        ],
      });

      const typed = await lensesOf(uri);
      expect(typed).toEqual([
        { title: 'object: apps/v1 Deployment · compiles', command: 'cel-lens.showRule' },
        { title: 'params: {apiVersion, …} · Edit', command: 'cel-lens.editType' },
        { title: 'Remove', command: 'cel-lens.removeType' },
      ]);

      // Remove takes the entry out, and the line asks to give the value a type again.
      const editValue = await lensArgument(uri, 'cel-lens.removeType');
      expect(await runCommand('cel-lens.removeType', editValue)).toMatchObject({
        cellens: pathToFileURL(join(root, '.cellens')).href,
      });
      expect(await readFile(join(root, '.cellens'), 'utf8')).not.toContain('maxReplicas');
      await session.connection.sendNotification(DidChangeWatchedFilesNotification.type, {
        changes: [
          { uri: pathToFileURL(join(root, '.cellens')).href, type: FileChangeType.Changed },
        ],
      });
      expect((await lensesOf(uri)).map((lens) => lens.title)).toContain(
        'params: dyn · Give it a type',
      );
    } finally {
      await stopEngine(session);
      engine = shared;
      await rm(root, { recursive: true, force: true });
    }
  });

  it('shows what a CRD rule does to its examples: the part of the line, the command’s answer as the picker lists it, and the toggle', async () => {
    expect(
      running().commands,
      'the engine of bin/ is the pinned one, which has example results',
    ).toContain('cel-lens.exampleResults');
    const root = await mkdtemp(join(tmpdir(), 'cel-lens-e2e-'));
    for (const [fixture, path] of [
      ['limit_crd.yaml', 'crd.yaml'],
      ['limit_small.yaml', 'config/samples/small.yaml'],
      ['limit_big.yaml', 'config/samples/big.yaml'],
      ['limit_huge.yaml', 'testdata/invalid/huge.yaml'],
      ['limit_fine.yaml', 'testdata/invalid/fine.yaml'],
    ]) {
      await mkdir(dirname(join(root, path ?? '')), { recursive: true });
      await copyFile(join(FIXTURES, fixture ?? ''), join(root, path ?? ''));
    }
    const session = await startEngine(await checkedExtensionPath(), { folder: root });
    const shared = engine;
    engine = session;
    try {
      const uri = pathToFileURL(join(root, 'crd.yaml')).href;
      const text = await readFile(join(root, 'crd.yaml'), 'utf8');
      await session.connection.sendNotification(DidOpenTextDocumentNotification.type, {
        textDocument: { uri, languageId: 'yaml', version: 1, text },
      });

      // The part of the line above the rule, shortened by the engine to the 80 characters the line keeps:
      // two wrong verdicts (one refused, one allowed), then the two that pass.
      const isPart = (lens: { command: string }): boolean =>
        lens.command === 'cel-lens.showExampleResults';
      const shown = await lensesWhen(uri, (lenses) => lenses.some(isPart));
      expect(shown.filter(isPart)).toEqual([
        {
          title: '2 wrong · 2 pass',
          command: 'cel-lens.showExampleResults',
        },
      ]);

      // The command the part runs, with its argument, as the extension runs it: a picker whose
      // lines list the wrong verdicts first, each with its file, line and the API server's message.
      const location = await lensArgument(uri, 'cel-lens.showExampleResults');
      const picked: PickedLines[] = [];
      const opened: { uri: string; line: number }[] = [];
      const flow = new ExampleResultsFlow(
        (command, argument, signal) =>
          signal === undefined
            ? session.connection.sendRequest('workspace/executeCommand', {
                command,
                arguments: [argument],
              })
            : session.connection.sendRequest(
                'workspace/executeCommand',
                { command, arguments: [argument] },
                cancellationOf(signal),
              ),
        pickerOf(picked, opened, '$(warning) config/samples/big.yaml:1'),
        { warn: fail, error: fail },
      );
      await flow.show(location);

      expect(picked).toHaveLength(1);
      expect(picked[0]?.title).toBe(
        `Example results of the rule on line ${String(lineOf(text, '- rule:') + 1)}`,
      );
      expect(picked[0]?.lines).toEqual([
        { label: 'Not as expected' },
        {
          label: '$(warning) config/samples/big.yaml:1',
          description: 'refused · expected allowed',
          detail: 'spec: Invalid value: at most 10 replicas',
        },
        {
          label: '$(warning) testdata/invalid/fine.yaml:1',
          description: 'allowed · expected refused',
        },
        { label: 'As expected' },
        { label: '$(pass) config/samples/small.yaml:1', description: 'allowed' },
        {
          label: '$(pass) testdata/invalid/huge.yaml:1',
          description: 'refused',
          detail: 'spec: Invalid value: at most 10 replicas',
        },
      ]);
      expect(opened).toEqual([
        { uri: pathToFileURL(join(root, 'config/samples/big.yaml')).href, line: 1 },
      ]);

      // The wrong verdicts are warnings: on the rule and on big.yaml, which the rule refuses though it
      // expects to be allowed, and on fine.yaml alone, which expects to be refused and is allowed.
      const bigUri = pathToFileURL(join(root, 'config/samples/big.yaml')).href;
      const fineUri = pathToFileURL(join(root, 'testdata/invalid/fine.yaml')).href;
      const warned = (list: Diagnostic[]): boolean =>
        list.filter((each) => each.severity === WARNING).length > 0;
      const crdWarnings = await diagnosticsFor(uri, warned);
      expect(crdWarnings).toHaveLength(1);
      expect(crdWarnings[0]).toMatchObject({ severity: WARNING, source: 'cel-lens' });
      expect(crdWarnings[0]?.message).toContain('refuses config/samples/big.yaml:1');
      const bigWarnings = await diagnosticsFor(bigUri, warned);
      expect(bigWarnings).toHaveLength(1);
      expect(bigWarnings[0]?.message).toContain('expected to be allowed');
      const fineWarnings = await diagnosticsFor(fineUri, warned);
      expect(fineWarnings).toHaveLength(1);
      expect(fineWarnings[0]?.message).toContain('expected to be refused');

      // Off: the part goes, the warnings go, and the engine refuses the command.
      await session.connection.sendNotification(DidChangeConfigurationNotification.type, {
        settings: { ...serverOptions('', undefined, []), exampleResults: false },
      });
      const quiet = await lensesWhen(uri, (lenses) => !lenses.some(isPart));
      expect(quiet.filter(isPart)).toEqual([]);
      expect(await diagnosticsFor(uri, (list) => !warned(list))).toEqual([]);
      expect(await diagnosticsFor(bigUri, (list) => !warned(list))).toEqual([]);
      expect(await diagnosticsFor(fineUri, (list) => !warned(list))).toEqual([]);
      await expect(
        session.connection.sendRequest('workspace/executeCommand', {
          command: 'cel-lens.exampleResults',
          arguments: [location],
        }),
      ).rejects.toMatchObject({ code: -32803 });

      // On again: the part and the warnings come back.
      await session.connection.sendNotification(DidChangeConfigurationNotification.type, {
        settings: { ...serverOptions('', undefined, []), exampleResults: true },
      });
      expect((await lensesWhen(uri, (lenses) => lenses.some(isPart))).filter(isPart)).toHaveLength(
        1,
      );
      expect(await diagnosticsFor(uri, warned)).toHaveLength(1);
      expect(await diagnosticsFor(bigUri, warned)).toHaveLength(1);
      expect(await diagnosticsFor(fineUri, warned)).toHaveLength(1);
    } finally {
      await stopEngine(session);
      engine = shared;
      await rm(root, { recursive: true, force: true });
    }
  });

  it('reads the CRDs below a folder created after start, on a Create change', async () => {
    const root = await mkdtemp(join(tmpdir(), 'cel-lens-e2e-'));
    const session = await startEngine(await checkedExtensionPath(), {
      folder: root,
      dynamicRegistration: true,
      experimental: ['crd-typed-policies'],
    });
    const shared = engine;
    engine = session;
    try {
      const { uri, text } = await open('widget_policy.yaml');
      expect(await diagnosticsFor(uri, (list) => list.length === 0)).toEqual([]);

      const crds = join(root, 'crds');
      await mkdir(crds);
      await copyFile(join(FIXTURES, 'widget_crd.yaml'), join(crds, 'widget_crd.yaml'));
      await session.connection.sendNotification(DidChangeWatchedFilesNotification.type, {
        changes: [{ uri: pathToFileURL(crds).href, type: FileChangeType.Created }],
      });

      const warned = await diagnosticsFor(uri, (list) => list.length > 0);
      expect(warned).toMatchObject([{ source: 'cel-lens', severity: WARNING }]);
      expect(warned[0]?.range.start.line).toBe(lineOf(text, '- expression:'));
    } finally {
      await stopEngine(session);
      engine = shared;
      await rm(root, { recursive: true, force: true });
    }
  });
});
