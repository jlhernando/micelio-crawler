import { describe, it, expect } from 'vitest';
import { analyzeUrlStructure, buildUrlStructureStats } from './url-structure.js';
import type { PageData, UrlStructureData } from './types.js';

describe('analyzeUrlStructure', () => {
  it('decomposes a simple HTTPS URL', () => {
    const result = analyzeUrlStructure('https://example.com/blog/my-post');
    expect(result.scheme).toBe('https');
    expect(result.hostname).toBe('example.com');
    expect(result.port).toBe('443');
    expect(result.pathDepth).toBe(2);
    expect(result.pathSegments).toEqual(['blog', 'my-post']);
    expect(result.lastSegment).toBe('my-post');
    expect(result.parameterCount).toBe(0);
    expect(result.hasFragment).toBe(false);
    expect(result.hasTrailingSlash).toBe(false);
    expect(result.fileExtension).toBe('');
  });

  it('decomposes HTTP URL with default port', () => {
    const result = analyzeUrlStructure('http://example.com/page');
    expect(result.scheme).toBe('http');
    expect(result.port).toBe('80');
  });

  it('detects custom port', () => {
    const result = analyzeUrlStructure('https://example.com:8080/path');
    expect(result.port).toBe('8080');
  });

  it('detects trailing slash', () => {
    const result = analyzeUrlStructure('https://example.com/blog/');
    expect(result.hasTrailingSlash).toBe(true);
  });

  it('does not flag root path trailing slash', () => {
    const result = analyzeUrlStructure('https://example.com/');
    expect(result.hasTrailingSlash).toBe(false);
  });

  it('extracts query parameters', () => {
    const result = analyzeUrlStructure('https://example.com/search?q=test&page=2');
    expect(result.queryParams).toEqual({ q: 'test', page: '2' });
    expect(result.parameterCount).toBe(2);
  });

  it('deduplicates query parameter keys (keeps first)', () => {
    const result = analyzeUrlStructure('https://example.com/page?a=1&a=2');
    expect(result.queryParams.a).toBe('1');
    expect(result.parameterCount).toBe(1);
  });

  it('detects fragments', () => {
    const result = analyzeUrlStructure('https://example.com/page#section');
    expect(result.hasFragment).toBe(true);
  });

  it('does not flag empty hash as fragment', () => {
    const result = analyzeUrlStructure('https://example.com/page#');
    expect(result.hasFragment).toBe(false);
  });

  it('detects common file extensions', () => {
    expect(analyzeUrlStructure('https://example.com/page.html').fileExtension).toBe('html');
    expect(analyzeUrlStructure('https://example.com/page.php').fileExtension).toBe('php');
    expect(analyzeUrlStructure('https://example.com/doc.pdf').fileExtension).toBe('pdf');
  });

  it('ignores non-web file extensions', () => {
    expect(analyzeUrlStructure('https://example.com/image.png').fileExtension).toBe('');
    expect(analyzeUrlStructure('https://example.com/file.zip').fileExtension).toBe('');
  });

  it('handles root URL', () => {
    const result = analyzeUrlStructure('https://example.com/');
    expect(result.pathDepth).toBe(0);
    expect(result.pathSegments).toEqual([]);
    expect(result.lastSegment).toBe('');
  });

  it('returns empty structure for invalid URL', () => {
    const result = analyzeUrlStructure('not-a-url');
    expect(result.scheme).toBe('');
    expect(result.hostname).toBe('');
    expect(result.pathDepth).toBe(0);
  });
});

describe('buildUrlStructureStats', () => {
  function makePage(url: string, urlStructure: UrlStructureData): Partial<PageData> {
    return { url, urlStructure } as Partial<PageData>;
  }

  it('returns null for empty pages', () => {
    expect(buildUrlStructureStats([])).toBeNull();
  });

  it('returns null when no pages have URL structure data', () => {
    const pages = [{ url: 'https://example.com' }] as PageData[];
    expect(buildUrlStructureStats(pages)).toBeNull();
  });

  it('computes depth distribution', () => {
    const pages = [
      makePage('https://example.com/', analyzeUrlStructure('https://example.com/')),
      makePage('https://example.com/a', analyzeUrlStructure('https://example.com/a')),
      makePage('https://example.com/a/b', analyzeUrlStructure('https://example.com/a/b')),
      makePage('https://example.com/a/b/c', analyzeUrlStructure('https://example.com/a/b/c')),
    ] as PageData[];

    const stats = buildUrlStructureStats(pages)!;
    expect(stats.totalUrls).toBe(4);
    expect(stats.depthDistribution[0]).toBe(1);
    expect(stats.depthDistribution[1]).toBe(1);
    expect(stats.depthDistribution[2]).toBe(1);
    expect(stats.depthDistribution[3]).toBe(1);
    expect(stats.maxPathDepth).toBe(3);
    expect(stats.avgPathDepth).toBe(1.5);
  });

  it('computes top directories', () => {
    const pages = [
      makePage('https://example.com/blog/a', analyzeUrlStructure('https://example.com/blog/a')),
      makePage('https://example.com/blog/b', analyzeUrlStructure('https://example.com/blog/b')),
      makePage('https://example.com/shop/c', analyzeUrlStructure('https://example.com/shop/c')),
    ] as PageData[];

    const stats = buildUrlStructureStats(pages)!;
    expect(stats.topDirectories[0]).toEqual({ directory: '/blog', count: 2 });
    expect(stats.topDirectories[1]).toEqual({ directory: '/shop', count: 1 });
  });

  it('counts URLs with parameters', () => {
    const pages = [
      makePage('https://example.com/a?q=1', analyzeUrlStructure('https://example.com/a?q=1')),
      makePage('https://example.com/b', analyzeUrlStructure('https://example.com/b')),
    ] as PageData[];

    const stats = buildUrlStructureStats(pages)!;
    expect(stats.urlsWithParams).toBe(1);
    expect(stats.topParameters).toEqual([{ parameter: 'q', count: 1 }]);
  });

  it('counts trailing slashes', () => {
    const pages = [
      makePage('https://example.com/a/', analyzeUrlStructure('https://example.com/a/')),
      makePage('https://example.com/b/', analyzeUrlStructure('https://example.com/b/')),
      makePage('https://example.com/c', analyzeUrlStructure('https://example.com/c')),
    ] as PageData[];

    const stats = buildUrlStructureStats(pages)!;
    expect(stats.urlsWithTrailingSlash).toBe(2);
  });

  it('computes extension distribution', () => {
    const pages = [
      makePage('https://example.com/a.html', analyzeUrlStructure('https://example.com/a.html')),
      makePage('https://example.com/b.html', analyzeUrlStructure('https://example.com/b.html')),
      makePage('https://example.com/c.php', analyzeUrlStructure('https://example.com/c.php')),
      makePage('https://example.com/d', analyzeUrlStructure('https://example.com/d')),
    ] as PageData[];

    const stats = buildUrlStructureStats(pages)!;
    expect(stats.extensionDistribution.find(e => e.extension === 'html')?.count).toBe(2);
    expect(stats.extensionDistribution.find(e => e.extension === 'php')?.count).toBe(1);
    expect(stats.extensionDistribution.find(e => e.extension === '(none)')?.count).toBe(1);
  });
});
