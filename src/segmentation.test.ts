import { describe, it, expect } from 'vitest';
import { parseSegment, assignSegments, buildSegmentStats } from './segmentation.js';
import type { PageData } from './types.js';

const page = (url: string, segments: string[]): PageData => ({
  url, statusCode: 200, responseTimeMs: 10, wordCount: 100, internalLinks: [], externalLinks: [],
  indexability: { indexable: true }, segments,
} as unknown as PageData);

describe('segments with the same name', () => {
  const rules = [parseSegment('shop:/shop/'), parseSegment('shop:/store/'), parseSegment('blog:/blog/')];

  it('a page matching two rules of one name is in that segment once', () => {
    expect(assignSegments('https://a.test/shop/store/x', rules)).toEqual(['shop']);
  });

  it('stats count each page once and list each segment once', () => {
    const pages = ['https://a.test/shop/1', 'https://a.test/store/2', 'https://a.test/shop/store/3', 'https://a.test/blog/4']
      .map((u) => page(u, assignSegments(u, rules)));
    // An older crawl may have saved a repeated name: still one count
    pages.push(page('https://a.test/shop/5', ['shop', 'shop']));
    const stats = buildSegmentStats(pages, rules);
    expect(stats.map((s) => s.name)).toEqual(['shop', 'blog']);
    expect(stats.find((s) => s.name === 'shop')?.pageCount).toBe(4);
  });

  it('an empty name is rejected', () => {
    expect(() => parseSegment('  :/x/')).toThrow(/name/i);
  });
});
