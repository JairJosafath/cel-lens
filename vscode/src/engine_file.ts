import { arch, platform } from 'node:os';

import { bundledBinaryPath } from './server_path';

/** The command that puts the pinned engine in `bin/`. */
export const ENGINE_COMMAND = 'npm run engine';

/** The engine bundled in `extensionPath`, or an error that names the command that puts it there. */
export async function requireEngineFile(
  extensionPath: string,
  isFile: (path: string) => Promise<boolean>,
): Promise<string> {
  const file = bundledBinaryPath(extensionPath, platform());
  if (!(await isFile(file))) {
    throw new Error(
      `The engine for ${platform()}-${arch()} is missing: ${file} does not exist. Run "${ENGINE_COMMAND}" to put it there.`,
    );
  }
  return file;
}
