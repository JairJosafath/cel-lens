// Starts the real cel-lens-core in bin/ (put there by `npm run engine`; no network here) with the command and arguments the
// extension uses, and talks to it as a language client over stdio. No VS Code, no display, no
// stand-in. A test that gets no answer fails when vitest's time limit passes
// (vitest.e2e.config.mts); an engine that exits fails it at once, with its error output.
import type { ChildProcess } from 'node:child_process';
import { readFile, stat } from 'node:fs/promises';
import { platform } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import {
  DidChangeConfigurationNotification,
  DidChangeTextDocumentNotification,
  DidOpenTextDocumentNotification,
  ExitNotification,
  InitializeRequest,
  InitializedNotification,
  PublishDiagnosticsNotification,
  ShutdownRequest,
  StreamMessageReader,
  StreamMessageWriter,
  createProtocolConnection,
  type CompletionItem,
  type CompletionList,
  type Diagnostic,
  type Hover,
  type ProtocolConnection,
} from 'vscode-languageserver-protocol/node';

import { parseVersionInfo, serverOptions } from '../../src/kubernetes_version';
import {
  readmeDefaultKubernetesVersion,
  readmeKubernetesVersions,
  readmeSection,
} from '../../src/readme';
import { requireEngineFile } from '../../src/engine_file';
import { resolveServer } from '../../src/server_path';
import { spawnEngine } from '../../src/spawn_engine';

const FIXTURES = fileURLToPath(new URL('fixtures/', import.meta.url));
const EXTENSION_PATH = fileURLToPath(new URL('../../', import.meta.url));
const ERROR = 1;
const WARNING = 2;
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

/** Starts the engine as the extension does and initializes a session. */
async function startEngine(extensionPath: string): Promise<Engine> {
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
  connection.listen();
  const initialized = await Promise.race([
    connection.sendRequest(InitializeRequest.type, {
      processId: process.pid,
      rootUri: null,
      capabilities: {},
      initializationOptions: serverOptions(''),
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
    published,
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

afterAll(async () => {
  if (engine !== undefined) {
    try {
      await Promise.race([engine.connection.sendRequest(ShutdownRequest.type), engine.exited]);
      await engine.connection.sendNotification(ExitNotification.type);
    } catch {
      // The engine already exited; the kill below is then a no-op.
    }
    engine.connection.dispose();
    engine.process.kill();
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
