import { describe, it, expect } from 'vitest';
import { mkdtempSync, writeFileSync, rmSync, readFileSync, readdirSync } from 'node:fs';
import { join, basename } from 'node:path';
import { tmpdir } from 'node:os';
import { spawn } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { buildOutputFilename } from './scheduler.js';

describe('buildOutputFilename', () => {
  const now = new Date('2026-11-03T09:00:12.345Z');

  it('gives two schedules on the same host in the same minute different files', () => {
    const a = buildOutputFilename('https://a.test/blog', '/out', 'jsonl', 'a-test-111111', now);
    const b = buildOutputFilename('https://a.test/shop', '/out', 'jsonl', 'a-test-222222', now);
    expect(a).not.toBe(b);
    expect(basename(a)).toMatch(/^a-test-111111-2026-11-03T09-00-12-345-[0-9a-f]{6}\.jsonl$/);
  });

  it('gives the same schedule a new file on every call (duplicate scheduler processes)', () => {
    const names = new Set(Array.from({ length: 50 }, () =>
      buildOutputFilename('https://a.test/', '/out', 'csv', 'a-test-111111', now)));
    expect(names.size).toBe(50);
  });

  it('never returns an existing file', () => {
    const dir = mkdtempSync(join(tmpdir(), 'micelio-sched-'));
    try {
      const first = buildOutputFilename('https://a.test/', dir, 'jsonl', 'a-test-1', now);
      writeFileSync(first, 'old');
      for (let i = 0; i < 20; i++) {
        expect(buildOutputFilename('https://a.test/', dir, 'jsonl', 'a-test-1', now)).not.toBe(first);
      }
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});

describe('updateScheduleState across processes', () => {
  it('several scheduler processes updating one schedule lose no run and never crash', async () => {
    const root = join(fileURLToPath(import.meta.url), '..', '..');
    const home = mkdtempSync(join(tmpdir(), 'micelio-sched-'));
    const script = join(home, 'worker.ts');
    writeFileSync(script, `
      import { updateScheduleState } from ${JSON.stringify(pathToFileURL(join(root, 'src', 'scheduler.ts')).href)};
      const init = () => ({ id: 'race', url: 'https://a.test/', cron: '* * * * *', createdAt: '', lastRun: null,
        lastStatus: null, lastPages: 0, lastDurationMs: 0, nextRun: '', totalRuns: 0, outputDir: '.' });
      for (let i = 0; i < 25; i++) updateScheduleState('race', init, (s) => { s.totalRuns++; s.lastPages = i; });
    `);
    const tsx = join(root, 'node_modules', '.bin', 'tsx');
    try {
      const codes = await Promise.all(Array.from({ length: 4 }, () => new Promise<number | null>((done) => {
        const child = spawn(tsx, [script], { env: { ...process.env, HOME: home, USERPROFILE: home }, stdio: 'inherit' });
        child.on('exit', done);
      })));
      expect(codes).toEqual([0, 0, 0, 0]);
      const dir = join(home, '.micelio', 'schedules');
      expect(JSON.parse(readFileSync(join(dir, 'race.json'), 'utf-8')).totalRuns).toBe(100);
      // No temp or lock file is left behind
      expect(readdirSync(dir)).toEqual(['race.json']);
    } finally {
      rmSync(home, { recursive: true, force: true });
    }
  }, 30_000);
});
