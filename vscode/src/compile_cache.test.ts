import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { factoryTitle, HAS_FACTORY_WORKFLOWS } from './factory_files';

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

  it.skipIf(!HAS_FACTORY_WORKFLOWS)(
    factoryTitle('the CI check step runs with NODE_DISABLE_COMPILE_CACHE=1'),
    () => {
      const workflow = readFileSync('.gitea/workflows/check.yaml', 'utf8');
      const step = workflow.slice(workflow.indexOf('- name: make check'));
      expect(step).toMatch(/env:\n(\s+#.*\n)*\s+NODE_DISABLE_COMPILE_CACHE: "?1"?\n/);
    },
  );

  it.skipIf(!HAS_FACTORY_WORKFLOWS)(factoryTitle('keeps npm cache in /tmp/npm-cache'), () => {
    const workflow = readFileSync('.gitea/workflows/check.yaml', 'utf8');
    expect(workflow).toContain('npm_config_cache: /tmp/npm-cache');
  });
});
