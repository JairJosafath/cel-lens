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
    include: ['src/**/*.test.ts'],
    // Fails the run when any test leaves a folder behind in TMPDIR.
    globalSetup: ['src/tmpdir_guard.global.ts'],
  },
});
