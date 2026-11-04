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
