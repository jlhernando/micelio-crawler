import type { PageData, CrawlStats, RedirectHop, ExternalLinkResult, SitemapStats, SitemapExtensionCounts, SitemapEntry } from './types.js';
import { getCruxAssessment } from './crux.js';
import { computePageRank } from './pagerank.js';
import { findNearDuplicates } from './simhash.js';
import { buildSegmentStats, type Segment } from './segmentation.js';
import { buildUrlStructureStats } from './url-structure.js';
import { normalizeUrlForComparison } from './utils.js';
import { buildTemplateStats } from './template-detector.js';

export function generateReport(
  pages: PageData[],
  durationMs: number,
  externalLinkResults: ExternalLinkResult[] = [],
  totalSitemapUrls: number = 0,
  gscDays: number = 90,
  ga4Days: number = 90,
  segmentRules: { name: string; pattern: string }[] = [],
  sitemapExtensionCounts: SitemapExtensionCounts = { news: 0, video: 0, image: 0 },
  sitemapValidationWarnings: string[] = [],
  sitemapEntries: SitemapEntry[] = [],
): CrawlStats {
  const statusCodes: Record<number, number> = {};
  let pagesWithoutTitle = 0;
  let pagesWithoutDescription = 0;
  let pagesWithoutH1 = 0;
  let imagesMissingAlt = 0;
  let totalImages = 0;
  let totalInternalLinks = 0;
  let totalExternalLinks = 0;
  const redirectChains: { url: string; chain: RedirectHop[] }[] = [];
  const brokenLinkMap = new Map<string, { statusCode: number; foundOn: string[] }>();
  const depthDistribution: Record<number, number> = {};
  const responseTimes: number[] = [];
  const slowPages: { url: string; responseTimeMs: number }[] = [];
  const longRedirectChains: { url: string; hops: number }[] = [];

  // Track inlinks for orphan page detection (with trailing-slash normalization)
  const inlinkCount = new Map<string, number>();
  const allUrls = new Set<string>();
  // Build URL resolver: maps both /path and /path/ to the canonical page URL
  const urlResolver = new Map<string, string>();
  for (const page of pages) {
    urlResolver.set(page.url, page.url);
    urlResolver.set(page.finalUrl, page.url);
    // Map trailing-slash variants to the same page
    const withSlash = page.url.endsWith('/') ? page.url : page.url + '/';
    const withoutSlash = page.url.endsWith('/') ? page.url.slice(0, -1) : page.url;
    if (!urlResolver.has(withSlash)) urlResolver.set(withSlash, page.url);
    if (!urlResolver.has(withoutSlash)) urlResolver.set(withoutSlash, page.url);
  }

  // Phase 2 accumulators
  const hreflangIssues: { url: string; issue: string }[] = [];
  let pagesWithStructuredData = 0;
  let pagesWithoutOg = 0;
  const thinContentPages: { url: string; wordCount: number }[] = [];
  const contentHashMap = new Map<string, string[]>();
  const nonDescriptiveAnchors: { url: string; text: string; foundOn: string }[] = [];
  const mixedContentPages: string[] = [];
  const nonHttpsPages: string[] = [];
  // Phase 3: Custom search accumulators
  const customSearchSummary: Record<string, { found: number; total: number }> = {};
  // Phase 4: Performance scores + AI
  const psiScores: number[] = [];
  let pagesWithAiAnalysis = 0;

  // Phase 6: Page weight accumulators
  const pageWeightTotals: { url: string; totalBytes: number }[] = [];
  const globalByType: Record<string, { count: number; bytes: number }> = {};

  // Phase 7: Content & Indexability accumulators
  const indexabilityReasons: Record<string, number> = {};
  let indexableCount = 0;
  let nonIndexableCount = 0;
  const simhashItems: { url: string; fingerprint: bigint }[] = [];
  const readabilityScores: { url: string; score: number }[] = [];
  const allUrlIssues: Record<string, string[]> = {};
  const soft404Pages: string[] = [];
  let nofollowedInternalLinkCount = 0;
  let followedInternalLinkCount = 0;
  const deadEndPages: string[] = [];
  const textToCodeRatios: { url: string; ratio: number }[] = [];

  // Phase 14.2: Image accessibility audit accumulators
  let imgMissingAltAttr = 0;
  let imgEmptyAlt = 0;
  const imgAltTooLong: { url: string; src: string; altLength: number }[] = [];
  let imgMissingDimensions = 0;
  const imgOversized: { url: string; src: string; sizeBytes: number }[] = [];
  let imgTotalBytes = 0;

  // Phase 9: Schema validation accumulators
  let schemaPageCount = 0;
  let schemaValidPageCount = 0;
  let schemaErrorPageCount = 0;
  const schemaRichResultEligible: Record<string, number> = {};
  const schemaIssueCounts: Record<string, number> = {};
  const schemaTypeCounts: Record<string, number> = {};

  let robotsBlockedCount = 0;

  for (const page of pages) {
    allUrls.add(page.url);

    // Skip robots-blocked pages from most stats (they have no real data)
    if (page.robotsBlocked) {
      robotsBlockedCount++;
      continue;
    }

    statusCodes[page.statusCode] = (statusCodes[page.statusCode] || 0) + 1;

    if (!page.title) pagesWithoutTitle++;
    if (!page.metaDescription) pagesWithoutDescription++;
    if (page.headings.h1.length === 0) pagesWithoutH1++;

    totalImages += page.images.length;
    imagesMissingAlt += page.images.filter((i) => i.missingAlt).length;

    // Phase 14.2: Image accessibility audit
    for (const img of page.images) {
      if (!img.hasAltAttribute) imgMissingAltAttr++;
      else if ((img.alt?.trim() ?? '') === '') imgEmptyAlt++;
      if (img.altTooLong) imgAltTooLong.push({ url: page.url, src: img.src, altLength: img.altLength });
      if (img.missingWidth || img.missingHeight) imgMissingDimensions++;
    }
    // Cross-reference image sizes from page weight data
    if (page.pageWeight) {
      const imgResources = page.pageWeight.resources.filter((r) => r.type === 'image' && r.sizeBytes !== null);
      for (const res of imgResources) {
        imgTotalBytes += res.sizeBytes!;
        if (res.sizeBytes! > 100 * 1024) {
          imgOversized.push({ url: page.url, src: res.url, sizeBytes: res.sizeBytes! });
        }
      }
    }

    totalInternalLinks += page.internalLinks.length;
    totalExternalLinks += page.externalLinks.length;

    // Depth distribution
    depthDistribution[page.depth] = (depthDistribution[page.depth] || 0) + 1;

    // Response times
    if (page.responseTimeMs > 0) {
      responseTimes.push(page.responseTimeMs);
    }
    if (page.responseTimeMs > 3000) {
      slowPages.push({ url: page.url, responseTimeMs: page.responseTimeMs });
    }

    // Redirect chains
    if (page.redirectChain.length > 0) {
      redirectChains.push({ url: page.url, chain: page.redirectChain });
      if (page.redirectChain.length > 2) {
        longRedirectChains.push({ url: page.url, hops: page.redirectChain.length });
      }
    }

    if (page.statusCode >= 400) {
      brokenLinkMap.set(page.url, { statusCode: page.statusCode, foundOn: [] });
    }

    // Count inlinks (resolve trailing-slash variants to canonical page URL)
    for (const link of page.internalLinks) {
      const resolved = urlResolver.get(link) || link;
      inlinkCount.set(resolved, (inlinkCount.get(resolved) || 0) + 1);
    }

    // Phase 2: Hreflang validation
    if (page.hreflang.length > 0) {
      const hasXDefault = page.hreflang.some((h) => h.lang === 'x-default');
      if (!hasXDefault) {
        hreflangIssues.push({ url: page.url, issue: 'Missing x-default hreflang' });
      }
      // B1 fix: Compare hostname + pathname, not just pathname
      const selfUrl = new URL(page.finalUrl);
      const hasSelfRef = page.hreflang.some((h) => {
        try {
          const hrefUrl = new URL(h.href);
          return hrefUrl.hostname === selfUrl.hostname && hrefUrl.pathname === selfUrl.pathname;
        } catch { return false; }
      });
      if (!hasSelfRef) {
        hreflangIssues.push({ url: page.url, issue: 'Missing self-referencing hreflang' });
      }
    }

    // Phase 2: Structured data
    if (page.structuredData.length > 0) pagesWithStructuredData++;

    // Phase 2: Open Graph (Q3 fix: only count 200 OK pages)
    if (Object.keys(page.openGraph).length === 0 && page.statusCode === 200 && !page.error) pagesWithoutOg++;

    // Phase 2: Thin content (< 200 words)
    if (page.wordCount > 0 && page.wordCount < 200 && page.statusCode === 200) {
      thinContentPages.push({ url: page.url, wordCount: page.wordCount });
    }

    // Phase 2: Content hash for duplicate detection
    if (page.contentHash) {
      const existing = contentHashMap.get(page.contentHash) || [];
      existing.push(page.url);
      contentHashMap.set(page.contentHash, existing);
    }

    // Phase 2: Non-descriptive anchors
    for (const anchor of page.anchors) {
      if (anchor.isNonDescriptive) {
        nonDescriptiveAnchors.push({ url: anchor.href, text: anchor.text, foundOn: page.url });
      }
    }

    // Phase 2: Security
    if (!page.security.isHttps && page.statusCode === 200) {
      nonHttpsPages.push(page.url);
    }
    if (page.security.hasMixedContent) {
      mixedContentPages.push(page.url);
    }

    // Phase 4: PageSpeed scores
    if (page.pagespeed && !page.pagespeed.error) {
      psiScores.push(page.pagespeed.performanceScore);
    }
    if (page.aiAnalysis && !page.aiAnalysis.startsWith('Error:')) {
      pagesWithAiAnalysis++;
    }

    // Phase 3: Custom search summary
    for (const [name, found] of Object.entries(page.customSearches)) {
      if (!customSearchSummary[name]) {
        customSearchSummary[name] = { found: 0, total: 0 };
      }
      customSearchSummary[name].total++;
      if (found) customSearchSummary[name].found++;
    }

    // Phase 6: Page weight
    if (page.pageWeight) {
      pageWeightTotals.push({ url: page.url, totalBytes: page.pageWeight.totalBytes });
      for (const [type, data] of Object.entries(page.pageWeight.byType)) {
        if (!globalByType[type]) {
          globalByType[type] = { count: 0, bytes: 0 };
        }
        globalByType[type].count += data.count;
        globalByType[type].bytes += data.bytes;
      }
    }

    // Phase 7: Indexability
    if (page.indexability) {
      if (page.indexability.indexable) {
        indexableCount++;
      } else {
        nonIndexableCount++;
        indexabilityReasons[page.indexability.reason] = (indexabilityReasons[page.indexability.reason] || 0) + 1;
      }
    }

    // Phase 7: SimHash for near-duplicate detection (only 200 OK pages with content)
    if (page.simhashFingerprint) {
      simhashItems.push({ url: page.url, fingerprint: BigInt(page.simhashFingerprint) });
    }

    // Phase 7: Readability
    if (page.readability) {
      readabilityScores.push({ url: page.url, score: page.readability.fleschKincaid });
    }

    // Phase 7: URL issues
    if (page.urlIssues.length > 0) {
      for (const issue of page.urlIssues) {
        if (!allUrlIssues[issue]) allUrlIssues[issue] = [];
        allUrlIssues[issue].push(page.url);
      }
    }

    // Phase 7: Soft 404
    if (page.isSoft404) {
      soft404Pages.push(page.url);
    }

    // Phase 7: Enhanced link analysis (nofollow/ugc/sponsored tracking)
    // Track counts only (avoid large arrays) — linksToNonIndexable computed post-loop
    for (const anchor of page.anchors) {
      if (anchor.isInternal) {
        const relLower = anchor.rel?.toLowerCase() || '';
        const isNofollow = relLower.includes('nofollow') || relLower.includes('ugc') || relLower.includes('sponsored');
        if (isNofollow) {
          nofollowedInternalLinkCount++;
        } else {
          followedInternalLinkCount++;
        }
      }
    }

    // Phase 7: Dead-end pages (0 outgoing internal links)
    if (page.statusCode === 200 && page.internalLinks.length === 0) {
      deadEndPages.push(page.url);
    }

    // Phase 7: Text-to-code ratio
    if (page.statusCode === 200 && page.textToCodeRatio > 0) {
      textToCodeRatios.push({ url: page.url, ratio: page.textToCodeRatio });
    }

    // Phase 9: Schema validation
    if (page.schemaValidation && page.schemaValidation.length > 0) {
      schemaPageCount++;
      let hasErrors = false;
      for (const sv of page.schemaValidation) {
        // Count types
        schemaTypeCounts[sv.type] = (schemaTypeCounts[sv.type] || 0) + 1;
        // Count rich result eligibility
        if (sv.richResultEligible && sv.richResultType) {
          schemaRichResultEligible[sv.richResultType] = (schemaRichResultEligible[sv.richResultType] || 0) + 1;
        }
        // Count issues
        for (const issue of sv.issues) {
          if (issue.severity === 'error') hasErrors = true;
          schemaIssueCounts[issue.message] = (schemaIssueCounts[issue.message] || 0) + 1;
        }
      }
      if (hasErrors) {
        schemaErrorPageCount++;
      } else {
        schemaValidPageCount++;
      }
    }
  }

  // Phase 2: Duplicate content groups (only groups with 2+ pages)
  const duplicateContentGroups: { hash: string; urls: string[] }[] = [];
  for (const [hash, urls] of contentHashMap) {
    if (urls.length > 1) {
      duplicateContentGroups.push({ hash, urls });
    }
  }

  // Phase 2: Hreflang return link validation
  // Build a map of each crawled page's hreflang entries for cross-referencing
  const normalizeUrl = (url: string): string => {
    try {
      const parsed = new URL(url);
      return parsed.hostname.replace(/^www\./, '') + parsed.pathname.replace(/\/$/, '');
    } catch { return url; }
  };
  const hreflangByUrl = new Map<string, { lang: string; href: string }[]>();
  for (const page of pages) {
    if (page.hreflang.length > 0) {
      hreflangByUrl.set(normalizeUrl(page.finalUrl), page.hreflang);
      // Also index by request URL in case they differ
      if (page.url !== page.finalUrl) {
        hreflangByUrl.set(normalizeUrl(page.url), page.hreflang);
      }
    }
  }
  // For each page with hreflang, check that each target page points back
  for (const page of pages) {
    if (page.hreflang.length === 0) continue;
    const sourceNorm = normalizeUrl(page.finalUrl);
    for (const entry of page.hreflang) {
      if (entry.lang === 'x-default') continue;
      const targetNorm = normalizeUrl(entry.href);
      if (targetNorm === sourceNorm) continue; // skip self-reference
      const targetHreflang = hreflangByUrl.get(targetNorm);
      if (!targetHreflang) continue; // target not crawled, can't validate
      // Check if target has a return link to this page
      const hasReturnLink = targetHreflang.some((h) => normalizeUrl(h.href) === sourceNorm);
      if (!hasReturnLink) {
        hreflangIssues.push({
          url: page.url,
          issue: `Missing return link: ${entry.lang} target ${entry.href} does not link back`,
        });
      }
    }
  }

  // Map broken links to their referrers
  for (const page of pages) {
    for (const link of page.internalLinks) {
      const broken = brokenLinkMap.get(link);
      if (broken) {
        broken.foundOn.push(page.url);
      }
    }
  }

  const brokenLinks = Array.from(brokenLinkMap.entries()).map(([url, data]) => ({
    url, statusCode: data.statusCode, foundOn: data.foundOn,
  }));

  // BUG-3 fix: Use Map lookup instead of O(n²) find()
  const depthByUrl = new Map(pages.map((p) => [p.url, p.depth]));
  const orphanPages: string[] = [];
  for (const url of allUrls) {
    if ((inlinkCount.get(url) || 0) === 0) {
      const depth = depthByUrl.get(url) ?? 0;
      if (depth > 0) {
        orphanPages.push(url);
      }
    }
  }

  // Phase 6: Sitemap audit stats
  let sitemapStats: SitemapStats | null = null;
  const pagesWithSitemapData = pages.filter((p) => p.sitemapData !== null);
  if (pagesWithSitemapData.length > 0 || totalSitemapUrls > 0) {
    const sitemapUrlSet = new Set(pages.filter((p) => p.sitemapData?.inSitemap).map((p) => p.url));
    const crawledUrlSet = new Set(pages.map((p) => p.url));
    const crawledFinalUrlSet = new Set(pages.map((p) => p.finalUrl));

    // Orphan URLs: in sitemap but not linked to by any other crawled page
    // In spider mode, also require depth > 0 (depth 0 is the seed URL).
    // In sitemap mode, all pages start at depth 0, so we only check inlink count.
    const allAtDepthZero = pages.every((p) => p.depth === 0);
    const orphanUrls = pages
      .filter((p) => {
        if (!p.sitemapData?.inSitemap) return false;
        if ((inlinkCount.get(p.url) || 0) > 0) return false;
        // In spider mode, skip the seed URL (depth 0 with no inlinks is expected)
        if (!allAtDepthZero && p.depth === 0) return false;
        return true;
      })
      .map((p) => p.url);

    // Missing from sitemap: crawled 200 OK indexable pages not in sitemap
    const missingFromSitemap = pages
      .filter((p) => {
        if (p.sitemapData?.inSitemap) return false;
        if (p.statusCode !== 200) return false;
        if (p.metaRobots?.includes('noindex')) return false;
        if (p.canonical && p.canonical !== p.url && p.canonical !== p.finalUrl) return false;
        return true;
      })
      .map((p) => p.url);

    // Non-indexable in sitemap: URLs in sitemap that are non-200, noindex, or canonical elsewhere
    const nonIndexableInSitemap = pages
      .filter((p) => {
        if (!p.sitemapData?.inSitemap) return false;
        if (p.statusCode !== 200) return true;
        if (p.metaRobots?.includes('noindex')) return true;
        if (p.canonical && p.canonical !== p.url && p.canonical !== p.finalUrl) return true;
        return false;
      })
      .map((p) => p.url);

    // Phase 6.2: Lastmod freshness analysis
    const now = Date.now();
    const ONE_YEAR_MS = 365 * 24 * 60 * 60 * 1000;
    let lastmodMissing = 0;
    let lastmodStale = 0;
    let lastmodFuture = 0;
    let lastmodInvalid = 0;
    for (const entry of sitemapEntries) {
      if (!entry.lastmod) { lastmodMissing++; continue; }
      const d = new Date(entry.lastmod);
      if (Number.isNaN(d.getTime())) { lastmodInvalid++; continue; }
      if (d.getTime() > now + 24 * 60 * 60 * 1000) { lastmodFuture++; }
      else if (now - d.getTime() > ONE_YEAR_MS) { lastmodStale++; }
    }

    // Phase 6.2: Redirects and status breakdown for sitemap URLs
    const redirectsInSitemap: string[] = [];
    const statusBreakdown: Record<number, number> = {};
    for (const page of pages) {
      if (!page.sitemapData?.inSitemap) continue;
      statusBreakdown[page.statusCode] = (statusBreakdown[page.statusCode] || 0) + 1;
      if (page.redirectChain && page.redirectChain.length > 0) {
        redirectsInSitemap.push(page.url);
      }
    }

    // Phase 6.3: Duplicate URLs across sitemaps
    const urlSourceMap = new Map<string, Set<string>>();
    for (const entry of sitemapEntries) {
      const sources = urlSourceMap.get(entry.url);
      if (sources) sources.add(entry.source);
      else urlSourceMap.set(entry.url, new Set([entry.source]));
    }
    const duplicateAcrossSitemaps: { url: string; sources: string[] }[] = [];
    for (const [url, sources] of urlSourceMap) {
      if (sources.size > 1) duplicateAcrossSitemaps.push({ url, sources: [...sources] });
    }

    // Phase 6.3: Uncrawled sitemap URLs (deduplicated)
    const uncrawledSet = new Set<string>();
    for (const entry of sitemapEntries) {
      if (!crawledUrlSet.has(entry.url) && !crawledFinalUrlSet.has(entry.url)) {
        uncrawledSet.add(entry.url);
      }
    }
    const uncrawledSitemapUrls = [...uncrawledSet];

    // Phase 6.3: Coverage stats
    const crawledAndInSitemap = pages.filter((p) => p.sitemapData?.inSitemap).length;
    const crawledNotInSitemap = pages.filter((p) => p.sitemapData && !p.sitemapData.inSitemap).length;

    sitemapStats = {
      totalSitemapUrls: totalSitemapUrls > 0 ? totalSitemapUrls : sitemapUrlSet.size,
      orphanUrls,
      missingFromSitemap,
      nonIndexableInSitemap,
      sitemapErrors: [],
      newsEntryCount: sitemapExtensionCounts.news,
      videoEntryCount: sitemapExtensionCounts.video,
      imageEntryCount: sitemapExtensionCounts.image,
      // Phase 6.2
      validationWarnings: sitemapValidationWarnings,
      lastmodStats: { missing: lastmodMissing, stale: lastmodStale, future: lastmodFuture, invalid: lastmodInvalid },
      redirectsInSitemap,
      statusBreakdown,
      // Phase 6.3
      duplicateAcrossSitemaps,
      uncrawledSitemapUrls,
      coverage: {
        crawledAndInSitemap,
        crawledNotInSitemap,
        inSitemapNotCrawled: uncrawledSitemapUrls.length,
      },
    };
  }

  // Phase 6: Page weight stats
  let pageWeightStats: CrawlStats['pageWeightStats'] = null;
  if (pageWeightTotals.length > 0) {
    const sorted = [...pageWeightTotals].sort((a, b) => b.totalBytes - a.totalBytes);
    const avgTotal = Math.round(sorted.reduce((s, p) => s + p.totalBytes, 0) / sorted.length);
    const oversized = sorted.filter((p) => p.totalBytes > 3 * 1024 * 1024); // >3MB
    pageWeightStats = {
      avgTotalBytes: avgTotal,
      heaviestPages: sorted.slice(0, 10),
      byType: globalByType,
      oversizedPages: oversized.slice(0, 20),
    };
  }

  // Phase 7: Near-duplicate detection via SimHash
  const nearDuplicateGroups = simhashItems.length > 1
    ? findNearDuplicates(simhashItems, 90)
    : [];

  // Phase 7: Readability stats
  let readabilityStats: CrawlStats['readabilityStats'] = null;
  if (readabilityScores.length > 0) {
    const avgScore = Math.round(readabilityScores.reduce((s, r) => s + r.score, 0) / readabilityScores.length * 10) / 10;
    const difficult = readabilityScores.filter((r) => r.score < 30).sort((a, b) => a.score - b.score).slice(0, 20);
    const veryEasy = readabilityScores.filter((r) => r.score > 80).sort((a, b) => b.score - a.score).slice(0, 20);
    readabilityStats = { avgScore, difficult, veryEasy };
  }

  // Phase 7: Links to non-indexable pages (computed post-loop to avoid large arrays)
  const nonIndexableUrls = new Set(pages.filter((p) => !p.indexability?.indexable).map((p) => p.url));
  const linksToNonIndexable: { from: string; to: string }[] = [];
  if (nonIndexableUrls.size > 0) {
    for (const page of pages) {
      for (const anchor of page.anchors) {
        if (anchor.isInternal && nonIndexableUrls.has(anchor.href)) {
          const relLower = anchor.rel?.toLowerCase() || '';
          const isNofollow = relLower.includes('nofollow') || relLower.includes('ugc') || relLower.includes('sponsored');
          if (!isNofollow && linksToNonIndexable.length < 50) {
            linksToNonIndexable.push({ from: page.url, to: anchor.href });
          }
        }
      }
    }
  }

  // Phase 7: Text-to-code ratio stats
  let textToCodeStats: CrawlStats['textToCodeStats'] = null;
  if (textToCodeRatios.length > 0) {
    const avgRatio = Math.round(textToCodeRatios.reduce((s, r) => s + r.ratio, 0) / textToCodeRatios.length * 10) / 10;
    const contentPoor = textToCodeRatios.filter((r) => r.ratio < 10).sort((a, b) => a.ratio - b.ratio).slice(0, 20);
    textToCodeStats = { avgRatio, contentPoor };
  }

  // Inlinks count — annotate each page with the number of unique pages linking to it
  for (const p of pages) {
    p.inlinks = inlinkCount.get(p.url) || 0;
  }

  // Internal PageRank — annotate each page with its score
  const pageRankScores = computePageRank(pages);
  for (const p of pages) {
    p.pageRank = pageRankScores.get(p.url) ?? 0;
  }

  // Response time percentiles
  responseTimes.sort((a, b) => a - b);
  // EDGE-4 fix: Clamp index to prevent out-of-bounds access
  const percentile = (arr: number[], p: number) =>
    arr.length > 0 ? arr[Math.min(Math.floor(arr.length * p / 100), arr.length - 1)] : 0;

  return {
    totalPages: pages.length,
    robotsBlockedCount,
    statusCodes,
    pagesWithoutTitle,
    pagesWithoutDescription,
    pagesWithoutH1,
    redirectChains,
    brokenLinks,
    brokenExternalLinks: externalLinkResults,
    imagesMissingAlt,
    totalImages,
    totalInternalLinks,
    totalExternalLinks,
    crawlDurationMs: durationMs,
    depthDistribution,
    responseTimePercentiles: {
      p50: percentile(responseTimes, 50),
      p90: percentile(responseTimes, 90),
      p99: percentile(responseTimes, 99),
    },
    slowPages: slowPages.sort((a, b) => b.responseTimeMs - a.responseTimeMs).slice(0, 10),
    longRedirectChains,
    orphanPages,
    // Phase 2 stats
    hreflangIssues,
    pagesWithStructuredData,
    pagesWithoutOg,
    thinContentPages: thinContentPages.sort((a, b) => a.wordCount - b.wordCount).slice(0, 20),
    duplicateContentGroups,
    nonDescriptiveAnchors: nonDescriptiveAnchors.slice(0, 50),
    mixedContentPages,
    nonHttpsPages,
    // Phase 3 stats
    customSearchSummary,
    // Phase 4 stats
    performanceScores: psiScores.length > 0 ? {
      avg: Math.round(psiScores.reduce((a, b) => a + b, 0) / psiScores.length),
      min: Math.min(...psiScores),
      max: Math.max(...psiScores),
    } : null,
    pagesWithAiAnalysis,
    pageRankScores,
    // Phase 6
    sitemapStats,
    pageWeightStats,
    // Phase 7
    indexabilityStats: {
      indexable: indexableCount,
      nonIndexable: nonIndexableCount,
      reasons: indexabilityReasons,
    },
    nearDuplicateGroups,
    readabilityStats,
    urlIssueStats: allUrlIssues,
    soft404Pages,
    linkAnalysis: {
      deadEndPages: deadEndPages.slice(0, 50),
      nofollowedInternalLinks: nofollowedInternalLinkCount,
      followedInternalLinks: followedInternalLinkCount,
      linksToNonIndexable,
    },
    textToCodeStats,
    // Phase 9: Schema validation
    schemaValidationStats: schemaPageCount > 0 ? {
      pagesWithSchema: schemaPageCount,
      pagesWithValidSchema: schemaValidPageCount,
      pagesWithErrors: schemaErrorPageCount,
      richResultEligible: schemaRichResultEligible,
      topIssues: Object.entries(schemaIssueCounts)
        .sort(([, a], [, b]) => b - a)
        .slice(0, 20)
        .map(([message, count]) => ({ message, count })),
      typeDistribution: schemaTypeCounts,
    } : null,
    // Phase 11: GSC stats
    gscStats: buildGscStats(pages, gscDays),
    // Phase 11: GA4 stats
    ga4Stats: buildGa4Stats(pages, ga4Days),
    // Phase 12: Render comparison stats
    renderCompareStats: buildRenderCompareStats(pages),
    // Phase 12: Segmentation stats
    segmentStats: segmentRules.length > 0
      ? buildSegmentStats(pages, segmentRules.map(r => ({ name: r.name, pattern: new RegExp(r.pattern) })))
      : null,
    // Phase 14.2: Image accessibility audit
    imageAuditStats: totalImages > 0 ? {
      totalImages,
      missingAltAttribute: imgMissingAltAttr,
      emptyAlt: imgEmptyAlt,
      altTooLong: imgAltTooLong.slice(0, 50),
      missingDimensions: imgMissingDimensions,
      oversizedImages: imgOversized.sort((a, b) => b.sizeBytes - a.sizeBytes).slice(0, 50),
      totalImageBytes: imgTotalBytes,
    } : null,
    // Link Intelligence (populated by CLI post-crawl)
    linkIntelligenceStats: null,
    // Phase 13: N-Grams (populated by CLI from crawl result)
    ngramStats: null,
    // Phase 13.2: Embedding stats (populated by CLI from crawl result)
    embeddingStats: null,
    // Redirect chain analysis (supersedes redirectChains/longRedirectChains for detailed analysis)
    redirectStats: buildRedirectStats(pages),
    // Canonical tag validation
    canonicalStats: buildCanonicalStats(pages),
    // Template type distribution
    templateTypeDistribution: buildTemplateStats(pages),
    // URL Structure Analytics
    urlStructureStats: buildUrlStructureStats(pages),
    // CrUX stats (populated from page-level data)
    cruxStats: buildCruxStats(pages),
    // Plausible Analytics stats (days passed separately, not available in page data)
    plausibleStats: buildPlausibleStats(pages, 0),
  };
}

function buildCanonicalStats(pages: PageData[]): CrawlStats['canonicalStats'] {
  const okPages = pages.filter(p => p.statusCode === 200 && !p.error);
  if (okPages.length === 0) return null;

  // Use shared normalization (strips www, trailing slash, protocol; sorts query params)
  const norm = normalizeUrlForComparison;

  // Build lookup maps
  const pageByUrl = new Map<string, PageData>();
  const pageByNormUrl = new Map<string, PageData>();
  for (const page of pages) {
    pageByUrl.set(page.url, page);
    pageByUrl.set(page.finalUrl, page);
    pageByNormUrl.set(norm(page.url), page);
    pageByNormUrl.set(norm(page.finalUrl), page);
  }

  let totalWithCanonical = 0;
  let totalWithoutCanonical = 0;
  let selfReferencing = 0;
  let canonicalized = 0;
  let multipleCanonicals = 0;
  const canonicalChains: { url: string; chain: string[] }[] = [];
  const canonicalLoops: { url: string; loop: string[] }[] = [];
  const canonicalToNon200: { url: string; canonical: string; targetStatus: number }[] = [];
  const canonicalToNonIndexable: { url: string; canonical: string }[] = [];
  const crossDomain: { url: string; canonical: string }[] = [];
  const httpHttpsMismatch: { url: string; canonical: string }[] = [];
  const relativeCanonicals: { url: string; rawHref: string }[] = [];
  const canonicalWithQueryString: { url: string; canonical: string }[] = [];
  const canonicalTargetCounts = new Map<string, number>();

  for (const page of okPages) {
    // Multiple canonicals
    if (page.canonicalCount > 1) {
      multipleCanonicals++;
    }

    // Missing canonical
    if (!page.canonical) {
      totalWithoutCanonical++;
      continue;
    }

    totalWithCanonical++;

    // Relative canonical detection
    if (page.canonicalRaw && !page.canonicalRaw.startsWith('http://') && !page.canonicalRaw.startsWith('https://') && !page.canonicalRaw.startsWith('//')) {
      relativeCanonicals.push({ url: page.url, rawHref: page.canonicalRaw });
    }

    const normCanonical = norm(page.canonical);
    const normUrl = norm(page.url);
    const normFinal = norm(page.finalUrl);

    // Self-referencing vs canonicalized
    if (normCanonical === normUrl || normCanonical === normFinal) {
      selfReferencing++;
    } else {
      canonicalized++;
      // Track canonical targets
      canonicalTargetCounts.set(page.canonical, (canonicalTargetCounts.get(page.canonical) || 0) + 1);
    }

    // Cross-domain canonical
    try {
      const pageHost = new URL(page.finalUrl).hostname.replace(/^www\./, '');
      const canonHost = new URL(page.canonical).hostname.replace(/^www\./, '');
      if (pageHost !== canonHost) {
        crossDomain.push({ url: page.url, canonical: page.canonical });
      }
    } catch { /* skip */ }

    // HTTP/HTTPS mismatch
    try {
      const pageProto = new URL(page.finalUrl).protocol;
      const canonProto = new URL(page.canonical).protocol;
      if (pageProto !== canonProto) {
        httpHttpsMismatch.push({ url: page.url, canonical: page.canonical });
      }
    } catch { /* skip */ }

    // Canonical with query string (only for non-self-referencing; self-ref with query is normal)
    if (normCanonical !== normUrl && normCanonical !== normFinal) {
      try {
        const canonParsed = new URL(page.canonical);
        if (canonParsed.search) {
          canonicalWithQueryString.push({ url: page.url, canonical: page.canonical });
        }
      } catch { /* skip */ }
    }

    // Canonical target checks (only for canonicalized pages pointing elsewhere)
    if (normCanonical !== normUrl && normCanonical !== normFinal) {
      const targetPage = pageByUrl.get(page.canonical) || pageByNormUrl.get(normCanonical);
      if (targetPage) {
        // Canonical pointing to non-200
        if (targetPage.statusCode !== 200) {
          canonicalToNon200.push({ url: page.url, canonical: page.canonical, targetStatus: targetPage.statusCode });
        }
        // Canonical pointing to non-indexable page
        if (targetPage.indexability && !targetPage.indexability.indexable) {
          canonicalToNonIndexable.push({ url: page.url, canonical: page.canonical });
        }
      }
    }
  }

  // Detect canonical chains and loops
  // A canonical chain is A -> B -> C (B has a canonical pointing to C, A has canonical pointing to B)
  // A canonical loop is A -> B -> A
  // Deduplicate loops: track reported cycle members to avoid reporting same loop from multiple entry points
  const reportedLoopSets = new Set<string>();
  for (const page of okPages) {
    if (!page.canonical) continue;
    const normUrl = norm(page.url);
    const normFinal = norm(page.finalUrl);
    const normCanonical = norm(page.canonical);
    if (normCanonical === normUrl || normCanonical === normFinal) continue;  // self-referencing

    // Follow the canonical chain up to 10 hops
    const chain: string[] = [page.url, page.canonical];
    const visited = new Set<string>([norm(page.url), normCanonical]);
    let current = page.canonical;
    let isLoop = false;

    for (let depth = 0; depth < 10; depth++) {
      const normCurrent = norm(current);
      const targetPage = pageByUrl.get(current) || pageByNormUrl.get(normCurrent);
      if (!targetPage || !targetPage.canonical) break;

      const normTargetCanonical = norm(targetPage.canonical);
      // Self-referencing at target means chain terminates here
      const normTargetUrl = norm(targetPage.url);
      const normTargetFinal = norm(targetPage.finalUrl);
      if (normTargetCanonical === normTargetUrl || normTargetCanonical === normTargetFinal) break;

      // Check for loop
      if (visited.has(normTargetCanonical)) {
        chain.push(targetPage.canonical);
        isLoop = true;
        break;
      }

      visited.add(normTargetCanonical);
      chain.push(targetPage.canonical);
      current = targetPage.canonical;
    }

    if (isLoop && canonicalLoops.length < 50) {
      // Deduplicate: create a sorted key from all normalized URLs in the cycle
      const loopKey = [...visited].sort().join('|');
      if (!reportedLoopSets.has(loopKey)) {
        reportedLoopSets.add(loopKey);
        canonicalLoops.push({ url: page.url, loop: chain });
      }
    } else if (chain.length > 2 && !isLoop && canonicalChains.length < 50) {
      canonicalChains.push({ url: page.url, chain });
    }
  }

  // Top 10 canonical targets
  const topCanonicalTargets = Array.from(canonicalTargetCounts.entries())
    .sort(([, a], [, b]) => b - a)
    .slice(0, 10)
    .map(([url, count]) => ({ url, count }));

  return {
    totalWithCanonical,
    totalWithoutCanonical,
    selfReferencing,
    canonicalized,
    multipleCanonicals,
    canonicalChains: canonicalChains.slice(0, 50),
    canonicalLoops: canonicalLoops.slice(0, 50),
    canonicalToNon200: canonicalToNon200.slice(0, 50),
    canonicalToNonIndexable: canonicalToNonIndexable.slice(0, 50),
    crossDomain: crossDomain.slice(0, 50),
    httpHttpsMismatch: httpHttpsMismatch.slice(0, 50),
    relativeCanonicals: relativeCanonicals.slice(0, 50),
    canonicalWithQueryString: canonicalWithQueryString.slice(0, 50),
    topCanonicalTargets,
  };
}

function buildRedirectStats(pages: PageData[]): CrawlStats['redirectStats'] {
  const redirectingPages = pages.filter(p => p.redirectChain.length > 0);
  if (redirectingPages.length === 0) return null;

  const totalRedirects = redirectingPages.length;
  const totalHops = redirectingPages.reduce((s, p) => s + p.redirectChain.length, 0);
  const avgChainLength = Math.round((totalHops / totalRedirects) * 10) / 10;
  const maxChainLength = redirectingPages.reduce((m, p) => Math.max(m, p.redirectChain.length), 0);

  // Count hops by status code
  const byStatusCode: Record<number, number> = {};
  for (const page of redirectingPages) {
    for (const hop of page.redirectChain) {
      byStatusCode[hop.statusCode] = (byStatusCode[hop.statusCode] || 0) + 1;
    }
  }

  // Classify redirect patterns
  const httpToHttps: { url: string; chain: RedirectHop[] }[] = [];
  const wwwNormalization: { url: string; chain: RedirectHop[] }[] = [];
  const trailingSlash: { url: string; chain: RedirectHop[] }[] = [];
  const crossDomain: { url: string; chain: RedirectHop[] }[] = [];
  const selfRedirects: string[] = [];
  const chainLoops: { url: string; chain: RedirectHop[] }[] = [];
  const temporaryRedirects: { url: string; chain: RedirectHop[] }[] = [];

  // Count redirect targets (which URLs receive the most redirects)
  const targetCounts = new Map<string, number>();

  for (const page of redirectingPages) {
    const chain = page.redirectChain;
    const firstUrl = chain[0].url;
    const finalUrl = page.finalUrl;

    // Track redirect target
    targetCounts.set(finalUrl, (targetCounts.get(finalUrl) || 0) + 1);

    try {
      const firstParsed = new URL(firstUrl);
      const finalParsed = new URL(finalUrl);

      // HTTP -> HTTPS
      if (firstParsed.protocol === 'http:' && finalParsed.protocol === 'https:') {
        httpToHttps.push({ url: page.url, chain });
      }

      // www normalization (add or remove www)
      const firstHost = firstParsed.hostname.replace(/^www\./, '');
      const finalHost = finalParsed.hostname.replace(/^www\./, '');
      if (firstHost === finalHost && firstParsed.hostname !== finalParsed.hostname) {
        wwwNormalization.push({ url: page.url, chain });
      }

      // Trailing slash normalization
      if (firstParsed.hostname === finalParsed.hostname &&
          firstParsed.protocol === finalParsed.protocol) {
        const firstPath = firstParsed.pathname;
        const finalPath = finalParsed.pathname;
        if ((firstPath === finalPath + '/' || firstPath + '/' === finalPath) &&
            firstParsed.search === finalParsed.search) {
          trailingSlash.push({ url: page.url, chain });
        }
      }

      // Cross-domain redirect
      if (firstHost !== finalHost) {
        crossDomain.push({ url: page.url, chain });
      }
    } catch {
      // Invalid URL, skip classification
    }

    // Self-redirect detection: 1-hop chain where origin equals destination
    if (chain.length === 1 && (firstUrl === finalUrl || page.url === finalUrl)) {
      selfRedirects.push(page.url);
    }

    // Loop detection: check if any URL appears more than once in the chain,
    // or if the final destination appears as a hop (circular back to start)
    const seenUrls = new Set<string>();
    let hasLoop = false;
    for (const hop of chain) {
      if (seenUrls.has(hop.url)) {
        hasLoop = true;
        break;
      }
      seenUrls.add(hop.url);
    }
    if (!hasLoop && seenUrls.has(page.finalUrl)) {
      hasLoop = true;
    }
    if (hasLoop) {
      chainLoops.push({ url: page.url, chain });
    }

    // Temporary redirect detection (302, 307)
    const hasTemporary = chain.some(hop => hop.statusCode === 302 || hop.statusCode === 307);
    if (hasTemporary) {
      temporaryRedirects.push({ url: page.url, chain });
    }
  }

  // Top 10 longest chains
  const longestChains = [...redirectingPages]
    .sort((a, b) => b.redirectChain.length - a.redirectChain.length)
    .slice(0, 10)
    .map(p => ({ url: p.url, chain: p.redirectChain, finalUrl: p.finalUrl }));

  // Top 10 redirect targets
  const topRedirectTargets = Array.from(targetCounts.entries())
    .sort(([, a], [, b]) => b - a)
    .slice(0, 10)
    .map(([url, count]) => ({ url, count }));

  return {
    totalRedirects,
    totalHops,
    avgChainLength,
    maxChainLength,
    byStatusCode,
    httpToHttps: httpToHttps.slice(0, 50),
    wwwNormalization: wwwNormalization.slice(0, 50),
    trailingSlash: trailingSlash.slice(0, 50),
    crossDomain: crossDomain.slice(0, 50),
    selfRedirects: selfRedirects.slice(0, 50),
    chainLoops: chainLoops.slice(0, 50),
    temporaryRedirects: temporaryRedirects.slice(0, 50),
    longestChains,
    topRedirectTargets,
  };
}

function buildCruxStats(pages: PageData[]): CrawlStats['cruxStats'] {
  const pagesWithCrux = pages.filter(p => p.cruxData !== null);
  if (pagesWithCrux.length === 0) return null;

  let lcpSum = 0, lcpCount = 0;
  let inpSum = 0, inpCount = 0;
  let clsSum = 0, clsCount = 0;
  let ttfbSum = 0, ttfbCount = 0;
  let fcpSum = 0, fcpCount = 0;
  let goodLcp = 0, poorLcp = 0;
  let goodInp = 0, poorInp = 0;
  let goodCls = 0, poorCls = 0;

  const lcpEntries: { url: string; lcpMs: number }[] = [];
  const inpEntries: { url: string; inpMs: number }[] = [];
  const clsEntries: { url: string; cls: number }[] = [];

  for (const page of pagesWithCrux) {
    const crux = page.cruxData!;
    if (crux.lcpMs !== null) {
      lcpSum += crux.lcpMs; lcpCount++;
      lcpEntries.push({ url: page.url, lcpMs: crux.lcpMs });
      const a = getCruxAssessment('lcp', crux.lcpMs);
      if (a === 'good') goodLcp++;
      if (a === 'poor') poorLcp++;
    }
    if (crux.inpMs !== null) {
      inpSum += crux.inpMs; inpCount++;
      inpEntries.push({ url: page.url, inpMs: crux.inpMs });
      const a = getCruxAssessment('inp', crux.inpMs);
      if (a === 'good') goodInp++;
      if (a === 'poor') poorInp++;
    }
    if (crux.cls !== null) {
      clsSum += crux.cls; clsCount++;
      clsEntries.push({ url: page.url, cls: crux.cls });
      const a = getCruxAssessment('cls', crux.cls);
      if (a === 'good') goodCls++;
      if (a === 'poor') poorCls++;
    }
    if (crux.ttfbMs !== null) { ttfbSum += crux.ttfbMs; ttfbCount++; }
    if (crux.fcpMs !== null) { fcpSum += crux.fcpMs; fcpCount++; }
  }

  // All pages are queried with the same form factor, so take from first page
  const formFactor = pagesWithCrux[0].cruxData!.formFactor;

  return {
    pagesWithData: pagesWithCrux.length,
    formFactor,
    avgLcpMs: lcpCount > 0 ? Math.round(lcpSum / lcpCount) : null,
    avgInpMs: inpCount > 0 ? Math.round(inpSum / inpCount) : null,
    avgCls: clsCount > 0 ? Math.round((clsSum / clsCount) * 1000) / 1000 : null,
    avgTtfbMs: ttfbCount > 0 ? Math.round(ttfbSum / ttfbCount) : null,
    avgFcpMs: fcpCount > 0 ? Math.round(fcpSum / fcpCount) : null,
    goodLcp, poorLcp,
    goodInp, poorInp,
    goodCls, poorCls,
    worstLcp: lcpEntries.sort((a, b) => b.lcpMs - a.lcpMs).slice(0, 10),
    worstInp: inpEntries.sort((a, b) => b.inpMs - a.inpMs).slice(0, 10),
    worstCls: clsEntries.sort((a, b) => b.cls - a.cls).slice(0, 10),
  };
}

function buildGscStats(pages: PageData[], days: number): CrawlStats['gscStats'] {
  const pagesWithData = pages.filter(p => p.gscData !== null);
  if (pagesWithData.length === 0) return null;

  let totalImpressions = 0;
  let totalClicks = 0;
  let positionSum = 0;
  let positionCount = 0;

  for (const p of pagesWithData) {
    const g = p.gscData!;
    totalImpressions += g.impressions;
    totalClicks += g.clicks;
    if (g.position > 0) {
      positionSum += g.position;
      positionCount++;
    }
  }

  const avgCtr = totalImpressions > 0 ? totalClicks / totalImpressions : 0;
  const avgPosition = positionCount > 0 ? Math.round((positionSum / positionCount) * 10) / 10 : 0;

  // Top pages by clicks
  const topByClicks = pagesWithData
    .map(p => ({
      url: p.url,
      clicks: p.gscData!.clicks,
      impressions: p.gscData!.impressions,
      position: p.gscData!.position,
    }))
    .sort((a, b) => b.clicks - a.clicks)
    .slice(0, 10);

  // Zombie pages: indexed (200 + indexable) but zero clicks
  const zombiePages = pages
    .filter(p => p.statusCode === 200 && p.indexability?.indexable && p.gscData && p.gscData.clicks === 0 && p.gscData.impressions > 0)
    .map(p => p.url);

  return {
    pagesWithData: pagesWithData.length,
    totalImpressions,
    totalClicks,
    avgCtr,
    avgPosition,
    days,
    topByClicks,
    zombiePages,
  };
}

function buildRenderCompareStats(pages: PageData[]): CrawlStats['renderCompareStats'] {
  const pagesWithDiffs = pages.filter(p => p.renderDiffs && p.renderDiffs.length > 0);
  const pagesCompared = pages.filter(p => p.renderDiffs !== null).length;

  if (pagesCompared === 0) return null;

  const fieldDiffCounts: Record<string, number> = {};
  const criticalDiffs: { url: string; field: string; original: string; rendered: string }[] = [];
  const CRITICAL_FIELDS = new Set(['canonical', 'meta_robots', 'title']);

  for (const page of pagesWithDiffs) {
    for (const diff of page.renderDiffs!) {
      fieldDiffCounts[diff.field] = (fieldDiffCounts[diff.field] || 0) + 1;
      if (CRITICAL_FIELDS.has(diff.field) && criticalDiffs.length < 50) {
        criticalDiffs.push({
          url: page.url,
          field: diff.field,
          original: diff.original.substring(0, 200),
          rendered: diff.rendered.substring(0, 200),
        });
      }
    }
  }

  return {
    pagesCompared,
    pagesWithDiffs: pagesWithDiffs.length,
    fieldDiffCounts,
    criticalDiffs,
  };
}

function buildGa4Stats(pages: PageData[], days: number): CrawlStats['ga4Stats'] {
  const pagesWithData = pages.filter(p => p.ga4Data !== null);
  if (pagesWithData.length === 0) return null;

  let totalSessions = 0;
  let totalPageviews = 0;
  let totalConversions = 0;
  let bounceRateSum = 0;
  let engagementRateSum = 0;

  for (const p of pagesWithData) {
    const g = p.ga4Data!;
    totalSessions += g.sessions;
    totalPageviews += g.pageviews;
    totalConversions += g.conversions;
    bounceRateSum += g.bounceRate;
    engagementRateSum += g.engagementRate;
  }

  const avgBounceRate = Math.round((bounceRateSum / pagesWithData.length) * 1000) / 1000;
  const avgEngagementRate = Math.round((engagementRateSum / pagesWithData.length) * 1000) / 1000;

  // Top pages by pageviews
  const topByPageviews = pagesWithData
    .map(p => ({
      url: p.url,
      pageviews: p.ga4Data!.pageviews,
      sessions: p.ga4Data!.sessions,
      bounceRate: p.ga4Data!.bounceRate,
    }))
    .sort((a, b) => b.pageviews - a.pageviews)
    .slice(0, 10);

  // No-traffic pages: indexed + indexable but 0 sessions
  const noTrafficPages = pages
    .filter(p => p.statusCode === 200 && p.indexability?.indexable && p.ga4Data && p.ga4Data.sessions === 0)
    .map(p => p.url);

  return {
    pagesWithData: pagesWithData.length,
    totalSessions,
    totalPageviews,
    totalConversions,
    avgBounceRate,
    avgEngagementRate,
    days,
    topByPageviews,
    noTrafficPages,
  };
}

function buildPlausibleStats(pages: PageData[], days: number): CrawlStats['plausibleStats'] {
  const pagesWithData = pages.filter(p => p.plausibleData !== null);
  if (pagesWithData.length === 0) return null;

  let totalVisitors = 0;
  let totalVisits = 0;
  let totalPageviews = 0;
  let totalConversions = 0;
  let bounceRateSum = 0;
  let visitDurationSum = 0;
  let scrollDepthSum = 0;
  let scrollDepthCount = 0;

  for (const p of pagesWithData) {
    const d = p.plausibleData!;
    totalVisitors += d.visitors;
    totalVisits += d.visits;
    totalPageviews += d.pageviews;
    totalConversions += d.conversions;
    bounceRateSum += d.bounceRate;
    visitDurationSum += d.visitDuration;
    if (d.scrollDepth != null) {
      scrollDepthSum += d.scrollDepth;
      scrollDepthCount++;
    }
  }

  const avgBounceRate = Math.round((bounceRateSum / pagesWithData.length) * 10) / 10;
  const avgVisitDuration = Math.round((visitDurationSum / pagesWithData.length) * 10) / 10;
  const avgScrollDepth = scrollDepthCount > 0
    ? Math.round((scrollDepthSum / scrollDepthCount) * 10) / 10
    : null;

  const topByPageviews = pagesWithData
    .map(p => ({
      url: p.url,
      pageviews: p.plausibleData!.pageviews,
      visitors: p.plausibleData!.visitors,
      bounceRate: p.plausibleData!.bounceRate,
    }))
    .sort((a, b) => b.pageviews - a.pageviews)
    .slice(0, 10);

  const noTrafficPages = pages
    .filter(p => p.statusCode === 200 && p.indexability?.indexable && p.plausibleData && p.plausibleData.visits === 0)
    .map(p => p.url);

  return {
    pagesWithData: pagesWithData.length,
    totalVisitors,
    totalVisits,
    totalPageviews,
    totalConversions,
    avgBounceRate,
    avgVisitDuration,
    avgScrollDepth,
    days,
    topByPageviews,
    noTrafficPages,
  };
}

export function printReport(stats: CrawlStats): void {
  const line = '─'.repeat(60);

  console.log(`\n${line}`);
  console.log('  Micelio — SEO Crawl Report');
  console.log(line);

  console.log(`\n  Total pages crawled:  ${stats.totalPages}`);
  if (stats.robotsBlockedCount > 0) {
    console.log(`  Blocked by robots.txt: ${stats.robotsBlockedCount}`);
  }
  console.log(`  Crawl duration:       ${(stats.crawlDurationMs / 1000).toFixed(1)}s`);

  // Status codes
  console.log(`\n  Status Codes:`);
  const sorted = Object.entries(stats.statusCodes).sort(([a], [b]) => Number(a) - Number(b));
  for (const [code, count] of sorted) {
    const pct = ((count / stats.totalPages) * 100).toFixed(1);
    console.log(`    ${code}: ${count} (${pct}%)`);
  }

  // Depth distribution
  console.log(`\n  Crawl Depth Distribution:`);
  const depthSorted = Object.entries(stats.depthDistribution).sort(([a], [b]) => Number(a) - Number(b));
  for (const [depth, count] of depthSorted) {
    console.log(`    Depth ${depth}: ${count} pages`);
  }

  // Response times
  console.log(`\n  Response Times:`);
  console.log(`    p50: ${stats.responseTimePercentiles.p50}ms`);
  console.log(`    p90: ${stats.responseTimePercentiles.p90}ms`);
  console.log(`    p99: ${stats.responseTimePercentiles.p99}ms`);

  if (stats.slowPages.length > 0) {
    console.log(`\n  Slow Pages (>3s): ${stats.slowPages.length}`);
    for (const sp of stats.slowPages.slice(0, 5)) {
      console.log(`    ${(sp.responseTimeMs / 1000).toFixed(1)}s — ${sp.url}`);
    }
  }

  // Metadata issues
  console.log(`\n  Metadata Issues:`);
  console.log(`    Missing title:        ${stats.pagesWithoutTitle}`);
  console.log(`    Missing description:  ${stats.pagesWithoutDescription}`);
  console.log(`    Missing H1:           ${stats.pagesWithoutH1}`);

  // Links
  console.log(`\n  Links:`);
  console.log(`    Internal links:  ${stats.totalInternalLinks}`);
  console.log(`    External links:  ${stats.totalExternalLinks}`);

  // Internal PageRank
  if (stats.pageRankScores.size > 0) {
    console.log(`\n  Internal PageRank (top 10):`);
    const sorted = Array.from(stats.pageRankScores.entries())
      .sort(([, a], [, b]) => b - a)
      .slice(0, 10);
    for (const [url, score] of sorted) {
      console.log(`    ${score.toFixed(2).padStart(5)} — ${url}`);
    }
  }

  // Images
  console.log(`\n  Images:`);
  console.log(`    Total images:     ${stats.totalImages}`);
  console.log(`    Missing alt text: ${stats.imagesMissingAlt}`);

  if (stats.imageAuditStats) {
    const ia = stats.imageAuditStats;
    console.log(`    Missing alt attribute: ${ia.missingAltAttribute}`);
    console.log(`    Empty alt (decorative): ${ia.emptyAlt}`);
    if (ia.altTooLong.length > 0) {
      console.log(`    Alt text >100 chars: ${ia.altTooLong.length}`);
      for (const img of ia.altTooLong.slice(0, 5)) {
        console.log(`      ${img.altLength} chars — ${img.src.substring(0, 80)}`);
        console.log(`        on: ${img.url}`);
      }
      if (ia.altTooLong.length > 5) console.log(`      ... and ${ia.altTooLong.length - 5} more`);
    }
    console.log(`    Missing width/height: ${ia.missingDimensions} (CLS risk)`);
    if (ia.oversizedImages.length > 0) {
      console.log(`    Oversized images (>100KB): ${ia.oversizedImages.length}`);
      for (const img of ia.oversizedImages.slice(0, 5)) {
        console.log(`      ${(img.sizeBytes / 1024).toFixed(1)} KB — ${img.src.substring(0, 80)}`);
        console.log(`        on: ${img.url}`);
      }
      if (ia.oversizedImages.length > 5) console.log(`      ... and ${ia.oversizedImages.length - 5} more`);
    }
    if (ia.totalImageBytes > 0) {
      console.log(`    Total image weight: ${(ia.totalImageBytes / (1024 * 1024)).toFixed(1)} MB`);
    }
  }

  // Broken internal links
  if (stats.brokenLinks.length > 0) {
    console.log(`\n  Broken Internal Links (${stats.brokenLinks.length}):`);
    for (const bl of stats.brokenLinks.slice(0, 20)) {
      console.log(`    [${bl.statusCode}] ${bl.url}`);
      if (bl.foundOn.length > 0) {
        console.log(`         ← linked from: ${bl.foundOn[0]}${bl.foundOn.length > 1 ? ` (+${bl.foundOn.length - 1} more)` : ''}`);
      }
    }
    if (stats.brokenLinks.length > 20) {
      console.log(`    ... and ${stats.brokenLinks.length - 20} more`);
    }
  }

  // Broken external links
  if (stats.brokenExternalLinks.length > 0) {
    console.log(`\n  Broken External Links (${stats.brokenExternalLinks.length}):`);
    for (const bl of stats.brokenExternalLinks.slice(0, 20)) {
      console.log(`    [${bl.statusCode}] ${bl.url}`);
      if (bl.foundOn.length > 0) {
        console.log(`         ← linked from: ${bl.foundOn[0]}${bl.foundOn.length > 1 ? ` (+${bl.foundOn.length - 1} more)` : ''}`);
      }
    }
    if (stats.brokenExternalLinks.length > 20) {
      console.log(`    ... and ${stats.brokenExternalLinks.length - 20} more`);
    }
  }

  // Redirect chains
  if (stats.redirectChains.length > 0) {
    console.log(`\n  Redirect Chains (${stats.redirectChains.length}):`);
    for (const rc of stats.redirectChains.slice(0, 10)) {
      const chain = rc.chain.map((h) => `[${h.statusCode}] ${h.url}`).join(' → ');
      console.log(`    ${chain} → ${rc.url}`);
    }
    if (stats.redirectChains.length > 10) {
      console.log(`    ... and ${stats.redirectChains.length - 10} more`);
    }
  }

  // Long redirect chains
  if (stats.longRedirectChains.length > 0) {
    console.log(`\n  Long Redirect Chains (>2 hops): ${stats.longRedirectChains.length}`);
    for (const lrc of stats.longRedirectChains.slice(0, 5)) {
      console.log(`    ${lrc.hops} hops — ${lrc.url}`);
    }
  }

  // Redirect analysis
  if (stats.redirectStats) {
    const rs = stats.redirectStats;
    console.log(`\n${line}`);
    console.log('  Redirect Chain Analysis');
    console.log(line);

    console.log(`\n  Overview:`);
    console.log(`    Pages with redirects:  ${rs.totalRedirects}`);
    console.log(`    Total redirect hops:   ${rs.totalHops}`);
    console.log(`    Avg chain length:      ${rs.avgChainLength}`);
    console.log(`    Max chain length:      ${rs.maxChainLength}`);

    // Breakdown by status code
    const statusEntries = Object.entries(rs.byStatusCode).sort(([a], [b]) => Number(a) - Number(b));
    if (statusEntries.length > 0) {
      console.log(`\n  Redirect Types:`);
      for (const [code, count] of statusEntries) {
        const label = Number(code) === 301 ? 'Permanent' :
          Number(code) === 302 ? 'Found (temporary)' :
          Number(code) === 303 ? 'See Other' :
          Number(code) === 307 ? 'Temporary' :
          Number(code) === 308 ? 'Permanent' : 'Other';
        console.log(`    ${code} (${label}): ${count} hops`);
      }
    }

    // Redirect patterns
    const patterns = [
      { label: 'HTTP -> HTTPS', count: rs.httpToHttps.length },
      { label: 'www normalization', count: rs.wwwNormalization.length },
      { label: 'Trailing slash', count: rs.trailingSlash.length },
      { label: 'Cross-domain', count: rs.crossDomain.length },
    ].filter(p => p.count > 0);

    if (patterns.length > 0) {
      console.log(`\n  Redirect Patterns:`);
      for (const p of patterns) {
        console.log(`    ${p.label}: ${p.count}`);
      }
    }

    // Problematic redirects
    if (rs.selfRedirects.length > 0) {
      console.log(`\n  Self-Redirects: ${rs.selfRedirects.length}`);
      for (const url of rs.selfRedirects.slice(0, 5)) {
        console.log(`    ${url}`);
      }
      if (rs.selfRedirects.length > 5) {
        console.log(`    ... and ${rs.selfRedirects.length - 5} more`);
      }
    }

    if (rs.chainLoops.length > 0) {
      console.log(`\n  Redirect Loops: ${rs.chainLoops.length}`);
      for (const loop of rs.chainLoops.slice(0, 5)) {
        const chain = loop.chain.map(h => `[${h.statusCode}] ${h.url}`).join(' -> ');
        console.log(`    ${chain}`);
      }
      if (rs.chainLoops.length > 5) {
        console.log(`    ... and ${rs.chainLoops.length - 5} more`);
      }
    }

    if (rs.temporaryRedirects.length > 0) {
      console.log(`\n  Temporary Redirects (302/307): ${rs.temporaryRedirects.length}`);
      for (const tr of rs.temporaryRedirects.slice(0, 5)) {
        const chain = tr.chain.map(h => `[${h.statusCode}] ${h.url}`).join(' -> ');
        console.log(`    ${chain}`);
      }
      if (rs.temporaryRedirects.length > 5) {
        console.log(`    ... and ${rs.temporaryRedirects.length - 5} more`);
      }
    }

    // Top redirect targets
    if (rs.topRedirectTargets.length > 0) {
      console.log(`\n  Top Redirect Targets (URLs receiving most redirects):`);
      for (const target of rs.topRedirectTargets.slice(0, 5)) {
        console.log(`    ${target.count}x — ${target.url}`);
      }
    }

    // Longest chains with full detail
    if (rs.longestChains.length > 0 && rs.maxChainLength > 1) {
      console.log(`\n  Longest Chains:`);
      for (const lc of rs.longestChains.slice(0, 5)) {
        const chain = lc.chain.map(h => `[${h.statusCode}] ${h.url}`).join(' -> ');
        console.log(`    ${lc.chain.length} hops: ${chain} -> ${lc.finalUrl}`);
      }
    }
  }

  // Canonical tag validation
  if (stats.canonicalStats) {
    const cs = stats.canonicalStats;
    console.log(`\n${line}`);
    console.log('  Canonical Tag Validation');
    console.log(line);

    console.log(`\n  Overview:`);
    console.log(`    Pages with canonical:     ${cs.totalWithCanonical}`);
    console.log(`    Pages without canonical:  ${cs.totalWithoutCanonical}`);
    console.log(`    Self-referencing:          ${cs.selfReferencing}`);
    console.log(`    Canonicalized (→ other):   ${cs.canonicalized}`);

    if (cs.multipleCanonicals > 0) {
      console.log(`\n  Multiple Canonical Tags: ${cs.multipleCanonicals}`);
    }

    if (cs.relativeCanonicals.length > 0) {
      console.log(`\n  Relative Canonical URLs: ${cs.relativeCanonicals.length}`);
      for (const rc of cs.relativeCanonicals.slice(0, 5)) {
        console.log(`    ${rc.url}`);
        console.log(`      raw href: "${rc.rawHref}"`);
      }
      if (cs.relativeCanonicals.length > 5) {
        console.log(`    ... and ${cs.relativeCanonicals.length - 5} more`);
      }
    }

    if (cs.canonicalChains.length > 0) {
      console.log(`\n  Canonical Chains: ${cs.canonicalChains.length}`);
      for (const cc of cs.canonicalChains.slice(0, 5)) {
        console.log(`    ${cc.chain.join(' → ')}`);
      }
      if (cs.canonicalChains.length > 5) {
        console.log(`    ... and ${cs.canonicalChains.length - 5} more`);
      }
    }

    if (cs.canonicalLoops.length > 0) {
      console.log(`\n  Canonical Loops: ${cs.canonicalLoops.length}`);
      for (const cl of cs.canonicalLoops.slice(0, 5)) {
        console.log(`    ${cl.loop.join(' → ')}`);
      }
      if (cs.canonicalLoops.length > 5) {
        console.log(`    ... and ${cs.canonicalLoops.length - 5} more`);
      }
    }

    if (cs.canonicalToNon200.length > 0) {
      console.log(`\n  Canonical Pointing to Non-200: ${cs.canonicalToNon200.length}`);
      for (const cn of cs.canonicalToNon200.slice(0, 5)) {
        console.log(`    ${cn.url}`);
        console.log(`      → [${cn.targetStatus}] ${cn.canonical}`);
      }
      if (cs.canonicalToNon200.length > 5) {
        console.log(`    ... and ${cs.canonicalToNon200.length - 5} more`);
      }
    }

    if (cs.canonicalToNonIndexable.length > 0) {
      console.log(`\n  Canonical Pointing to Non-Indexable: ${cs.canonicalToNonIndexable.length}`);
      for (const ci of cs.canonicalToNonIndexable.slice(0, 5)) {
        console.log(`    ${ci.url} → ${ci.canonical}`);
      }
      if (cs.canonicalToNonIndexable.length > 5) {
        console.log(`    ... and ${cs.canonicalToNonIndexable.length - 5} more`);
      }
    }

    if (cs.crossDomain.length > 0) {
      console.log(`\n  Cross-Domain Canonicals: ${cs.crossDomain.length}`);
      for (const cd of cs.crossDomain.slice(0, 5)) {
        console.log(`    ${cd.url} → ${cd.canonical}`);
      }
      if (cs.crossDomain.length > 5) {
        console.log(`    ... and ${cs.crossDomain.length - 5} more`);
      }
    }

    if (cs.httpHttpsMismatch.length > 0) {
      console.log(`\n  HTTP/HTTPS Mismatch: ${cs.httpHttpsMismatch.length}`);
      for (const hm of cs.httpHttpsMismatch.slice(0, 5)) {
        console.log(`    ${hm.url} → ${hm.canonical}`);
      }
      if (cs.httpHttpsMismatch.length > 5) {
        console.log(`    ... and ${cs.httpHttpsMismatch.length - 5} more`);
      }
    }

    if (cs.canonicalWithQueryString.length > 0) {
      console.log(`\n  Canonicals with Query Strings: ${cs.canonicalWithQueryString.length}`);
      for (const qs of cs.canonicalWithQueryString.slice(0, 5)) {
        console.log(`    ${qs.url} → ${qs.canonical}`);
      }
      if (cs.canonicalWithQueryString.length > 5) {
        console.log(`    ... and ${cs.canonicalWithQueryString.length - 5} more`);
      }
    }

    if (cs.topCanonicalTargets.length > 0 && cs.canonicalized > 0) {
      console.log(`\n  Top Canonical Targets:`);
      for (const target of cs.topCanonicalTargets.slice(0, 5)) {
        console.log(`    ${target.count}x — ${target.url}`);
      }
    }
  }

  // Orphan pages
  if (stats.orphanPages.length > 0) {
    console.log(`\n  Orphan Pages (0 inlinks): ${stats.orphanPages.length}`);
    for (const op of stats.orphanPages.slice(0, 10)) {
      console.log(`    ${op}`);
    }
    if (stats.orphanPages.length > 10) {
      console.log(`    ... and ${stats.orphanPages.length - 10} more`);
    }
  }

  // ── Phase 2: Enhanced SEO ──────────────────────────────────

  console.log(`\n${line}`);
  console.log('  Enhanced SEO Analysis');
  console.log(line);

  // Structured Data
  console.log(`\n  Structured Data:`);
  console.log(`    Pages with structured data: ${stats.pagesWithStructuredData}/${stats.totalPages}`);

  // Open Graph
  console.log(`\n  Social Meta Tags:`);
  console.log(`    Pages without Open Graph: ${stats.pagesWithoutOg}`);

  // Hreflang
  if (stats.hreflangIssues.length > 0) {
    console.log(`\n  Hreflang Issues (${stats.hreflangIssues.length}):`);
    for (const issue of stats.hreflangIssues.slice(0, 10)) {
      console.log(`    ${issue.issue}`);
      console.log(`      → ${issue.url}`);
    }
    if (stats.hreflangIssues.length > 10) {
      console.log(`    ... and ${stats.hreflangIssues.length - 10} more`);
    }
  }

  // Thin content
  if (stats.thinContentPages.length > 0) {
    console.log(`\n  Thin Content (<200 words): ${stats.thinContentPages.length}`);
    for (const tc of stats.thinContentPages.slice(0, 10)) {
      console.log(`    ${tc.wordCount} words — ${tc.url}`);
    }
    if (stats.thinContentPages.length > 10) {
      console.log(`    ... and ${stats.thinContentPages.length - 10} more`);
    }
  }

  // Duplicate content
  if (stats.duplicateContentGroups.length > 0) {
    console.log(`\n  Duplicate Content Groups: ${stats.duplicateContentGroups.length}`);
    for (const group of stats.duplicateContentGroups.slice(0, 5)) {
      console.log(`    Hash: ${group.hash.substring(0, 8)}... (${group.urls.length} pages)`);
      for (const url of group.urls.slice(0, 3)) {
        console.log(`      ${url}`);
      }
      if (group.urls.length > 3) {
        console.log(`      ... and ${group.urls.length - 3} more`);
      }
    }
  }

  // Non-descriptive anchors
  if (stats.nonDescriptiveAnchors.length > 0) {
    console.log(`\n  Non-Descriptive Anchor Text: ${stats.nonDescriptiveAnchors.length}`);
    for (const a of stats.nonDescriptiveAnchors.slice(0, 10)) {
      console.log(`    "${a.text}" → ${a.url}`);
      console.log(`      found on: ${a.foundOn}`);
    }
    if (stats.nonDescriptiveAnchors.length > 10) {
      console.log(`    ... and ${stats.nonDescriptiveAnchors.length - 10} more`);
    }
  }

  // Security
  console.log(`\n  Security:`);
  if (stats.nonHttpsPages.length > 0) {
    console.log(`    Non-HTTPS pages: ${stats.nonHttpsPages.length}`);
    for (const url of stats.nonHttpsPages.slice(0, 5)) {
      console.log(`      ${url}`);
    }
  } else {
    console.log(`    All pages served over HTTPS ✓`);
  }
  if (stats.mixedContentPages.length > 0) {
    console.log(`    Mixed content pages: ${stats.mixedContentPages.length}`);
    for (const url of stats.mixedContentPages.slice(0, 5)) {
      console.log(`      ${url}`);
    }
  } else {
    console.log(`    No mixed content detected ✓`);
  }

  // ── Phase 3: Custom Extraction ──────────────────────────────
  const searchNames = Object.keys(stats.customSearchSummary);
  if (searchNames.length > 0) {
    console.log(`\n${line}`);
    console.log('  Custom Search Results');
    console.log(line);
    for (const name of searchNames) {
      const s = stats.customSearchSummary[name];
      console.log(`\n    "${name}": found on ${s.found}/${s.total} pages`);
    }
  }

  // ── Phase 4: Integrations ──────────────────────────────────
  const hasPhase4 = stats.performanceScores || stats.pagesWithAiAnalysis > 0;
  if (hasPhase4) {
    console.log(`\n${line}`);
    console.log('  Integrations');
    console.log(line);

    if (stats.performanceScores) {
      console.log(`\n  PageSpeed Insights (Mobile):`);
      console.log(`    Avg performance score: ${stats.performanceScores.avg}/100`);
      console.log(`    Min: ${stats.performanceScores.min}/100  Max: ${stats.performanceScores.max}/100`);
    }

    if (stats.pagesWithAiAnalysis > 0) {
      console.log(`\n  AI Analysis:`);
      console.log(`    Pages analyzed: ${stats.pagesWithAiAnalysis}/${stats.totalPages}`);
    }
  }

  // ── Phase 6: Sitemap Audit ──────────────────────────────────
  if (stats.sitemapStats) {
    console.log(`\n${line}`);
    console.log('  Sitemap Audit');
    console.log(line);

    const sm = stats.sitemapStats;
    console.log(`\n  Total URLs in sitemap(s): ${sm.totalSitemapUrls}`);

    // Phase 6.3: Coverage summary
    if (sm.coverage) {
      const total = sm.coverage.crawledAndInSitemap + sm.coverage.crawledNotInSitemap + sm.coverage.inSitemapNotCrawled;
      console.log(`\n  Coverage:`);
      console.log(`    Crawled & in sitemap:     ${sm.coverage.crawledAndInSitemap}`);
      console.log(`    Crawled, not in sitemap:  ${sm.coverage.crawledNotInSitemap}`);
      console.log(`    In sitemap, not crawled:  ${sm.coverage.inSitemapNotCrawled}`);
      if (sm.totalSitemapUrls > 0) {
        const pct = Math.round((sm.coverage.crawledAndInSitemap / sm.totalSitemapUrls) * 100);
        console.log(`    Sitemap coverage:         ${pct}%`);
      }
    }

    // Show extension counts if any were found
    if (sm.newsEntryCount > 0 || sm.videoEntryCount > 0 || sm.imageEntryCount > 0) {
      console.log('\n  Sitemap Extensions:');
      if (sm.newsEntryCount > 0) console.log(`    News entries:  ${sm.newsEntryCount}`);
      if (sm.videoEntryCount > 0) console.log(`    Video entries: ${sm.videoEntryCount}`);
      if (sm.imageEntryCount > 0) console.log(`    Image entries: ${sm.imageEntryCount}`);
    }

    // Phase 6.2: Lastmod freshness
    if (sm.lastmodStats) {
      const lm = sm.lastmodStats;
      const hasIssues = lm.missing > 0 || lm.stale > 0 || lm.future > 0 || lm.invalid > 0;
      if (hasIssues) {
        console.log(`\n  Lastmod Analysis:`);
        if (lm.missing > 0) console.log(`    Missing lastmod:  ${lm.missing}`);
        if (lm.stale > 0)   console.log(`    Stale (>1 year):  ${lm.stale}`);
        if (lm.future > 0)  console.log(`    Future dates:     ${lm.future}`);
        if (lm.invalid > 0) console.log(`    Invalid format:   ${lm.invalid}`);
      }
    }

    // Phase 6.2: Status breakdown for sitemap URLs
    if (sm.statusBreakdown && Object.keys(sm.statusBreakdown).length > 1) {
      console.log(`\n  Status Codes (sitemap URLs):`);
      for (const [code, count] of Object.entries(sm.statusBreakdown).sort()) {
        console.log(`    ${code}: ${count}`);
      }
    }

    // Phase 6.2: Redirects in sitemap
    if (sm.redirectsInSitemap.length > 0) {
      console.log(`\n  Redirecting URLs in sitemap: ${sm.redirectsInSitemap.length}`);
      for (const url of sm.redirectsInSitemap.slice(0, 10)) {
        console.log(`    ${url}`);
      }
      if (sm.redirectsInSitemap.length > 10) {
        console.log(`    ... and ${sm.redirectsInSitemap.length - 10} more`);
      }
    }

    if (sm.orphanUrls.length > 0) {
      console.log(`\n  Orphan URLs (in sitemap, 0 internal links): ${sm.orphanUrls.length}`);
      for (const url of sm.orphanUrls.slice(0, 10)) {
        console.log(`    ${url}`);
      }
      if (sm.orphanUrls.length > 10) {
        console.log(`    ... and ${sm.orphanUrls.length - 10} more`);
      }
    }

    if (sm.missingFromSitemap.length > 0) {
      console.log(`\n  Missing from sitemap (crawled, indexable, not in sitemap): ${sm.missingFromSitemap.length}`);
      for (const url of sm.missingFromSitemap.slice(0, 10)) {
        console.log(`    ${url}`);
      }
      if (sm.missingFromSitemap.length > 10) {
        console.log(`    ... and ${sm.missingFromSitemap.length - 10} more`);
      }
    }

    if (sm.nonIndexableInSitemap.length > 0) {
      console.log(`\n  Non-indexable in sitemap: ${sm.nonIndexableInSitemap.length}`);
      for (const url of sm.nonIndexableInSitemap.slice(0, 10)) {
        console.log(`    ${url}`);
      }
      if (sm.nonIndexableInSitemap.length > 10) {
        console.log(`    ... and ${sm.nonIndexableInSitemap.length - 10} more`);
      }
    }

    // Phase 6.3: Duplicate URLs across sitemaps
    if (sm.duplicateAcrossSitemaps.length > 0) {
      console.log(`\n  URLs in multiple sitemaps: ${sm.duplicateAcrossSitemaps.length}`);
      for (const dup of sm.duplicateAcrossSitemaps.slice(0, 5)) {
        console.log(`    ${dup.url}`);
        console.log(`      Found in: ${dup.sources.join(', ')}`);
      }
      if (sm.duplicateAcrossSitemaps.length > 5) {
        console.log(`    ... and ${sm.duplicateAcrossSitemaps.length - 5} more`);
      }
    }

    // Phase 6.3: Uncrawled sitemap URLs
    if (sm.uncrawledSitemapUrls.length > 0) {
      console.log(`\n  Uncrawled sitemap URLs (not reached during crawl): ${sm.uncrawledSitemapUrls.length}`);
      for (const url of sm.uncrawledSitemapUrls.slice(0, 10)) {
        console.log(`    ${url}`);
      }
      if (sm.uncrawledSitemapUrls.length > 10) {
        console.log(`    ... and ${sm.uncrawledSitemapUrls.length - 10} more`);
      }
    }

    if (sm.sitemapErrors.length > 0) {
      console.log(`\n  Sitemap Errors:`);
      for (const err of sm.sitemapErrors) {
        console.log(`    ${err}`);
      }
    }

    // Phase 6.2: Validation warnings
    if (sm.validationWarnings.length > 0) {
      console.log(`\n  Validation Warnings: ${sm.validationWarnings.length}`);
      for (const w of sm.validationWarnings.slice(0, 10)) {
        console.log(`    ${w}`);
      }
      if (sm.validationWarnings.length > 10) {
        console.log(`    ... and ${sm.validationWarnings.length - 10} more`);
      }
    }
  }

  // ── Phase 6: Page Weight ──────────────────────────────────
  if (stats.pageWeightStats) {
    console.log(`\n${line}`);
    console.log('  Page Weight Analysis');
    console.log(line);

    const pw = stats.pageWeightStats;
    console.log(`\n  Average total page weight: ${formatBytes(pw.avgTotalBytes)}`);

    console.log(`\n  Weight by resource type:`);
    const typeSorted = Object.entries(pw.byType).sort(([, a], [, b]) => b.bytes - a.bytes);
    for (const [type, data] of typeSorted) {
      console.log(`    ${type.padEnd(12)} ${data.count.toString().padStart(5)} files  ${formatBytes(data.bytes).padStart(10)}`);
    }

    if (pw.heaviestPages.length > 0) {
      console.log(`\n  Heaviest pages (top 10):`);
      for (const hp of pw.heaviestPages.slice(0, 10)) {
        console.log(`    ${formatBytes(hp.totalBytes).padStart(10)} — ${hp.url}`);
      }
    }

    if (pw.oversizedPages.length > 0) {
      console.log(`\n  Oversized pages (>3MB): ${pw.oversizedPages.length}`);
      for (const op of pw.oversizedPages.slice(0, 5)) {
        console.log(`    ${formatBytes(op.totalBytes)} — ${op.url}`);
      }
    }
  }

  // ── Phase 7: Content & Indexability ──────────────────────────────
  const hasPhase7 = stats.indexabilityStats.nonIndexable > 0 ||
    stats.nearDuplicateGroups.length > 0 ||
    stats.readabilityStats ||
    Object.keys(stats.urlIssueStats).length > 0 ||
    stats.soft404Pages.length > 0 ||
    stats.linkAnalysis.deadEndPages.length > 0 ||
    stats.textToCodeStats;

  if (hasPhase7) {
    console.log(`\n${line}`);
    console.log('  Content & Indexability Analysis');
    console.log(line);

    // 7.1 Indexability
    console.log(`\n  Indexability:`);
    console.log(`    Indexable:      ${stats.indexabilityStats.indexable}/${stats.totalPages}`);
    console.log(`    Non-indexable:  ${stats.indexabilityStats.nonIndexable}/${stats.totalPages}`);
    const reasons = Object.entries(stats.indexabilityStats.reasons).sort(([, a], [, b]) => b - a);
    if (reasons.length > 0) {
      console.log(`    Reasons:`);
      for (const [reason, count] of reasons.slice(0, 10)) {
        console.log(`      ${count}x — ${reason}`);
      }
    }

    // 7.2 Near-duplicates
    if (stats.nearDuplicateGroups.length > 0) {
      console.log(`\n  Near-Duplicate Content Groups: ${stats.nearDuplicateGroups.length}`);
      for (const group of stats.nearDuplicateGroups.slice(0, 5)) {
        console.log(`    ~${group.similarity}% similar (${group.urls.length} pages):`);
        for (const url of group.urls.slice(0, 3)) {
          console.log(`      ${url}`);
        }
        if (group.urls.length > 3) {
          console.log(`      ... and ${group.urls.length - 3} more`);
        }
      }
      if (stats.nearDuplicateGroups.length > 5) {
        console.log(`    ... and ${stats.nearDuplicateGroups.length - 5} more groups`);
      }
    }

    // 7.3 Readability
    if (stats.readabilityStats) {
      console.log(`\n  Readability (Flesch-Kincaid Reading Ease):`);
      console.log(`    Average score: ${stats.readabilityStats.avgScore}/100`);
      if (stats.readabilityStats.difficult.length > 0) {
        console.log(`    Difficult to read (<30): ${stats.readabilityStats.difficult.length}`);
        for (const p of stats.readabilityStats.difficult.slice(0, 5)) {
          console.log(`      ${p.score.toFixed(1)} — ${p.url}`);
        }
      }
      if (stats.readabilityStats.veryEasy.length > 0) {
        console.log(`    Very easy to read (>80): ${stats.readabilityStats.veryEasy.length}`);
      }
    }

    // 7.4 URL issues
    const urlIssueNames = Object.keys(stats.urlIssueStats);
    if (urlIssueNames.length > 0) {
      console.log(`\n  URL Issues:`);
      for (const issue of urlIssueNames.sort()) {
        const urls = stats.urlIssueStats[issue];
        console.log(`    ${issue.replace(/_/g, ' ')}: ${urls.length} URLs`);
        for (const url of urls.slice(0, 3)) {
          console.log(`      ${url}`);
        }
        if (urls.length > 3) {
          console.log(`      ... and ${urls.length - 3} more`);
        }
      }
    }

    // 7.5 Soft 404
    if (stats.soft404Pages.length > 0) {
      console.log(`\n  Suspected Soft 404 Pages: ${stats.soft404Pages.length}`);
      for (const url of stats.soft404Pages.slice(0, 10)) {
        console.log(`    ${url}`);
      }
      if (stats.soft404Pages.length > 10) {
        console.log(`    ... and ${stats.soft404Pages.length - 10} more`);
      }
    }

    // 7.6 Link analysis
    const la = stats.linkAnalysis;
    if (la.deadEndPages.length > 0 || la.nofollowedInternalLinks > 0 || la.linksToNonIndexable.length > 0) {
      console.log(`\n  Link Analysis:`);
      console.log(`    Followed internal links:    ${la.followedInternalLinks}`);
      console.log(`    Nofollowed internal links:   ${la.nofollowedInternalLinks}`);

      if (la.deadEndPages.length > 0) {
        console.log(`\n    Dead-end pages (0 outgoing internal links): ${la.deadEndPages.length}`);
        for (const url of la.deadEndPages.slice(0, 5)) {
          console.log(`      ${url}`);
        }
        if (la.deadEndPages.length > 5) {
          console.log(`      ... and ${la.deadEndPages.length - 5} more`);
        }
      }

      if (la.linksToNonIndexable.length > 0) {
        console.log(`\n    Links to non-indexable pages: ${la.linksToNonIndexable.length}`);
        for (const link of la.linksToNonIndexable.slice(0, 5)) {
          console.log(`      ${link.from} → ${link.to}`);
        }
        if (la.linksToNonIndexable.length > 5) {
          console.log(`      ... and ${la.linksToNonIndexable.length - 5} more`);
        }
      }
    }

    // 7.7 Text-to-code ratio
    if (stats.textToCodeStats) {
      console.log(`\n  Text-to-Code Ratio:`);
      console.log(`    Average ratio: ${stats.textToCodeStats.avgRatio}%`);
      if (stats.textToCodeStats.contentPoor.length > 0) {
        console.log(`    Content-poor pages (<10%): ${stats.textToCodeStats.contentPoor.length}`);
        for (const p of stats.textToCodeStats.contentPoor.slice(0, 5)) {
          console.log(`      ${p.ratio.toFixed(1)}% — ${p.url}`);
        }
      }
    }
  }

  // ── Phase 9: Schema Validation ────────────────────────────────
  if (stats.schemaValidationStats) {
    const sv = stats.schemaValidationStats;
    console.log(`\n${line}`);
    console.log('  Schema Validation & Rich Results');
    console.log(line);

    console.log(`\n  Overview:`);
    console.log(`    Pages with structured data: ${sv.pagesWithSchema}/${stats.totalPages}`);
    console.log(`    Valid (no errors):          ${sv.pagesWithValidSchema}`);
    console.log(`    With errors:                ${sv.pagesWithErrors}`);

    // Type distribution
    const typeEntries = Object.entries(sv.typeDistribution).sort(([, a], [, b]) => b - a);
    if (typeEntries.length > 0) {
      console.log(`\n  Schema types found:`);
      for (const [type, count] of typeEntries.slice(0, 15)) {
        console.log(`    ${count}x ${type}`);
      }
    }

    // Rich result eligibility
    const richEntries = Object.entries(sv.richResultEligible).sort(([, a], [, b]) => b - a);
    if (richEntries.length > 0) {
      console.log(`\n  Rich result eligible:`);
      for (const [type, count] of richEntries) {
        console.log(`    ${type}: ${count} page(s)`);
      }
    } else {
      console.log(`\n  Rich result eligible: 0 pages`);
    }

    // Top issues
    if (sv.topIssues.length > 0) {
      console.log(`\n  Top validation issues:`);
      for (const issue of sv.topIssues.slice(0, 10)) {
        console.log(`    ${issue.count}x — ${issue.message}`);
      }
    }
  }

  // ── Phase 11: GSC Stats ──────────────────────────────────────
  if (stats.gscStats) {
    const gsc = stats.gscStats;
    console.log(`\n${line}`);
    console.log('  Google Search Console Data');
    console.log(line);

    console.log(`\n  Overview (last ${gsc.days} days):`);
    console.log(`    Pages with GSC data:  ${gsc.pagesWithData}/${stats.totalPages}`);
    console.log(`    Total impressions:    ${gsc.totalImpressions.toLocaleString()}`);
    console.log(`    Total clicks:         ${gsc.totalClicks.toLocaleString()}`);
    console.log(`    Average CTR:          ${(gsc.avgCtr * 100).toFixed(1)}%`);
    console.log(`    Average position:     ${gsc.avgPosition}`);

    if (gsc.topByClicks.length > 0) {
      console.log(`\n  Top pages by clicks:`);
      for (const p of gsc.topByClicks.slice(0, 10)) {
        console.log(`    ${p.clicks} clicks, ${p.impressions} imp, pos ${p.position} — ${p.url}`);
      }
    }

    if (gsc.zombiePages.length > 0) {
      console.log(`\n  Zombie pages (visible in search, 0 clicks): ${gsc.zombiePages.length}`);
      for (const url of gsc.zombiePages.slice(0, 5)) {
        console.log(`    ${url}`);
      }
      if (gsc.zombiePages.length > 5) {
        console.log(`    ... and ${gsc.zombiePages.length - 5} more`);
      }
    }
  }

  // ── Phase 11: GA4 Stats ──────────────────────────────────────
  if (stats.ga4Stats) {
    const ga4 = stats.ga4Stats;
    console.log(`\n${line}`);
    console.log('  Google Analytics 4 Data');
    console.log(line);

    console.log(`\n  Overview (last ${ga4.days} days):`);
    console.log(`    Pages with GA4 data:  ${ga4.pagesWithData}/${stats.totalPages}`);
    console.log(`    Total sessions:       ${ga4.totalSessions.toLocaleString()}`);
    console.log(`    Total pageviews:      ${ga4.totalPageviews.toLocaleString()}`);
    console.log(`    Total conversions:    ${ga4.totalConversions.toLocaleString()}`);
    console.log(`    Avg bounce rate:      ${(ga4.avgBounceRate * 100).toFixed(1)}%`);
    console.log(`    Avg engagement rate:  ${(ga4.avgEngagementRate * 100).toFixed(1)}%`);

    if (ga4.topByPageviews.length > 0) {
      console.log(`\n  Top pages by pageviews:`);
      for (const p of ga4.topByPageviews.slice(0, 10)) {
        console.log(`    ${p.pageviews} views, ${p.sessions} sessions, ${(p.bounceRate * 100).toFixed(0)}% bounce — ${p.url}`);
      }
    }

    if (ga4.noTrafficPages.length > 0) {
      console.log(`\n  No-traffic pages (indexed, 0 sessions): ${ga4.noTrafficPages.length}`);
      for (const url of ga4.noTrafficPages.slice(0, 5)) {
        console.log(`    ${url}`);
      }
      if (ga4.noTrafficPages.length > 5) {
        console.log(`    ... and ${ga4.noTrafficPages.length - 5} more`);
      }
    }
  }

  // ── Plausible Analytics ──────────────────────────────────
  if (stats.plausibleStats) {
    const pl = stats.plausibleStats;
    console.log(`\n${line}`);
    console.log('  Plausible Analytics');
    console.log(line);

    console.log(`\n  Overview (last ${pl.days} days):`);
    console.log(`    Pages with data:      ${pl.pagesWithData}/${stats.totalPages}`);
    console.log(`    Total visitors:       ${pl.totalVisitors.toLocaleString()}`);
    console.log(`    Total visits:         ${pl.totalVisits.toLocaleString()}`);
    console.log(`    Total pageviews:      ${pl.totalPageviews.toLocaleString()}`);
    console.log(`    Total conversions:    ${pl.totalConversions.toLocaleString()}`);
    console.log(`    Avg bounce rate:      ${pl.avgBounceRate.toFixed(1)}%`);
    console.log(`    Avg visit duration:   ${pl.avgVisitDuration.toFixed(1)}s`);
    if (pl.avgScrollDepth != null) {
      console.log(`    Avg scroll depth:     ${pl.avgScrollDepth.toFixed(1)}%`);
    }

    if (pl.topByPageviews.length > 0) {
      console.log(`\n  Top pages by pageviews:`);
      for (const p of pl.topByPageviews.slice(0, 10)) {
        console.log(`    ${p.pageviews} views, ${p.visitors} visitors, ${p.bounceRate.toFixed(0)}% bounce — ${p.url}`);
      }
    }

    if (pl.noTrafficPages.length > 0) {
      console.log(`\n  No-traffic pages (indexed, 0 visits): ${pl.noTrafficPages.length}`);
      for (const url of pl.noTrafficPages.slice(0, 5)) {
        console.log(`    ${url}`);
      }
      if (pl.noTrafficPages.length > 5) {
        console.log(`    ... and ${pl.noTrafficPages.length - 5} more`);
      }
    }
  }

  // ── Phase 12: Render Comparison ──────────────────────────────────
  if (stats.renderCompareStats) {
    const rc = stats.renderCompareStats;
    console.log(`\n${line}`);
    console.log('  Rendered vs Original HTML');
    console.log(line);

    console.log(`\n  Pages compared: ${rc.pagesCompared}`);
    console.log(`  Pages with differences: ${rc.pagesWithDiffs}`);

    if (Object.keys(rc.fieldDiffCounts).length > 0) {
      console.log(`\n  Differences by field:`);
      const sorted = Object.entries(rc.fieldDiffCounts).sort(([, a], [, b]) => b - a);
      for (const [field, count] of sorted) {
        const label = field.replace(/_/g, ' ');
        console.log(`    ${label}: ${count} pages`);
      }
    }

    if (rc.criticalDiffs.length > 0) {
      console.log(`\n  Critical rendering differences:`);
      for (const d of rc.criticalDiffs.slice(0, 10)) {
        console.log(`    [${d.field}] ${d.url}`);
        console.log(`      Original: ${d.original || '(empty)'}`);
        console.log(`      Rendered: ${d.rendered || '(empty)'}`);
      }
      if (rc.criticalDiffs.length > 10) {
        console.log(`    ... and ${rc.criticalDiffs.length - 10} more`);
      }
    }
  }

  // ── Phase 12: Segmentation ──────────────────────────────────────
  if (stats.segmentStats && stats.segmentStats.length > 0) {
    console.log(`\n${line}`);
    console.log('  URL Segments');
    console.log(line);

    for (const seg of stats.segmentStats) {
      console.log(`\n  ${seg.name} (${seg.pageCount} pages):`);
      // Status codes
      const statusEntries = Object.entries(seg.statusCodes).sort(([a], [b]) => Number(a) - Number(b));
      const statusStr = statusEntries.map(([code, count]) => `${code}:${count}`).join(', ');
      console.log(`    Status: ${statusStr}`);
      console.log(`    Avg response: ${seg.avgResponseTimeMs}ms`);
      console.log(`    Indexable: ${seg.indexable}/${seg.pageCount}`);
      console.log(`    Avg words: ${seg.avgWordCount}`);
      console.log(`    Links: ${seg.totalInternalLinks} internal, ${seg.totalExternalLinks} external`);
      if (seg.pagesWithErrors > 0) {
        console.log(`    Errors: ${seg.pagesWithErrors}`);
      }
      if (seg.totalImpressions > 0 || seg.totalClicks > 0) {
        console.log(`    GSC: ${seg.totalImpressions.toLocaleString()} impressions, ${seg.totalClicks.toLocaleString()} clicks`);
      }
      if (seg.totalSessions > 0 || seg.totalPageviews > 0) {
        console.log(`    GA4: ${seg.totalSessions.toLocaleString()} sessions, ${seg.totalPageviews.toLocaleString()} pageviews`);
      }
    }
  }

  // ── Phase 13: N-Grams ──────────────────────────────────────────
  if (stats.ngramStats) {
    const ng = stats.ngramStats;
    console.log(`\n${line}`);
    console.log('  N-Gram Analysis');
    console.log(line);

    console.log(`\n  Analyzed ${ng.totalPages} pages, ${ng.totalTokens.toLocaleString()} tokens\n`);

    const printNgrams = (label: string, entries: typeof ng.unigrams) => {
      if (entries.length === 0) return;
      console.log(`  ${label} (top ${Math.min(entries.length, 20)}):`);
      console.log(`    ${'Term'.padEnd(30)} ${'Count'.padStart(7)} ${'Pages'.padStart(7)} ${'TF-IDF'.padStart(8)}`);
      console.log(`    ${'─'.repeat(30)} ${'─'.repeat(7)} ${'─'.repeat(7)} ${'─'.repeat(8)}`);
      for (const e of entries.slice(0, 20)) {
        const term = e.term.length > 28 ? e.term.substring(0, 28) + '..' : e.term;
        console.log(
          `    ${term.padEnd(30)} ${e.count.toString().padStart(7)} ${e.pages.toString().padStart(7)} ${e.tfidf.toFixed(4).padStart(8)}`,
        );
      }
    };

    printNgrams('Unigrams (single words)', ng.unigrams);
    console.log('');
    printNgrams('Bigrams (two-word phrases)', ng.bigrams);
    console.log('');
    printNgrams('Trigrams (three-word phrases)', ng.trigrams);
  }

  // ── Phase 13.2: Semantic Similarity ──────────────────────────────
  if (stats.embeddingStats) {
    const es = stats.embeddingStats;
    console.log(`\n${line}`);
    console.log('  Semantic Similarity Analysis');
    console.log(line);

    console.log(`\n  Provider: ${es.provider} (${es.model}, ${es.dimensions} dimensions)`);
    console.log(`  Pages embedded: ${es.pagesEmbedded}`);
    console.log(`  Similar pairs found: ${es.similarPairs.length}`);
    console.log(`  Cannibalization groups: ${es.cannibalizationGroups.length}`);

    if (es.similarPairs.length > 0) {
      console.log(`\n  Top Similar Pairs:`);
      console.log(`    ${'URL 1'.padEnd(40)} ${'URL 2'.padEnd(40)} ${'Similarity'.padStart(10)}`);
      console.log(`    ${'─'.repeat(40)} ${'─'.repeat(40)} ${'─'.repeat(10)}`);
      for (const pair of es.similarPairs.slice(0, 15)) {
        const u1 = pair.url1.length > 38 ? pair.url1.substring(0, 38) + '..' : pair.url1;
        const u2 = pair.url2.length > 38 ? pair.url2.substring(0, 38) + '..' : pair.url2;
        console.log(`    ${u1.padEnd(40)} ${u2.padEnd(40)} ${(pair.similarity * 100).toFixed(1).padStart(9)}%`);
      }
    }

    if (es.cannibalizationGroups.length > 0) {
      console.log(`\n  Cannibalization Groups (pages competing for same topic):`);
      for (let i = 0; i < Math.min(es.cannibalizationGroups.length, 10); i++) {
        const group = es.cannibalizationGroups[i];
        console.log(`    Group ${i + 1} (${group.urls.length} pages, avg similarity: ${(group.similarity * 100).toFixed(1)}%):`);
        for (const url of group.urls.slice(0, 5)) {
          console.log(`      - ${url}`);
        }
        if (group.urls.length > 5) {
          console.log(`      ... and ${group.urls.length - 5} more`);
        }
      }
    }
  }

  // ── Link Intelligence ──────────────────────────────────────────
  if (stats.linkIntelligenceStats) {
    const li = stats.linkIntelligenceStats;
    console.log(`\n${line}`);
    console.log('  Internal Link Intelligence');
    console.log(line);

    // Click depth
    console.log(`\n  Click Depth from Homepage:`);
    console.log(`    Average: ${li.avgClickDepth}`);
    console.log(`    Maximum: ${li.maxClickDepth}`);

    const depths = Object.entries(li.clickDepthDistribution)
      .sort(([a], [b]) => Number(a) - Number(b));
    if (depths.length > 0) {
      console.log(`\n    Distribution:`);
      for (const [depth, count] of depths) {
        const bar = '#'.repeat(Math.min(count, 50));
        const label = Number(depth) > 3 ? `    Depth ${depth}: ${count} pages  !!` : `    Depth ${depth}: ${count} pages`;
        console.log(`    ${label}  ${bar}`);
      }
    }

    if (li.unreachablePagesCount > 0) {
      console.log(`\n    Unreachable from homepage: ${li.unreachablePagesCount}`);
      for (const url of li.unreachablePages.slice(0, 5)) {
        console.log(`      ${url}`);
      }
      if (li.unreachablePagesCount > 5) {
        console.log(`      ... and ${li.unreachablePagesCount - 5} more`);
      }
    }

    // Near-orphans
    if (li.nearOrphansCount > 0) {
      console.log(`\n  Near-Orphan Pages: ${li.nearOrphansCount}`);
      console.log(`    (Pages with few inlinks, all from deep pages)`);
      for (const no of li.nearOrphans.slice(0, 10)) {
        const depthLabel = no.worstSourceDepth !== null ? `source depth ${no.worstSourceDepth}` : 'unreachable source';
        console.log(`    In-Degree ${no.inDegree}, ${depthLabel} -- ${no.url}`);
      }
      if (li.nearOrphansCount > 10) {
        console.log(`    ... and ${li.nearOrphansCount - 10} more`);
      }
    }

    // Link dilution
    if (li.dilutionWarningsCount > 0) {
      console.log(`\n  Link Equity Dilution Warnings: ${li.dilutionWarningsCount}`);
      for (const dw of li.dilutionWarnings.slice(0, 10)) {
        const severity = dw.warning === 'excessive' ? '[EXCESSIVE]' : '[HIGH]';
        console.log(`    ${severity} ${dw.outDegree} outgoing links -- ${dw.url}`);
      }
      if (li.dilutionWarningsCount > 10) {
        console.log(`    ... and ${li.dilutionWarningsCount - 10} more`);
      }
    }

    // Link position distribution
    const posEntries = Object.entries(li.linkPositionDistribution)
      .filter(([, count]) => count > 0)
      .sort(([, a], [, b]) => b - a);
    if (posEntries.length > 0) {
      const totalLinks = posEntries.reduce((s, [, c]) => s + c, 0);
      console.log(`\n  Link Position Distribution (${totalLinks.toLocaleString()} internal links):`);
      for (const [pos, count] of posEntries) {
        const pct = totalLinks > 0 ? ((count / totalLinks) * 100).toFixed(1) : '0';
        console.log(`    ${pos.charAt(0).toUpperCase() + pos.slice(1)}: ${count.toLocaleString()} (${pct}%)`);
      }
    }

    // Pages with no content links
    if (li.pagesWithNoContentLinksCount > 0) {
      console.log(`\n  Pages with 0 content-area inlinks: ${li.pagesWithNoContentLinksCount}`);
      for (const url of li.pagesWithNoContentLinks.slice(0, 5)) {
        console.log(`    ${url}`);
      }
      if (li.pagesWithNoContentLinksCount > 5) {
        console.log(`    ... and ${li.pagesWithNoContentLinksCount - 5} more`);
      }
    }

    // HITS Hub/Authority Scores
    if (li.topAuthorities.length > 0) {
      console.log(`\n  HITS Analysis:`);
      console.log(`    Top Authorities (pillar content):`);
      for (const entry of li.topAuthorities) {
        console.log(`      ${entry.score.toFixed(2).padStart(5)} -- ${entry.url}`);
      }
    }
    if (li.topHubs.length > 0) {
      console.log(`\n    Top Hubs (navigation/resource pages):`);
      for (const entry of li.topHubs) {
        console.log(`      ${entry.score.toFixed(2).padStart(5)} -- ${entry.url}`);
      }
    }

    // Internal Linking Suggestions
    if (li.linkSuggestionsCount > 0) {
      console.log(`\n  Internal Linking Suggestions: ${li.linkSuggestionsCount}`);
      for (const [i, s] of li.linkSuggestions.slice(0, 10).entries()) {
        console.log(`\n    ${i + 1}. Score: ${s.score}  ${s.sourceUrl}`);
        console.log(`       -> ${s.targetUrl}`);
        console.log(`       ${s.reason}`);
      }
      if (li.linkSuggestionsCount > 10) {
        console.log(`\n    ... and ${li.linkSuggestionsCount - 10} more (see *-link-suggestions.csv)`);
      }
    }

    // Betweenness Centrality (Bridge Pages)
    if (li.centralitySkipped) {
      console.log(`\n  Centrality: ${li.centralitySkipReason}`);
    } else {
      if (li.topBridges.length > 0) {
        console.log(`\n  Bridge Pages (Betweenness Centrality):`);
        for (const entry of li.topBridges) {
          const marker = entry.score > 8.0 ? ' *** SINGLE POINT OF FAILURE ***' : '';
          console.log(`      ${entry.score.toFixed(2).padStart(5)} -- ${entry.url}${marker}`);
        }
      }
      if (li.singlePointOfFailure.length > 0) {
        console.log(`\n  WARNING: ${li.singlePointOfFailure.length} single point(s) of failure detected`);
        console.log(`    These pages are critical connectors. If removed, they would disconnect parts of the site.`);
      }

      if (li.mostConnected.length > 0) {
        console.log(`\n  Most Connected Pages (Closeness Centrality):`);
        for (const entry of li.mostConnected) {
          console.log(`      ${entry.score.toFixed(2).padStart(5)} -- ${entry.url}`);
        }
      }
      if (li.mostIsolated.length > 0) {
        console.log(`\n  Most Isolated Pages (Closeness Centrality):`);
        for (const entry of li.mostIsolated) {
          console.log(`      ${entry.score.toFixed(2).padStart(5)} -- ${entry.url}`);
        }
      }
    }

    // Semantic Link Distance
    if (li.semanticLinkAnalysis) {
      const sla = li.semanticLinkAnalysis;
      console.log(`\n  Semantic Link Analysis:`);
      console.log(`    Total internal links analyzed: ${sla.totalLinks}`);
      console.log(`    Average semantic relevance:    ${(sla.avgSemSimilarity * 100).toFixed(1)}%`);
      console.log(`    Weak links (<15% similarity):  ${sla.weakLinksCount}`);
      console.log(`    Strong links (>60% similarity): ${sla.strongLinksCount}`);

      if (sla.weakLinks.length > 0) {
        console.log(`\n    Weakest Links (topically unrelated):`);
        for (const link of sla.weakLinks.slice(0, 5)) {
          console.log(`      ${(link.similarity * 100).toFixed(1)}% -- ${link.source} -> ${link.target}`);
        }
        if (sla.weakLinksCount > 5) {
          console.log(`      ... and ${sla.weakLinksCount - 5} more`);
        }
      }
    }
  }

  // ── URL Structure Analytics ──────────────────────────────────
  if (stats.urlStructureStats) {
    const us = stats.urlStructureStats;
    console.log(`\n${line}`);
    console.log('  URL Structure Analytics');
    console.log(line);

    console.log(`\n  Total URLs analyzed: ${us.totalUrls}`);
    console.log(`  Avg path depth:     ${us.avgPathDepth}`);
    console.log(`  Max path depth:     ${us.maxPathDepth}`);
    console.log(`  URLs with params:   ${us.urlsWithParams} (${((us.urlsWithParams / us.totalUrls) * 100).toFixed(1)}%)`);
    console.log(`  Trailing slash:     ${us.urlsWithTrailingSlash} (${((us.urlsWithTrailingSlash / us.totalUrls) * 100).toFixed(1)}%)`);

    // Depth distribution
    console.log('\n  Depth Distribution:');
    const sortedDepths = Object.entries(us.depthDistribution).sort(([a], [b]) => Number(a) - Number(b));
    for (const [depth, count] of sortedDepths) {
      const pct = ((count / us.totalUrls) * 100).toFixed(1);
      console.log(`    depth ${depth}: ${count} (${pct}%)`);
    }

    // Top directories
    if (us.topDirectories.length > 0) {
      console.log('\n  Top Directories:');
      for (const { directory, count } of us.topDirectories.slice(0, 10)) {
        const pct = ((count / us.totalUrls) * 100).toFixed(1);
        console.log(`    ${directory}: ${count} (${pct}%)`);
      }
    }

    // Top query parameters
    if (us.topParameters.length > 0) {
      console.log('\n  Top Query Parameters:');
      for (const { parameter, count } of us.topParameters.slice(0, 10)) {
        console.log(`    ${parameter}: ${count} URLs`);
      }
    }

    // File extensions
    if (us.extensionDistribution.length > 1 || (us.extensionDistribution.length === 1 && us.extensionDistribution[0].extension !== '(none)')) {
      console.log('\n  File Extensions:');
      for (const { extension, count } of us.extensionDistribution) {
        const pct = ((count / us.totalUrls) * 100).toFixed(1);
        console.log(`    ${extension}: ${count} (${pct}%)`);
      }
    }
  }

  // ── CrUX (Chrome User Experience Report) ──────────────────────────
  if (stats.cruxStats) {
    const crux = stats.cruxStats;
    console.log(`\n${line}`);
    console.log('  Chrome User Experience Report (CrUX)');
    console.log(line);

    console.log(`\n  Overview (form factor: ${crux.formFactor}):`);
    console.log(`    Pages with CrUX data: ${crux.pagesWithData}/${stats.totalPages}`);

    if (crux.avgLcpMs !== null) {
      const lcpAssess = getCruxAssessment('lcp', crux.avgLcpMs);
      console.log(`\n  Largest Contentful Paint (LCP):`);
      console.log(`    Average p75: ${(crux.avgLcpMs / 1000).toFixed(2)}s [${lcpAssess}]`);
      console.log(`    Good (<=2.5s): ${crux.goodLcp}  Poor (>4s): ${crux.poorLcp}`);
      if (crux.worstLcp.length > 0) {
        console.log(`    Worst pages:`);
        for (const entry of crux.worstLcp.slice(0, 5)) {
          console.log(`      ${(entry.lcpMs / 1000).toFixed(2)}s — ${entry.url}`);
        }
      }
    }

    if (crux.avgInpMs !== null) {
      const inpAssess = getCruxAssessment('inp', crux.avgInpMs);
      console.log(`\n  Interaction to Next Paint (INP):`);
      console.log(`    Average p75: ${crux.avgInpMs}ms [${inpAssess}]`);
      console.log(`    Good (<=200ms): ${crux.goodInp}  Poor (>500ms): ${crux.poorInp}`);
      if (crux.worstInp.length > 0) {
        console.log(`    Worst pages:`);
        for (const entry of crux.worstInp.slice(0, 5)) {
          console.log(`      ${entry.inpMs}ms — ${entry.url}`);
        }
      }
    }

    if (crux.avgCls !== null) {
      const clsAssess = getCruxAssessment('cls', crux.avgCls);
      console.log(`\n  Cumulative Layout Shift (CLS):`);
      console.log(`    Average p75: ${crux.avgCls.toFixed(3)} [${clsAssess}]`);
      console.log(`    Good (<=0.1): ${crux.goodCls}  Poor (>0.25): ${crux.poorCls}`);
      if (crux.worstCls.length > 0) {
        console.log(`    Worst pages:`);
        for (const entry of crux.worstCls.slice(0, 5)) {
          console.log(`      ${entry.cls.toFixed(3)} — ${entry.url}`);
        }
      }
    }

    if (crux.avgTtfbMs !== null) {
      console.log(`\n  Time to First Byte (TTFB): avg p75 ${crux.avgTtfbMs}ms`);
    }
    if (crux.avgFcpMs !== null) {
      console.log(`  First Contentful Paint (FCP): avg p75 ${(crux.avgFcpMs / 1000).toFixed(2)}s`);
    }
  }

  // ── Template Type Distribution ──────────────────────────────
  if (stats.templateTypeDistribution) {
    const ttd = stats.templateTypeDistribution;
    const types = Object.entries(ttd).sort(([, a], [, b]) => b - a);
    if (types.length > 0) {
      console.log(`\n${line}`);
      console.log('  Page Template Types');
      console.log(line);
      for (const [type, count] of types) {
        const pct = ((count / stats.totalPages) * 100).toFixed(1);
        const bar = '\u2588'.repeat(Math.round(count / stats.totalPages * 30));
        console.log(`    ${type.padEnd(12)} ${String(count).padStart(5)} (${pct.padStart(5)}%) ${bar}`);
      }
    }
  }

  console.log(`\n${line}\n`);
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / Math.pow(1024, i);
  return `${value.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}
