import { describe, it, expect, vi, beforeAll, afterAll, afterEach } from 'vitest';
import { createServer, type Server } from 'node:http';
import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import type { AddressInfo } from 'node:net';
import { crawl, DB_RELOAD_LIMITS, dbReloadBudgetBytes } from './orchestrator.js';
import { getHeapStatistics } from 'node:v8';
import { mergeConfig } from './config.js';
import { CrawlStore } from './db-store.js';
import { setAllowPrivateNetworks, setDnsLookupForTests } from './net-guard.js';
import type { PageData } from './types.js';
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
      if (req.url === '/sitemap.xml') {
        res.writeHead(200, { 'content-type': 'application/xml' });
        res.end(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>${base}/s1</loc></url><url><loc>${base}/s2</loc></url></urlset>`);
        return;
      }
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
  it('in DB mode keeps no page in memory during the crawl and reads them back once', async () => {
    const inserted: PageData[] = [];
    const insert = CrawlStore.prototype.insertPage;
    vi.spyOn(CrawlStore.prototype, 'insertPage').mockImplementation(function (this: CrawlStore, page: PageData) {
      inserted.push(page);
      insert.call(this, page);
    });
    const load = vi.spyOn(CrawlStore.prototype, 'getAllPages');
    const dbPath = join(dir, 'fresh.db');
    const result = await crawl(listConfig(5, { dbPath }));
    expect(inserted).toHaveLength(5);
    expect(load).toHaveBeenCalledTimes(1);
    expect(result.pages.map((p) => p.url).sort()).toEqual(inserted.map((p) => p.url).sort());
    // The returned pages come from the DB, not from objects kept since the crawl
    for (const page of result.pages) expect(inserted).not.toContain(page);
    const store = new CrawlStore(dbPath);
    expect(store.getPageCount()).toBe(5);
    store.close();
  });

  it('on resume returns the earlier pages plus the new ones', async () => {
    const dbPath = join(dir, `resume-${Math.random().toString(36).slice(2)}.db`);
    await crawl(listConfig(3, { dbPath }));
    const store = new CrawlStore(dbPath);
    store.enqueue(`${base}/later`, 0);
    store.close();
    const result = await crawl(listConfig(3, { dbPath, resume: true }));
    expect(result.pages.map((p) => p.url)).toEqual([`${base}/p0`, `${base}/p1`, `${base}/p2`, `${base}/later`]);
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

  it('sitemap mode works without a seedUrl (dashboard config)', async () => {
    const result = await crawl(mergeConfig({
      mode: 'sitemap',
      seedUrl: '',
      sitemapUrls: [`${base}/sitemap.xml`],
      concurrency: 1,
      delayMs: 0,
      outputPath: join(dir, `sm-${Math.random().toString(36).slice(2)}.jsonl`),
    }));
    expect(result.pages.map((p) => p.url).sort()).toEqual([`${base}/s1`, `${base}/s2`]);
  });

  it('sitemap mode without any sitemap URL rejects with a clear error', async () => {
    await expect(crawl(mergeConfig({ mode: 'sitemap', seedUrl: '', sitemapUrls: [] })))
      .rejects.toThrow(/at least one sitemap URL/);
  });

  it('stops and rejects when a page cannot be saved to the DB', async () => {
    const insert = CrawlStore.prototype.insertPage;
    let calls = 0;
    vi.spyOn(CrawlStore.prototype, 'insertPage').mockImplementation(function (this: CrawlStore, page: PageData) {
      if (++calls === 2) throw new Error('SQLITE_FULL: database or disk is full');
      insert.call(this, page);
    });
    const progress: number[] = [];
    const dbPath = join(dir, `dbfail-${Math.random().toString(36).slice(2)}.db`);
    await expect(crawl(listConfig(5, { dbPath }), (p) => progress.push(p.completed)))
      .rejects.toThrow(/cannot save pages to the database.*SQLITE_FULL/);
    // The crawl stopped early (only pages already in flight finished) and
    // the page the DB refused was never counted as crawled
    const store = new CrawlStore(dbPath);
    const stored = store.getPageCount();
    expect(store.getPage(`${base}/p1`)).toBeNull();
    store.close();
    expect(stored).toBeLessThan(4);
    expect(Math.max(0, ...progress)).toBe(stored);
  });

  it('rejects instead of running out of memory when the DB is too big to read back', async () => {
    const saved = DB_RELOAD_LIMITS.maxJsonBytes;
    DB_RELOAD_LIMITS.maxJsonBytes = 100;
    const load = vi.spyOn(CrawlStore.prototype, 'getAllPages');
    try {
      const dbPath = join(dir, `big-${Math.random().toString(36).slice(2)}.db`);
      await expect(crawl(listConfig(3, { dbPath }))).rejects.toThrow(/too large to analyse in memory: 3 pages/);
      expect(load).not.toHaveBeenCalled();
      const store = new CrawlStore(dbPath);
      expect(store.getPageCount()).toBe(3);
      store.close();
    } finally {
      DB_RELOAD_LIMITS.maxJsonBytes = saved;
    }
  });

  it('sizes the reload budget from the heap limit, not the current (garbage-inflated) heap use', () => {
    const limit = getHeapStatistics().heap_size_limit;
    const garbage = Array.from({ length: 200_000 }, (_, i) => ({ i, s: `x${i}` }));
    const budget = dbReloadBudgetBytes();
    expect(garbage.length).toBe(200_000);
    expect(budget).toBe(Math.floor((limit * DB_RELOAD_LIMITS.heapShare) / DB_RELOAD_LIMITS.parsedPerJsonByte));
  });

  it('rejects instead of crashing when the output file cannot be written', async () => {
    const cfg = listConfig(3, { outputPath: join(dir, 'missing', 'sub', 'out.jsonl') });
    await expect(crawl(cfg)).rejects.toThrow(/ENOENT/);
  });
});

describe('crawl credentials', () => {
  // One server for every name; fake DNS points the test names at it
  let site: Server;
  let port = 0;
  const seen: { host: string; path: string; cookie?: string; sig?: string }[] = [];

  beforeAll(async () => {
    site = createServer((req, res) => {
      const host = (req.headers.host || '').split(':')[0];
      const path = req.url || '/';
      seen.push({ host, path, cookie: req.headers.cookie, sig: req.headers['x-signature'] as string | undefined });
      const redirect = ({ 'acme.test': 'shop.acme.test', 'apex.test': 'www.apex.test', 'seed.test': 'other.test' } as Record<string, string>)[host];
      if (path === '/' && redirect) {
        res.writeHead(302, { Location: `http://${redirect}:${port}/` });
        res.end();
      } else if (path === '/robots.txt' || path.includes('sitemap')) {
        res.writeHead(404);
        res.end();
      } else {
        res.writeHead(200, { 'content-type': 'text/html' });
        res.end(`<html><head><title>${host}${path}</title></head><body><a href="/next">next</a></body></html>`);
      }
    });
    await new Promise<void>((r) => site.listen(0, '127.0.0.1', r));
    port = (site.address() as AddressInfo).port;
    setDnsLookupForTests((hostname, _opts, cb) => {
      if (hostname.endsWith('.test')) return cb(null, [{ address: '127.0.0.1', family: 4 }]);
      cb(Object.assign(new Error('ENOTFOUND'), { code: 'ENOTFOUND' }), []);
    });
  });

  afterAll(async () => {
    setDnsLookupForTests(null);
    await new Promise((r) => site.close(r));
  });

  const spider = (seed: string) => mergeConfig({
    mode: 'spider', seedUrl: `http://${seed}:${port}/`, maxDepth: 2, maxPages: 10, concurrency: 1, delayMs: 0,
    cookies: 'sid=1', customHeaders: { 'X-Signature': 'sig' },
    outputPath: join(dir, `cred-${Math.random().toString(36).slice(2)}.jsonl`),
  });
  const pageHits = (host: string) => seen.filter((s) => s.host === host && s.path !== '/robots.txt' && !s.path.includes('sitemap'));

  it('a seed redirect to a subdomain or another site never gets the crawl credentials', async () => {
    for (const [seed, target] of [['acme.test', 'shop.acme.test'], ['seed.test', 'other.test']]) {
      seen.length = 0;
      const result = await crawl(spider(seed));
      expect(pageHits(seed)[0].cookie).toBe('sid=1');
      const hits = pageHits(target);
      expect(hits.map((h) => h.path)).toEqual(['/', '/next']); // the crawl moved to the new host
      for (const h of hits) {
        expect(h.cookie, `${target}${h.path}`).toBeUndefined();
        expect(h.sig, `${target}${h.path}`).toBeUndefined();
      }
      expect(result.pages.length).toBeGreaterThan(0);
    }
  });

  it('a seed redirect from apex to www keeps the credentials', async () => {
    seen.length = 0;
    await crawl(spider('apex.test'));
    const hits = pageHits('www.apex.test');
    expect(hits.map((h) => h.path)).toEqual(['/', '/next']);
    for (const h of hits) {
      expect(h.cookie).toBe('sid=1');
      expect(h.sig).toBe('sig');
    }
  });
});
