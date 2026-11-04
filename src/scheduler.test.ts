import { describe, it, expect } from 'vitest';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { join, basename } from 'node:path';
import { tmpdir } from 'node:os';
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
