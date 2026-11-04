import { describe, it, expect, vi, beforeAll, afterAll, afterEach } from 'vitest';
import { createServer, type Server } from 'node:http';
import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import type { AddressInfo } from 'node:net';
import { crawl } from './orchestrator.js';
import { mergeConfig } from './config.js';
import { CrawlStore } from './db-store.js';
import { setAllowPrivateNetworks } from './net-guard.js';

let server: Server;
let base = '';
let dir = '';

beforeAll(async () => {
  setAllowPrivateNetworks(true);
  server = createServer((req, res) => {
    // Slow enough that a crawl can be paused or cancelled mid-way
    setTimeout(() => {
      res.writeHead(200, { 'content-type': 'text/html' });
      res.end(`<html><head><title>${req.url}</title></head><body>page</body></html>`);
    }, 30);
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  dir = mkdtempSync(join(tmpdir(), 'micelio-orch-'));
});

afterAll(async () => {
  setAllowPrivateNetworks(false);
  await new Promise((r) => server.close(r));
  rmSync(dir, { recursive: true, force: true });
});

afterEach(() => vi.restoreAllMocks());

const listConfig = (n: number, extra: Record<string, unknown> = {}) => mergeConfig({
  mode: 'list',
  urls: Array.from({ length: n }, (_, i) => `${base}/p${i}`),
  concurrency: 1,
  delayMs: 0,
  outputPath: join(dir, `out-${Math.random().toString(36).slice(2)}.jsonl`),
  ...extra,
});

describe('crawl', () => {
  it('in DB mode returns the pages without reloading a second copy from the DB', async () => {
    const spy = vi.spyOn(CrawlStore.prototype, 'getAllPages');
    const dbPath = join(dir, 'fresh.db');
    const result = await crawl(listConfig(5, { dbPath }));
    expect(result.pages).toHaveLength(5);
    expect(spy).not.toHaveBeenCalled();
    const store = new CrawlStore(dbPath);
    expect(store.getPageCount()).toBe(5);
    store.close();
  });

  it('stops starting pages once the signal is aborted', async () => {
    const ac = new AbortController();
    const result = await crawl(listConfig(30), (p) => {
      if (p.completed >= 2) ac.abort();
    }, { signal: ac.signal });
    expect(result.pages.length).toBeGreaterThanOrEqual(2);
    expect(result.pages.length).toBeLessThan(30);
  });

  it('starts no page while paused', async () => {
    const ac = new AbortController();
    let paused = true;
    const crawled: string[] = [];
    setTimeout(() => ac.abort(), 400);
    await crawl(listConfig(5), (p) => { if (p.lastPage) crawled.push(p.lastPage.url); }, {
      signal: ac.signal,
      isPaused: () => paused,
    });
    expect(crawled).toHaveLength(0);
    paused = false;
  });

  it('rejects instead of crashing when the output file cannot be written', async () => {
    const cfg = listConfig(3, { outputPath: join(dir, 'missing', 'sub', 'out.jsonl') });
    await expect(crawl(cfg)).rejects.toThrow(/ENOENT/);
  });
});
