import { describe, it, expect, vi, afterEach } from 'vitest';
import { fetchPlausibleData } from './plausible.js';

afterEach(() => vi.unstubAllGlobals());

describe('fetchPlausibleData path dedup', () => {
  it('sends each path once, in crawl order', async () => {
    const bodies: { filters: unknown[] }[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init: { body: string }) => {
      bodies.push(JSON.parse(init.body));
      return new Response(JSON.stringify({ results: [] }), { status: 200 });
    }));
    await fetchPlausibleData({
      siteId: 'a.test', apiKey: 'k', days: 30,
      urls: ['https://a.test/b', 'https://a.test/a', 'https://a.test/b?x=1', 'https://a.test/c'],
    });
    const paths = JSON.stringify(bodies[0].filters);
    expect(paths.indexOf('/b')).toBeLessThan(paths.indexOf('/a'));
    expect(paths.match(/"\/b"/g)).toHaveLength(1);
  });

  it('handles 100k unique paths without quadratic dedup', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ results: [] }), { status: 200 })));
    const urls = Array.from({ length: 100_000 }, (_, i) => `https://a.test/section/page-${i}`);
    const start = Date.now();
    await fetchPlausibleData({ siteId: 'a.test', apiKey: 'k', days: 30, urls });
    // Array.includes dedup took tens of seconds here
    expect(Date.now() - start).toBeLessThan(5000);
  }, 20_000);
});

const row = (path: string, visitors: number) => ({ dimensions: [path], metrics: [visitors, 1, 1, 0, 0, 0, 0] });

describe('fetchPlausibleData matching', () => {
  it('keeps /Docs and /docs apart (Plausible paths are case-sensitive)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      results: [row('/Docs', 7), row('/docs', 3)],
    }), { status: 200 })));
    const data = await fetchPlausibleData({
      siteId: 'a.test', apiKey: 'k', days: 30,
      urls: ['https://a.test/Docs', 'https://a.test/docs/'],
    });
    expect(data.get('https://a.test/Docs')?.visitors).toBe(7);
    expect(data.get('https://a.test/docs/')?.visitors).toBe(3);
  });
});

describe('fetchPlausibleData timeout', () => {
  afterEach(() => vi.useRealTimers());

  it('a request that never answers is aborted and retried, then fails', async () => {
    const fetchMock = vi.fn((_url: string, init: { signal: AbortSignal }) => new Promise<Response>((_, reject) => {
      init.signal.addEventListener('abort', () => reject(init.signal.reason));
    }));
    vi.stubGlobal('fetch', fetchMock);
    // Only the retry back-off is faked; the request deadline stays real (5 ms)
    vi.useFakeTimers({ toFake: ['setTimeout'] });
    let error: unknown = null;
    let done = false;
    fetchPlausibleData({ siteId: 'a.test', apiKey: 'k', days: 30, urls: ['https://a.test/a'], timeoutMs: 5 })
      .catch((err) => { error = err; })
      .finally(() => { done = true; });
    while (!done) {
      await new Promise((r) => setImmediate(r));
      await vi.advanceTimersByTimeAsync(30_000);
    }
    expect(fetchMock).toHaveBeenCalledTimes(4);
    expect(String(error)).toMatch(/Plausible API request failed: .*timeout/i);
  }, 10_000);
});
