import { describe, it, expect, vi, afterEach } from 'vitest';
import { fetchCruxData, retryAfterMs } from './crux.js';

afterEach(() => vi.unstubAllGlobals());

const record = { record: { metrics: { largest_contentful_paint: { percentiles: { p75: 1200 } } } } };

describe('fetchCruxData', () => {
  it('retries a URL after an HTTP 429 and keeps its data', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response('{"error":{"code":429}}', { status: 429, headers: { 'Retry-After': '0' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify(record), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const data = await fetchCruxData({ apiKey: 'k', urls: ['https://a.test/'] });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(data.get('https://a.test/')?.lcpMs).toBe(1200);
  });

  it('gives up after a bounded number of 429 retries', async () => {
    const fetchMock = vi.fn(async () => new Response('', { status: 429, headers: { 'Retry-After': '0' } }));
    vi.stubGlobal('fetch', fetchMock);
    const data = await fetchCruxData({ apiKey: 'k', urls: ['https://a.test/'] });
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(data.size).toBe(0);
  });

  it('cancels the body of a 404 (no data) answer', async () => {
    const cancel = vi.fn();
    const body = new ReadableStream({ cancel });
    vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { status: 404 })));
    await fetchCruxData({ apiKey: 'k', urls: ['https://a.test/'] });
    expect(cancel).toHaveBeenCalled();
  });
});

describe('retryAfterMs', () => {
  it('reads seconds and dates, caps at 60 s, defaults to 60 s', () => {
    expect(retryAfterMs('2')).toBe(2000);
    expect(retryAfterMs('3600')).toBe(60_000);
    expect(retryAfterMs(null)).toBe(60_000);
    expect(retryAfterMs('soon')).toBe(60_000);
    expect(retryAfterMs(new Date(10_000).toUTCString(), 5_000)).toBe(5_000);
  });
});
