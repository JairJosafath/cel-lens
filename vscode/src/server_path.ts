import { posix, win32 } from 'node:path';

/** The language server binary the extension looks for. */
export const SERVER_BINARY = 'cel-lens-core';

/** The arguments that make the binary speak LSP over stdio. */
export const SERVER_ARGS: readonly string[] = ['lsp'];

/** The folder inside the extension that holds the bundled binary. */
export const BUNDLED_FOLDER = 'bin';

/** Where a server was found: the `cel-lens.serverPath` setting, or the binary bundled in the extension. */
export type ServerSource = 'setting' | 'bundled';

/** What `resolveServer` needs from its surroundings, so it stays pure. */
export interface ResolveInput {
  /** The `cel-lens.serverPath` setting; blank means "use the bundled binary". */
  readonly setting: string;
  /** The folder the extension is installed in. */
  readonly extensionPath: string;
  /** `process.platform`. */
  readonly platform: string;
  /** Whether `path` is a file that exists. */
  readonly isFile: (path: string) => Promise<boolean>;
}

/** A server to start: the command and the arguments to run it with. */
export interface ServerFound {
  readonly found: true;
  readonly command: string;
  readonly args: readonly string[];
  readonly source: ServerSource;
}

/** No server: what was looked for, and where. */
export interface ServerMissing {
  readonly found: false;
  readonly source: ServerSource;
  readonly message: string;
}

export type ServerResolution = ServerFound | ServerMissing;

/** The bundled binary's path inside the extension folder; Windows binaries end in `.exe`. */
export function bundledBinaryPath(extensionPath: string, platform: string): string {
  const windows = platform === 'win32';
  return (windows ? win32 : posix).join(
    extensionPath,
    BUNDLED_FOLDER,
    windows ? `${SERVER_BINARY}.exe` : SERVER_BINARY,
  );
}

/**
 * Finds the language server: the `cel-lens.serverPath` setting if it is set,
 * otherwise the binary bundled in the extension. A setting that points nowhere
 * is an error, never a silent fall back to the bundled binary, so the user
 * fixes the setting.
 */
export async function resolveServer(input: ResolveInput): Promise<ServerResolution> {
  const setting = input.setting.trim();
  if (setting !== '') {
    if (await input.isFile(setting)) {
      return { found: true, command: setting, args: SERVER_ARGS, source: 'setting' };
    }
    return {
      found: false,
      source: 'setting',
      message: `The cel-lens engine failed to start: the engine file was not found: ${setting}. Fix the cel-lens.serverPath setting, or clear it to use the bundled engine.`,
    };
  }

  const bundled = bundledBinaryPath(input.extensionPath, input.platform);
  if (await input.isFile(bundled)) {
    return { found: true, command: bundled, args: SERVER_ARGS, source: 'bundled' };
  }
  return {
    found: false,
    source: 'bundled',
    message: `The cel-lens engine is missing from the extension (${bundled}). Reinstall the extension for your platform.`,
  };
}
