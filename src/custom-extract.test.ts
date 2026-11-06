import { describe, it, expect } from 'vitest';
import { runCustomSearches, parseSearchRule, REGEX_TIME_LIMIT_MS } from './custom-extract.js';

describe('runCustomSearches', () => {
  it('finds plain text and regex matches', () => {
    const rules = [parseSearchRule('promo'), parseSearchRule('/price:\\s*\\d+/')];
    expect(runCustomSearches('<p>promo price: 10</p>', rules)).toEqual({ promo: true, 'price:\\s*\\d+': true });
    expect(runCustomSearches('<p>nothing</p>', rules)).toEqual({ promo: false, 'price:\\s*\\d+': false });
  });

  it('a catastrophic-backtracking regex is stopped instead of freezing the process', () => {
    const rule = parseSearchRule('/(a+)+$/');
    const evil = 'a'.repeat(40) + 'b'; // minutes of backtracking without a limit
    const start = Date.now();
    expect(runCustomSearches(evil, [rule])).toEqual({ [rule.name]: false });
    expect(Date.now() - start).toBeLessThan(REGEX_TIME_LIMIT_MS + 2000);
    // The rule is not run again on later pages of the crawl
    const again = Date.now();
    runCustomSearches(evil, [rule]);
    expect(Date.now() - again).toBeLessThan(50);
  });
});
