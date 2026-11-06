import { describe, it, expect } from 'vitest';
import { extractPageData } from './extractor.js';
import { generateReport } from './reporter.js';
import {
  computeLinkIntelligence, generateLinkSuggestions, analyzeSemanticLinkDistance, computeClosenessCentrality,
} from './link-intelligence.js';
import { buildPageUrlResolver } from './utils.js';
import type { PageData } from './types.js';

const A = 'https://a.test';
const words = Array.from({ length: 250 }, (_, i) => `word${i}`).join(' ');

/** A real PageData, built by the extractor from HTML. */
function page(url: string, opts: { finalUrl?: string; status?: number; links?: string[]; canonical?: string; html?: string } = {}): PageData {
  const links = (opts.links ?? []).map((h) => `<a href="${h}">link to ${h}</a>`).join(' ');
  const canonical = opts.canonical ? `<link rel="canonical" href="${opts.canonical}">` : '';
  const html = opts.html ?? `<html><head><title>${url}</title>${canonical}</head><body><main><p>${words}</p>${links}</main></body></html>`;
  const p = extractPageData({
    url, finalUrl: opts.finalUrl ?? url, statusCode: 200, redirectChain: [], headers: {}, html,
    contentType: 'text/html', responseTimeMs: 10, transferSize: html.length,
  }, 0, 'a.test', { linkIntelligence: true, seedUrl: `${A}/` });
  if (opts.status) p.statusCode = opts.status;
  return p;
}

describe('buildPageUrlResolver', () => {
  it('a redirect to D never takes D from the crawled page D, in any order', () => {
    const pages = [{ url: `${A}/d`, finalUrl: `${A}/d` }, { url: `${A}/old`, finalUrl: `${A}/d` }];
    for (const order of [pages, [...pages].reverse()]) {
      const r = buildPageUrlResolver(order);
      expect(r.get(`${A}/d`)).toBe(`${A}/d`);
      expect(r.get(`${A}/old`)).toBe(`${A}/old`);
      expect(r.get(`${A}/d/`)).toBe(`${A}/d`);
    }
  });
});

describe('redirect aliases (destination crawled before the redirect)', () => {
  // D first, then R (redirects to D), then S (links to D, canonical to D)
  const build = () => [
    page(`${A}/d`),
    page(`${A}/old`, { finalUrl: `${A}/d`, status: 301 }),
    page(`${A}/s`, { links: [`${A}/d`], canonical: `${A}/d` }),
  ];

  it('report: inlinks go to D, and the canonical to D is not reported as a non-200 target', () => {
    const pages = build();
    const stats = generateReport(pages, 0);
    expect(pages[0].inlinks).toBe(1);
    expect(pages[1].inlinks).toBe(0);
    expect(stats.canonicalStats?.canonicalToNon200).toEqual([]);
  });

  it('link intelligence: in-degree goes to D', () => {
    const pages = build();
    computeLinkIntelligence(pages, `${A}/d`, { noCentrality: true });
    expect(pages[0].linkIntelligence?.inDegree).toBe(1);
    expect(pages[1].linkIntelligence?.inDegree).toBe(0);
  });
});

describe('links in another URL form', () => {
  const target = () => page(`${A}/target`);

  it('no suggestion to add a link that exists as /target/', () => {
    const pages = [target(), page(`${A}/linker`, { links: [`${A}/target/`] }), page(`${A}/other`)];
    const suggestions = generateLinkSuggestions(pages, null, new Map(), new Map(), new Map());
    const pairs = suggestions.map((s) => `${s.sourceUrl} -> ${s.targetUrl}`);
    expect(pairs).not.toContain(`${A}/linker -> ${A}/target`);
    // Not vacuous: a page without the link does get the suggestion
    expect(pairs).toContain(`${A}/other -> ${A}/target`);
  });

  it('semantic link analysis resolves /target/ to the page embedding', () => {
    const pages = [target(), page(`${A}/linker`, { links: [`${A}/target/`] })];
    const embeddings = new Map([[`${A}/target`, [1, 0]], [`${A}/linker`, [1, 0]]]);
    expect(analyzeSemanticLinkDistance(pages, embeddings).totalLinks).toBe(1);
  });
});

describe('computeClosenessCentrality sampling', () => {
  it('every page of a connected graph gets a score, not only the sampled ones', () => {
    const n = 800;
    const pages = Array.from({ length: n }, (_, i) => ({
      url: `${A}/${i}`, finalUrl: `${A}/${i}`,
      internalLinks: [`${A}/${(i + 1) % n}`, `${A}/${(i + 7) % n}`],
    })) as unknown as PageData[];
    // maxNodes 100 < 800 pages: sampled mode (500 sources)
    const scores = computeClosenessCentrality(pages, 100);
    const zero = [...scores.values()].filter((s) => s === 0).length;
    expect(zero).toBe(0);
  });
});
