import { spawn } from 'node:child_process';

import { describe, expect, it, vi } from 'vitest';

import { spawnEngine } from './spawn_engine';

vi.mock('node:child_process', () => ({ spawn: vi.fn(() => ({ pid: 1 })) }));

describe('spawnEngine', () => {
  it.each([
    { command: '/opt/engine', args: [] as string[] },
    { command: 'cel-lens-core', args: ['lsp', '--stdio'] },
  ])('spawns $command with its arguments and piped stdio', ({ command, args }) => {
    spawnEngine(command, args);

    expect(spawn).toHaveBeenLastCalledWith(command, args, { stdio: ['pipe', 'pipe', 'pipe'] });
  });
});
