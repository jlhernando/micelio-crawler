import { createHash } from 'node:crypto';
import * as cheerio from 'cheerio';
import type {
  FetchResult, PageData, HeadingData, ImageData,
  HreflangEntry, StructuredDataEntry, AnchorData, SecurityData,
  CustomExtractionRule, CustomSearchRule, ResourceEntry,
  IndexabilityData, ReadabilityData, SchemaValidationEntry,
  LinkPosition,
} from './types.js';
import { runCustomExtractions, runCustomSearches } from './custom-extract.js';
import { simhash } from './simhash.js';
import { validateStructuredData } from './schema-validator.js';
import { normalizeUrlForComparison } from './utils.js';
import { detectTemplateType, type TemplateDetectionInput } from './template-detector.js';

export interface ExtractionOptions {
  customExtractions?: CustomExtractionRule[];
  customSearches?: CustomSearchRule[];
  linkIntelligence?: boolean;
  seedUrl?: string;
}

export function extractPageData(
  fetchResult: FetchResult,
  depth: number,
  seedDomain: string,
  options: ExtractionOptions = {},
): PageData {
  if (!fetchResult.html || fetchResult.error) {
    return buildErrorPage(fetchResult, depth);
  }

  const $ = cheerio.load(fetchResult.html);

  const title = extractTitle($);
  const metaDescription = extractMetaDescription($);
  // #12: Resolve canonical to absolute URL; also count tags and store raw href
  const canonicalResult = extractCanonicalFull($, fetchResult.finalUrl);
  const canonical = canonicalResult.canonical;
  const metaRobots = extractMetaRobots($);
  const xRobotsTag = fetchResult.headers['x-robots-tag'] || null;
  const headings = extractHeadings($);
  // #13: Links are now deduplicated
  const { internalLinks, externalLinks } = extractLinks($, fetchResult.finalUrl, seedDomain);
  const images = extractImages($, fetchResult.finalUrl);

  // Phase 2: Enhanced SEO fields
  const hreflang = extractHreflang($, fetchResult.finalUrl);
  const structuredData = extractStructuredData($);
  const openGraph = extractOpenGraph($);
  const twitterCard = extractTwitterCard($);
  const bodyText = extractBodyText($);
  const wordCount = countWords(bodyText);
  const contentHash = computeContentHash(bodyText);
  const anchors = extractAnchors($, fetchResult.finalUrl, seedDomain, options.linkIntelligence);
  const security = extractSecurityData(fetchResult, $);

  // Phase 3: Custom extractions (using existing $) and searches
  const customExtractions = runCustomExtractions($, options.customExtractions || []);
  const customSearches = runCustomSearches(fetchResult.html, options.customSearches || []);

  // Phase 7: Content & Indexability
  const indexability = computeIndexability(fetchResult.statusCode, metaRobots, xRobotsTag, canonical, fetchResult.url, fetchResult.finalUrl);
  const readability = computeReadability(bodyText);
  const urlIssues = detectUrlIssues(fetchResult.finalUrl, canonical);
  const isSoft404 = detectSoft404(fetchResult.statusCode, bodyText, wordCount);
  const textToCodeRatio = fetchResult.html.length > 0
    ? Math.round((bodyText.length / fetchResult.html.length) * 10000) / 100
    : 0;
  // SimHash fingerprint stored as string because BigInt is not JSON-serializable
  const simhashFingerprint = wordCount >= 30 ? simhash(bodyText).toString() : '';

  // Phase 9: Schema validation
  const schemaValidation: SchemaValidationEntry[] = validateStructuredData(structuredData);

  // Build input for template detection (narrower than full PageData)
  const partialPage: TemplateDetectionInput = {
    url: fetchResult.url,
    finalUrl: fetchResult.finalUrl,
    depth,
    wordCount,
    internalLinks,
    externalLinks,
    images,
    headings,
    anchors,
    structuredData,
    schemaValidation,
    openGraph,
  };

  const templateType = detectTemplateType(partialPage, options.seedUrl || fetchResult.finalUrl);

  return {
    url: fetchResult.url,
    finalUrl: fetchResult.finalUrl,
    // SEO convention: show redirect status (301/302) when URL was redirected
    statusCode: fetchResult.redirectChain.length > 0
      ? fetchResult.redirectChain[0].statusCode
      : fetchResult.statusCode,
    redirectChain: fetchResult.redirectChain,
    responseTimeMs: fetchResult.responseTimeMs,
    title,
    metaDescription,
    canonical,
    canonicalCount: canonicalResult.count,
    canonicalRaw: canonicalResult.rawHref,
    metaRobots,
    xRobotsTag,
    headings,
    internalLinks,
    externalLinks,
    images,
    depth,
    crawledAt: new Date().toISOString(),
    hreflang,
    structuredData,
    openGraph,
    twitterCard,
    wordCount,
    contentHash,
    anchors,
    security,
    customExtractions,
    customSearches,
    snippetResults: {},
    pagespeed: null,
    aiAnalysis: null,
    sitemapData: null,
    pageWeight: null,
    indexability,
    readability,
    urlIssues,
    isSoft404,
    textToCodeRatio,
    simhashFingerprint,
    schemaValidation,
    gscData: null,
    ga4Data: null,
    segments: [],
    renderDiffs: null,
    linkIntelligence: null,
    cruxData: null,
    plausibleData: null,
    robotsBlocked: false,
    templateType,
    inlinks: 0,
    pageRank: 0,
  };
}

function buildErrorPage(fetchResult: FetchResult, depth: number): PageData {
  return {
    url: fetchResult.url,
    finalUrl: fetchResult.finalUrl,
    statusCode: fetchResult.statusCode,
    redirectChain: fetchResult.redirectChain,
    responseTimeMs: fetchResult.responseTimeMs,
    title: null,
    metaDescription: null,
    canonical: null,
    canonicalCount: 0,
    canonicalRaw: null,
    metaRobots: null,
    xRobotsTag: null,
    headings: { h1: [], h2: [], h3: [], h4: [], h5: [], h6: [] },
    internalLinks: [],
    externalLinks: [],
    images: [],
    depth,
    crawledAt: new Date().toISOString(),
    error: fetchResult.error,
    hreflang: [],
    structuredData: [],
    openGraph: {},
    twitterCard: {},
    wordCount: 0,
    contentHash: '',
    anchors: [],
    security: {
      isHttps: fetchResult.finalUrl.startsWith('https://'),
      hasMixedContent: false,
      mixedContentUrls: [],
      hasHsts: false,
      hasXFrameOptions: false,
      hasCsp: false,
    },
    customExtractions: {},
    customSearches: {},
    snippetResults: {},
    pagespeed: null,
    aiAnalysis: null,
    sitemapData: null,
    pageWeight: null,
    indexability: computeIndexability(fetchResult.statusCode, null, null, null, fetchResult.url, fetchResult.finalUrl),
    readability: null,
    urlIssues: detectUrlIssues(fetchResult.finalUrl, null),
    isSoft404: false,
    textToCodeRatio: 0,
    simhashFingerprint: '',
    schemaValidation: [],
    gscData: null,
    ga4Data: null,
    segments: [],
    renderDiffs: null,
    linkIntelligence: null,
    cruxData: null,
    plausibleData: null,
    robotsBlocked: false,
    templateType: 'other',
    inlinks: 0,
    pageRank: 0,
  };
}

function extractTitle($: cheerio.CheerioAPI): { text: string; length: number } | null {
  const text = $('title').first().text().trim();
  if (!text) return null;
  return { text, length: text.length };
}

function extractMetaDescription($: cheerio.CheerioAPI): { text: string; length: number } | null {
  const text = $('meta[name="description"]').attr('content')?.trim() || '';
  if (!text) return null;
  return { text, length: text.length };
}

// #12: Resolve canonical URL to absolute; count tags and store raw href (single DOM query)
function extractCanonicalFull($: cheerio.CheerioAPI, pageUrl: string): { canonical: string | null; count: number; rawHref: string | null } {
  const canonicals = $('link[rel="canonical"]');
  const count = canonicals.length;
  const rawHref = canonicals.first().attr('href')?.trim() || null;
  if (!rawHref) return { canonical: null, count, rawHref: null };
  try {
    return { canonical: new URL(rawHref, pageUrl).toString(), count, rawHref };
  } catch {
    return { canonical: rawHref, count, rawHref };
  }
}

function extractMetaRobots($: cheerio.CheerioAPI): string | null {
  return $('meta[name="robots"]').attr('content')?.trim() || null;
}

function extractHeadings($: cheerio.CheerioAPI): HeadingData {
  const headings: HeadingData = { h1: [], h2: [], h3: [], h4: [], h5: [], h6: [] };
  for (const level of ['h1', 'h2', 'h3', 'h4', 'h5', 'h6'] as const) {
    $(level).each((_, el) => {
      const text = $(el).text().trim();
      if (text) headings[level].push(text);
    });
  }
  return headings;
}

// #13: Deduplicate links using Sets
function extractLinks(
  $: cheerio.CheerioAPI,
  pageUrl: string,
  seedDomain: string,
): { internalLinks: string[]; externalLinks: string[] } {
  const internalSet = new Set<string>();
  const externalSet = new Set<string>();

  $('a[href]').each((_, el) => {
    const href = $(el).attr('href');
    if (!href) return;
    if (href.startsWith('#') || href.startsWith('mailto:') || href.startsWith('tel:') || href.startsWith('javascript:')) return;

    try {
      const absolute = new URL(href, pageUrl).toString();
      const parsed = new URL(absolute);
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return;

      if (parsed.hostname === seedDomain) {
        internalSet.add(absolute);
      } else {
        externalSet.add(absolute);
      }
    } catch {
      // skip malformed URLs
    }
  });

  return { internalLinks: [...internalSet], externalLinks: [...externalSet] };
}

function extractImages($: cheerio.CheerioAPI, pageUrl: string): ImageData[] {
  const images: ImageData[] = [];
  $('img').each((_, el) => {
    const src = $(el).attr('src');
    if (!src) return;

    let absoluteSrc: string;
    try {
      absoluteSrc = new URL(src, pageUrl).toString();
    } catch {
      absoluteSrc = src;
    }

    const hasAltAttribute = $(el).attr('alt') !== undefined;
    const alt = $(el).attr('alt') ?? null;
    const altText = alt?.trim() ?? '';
    const width = $(el).attr('width') ?? null;
    const height = $(el).attr('height') ?? null;

    images.push({
      src: absoluteSrc,
      alt,
      missingAlt: !hasAltAttribute || altText === '',
      hasAltAttribute,
      altLength: altText.length,
      altTooLong: altText.length > 100,
      missingWidth: width === null,
      missingHeight: height === null,
      width,
      height,
    });
  });
  return images;
}

// ── Phase 2: Enhanced SEO Extraction ──────────────────────────────

// 2.2 Hreflang extraction
// B3 fix: Resolve href to absolute URL at extraction time
function extractHreflang($: cheerio.CheerioAPI, pageUrl: string): HreflangEntry[] {
  const entries: HreflangEntry[] = [];
  $('link[rel="alternate"][hreflang]').each((_, el) => {
    const lang = $(el).attr('hreflang')?.trim();
    const href = $(el).attr('href')?.trim();
    if (lang && href) {
      try {
        entries.push({ lang, href: new URL(href, pageUrl).toString() });
      } catch {
        entries.push({ lang, href });
      }
    }
  });
  return entries;
}

// 2.3 Structured Data extraction (JSON-LD + Microdata)
function extractStructuredData($: cheerio.CheerioAPI): StructuredDataEntry[] {
  const entries: StructuredDataEntry[] = [];

  // JSON-LD — store full raw for validation, display truncation happens in CSV/display
  const JSONLD_MAX_RAW = 51200; // 50KB — covers virtually all JSON-LD blocks
  $('script[type="application/ld+json"]').each((_, el) => {
    const raw = $(el).html()?.trim();
    if (!raw) return;
    try {
      const parsed = JSON.parse(raw);
      const type = parsed['@type'] || (Array.isArray(parsed['@graph']) ? 'Graph' : 'Unknown');
      entries.push({ type: String(type), format: 'json-ld', raw: raw.slice(0, JSONLD_MAX_RAW) });
    } catch {
      entries.push({ type: 'ParseError', format: 'json-ld', raw: raw.slice(0, JSONLD_MAX_RAW) });
    }
  });

  // Microdata (top-level itemscope elements)
  $('[itemscope]:not([itemscope] [itemscope])').each((_, el) => {
    const itemtype = $(el).attr('itemtype') || 'Unknown';
    // Extract just the schema type name from the URL
    const typeName = itemtype.split('/').pop() || itemtype;
    entries.push({ type: typeName, format: 'microdata', raw: itemtype });
  });

  return entries;
}

// 2.4 Open Graph meta tags
function extractOpenGraph($: cheerio.CheerioAPI): Record<string, string> {
  const og: Record<string, string> = {};
  $('meta[property^="og:"]').each((_, el) => {
    const property = $(el).attr('property');
    const content = $(el).attr('content')?.trim();
    if (property && content) {
      og[property] = content;
    }
  });
  return og;
}

// 2.4 Twitter Card meta tags
// E4 fix: Also match property attribute (some sites use property instead of name)
function extractTwitterCard($: cheerio.CheerioAPI): Record<string, string> {
  const tc: Record<string, string> = {};
  $('meta[name^="twitter:"], meta[property^="twitter:"]').each((_, el) => {
    const key = $(el).attr('name') || $(el).attr('property');
    const content = $(el).attr('content')?.trim();
    if (key && content) {
      tc[key] = content;
    }
  });
  return tc;
}

// 2.5 Extract visible body text (strip scripts, styles, etc.)
// P1 fix: Clone only <body> subtree instead of re-parsing entire DOM
export function extractBodyText($: cheerio.CheerioAPI): string {
  const $body = $('body').clone();
  $body.find('script, style, noscript, svg, iframe').remove();
  return $body.text().replace(/\s+/g, ' ').trim();
}

// 2.5 Word count
function countWords(text: string): number {
  if (!text) return 0;
  return text.split(/\s+/).filter((w) => w.length > 0).length;
}

// 2.1 Content hash for duplicate detection
function computeContentHash(text: string): string {
  if (!text) return '';
  return createHash('md5').update(text).digest('hex');
}

// 2.6 Anchor text analysis
// E3 fix: Extended with common variants
const NON_DESCRIPTIVE_PATTERNS = /^(click here|here|read more|more|learn more|link|this|go|download|visit|page|click|see more|details|info|continue|find out more|view more|see details|show more|»|>>|>)$/i;

// Link Intelligence: Detect the position of a link based on its DOM context
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function detectLinkPosition($: cheerio.CheerioAPI, el: any): LinkPosition {
  let $current = $(el);
  const maxDepth = 15; // Limit ancestor traversal

  for (let depth = 0; depth < maxDepth; depth++) {
    const $parent = $current.parent();
    if (!$parent.length) break;

    const tagName = (($parent.prop('tagName') as string) || '').toLowerCase();
    const role = ($parent.attr('role') || '').toLowerCase();

    // Check semantic HTML5 elements
    if (tagName === 'main' || tagName === 'article' || role === 'main') return 'content';
    if (tagName === 'nav' || role === 'navigation') return 'navigation';
    if (tagName === 'footer' || role === 'contentinfo') return 'footer';
    if (tagName === 'aside' || role === 'complementary') return 'sidebar';
    if (tagName === 'header' || role === 'banner') return 'header';

    // Check common class/id patterns
    const classList = ($parent.attr('class') || '').toLowerCase();
    const id = ($parent.attr('id') || '').toLowerCase();
    const combined = classList + ' ' + id;

    if (/\b(nav|menu|navbar|navigation)\b/.test(combined)) return 'navigation';
    if (/\b(footer|foot|colophon)\b/.test(combined)) return 'footer';
    if (/\b(sidebar|aside|widget)\b/.test(combined)) return 'sidebar';
    if (/\b(header|masthead|top-bar|topbar)\b/.test(combined)) return 'header';
    // MINOR-1 fix: Require more specific patterns for content detection to avoid
    // matching generic wrappers like "main-wrapper" or "content-sidebar-layout"
    if (/\b(article-body|post-content|entry-content|main-content|body-content|page-content|article-content)\b/.test(combined)) return 'content';
    if (/\b(article|post|entry)\b/.test(combined) && !/\b(nav|menu|footer|sidebar|header)\b/.test(combined)) return 'content';

    $current = $parent;
  }
  return 'other';
}

function extractAnchors(
  $: cheerio.CheerioAPI,
  pageUrl: string,
  seedDomain: string,
  detectPosition = false,
): AnchorData[] {
  const anchors: AnchorData[] = [];
  $('a[href]').each((_, el) => {
    const href = $(el).attr('href');
    if (!href) return;
    if (href.startsWith('#') || href.startsWith('mailto:') || href.startsWith('tel:') || href.startsWith('javascript:')) return;

    const text = $(el).text().trim();
    let isInternal = false;
    let resolvedHref = href;

    try {
      const absolute = new URL(href, pageUrl);
      if (absolute.protocol !== 'http:' && absolute.protocol !== 'https:') return;
      resolvedHref = absolute.toString();
      isInternal = absolute.hostname === seedDomain;
    } catch {
      return;
    }

    const rel = $(el).attr('rel')?.trim() || null;
    // BUG-2 fix: Only detect position when --link-intelligence is enabled
    const position = detectPosition ? detectLinkPosition($, el) : 'other';
    anchors.push({
      href: resolvedHref,
      text,
      isInternal,
      // B2 fix: Empty anchor text is also non-descriptive
      isNonDescriptive: text === '' || NON_DESCRIPTIVE_PATTERNS.test(text),
      rel,
      position,
    });
  });
  return anchors;
}

// 2.7 Security audit
// P2 fix: Accept existing $ instance instead of re-parsing HTML
// S2 fix: Only check link[rel="stylesheet"] for mixed content
function extractSecurityData(fetchResult: FetchResult, $: cheerio.CheerioAPI): SecurityData {
  const isHttps = fetchResult.finalUrl.startsWith('https://');
  const headers = fetchResult.headers;

  // Check for mixed content: HTTP resources loaded on HTTPS page
  const mixedContentUrls: string[] = [];
  if (isHttps) {
    const resourceAttrs: { selector: string; attr: string }[] = [
      { selector: 'img[src]', attr: 'src' },
      { selector: 'script[src]', attr: 'src' },
      { selector: 'link[rel="stylesheet"][href]', attr: 'href' },
      { selector: 'video[src]', attr: 'src' },
      { selector: 'audio[src]', attr: 'src' },
      { selector: 'source[src]', attr: 'src' },
      { selector: 'iframe[src]', attr: 'src' },
      { selector: 'object[data]', attr: 'data' },
    ];
    for (const { selector, attr } of resourceAttrs) {
      $(selector).each((_, el) => {
        const url = $(el).attr(attr);
        if (url && url.startsWith('http://')) {
          mixedContentUrls.push(url);
        }
      });
    }
  }

  return {
    isHttps,
    hasMixedContent: mixedContentUrls.length > 0,
    mixedContentUrls: [...new Set(mixedContentUrls)].slice(0, 20),
    hasHsts: !!headers['strict-transport-security'],
    hasXFrameOptions: !!headers['x-frame-options'],
    hasCsp: !!headers['content-security-policy'],
  };
}

// ── Phase 7: Content & Indexability ──────────────────────────────

// 7.1 Indexability status calculation
function computeIndexability(
  statusCode: number,
  metaRobots: string | null,
  xRobotsTag: string | null,
  canonical: string | null,
  requestUrl: string,
  finalUrl: string,
): IndexabilityData {
  if (statusCode !== 200) {
    return { indexable: false, reason: `HTTP ${statusCode}` };
  }
  const metaRobotsLower = metaRobots?.toLowerCase() || '';
  if (metaRobotsLower.includes('noindex') || metaRobotsLower === 'none') {
    return { indexable: false, reason: 'noindex via meta robots' };
  }
  if (xRobotsTag && xRobotsTag.toLowerCase().includes('noindex')) {
    return { indexable: false, reason: 'noindex via X-Robots-Tag' };
  }
  if (canonical) {
    // Check if canonical points elsewhere (not self-referencing)
    const normCanonical = normalizeUrlForComparison(canonical);
    const normUrl = normalizeUrlForComparison(requestUrl);
    const normFinal = normalizeUrlForComparison(finalUrl);
    if (normCanonical !== normUrl && normCanonical !== normFinal) {
      return { indexable: false, reason: `canonical points to ${canonical}` };
    }
  }
  return { indexable: true, reason: 'indexable' };
}

// 7.3 Readability scoring (Flesch-Kincaid Reading Ease)
function computeReadability(bodyText: string): ReadabilityData | null {
  if (!bodyText || bodyText.length < 100) return null;

  const sentences = bodyText
    .split(/[.!?]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
  const sentenceCount = Math.max(sentences.length, 1);

  const words = bodyText.split(/\s+/).filter((w) => w.length > 0);
  const wordCount = words.length;
  if (wordCount < 30) return null;

  const syllableCount = words.reduce((sum, word) => sum + countSyllables(word), 0);
  const avgWordsPerSentence = wordCount / sentenceCount;
  const avgSyllablesPerWord = syllableCount / wordCount;

  // Flesch-Kincaid Reading Ease formula
  const fleschKincaid = Math.round(
    (206.835 - 1.015 * avgWordsPerSentence - 84.6 * avgSyllablesPerWord) * 10,
  ) / 10;

  return {
    fleschKincaid: Math.max(0, Math.min(100, fleschKincaid)),
    sentenceCount,
    avgWordsPerSentence: Math.round(avgWordsPerSentence * 10) / 10,
    syllableCount,
  };
}

// Syllable counting heuristic for English text
function countSyllables(word: string): number {
  const w = word.toLowerCase().replace(/[^a-z]/g, '');
  if (w.length <= 2) return 1;

  // Count vowel groups
  const vowelGroups = w.match(/[aeiouy]+/g);
  let count = vowelGroups ? vowelGroups.length : 1;

  // Subtract silent e
  if (w.endsWith('e') && !w.endsWith('le') && count > 1) {
    count--;
  }
  // Handle common suffixes
  if (w.endsWith('ed') && !w.endsWith('ted') && !w.endsWith('ded') && count > 1) {
    count--;
  }

  return Math.max(count, 1);
}

// 7.4 URL issues detection
function detectUrlIssues(url: string, canonical: string | null): string[] {
  const issues: string[] = [];
  try {
    const parsed = new URL(url);
    const path = parsed.pathname;
    const fullUrl = parsed.toString();

    // Uppercase characters in path
    if (path !== path.toLowerCase()) {
      issues.push('uppercase_in_url');
    }

    // Spaces or encoded spaces
    if (path.includes('%20') || path.includes(' ')) {
      issues.push('spaces_in_url');
    }

    // Double slashes in path (not the protocol //)
    if (/\/\//.test(path)) {
      issues.push('double_slashes');
    }

    // Non-ASCII characters
    if (/[^\x00-\x7F]/.test(decodeURIComponent(path))) {
      issues.push('non_ascii_chars');
    }

    // URL too long (>200 chars, SEO best practice)
    if (fullUrl.length > 200) {
      issues.push('url_too_long');
    }

    // Tracking parameters
    const trackingParams = ['utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content', 'fbclid', 'gclid', 'msclkid'];
    for (const param of trackingParams) {
      if (parsed.searchParams.has(param)) {
        issues.push('tracking_parameters');
        break;
      }
    }

    // Repetitive path segments
    const segments = path.split('/').filter((s) => s.length > 0);
    const segmentSet = new Set<string>();
    for (const seg of segments) {
      if (segmentSet.has(seg)) {
        issues.push('repetitive_path_segments');
        break;
      }
      segmentSet.add(seg);
    }

    // Trailing slash inconsistency with canonical
    if (canonical) {
      const urlHasTrailing = path.length > 1 && path.endsWith('/');
      try {
        const canonicalPath = new URL(canonical).pathname;
        const canonicalHasTrailing = canonicalPath.length > 1 && canonicalPath.endsWith('/');
        if (urlHasTrailing !== canonicalHasTrailing) {
          issues.push('trailing_slash_mismatch');
        }
      } catch {
        // skip invalid canonical
      }
    }

    // Underscores in URL (hyphens preferred)
    if (path.includes('_')) {
      issues.push('underscores_in_url');
    }
  } catch {
    // skip malformed URLs
  }
  return issues;
}

// 7.5 Soft 404 detection
const SOFT_404_PATTERNS = [
  /page\s*not\s*found/i,
  /404\s*(error|not\s*found|page)/i,
  /this\s*page\s*(was|is)\s*not\s*found/i,
  /no\s*longer\s*available/i,
  /does\s*not\s*exist/i,
  /has\s*been\s*removed/i,
  /page\s*doesn['']?t\s*exist/i,
  /we\s*couldn['']?t\s*find/i,
  /sorry.*the\s*page/i,
  /oops.*page/i,
  /the\s*requested\s*(url|page)\s*(was|is)\s*not\s*found/i,
];

function detectSoft404(statusCode: number, bodyText: string, wordCount: number): boolean {
  // Only check 200 OK pages
  if (statusCode !== 200) return false;
  // Only flag if low word count (likely not a content page mentioning "not found")
  if (wordCount > 200) return false;
  if (!bodyText) return false;

  return SOFT_404_PATTERNS.some((pattern) => pattern.test(bodyText));
}

// ── Phase 6: Resource Extraction for Page Weight ──────────────────

function classifyResourceType(tagName: string, rel?: string, href?: string): ResourceEntry['type'] {
  switch (tagName) {
    case 'img':
    case 'picture':
      return 'image';
    case 'script':
      return 'script';
    case 'video':
      return 'video';
    case 'audio':
      return 'audio';
    case 'link': {
      if (rel?.includes('stylesheet')) return 'stylesheet';
      if (rel?.includes('icon') || rel?.includes('shortcut')) return 'image';
      if (rel?.includes('preload')) {
        // Try to infer from file extension
        if (href) {
          const ext = href.split('?')[0].split('.').pop()?.toLowerCase();
          if (ext && ['woff', 'woff2', 'ttf', 'otf', 'eot'].includes(ext)) return 'font';
          if (ext && ['js', 'mjs'].includes(ext)) return 'script';
          if (ext && ['css'].includes(ext)) return 'stylesheet';
          if (ext && ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'avif', 'ico'].includes(ext)) return 'image';
        }
        return 'other';
      }
      return 'other';
    }
    case 'source':
      return 'other';
    default:
      return 'other';
  }
}

export function extractResourceRefs($: cheerio.CheerioAPI, pageUrl: string): ResourceEntry[] {
  const seen = new Set<string>();
  const resources: ResourceEntry[] = [];

  const selectors: { selector: string; attr: string; tag: string }[] = [
    { selector: 'img[src]', attr: 'src', tag: 'img' },
    { selector: 'script[src]', attr: 'src', tag: 'script' },
    { selector: 'link[rel="stylesheet"][href]', attr: 'href', tag: 'link' },
    { selector: 'link[rel="preload"][href]', attr: 'href', tag: 'link' },
    { selector: 'link[rel="icon"][href], link[rel="shortcut icon"][href]', attr: 'href', tag: 'link' },
    { selector: 'video[src]', attr: 'src', tag: 'video' },
    { selector: 'audio[src]', attr: 'src', tag: 'audio' },
    { selector: 'source[src]', attr: 'src', tag: 'source' },
  ];

  for (const { selector, attr, tag } of selectors) {
    $(selector).each((_, el) => {
      const raw = $(el).attr(attr);
      if (!raw) return;
      // Skip data URIs and empty values
      if (raw.startsWith('data:') || raw.trim() === '') return;

      let absolute: string;
      try {
        absolute = new URL(raw, pageUrl).toString();
      } catch {
        return;
      }

      if (seen.has(absolute)) return;
      seen.add(absolute);

      const rel = $(el).attr('rel') || '';
      resources.push({
        url: absolute,
        type: classifyResourceType(tag, rel, absolute),
        sizeBytes: null,
      });
    });
  }

  // Also check <img srcset> for additional image references
  $('img[srcset], source[srcset]').each((_, el) => {
    const srcset = $(el).attr('srcset');
    if (!srcset) return;
    // Parse srcset: "url1 1x, url2 2x" or "url1 300w, url2 600w"
    for (const part of srcset.split(',')) {
      const src = part.trim().split(/\s+/)[0];
      if (!src || src.startsWith('data:')) continue;
      try {
        const absolute = new URL(src, pageUrl).toString();
        if (seen.has(absolute)) continue;
        seen.add(absolute);
        resources.push({ url: absolute, type: 'image', sizeBytes: null });
      } catch {
        // skip invalid
      }
    }
  });

  return resources;
}
