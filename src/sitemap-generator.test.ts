import { describe, it, expect } from 'vitest';
import { generateSitemap, generateMultiSitemap } from './sitemap-generator.js';
import type { PageData } from './types.js';

function makePage(overrides: Partial<PageData> & { url: string; finalUrl: string }): PageData {
  return {
    statusCode: 200,
    indexability: { indexable: true, reasons: [] },
    isSoft404: false,
    error: undefined,
    crawledAt: '2024-03-15T10:00:00Z',
    ...overrides,
  } as PageData;
}

describe('generateSitemap', () => {
  it('generates valid XML for indexable pages', () => {
    const pages = [
      makePage({ url: 'https://example.com/', finalUrl: 'https://example.com/' }),
      makePage({ url: 'https://example.com/about', finalUrl: 'https://example.com/about' }),
    ];
    const result = generateSitemap(pages);
    expect(result.urlCount).toBe(2);
    expect(result.truncated).toBe(false);
    expect(result.xml).toContain('<?xml version="1.0" encoding="UTF-8"?>');
    expect(result.xml).toContain('<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">');
    expect(result.xml).toContain('<loc>https://example.com/</loc>');
    expect(result.xml).toContain('<loc>https://example.com/about</loc>');
    expect(result.xml).toContain('</urlset>');
  });

  it('includes lastmod from crawledAt date', () => {
    const pages = [
      makePage({
        url: 'https://example.com/',
        finalUrl: 'https://example.com/',
        crawledAt: '2024-06-15T14:30:00Z',
      }),
    ];
    const result = generateSitemap(pages);
    expect(result.xml).toContain('<lastmod>2024-06-15</lastmod>');
  });

  it('filters non-200 pages', () => {
    const pages = [
      makePage({ url: 'https://example.com/ok', finalUrl: 'https://example.com/ok', statusCode: 200 }),
      makePage({ url: 'https://example.com/redir', finalUrl: 'https://example.com/other', statusCode: 301 }),
      makePage({ url: 'https://example.com/error', finalUrl: 'https://example.com/error', statusCode: 500 }),
    ];
    const result = generateSitemap(pages);
    expect(result.urlCount).toBe(1);
    expect(result.xml).toContain('https://example.com/ok');
    expect(result.xml).not.toContain('https://example.com/redir');
    expect(result.xml).not.toContain('https://example.com/error');
  });

  it('filters non-indexable pages', () => {
    const pages = [
      makePage({ url: 'https://example.com/ok', finalUrl: 'https://example.com/ok' }),
      makePage({
        url: 'https://example.com/noindex',
        finalUrl: 'https://example.com/noindex',
        indexability: { indexable: false, reasons: ['noindex'] },
      } as any),
    ];
    const result = generateSitemap(pages);
    expect(result.urlCount).toBe(1);
    expect(result.xml).not.toContain('noindex');
  });

  it('filters soft 404 pages', () => {
    const pages = [
      makePage({ url: 'https://example.com/ok', finalUrl: 'https://example.com/ok' }),
      makePage({ url: 'https://example.com/soft404', finalUrl: 'https://example.com/soft404', isSoft404: true } as any),
    ];
    const result = generateSitemap(pages);
    expect(result.urlCount).toBe(1);
  });

  it('filters pages with errors', () => {
    const pages = [
      makePage({ url: 'https://example.com/ok', finalUrl: 'https://example.com/ok' }),
      makePage({ url: 'https://example.com/err', finalUrl: 'https://example.com/err', error: 'timeout' } as any),
    ];
    const result = generateSitemap(pages);
    expect(result.urlCount).toBe(1);
  });

  it('escapes XML special characters in URLs', () => {
    const pages = [
      makePage({
        url: 'https://example.com/page?a=1&b=2',
        finalUrl: 'https://example.com/page?a=1&b=2',
      }),
    ];
    const result = generateSitemap(pages);
    expect(result.xml).toContain('&amp;');
    expect(result.xml).not.toMatch(/<loc>[^<]*[^;]&[^a]/); // No unescaped &
  });

  it('truncates at 50,000 URLs', () => {
    // Create 50,001 eligible pages
    const pages = Array.from({ length: 50001 }, (_, i) =>
      makePage({ url: `https://example.com/page-${i}`, finalUrl: `https://example.com/page-${i}` })
    );
    const result = generateSitemap(pages);
    expect(result.urlCount).toBe(50000);
    expect(result.truncated).toBe(true);
    expect(result.totalEligible).toBe(50001);
  });

  it('handles empty page array', () => {
    const result = generateSitemap([]);
    expect(result.urlCount).toBe(0);
    expect(result.truncated).toBe(false);
    expect(result.xml).toContain('<urlset');
    expect(result.xml).toContain('</urlset>');
  });

  it('uses finalUrl for location', () => {
    const pages = [
      makePage({
        url: 'https://example.com/old',
        finalUrl: 'https://example.com/new',
      }),
    ];
    const result = generateSitemap(pages);
    expect(result.xml).toContain('https://example.com/new');
  });

  it('omits lastmod when crawledAt is missing', () => {
    const pages = [
      makePage({ url: 'https://example.com/', finalUrl: 'https://example.com/', crawledAt: '' }),
    ];
    const result = generateSitemap(pages);
    expect(result.xml).not.toContain('<lastmod>');
  });

  it('filters non-self-canonical pages', () => {
    const pages = [
      makePage({ url: 'https://example.com/ok', finalUrl: 'https://example.com/ok', canonical: 'https://example.com/ok' } as any),
      makePage({ url: 'https://example.com/dup', finalUrl: 'https://example.com/dup', canonical: 'https://example.com/ok' } as any),
      makePage({ url: 'https://example.com/nocanon', finalUrl: 'https://example.com/nocanon', canonical: null } as any),
    ];
    const result = generateSitemap(pages);
    expect(result.urlCount).toBe(2);
    expect(result.xml).toContain('https://example.com/ok');
    expect(result.xml).toContain('https://example.com/nocanon');
    expect(result.xml).not.toContain('https://example.com/dup');
  });

  it('includes changefreq and priority when provided', () => {
    const pages = [
      makePage({ url: 'https://example.com/', finalUrl: 'https://example.com/' }),
    ];
    const result = generateSitemap(pages, { changefreq: 'weekly', priority: '0.8' });
    expect(result.xml).toContain('<changefreq>weekly</changefreq>');
    expect(result.xml).toContain('<priority>0.8</priority>');
  });

  it('omits changefreq and priority when not provided', () => {
    const pages = [
      makePage({ url: 'https://example.com/', finalUrl: 'https://example.com/' }),
    ];
    const result = generateSitemap(pages);
    expect(result.xml).not.toContain('<changefreq>');
    expect(result.xml).not.toContain('<priority>');
  });
});

describe('generateMultiSitemap', () => {
  it('returns single sitemap when under 50K URLs', () => {
    const pages = [
      makePage({ url: 'https://example.com/a', finalUrl: 'https://example.com/a' }),
      makePage({ url: 'https://example.com/b', finalUrl: 'https://example.com/b' }),
    ];
    const result = generateMultiSitemap(pages, 'https://example.com', 'sitemap');
    expect(result.sitemaps.length).toBe(1);
    expect(result.index).toBeNull();
    expect(result.totalEligible).toBe(2);
  });

  it('returns sitemap index when over 50K URLs', () => {
    const pages = Array.from({ length: 50001 }, (_, i) =>
      makePage({ url: `https://example.com/page-${i}`, finalUrl: `https://example.com/page-${i}` })
    );
    const result = generateMultiSitemap(pages, 'https://example.com', 'sitemap');
    expect(result.sitemaps.length).toBe(2);
    expect(result.sitemaps[0].urlCount).toBe(50000);
    expect(result.sitemaps[1].urlCount).toBe(1);
    expect(result.index).not.toBeNull();
    expect(result.index).toContain('<sitemapindex');
    expect(result.index).toContain('https://example.com/sitemap.xml');
    expect(result.index).toContain('https://example.com/sitemap-2.xml');
  });
});
