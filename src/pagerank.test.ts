import { describe, it, expect } from 'vitest';
import { computePageRank } from './pagerank.js';
import type { PageData } from './types.js';

function makePage(url: string, internalLinks: string[]) {
  return { url, internalLinks } as Pick<PageData, 'url' | 'internalLinks'> as PageData;
}

describe('computePageRank', () => {
  it('returns empty map for empty input', () => {
    const result = computePageRank([]);
    expect(result.size).toBe(0);
  });

  it('assigns score 10 to a single page', () => {
    const pages = [makePage('https://example.com/', [])] as PageData[];
    const result = computePageRank(pages);
    expect(result.get('https://example.com/')).toBe(10);
  });

  it('assigns higher rank to pages with more inlinks', () => {
    // Star topology: A links to B, C links to B, D links to B
    const pages = [
      makePage('https://example.com/a', ['https://example.com/b']),
      makePage('https://example.com/b', []),
      makePage('https://example.com/c', ['https://example.com/b']),
      makePage('https://example.com/d', ['https://example.com/b']),
    ] as PageData[];
    const result = computePageRank(pages);
    const rankB = result.get('https://example.com/b')!;
    const rankA = result.get('https://example.com/a')!;
    expect(rankB).toBeGreaterThan(rankA);
  });

  it('distributes rank in a linear chain', () => {
    // A → B → C
    const pages = [
      makePage('https://example.com/a', ['https://example.com/b']),
      makePage('https://example.com/b', ['https://example.com/c']),
      makePage('https://example.com/c', []),
    ] as PageData[];
    const result = computePageRank(pages);
    // C should have the highest rank (receives all link juice)
    const rankC = result.get('https://example.com/c')!;
    const rankA = result.get('https://example.com/a')!;
    expect(rankC).toBeGreaterThan(rankA);
  });

  it('handles circular links', () => {
    // A → B → C → A
    const pages = [
      makePage('https://example.com/a', ['https://example.com/b']),
      makePage('https://example.com/b', ['https://example.com/c']),
      makePage('https://example.com/c', ['https://example.com/a']),
    ] as PageData[];
    const result = computePageRank(pages);
    // All should have equal rank in a perfect cycle
    const ranks = [...result.values()];
    const avg = ranks.reduce((a, b) => a + b, 0) / ranks.length;
    for (const rank of ranks) {
      expect(Math.abs(rank - avg)).toBeLessThan(0.5);
    }
  });

  it('handles dangling nodes (no outlinks)', () => {
    const pages = [
      makePage('https://example.com/a', ['https://example.com/b']),
      makePage('https://example.com/b', []), // dangling
    ] as PageData[];
    const result = computePageRank(pages);
    expect(result.size).toBe(2);
    // Both should have valid scores
    for (const score of result.values()) {
      expect(score).toBeGreaterThanOrEqual(0);
      expect(score).toBeLessThanOrEqual(10);
    }
  });

  it('normalizes scores to 0-10 scale', () => {
    const pages = [
      makePage('https://example.com/a', ['https://example.com/b']),
      makePage('https://example.com/b', ['https://example.com/a']),
      makePage('https://example.com/c', ['https://example.com/a', 'https://example.com/b']),
    ] as PageData[];
    const result = computePageRank(pages);
    // Max score should be 10
    const maxScore = Math.max(...result.values());
    expect(maxScore).toBe(10);
    // All scores between 0-10
    for (const score of result.values()) {
      expect(score).toBeGreaterThanOrEqual(0);
      expect(score).toBeLessThanOrEqual(10);
    }
  });

  it('ignores self-links', () => {
    const pages = [
      makePage('https://example.com/a', ['https://example.com/a', 'https://example.com/b']),
      makePage('https://example.com/b', []),
    ] as PageData[];
    const result = computePageRank(pages);
    expect(result.size).toBe(2);
  });

  it('ignores links to external URLs not in the page set', () => {
    const pages = [
      makePage('https://example.com/a', ['https://example.com/b', 'https://other.com/page']),
      makePage('https://example.com/b', []),
    ] as PageData[];
    const result = computePageRank(pages);
    expect(result.size).toBe(2);
    // external link is simply ignored
    expect(result.has('https://other.com/page')).toBe(false);
  });

  it('rounds scores to 2 decimal places', () => {
    const pages = [
      makePage('https://example.com/a', ['https://example.com/b']),
      makePage('https://example.com/b', ['https://example.com/c']),
      makePage('https://example.com/c', []),
    ] as PageData[];
    const result = computePageRank(pages);
    for (const score of result.values()) {
      const rounded = Math.round(score * 100) / 100;
      expect(score).toBe(rounded);
    }
  });

  it('accepts custom damping factor', () => {
    // Use more pages so the damping effect is visible after normalization
    const pages = [
      makePage('https://example.com/a', ['https://example.com/c']),
      makePage('https://example.com/b', ['https://example.com/c']),
      makePage('https://example.com/c', []),
      makePage('https://example.com/d', []),
    ] as PageData[];
    const result1 = computePageRank(pages, 0.5);
    const result2 = computePageRank(pages, 0.99);
    // Different damping factors → different rank distributions for non-max node
    expect(result1.get('https://example.com/a')).not.toBe(result2.get('https://example.com/a'));
  });
});
