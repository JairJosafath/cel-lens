import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, dirname, join } from 'node:path';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { guardTmpdir } from './tmpdir_guard';

describe('guardTmpdir', () => {
  let outer = '';
  beforeEach(async () => {
    outer = await mkdtemp(join(tmpdir(), 'guard-outer-'));
    vi.stubEnv('TMPDIR', outer);
  });
  afterEach(async () => {
    vi.unstubAllEnvs();
    await rm(outer, { recursive: true, force: true });
  });

  it('points TMPDIR at a fresh folder and restores it afterwards', async () => {
    const check = await guardTmpdir();
    expect(dirname(tmpdir())).toBe(outer);
    expect(basename(tmpdir())).toMatch(/^cel-lens-leaks-/);
    await check();
    expect(tmpdir()).toBe(outer);
  });

  it('passes when the tests clean up after themselves', async () => {
    const check = await guardTmpdir();
    const folder = await mkdtemp(join(tmpdir(), 'tidy-'));
    await rm(folder, { recursive: true });
    await expect(check()).resolves.toBeUndefined();
  });

  it("ignores Node's own compile cache", async () => {
    const check = await guardTmpdir();
    await mkdir(join(tmpdir(), 'node-compile-cache'));
    await expect(check()).resolves.toBeUndefined();
  });

  it('fails and names the folder a test left behind', async () => {
    const check = await guardTmpdir();
    await mkdir(join(tmpdir(), 'leaked-folder'));
    await expect(check()).rejects.toThrow('leaked-folder');
  });
});

it('runs every test file with TMPDIR inside the global setup folder', () => {
  expect(basename(tmpdir())).toMatch(/^cel-lens-leaks-/);
});
