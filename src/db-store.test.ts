import { describe, it, expect } from 'vitest';
import { mkdtempSync, rmSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { CrawlStore } from './db-store.js';
import { mergeConfig } from './config.js';

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
