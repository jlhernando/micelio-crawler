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
import { checkExternalUrl } from './fetcher.js';
import { computeEmbeddings } from './embeddings.js';

// Post-crawl phases that cost money or time: a cancelled crawl must skip them
vi.mock('./fetcher.js', async (orig) => ({
  ...(await orig<typeof import('./fetcher.js')>()),
  checkExternalUrl: vi.fn(async () => ({ statusCode: 200 })),
}));
vi.mock('./embeddings.js', async (orig) => ({
  ...(await orig<typeof import('./embeddings.js')>()),
  computeEmbeddings: vi.fn(async () => ({
    pagesEmbedded: 0, similarPairs: [], cannibalizationGroups: [],
    provider: 'openai', model: 'm', dimensions: 0, vectors: new Map(),
  })),
}));

let server: Server;
let base = '';
let dir = '';

beforeAll(async () => {
  setAllowPrivateNetworks(true);
  server = createServer((req, res) => {
    // Slow enough that a crawl can be paused or cancelled mid-way
    setTimeout(() => {
      res.writeHead(200, { 'content-type': 'text/html' });
      res.end(`<html><head><title>${req.url}</title></head><body><p>page ${req.url} with enough body text for the embeddings phase to pick it up</p><a href="https://external.example/x">out</a></body></html>`);
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

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(checkExternalUrl).mockClear();
  vi.mocked(computeEmbeddings).mockClear();
});

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

  const postCrawl = { checkExternal: true, embeddings: true, aiProvider: 'openai', aiKey: 'k' };

  it('runs external checks and embeddings after a full crawl', async () => {
    await crawl(listConfig(3, postCrawl));
    expect(checkExternalUrl).toHaveBeenCalled();
    expect(computeEmbeddings).toHaveBeenCalled();
  });

  it('skips external checks and embeddings once cancelled', async () => {
    const ac = new AbortController();
    const dbPath = join(dir, `cancel-${Math.random().toString(36).slice(2)}.db`);
    const result = await crawl(listConfig(30, { ...postCrawl, dbPath }), (p) => {
      if (p.completed >= 3) ac.abort();
    }, { signal: ac.signal });
    expect(result.pages.length).toBeGreaterThanOrEqual(3);
    expect(result.pages.length).toBeLessThan(30);
    expect(checkExternalUrl).not.toHaveBeenCalled();
    expect(computeEmbeddings).not.toHaveBeenCalled();
    expect(result.embeddingStats).toBeNull();
    // Store closed and not marked complete
    const store = new CrawlStore(dbPath);
    expect(store.getCrawlStatus()).not.toBe('complete');
    store.close();
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
