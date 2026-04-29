import { describe, it, expect } from 'vitest';
import { diffPages, parseUrlMap, DIFF_FIELDS } from './diff.js';
import type { PageData } from './types.js';

function makePage(overrides: Partial<PageData> & { url: string }): PageData {
  return {
    statusCode: 200,
    title: { text: '', length: 0 },
    metaDescription: { text: '', length: 0 },
    canonical: '',
    metaRobots: '',
    headings: { h1: [], h2: [], h3: [] },
    wordCount: 0,
    indexability: { indexable: true, reasons: [] },
    redirectChain: [],
    internalLinks: [],
    externalLinks: [],
    images: [],
    anchors: [],
    structuredData: [],
    schemaValidation: [],
    openGraph: {},
    twitterCard: {},
    hreflang: [],
    responseTimeMs: 0,
    depth: 0,
    crawledAt: '',
    finalUrl: overrides.url,
    ...overrides,
  } as PageData;
}

describe('diffPages', () => {
  it('detects added URLs', () => {
    const oldPages = [makePage({ url: 'https://example.com/a' })];
    const newPages = [
      makePage({ url: 'https://example.com/a' }),
      makePage({ url: 'https://example.com/b' }),
    ];
    const result = diffPages(oldPages, newPages);
    expect(result.addedUrls).toEqual(['https://example.com/b']);
    expect(result.removedUrls).toEqual([]);
  });

  it('detects removed URLs', () => {
    const oldPages = [
      makePage({ url: 'https://example.com/a' }),
      makePage({ url: 'https://example.com/b' }),
    ];
    const newPages = [makePage({ url: 'https://example.com/a' })];
    const result = diffPages(oldPages, newPages);
    expect(result.removedUrls).toEqual(['https://example.com/b']);
    expect(result.addedUrls).toEqual([]);
  });

  it('detects changed fields', () => {
    const oldPages = [makePage({
      url: 'https://example.com/a',
      title: { text: 'Old Title', length: 9 },
    })];
    const newPages = [makePage({
      url: 'https://example.com/a',
      title: { text: 'New Title', length: 9 },
    })];
    const result = diffPages(oldPages, newPages);
    expect(result.changedUrls.length).toBe(1);
    expect(result.changedUrls[0].url).toBe('https://example.com/a');
    const titleChange = result.changedUrls[0].changes.find(c => c.field === 'title');
    expect(titleChange?.oldValue).toBe('Old Title');
    expect(titleChange?.newValue).toBe('New Title');
  });

  it('reports unchanged count', () => {
    const page = makePage({ url: 'https://example.com/a' });
    const result = diffPages([page], [page]);
    expect(result.unchangedCount).toBe(1);
    expect(result.changedUrls.length).toBe(0);
    expect(result.addedUrls.length).toBe(0);
    expect(result.removedUrls.length).toBe(0);
  });

  it('detects status code changes', () => {
    const oldPages = [makePage({ url: 'https://example.com/a', statusCode: 200 })];
    const newPages = [makePage({ url: 'https://example.com/a', statusCode: 301 })];
    const result = diffPages(oldPages, newPages);
    expect(result.changedUrls.length).toBe(1);
    const change = result.changedUrls[0].changes.find(c => c.field === 'statusCode');
    expect(change?.oldValue).toBe(200);
    expect(change?.newValue).toBe(301);
  });

  it('detects word count changes', () => {
    const oldPages = [makePage({ url: 'https://example.com/a', wordCount: 100 })];
    const newPages = [makePage({ url: 'https://example.com/a', wordCount: 250 })];
    const result = diffPages(oldPages, newPages);
    const change = result.changedUrls[0].changes.find(c => c.field === 'wordCount');
    expect(change?.oldValue).toBe(100);
    expect(change?.newValue).toBe(250);
  });

  it('respects custom field selection', () => {
    const oldPages = [makePage({
      url: 'https://example.com/a',
      title: { text: 'Old', length: 3 },
      statusCode: 200,
    })];
    const newPages = [makePage({
      url: 'https://example.com/a',
      title: { text: 'New', length: 3 },
      statusCode: 301,
    })];
    // Only check statusCode
    const result = diffPages(oldPages, newPages, { fields: ['statusCode'] });
    expect(result.changedUrls[0].changes.length).toBe(1);
    expect(result.changedUrls[0].changes[0].field).toBe('statusCode');
  });

  it('populates fieldSummary', () => {
    const oldPages = [
      makePage({ url: 'https://example.com/a', statusCode: 200 }),
      makePage({ url: 'https://example.com/b', statusCode: 200 }),
    ];
    const newPages = [
      makePage({ url: 'https://example.com/a', statusCode: 301 }),
      makePage({ url: 'https://example.com/b', statusCode: 404 }),
    ];
    const result = diffPages(oldPages, newPages);
    expect(result.fieldSummary.statusCode).toBe(2);
  });

  it('handles empty arrays', () => {
    const result = diffPages([], []);
    expect(result.oldCount).toBe(0);
    expect(result.newCount).toBe(0);
    expect(result.addedUrls).toEqual([]);
    expect(result.removedUrls).toEqual([]);
    expect(result.changedUrls).toEqual([]);
  });

  it('sorts results alphabetically', () => {
    const oldPages: PageData[] = [];
    const newPages = [
      makePage({ url: 'https://example.com/c' }),
      makePage({ url: 'https://example.com/a' }),
      makePage({ url: 'https://example.com/b' }),
    ];
    const result = diffPages(oldPages, newPages);
    expect(result.addedUrls).toEqual([
      'https://example.com/a',
      'https://example.com/b',
      'https://example.com/c',
    ]);
  });
});

describe('parseUrlMap', () => {
  it('parses sed-style mapping with / delimiter', () => {
    const mapping = parseUrlMap('s/staging/production/');
    expect(mapping.pattern.source).toBe('staging');
    expect(mapping.replacement).toBe('production');
  });

  it('parses sed-style mapping with | delimiter', () => {
    const mapping = parseUrlMap('s|old.com|new.com|');
    // Pattern is stored as raw regex (dot matches any char)
    expect(mapping.pattern.source).toBe('old.com');
    expect(mapping.replacement).toBe('new.com');
  });

  it('handles replacement with URL path characters', () => {
    const mapping = parseUrlMap('s|/old-path|/new-path|');
    expect(mapping.replacement).toBe('/new-path');
  });

  it('handles missing trailing delimiter', () => {
    const mapping = parseUrlMap('s/old/new');
    expect(mapping.pattern.source).toBe('old');
    expect(mapping.replacement).toBe('new');
  });

  it('throws on invalid mapping format', () => {
    expect(() => parseUrlMap('not-sed')).toThrow('Invalid URL mapping');
  });

  it('throws on empty pattern', () => {
    expect(() => parseUrlMap('s//new/')).toThrow('Pattern cannot be empty');
  });

  it('creates global regex', () => {
    const mapping = parseUrlMap('s/old/new/');
    expect(mapping.pattern.flags).toContain('g');
  });

  it('throws on invalid regex in pattern', () => {
    expect(() => parseUrlMap('s/foo(/bar/')).toThrow();
  });
});

describe('DIFF_FIELDS', () => {
  it('contains expected fields', () => {
    expect(DIFF_FIELDS).toContain('statusCode');
    expect(DIFF_FIELDS).toContain('title');
    expect(DIFF_FIELDS).toContain('metaDescription');
    expect(DIFF_FIELDS).toContain('canonical');
    expect(DIFF_FIELDS).toContain('metaRobots');
    expect(DIFF_FIELDS).toContain('h1');
    expect(DIFF_FIELDS).toContain('wordCount');
    expect(DIFF_FIELDS).toContain('indexable');
    expect(DIFF_FIELDS).toContain('redirectChainLength');
  });
});
