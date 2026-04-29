/**
 * URL Segmentation — group URLs by regex patterns for per-segment analysis.
 *
 * Usage: --segment "blog:/blog/.*" --segment "products:/products/.*"
 * Format: name:pattern (pattern is matched against the full URL)
 */

import type { PageData, CrawlStats } from './types.js';

export interface Segment {
  name: string;
  pattern: RegExp;
}

export interface SegmentStats {
  name: string;
  pageCount: number;
  statusCodes: Record<number, number>;
  avgResponseTimeMs: number;
  indexable: number;
  nonIndexable: number;
  avgWordCount: number;
  totalInternalLinks: number;
  totalExternalLinks: number;
  pagesWithErrors: number;
  // GSC data if available
  totalImpressions: number;
  totalClicks: number;
  // GA4 data if available
  totalSessions: number;
  totalPageviews: number;
}

/**
 * Parse a segment rule string in "name:pattern" format.
 * The pattern part is treated as a regex.
 */
export function parseSegment(value: string): Segment {
  const colonIdx = value.indexOf(':');
  if (colonIdx === -1 || colonIdx === 0) {
    throw new Error(
      `Invalid segment format "${value}". Expected "name:pattern" (e.g., "blog:/blog/.*")`
    );
  }

  const name = value.substring(0, colonIdx).trim();
  const patternStr = value.substring(colonIdx + 1).trim();

  if (!patternStr) {
    throw new Error(`Empty pattern in segment "${name}"`);
  }

  try {
    const pattern = new RegExp(patternStr);
    return { name, pattern };
  } catch (err) {
    throw new Error(
      `Invalid regex "${patternStr}" in segment "${name}": ${err instanceof Error ? err.message : err}`
    );
  }
}

/**
 * Determine which segments a URL belongs to.
 * A URL can match multiple segments. Returns list of segment names.
 */
export function assignSegments(url: string, segments: Segment[]): string[] {
  const matched: string[] = [];
  for (const seg of segments) {
    if (seg.pattern.test(url)) {
      matched.push(seg.name);
    }
  }
  return matched;
}

/**
 * Build per-segment statistics from crawled pages.
 */
export function buildSegmentStats(pages: PageData[], segments: Segment[]): SegmentStats[] {
  if (segments.length === 0) return [];

  const statsMap = new Map<string, SegmentStats>();

  // Initialize stats for each segment
  for (const seg of segments) {
    statsMap.set(seg.name, {
      name: seg.name,
      pageCount: 0,
      statusCodes: {},
      avgResponseTimeMs: 0,
      indexable: 0,
      nonIndexable: 0,
      avgWordCount: 0,
      totalInternalLinks: 0,
      totalExternalLinks: 0,
      pagesWithErrors: 0,
      totalImpressions: 0,
      totalClicks: 0,
      totalSessions: 0,
      totalPageviews: 0,
    });
  }

  // Accumulators for averaging
  const responseTimes = new Map<string, number[]>();
  const wordCounts = new Map<string, number[]>();
  for (const seg of segments) {
    responseTimes.set(seg.name, []);
    wordCounts.set(seg.name, []);
  }

  // Process pages
  for (const page of pages) {
    const matchedSegments = page.segments || [];

    for (const segName of matchedSegments) {
      const stats = statsMap.get(segName);
      if (!stats) continue;

      stats.pageCount++;
      stats.statusCodes[page.statusCode] = (stats.statusCodes[page.statusCode] || 0) + 1;

      if (page.responseTimeMs > 0) {
        responseTimes.get(segName)!.push(page.responseTimeMs);
      }

      if (page.indexability?.indexable) {
        stats.indexable++;
      } else {
        stats.nonIndexable++;
      }

      if (page.wordCount > 0) {
        wordCounts.get(segName)!.push(page.wordCount);
      }

      stats.totalInternalLinks += page.internalLinks.length;
      stats.totalExternalLinks += page.externalLinks.length;

      if (page.error || page.statusCode >= 400) {
        stats.pagesWithErrors++;
      }

      // GSC data
      if (page.gscData) {
        stats.totalImpressions += page.gscData.impressions;
        stats.totalClicks += page.gscData.clicks;
      }

      // GA4 data
      if (page.ga4Data) {
        stats.totalSessions += page.ga4Data.sessions;
        stats.totalPageviews += page.ga4Data.pageviews;
      }
    }
  }

  // Compute averages
  for (const seg of segments) {
    const stats = statsMap.get(seg.name)!;
    const times = responseTimes.get(seg.name)!;
    const words = wordCounts.get(seg.name)!;

    stats.avgResponseTimeMs = times.length > 0
      ? Math.round(times.reduce((a, b) => a + b, 0) / times.length)
      : 0;

    stats.avgWordCount = words.length > 0
      ? Math.round(words.reduce((a, b) => a + b, 0) / words.length)
      : 0;
  }

  // Return only segments that have at least one page
  return segments
    .map(seg => statsMap.get(seg.name)!)
    .filter(s => s.pageCount > 0);
}
