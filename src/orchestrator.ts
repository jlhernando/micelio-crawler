import pLimit from 'p-limit';
import * as cheerio from 'cheerio';
import { ProxyAgent, type Dispatcher } from 'undici';
import type { CrawlConfig, PageData, ExternalLinkResult, ResourceEntry, PageWeightData, SitemapExtensionCounts, SitemapEntry } from './types.js';
import { CrawlQueue } from './queue.js';
import { RobotsChecker } from './robots.js';
import { fetchPage, checkExternalUrl, headResourceSize, type FetchOptions } from './fetcher.js';
import { formatError } from './utils.js';
import { extractPageData, extractResourceRefs, extractBodyText, type ExtractionOptions } from './extractor.js';
import { ResultWriter } from './writer.js';
import { closeBrowser, setBrowserProxy } from './browser.js';
import { loadSnippets, runSnippets, type LoadedSnippet } from './snippets.js';
import { fetchPageSpeed } from './pagespeed.js';
import { analyzeWithAi, resolveAiKey, type AiConfig } from './ai-analysis.js';
import { assignSegments, type Segment } from './segmentation.js';
import { analyzeUrlStructure } from './url-structure.js';
import { compareRender } from './render-compare.js';
import { CrawlStore } from './db-store.js';
import { NgramAnalyzer } from './ngrams.js';
import { computeEmbeddings, type EmbeddingStats } from './embeddings.js';

export interface CrawlProgress {
  completed: number;
  failed: number;
  pending: number;
  total: number;
  lastPage?: {
    url: string;
    statusCode: number;
    responseTimeMs: number;
    error?: string;
  };
}

export interface CrawlResult {
  pages: PageData[];
  externalLinkResults: ExternalLinkResult[];
  sitemapErrors: string[];
  sitemapValidationWarnings: string[];
  sitemapEntries: SitemapEntry[];
  totalSitemapUrls: number;
  sitemapExtensionCounts: SitemapExtensionCounts;
  ngramStats: import('./ngrams.js').NgramStats | null;
  embeddingStats: EmbeddingStats | null;
}

export async function crawl(
  config: CrawlConfig,
  onProgress?: (progress: CrawlProgress) => void,
): Promise<CrawlResult> {
  const isListMode = config.mode === 'list';
  const isSitemapMode = config.mode === 'sitemap';
  const isSpiderMode = config.mode === 'spider';
  const seedUrl = isListMode
    ? (config.urls[0] ? new URL(config.urls[0]) : new URL('https://localhost'))
    : new URL(config.seedUrl);
  let seedDomain = seedUrl.hostname;

  // Initialize robots.txt checker (skip for list and sitemap modes)
  const robots = new RobotsChecker();
  let effectiveDelay = config.delayMs;
  const MAX_ADAPTIVE_DELAY = 30_000; // Cap at 30s
  let consecutiveSuccesses = 0;

  if (isSpiderMode) {
    process.stderr.write(`Checking robots.txt for ${seedDomain}...\n`);
    await robots.init(config.seedUrl, config.userAgent);

    // 1.6: Respect Crawl-Delay unless user explicitly set --delay or disabled robots.txt
    const crawlDelay = config.respectRobots ? robots.getCrawlDelay(config.userAgent) : null;
    if (crawlDelay !== null && !config.delayExplicit) {
      const crawlDelayMs = crawlDelay * 1000;
      if (crawlDelayMs > effectiveDelay) {
        effectiveDelay = crawlDelayMs;
        process.stderr.write(`  Crawl-Delay: ${crawlDelay}s (from robots.txt)\n`);
      }
    }
  }

  // Set baseDelay AFTER robots.txt adjustment so adaptive recovery never drops below it
  const baseDelay = effectiveDelay;

  // Sitemap mode: parse sitemaps to discover URLs
  let sitemapDiscoveredUrls: string[] = [];
  if (isSitemapMode) {
    process.stderr.write(`Parsing ${config.sitemapUrls.length} sitemap(s)...\n`);
    robots.setSitemapUrls(config.sitemapUrls);
    const entries = await robots.getSitemapEntries(config.userAgent);
    sitemapDiscoveredUrls = entries.map((e) => e.url);
    process.stderr.write(`  Found ${sitemapDiscoveredUrls.length} URLs in sitemap(s)\n`);

    // Report any parse errors
    const sitemapErrors = robots.getSitemapErrors();
    for (const err of sitemapErrors) {
      process.stderr.write(`  Warning: ${err}\n`);
    }

    if (sitemapDiscoveredUrls.length === 0) {
      process.stderr.write(`Error: No URLs found in the provided sitemap(s)\n`);
    }
  }

  // Phase 12.1: Database storage mode
  const useDb = !!config.dbPath;
  let store: CrawlStore | null = null;

  if (useDb) {
    store = new CrawlStore(config.dbPath);
    if (config.resume) {
      const meta = store.getCrawlMeta();
      const existingPages = store.getPageCount();
      if (meta && existingPages > 0) {
        process.stderr.write(`Resuming crawl: ${existingPages} pages already in database, ${store.pendingCount} URLs pending\n`);
        // MINOR-3 fix: Warn if crawl was already completed
        const prevStatus = store.getCrawlStatus();
        if (prevStatus === 'complete') {
          process.stderr.write(`  Note: Previous crawl was already complete. Re-running may produce duplicates.\n`);
        }
      } else {
        process.stderr.write(`No previous crawl found in database, starting fresh\n`);
      }
    }
    store.saveCrawlMeta(config, Date.now());
    process.stderr.write(`Database storage: ${config.dbPath}\n`);
  }

  // Initialize queue
  const maxDepth = isSpiderMode ? config.maxDepth : 0;
  const maxPages = isListMode
    ? config.urls.length
    : isSitemapMode
      ? Math.min(config.maxPages, sitemapDiscoveredUrls.length || config.maxPages)
      : config.maxPages;
  const queueSeedUrl = isListMode
    ? (config.urls[0] || 'https://localhost')
    : isSitemapMode
      ? (sitemapDiscoveredUrls[0] || config.seedUrl)
      : config.seedUrl;

  const queue = new CrawlQueue(queueSeedUrl, maxDepth, maxPages, {
    includePatterns: config.includePatterns,
    excludePatterns: config.excludePatterns,
    enforceInternal: isSpiderMode,
    allowedDomains: config.allowedDomains,
  });

  // When resuming with DB, re-populate queue visited set from DB
  // and let CrawlStore's pending URLs drive the crawl
  const isResuming = useDb && config.resume && store!.getPageCount() > 0;

  if (isResuming) {
    // BUG-2 fix: Pre-populate queue visited set from DB so it can filter new links correctly
    const visitedUrls = store!.getVisitedUrls();
    for (const url of visitedUrls) {
      queue.markVisited(url);
    }
    process.stderr.write(`  Loaded ${visitedUrls.length} visited URLs into queue filter\n`);
  } else {
    if (isListMode) {
      for (const url of config.urls) {
        // BUG-1 fix: Only enqueue to store if queue accepted the URL (applies filters)
        if (queue.enqueue(url, 0)) store?.enqueue(url, 0);
      }
      process.stderr.write(`List mode: ${config.urls.length} URLs to crawl\n`);
    } else if (isSitemapMode) {
      for (const url of sitemapDiscoveredUrls) {
        if (queue.enqueue(url, 0)) store?.enqueue(url, 0);
      }
      process.stderr.write(`Sitemap mode: ${queue.totalSeen} URLs queued for crawling\n`);
    } else {
      if (queue.enqueue(config.seedUrl, 0)) store?.enqueue(config.seedUrl, 0);

      // Seed from sitemap
      const sitemapUrls = await robots.getSitemapUrls(config.userAgent);
      if (sitemapUrls.length > 0) {
        process.stderr.write(`Found ${sitemapUrls.length} URLs in sitemap(s)\n`);
        for (const url of sitemapUrls) {
          if (queue.enqueue(url, 1)) store?.enqueue(url, 1);
        }
      }
    }
  }

  // Initialize writer
  const writer = new ResultWriter(config.outputPath, config.outputFormat);

  const limit = pLimit(config.concurrency);
  const results: PageData[] = [];
  const stats = { completed: 0, failed: 0 };
  // When resuming, start stats from existing DB page count
  if (isResuming) {
    stats.completed = store!.getPageCount();
  }
  const allTasks: Promise<void>[] = [];

  // Track external links for optional checking
  const externalLinkMap = new Map<string, string[]>();

  // Phase 6: Track transfer sizes and resource refs for page weight
  const transferSizeMap = new Map<string, number>();
  const resourceRefsMap = new Map<string, ResourceEntry[]>();

  // Phase 12.3: Create shared ProxyAgent if proxy is configured
  let proxyDispatcher: Dispatcher | undefined;
  if (config.proxy) {
    proxyDispatcher = new ProxyAgent(config.proxy);
    setBrowserProxy(config.proxy);
    // Redact credentials before logging
    try {
      const proxyUrl = new URL(config.proxy);
      if (proxyUrl.password) proxyUrl.password = '***';
      process.stderr.write(`Proxy: ${proxyUrl.toString()}\n`);
    } catch {
      process.stderr.write(`Proxy: ${config.proxy}\n`);
    }
  }

  const fetchOpts: FetchOptions = {
    userAgent: config.userAgent,
    jsRendering: config.jsRendering,
    customHeaders: config.customHeaders,
    cookies: config.cookies,
    dispatcher: proxyDispatcher,
  };

  // Phase 3: Extraction options
  const extractionOpts: ExtractionOptions = {
    customExtractions: config.customExtractions,
    customSearches: config.customSearches,
    linkIntelligence: config.linkIntelligence,
    seedUrl: config.seedUrl,
  };

  // Phase 3: Load JS snippets
  let snippets: LoadedSnippet[] = [];
  if (config.snippetPaths.length > 0) {
    if (!config.jsRendering) {
      process.stderr.write(`Warning: --snippet requires --js flag. Enabling JS rendering.\n`);
      fetchOpts.jsRendering = true;
    }
    snippets = await loadSnippets(config.snippetPaths);
    process.stderr.write(`Loaded ${snippets.length} snippet(s)\n`);
  }

  if (config.customExtractions.length > 0) {
    process.stderr.write(`Custom extractions: ${config.customExtractions.map((r) => r.name).join(', ')}\n`);
  }
  if (config.customSearches.length > 0) {
    process.stderr.write(`Custom searches: ${config.customSearches.map((r) => r.name).join(', ')}\n`);
  }

  // Phase 4: PSI setup
  const usePsi = config.psi;
  if (usePsi) {
    process.stderr.write(`PageSpeed Insights: enabled${config.psiKey ? ' (with API key)' : ' (no key — rate limited)'}\n`);
  }

  // Phase 4: AI setup
  // E2 fix: Warn when only one of --ai-prompt/--ai-provider is set
  if (config.aiPrompt && !config.aiProvider) {
    process.stderr.write(`Warning: --ai-prompt requires --ai-provider. AI analysis disabled.\n`);
  } else if (!config.aiPrompt && config.aiProvider) {
    process.stderr.write(`Warning: --ai-provider requires --ai-prompt. AI analysis disabled.\n`);
  }
  let aiConfig: AiConfig | null = null;
  if (config.aiPrompt && config.aiProvider) {
    const aiKey = resolveAiKey(config.aiProvider, config.aiKey);
    aiConfig = {
      provider: config.aiProvider,
      model: config.aiModel,
      apiKey: aiKey,
      prompt: config.aiPrompt,
    };
    process.stderr.write(`AI Analysis: ${config.aiProvider} (${aiConfig.model || 'default model'})\n`);
  }

  // Phase 12: Parse segment rules
  const segments: Segment[] = config.segmentRules.map(r => ({
    name: r.name,
    pattern: new RegExp(r.pattern),
  }));
  if (segments.length > 0) {
    process.stderr.write(`Segments: ${segments.map(s => s.name).join(', ')}\n`);
  }

  // Phase 13: N-Grams analyzer (with multilingual stopword support)
  const ngramAnalyzer = config.ngrams ? new NgramAnalyzer(config.language || undefined) : null;
  let ngramLanguageDetected = !!config.language;
  if (ngramAnalyzer) {
    const langLabel = config.language ? ` (language: ${config.language})` : ' (auto-detect language)';
    process.stderr.write(`N-Grams: enabled (unigrams, bigrams, trigrams)${langLabel}\n`);
  }

  // Phase 13.2: Body text map for embeddings (collected during crawl)
  const bodyTextMap = config.embeddings ? new Map<string, string>() : null;

  if (config.allowedDomains.length > 0) {
    process.stderr.write(`Allowed domains: ${config.allowedDomains.join(', ')}\n`);
  }

  const modeLabel = isListMode ? 'list' : isSitemapMode ? `sitemap (${sitemapDiscoveredUrls.length} URLs)` : config.seedUrl;
  process.stderr.write(`Starting crawl of ${modeLabel}\n`);
  // Calculate estimated crawl rate: concurrency * (1000 / delay) pages per minute, capped by concurrency
  const effectiveRate = effectiveDelay > 0
    ? Math.min(config.concurrency, config.concurrency * (1000 / effectiveDelay))
    : config.concurrency;
  const pagesPerMin = Math.round(effectiveRate * 60);
  process.stderr.write(`  Workers:    ${config.concurrency} concurrent\n`);
  process.stderr.write(`  Crawl rate: ~${pagesPerMin} pages/min (${effectiveDelay}ms delay)\n`);
  process.stderr.write(`  Depth:      ${maxDepth}\n`);
  process.stderr.write(`  Page limit: ${maxPages}\n\n`);

  const startTime = Date.now();

  // Helper: get next URL to crawl (from DB when resuming, otherwise from in-memory queue)
  function getNextEntry() {
    if (isResuming && store) {
      return store.dequeue();
    }
    return queue.dequeue();
  }

  function hasMoreWork(): boolean {
    if (isResuming && store) {
      return store.pendingCount > 0;
    }
    return queue.size > 0;
  }

  function getTotalSeen(): number {
    if (store) {
      return store.totalSeen;
    }
    return queue.totalSeen;
  }

  // Conditional termination tracking
  let crawlAborted = false;
  const crawlStartMs = Date.now();

  function shouldAbort(): boolean {
    if (crawlAborted) return true;
    if (config.maxErrors > 0 && stats.failed >= config.maxErrors) {
      if (!crawlAborted) {
        crawlAborted = true;
        process.stderr.write(`\n  Crawl stopped: reached ${stats.failed} errors (--max-errors ${config.maxErrors})\n`);
      }
      return true;
    }
    if (config.timeoutSeconds > 0 && (Date.now() - crawlStartMs) / 1000 >= config.timeoutSeconds) {
      if (!crawlAborted) {
        crawlAborted = true;
        process.stderr.write(`\n  Crawl stopped: timeout after ${config.timeoutSeconds}s (--timeout)\n`);
      }
      return true;
    }
    return false;
  }

  // Process queue
  while ((hasMoreWork() || limit.activeCount > 0) && !shouldAbort()) {
    while (hasMoreWork() && limit.activeCount + limit.pendingCount < config.concurrency * 2 && !shouldAbort()) {
      const entry = getNextEntry();
      if (!entry) break;

      // Check robots.txt (only in spider mode, when respectRobots is enabled)
      if (isSpiderMode && config.respectRobots && !robots.isAllowed(entry.url, config.userAgent)) {
        process.stderr.write(`  [blocked] ${entry.url} (robots.txt)\n`);
        if (config.showBlockedInternal) {
          // Track blocked URL as a result with robotsBlocked flag
          const blockedPage: PageData = {
            url: entry.url,
            finalUrl: entry.url,
            statusCode: 0,
            redirectChain: [],
            responseTimeMs: 0,
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
            depth: entry.depth,
            crawledAt: new Date().toISOString(),
            error: 'Blocked by robots.txt',
            hreflang: [],
            structuredData: [],
            openGraph: {},
            twitterCard: {},
            wordCount: 0,
            contentHash: '',
            anchors: [],
            security: { isHttps: entry.url.startsWith('https'), hasMixedContent: false, mixedContentUrls: [], hasHsts: false, hasXFrameOptions: false, hasCsp: false },
            customExtractions: {},
            customSearches: {},
            snippetResults: {},
            pagespeed: null,
            aiAnalysis: null,
            sitemapData: null,
            pageWeight: null,
            indexability: { indexable: false, reason: 'Blocked by robots.txt' },
            readability: null,
            urlIssues: [],
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
            robotsBlocked: true,
            templateType: 'other',
            inlinks: 0,
            pageRank: 0,
          };
          writer.write(blockedPage);
          results.push(blockedPage);
          if (store) {
            try { store.insertPage(blockedPage); } catch (e) { process.stderr.write(`  [warn] Failed to store blocked page ${blockedPage.url}: ${e}\n`); }
          }
        }
        continue;
      }

      const task = limit(async () => {
        try {
          if (effectiveDelay > 0) {
            await new Promise((r) => setTimeout(r, effectiveDelay));
          }

          const fetchResult = await fetchPage(entry.url, fetchOpts);
          let pageData = extractPageData(fetchResult, entry.depth, seedDomain, extractionOpts);

          // Detect cross-domain redirect on seed URL (e.g., example.com -> www.example.com)
          // If the first page redirects to a different hostname, update seedDomain so
          // discovered links are correctly classified as internal.
          if (isSpiderMode && entry.depth === 0 && stats.completed === 0 && stats.failed === 0) {
            try {
              const finalHostname = new URL(fetchResult.finalUrl).hostname;
              if (finalHostname !== seedDomain) {
                process.stderr.write(`\n  Redirect detected: ${seedDomain} → ${finalHostname}. Updating seed domain.\n`);
                seedDomain = finalHostname;
                queue.updateSeedDomain(finalHostname);
                // Re-extract with the corrected seedDomain so links are classified correctly
                pageData = extractPageData(fetchResult, entry.depth, seedDomain, extractionOpts);
              }
            } catch {
              // ignore URL parse errors
            }
          }

          // Phase 12: Assign segments
          if (segments.length > 0) {
            pageData.segments = assignSegments(pageData.url, segments);
          }

          // URL Structure Analytics
          pageData.urlStructure = analyzeUrlStructure(pageData.finalUrl);

          // Phase 12: Render comparison (when JS rendering produced different HTML)
          if (fetchResult.rawHtml && fetchResult.html !== fetchResult.rawHtml) {
            pageData.renderDiffs = compareRender(fetchResult.rawHtml, fetchResult.html);
          }

          // Shared cheerio parse for n-grams, page-weight, and embeddings (avoid redundant parsing)
          const needCheerio = (ngramAnalyzer || config.pageWeight || bodyTextMap) && !pageData.error && pageData.statusCode === 200 && fetchResult.html;
          const $shared = needCheerio ? cheerio.load(fetchResult.html) : null;

          // Phase 13: Feed body text into n-gram analyzer and/or store for embeddings
          if ($shared && (ngramAnalyzer || bodyTextMap)) {
            // Auto-detect language from first page's <html lang> attribute
            if (ngramAnalyzer && !ngramLanguageDetected) {
              const htmlLang = $shared('html').attr('lang')?.trim().toLowerCase();
              if (htmlLang) {
                const langCode = htmlLang.split('-')[0]; // "en-US" -> "en"
                ngramAnalyzer.setLanguage(langCode);
                ngramLanguageDetected = true;
                process.stderr.write(`\n  N-Grams: detected language "${langCode}" from <html lang>\n`);
              }
            }
            const bodyText = extractBodyText($shared);
            if (ngramAnalyzer) ngramAnalyzer.addPage(bodyText);
            if (bodyTextMap && bodyText.length > 50) bodyTextMap.set(pageData.url, bodyText);
          }

          // Phase 6: Store transfer size and extract resource refs for page weight
          if (config.pageWeight && $shared) {
            transferSizeMap.set(pageData.url, fetchResult.transferSize);
            const refs = extractResourceRefs($shared, fetchResult.finalUrl);
            resourceRefsMap.set(pageData.url, refs);
          }

          // Phase 3: Run JS snippets if any
          if (snippets.length > 0 && !pageData.error) {
            try {
              pageData.snippetResults = await runSnippets(entry.url, snippets, config.userAgent);
            } catch (err) {
              pageData.snippetResults = { error: formatError(err) };
            }
          }

          // Phase 4: PageSpeed Insights (within concurrent crawl tasks)
          // B2 fix: fetchPageSpeed handles errors internally, no outer catch needed
          if (usePsi && !pageData.error && pageData.statusCode === 200) {
            pageData.pagespeed = await fetchPageSpeed(pageData.finalUrl, config.psiKey);
          }

          // Phase 4: AI Analysis
          if (aiConfig && !pageData.error && pageData.statusCode === 200) {
            try {
              const AI_HTML_SAMPLE_LIMIT = 20_000;
              const htmlSample = fetchResult.html.substring(0, AI_HTML_SAMPLE_LIMIT);
              const bodyText = htmlSample
                .replace(/<script[^>]*>[\s\S]*?<\/script>/gi, '')
                .replace(/<style[^>]*>[\s\S]*?<\/style>/gi, '')
                .replace(/<[^>]+>/g, ' ')
                .replace(/\s+/g, ' ')
                .trim();
              pageData.aiAnalysis = await analyzeWithAi(aiConfig, {
                url: pageData.finalUrl,
                title: pageData.title?.text || '',
                description: pageData.metaDescription?.text || '',
                h1: pageData.headings.h1.join(', '),
                bodyText,
              });
            } catch (err) {
              pageData.aiAnalysis = `Error: ${formatError(err)}`;
            }
          }

          // Enqueue discovered internal links (spider mode only)
          // BUG-1 fix: Only enqueue to store if queue accepted (respects maxPages, maxDepth, patterns)
          if (isSpiderMode) {
            for (const link of pageData.internalLinks) {
              const accepted = queue.enqueue(link, entry.depth + 1, entry.url);
              if (accepted) store?.enqueue(link, entry.depth + 1, entry.url);
            }
          }

          // Track external links for later checking
          if (config.checkExternal) {
            for (const extLink of pageData.externalLinks) {
              const existing = externalLinkMap.get(extLink) || [];
              existing.push(pageData.url);
              externalLinkMap.set(extLink, existing);
            }
          }

          writer.write(pageData);
          results.push(pageData);
          // Phase 12.1: Persist to DB immediately (auto-save)
          // ISSUE-2 fix: Graceful handling of DB write failures
          if (store) {
            try {
              store.insertPage(pageData);
            } catch (dbErr) {
              process.stderr.write(`\n  [db-warn] Failed to save to DB: ${formatError(dbErr)}\n`);
            }
          }

          // Adaptive throttling: back off on 429, recover on consecutive successes
          if (pageData.statusCode === 429) {
            const prevDelay = effectiveDelay;
            effectiveDelay = Math.min(effectiveDelay < 500 ? 2000 : effectiveDelay * 2, MAX_ADAPTIVE_DELAY);
            consecutiveSuccesses = 0;
            process.stderr.write(`\n  [429] Rate limited on ${entry.url.substring(0, 60)} — delay ${prevDelay}ms → ${effectiveDelay}ms\n`);
          } else if (!pageData.error) {
            consecutiveSuccesses++;
            // After 10 consecutive successes, gradually reduce delay toward base
            if (consecutiveSuccesses >= 10 && effectiveDelay > baseDelay) {
              const prevDelay = effectiveDelay;
              effectiveDelay = Math.max(baseDelay, Math.floor(effectiveDelay * 0.75));
              consecutiveSuccesses = 0;
              process.stderr.write(`\n  [throttle] 10 consecutive OK — delay ${prevDelay}ms → ${effectiveDelay}ms\n`);
            }
          }

          if (pageData.error) {
            stats.failed++;
          } else {
            stats.completed++;
          }

          const total = getTotalSeen();
          const elapsed = ((Date.now() - startTime) / 1000).toFixed(1);
          process.stderr.write(
            `\r  [${stats.completed + stats.failed}/${total}] ${elapsed}s - ${pageData.statusCode} ${entry.url.substring(0, 80)}`,
          );

          onProgress?.({
            completed: stats.completed,
            failed: stats.failed,
            pending: queue.size,
            total,
            lastPage: {
              url: pageData.url,
              statusCode: pageData.statusCode,
              responseTimeMs: pageData.responseTimeMs,
              error: pageData.error || undefined,
            },
          });
        } catch (err) {
          stats.failed++;
          process.stderr.write(`\n  [error] ${entry.url}: ${formatError(err)}\n`);
        }
      });

      allTasks.push(task);
    }

    if (limit.activeCount > 0 || limit.pendingCount > 0) {
      await new Promise((r) => setTimeout(r, 50));
    }
  }

  await Promise.all(allTasks);
  await writer.close();

  // Phase 6: Sitemap audit — annotate pages with sitemap presence
  if (isSpiderMode || isSitemapMode) {
    const sitemapEntryMap = robots.getSitemapEntryMap();
    if (sitemapEntryMap.size > 0) {
      process.stderr.write(`\nSitemap audit: cross-referencing ${sitemapEntryMap.size} sitemap URLs with ${results.length} crawled pages...\n`);
      for (const page of results) {
        const entry = sitemapEntryMap.get(page.url) || sitemapEntryMap.get(page.finalUrl);
        if (entry) {
          page.sitemapData = { inSitemap: true, sitemapLastmod: entry.lastmod };
        } else {
          page.sitemapData = { inSitemap: false };
        }
      }
    }
  }

  // Phase 6: Page weight — resolve resource sizes via HEAD requests
  if (config.pageWeight && resourceRefsMap.size > 0) {
    process.stderr.write(`\nPage weight analysis...\n`);
    const resourceSizeCache = new Map<string, number | null>();

    // Collect unique resource URLs for HEAD requests
    const uniqueResourceUrls = new Set<string>();
    for (const resources of resourceRefsMap.values()) {
      for (const r of resources) {
        uniqueResourceUrls.add(r.url);
      }
    }

    if (uniqueResourceUrls.size > 0) {
      process.stderr.write(`  Fetching sizes for ${uniqueResourceUrls.size} unique resources...\n`);
      const resLimit = pLimit(config.concurrency);
      const resTasks: Promise<void>[] = [];
      let resChecked = 0;

      for (const resUrl of uniqueResourceUrls) {
        const t = resLimit(async () => {
          const size = await headResourceSize(resUrl, config.userAgent);
          resourceSizeCache.set(resUrl, size);
          resChecked++;
          if (resChecked % 20 === 0) {
            process.stderr.write(`\r  Resources: ${resChecked}/${uniqueResourceUrls.size}`);
          }
        });
        resTasks.push(t);
      }
      await Promise.all(resTasks);
      process.stderr.write(`\r  Resources: ${resChecked}/${uniqueResourceUrls.size} done\n`);
    }

    // Build PageWeightData for each page
    for (const page of results) {
      const resources = resourceRefsMap.get(page.url);
      if (!resources) continue;

      const htmlBytes = transferSizeMap.get(page.url) || 0;

      // Apply cached sizes to resources
      for (const r of resources) {
        r.sizeBytes = resourceSizeCache.get(r.url) ?? null;
      }

      const byType: Record<string, { count: number; bytes: number }> = {};
      let totalBytes = htmlBytes;

      // Add HTML as a type
      byType['html'] = { count: 1, bytes: htmlBytes };

      for (const r of resources) {
        const size = r.sizeBytes ?? 0;
        totalBytes += size;
        if (!byType[r.type]) {
          byType[r.type] = { count: 0, bytes: 0 };
        }
        byType[r.type].count++;
        byType[r.type].bytes += size;
      }

      page.pageWeight = {
        totalBytes,
        htmlBytes,
        byType,
        resources,
      };
    }
  }

  // 1.5: Check external links
  const externalLinkResults: ExternalLinkResult[] = [];
  if (config.checkExternal && externalLinkMap.size > 0) {
    process.stderr.write(`\n\nChecking ${externalLinkMap.size} external links...\n`);
    const extLimit = pLimit(config.concurrency);
    const extTasks: Promise<void>[] = [];
    let extChecked = 0;

    for (const [extUrl, foundOn] of externalLinkMap) {
      const t = extLimit(async () => {
        const result = await checkExternalUrl(extUrl, config.userAgent);
        extChecked++;
        if (result.statusCode >= 400 || result.statusCode === 0) {
          externalLinkResults.push({
            url: extUrl,
            statusCode: result.statusCode,
            error: result.error,
            foundOn,
          });
        }
        if (extChecked % 10 === 0) {
          process.stderr.write(`\r  External: ${extChecked}/${externalLinkMap.size}`);
        }
      });
      extTasks.push(t);
    }
    await Promise.all(extTasks);
    process.stderr.write(`\r  External: ${extChecked}/${externalLinkMap.size} — ${externalLinkResults.length} broken\n`);
  }

  if (config.jsRendering || snippets.length > 0) {
    await closeBrowser();
  }

  // Close ProxyAgent to release connection pool
  if (proxyDispatcher && 'close' in proxyDispatcher) {
    await (proxyDispatcher as ProxyAgent).close();
  }

  // Memory optimization: Clear transient maps that are no longer needed
  externalLinkMap.clear();
  transferSizeMap.clear();
  resourceRefsMap.clear();

  // Phase 6: Re-write output file if post-crawl enrichment was applied
  const hasPostCrawlEnrichment = results.some((p) => p.sitemapData !== null || p.pageWeight !== null);
  if (hasPostCrawlEnrichment) {
    process.stderr.write(`Updating output with post-crawl data...\n`);
    const rewriter = new ResultWriter(config.outputPath, config.outputFormat);
    for (const page of results) {
      rewriter.write(page);
    }
    await rewriter.close();

    // Phase 12.1: Update enriched pages in DB
    if (store) {
      store.updatePages(results);
    }
  }

  // Phase 12.1: Load all pages and close DB (with try/finally for ISSUE-3)
  // Note: getAllPages() loads all pages into memory for report generation.
  // This is a known v1 limitation; for crawls with millions of pages,
  // future work should stream pages through the reporter or paginate from DB.
  let allPages: PageData[];
  if (store) {
    try {
      allPages = store.getAllPages();
      store.markCrawlComplete();
      const resumedCount = isResuming ? (allPages.length - results.length) : 0;
      // MINOR-4 fix: Separate previously crawled from newly crawled in output
      if (resumedCount > 0) {
        process.stderr.write(`Database: ${allPages.length} total pages (${resumedCount} from previous run + ${results.length} new)\n`);
      } else {
        process.stderr.write(`Database: ${allPages.length} total pages stored in ${config.dbPath}\n`);
      }
    } finally {
      store.close();
    }
  } else {
    allPages = results;
  }

  const duration = ((Date.now() - startTime) / 1000).toFixed(1);
  const memUsage = process.memoryUsage();
  const rssMB = (memUsage.rss / 1024 / 1024).toFixed(0);
  const heapMB = (memUsage.heapUsed / 1024 / 1024).toFixed(0);
  // MINOR-4 fix: Show only newly crawled count in completion message
  process.stderr.write(`\nCrawl complete: ${results.length} pages crawled in ${duration}s (${stats.failed} failed)\n`);
  process.stderr.write(`Output: ${config.outputPath}\n`);
  process.stderr.write(`Memory: ${rssMB} MB RSS, ${heapMB} MB heap\n\n`);

  // Phase 13: Finalize n-gram analysis
  const ngramStats = ngramAnalyzer ? ngramAnalyzer.getResults() : null;
  if (ngramAnalyzer) {
    ngramAnalyzer.clear();
  }

  // Phase 13.2: Compute semantic similarity via embeddings
  let embeddingStats: EmbeddingStats | null = null;
  if (bodyTextMap && config.embeddings && !(config.aiProvider === 'openai' || config.aiProvider === 'ollama')) {
    process.stderr.write('\n--embeddings requires --ai-provider openai or ollama\n');
  } else if (bodyTextMap && bodyTextMap.size >= 2 && config.aiProvider && (config.aiProvider === 'openai' || config.aiProvider === 'ollama')) {
    process.stderr.write('\nComputing semantic similarity embeddings...\n');
    const embeddingPages = Array.from(bodyTextMap.entries()).map(([url, bodyText]) => ({ url, bodyText }));
    // Free the body text map early
    bodyTextMap.clear();
    try {
      const key = config.aiKey || '';
      embeddingStats = await computeEmbeddings(
        embeddingPages,
        config.aiProvider as 'openai' | 'ollama',
        key,
        config.embeddingModel,
        config.similarityThreshold,
        (done, total) => {
          process.stderr.write(`  Embedding progress: ${done}/${total} pages\r`);
        },
      );
      process.stderr.write(`\nEmbeddings complete: ${embeddingStats.pagesEmbedded} pages, ${embeddingStats.similarPairs.length} similar pairs found\n`);
    } catch (err) {
      process.stderr.write(`\nEmbedding computation failed: ${formatError(err)}\n`);
    }
  } else if (bodyTextMap) {
    process.stderr.write('  Not enough pages with content for embedding analysis (need 2+)\n');
  }

  // Compute sitemap extension counts (deduplicate since map stores both raw + normalized URLs)
  const sitemapExtensionCounts: SitemapExtensionCounts = { news: 0, video: 0, image: 0 };
  const seen = new Set<SitemapEntry>();
  for (const entry of robots.getSitemapEntryMap().values()) {
    if (seen.has(entry)) continue;
    seen.add(entry);
    if (entry.news?.length) sitemapExtensionCounts.news += entry.news.length;
    if (entry.videos?.length) sitemapExtensionCounts.video += entry.videos.length;
    if (entry.images?.length) sitemapExtensionCounts.image += entry.images.length;
  }

  // Collect unique sitemap entries (deduplicate since map stores both raw + normalized)
  const uniqueSitemapEntries: SitemapEntry[] = [];
  const seenEntries = new Set<SitemapEntry>();
  for (const entry of robots.getSitemapEntryMap().values()) {
    if (seenEntries.has(entry)) continue;
    seenEntries.add(entry);
    uniqueSitemapEntries.push(entry);
  }

  return {
    pages: allPages,
    externalLinkResults,
    sitemapErrors: robots.getSitemapErrors(),
    sitemapValidationWarnings: robots.getSitemapValidationWarnings(),
    sitemapEntries: uniqueSitemapEntries,
    totalSitemapUrls: uniqueSitemapEntries.length,
    sitemapExtensionCounts,
    ngramStats,
    embeddingStats,
  };
}
