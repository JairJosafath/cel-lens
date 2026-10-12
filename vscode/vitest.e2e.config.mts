import { defineConfig } from 'vitest/config';

// The end-to-end test starts the real engine, so it is not part of `npm test`.
export default defineConfig({
  test: {
    include: ['test/e2e/**/*.test.ts'],
    // Fetching the pinned engine and waiting for it to answer can be slow on a shared runner.
    testTimeout: 60_000,
    hookTimeout: 120_000,
  },
});
