import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
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

const { UiCrawlManager, statsPathFor } = await import('./ui-crawl-manager.js');
const { CrawlStore } = await import('./db-store.js');
const { createApiHandler } = await import('./ui-api.js');

type Job = { id: string; status: string; dbPath: string; config: Record<string, unknown> };

function fakeStore(jobs: Record<string, Job>) {
  return {
    getCrawlJob: (id: string) => jobs[id] ?? null,
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
    const results = restarted.getResults('a');
    expect(results?.stats).toEqual({ totalPages: 1 });
    expect((results?.pages as PageData[]).map((p) => p.url)).toEqual(['https://a.test/']);
  });

  it('returns null for an old job without saved stats', () => {
    const dbPath = join(dir, 'old.db');
    writeFileSync(dbPath, '');
    const mgr = new UiCrawlManager(fakeStore({ old: { id: 'old', status: 'completed', dbPath, config: {} } }));
    expect(mgr.getResults('old')).toBeNull();
    expect(statsPathFor(dbPath)).toBe(join(dir, 'old.stats.json'));
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
