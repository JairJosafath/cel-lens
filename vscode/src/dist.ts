// Builds the release files. Run by `make dist` through `npm run dist`.
//
//   node src/dist.ts                       one .vsix per platform into dist/
//   node src/dist.ts fetch --out DIR [--pin FILE] [--base-url URL]
//                                          only download and verify the binaries
//   node src/dist.ts install [--bin DIR] [--pin FILE] [--base-url URL]
//                                          put this machine's binary in bin/ unless it is there
//   node src/dist.ts verify [DIR]          check what each .vsix in DIR (dist/) holds
//
// The binaries are the cel-lens-core release pinned in core-version.json, each
// checked against its SHA-256. The pinned SHA-256 of all six are first compared
// with the release's checksums.txt. Nothing is written unless all of them match.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  chmod,
  copyFile,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  writeFile,
} from 'node:fs/promises';
import { arch, platform, tmpdir } from 'node:os';
import { join } from 'node:path';
import { argv, exit, stderr, stdout } from 'node:process';
import { fileURLToPath } from 'node:url';
import { inflateRawSync } from 'node:zlib';

import { releaseDownloadUrl } from './core_releases.ts';

/** One binary of the pinned engine release. */
export interface CoreAsset {
  readonly name: string;
  readonly sha256: string;
}

/** The contents of core-version.json: the engine release and its assets by target. */
export interface CorePin {
  readonly version: string;
  readonly baseUrl: string;
  readonly assets: Readonly<Record<string, CoreAsset>>;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

/** Checks the parsed core-version.json and returns it; throws saying what is wrong. */
export function parsePin(json: unknown): CorePin {
  if (
    !isRecord(json) ||
    typeof json['version'] !== 'string' ||
    (json['baseUrl'] !== undefined && typeof json['baseUrl'] !== 'string')
  ) {
    throw new Error(
      'core-version.json needs a "version" (text); "baseUrl", if present, must be text.',
    );
  }
  const assets: Record<string, CoreAsset> = {};
  const listed = json['assets'];
  for (const [target, asset] of Object.entries(isRecord(listed) ? listed : {})) {
    if (
      !isRecord(asset) ||
      typeof asset['name'] !== 'string' ||
      typeof asset['sha256'] !== 'string'
    ) {
      throw new Error(`core-version.json: asset "${target}" needs a "name" and a "sha256".`);
    }
    assets[target] = { name: asset['name'], sha256: asset['sha256'] };
  }
  if (Object.keys(assets).length === 0) {
    throw new Error('core-version.json lists no assets.');
  }
  return {
    version: json['version'],
    baseUrl: json['baseUrl'] ?? releaseDownloadUrl(json['version']),
    assets,
  };
}

/** The entry names of a .vsix and its extension.vsixmanifest. */
export interface VsixContents {
  readonly entries: readonly string[];
  readonly manifest: string;
}

const END_SIGNATURE = 0x06054b50;
const CENTRAL_SIGNATURE = 0x02014b50;
const MANIFEST_ENTRY = 'extension.vsixmanifest';
const FILE_NAME = /^cel-lens-vscode-([a-z0-9]+-[a-z0-9]+)-\d[^/]*\.vsix$/;

/** Reads the entry names and the manifest from the bytes of a zip file; throws when it is not one. */
export function readVsix(zip: Buffer): VsixContents {
  let end = zip.length - 22;
  while (end >= 0 && zip.readUInt32LE(end) !== END_SIGNATURE) {
    end -= 1;
  }
  if (end < 0) {
    throw new Error('not a zip file: no end-of-directory record.');
  }
  const count = zip.readUInt16LE(end + 10);
  let offset = zip.readUInt32LE(end + 16);
  const entries: string[] = [];
  let manifest: string | undefined;
  for (let index = 0; index < count; index += 1) {
    if (zip.readUInt32LE(offset) !== CENTRAL_SIGNATURE) {
      throw new Error('not a zip file: damaged central directory.');
    }
    const method = zip.readUInt16LE(offset + 10);
    const size = zip.readUInt32LE(offset + 20);
    const nameLength = zip.readUInt16LE(offset + 28);
    const extraLength = zip.readUInt16LE(offset + 30);
    const commentLength = zip.readUInt16LE(offset + 32);
    const local = zip.readUInt32LE(offset + 42);
    const name = zip.toString('utf8', offset + 46, offset + 46 + nameLength);
    entries.push(name);
    if (name === MANIFEST_ENTRY) {
      const start = local + 30 + zip.readUInt16LE(local + 26) + zip.readUInt16LE(local + 28);
      const data = zip.subarray(start, start + size);
      manifest = (method === 8 ? inflateRawSync(data) : data).toString('utf8');
    }
    offset += 46 + nameLength + extraLength + commentLength;
  }
  if (manifest === undefined) {
    throw new Error(`no ${MANIFEST_ENTRY} inside.`);
  }
  return { entries, manifest };
}

/** The target a dist file name states (`linux-x64` in cel-lens-vscode-linux-x64-0.2.4.vsix), if any. */
export function targetOfFileName(fileName: string): string | undefined {
  return FILE_NAME.exec(fileName)?.[1];
}

/** The TargetPlatform attribute of the manifest's Identity element, if any. */
export function manifestTarget(manifest: string): string | undefined {
  const identity = /<Identity\b[^>]*>/.exec(manifest)?.[0];
  return /\bTargetPlatform="([^"]*)"/.exec(identity ?? '')?.[1];
}

/** The problems of one dist file; empty when it holds exactly its own binary and TargetPlatform. */
export function checkVsix(fileName: string, contents: VsixContents): string[] {
  const target = targetOfFileName(fileName);
  if (target === undefined) {
    return [
      `${fileName}: the name does not say a target (cel-lens-vscode-<target>-<version>.vsix)`,
    ];
  }
  const problems: string[] = [];
  const declared = manifestTarget(contents.manifest);
  if (declared !== target) {
    problems.push(
      `${fileName}: the manifest TargetPlatform is ${declared ?? 'missing'}, the name says ${target}`,
    );
  }
  const own = target.startsWith('win32-') ? 'cel-lens-core.exe' : 'cel-lens-core';
  const binaries = contents.entries.filter((entry) => /(^|\/)cel-lens-core(\.exe)?$/.test(entry));
  const wanted = `extension/bin/${own}`;
  if (binaries.length !== 1 || binaries[0] !== wanted) {
    problems.push(
      `${fileName}: expected exactly ${wanted}, found ${binaries.length === 0 ? 'no engine binary' : binaries.join(', ')}`,
    );
  }
  const stray = contents.entries.filter(
    (entry) => entry.startsWith('extension/bin/') && entry !== wanted && !binaries.includes(entry),
  );
  if (stray.length > 0) {
    problems.push(`${fileName}: unexpected files in bin/: ${stray.join(', ')}`);
  }
  return problems;
}

/** Problems across a dist folder: each file checked, and every expected target present once. */
export function checkDist(
  files: ReadonlyMap<string, VsixContents>,
  expectedTargets: readonly string[],
): string[] {
  const problems: string[] = [];
  for (const [fileName, contents] of files) {
    problems.push(...checkVsix(fileName, contents));
  }
  for (const target of expectedTargets) {
    const found = [...files.keys()].filter((name) => targetOfFileName(name) === target);
    if (found.length !== 1) {
      problems.push(`dist/ has ${String(found.length)} .vsix files for ${target}, expected 1`);
    }
  }
  return problems;
}

/** The binary's file name inside the extension for a target such as `win32-x64`. */
export function binaryName(target: string): string {
  return target.startsWith('win32-') ? 'cel-lens-core.exe' : 'cel-lens-core';
}

/**
 * Compares every pinned sha256 with the release's checksums.txt (`sha256sum`
 * output: a hash, then the asset name). Throws, naming each asset that is
 * missing from the file or differs.
 */
export function checkPinChecksums(pin: CorePin, checksums: string): void {
  const listed = new Map<string, string>();
  for (const line of checksums.split('\n')) {
    const match = /^([0-9a-fA-F]{64}) [ *](.+?)\r?$/.exec(line);
    if (match?.[1] !== undefined && match[2] !== undefined) {
      listed.set(match[2], match[1].toLowerCase());
    }
  }
  const problems: string[] = [];
  for (const asset of Object.values(pin.assets)) {
    const expected = listed.get(asset.name);
    if (expected === undefined) {
      problems.push(`${asset.name} is not listed in checksums.txt`);
    } else if (expected !== asset.sha256.toLowerCase()) {
      problems.push(
        `${asset.name}: core-version.json pins ${asset.sha256}, checksums.txt has ${expected}`,
      );
    }
  }
  if (problems.length > 0) {
    throw new Error(
      `core-version.json disagrees with the ${pin.version} checksums.txt:\n${problems.join('\n')}`,
    );
  }
}

/** Fetches `url`; throws "Download of <url> failed: <reason>" on an HTTP error or no connection. */
async function download(url: string): Promise<Response> {
  let response: Response;
  try {
    response = await fetch(url);
  } catch (error) {
    const cause = error instanceof Error && error.cause instanceof Error ? error.cause : error;
    throw new Error(
      `Download of ${url} failed: ${cause instanceof Error ? cause.message : String(cause)}`,
      { cause: error },
    );
  }
  if (!response.ok) {
    throw new Error(`Download of ${url} failed: HTTP ${String(response.status)}`);
  }
  return response;
}

/** Downloads the release's checksums.txt and checks every pinned asset against it. */
export async function verifyPin(pin: CorePin, baseUrl: string = pin.baseUrl): Promise<void> {
  const url = `${baseUrl}/checksums.txt`;
  checkPinChecksums(pin, await (await download(url)).text());
}

/**
 * Downloads every asset of the pin and returns the bytes by target. Throws,
 * naming the asset, on a failed download or a checksum mismatch.
 */
export async function fetchCore(
  pin: CorePin,
  baseUrl: string = pin.baseUrl,
): Promise<Map<string, Buffer>> {
  const binaries = new Map<string, Buffer>();
  for (const [target, asset] of Object.entries(pin.assets)) {
    const url = `${baseUrl}/${asset.name}`;
    const bytes = Buffer.from(await (await download(url)).arrayBuffer());
    const actual = createHash('sha256').update(bytes).digest('hex');
    if (actual !== asset.sha256) {
      throw new Error(
        `Checksum mismatch for ${asset.name}: core-version.json pins ${asset.sha256}, ${url} has ${actual}`,
      );
    }
    binaries.set(target, bytes);
  }
  return binaries;
}

/** Writes the verified binaries into `out`, one folder per target. */
export async function writeCore(binaries: ReadonlyMap<string, Buffer>, out: string): Promise<void> {
  await rm(out, { recursive: true, force: true });
  for (const [target, bytes] of binaries) {
    const directory = join(out, target);
    await mkdir(directory, { recursive: true });
    const file = join(directory, binaryName(target));
    await writeFile(file, bytes);
    await chmod(file, 0o755);
  }
}

async function readPin(file: string): Promise<CorePin> {
  return parsePin(JSON.parse(await readFile(file, 'utf8')));
}

function option(name: string): string | undefined {
  const index = argv.indexOf(name);
  return index === -1 ? undefined : argv[index + 1];
}

const ROOT = fileURLToPath(new URL('..', import.meta.url));

/** Builds dist/cel-lens-vscode-<target>-<version>.vsix for every target of the pin. */
async function buildVsix(): Promise<void> {
  const manifest: unknown = JSON.parse(await readFile(join(ROOT, 'package.json'), 'utf8'));
  const version = isRecord(manifest) ? manifest['version'] : undefined;
  if (typeof version !== 'string') {
    throw new Error('package.json has no version.');
  }
  const pin = await readPin(join(ROOT, 'core-version.json'));
  const staging = await mkdtemp(join(tmpdir(), 'cel-lens-core-'));
  try {
    await verifyPin(pin);
    // All binaries are downloaded and verified before the first package is built.
    await writeCore(await fetchCore(pin), staging);
    await mkdir(join(ROOT, 'dist'), { recursive: true });
    for (const target of Object.keys(pin.assets)) {
      await rm(join(ROOT, 'bin'), { recursive: true, force: true });
      await mkdir(join(ROOT, 'bin'));
      await copyFile(
        join(staging, target, binaryName(target)),
        join(ROOT, 'bin', binaryName(target)),
      );
      execFileSync(
        join(ROOT, 'node_modules', '.bin', 'vsce'),
        [
          'package',
          '--target',
          target,
          '--no-dependencies',
          '--skip-license',
          '--allow-missing-repository',
          '--out',
          join(ROOT, 'dist', `cel-lens-vscode-${target}-${version}.vsix`),
        ],
        { cwd: ROOT, stdio: 'inherit' },
      );
    }
    await verifyDist(join(ROOT, 'dist'), Object.keys(pin.assets));
  } finally {
    await rm(join(ROOT, 'bin'), { recursive: true, force: true });
    await rm(staging, { recursive: true, force: true });
  }
}

/** Puts the pinned engine for this machine in `bin`, where the extension looks for it; keeps one that has the pinned checksum. */
async function installEngine(): Promise<void> {
  const bin = option('--bin') ?? join(ROOT, 'bin');
  const target = `${platform()}-${arch()}`;
  const file = join(bin, binaryName(target));
  const pin = await readPin(option('--pin') ?? join(ROOT, 'core-version.json'));
  const asset = pin.assets[target];
  if (asset === undefined) {
    throw new Error(`core-version.json has no engine for ${target}.`);
  }
  const existing = await readFile(file).then(
    (bytes) => bytes,
    () => undefined,
  );
  if (
    existing !== undefined &&
    createHash('sha256').update(existing).digest('hex') === asset.sha256
  ) {
    stdout.write(`${file} is already there\n`);
    return;
  }
  const baseUrl = option('--base-url');
  stdout.write(`Fetching cel-lens-core ${pin.version} from ${baseUrl ?? pin.baseUrl}\n`);
  await verifyPin(pin, baseUrl);
  const binaries = await fetchCore({ ...pin, assets: { [target]: asset } }, baseUrl);
  const bytes = binaries.get(target);
  if (bytes === undefined) {
    throw new Error(`No engine was fetched for ${target}.`);
  }
  await mkdir(bin, { recursive: true });
  await writeFile(file, bytes);
  await chmod(file, 0o755);
  stdout.write(`Installed cel-lens-core ${pin.version} as ${file}\n`);
}

/** Throws unless every .vsix in `directory` holds exactly its own binary and TargetPlatform. */
export async function verifyDist(directory: string, targets: readonly string[]): Promise<void> {
  const files = new Map<string, VsixContents>();
  let names: string[];
  try {
    names = await readdir(directory);
  } catch (error) {
    if (error instanceof Error && 'code' in error && error.code === 'ENOENT') {
      throw new Error(`The folder ${directory} does not exist. Run "make dist" to create it.`, {
        cause: error,
      });
    }
    throw error;
  }
  for (const name of names.sort()) {
    if (name.endsWith('.vsix')) {
      try {
        files.set(name, readVsix(await readFile(join(directory, name))));
      } catch (error) {
        throw new Error(`${name}: ${error instanceof Error ? error.message : String(error)}`, {
          cause: error,
        });
      }
    }
  }
  const problems = checkDist(files, targets);
  if (problems.length > 0) {
    throw new Error(`The .vsix files in ${directory} are wrong:\n${problems.join('\n')}`);
  }
}

async function main(): Promise<void> {
  if (argv[2] === 'install') {
    await installEngine();
    return;
  }
  if (argv[2] === 'verify') {
    const pin = await readPin(join(ROOT, 'core-version.json'));
    await verifyDist(argv[3] ?? join(ROOT, 'dist'), Object.keys(pin.assets));
    stdout.write('Every .vsix holds exactly its own engine binary and TargetPlatform.\n');
    return;
  }
  if (argv[2] !== 'fetch') {
    await buildVsix();
    return;
  }
  const out = option('--out');
  if (out === undefined) {
    throw new Error('Usage: node src/dist.ts fetch --out DIR [--pin FILE] [--base-url URL]');
  }
  const pin = await readPin(option('--pin') ?? join(ROOT, 'core-version.json'));
  await verifyPin(pin, option('--base-url'));
  await writeCore(await fetchCore(pin, option('--base-url')), out);
  stdout.write(`Fetched cel-lens-core ${pin.version} into ${out}\n`);
}

if (argv[1] === fileURLToPath(import.meta.url)) {
  main().catch((error: unknown) => {
    stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    exit(1);
  });
}
