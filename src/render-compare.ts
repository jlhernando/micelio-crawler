/**
 * Rendered vs Original HTML Comparison.
 *
 * Compares SEO-critical elements between the initial HTTP response HTML
 * and the Playwright-rendered HTML after JS execution.
 *
 * Critical for JS-heavy sites to understand what Googlebot sees vs
 * what developers see in the source code.
 */

import * as cheerio from 'cheerio';
import type { RenderDiff } from './types.js';

/**
 * Compare pre-render and post-render HTML for SEO-critical differences.
 * Returns list of field-level differences.
 */
export function compareRender(rawHtml: string, renderedHtml: string): RenderDiff[] {
  const diffs: RenderDiff[] = [];

  const raw = cheerio.load(rawHtml);
  const rendered = cheerio.load(renderedHtml);

  // 1. Title
  const rawTitle = raw('title').first().text().trim();
  const renderedTitle = rendered('title').first().text().trim();
  if (rawTitle !== renderedTitle) {
    diffs.push({ field: 'title', original: rawTitle, rendered: renderedTitle });
  }

  // 2. Meta description
  const rawDesc = raw('meta[name="description"]').attr('content')?.trim() || '';
  const renderedDesc = rendered('meta[name="description"]').attr('content')?.trim() || '';
  if (rawDesc !== renderedDesc) {
    diffs.push({ field: 'meta_description', original: rawDesc, rendered: renderedDesc });
  }

  // 3. Canonical URL
  const rawCanonical = raw('link[rel="canonical"]').attr('href')?.trim() || '';
  const renderedCanonical = rendered('link[rel="canonical"]').attr('href')?.trim() || '';
  if (rawCanonical !== renderedCanonical) {
    diffs.push({ field: 'canonical', original: rawCanonical, rendered: renderedCanonical });
  }

  // 4. Meta robots
  const rawRobots = raw('meta[name="robots"]').attr('content')?.trim() || '';
  const renderedRobots = rendered('meta[name="robots"]').attr('content')?.trim() || '';
  if (rawRobots !== renderedRobots) {
    diffs.push({ field: 'meta_robots', original: rawRobots, rendered: renderedRobots });
  }

  // 5. H1
  const rawH1 = raw('h1').map((_, el) => raw(el).text().trim()).get().join(' | ');
  const renderedH1 = rendered('h1').map((_, el) => rendered(el).text().trim()).get().join(' | ');
  if (rawH1 !== renderedH1) {
    diffs.push({ field: 'h1', original: rawH1, rendered: renderedH1 });
  }

  // 6. Internal links count
  // Use simplified heuristic: relative paths (/...) or paths without a protocol scheme
  const rawLinks = new Set<string>();
  raw('a[href]').each((_, el) => {
    const href = raw(el).attr('href');
    if (href && isLikelyInternalHref(href)) {
      rawLinks.add(href);
    }
  });
  const renderedLinks = new Set<string>();
  rendered('a[href]').each((_, el) => {
    const href = rendered(el).attr('href');
    if (href && isLikelyInternalHref(href)) {
      renderedLinks.add(href);
    }
  });
  if (rawLinks.size !== renderedLinks.size) {
    diffs.push({
      field: 'internal_links_count',
      original: String(rawLinks.size),
      rendered: String(renderedLinks.size),
    });
  }

  // 7. Word count comparison
  // Threshold: only report differences > 20% to avoid noise from minor whitespace changes
  const WORD_COUNT_DIFF_THRESHOLD = 0.2;
  const rawWordCount = countWords(raw);
  const renderedWordCount = countWords(rendered);
  if (rawWordCount > 0 && renderedWordCount > 0) {
    const ratio = Math.abs(renderedWordCount - rawWordCount) / Math.max(rawWordCount, 1);
    if (ratio > WORD_COUNT_DIFF_THRESHOLD) {
      diffs.push({
        field: 'word_count',
        original: String(rawWordCount),
        rendered: String(renderedWordCount),
      });
    }
  } else if (rawWordCount === 0 && renderedWordCount > 50) {
    // Content was entirely JS-rendered (SPA, client-side rendering)
    diffs.push({
      field: 'word_count',
      original: '0',
      rendered: String(renderedWordCount),
    });
  } else if (rawWordCount > 50 && renderedWordCount === 0) {
    // JS execution destroyed all visible content (broken JS, redirect, paywall)
    diffs.push({
      field: 'word_count',
      original: String(rawWordCount),
      rendered: '0',
    });
  }

  // 8. Structured data (JSON-LD)
  const rawJsonLd = raw('script[type="application/ld+json"]').length;
  const renderedJsonLd = rendered('script[type="application/ld+json"]').length;
  if (rawJsonLd !== renderedJsonLd) {
    diffs.push({
      field: 'json_ld_count',
      original: String(rawJsonLd),
      rendered: String(renderedJsonLd),
    });
  }

  return diffs;
}

/**
 * Detect hrefs that are likely internal (relative paths, not special protocols).
 * Excludes mailto:, tel:, javascript:, data:, #hash-only, and absolute external URLs.
 */
function isLikelyInternalHref(href: string): boolean {
  if (href.startsWith('/') && !href.startsWith('//')) return true; // absolute path
  if (href.startsWith('#')) return false; // fragment-only
  if (href.includes(':')) return false; // has protocol (http:, mailto:, tel:, javascript:, data:)
  return true; // relative path (e.g., "about", "../page")
}

/**
 * Count words in the visible body text (excluding scripts, styles, nav, footer).
 */
function countWords($: cheerio.CheerioAPI): number {
  const body = $('body').clone();
  body.find('script, style, nav, footer, header, noscript').remove();
  const text = body.text().replace(/\s+/g, ' ').trim();
  if (!text) return 0;
  return text.split(/\s+/).length;
}
