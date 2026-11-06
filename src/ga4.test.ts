import { describe, it, expect, vi } from 'vitest';
import type { Ga4Data } from './types.js';

const runReport = vi.hoisted(() => ({ fn: (() => Promise.resolve([{ rows: [] }])) as (req: unknown) => Promise<unknown> }));
vi.mock('@google-analytics/data', () => ({
  BetaAnalyticsDataClient: class { runReport(req: unknown) { return runReport.fn(req); } },
}));

const { fetchGa4Data, Ga4PathMatcher } = await import('./ga4.js');

const m = (sessions: number): Ga4Data => ({
  sessions, pageviews: 0, bounceRate: 0, conversions: 0, activeUsers: 0, engagementRate: 0, avgSessionDuration: 0,
});

describe('Ga4PathMatcher', () => {
  it('keeps case-distinct paths apart', () => {
    const matcher = new Ga4PathMatcher(['https://a.test/Product', 'https://a.test/product']);
    matcher.add('/Product', m(5));
    matcher.add('/product', m(9));
    const out = matcher.results();
    expect(out.get('https://a.test/Product')?.sessions).toBe(5);
    expect(out.get('https://a.test/product')?.sessions).toBe(9);
  });

  it('falls back to a case-insensitive match only when unambiguous', () => {
    const matcher = new Ga4PathMatcher(['https://a.test/About/', 'https://a.test/Team', 'https://a.test/team']);
    matcher.add('/about', m(3)); // one crawled path folds to /about: matched
    matcher.add('/TEAM', m(4)); // two crawled paths fold to /team: ambiguous, dropped
    const out = matcher.results();
    expect(out.get('https://a.test/About/')?.sessions).toBe(3);
    expect(out.has('https://a.test/Team')).toBe(false);
    expect(out.has('https://a.test/team')).toBe(false);
  });

  it('an exact row wins over a case-insensitive one', () => {
    const matcher = new Ga4PathMatcher(['https://a.test/Docs']);
    matcher.add('/docs', m(1));
    matcher.add('/Docs', m(2));
    expect(matcher.results().get('https://a.test/Docs')?.sessions).toBe(2);
  });
});

describe('fetchGa4Data errors', () => {
  it('PERMISSION_DENIED fails at once with an access message, no retries', async () => {
    const calls = vi.fn(() => Promise.reject(Object.assign(new Error('denied'), { code: 7 })));
    runReport.fn = calls;
    const start = Date.now();
    await expect(fetchGa4Data({ propertyId: '1', days: 30, urls: ['https://a.test/'], accessToken: 't' }))
      .rejects.toThrow(/permission denied/i);
    expect(calls).toHaveBeenCalledTimes(1);
    expect(Date.now() - start).toBeLessThan(1000);
  });
});
