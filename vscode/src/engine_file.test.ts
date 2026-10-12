import { describe, expect, it } from 'vitest';

import { ENGINE_COMMAND, requireEngineFile } from './engine_file';

describe('requireEngineFile', () => {
  it('fails with a message that names the command that installs the engine and the missing path', async () => {
    const failure = requireEngineFile('/ext', () => Promise.resolve(false));

    await expect(failure).rejects.toThrow(`Run "${ENGINE_COMMAND}"`);
    await expect(failure).rejects.toThrow('/ext/bin/cel-lens-core');
    expect(ENGINE_COMMAND).toBe('npm run engine');
  });

  it('returns the path of the engine when it is there', async () => {
    await expect(requireEngineFile('/ext', () => Promise.resolve(true))).resolves.toMatch(
      /ext[\\/]bin[\\/]cel-lens-core/,
    );
  });
});
