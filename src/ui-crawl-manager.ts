import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import type { UiStore } from './ui-store.js';
import type { CrawlConfig, PageData, CrawlStats } from './types.js';
import type { CrawlProgress } from './orchestrator.js';
import { CrawlStore } from './db-store.js';

interface ActiveCrawl {
  id: string;
  config: Record<string, unknown>;
  startedAt: number;
  pageCount: number;
  errorCount: number;
  abortController: AbortController;
  paused: boolean;
}

export type ProgressCallback = (crawlId: string, data: {
  type: 'progress' | 'page' | 'error' | 'complete' | 'paused' | 'resumed' | 'cancelled';
  data: Record<string, unknown>;
}) => void;

/** Output file of a job, next to its crawl DB (fallback for jobs without one). */
export function outputPathFor(dbPath: string, ext: 'jsonl' | 'csv'): string {
  return dbPath ? dbPath.replace(/\.db$/, '') + '.' + ext : 'output.' + ext;
}

/** Saved report stats of a job, next to its crawl DB. */
export function statsPathFor(dbPath: string): string {
  return dbPath.replace(/\.db$/, '') + '.stats.json';
}

export class UiCrawlManager {
  private store: UiStore;
  private activeCrawl: ActiveCrawl | null = null;
  private onProgress: ProgressCallback | null = null;
  private results: Map<string, { pages: unknown[]; stats: unknown }> = new Map();

  constructor(store: UiStore) {
    this.store = store;
  }

  setProgressCallback(cb: ProgressCallback): void {
    this.onProgress = cb;
  }

  async startCrawl(crawlId: string, config: Record<string, unknown>): Promise<void> {
    if (this.activeCrawl) {
      throw new Error('A crawl is already running. Cancel it first.');
    }

    const abortController = new AbortController();
    const startedAt = Date.now();
    this.activeCrawl = {
      id: crawlId,
      config,
      startedAt,
      pageCount: 0,
      errorCount: 0,
      abortController,
      paused: false,
    };

    this.store.updateCrawlJob(crawlId, { status: 'running' });

    // Run the crawl in the background
    this.runCrawl(crawlId, config).catch(err => {
      console.error(`Crawl ${crawlId} failed:`, err.message);
      if (this.activeCrawl?.id === crawlId) this.activeCrawl = null;
      if (abortController.signal.aborted) return; // stays 'cancelled'
      this.store.updateCrawlJob(crawlId, {
        status: 'failed',
        completedAt: new Date().toISOString(),
        durationMs: Date.now() - startedAt,
      });
    });
  }

  private async runCrawl(crawlId: string, config: Record<string, unknown>): Promise<void> {
    const active = this.activeCrawl;
    try {
      // Dynamic import to avoid loading the full crawl engine on startup
      const { crawl } = await import('./orchestrator.js');
      const { generateReport } = await import('./reporter.js');

      // Build a CrawlConfig from the UI config. Pages go to the job's own
      // crawl DB and output file, so results survive a restart and two jobs
      // never write the same file.
      const dbPath = this.store.getCrawlJob(crawlId)?.dbPath || '';
      const crawlConfig = this.buildCrawlConfig(config, dbPath);

      // Run the crawl with progress reporting
      const crawlStart = Date.now();
      const progressHandler = (progress: CrawlProgress) => {
        if (!this.activeCrawl || this.activeCrawl.id !== crawlId) return;

        this.activeCrawl.pageCount = progress.completed + progress.failed;
        this.activeCrawl.errorCount = progress.failed;

        // Emit progress event
        this.onProgress?.(crawlId, {
          type: 'progress',
          data: {
            completed: progress.completed,
            failed: progress.failed,
            pending: progress.pending,
            total: progress.total,
            elapsedMs: Date.now() - crawlStart,
          },
        });

        // Emit per-page event
        if (progress.lastPage) {
          const eventType = progress.lastPage.error ? 'error' : 'page';
          this.onProgress?.(crawlId, {
            type: eventType,
            data: {
              url: progress.lastPage.url,
              statusCode: progress.lastPage.statusCode,
              responseTimeMs: progress.lastPage.responseTimeMs,
              error: progress.lastPage.error,
            },
          });
        }

        // Update store periodically (every 10 pages to avoid DB thrash)
        if ((progress.completed + progress.failed) % 10 === 0) {
          this.store.updateCrawlJob(crawlId, {
            pageCount: progress.completed + progress.failed,
            errorCount: progress.failed,
          });
        }
      };

      const { pages, externalLinkResults } = await crawl(crawlConfig, progressHandler, {
        signal: active?.abortController.signal,
        isPaused: () => active?.paused === true,
      });
      const durationMs = Date.now() - crawlStart;

      // Cancelled: cancelCrawl already stored the final status
      if (!this.activeCrawl || this.activeCrawl.id !== crawlId || active?.abortController.signal.aborted) return;

      // Generate report stats
      const stats = generateReport(pages, durationMs, externalLinkResults);

      // Post-crawl: Link Intelligence (mirrors CLI post-crawl in cli.ts)
      if (crawlConfig.linkIntelligence && pages.length > 0) {
        const { computeLinkIntelligence } = await import('./link-intelligence.js');
        const seedUrl = crawlConfig.mode === 'list'
          ? (crawlConfig.urls[0] || '')
          : crawlConfig.seedUrl;
        const liStats = computeLinkIntelligence(
          pages as PageData[],
          seedUrl,
          {
            pageRanks: (stats as CrawlStats).pageRankScores,
            maxSuggestions: crawlConfig.liMaxSuggestions,
            noCentrality: crawlConfig.liNoCentrality,
          },
        );
        (stats as CrawlStats).linkIntelligenceStats = liStats;
      }

      // Store results in memory; the stats also go next to the crawl DB so
      // getResults can rebuild them after a restart
      this.results.set(crawlId, { pages, stats });
      if (dbPath) {
        try {
          writeFileSync(statsPathFor(dbPath), JSON.stringify(stats), { mode: 0o600 });
        } catch (err) {
          console.error(`Crawl ${crawlId}: cannot save stats: ${(err as Error).message}`);
        }
      }

      // Update job status (pageCount = total pages, errorCount = final failed count)
      this.store.updateCrawlJob(crawlId, {
        status: 'completed',
        completedAt: new Date().toISOString(),
        pageCount: pages.length,
        errorCount: this.activeCrawl.errorCount,
        durationMs,
      });

      // Notify clients
      this.onProgress?.(crawlId, {
        type: 'complete',
        data: { crawlId, totalPages: pages.length, durationMs },
      });
    } finally {
      if (this.activeCrawl?.id === crawlId) {
        this.activeCrawl = null;
      }
    }
  }

  private buildCrawlConfig(config: Record<string, unknown>, dbPath: string): CrawlConfig {
    const num = (v: unknown, d: number): number => typeof v === 'number' ? v : d;
    const bool = (v: unknown, d: boolean): boolean => typeof v === 'boolean' ? v : d;
    const str = (v: unknown, d: string = ''): string => typeof v === 'string' ? v : d;

    return {
      seedUrl: str(config.seedUrl),
      urls: [],
      sitemapUrls: Array.isArray(config.sitemapUrls) ? config.sitemapUrls : [],
      mode: (config.mode as CrawlConfig['mode']) || 'spider',
      maxDepth: num(config.depth, 3),
      maxPages: num(config.limit, 500),
      concurrency: num(config.concurrency, 3),
      delayMs: num(config.delay, 0),
      delayExplicit: num(config.delay, 0) > 0,
      userAgent: str(config.userAgent, 'Micelio/1.0'),
      jsRendering: bool(config.jsRendering, false),
      outputPath: outputPathFor(dbPath, config.outputFormat === 'csv' ? 'csv' : 'jsonl'),
      outputFormat: (config.outputFormat as CrawlConfig['outputFormat']) || 'jsonl',
      customHeaders: (config.customHeaders as Record<string, string>) || {},
      cookies: str(config.cookies),
      includePatterns: (Array.isArray(config.includePatterns) ? config.includePatterns : []).map((p: string) => new RegExp(p)),
      excludePatterns: (Array.isArray(config.excludePatterns) ? config.excludePatterns : []).map((p: string) => new RegExp(p)),
      checkExternal: bool(config.checkExternal, false),
      customExtractions: (config.customExtractions as CrawlConfig['customExtractions']) || [],
      customSearches: (config.customSearches as CrawlConfig['customSearches']) || [],
      // Snippets are code: never loaded from a dashboard config (CLI --snippet only)
      snippetPaths: [],
      psi: bool(config.psi, false),
      psiKey: str(config.psiKey),
      aiPrompt: str(config.aiPrompt),
      aiProvider: (config.aiProvider as CrawlConfig['aiProvider']) || '',
      aiModel: str(config.aiModel),
      aiKey: str(config.aiKey),
      htmlReport: false,
      htmlOpen: false,
      pageWeight: bool(config.pageWeight, false),
      gsc: bool(config.gsc, false),
      gscProperty: str(config.gscProperty),
      gscDays: num(config.gscDays, 90),
      gscKeyFile: str(config.gscKeyFile),
      gscBqDataset: '',
      ga4: bool(config.ga4, false),
      ga4Property: str(config.ga4Property),
      ga4Days: num(config.ga4Days, 90),
      ga4KeyFile: str(config.ga4KeyFile),
      segmentRules: (config.segmentRules as CrawlConfig['segmentRules']) || [],
      dbPath,
      resume: false,
      proxy: str(config.proxy),
      ngrams: bool(config.ngrams, false),
      embeddings: bool(config.embeddings, false),
      embeddingModel: str(config.embeddingModel),
      similarityThreshold: num(config.similarityThreshold, 0.85),
      linkIntelligence: bool(config.linkIntelligence, false),
      liMaxSuggestions: num(config.liMaxSuggestions, 50),
      liNoCentrality: bool(config.liNoCentrality, false),
      sitemapOut: bool(config.sitemapOut, false),
      sitemapChangefreq: str(config.sitemapChangefreq),
      sitemapPriority: str(config.sitemapPriority),
      crux: bool(config.crux, false),
      cruxKey: str(config.cruxKey),
      cruxFormFactor: '' as CrawlConfig['cruxFormFactor'],
      plausible: bool(config.plausible, false),
      plausibleSiteId: str(config.plausibleSiteId),
      plausibleApiKey: str(config.plausibleApiKey),
      plausibleDays: num(config.plausibleDays, 30),
      plausibleHost: str(config.plausibleHost, 'https://plausible.io'),
      maxErrors: num(config.maxErrors, 0),
      timeoutSeconds: num(config.timeoutSeconds, 0),
      allowedDomains: Array.isArray(config.allowedDomains) ? config.allowedDomains : [],
      language: str(config.language, 'en'),
      respectRobots: bool(config.respectRobots, true),
      showBlockedInternal: bool(config.showBlockedInternal, false),
    };
  }

  /** Pause the active crawl. False when crawlId is not the active crawl. */
  pauseCrawl(crawlId: string): boolean {
    if (this.activeCrawl?.id !== crawlId || this.activeCrawl.abortController.signal.aborted) return false;
    this.activeCrawl.paused = true;
    this.store.updateCrawlJob(crawlId, { status: 'paused' });
    this.onProgress?.(crawlId, { type: 'paused', data: { crawlId } });
    return true;
  }

  /** Resume the active crawl. False when crawlId is not the active crawl. */
  resumeCrawl(crawlId: string): boolean {
    if (this.activeCrawl?.id !== crawlId || this.activeCrawl.abortController.signal.aborted) return false;
    this.activeCrawl.paused = false;
    this.store.updateCrawlJob(crawlId, { status: 'running' });
    this.onProgress?.(crawlId, { type: 'resumed', data: { crawlId } });
    return true;
  }

  /**
   * Cancel the active crawl: no new page starts, and the crawl stops once the
   * pages in flight finish. The crawl stays active until then, so a new one
   * cannot start alongside it. False when crawlId is not the active crawl.
   */
  cancelCrawl(crawlId: string): boolean {
    if (this.activeCrawl?.id !== crawlId) return false;
    if (this.activeCrawl.abortController.signal.aborted) return true;
    this.activeCrawl.abortController.abort();
    this.activeCrawl.paused = false;
    this.store.updateCrawlJob(crawlId, {
      status: 'cancelled',
      completedAt: new Date().toISOString(),
      durationMs: Date.now() - this.activeCrawl.startedAt,
    });
    this.onProgress?.(crawlId, { type: 'cancelled', data: { crawlId } });
    return true;
  }

  /**
   * Results of a crawl: from memory, or for a completed crawl from a previous
   * server run, from its crawl DB and saved stats.
   */
  getResults(crawlId: string): { pages: unknown[]; stats: unknown } | null {
    const cached = this.results.get(crawlId);
    if (cached) return cached;
    const job = this.store.getCrawlJob(crawlId);
    if (!job || job.status !== 'completed' || !job.dbPath || !existsSync(job.dbPath)) return null;
    const statsPath = statsPathFor(job.dbPath);
    if (!existsSync(statsPath)) return null;
    try {
      const stats = JSON.parse(readFileSync(statsPath, 'utf8'));
      const crawlStore = new CrawlStore(job.dbPath);
      let pages: PageData[];
      try {
        pages = crawlStore.getAllPages();
      } finally {
        crawlStore.close();
      }
      const loaded = { pages, stats };
      this.results.set(crawlId, loaded);
      return loaded;
    } catch (err) {
      console.error(`Crawl ${crawlId}: cannot load stored results: ${(err as Error).message}`);
      return null;
    }
  }

  getActiveCrawlId(): string | null {
    return this.activeCrawl?.id || null;
  }
}
