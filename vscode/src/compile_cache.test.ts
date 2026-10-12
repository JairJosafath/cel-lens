import { execFileSync } from 'node:child_process';
import { describe, expect, it } from 'vitest';

const MAKE_TIMEOUT_MS = 60_000;

describe('the check', () => {
  it(
    'makes sets NODE_DISABLE_COMPILE_CACHE=1 for every process it starts',
    () => {
      const env = Object.fromEntries(
        Object.entries(process.env).filter(([name]) => name !== 'NODE_DISABLE_COMPILE_CACHE'),
      );
      const printed = execFileSync(
        'make',
        ['--no-print-directory', '-f', 'Makefile', '-f', '-', 'print-compile-cache'],
        {
          encoding: 'utf8',
          env,
          input: 'print-compile-cache:\n\t@printenv NODE_DISABLE_COMPILE_CACHE\n',
        },
      );
      expect(printed.trim()).toBe('1');
    },
    MAKE_TIMEOUT_MS,
  );
});
