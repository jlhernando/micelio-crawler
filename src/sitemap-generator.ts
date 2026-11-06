import type { PageData } from './types.js';

/**
 * Escape special XML characters to prevent injection.
 */
function xmlEscape(str: string): string {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

const MAX_URLS_PER_SITEMAP = 50000;

export interface SitemapOptions {
  changefreq?: string;
  priority?: string;
}

export interface SitemapResult {
  xml: string;
  urlCount: number;
  truncated: boolean;
  totalEligible: number;
}

export interface MultiSitemapResult {
  sitemaps: { xml: string; urlCount: number }[];
  index: string | null;
  totalEligible: number;
}

/**
 * Filter pages to only those eligible for sitemap inclusion:
 * 200 OK, indexable, not soft 404, no error, self-canonicalizing.
 */
function filterEligible(pages: PageData[]): PageData[] {
  return pages.filter((p) =>
    p.statusCode === 200 &&
    p.indexability?.indexable &&
    !p.isSoft404 &&
    !p.error &&
    // Self-canonicalizing: no canonical set, or canonical points to self
    (!p.canonical || p.canonical === p.url || p.canonical === p.finalUrl)
  );
}

/**
 * <lastmod> date (YYYY-MM-DD): when the page content last changed, from the
 * server's Last-Modified header or the source sitemap's lastmod. Never the
 * crawl time: that would mark every page as changed on every crawl, and
 * search engines learn to ignore a lastmod that lies. Omitted when unknown.
 */
function lastmodOf(page: PageData): string | null {
  for (const value of [page.lastModified, page.sitemapData?.sitemapLastmod]) {
    if (!value) continue;
    const at = Date.parse(value);
    if (!Number.isNaN(at) && at <= Date.now() + 86_400_000) return new Date(at).toISOString().split('T')[0];
  }
  return null;
}

function buildUrlEntry(page: PageData, opts: SitemapOptions): string {
  const loc = `  <url>\n    <loc>${xmlEscape(page.finalUrl)}</loc>`;
  const date = lastmodOf(page);
  const lastmod = date ? `\n    <lastmod>${date}</lastmod>` : '';
  const changefreq = opts.changefreq
    ? `\n    <changefreq>${xmlEscape(opts.changefreq)}</changefreq>`
    : '';
  const priority = opts.priority
    ? `\n    <priority>${xmlEscape(opts.priority)}</priority>`
    : '';
  return `${loc}${lastmod}${changefreq}${priority}\n  </url>`;
}

function buildSitemapXml(entries: string[]): string {
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
    ...entries,
    '</urlset>',
    '',
  ].join('\n');
}

/**
 * Generate a single XML sitemap. Truncates at 50,000 URLs.
 */
export function generateSitemap(pages: PageData[], opts: SitemapOptions = {}): SitemapResult {
  const eligiblePages = filterEligible(pages);
  const truncated = eligiblePages.length > MAX_URLS_PER_SITEMAP;
  const sitemapPages = eligiblePages.slice(0, MAX_URLS_PER_SITEMAP);
  const urls = sitemapPages.map((p) => buildUrlEntry(p, opts));
  const xml = buildSitemapXml(urls);
  return { xml, urlCount: sitemapPages.length, truncated, totalEligible: eligiblePages.length };
}

/**
 * Generate multiple sitemaps split at 50K boundary, plus a sitemap index.
 * Returns null index if all URLs fit in one sitemap.
 */
export function generateMultiSitemap(
  pages: PageData[],
  baseUrl: string,
  sitemapBaseName: string,
  opts: SitemapOptions = {},
): MultiSitemapResult {
  const eligiblePages = filterEligible(pages);

  if (eligiblePages.length <= MAX_URLS_PER_SITEMAP) {
    const urls = eligiblePages.map((p) => buildUrlEntry(p, opts));
    return {
      sitemaps: [{ xml: buildSitemapXml(urls), urlCount: eligiblePages.length }],
      index: null,
      totalEligible: eligiblePages.length,
    };
  }

  // Split into chunks of 50K
  const sitemaps: { xml: string; urlCount: number }[] = [];
  for (let i = 0; i < eligiblePages.length; i += MAX_URLS_PER_SITEMAP) {
    const chunk = eligiblePages.slice(i, i + MAX_URLS_PER_SITEMAP);
    const urls = chunk.map((p) => buildUrlEntry(p, opts));
    sitemaps.push({ xml: buildSitemapXml(urls), urlCount: chunk.length });
  }

  // Generate sitemap index
  const today = new Date().toISOString().split('T')[0];
  const indexEntries = sitemaps.map((_, i) => {
    const suffix = i === 0 ? '' : `-${i + 1}`;
    const sitemapUrl = `${baseUrl}/${sitemapBaseName}${suffix}.xml`;
    return `  <sitemap>\n    <loc>${xmlEscape(sitemapUrl)}</loc>\n    <lastmod>${today}</lastmod>\n  </sitemap>`;
  });

  const index = [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
    ...indexEntries,
    '</sitemapindex>',
    '',
  ].join('\n');

  return { sitemaps, index, totalEligible: eligiblePages.length };
}
