import { describe, it, expect, vi } from 'vitest';
import { mkdtempSync, rmSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { CrawlStore } from './db-store.js';
import { mergeConfig } from './config.js';
import type { PageData } from './types.js';

describe('CrawlStore.saveCrawlMeta', () => {
  it('stores the config without credentials', () => {
    const dir = mkdtempSync(join(tmpdir(), 'micelio-db-'));
    try {
      const dbPath = join(dir, 'c.db');
      const store = new CrawlStore(dbPath);
      store.saveCrawlMeta(mergeConfig({
        seedUrl: 'https://a.test/',
        psiKey: 'PSI-SECRET', aiKey: 'AI-SECRET', cruxKey: 'CRUX-SECRET', plausibleApiKey: 'PL-SECRET',
        cookies: 'session=COOKIE-SECRET', customHeaders: { Authorization: 'Bearer HDR-SECRET' },
        proxy: 'http://user:PROXY-SECRET@proxy.test:8080',
      }), 1);
      const meta = store.getCrawlMeta();
      store.close();
      expect(meta?.config.seedUrl).toBe('https://a.test/');
      expect(meta?.config.proxy).toBe('http://user:redacted@proxy.test:8080/');
      const raw = readFileSync(dbPath).toString('latin1'); // WAL is checkpointed on close
      expect(raw).not.toMatch(/SECRET/);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});

describe('CrawlStore.averagePageBytes', () => {
  const page = (url: string, pad: number) => ({ url, statusCode: 200, crawledAt: '', title: 'x'.repeat(pad) }) as unknown as PageData;

  it('uses the running total of inserted pages, without reading them back', () => {
    const dir = mkdtempSync(join(tmpdir(), 'micelio-db-'));
    try {
      const store = new CrawlStore(join(dir, 'a.db'));
      const pages = [page('https://a.test/1', 100), page('https://a.test/2', 300)];
      for (const p of pages) store.insertPage(p);
      const expected = pages.reduce((n, p) => n + JSON.stringify(p).length, 0) / pages.length;
      const prepare = vi.spyOn((store as unknown as { db: { prepare: (sql: string) => unknown } }).db, 'prepare');
      expect(store.averagePageBytes()).toBe(expected);
      expect(prepare).not.toHaveBeenCalled();
      store.close();
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it('samples the last 100 rows when this object inserted nothing (resume)', () => {
    const dir = mkdtempSync(join(tmpdir(), 'micelio-db-'));
    try {
      const dbPath = join(dir, 'b.db');
      const first = new CrawlStore(dbPath);
      for (let i = 0; i < 150; i++) first.insertPage(page(`https://a.test/${i}`, i < 50 ? 10_000 : 200));
      first.close();
      const again = new CrawlStore(dbPath);
      const avg = again.averagePageBytes();
      again.close();
      // Only the last 100 (small) pages are read
      expect(avg).toBeGreaterThan(200);
      expect(avg).toBeLessThan(400);
      expect(new CrawlStore(join(dir, 'empty.db')).averagePageBytes()).toBe(0);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
