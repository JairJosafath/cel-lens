// Starts the real cel-lens-core in bin/ (put there by `npm run engine`; no network here) with the command and arguments the
// extension uses, and talks to it as a language client over stdio. No VS Code, no display, no
// stand-in. A test that gets no answer fails when vitest's time limit passes
// (vitest.e2e.config.mts); an engine that exits fails it at once, with its error output.
import type { ChildProcess } from 'node:child_process';
import { copyFile, mkdir, mkdtemp, readFile, rm, stat } from 'node:fs/promises';
import { platform, tmpdir } from 'node:os';
import { join } from 'node:path';
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
  type Registration,
  type Hover,
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
import { requireEngineFile } from '../../src/engine_file';
import { resolveServer } from '../../src/server_path';
import { spawnEngine } from '../../src/spawn_engine';

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
  /** Resolves on the next publishDiagnostics. */
  nextPublish: () => Promise<void>;
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
}

/** Starts the engine as the extension does and initializes a session. */
async function startEngine(extensionPath: string, options: SessionOptions = {}): Promise<Engine> {
  const server = await resolveServer({
    setting: '',
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
  const registrations: Registration[] = [];
  connection.onRequest(RegistrationRequest.type, (params) => {
    registrations.push(...params.registrations);
  });
  connection.listen();
  const initialized = await Promise.race([
    connection.sendRequest(InitializeRequest.type, {
      processId: process.pid,
      rootUri: null,
      workspaceFolders:
        options.folder === undefined
          ? null
          : [{ uri: pathToFileURL(options.folder).href, name: 'workspace' }],
      capabilities: options.dynamicRegistration
        ? { workspace: { didChangeWatchedFiles: { dynamicRegistration: true } } }
        : {},
      initializationOptions: serverOptions('', undefined, options.experimental),
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
    nextPublish: () =>
      new Promise<void>((resolve) => {
        waiting.push(resolve);
      }),
  };
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
