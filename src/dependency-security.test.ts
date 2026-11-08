import { describe, it, expect } from 'vitest';
import { createRequire } from 'node:module';
import * as fs from 'node:fs';
import { mkdtemp, writeFile, mkdir, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const require = createRequire(import.meta.url);
// Exercise the versions used by the Google SDK's cleanup dependency.
const { glob } = require('glob');
const { rimraf } = require('rimraf');
const { minimatch } = require('minimatch');

describe('security-patched transitive dependencies', () => {
 it('keeps rimraf 5 glob cleanup compatible and scoped to matching files', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'micelio-cleanup-'));
  try {
   await Promise.all(['remove-a.txt', 'remove-b.txt', 'keep.txt'].map(f => writeFile(join(dir, f), 'data')));
   await rimraf(join(dir, 'remove-*.txt'), { glob: true });
   expect(await readdir(dir)).toEqual(['keep.txt']);
  } finally { await rm(dir, { recursive: true, force: true }); }
 });

 it('stops glob traversal after cancellation during the first directory read', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'micelio-glob-abort-'));
  const controller = new AbortController();
  const reason = new Error('test traversal budget');
  let reads = 0;
  const observedFs = {
   ...fs,
   readdir: (...args: any[]) => {
    reads++;
    const cb = args.pop();
    (fs.readdir as any)(...args, (...result: any[]) => {
     if (!controller.signal.aborted) controller.abort(reason);
     cb(...result);
    });
   },
  };
  try {
   await Promise.all(Array.from({ length: 8 }, async (_, i) => {
    await mkdir(join(dir, `d${i}`, 'nested'), { recursive: true });
    await writeFile(join(dir, `d${i}`, 'nested', 'file.txt'), 'data');
   }));
   await expect(glob('**/*', { cwd: dir, signal: controller.signal, fs: observedFs })).rejects.toThrow('test traversal budget');
   const atRejection = reads;
   await new Promise(resolve => setTimeout(resolve, 50));
   expect(reads).toBe(atRejection);
   expect(reads).toBe(1);
  } finally { await rm(dir, { recursive: true, force: true }); }
 });

 it('preserves glob matching behavior after the minimatch major upgrade', () => {
  expect(minimatch('src/nested/file.ts', '**/*.ts')).toBe(true);
  expect(minimatch('src/.hidden.ts', '**/*.ts')).toBe(false);
  expect(minimatch('src/.hidden.ts', '**/*.ts', { dot: true })).toBe(true);
  expect(minimatch('A/FILE.TS', '{a,b}/**/*.ts', { nocase: true })).toBe(true);
  expect(minimatch('keep.txt', 'remove-*.txt')).toBe(false);
 });
});
