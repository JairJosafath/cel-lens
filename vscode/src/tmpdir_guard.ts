import { mkdtemp, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

/** Node's own module compile cache, which Vitest fills in TMPDIR; not a test's leak. */
const NODE_COMPILE_CACHE = 'node-compile-cache';

/** Points TMPDIR at a fresh folder and returns the check that nothing was left in it. */
export async function guardTmpdir(): Promise<() => Promise<void>> {
  const original = process.env['TMPDIR'];
  const root = await mkdtemp(join(tmpdir(), 'cel-lens-leaks-'));
  process.env['TMPDIR'] = root;
  return async () => {
    if (original === undefined) {
      delete process.env['TMPDIR'];
    } else {
      process.env['TMPDIR'] = original;
    }
    const leaked = (await readdir(root)).filter((name) => name !== NODE_COMPILE_CACHE);
    await rm(root, { recursive: true, force: true });
    if (leaked.length > 0) {
      throw new Error(`The tests left files or folders in TMPDIR: ${leaked.join(', ')}`);
    }
  };
}
