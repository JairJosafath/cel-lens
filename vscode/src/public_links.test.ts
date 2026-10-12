import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

const FACTORY = /gitea|c4factory/i;
const PUBLIC_REPOSITORY = 'github.com/JairJosafath/cel-lens';
const OUTSIDE = /^(\.gitea|\.devcontainer)\//;
// Protected files that hold the factory's own configuration or rules, not what users read.
const FACTORY_FILES = new Set([
  '.c4factory.yaml',
  '.prettierignore',
  'AGENTS.md',
  'package-lock.json',
]);
// Tests that check the factory's workflows must name them.
const CHECKS_WORKFLOWS = /_workflow\.test\.ts$|compile_cache\.test\.ts$|public_links\.test\.ts$/;

describe('public links', () => {
  const tracked = execFileSync('git', ['ls-files'], { encoding: 'utf8' })
    .split('\n')
    .filter(Boolean);

  it('names the factory in no file a user or contributor reads', () => {
    const offenders = tracked
      .filter(
        (file) =>
          !OUTSIDE.test(file) &&
          !FACTORY_FILES.has(file) &&
          !file.startsWith('docs/agents/') &&
          !CHECKS_WORKFLOWS.test(file),
      )
      .filter((file) => FACTORY.test(readFileSync(file, 'utf8')));

    expect(offenders).toEqual([]);
  });

  it.each([
    ['repository', 'https://github.com/JairJosafath/cel-lens'],
    ['bugs', 'https://github.com/JairJosafath/cel-lens/issues'],
  ])('points package.json %s at the public repository', (field, url) => {
    const manifest = JSON.parse(readFileSync('package.json', 'utf8')) as Record<string, unknown>;
    const value = manifest[field];
    const actual =
      typeof value === 'object' && value !== null && 'url' in value ? value.url : value;

    expect(actual).toBe(url);
    expect(manifest['repository']).toMatchObject({ directory: 'vscode' });
    expect(manifest['homepage']).toBe(`https://${PUBLIC_REPOSITORY}/tree/main/vscode#readme`);
  });
});
