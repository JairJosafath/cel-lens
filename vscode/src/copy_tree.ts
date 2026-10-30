import { spawnSync } from 'node:child_process';
import { cpSync, mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';

export type LinkTree = (from: string, to: string) => boolean;

export function hardLinkTree(from: string, to: string): boolean {
  return spawnSync('cp', ['-al', from, to]).status === 0;
}

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

// Copies root (minus what isAbsent names) into a new public-layout-* folder under the first of
// parents that works, with node_modules hard-linked: the same files without the minutes of
// copying, and unlike symbolic links npm list accepts the tree. Hard links need the repository's
// file system, so when no parent allows them, node_modules is copied into the first parent that
// has room. A folder is removed on any throw.
export function copyTree(
  root: string,
  parents: readonly string[],
  isAbsent: (relativePath: string) => boolean,
  link: LinkTree = hardLinkTree,
): string {
  const modules = join(root, 'node_modules');
  const errors: string[] = [];
  const make = (parent: string): string | undefined => {
    let folder: string;
    try {
      folder = mkdtempSync(join(parent, 'public-layout-'));
    } catch (error) {
      errors.push(describeError(error));
      return undefined;
    }
    try {
      cpSync(root, folder, {
        recursive: true,
        filter: (source) => !isAbsent(source.slice(root.length + 1).replaceAll('\\', '/')),
      });
    } catch (error) {
      rmSync(folder, { recursive: true, force: true });
      throw error;
    }
    return folder;
  };

  let fallback: string | undefined;
  for (const parent of parents) {
    const folder = make(parent);
    if (folder === undefined) {
      continue;
    }
    try {
      if (link(modules, join(folder, 'node_modules'))) {
        return folder;
      }
    } catch (error) {
      rmSync(folder, { recursive: true, force: true });
      throw error;
    }
    rmSync(folder, { recursive: true, force: true });
    fallback ??= parent;
  }
  if (fallback === undefined) {
    throw new Error(`No folder to copy the repository into: ${errors.join('; ')}`);
  }
  const folder = make(fallback);
  if (folder === undefined) {
    throw new Error(`No folder to copy the repository into: ${errors.join('; ')}`);
  }
  try {
    cpSync(modules, join(folder, 'node_modules'), { recursive: true });
  } catch (error) {
    rmSync(folder, { recursive: true, force: true });
    throw error;
  }
  return folder;
}
