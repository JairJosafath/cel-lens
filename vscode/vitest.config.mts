import { fileURLToPath } from 'node:url';

import { defineConfig } from 'vitest/config';

export default defineConfig({
  resolve: {
    alias: {
      // Unit tests run in plain Node, where the vscode module doesn't exist:
      // they get the stand-in in test/vscode.ts instead.
      vscode: fileURLToPath(new URL('test/vscode.ts', import.meta.url)),
    },
  },
  test: {
    // Several files spawn processes (vsce, make, git); fewer workers keep them
    // from starving each other on a busy runner, and the longer timeout is for
    // that load, not for slow code.
    maxWorkers: 4,
    testTimeout: 60_000,
    include: ['src/**/*.test.ts'],
    // Fails the run when any test leaves a folder behind in TMPDIR.
    globalSetup: ['src/tmpdir_guard.global.ts'],
  },
});
