/** The public page where people download cel-lens; engine releases are tagged `core-vX.Y.Z`, the extension's `vscode-vX.Y.Z`. */
export const RELEASES_PAGE_URL = 'https://github.com/JairJosafath/cel-lens/releases';

/** The environment variable that makes the build fetch the engine from another releases page, such as the factory CI's own. */
export const CORE_RELEASES_ENV = 'CEL_LENS_CORE_RELEASES_URL';

/**
 * The folder that serves the files of one engine release (`version` is `v0.15.0`). By default
 * the public release tagged `core-v0.15.0`; with the variable set, `<override>/download/v0.15.0`.
 */
export function releaseDownloadUrl(
  version: string,
  env: Readonly<Record<string, string | undefined>> = process.env,
): string {
  const override = env[CORE_RELEASES_ENV];
  if (override !== undefined && override !== '') {
    return `${override.replace(/\/+$/, '')}/download/${version}`;
  }
  return `${RELEASES_PAGE_URL}/download/core-${version}`;
}
