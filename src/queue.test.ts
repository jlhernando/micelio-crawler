import { describe, it, expect } from 'vitest';
import { CrawlQueue } from './queue.js';

describe('CrawlQueue', () => {
  it('enqueues and dequeues URLs', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    expect(queue.enqueue('https://example.com/a', 0)).toBe(true);
    expect(queue.enqueue('https://example.com/b', 0)).toBe(true);
    expect(queue.size).toBe(2);

    const first = queue.dequeue();
    expect(first?.url).toBe('https://example.com/a');
    expect(queue.size).toBe(1);
  });

  it('deduplicates URLs', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    expect(queue.enqueue('https://example.com/a', 0)).toBe(true);
    expect(queue.enqueue('https://example.com/a', 0)).toBe(false);
    expect(queue.size).toBe(1);
  });

  it('normalizes URLs for deduplication', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    expect(queue.enqueue('https://example.com/page/', 0)).toBe(true);
    // Trailing slash stripped → same normalized URL
    expect(queue.enqueue('https://example.com/page', 0)).toBe(false);
    expect(queue.size).toBe(1);
  });

  it('preserves original URL for fetching (normalization only for dedup)', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    // Enqueue URL with trailing slash
    queue.enqueue('https://example.com/blog/', 0);
    const entry = queue.dequeue();
    // The dequeued URL must be the original (with trailing slash), not the normalized one
    expect(entry?.url).toBe('https://example.com/blog/');

    // But the normalized variant is still detected as visited (dedup works)
    expect(queue.has('https://example.com/blog')).toBe(true);
    expect(queue.has('https://example.com/blog/')).toBe(true);
  });

  it('preserves original URL with query params and fragments stripped', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    // Fragment is stripped by normalizeUrl, but the original URL is kept as-is
    queue.enqueue('https://example.com/page?a=1&b=2', 0);
    const entry = queue.dequeue();
    expect(entry?.url).toBe('https://example.com/page?a=1&b=2');
  });

  it('enforces max depth', () => {
    const queue = new CrawlQueue('https://example.com', 2, 100);
    expect(queue.enqueue('https://example.com/a', 1)).toBe(true);
    expect(queue.enqueue('https://example.com/b', 2)).toBe(true);
    expect(queue.enqueue('https://example.com/c', 3)).toBe(false); // exceeds maxDepth
  });

  it('enforces max pages', () => {
    const queue = new CrawlQueue('https://example.com', 10, 2);
    expect(queue.enqueue('https://example.com/a', 0)).toBe(true);
    expect(queue.enqueue('https://example.com/b', 0)).toBe(true);
    expect(queue.enqueue('https://example.com/c', 0)).toBe(false); // exceeds maxPages
  });

  it('enforces internal URL only (default)', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    expect(queue.enqueue('https://example.com/page', 0)).toBe(true);
    expect(queue.enqueue('https://other.com/page', 0)).toBe(false);
  });

  it('allows external URLs when enforceInternal is false', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100, {
      enforceInternal: false,
    });
    expect(queue.enqueue('https://other.com/page', 0)).toBe(true);
  });

  it('supports allowed domains', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100, {
      allowedDomains: ['blog.example.com'],
    });
    expect(queue.enqueue('https://example.com/page', 0)).toBe(true);
    expect(queue.enqueue('https://blog.example.com/post', 0)).toBe(true);
    expect(queue.enqueue('https://other.com/page', 0)).toBe(false);
  });

  it('applies include patterns', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100, {
      includePatterns: [/\/blog\//],
    });
    expect(queue.enqueue('https://example.com/blog/post', 0)).toBe(true);
    expect(queue.enqueue('https://example.com/about', 0)).toBe(false);
  });

  it('applies exclude patterns', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100, {
      excludePatterns: [/\/admin/],
    });
    expect(queue.enqueue('https://example.com/page', 0)).toBe(true);
    expect(queue.enqueue('https://example.com/admin/dashboard', 0)).toBe(false);
  });

  it('applies both include and exclude patterns together', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100, {
      includePatterns: [/\/blog\//],
      excludePatterns: [/\/blog\/draft/],
    });
    expect(queue.enqueue('https://example.com/blog/post', 0)).toBe(true);
    expect(queue.enqueue('https://example.com/blog/draft-1', 0)).toBe(false);
    expect(queue.enqueue('https://example.com/about', 0)).toBe(false);
  });

  it('tracks visited URLs with has()', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    expect(queue.has('https://example.com/a')).toBe(false);
    queue.enqueue('https://example.com/a', 0);
    expect(queue.has('https://example.com/a')).toBe(true);
  });

  it('supports markVisited()', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    queue.markVisited('https://example.com/visited');
    expect(queue.has('https://example.com/visited')).toBe(true);
    expect(queue.enqueue('https://example.com/visited', 0)).toBe(false);
  });

  it('tracks totalSeen count', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    queue.enqueue('https://example.com/a', 0);
    queue.enqueue('https://example.com/b', 0);
    queue.markVisited('https://example.com/c');
    expect(queue.totalSeen).toBe(3);
  });

  it('returns undefined when dequeuing empty queue', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    expect(queue.dequeue()).toBeUndefined();
  });

  it('updates seed domain', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    expect(queue.getSeedDomain()).toBe('example.com');
    queue.updateSeedDomain('www.example.com');
    expect(queue.getSeedDomain()).toBe('www.example.com');
    // After update, the new domain is considered internal
    expect(queue.enqueue('https://www.example.com/page', 0)).toBe(true);
  });

  it('stores referrer information', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    queue.enqueue('https://example.com/a', 1, 'https://example.com/');
    const entry = queue.dequeue();
    expect(entry?.referrer).toBe('https://example.com/');
    expect(entry?.depth).toBe(1);
  });

  it('rejects non-HTTP URLs', () => {
    const queue = new CrawlQueue('https://example.com', 3, 100);
    expect(queue.enqueue('ftp://example.com/file', 0)).toBe(false);
    expect(queue.enqueue('mailto:user@example.com', 0)).toBe(false);
  });
});
