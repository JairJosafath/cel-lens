// Starts the real cel-lens-core in bin/ (put there by `npm run engine`; no network here) with the command and arguments the
// extension uses, and talks to it as a language client over stdio. No VS Code, no display, no
// stand-in. A test that gets no answer fails when vitest's time limit passes
// (vitest.e2e.config.mts); an engine that exits fails it at once, with its error output.
import type { ChildProcess } from 'node:child_process';
import { copyFile, mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { platform, tmpdir } from 'node:os';
import { basename, dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
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
import {
  ENGINE_RULE_LIST,
  RuleList,
  parseRuleListAnswer,
  parseScanLimits,
} from '../../src/rule_list';
import { RuleTree, type TreeNode } from '../../src/rule_tree';
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
  /** The engine's whole `initialize` result. */
  readonly initializeResult: unknown;
  /** The `window/showMessageRequest` the engine sent, as it sent them. */
  readonly messageRequests: { readonly params: unknown }[];
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
  /** Several workspace folders on disk, named by their folder's name, in this order. */
  readonly folders?: readonly string[];
  /** Tells the engine the client registers file watchers. */
  readonly dynamicRegistration?: boolean;
  readonly experimental?: readonly string[];
  /** Whether the engine draws the line above each rule: the extension's `cel-lens.inlineInfo`. */
  readonly inlineInfo?: boolean;
  /** Tells the engine the client applies the edits that write `.cellens`, and applies them to disk. */
  readonly writesCellens?: boolean;
  /** Whether the engine shows what each rule does to its examples: the extension's `cel-lens.exampleResults`. */
  readonly exampleResults?: boolean;
  /** Whether the engine keeps the list of the rules of the workspace: what the cel-lens view asks with `ruleList: true`. */
  readonly ruleList?: boolean;
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
  const messageRequests: { params: unknown }[] = [];
  connection.onRequest('window/showMessageRequest', (params: unknown) => {
    messageRequests.push({ params });
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
        options.folders !== undefined
          ? options.folders.map((folder) => ({
              uri: pathToFileURL(folder).href,
              name: basename(folder),
            }))
          : options.folder === undefined
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
        ...(options.ruleList === true && { ruleList: true }),
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
    initializeResult: initialized,
    messageRequests,
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
  // cellens-types is on for the shared session, which most of the tests below read the lines of.
  engine = await startEngine(await checkedExtensionPath(), { experimental: ['cellens-types'] });
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
    const quiet = await startEngine(await checkedExtensionPath(), {
      inlineInfo: false,
      experimental: ['cellens-types'],
    });
    const shared = engine;
    engine = quiet;
    try {
      const { uri } = await open('replica_policy.yaml');
      await diagnosticsFor(uri, () => true);
      expect(await lensesOf(uri)).toEqual([]);

      await quiet.connection.sendNotification(DidChangeConfigurationNotification.type, {
        settings: { ...serverOptions('', undefined, ['cellens-types']), inlineInfo: true },
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
      experimental: ['cellens-types'],
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

  it('offers no Give it a type while cellens-types is off, and offers it, and types a value from .cellens, once it is on', async () => {
    const root = await mkdtemp(join(tmpdir(), 'cel-lens-e2e-'));
    await copyFile(join(FIXTURES, 'replica_policy.yaml'), join(root, 'policy.yaml'));
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
      const titles = async (): Promise<string[]> => (await lensesOf(uri)).map((lens) => lens.title);

      // Off: no part to give a type, and the commands behind it refuse.
      expect(await titles()).toEqual(['object: apps/v1 Deployment · compiles']);
      await expect(
        runCommand('cel-lens.setType', {
          file: 'policy.yaml',
          document: 'ValidatingAdmissionPolicy replica-limit',
          variable: 'params',
          type: 'int',
        }),
      ).rejects.toThrow(/cellens-types/);

      // On: the part is there, as it asks the editor to ask for the lines again.
      await session.connection.sendNotification(DidChangeConfigurationNotification.type, {
        settings: { ...serverOptions('', undefined, ['cellens-types']), inlineInfo: true },
      });
      const on = await lensesWhen(uri, (found) =>
        found.some((lens) => lens.title === 'params: dyn · Give it a type'),
      );
      expect(on.map((lens) => lens.title)).toEqual([
        'object: apps/v1 Deployment · compiles',
        'params: dyn · Give it a type',
      ]);

      // A value typed from .cellens: the entry is written, and the line follows it.
      const value = await lensArgument(uri, 'cel-lens.giveType');
      const written = await runCommand('cel-lens.setType', {
        ...value,
        schema: { type: 'object', properties: { maxReplicas: { type: 'integer' } } },
      });
      expect(written).toMatchObject({ change: 'created' });
      await session.connection.sendNotification(DidChangeWatchedFilesNotification.type, {
        changes: [
          { uri: pathToFileURL(join(root, '.cellens')).href, type: FileChangeType.Created },
        ],
      });
      const typed = await lensesWhen(uri, (found) =>
        found.some((lens) => lens.title === 'params: {maxReplicas} · Edit'),
      );
      expect(typed.map((lens) => lens.command)).toContain('cel-lens.editType');

      // Off again: the typed value is dyn, and the part is gone.
      await session.connection.sendNotification(DidChangeConfigurationNotification.type, {
        settings: { ...serverOptions('', undefined, []), inlineInfo: true },
      });
      const off = await lensesWhen(
        uri,
        (found) => !found.some((lens) => lens.command === 'cel-lens.editType'),
      );
      expect(off.map((lens) => lens.title)).toEqual(['object: apps/v1 Deployment · compiles']);
    } finally {
      await stopEngine(session);
      engine = shared;
      await rm(root, { recursive: true, force: true });
    }
  });

  it('asks the client once, with an action that names cellens-types, when the workspace holds a .cellens it does not read', async () => {
    const root = await mkdtemp(join(tmpdir(), 'cel-lens-e2e-'));
    await copyFile(join(FIXTURES, 'replica_policy.yaml'), join(root, 'policy.yaml'));
    await writeFile(
      join(root, '.cellens'),
      'types:\n  - file: policy.yaml\n    document: ValidatingAdmissionPolicy replica-limit\n    variable: params\n    type: "{maxReplicas: int}"\n',
    );
    const session = await startEngine(await checkedExtensionPath(), { folder: root });
    const shared = engine;
    engine = session;
    try {
      await vi.waitFor(
        () => {
          expect(session.messageRequests).toHaveLength(1);
        },
        { timeout: 10_000 },
      );
      expect(session.messageRequests[0]?.params).toMatchObject({
        type: 2,
        message: expect.stringContaining('cellens-types') as string,
        actions: [{ title: 'Turn on cellens-types', experimental: 'cellens-types' }],
      });

      // Opening the policy checks it again, and the notice does not come a second time.
      const uri = pathToFileURL(join(root, 'policy.yaml')).href;
      const text = await readFile(join(root, 'policy.yaml'), 'utf8');
      await session.connection.sendNotification(DidOpenTextDocumentNotification.type, {
        textDocument: { uri, languageId: 'yaml', version: 1, text },
      });
      await diagnosticsFor(uri, () => true);
      expect(await lensesOf(uri)).toEqual([
        { title: 'object: apps/v1 Deployment · compiles', command: 'cel-lens.showRule' },
      ]);
      expect(session.messageRequests).toHaveLength(1);
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

  it('shows what a ValidatingAdmissionPolicy does to its examples under each binding: the part of the line, and the command’s answer as the picker lists it', async () => {
    const root = await mkdtemp(join(tmpdir(), 'cel-lens-e2e-'));
    for (const [fixture, path] of [
      ['policy_limit.yaml', 'policy.yaml'],
      ['policy_bindings.yaml', 'bindings.yaml'],
      ['policy_params.yaml', 'config/samples/limits.yaml'],
      ['policy_small.yaml', 'config/samples/small.yaml'],
      ['policy_big.yaml', 'config/samples/big.yaml'],
    ]) {
      await mkdir(dirname(join(root, path ?? '')), { recursive: true });
      await copyFile(join(FIXTURES, fixture ?? ''), join(root, path ?? ''));
    }
    const session = await startEngine(await checkedExtensionPath(), { folder: root });
    const shared = engine;
    engine = session;
    try {
      const uri = pathToFileURL(join(root, 'policy.yaml')).href;
      const text = await readFile(join(root, 'policy.yaml'), 'utf8');
      await session.connection.sendNotification(DidOpenTextDocumentNotification.type, {
        textDocument: { uri, languageId: 'yaml', version: 1, text },
      });

      // The validation's part, and the audit annotation's: the validation is refused under one
      // binding and warned under the other, the annotation has a value under both.
      const isPart = (lens: { command: string }): boolean =>
        lens.command === 'cel-lens.showExampleResults';
      const shown = await lensesWhen(uri, (lenses) => lenses.filter(isPart).length === 2);
      expect(shown.filter(isPart).map((lens) => lens.title)).toEqual([
        '1 wrong · 1 passes',
        'annotates 2 examples · under 2 bindings',
      ]);

      // The command the validation's part runs: each example under each binding, with its verdict, the
      // binding and the param it ran under, and the API server's answer.
      const location = await lensArgument(uri, 'cel-lens.showExampleResults');
      const picked: PickedLines[] = [];
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
        pickerOf(picked, [], 'none'),
        { warn: fail, error: fail },
      );
      await flow.show(location);

      expect(picked[0]?.title).toBe(
        `Example results of the rule on line ${String(lineOf(text, '- expression:') + 1)}`,
      );
      expect(picked[0]?.lines).toEqual([
        { label: 'Not as expected' },
        {
          label: '$(warning) config/samples/big.yaml:1 · deny-limits',
          description: 'refused · expected allowed',
          detail:
            'param config/samples/limits.yaml:1; denied: a deployment may not run more replicas than its limit; annotations: replica-limit/replicas=20',
        },
        { label: 'As expected' },
        {
          label: '$(megaphone) config/samples/big.yaml:1 · warn-limits',
          description: 'warned · allowed, with a warning',
          detail: expect.stringMatching(
            /^param config\/samples\/limits\.yaml:1; warnings: a deployment may not run more replicas than its limit; annotations: replica-limit\/replicas=20, validation_failure=\[\{"message":/,
          ) as string,
        },
        {
          label: '$(pass) config/samples/small.yaml:1 · deny-limits',
          description: 'allowed',
          detail: 'param config/samples/limits.yaml:1; annotations: replica-limit/replicas=2',
        },
        {
          label: '$(pass) config/samples/small.yaml:1 · warn-limits',
          description: 'allowed',
          detail: 'param config/samples/limits.yaml:1; annotations: replica-limit/replicas=2',
        },
      ]);

      // The wrong verdict, refused under the Deny binding, is a warning on the policy and on the example.
      const warned = (list: { severity?: number }[]): boolean =>
        list.some((each) => each.severity === WARNING);
      const policyWarnings = await diagnosticsFor(uri, warned);
      expect(policyWarnings[0]?.message).toContain(
        'refuses config/samples/big.yaml:1 under binding deny-limits with param config/samples/limits.yaml:1',
      );
    } finally {
      await stopEngine(session);
      engine = shared;
      await rm(root, { recursive: true, force: true });
    }
  });

  it('says an unquoted on in an example is an error at its field, with the line of the word, where the API server reads a boolean, and nothing of a quoted one', async () => {
    const root = await mkdtemp(join(tmpdir(), 'cel-lens-e2e-'));
    for (const [fixture, path] of [
      ['tier_crd.yaml', 'crd.yaml'],
      ['tier_on.yaml', 'config/samples/gold.yaml'],
      ['tier_quoted.yaml', 'config/samples/silver.yaml'],
    ]) {
      await mkdir(dirname(join(root, path ?? '')), { recursive: true });
      await copyFile(join(FIXTURES, fixture ?? ''), join(root, path ?? ''));
    }
    const session = await startEngine(await checkedExtensionPath(), { folder: root });
    const shared = engine;
    engine = session;
    try {
      const open = async (path: string): Promise<{ uri: string; text: string }> => {
        const uri = pathToFileURL(join(root, path)).href;
        const text = await readFile(join(root, path), 'utf8');
        await session.connection.sendNotification(DidOpenTextDocumentNotification.type, {
          textDocument: { uri, languageId: 'yaml', version: 1, text },
        });
        return { uri, text };
      };
      const gold = await open('config/samples/gold.yaml');
      const silver = await open('config/samples/silver.yaml');

      const errors = await diagnosticsFor(gold.uri, (list) =>
        list.some((each) => each.severity === ERROR),
      );
      expect(errors).toHaveLength(1);
      expect(errors[0]).toMatchObject({ source: 'cel-lens', severity: ERROR });
      // The engine says what the API server says, and which line the word is on, and puts it on the
      // field the word is the value of: the key `tier`, which the line above it names.
      expect(errors[0]?.message).toContain('spec.tier in body must be of type string');
      expect(errors[0]?.message).toContain(
        `(line ${String(lineOf(gold.text, 'tier: on') + 1)}: kubectl reads the unquoted on as a boolean, as YAML 1.1 does; quote it if a string is meant)`,
      );
      const line = lineOf(gold.text, 'tier: on');
      const column = (gold.text.split('\n')[line] ?? '').indexOf('tier');
      expect(errors[0]?.range).toEqual({
        start: { line, character: column },
        end: { line, character: column + 'tier'.length },
      });
      // The quoted word is a string: nothing is said about it.
      expect(await diagnosticsFor(silver.uri, () => true)).toEqual([]);
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

describe('the cel-lens.ruleList command, which the cel-lens view lists the rules of the workspace with', () => {
  it('answers the documents with rules of a workspace folder, and the view draws them', async () => {
    const root = await mkdtemp(join(tmpdir(), 'cel-lens-e2e-'));
    await mkdir(join(root, 'config'), { recursive: true });
    for (const [fixture, path] of [
      ['valid_crd.yaml', 'config/valid.yaml'],
      ['broken_crd.yaml', 'config/broken.yaml'],
      ['widget_policy.yaml', 'config/policy.yaml'],
    ]) {
      await copyFile(join(FIXTURES, fixture ?? ''), join(root, path ?? ''));
    }
    // A CRD being written, before it is named: the engine lists it with an empty name (review of #27).
    await writeFile(
      join(root, 'config/unnamed.yaml'),
      (await readFile(join(FIXTURES, 'valid_crd.yaml'), 'utf8')).replace(/^ {2}name: .*\n/m, ''),
    );
    const session = await startEngine(await checkedExtensionPath(), {
      folder: root,
      ruleList: true,
    });
    try {
      expect(session.commands).toContain(ENGINE_RULE_LIST);

      const answer = parseRuleListAnswer(
        await session.connection.sendRequest('workspace/executeCommand', {
          command: ENGINE_RULE_LIST,
          arguments: [],
        }),
      );

      expect(answer).toBeDefined();
      expect(answer?.documents.map((document) => document.file).sort()).toEqual([
        'config/broken.yaml',
        'config/policy.yaml',
        'config/unnamed.yaml',
        'config/valid.yaml',
      ]);
      const folder = pathToFileURL(root).href;
      const list = new RuleList();
      const limits = parseScanLimits(session.initializeResult);
      expect(limits.files).toBeGreaterThan(0);
      expect(limits.fileBytes).toBeGreaterThan(0);
      const tree = new RuleTree(list, {
        folders: [{ uri: folder, name: 'workspace' }],
        normalize: (uri) => new URL(uri).href.replace(/\/+$/, ''),
        showExamples: () => true,
        limits,
        problemsOnly: false,
      });
      if (answer === undefined) {
        throw new Error('the engine answered something the extension does not read');
      }
      tree.update(list.apply(answer, 'all'));
      const rows = (node: TreeNode): string[] => [
        `${node.label} | ${node.description ?? ''}`,
        ...node.children.flatMap(rows),
      ];

      const drawn = tree.roots().flatMap(rows);
      expect(drawn).toContain('widgets.example.com | CRD · config/valid.yaml');
      expect(drawn).toContain('self.replicas > 1 | compiles');
      expect(drawn).toContain('widgets.example.com | CRD · config/broken.yaml');
      expect(drawn.some((row) => row.endsWith('| 1 problem'))).toBe(true);
      expect(drawn).toContain('widget-limit | ValidatingAdmissionPolicy · config/policy.yaml');
      expect(drawn).toContain('(no name) CRD | CRD · config/unnamed.yaml');
      // A rule opens at its expression: the line of `rule:` in its file.
      const valid = answer.documents.find((document) => document.file === 'config/valid.yaml');
      const text = await readFile(join(root, 'config/valid.yaml'), 'utf8');
      expect(valid?.rules[0]?.location.range.start.line).toBe(lineOf(text, 'rule:'));
      expect(list.totals.rules).toBeGreaterThanOrEqual(3);
      // The badge counts the rule that does not compile, and the cursor on it finds its row.
      expect(list.problems).toBeGreaterThanOrEqual(1);
      const broken = await readFile(join(root, 'config/broken.yaml'), 'utf8');
      const ruleLine = lineOf(broken, 'rule:');
      const found = tree.at(`${folder}/config/broken.yaml`, ruleLine, 8);
      expect(found?.kind).toBe('rule');
      expect(found?.icon.id).toBe('error');
    } finally {
      await stopEngine(session);
      await rm(root, { recursive: true, force: true });
    }
  });

  it('tells a client that asked for the list when it changed, and answers what changed since', async () => {
    const root = await mkdtemp(join(tmpdir(), 'cel-lens-e2e-'));
    await mkdir(join(root, 'config'), { recursive: true });
    await copyFile(join(FIXTURES, 'valid_crd.yaml'), join(root, 'config/valid.yaml'));
    await copyFile(join(FIXTURES, 'broken_crd.yaml'), join(root, 'config/other.yaml'));
    const session = await startEngine(await checkedExtensionPath(), {
      folder: root,
      ruleList: true,
    });
    try {
      expect(session.commands).toContain(ENGINE_RULE_LIST);
      const notifications: unknown[] = [];
      session.connection.onNotification('cel-lens/ruleListChanged', (params: unknown) => {
        notifications.push(params);
      });
      const ask = async (
        argument?: unknown,
      ): Promise<NonNullable<ReturnType<typeof parseRuleListAnswer>>> => {
        const answer = parseRuleListAnswer(
          await session.connection.sendRequest('workspace/executeCommand', {
            command: ENGINE_RULE_LIST,
            arguments: argument === undefined ? [] : [argument],
          }),
        );
        if (answer === undefined) {
          throw new Error('the engine answered something the extension does not read');
        }
        return answer;
      };
      const first = await ask();
      expect(first.generation).toBeDefined();
      const generation = first.generation ?? 0;
      expect(generation).toBeGreaterThan(1_000_000_000_000);

      // Edit the valid CRD in the editor: a rule that does not compile.
      const uri = pathToFileURL(join(root, 'config/valid.yaml')).href;
      const text = await readFile(join(root, 'config/valid.yaml'), 'utf8');
      notifications.length = 0;
      await session.connection.sendNotification(DidOpenTextDocumentNotification.type, {
        textDocument: { uri, languageId: 'yaml', version: 1, text },
      });
      await session.connection.sendNotification(DidChangeTextDocumentNotification.type, {
        textDocument: { uri, version: 2 },
        contentChanges: [{ text: text.replace('self.replicas > 1', 'self.replicas >') }],
      });
      // The edit's own notification: one that came after the edit, with a generation beyond the one held before it.
      await vi.waitFor(
        () => {
          expect(
            notifications.some(
              (notice) =>
                isRecord(notice) &&
                typeof notice['generation'] === 'number' &&
                notice['generation'] > generation,
            ),
          ).toBe(true);
        },
        { timeout: 10_000 },
      );

      const change = await ask({ since: generation });
      expect(change.full).toBe(false);
      expect(change.documents.map((document) => document.uri)).toEqual([uri]);
      expect(change.generation).toBeGreaterThan(generation);
      const lastNotice = notifications.at(-1);
      expect(lastNotice).toMatchObject({ generation: expect.any(Number) as number });

      // The model applies it file by file: only that file changes, and the badge follows.
      const list = new RuleList();
      list.apply(first, 'all');
      const before = list.problems;
      const applied = list.apply(change, 'since');
      expect(applied.files).toEqual([uri]);
      expect(list.problems).toBeGreaterThan(before);

      // An older generation than the engine has is answered with the whole list.
      const whole = await ask({ since: 1 });
      expect(whole.full).toBe(true);
      expect(whole.documents.length).toBeGreaterThanOrEqual(2);
    } finally {
      await stopEngine(session);
      await rm(root, { recursive: true, force: true });
    }
  });

  it('lists a workspace of two folders with its states, as the view reads them, and gives back an edited file through since', async () => {
    const root = await mkdtemp(join(tmpdir(), 'cel-lens-e2e-'));
    const one = join(root, 'one');
    const two = join(root, 'two');
    for (const [fixture, path] of [
      ['broken_crd.yaml', 'one/crd/broken.yaml'],
      ['limit_crd.yaml', 'one/crd/limit.yaml'],
      ['limit_small.yaml', 'one/config/samples/small.yaml'],
      ['limit_big.yaml', 'one/config/samples/big.yaml'],
      ['widget_policy.yaml', 'two/policy.yaml'],
      ['mutating_policy.yaml', 'two/mutating.yaml'],
    ]) {
      await mkdir(dirname(join(root, path ?? '')), { recursive: true });
      await copyFile(join(FIXTURES, fixture ?? ''), join(root, path ?? ''));
    }
    const session = await startEngine(await checkedExtensionPath(), {
      folders: [one, two],
      ruleList: true,
    });
    try {
      const notifications: unknown[] = [];
      session.connection.onNotification('cel-lens/ruleListChanged', (params: unknown) => {
        notifications.push(params);
      });
      const ask = async (
        argument?: unknown,
      ): Promise<NonNullable<ReturnType<typeof parseRuleListAnswer>>> => {
        const answer = parseRuleListAnswer(
          await session.connection.sendRequest('workspace/executeCommand', {
            command: ENGINE_RULE_LIST,
            arguments: argument === undefined ? [] : [argument],
          }),
        );
        if (answer === undefined) {
          throw new Error('the engine answered something the extension does not read');
        }
        return answer;
      };
      // The check of the examples runs in the background: ask until the rule of the CRD says what its examples did.
      let first = await ask();
      await vi.waitFor(
        async () => {
          first = await ask();
          expect(
            first.documents
              .flatMap((document) => document.rules)
              .map((rule) => rule.examples?.state),
          ).toContain('wrong');
        },
        { timeout: 20_000, interval: 250 },
      );

      const list = new RuleList();
      const folderOf = (path: string): string => pathToFileURL(path).href;
      const tree = new RuleTree(list, {
        folders: [
          { uri: folderOf(one), name: 'one' },
          { uri: folderOf(two), name: 'two' },
        ],
        normalize: (uri) => new URL(uri).href.replace(/\/+$/, ''),
        showExamples: () => true,
        limits: parseScanLimits(session.initializeResult),
        problemsOnly: false,
      });
      tree.update(list.apply(first, 'all'));

      // The folders in the workspace's order, then their documents, then the rules of each.
      const outline = (nodes: readonly TreeNode[], depth = 0): string[] =>
        nodes.flatMap((node) => [
          `${'  '.repeat(depth)}${node.label}`,
          ...outline(node.children, depth + 1),
        ]);
      const roots = tree.roots();
      expect(roots.map((node) => node.label)).toEqual(['one', 'two']);
      expect(roots.map((node) => node.children.map((child) => child.description))).toEqual([
        ['CRD · crd/broken.yaml', 'CRD · crd/limit.yaml'],
        ['MutatingAdmissionPolicy · mutating.yaml', 'ValidatingAdmissionPolicy · policy.yaml'],
      ]);
      const rules = roots.flatMap((node) => node.children.flatMap((child) => child.children));
      const byDocument = (name: string): TreeNode[] =>
        roots.flatMap((node) => node.children.filter((child) => child.label === name))[0]
          ?.children ?? [];
      // A rule that does not compile is an error with its problem counted; one whose example is wrong is a warning with the line's words.
      const [broken] = roots[0]?.children[0]?.children ?? [];
      expect(broken).toMatchObject({ description: '1 problem', icon: { id: 'error' } });
      expect(broken?.rule?.problem).toBe(true);
      const [limit] = roots[0]?.children[1]?.children ?? [];
      expect(limit?.description).toContain('refuses 1 example it should allow');
      expect(limit).toMatchObject({ icon: { id: 'warning' }, contextValue: 'ruleWithExamples' });
      expect(limit?.rule?.problem).toBe(true);
      // The policies compile; a rule with no example has none of its words.
      for (const node of rules.filter((rule) => rule.document?.kind.endsWith('Policy') === true)) {
        expect(node.rule?.compile.state).not.toBe('error');
      }
      expect(byDocument('widget-limit').length).toBeGreaterThan(0);
      expect(outline(roots)).toContain('  sidecar-policy.example.com');
      const problems = list.problems;
      expect(problems).toBeGreaterThanOrEqual(2);

      // An edit of an open file reaches the view as that file alone, through since.
      const generation = first.generation ?? 0;
      const uri = pathToFileURL(join(two, 'policy.yaml')).href;
      const text = await readFile(join(two, 'policy.yaml'), 'utf8');
      notifications.length = 0;
      await session.connection.sendNotification(DidOpenTextDocumentNotification.type, {
        textDocument: { uri, languageId: 'yaml', version: 1, text },
      });
      await session.connection.sendNotification(DidChangeTextDocumentNotification.type, {
        textDocument: { uri, version: 2 },
        contentChanges: [
          { text: text.replace('params.spec.maxReplica > 0', 'params.spec.maxReplica >') },
        ],
      });
      // The edit's own notification: one that came after the edit, with a generation beyond the one held before it.
      await vi.waitFor(
        () => {
          expect(
            notifications.some(
              (notice) =>
                isRecord(notice) &&
                typeof notice['generation'] === 'number' &&
                notice['generation'] > generation,
            ),
          ).toBe(true);
        },
        { timeout: 10_000 },
      );
      const change = await ask({ since: generation });
      expect(change.full).toBe(false);
      expect(change.documents.map((document) => document.uri)).toEqual([uri]);
      const refresh = tree.update(list.apply(change, 'since'));
      expect(list.problems).toBe(problems + 1);
      expect(refresh).toHaveLength(1);
      expect(refresh[0]?.label).toBe('widget-limit');
      expect(tree.at(uri, lineOf(text, '- expression:'), 6)?.kind).toBe('rule');
    } finally {
      await stopEngine(session);
      await rm(root, { recursive: true, force: true });
    }
  });
});
