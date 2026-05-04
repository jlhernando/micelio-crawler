/** Fetch wrapper for REST API calls */

export interface UpdateStatus {
  current: string;
  latest?: string;
  updateAvailable: boolean;
  isDevBuild: boolean;
  downloadable: boolean;
  assetName?: string;
  releaseUrl?: string;
  releaseName?: string;
  publishedAt?: string;
  lastCheckedAt?: string;
  repo: string;
  platform: string;
  notes?: string;
}

export interface InstallResult {
  installed: boolean;
  restartRequired: boolean;
  status: UpdateStatus;
}

export interface ScheduleState {
  id: string;
  url: string;
  cron: string;
  description?: string;
  createdAt: string;
  lastRun?: string;
  lastStatus?: string;
  nextRun: string;
  outputDir: string;
  lastPages: number;
  lastDurationMs: number;
  totalRuns: number;
}

const BASE = '';

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const opts: RequestInit = {
    method,
    headers: { 'Content-Type': 'application/json' },
  };
  if (body) opts.body = JSON.stringify(body);

  const res = await fetch(`${BASE}${path}`, opts);
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    const msg = typeof err.error === 'string' ? err.error : err.error?.message;
    throw new Error(msg || `API error: ${res.status}`);
  }
  return res.json();
}

export const api = {
  // Crawl operations
  startCrawl: (config: Record<string, unknown>) =>
    request<{ id: string; status: string }>('POST', '/api/crawl/start', config),

  getCrawlStatus: (id: string) =>
    request<Record<string, unknown>>('GET', `/api/crawl/${id}/status`),

  pauseCrawl: (id: string) =>
    request<{ status: string }>('POST', `/api/crawl/${id}/pause`),

  resumeCrawl: (id: string) =>
    request<{ status: string }>('POST', `/api/crawl/${id}/resume`),

  stopCrawl: (id: string) =>
    request<{ status: string }>('POST', `/api/crawl/${id}/stop`),

  cancelCrawl: (id: string) =>
    request<{ status: string }>('POST', `/api/crawl/${id}/cancel`),

  restartCrawl: (id: string, config?: Record<string, unknown>) =>
    request<{ id: string; status: string }>('POST', `/api/crawl/${id}/restart`, config),

  getCrawlResults: (id: string) =>
    request<{ pages: unknown[]; stats: unknown }>('GET', `/api/crawl/${id}/results`),

  getCrawlPages: (id: string, params: {
    offset?: number; limit?: number; sort?: string; order?: string;
    search?: string; status?: string; indexable?: string;
    template?: string; classification?: string; issue?: string;
    robotsBlocked?: string;
  } = {}) => {
    const q = new URLSearchParams();
    if (params.offset !== undefined) q.set('offset', String(params.offset));
    if (params.limit !== undefined) q.set('limit', String(params.limit));
    if (params.sort) q.set('sort', params.sort);
    if (params.order) q.set('order', params.order);
    if (params.search) q.set('search', params.search);
    if (params.status && params.status !== 'all') q.set('status', params.status);
    if (params.indexable && params.indexable !== 'all') q.set('indexable', params.indexable);
    if (params.template && params.template !== 'all') q.set('template', params.template);
    if (params.classification && params.classification !== 'all') q.set('classification', params.classification);
    if (params.issue) q.set('issue', params.issue);
    if (params.robotsBlocked && params.robotsBlocked !== 'all') q.set('robotsBlocked', params.robotsBlocked);
    return request<{
      pages: Record<string, unknown>[]; total: number; totalAll: number;
      offset: number; limit: number; templateTypes: string[];
    }>('GET', `/api/crawl/${id}/pages?${q.toString()}`);
  },

  getPageInlinks: (id: string, url: string) =>
    request<{ url: string; sources: string[] }>(
      'GET', `/api/crawl/${id}/page-inlinks?url=${encodeURIComponent(url)}`
    ),

  getCrawlStats: (id: string) =>
    request<{ stats: Record<string, unknown>; pageCount: number; analysisPending?: boolean; pagesReady?: boolean; loadingProgress?: number }>('GET', `/api/crawl/${id}/stats`),

  getFilteredStats: (id: string, template: string) =>
    request<{ stats: Record<string, unknown>; pageCount: number }>('GET', `/api/crawl/${id}/stats/filtered?template=${encodeURIComponent(template)}`),

  getLoadingProgress: (id: string) =>
    request<{ pagesReady: boolean; loadingProgress: number; pageCount: number }>('GET', `/api/crawl/${id}/loading-progress`),

  getCrawlGraph: (id: string) =>
    request<{
      nodes: Array<{
        id: string;
        depth: number | null;
        authority: number;
        hub: number;
        centrality: number;
        closeness: number;
        inDegree: number;
        outDegree: number;
        title: string;
      }>;
      edges: Array<{ source: string; target: string }>;
      meta: {
        maxDepth: number;
        maxAuthority: number;
        maxHub: number;
        totalEdgesBeforeSampling: number;
        samplingApplied: boolean;
      };
    }>('GET', `/api/crawl/${id}/graph`),

  getCrawlSubgraph: (id: string, root?: string, depth: number = 1, max: number = 500) => {
    const params = new URLSearchParams();
    if (root) params.set('root', root);
    params.set('depth', String(depth));
    params.set('max', String(max));
    return request<{
      nodes: Array<{
        id: string; depth: number | null; authority: number; hub: number;
        centrality: number; closeness: number; inDegree: number; outDegree: number;
        title: string; pageRank: number; expandable: boolean; totalOutlinks: number;
      }>;
      edges: Array<{ source: string; target: string }>;
      meta: { rootURL: string; hops: number; totalNodesInGraph: number; nodesInView: number; pending?: boolean };
    }>('GET', `/api/crawl/${id}/graph/subgraph?${params.toString()}`);
  },

  searchGraphNodes: (id: string, query: string, limit: number = 20) =>
    request<{
      results: Array<{ id: string; title: string; depth: number; pageRank: number }>;
    }>('GET', `/api/crawl/${id}/graph/search?q=${encodeURIComponent(query)}&limit=${limit}`),

  getAnchorStats: (id: string) =>
    request<{
      anchors: Array<{ text: string; count: number; isInternal: boolean; isNonDescriptive: boolean }>;
      totalUnique: number;
    }>('GET', `/api/crawl/${id}/anchor-stats`),

  getDirectoryTree: (id: string) =>
    request<{
      tree: {
        name: string; path: string; pages: number; depth: number; isPage: boolean;
        statusCodes: Record<number, number>; avgResponseTime: number;
        children?: Array<unknown>;
      };
      totalPages: number;
    }>('GET', `/api/crawl/${id}/directory-tree`),

  deleteCrawl: (id: string) =>
    request<{ deleted: boolean }>('DELETE', `/api/crawl/${id}`),

  // History
  listCrawls: () =>
    request<{ crawls: Array<{ id: string; seedUrl: string; mode: string; status: string; startedAt: string; completedAt: string | null; pageCount: number; errorCount: number; durationMs: number | null; stats?: Record<string, unknown> }>; total: number }>('GET', '/api/crawls'),

  // Diff
  diffCrawls: (oldId: string, newId: string, fields?: string[]) =>
    request<unknown>('POST', '/api/crawl/diff', { oldId, newId, fields }),

  compareCrawls: (oldId: string, newId: string) =>
    request<{ comparison: Record<string, unknown> }>('POST', '/api/crawl/diff', { oldId, newId, full: true }),

  // Presets
  listPresets: () =>
    request<unknown[]>('GET', '/api/presets'),

  savePreset: (name: string, config: Record<string, unknown>) =>
    request<unknown>('POST', '/api/presets', { name, config }),

  deletePreset: (id: string) =>
    request<{ deleted: boolean }>('DELETE', `/api/presets/${id}`),

  // Settings
  getSettings: () =>
    request<Record<string, unknown>>('GET', '/api/settings'),

  updateSettings: (settings: Record<string, unknown>) =>
    request<Record<string, unknown>>('PUT', '/api/settings', settings),

  // Schedules
  listSchedules: () =>
    request<ScheduleState[]>('GET', '/api/schedules'),

  createSchedule: (config: {
    url: string; cron: string; depth?: number; limit?: number;
    concurrency?: number; delay?: number; webhook?: string; outputDir?: string;
  }) =>
    request<ScheduleState>('POST', '/api/schedules', config),

  deleteSchedule: (id: string) =>
    request<{ deleted: boolean }>('DELETE', `/api/schedules/${id}`),

  // Log Analysis — single file (legacy)
  uploadLog: async (file: File, format?: string) => {
    return api.uploadLogs([file], format);
  },

  // Log Analysis — one or more files merged into a single unified analysis job.
  uploadLogs: async (files: File[], format?: string) => {
    const form = new FormData();
    for (const f of files) form.append('file', f);
    if (format) form.append('format', format);
    const res = await fetch(`${BASE}/api/logs/upload`, { method: 'POST', body: form });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: { message: res.statusText } }));
      throw new Error(err.error?.message || `Upload failed: ${res.status}`);
    }
    return res.json() as Promise<{ id: string; status: string; fileSize?: number; fileCount?: number }>;
  },

  listLogs: () =>
    request<{ jobs: Array<{ id: string; filename: string; format: string; status: string; createdAt: string; completedAt?: string; fileSize: number; totalLines: number; processedLines: number; durationMs?: number; errorMsg?: string }>; total: number }>('GET', '/api/logs'),

  getLogStatus: (id: string) =>
    request<Record<string, unknown>>('GET', `/api/logs/${id}/status`),

  getLogOverview: (id: string) =>
    request<Record<string, unknown>>('GET', `/api/logs/${id}/overview`),

  getLogBots: (id: string) =>
    request<Record<string, unknown>>('GET', `/api/logs/${id}/bots`),

  getLogUrls: (id: string) =>
    request<{ urls: Array<{ path: string; hits: number; botHits: number; humanHits: number; topBot: string; status: number }> }>('GET', `/api/logs/${id}/urls`),

  mergeLogWithCrawl: (logId: string, crawlId: string) =>
    request<Record<string, unknown>>('POST', `/api/logs/${logId}/merge`, { crawlId }),

  // Paginated/sorted/filtered fetch over the persisted merge cache.
  getLogMergedPages: (logId: string, params: {
    crawlId: string;
    page?: number;
    pageSize?: number;
    sort?: string;
    order?: 'asc' | 'desc';
    segment?: string;
    search?: string;       // URL substring
    reason?: string;       // reason substring
    topBot?: string;       // top bot substring
    logStatus?: string;    // "200", "404", "2xx", etc.
    crawlStatus?: string;  // same as logStatus
    indexable?: string;    // "yes" | "no" | ""
    coverage?: string;     // "crawl_only" | "logs_only" | "both" | ""
  }) => {
    const qs = new URLSearchParams();
    qs.set('crawlId', params.crawlId);
    if (params.page) qs.set('page', String(params.page));
    if (params.pageSize) qs.set('pageSize', String(params.pageSize));
    if (params.sort) qs.set('sort', params.sort);
    if (params.order) qs.set('order', params.order);
    if (params.segment) qs.set('segment', params.segment);
    if (params.search) qs.set('search', params.search);
    if (params.reason) qs.set('reason', params.reason);
    if (params.topBot) qs.set('topBot', params.topBot);
    if (params.logStatus) qs.set('logStatus', params.logStatus);
    if (params.crawlStatus) qs.set('crawlStatus', params.crawlStatus);
    if (params.indexable) qs.set('indexable', params.indexable);
    if (params.coverage) qs.set('coverage', params.coverage);
    return request<{
      pages: Array<Record<string, unknown>>;
      total: number;
      page: number;
      pageSize: number;
    }>('GET', `/api/logs/${logId}/merged?${qs.toString()}`);
  },

  verifyLogBots: (id: string) =>
    request<{ verification: Record<string, { totalIps: number; verified: number; spoofed: number; unverified: number }>; spoofed: Array<{ ip: string; hostname?: string; verified: boolean; claimedBot: string; spoofDetected: boolean; method: string }> | null }>('POST', `/api/logs/${id}/verify`),

  deleteLog: (id: string) =>
    request<{ deleting: boolean }>('DELETE', `/api/logs/${id}`),

  // Auto-update
  getUpdateStatus: () =>
    request<UpdateStatus>('GET', '/api/update/status'),

  forceUpdateCheck: () =>
    request<UpdateStatus>('POST', '/api/update/check'),

  installUpdate: () =>
    request<InstallResult>('POST', '/api/update/install'),
};
