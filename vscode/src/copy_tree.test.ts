import { chmodSync, mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { copyTree } from './copy_tree';

const folders: string[] = [];

function scratch(): string {
  const folder = mkdtempSync(join(tmpdir(), 'copy-tree-test-'));
  folders.push(folder);
  return folder;
}

function makeRoot(): string {
  const root = join(scratch(), 'repo');
  mkdirSync(join(root, 'node_modules'), { recursive: true });
  writeFileSync(join(root, 'node_modules', 'pkg.js'), 'x');
  writeFileSync(join(root, 'a.txt'), 'a');
  return root;
}

function leftovers(parent: string): string[] {
  return readdirSync(parent).filter((name) => name.startsWith('public-layout-'));
}

const never = (): boolean => false;
const unlinkable = (): boolean => false;

afterEach(() => {
  for (const folder of folders.splice(0)) {
    chmodSync(folder, 0o755);
    rmSync(folder, { recursive: true, force: true });
  }
});

describe('copyTree', () => {
  it('copies node_modules into the first parent when linking fails and no other parent can be made', () => {
    const root = makeRoot();
    const tmp = scratch();
    const readOnly = scratch();
    chmodSync(readOnly, 0o555);
    const copy = copyTree(root, [tmp, readOnly], never, unlinkable);
    expect(copy.startsWith(tmp)).toBe(true);
    expect(readdirSync(join(copy, 'node_modules'))).toEqual(['pkg.js']);
    expect(leftovers(tmp)).toHaveLength(1);
  });

  it('leaves no folder behind when copying fails', () => {
    const root = makeRoot();
    rmSync(join(root, 'node_modules'), { recursive: true });
    const tmp = scratch();
    expect(() => copyTree(root, [tmp], never, unlinkable)).toThrow();
    expect(leftovers(tmp)).toEqual([]);
  });

  it('leaves no folder behind when the link step throws', () => {
    const root = makeRoot();
    const tmp = scratch();
    const boom = (): boolean => {
      throw new Error('boom');
    };
    expect(() => copyTree(root, [tmp], never, boom)).toThrow('boom');
    expect(leftovers(tmp)).toEqual([]);
  });

  it('puts the file system error in the failure message when no folder can be made', () => {
    const root = makeRoot();
    const missing = join(scratch(), 'absent');
    expect(() => copyTree(root, [missing], never, unlinkable)).toThrow(/ENOENT/);
  });
});
