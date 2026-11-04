import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mkdtempSync, writeFileSync, rmSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { Readable } from 'node:stream';
import { setTrustedProxy } from './net-guard.js';
import type { CrawlConfig, PageData } from './types.js';
import type { CrawlControl } from './orchestrator.js';

// A fake crawl engine the test drives by hand
const engine = vi.hoisted(() => ({
  calls: [] as { config: CrawlConfig; control: CrawlControl; finish: (pages?: unknown[]) => void }[],
}));
vi.mock('./orchestrator.js', () => ({
  crawl: (config: CrawlConfig, _onProgress: unknown, control: CrawlControl) =>
    new Promise((resolve) => {
      engine.calls.push({ config, control, finish: (pages = []) => resolve({ pages, externalLinkResults: [] }) });
    }),
}));
vi.mock('./reporter.js', () => ({ generateReport: () => ({ totalPages: 1 }) }));

const { UiCrawlManager, statsPathFor, crawlFilesFor } = await import('./ui-crawl-manager.js');
const { CrawlStore } = await import('./db-store.js');
const { createApiHandler } = await import('./ui-api.js');

type Job = { id: string; status: string; dbPath: string; config: Record<string, unknown> };

function fakeStore(jobs: Record<string, Job>) {
  return {
    getCrawlJob: (id: string) => jobs[id] ?? null,
    deleteCrawlJob: (id: string) => { const had = id in jobs; delete jobs[id]; return had; },
    updateCrawlJob: (id: string, u: Partial<Job>) => { if (jobs[id]) Object.assign(jobs[id], u); },
  } as unknown as import('./ui-store.js').UiStore;
}

const tick = () => new Promise((r) => setTimeout(r, 0));

let dir: string;
beforeEach(() => {
  engine.calls.length = 0;
  dir = mkdtempSync(join(tmpdir(), 'micelio-mgr-'));
});
afterEach(() => rmSync(dir, { recursive: true, force: true }));

describe('UiCrawlManager', () => {
  it('passes the job DB path and a per-job output file to the crawl', async () => {
    const jobs = { a: { id: 'a', status: 'pending', dbPath: join(dir, 'a.db'), config: {} } };
    const mgr = new UiCrawlManager(fakeStore(jobs));
    await mgr.startCrawl('a', { seedUrl: 'https://a.test/' });
    await vi.waitFor(() => expect(engine.calls).toHaveLength(1));
    expect(engine.calls[0].config.dbPath).toBe(join(dir, 'a.db'));
    expect(engine.calls[0].config.outputPath).toBe(join(dir, 'a.jsonl'));
    engine.calls[0].finish();
  });

  it('pause and resume reach the crawl engine', async () => {
    const jobs = { a: { id: 'a', status: 'pending', dbPath: join(dir, 'a.db'), config: {} } };
    const mgr = new UiCrawlManager(fakeStore(jobs));
    await mgr.startCrawl('a', { seedUrl: 'https://a.test/' });
    await vi.waitFor(() => expect(engine.calls).toHaveLength(1));
    const { control } = engine.calls[0];
    expect(control.isPaused?.()).toBe(false);
    expect(mgr.pauseCrawl('a')).toBe(true);
    expect(control.isPaused?.()).toBe(true);
    expect(mgr.resumeCrawl('a')).toBe(true);
    expect(control.isPaused?.()).toBe(false);
    engine.calls[0].finish();
  });

  it('cancel aborts the crawl signal and keeps the crawl active until it stops', async () => {
    const jobs = { a: { id: 'a', status: 'pending', dbPath: join(dir, 'a.db'), config: {} } };
    const mgr = new UiCrawlManager(fakeStore(jobs));
    await mgr.startCrawl('a', { seedUrl: 'https://a.test/' });
    await vi.waitFor(() => expect(engine.calls).toHaveLength(1));
    expect(mgr.cancelCrawl('a')).toBe(true);
    expect(engine.calls[0].control.signal?.aborted).toBe(true);
    expect(jobs.a.status).toBe('cancelled');
    // A second crawl cannot start while the first is still stopping
    await expect(mgr.startCrawl('b', {})).rejects.toThrow(/already running/);
    engine.calls[0].finish([{ url: 'https://a.test/' }]);
    await vi.waitFor(() => expect(mgr.getActiveCrawlId()).toBeNull());
    expect(jobs.a.status).toBe('cancelled'); // not overwritten by 'completed'
  });

  it('returns false for crawls that are not active', () => {
    const mgr = new UiCrawlManager(fakeStore({}));
    expect(mgr.pauseCrawl('nope')).toBe(false);
    expect(mgr.resumeCrawl('nope')).toBe(false);
    expect(mgr.cancelCrawl('nope')).toBe(false);
  });

  it('saves stats and reloads a completed crawl after a restart', async () => {
    const dbPath = join(dir, 'a.db');
    const jobs = { a: { id: 'a', status: 'pending', dbPath, config: {} } };
    const first = new UiCrawlManager(fakeStore(jobs));
    await first.startCrawl('a', { seedUrl: 'https://a.test/' });
    await vi.waitFor(() => expect(engine.calls).toHaveLength(1));
    // The real engine writes pages to the job DB
    const crawlStore = new CrawlStore(dbPath);
    crawlStore.insertPage({ url: 'https://a.test/', statusCode: 200, crawledAt: 'now' } as PageData);
    crawlStore.close();
    engine.calls[0].finish([{ url: 'https://a.test/' }]);
    await vi.waitFor(() => expect(jobs.a.status).toBe('completed'));

    const restarted = new UiCrawlManager(fakeStore(jobs));
    const results = await restarted.getResults('a');
    expect(results?.stats).toEqual({ totalPages: 1 });
    expect((results?.pages as PageData[]).map((p) => p.url)).toEqual(['https://a.test/']);
  });

  it('returns null for an old job without saved stats', async () => {
    const dbPath = join(dir, 'old.db');
    writeFileSync(dbPath, '');
    const mgr = new UiCrawlManager(fakeStore({ old: { id: 'old', status: 'completed', dbPath, config: {} } }));
    expect(await mgr.getResults('old')).toBeNull();
    expect(statsPathFor(dbPath)).toBe(join(dir, 'old.stats.json'));
  });

  /** A completed crawl from an earlier server run, with `n` stored pages. */
  function storedCrawl(jobs: Record<string, Job>, id: string, n = 1): void {
    const dbPath = join(dir, `${id}.db`);
    const crawlStore = new CrawlStore(dbPath);
    for (let i = 0; i < n; i++) {
      crawlStore.insertPage({ url: `https://${id}.test/${i}`, statusCode: 200, crawledAt: 'now' } as PageData);
    }
    crawlStore.close();
    writeFileSync(statsPathFor(dbPath), '{}');
    jobs[id] = { id, status: 'completed', dbPath, config: {} };
  }

  it('loads a big stored crawl in batches, in order', async () => {
    const jobs: Record<string, Job> = {};
    storedCrawl(jobs, 'big', 1203);
    const mgr = new UiCrawlManager(fakeStore(jobs), { crawlsDir: dir });
    const pages = (await mgr.getResults('big'))?.pages as PageData[];
    expect(pages).toHaveLength(1203);
    expect(pages[1202].url).toBe('https://big.test/1202');
  });

  it('keeps only the most recently used results in memory', async () => {
    const jobs: Record<string, Job> = {};
    for (const id of ['a', 'b', 'c']) storedCrawl(jobs, id);
    const mgr = new UiCrawlManager(fakeStore(jobs), { crawlsDir: dir, maxCachedResults: 2 });
    await mgr.getResults('a');
    await mgr.getResults('b');
    await mgr.getResults('a'); // a is now newer than b
    await mgr.getResults('c');
    expect(mgr.cachedResultIds()).toEqual(['a', 'c']);
  });

  it('bounds the cache by total pages but keeps the newest crawl', async () => {
    const jobs: Record<string, Job> = {};
    storedCrawl(jobs, 'a', 3);
    storedCrawl(jobs, 'b', 5);
    const mgr = new UiCrawlManager(fakeStore(jobs), { crawlsDir: dir, maxCachedPages: 4 });
    await mgr.getResults('a');
    await mgr.getResults('b');
    expect(mgr.cachedResultIds()).toEqual(['b']);
  });

  it('delete frees the results and removes the crawl files', async () => {
    const jobs: Record<string, Job> = {};
    storedCrawl(jobs, 'a');
    const files = crawlFilesFor(jobs.a.dbPath);
    for (const f of files) if (!existsSync(f)) writeFileSync(f, '');
    const mgr = new UiCrawlManager(fakeStore(jobs), { crawlsDir: dir });
    await mgr.getResults('a');
    expect(mgr.deleteCrawl('a')).toBe(true);
    expect(mgr.cachedResultIds()).toEqual([]);
    expect(jobs.a).toBeUndefined();
    for (const f of files) expect(existsSync(f)).toBe(false);
  });

  it('a crawl deleted while it loads is not cached', async () => {
    const jobs: Record<string, Job> = {};
    storedCrawl(jobs, 'a', 1200);
    const mgr = new UiCrawlManager(fakeStore(jobs), { crawlsDir: dir });
    const load = mgr.getResults('a');
    mgr.deleteCrawl('a');
    await load;
    expect(mgr.cachedResultIds()).toEqual([]);
  });

  it('delete never removes files outside the crawls directory', () => {
    const outside = mkdtempSync(join(tmpdir(), 'micelio-user-'));
    try {
      const dbPath = join(outside, 'x.db');
      writeFileSync(dbPath, '');
      const jobs: Record<string, Job> = { x: { id: 'x', status: 'completed', dbPath, config: {} } };
      const mgr = new UiCrawlManager(fakeStore(jobs), { crawlsDir: dir });
      expect(mgr.deleteCrawl('x')).toBe(true);
      expect(existsSync(dbPath)).toBe(true);
    } finally {
      rmSync(outside, { recursive: true, force: true });
    }
  });

  it('refuses to delete the running crawl', async () => {
    const jobs: Record<string, Job> = { a: { id: 'a', status: 'pending', dbPath: join(dir, 'a.db'), config: {} } };
    writeFileSync(join(dir, 'a.db'), '');
    const mgr = new UiCrawlManager(fakeStore(jobs), { crawlsDir: dir });
    await mgr.startCrawl('a', { seedUrl: 'https://a.test/' });
    await vi.waitFor(() => expect(engine.calls).toHaveLength(1));
    expect(mgr.deleteCrawl('a')).toBe('running');
    expect(jobs.a).toBeDefined();
    expect(existsSync(join(dir, 'a.db'))).toBe(true);
    engine.calls[0].finish();
  });
});

describe('crawl control API', () => {
  async function call(handler: ReturnType<typeof createApiHandler>, path: string) {
    let status = 0;
    let body = '';
    const res = {
      writeHead: (s: number) => { status = s; },
      setHeader: () => {},
      end: (b: string) => { body = b; },
    };
    await handler({ url: path, method: 'POST', headers: { host: 'x' } } as never, res as never);
    return { status, body: JSON.parse(body) };
  }

  it('404s unknown crawls and 409s crawls that are not running', async () => {
    const jobs = { done: { id: 'done', status: 'completed', dbPath: join(dir, 'd.db'), config: {} } };
    const store = fakeStore(jobs);
    const handler = createApiHandler(store, new UiCrawlManager(store));
    for (const action of ['pause', 'resume', 'cancel']) {
      expect((await call(handler, `/api/crawl/nope/${action}`)).status).toBe(404);
      const r = await call(handler, `/api/crawl/done/${action}`);
      expect(r.status).toBe(409);
      expect(r.body.error.code).toBe('NOT_RUNNING');
    }
    await tick();
  });
});

describe('proxy in dashboard crawls', () => {
  afterEach(() => setTrustedProxy(''));

  async function start(handler: ReturnType<typeof createApiHandler>, config: Record<string, unknown>) {
    let status = 0;
    let body = '';
    const req = Object.assign(Readable.from([Buffer.from(JSON.stringify(config))]), {
      url: '/api/crawl/start', method: 'POST', headers: { host: 'x' },
    });
    const res = {
      writeHead: (s: number) => { status = s; },
      setHeader: () => {},
      end: (b: string) => { body = b; },
    };
    await handler(req as never, res as never);
    return { status, body: JSON.parse(body) };
  }

  function apiWithStore() {
    const jobs: Record<string, Job> = {};
    const store = {
      ...fakeStore(jobs),
      getSettings: () => ({}),
      createCrawlJob: (config: Record<string, unknown>) => {
        const job = { id: `j${Object.keys(jobs).length}`, status: 'pending', dbPath: join(dir, 'j.db'), config };
        jobs[job.id] = job;
        return job;
      },
    } as unknown as import('./ui-store.js').UiStore;
    return createApiHandler(store, new UiCrawlManager(store));
  }

  it('POST /api/crawl/start refuses a proxy that is not the trusted one (400)', async () => {
    const handler = apiWithStore();
    const r = await start(handler, { seedUrl: 'https://93.184.215.14/', proxy: 'http://10.0.0.5:3128' });
    expect(r.status).toBe(400);
    expect(r.body.error.message).toMatch(/--trusted-proxy/);
    setTrustedProxy('http://proxy.example.com:8080');
    const r2 = await start(handler, { seedUrl: 'https://93.184.215.14/', proxy: 'http://10.0.0.5:3128' });
    expect(r2.status).toBe(400);
    expect(engine.calls).toHaveLength(0);
  });

  it('POST /api/crawl/start accepts the trusted proxy and the crawl uses it', async () => {
    setTrustedProxy('http://u:pw@proxy.example.com:8080');
    const handler = apiWithStore();
    const r = await start(handler, { seedUrl: 'https://93.184.215.14/', proxy: 'http://u:pw@proxy.example.com:8080' });
    expect(r.status).toBe(200);
    await vi.waitFor(() => expect(engine.calls).toHaveLength(1));
    expect(engine.calls[0].config.proxy).toBe('http://u:pw@proxy.example.com:8080');
    engine.calls[0].finish();
  });

  it('a crawl config with an untrusted proxy never reaches the engine', async () => {
    const jobs = { a: { id: 'a', status: 'pending', dbPath: join(dir, 'a.db'), config: {} } };
    const mgr = new UiCrawlManager(fakeStore(jobs));
    await mgr.startCrawl('a', { seedUrl: 'https://a.test/', proxy: 'http://10.0.0.5:3128' });
    await vi.waitFor(() => expect(jobs.a.status).toBe('failed'));
    expect(engine.calls).toHaveLength(0);
  });
});
