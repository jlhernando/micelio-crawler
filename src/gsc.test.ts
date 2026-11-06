import { describe, it, expect } from 'vitest';
import { mergeGscData } from './gsc.js';
import { bqDateRange, estimateQueryCost } from './gsc-bq.js';
import type { GscData } from './types.js';

const g = (clicks: number): GscData => ({ impressions: 10, clicks, ctr: 0, position: 1 });

describe('mergeGscData', () => {
  it('keeps /a%2Fb and /a/b apart', () => {
    const merged = mergeGscData(
      ['https://a.test/a%2Fb', 'https://a.test/a/b'],
      new Map([['https://a.test/a%2Fb', g(1)], ['https://a.test/a/b', g(2)]]),
    );
    expect(merged.get('https://a.test/a%2Fb')?.clicks).toBe(1);
    expect(merged.get('https://a.test/a/b')?.clicks).toBe(2);
  });

  it('matches equivalent encodings of unreserved characters', () => {
    const merged = mergeGscData(['https://a.test/%7Euser'], new Map([['https://a.test/~user', g(4)]]));
    expect(merged.get('https://a.test/%7Euser')?.clicks).toBe(4);
  });

  it('matches across http/https and www when only one GSC URL fits', () => {
    const merged = mergeGscData(['http://a.test/page'], new Map([['https://www.a.test/page', g(3)]]));
    expect(merged.get('http://a.test/page')?.clicks).toBe(3);
  });

  it('prefers the exact URL and does not guess between two variants', () => {
    const rows = new Map([['https://a.test/p', g(1)], ['https://www.a.test/p', g(2)]]);
    expect(mergeGscData(['https://www.a.test/p'], rows).get('https://www.a.test/p')?.clicks).toBe(2);
    expect(mergeGscData(['http://a.test/p'], rows).has('http://a.test/p')).toBe(false);
  });
});

describe('bqDateRange', () => {
  it('a 90-day window holds 90 dates (BETWEEN is inclusive)', () => {
    const { startStr, endStr } = bqDateRange(90, new Date('2026-03-31T12:00:00Z'));
    expect(endStr).toBe('2026-03-28');
    const dates = (Date.parse(endStr) - Date.parse(startStr)) / 86_400_000 + 1;
    expect(dates).toBe(90);
    expect(bqDateRange(1, new Date('2026-03-31T12:00:00Z'))).toEqual({ startStr: '2026-03-28', endStr: '2026-03-28' });
  });

  it('the cost estimate scales with the same day count', () => {
    expect(estimateQueryCost(1000, 90).estimatedMB).toBe(Math.round(1000 * 90 * 10 * 200 / (1024 * 1024)));
  });
});
