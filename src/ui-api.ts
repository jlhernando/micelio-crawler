import type { IncomingMessage, ServerResponse } from 'node:http';
import { gzipSync } from 'node:zlib';
import type { UiStore } from './ui-store.js';
import type { UiCrawlManager } from './ui-crawl-manager.js';
import { diffPages, DIFF_FIELDS } from './diff.js';
import type { PageData } from './types.js';
import { format as csvFormat } from 'fast-csv';
import { flattenPageForCsv } from './writer.js';
import { parseRequestUrl } from './ui-auth.js';
import { assertPublicUrl, isTrustedProxy, trustedProxy } from './net-guard.js';
import { formatError } from './utils.js';

// Allowlist of valid settings keys with expected types
const VALID_SETTINGS: Record<string, 'string' | 'number' | 'boolean'> = {
  defaultDepth: 'number', defaultLimit: 'number', defaultConcurrency: 'number',
  defaultDelay: 'number', defaultUserAgent: 'string', psiKey: 'string',
  aiProvider: 'string', aiModel: 'string', aiKey: 'string',
  gscKeyFile: 'string', gscProperty: 'string', gscDays: 'number',
  ga4KeyFile: 'string', ga4Property: 'string', ga4Days: 'number',
  cruxKey: 'string', plausibleApiKey: 'string', plausibleSiteId: 'string',
  plausibleHost: 'string', plausibleDays: 'number', defaultEmbeddings: 'boolean',
  embeddingModel: 'string', similarityThreshold: 'number',
  defaultNgrams: 'boolean', defaultLinkIntelligence: 'boolean',
  liMaxSuggestions: 'number', liNoCentrality: 'boolean',
  defaultSitemapOut: 'boolean', defaultOutputFormat: 'string',
  defaultHtmlReport: 'boolean', defaultCheckExternal: 'boolean',
  defaultJsRendering: 'boolean',
  defaultRespectRobots: 'boolean',
  defaultShowBlockedInternal: 'boolean',
};

// Env var fallbacks for sensitive API keys
const ENV_FALLBACKS: Record<string, string> = {
  psiKey: 'MICELIO_PSI_KEY',
  cruxKey: 'MICELIO_CRUX_KEY',
  plausibleApiKey: 'MICELIO_PLAUSIBLE_API_KEY',
  plausibleSiteId: 'MICELIO_PLAUSIBLE_SITE_ID',
  plausibleHost: 'MICELIO_PLAUSIBLE_HOST',
  aiKey: 'MICELIO_AI_KEY',
};

// File path settings that need validation
const FILE_PATH_KEYS = new Set(['gscKeyFile', 'ga4KeyFile']);

// Settings and crawl-config keys that hold API keys. Masked in every response.
const SECRET_KEYS = ['psiKey', 'aiKey', 'cruxKey', 'plausibleApiKey'];

/** Placeholder sent instead of a secret (same as the Go dashboard). */
export const MASKED_SECRET = '••••••••';

/** Copy of settings with every set secret replaced by MASKED_SECRET. */
export function maskSecrets(settings: Record<string, unknown>): Record<string, unknown> {
  const result = { ...settings };
  for (const key of SECRET_KEYS) {
    if (typeof result[key] === 'string' && result[key] !== '') result[key] = MASKED_SECRET;
  }
  return result;
}

/**
 * Copy of a crawl/preset config safe to return from the API: API keys,
 * cookies, custom header values and proxy passwords are masked.
 */
export function redactConfig(config: Record<string, unknown>): Record<string, unknown> {
  const result = maskSecrets(config);
  if (typeof result.cookies === 'string' && result.cookies !== '') result.cookies = MASKED_SECRET;
  if (result.customHeaders && typeof result.customHeaders === 'object') {
    result.customHeaders = Object.fromEntries(
      Object.keys(result.customHeaders as Record<string, unknown>).map((k) => [k, MASKED_SECRET]),
    );
  }
  if (typeof result.proxy === 'string' && result.proxy) result.proxy = redactProxy(result.proxy);
  return result;
}

/** Proxy URL with its password masked. */
function redactProxy(proxy: string): string {
  try {
    const u = new URL(proxy);
    if (u.password) {
      u.password = MASKED_SECRET;
      return u.toString();
    }
  } catch { /* not a URL: leave as is */ }
  return proxy;
}

/**
 * Undo masking in a config sent back by the dashboard: masked API keys take
 * the saved setting (or env) value; masked cookies and headers are dropped.
 * A masked proxy password cannot be recovered and is an error.
 */
export function unmaskConfig(config: Record<string, unknown>, settings: Record<string, unknown>): Record<string, unknown> {
  const result = { ...config };
  for (const key of SECRET_KEYS) {
    if (result[key] === MASKED_SECRET) {
      if (typeof settings[key] === 'string' && settings[key]) result[key] = settings[key];
      else delete result[key];
    }
  }
  if (result.cookies === MASKED_SECRET) delete result.cookies;
  if (result.customHeaders && typeof result.customHeaders === 'object') {
    result.customHeaders = Object.fromEntries(
      Object.entries(result.customHeaders as Record<string, unknown>).filter(([, v]) => v !== MASKED_SECRET),
    );
  }
  if (typeof result.proxy === 'string' && result.proxy.includes(encodeURIComponent(MASKED_SECRET))) {
    // The masked form of the trusted proxy stands for the trusted proxy
    const trusted = trustedProxy();
    if (trusted && result.proxy === redactProxy(trusted)) result.proxy = trusted;
    else throw new BadRequestError('Proxy password is masked; enter it again');
  }
  return result;
}

export class BadRequestError extends Error {}

function redactJob<T extends { config: Record<string, unknown> }>(job: T): T {
  return { ...job, config: redactConfig(job.config) };
}

/**
 * Check a crawl config sent to the HTTP API. Snippet files are never loaded
 * from the API (they run as code); entry URLs must be public http(s) URLs.
 * A proxy is only accepted when it is the one the local user configured with
 * micelio ui --trusted-proxy (or MICELIO_TRUSTED_PROXY): a proxy resolves
 * and connects to targets itself, out of reach of the SSRF guard.
 */
export async function validateCrawlRequest(config: Record<string, unknown>): Promise<void> {
  if (Array.isArray(config.snippetPaths) && config.snippetPaths.length > 0) {
    throw new BadRequestError('snippetPaths is not accepted by the API; use the CLI --snippet flag');
  }
  if (config.proxy !== undefined && config.proxy !== null && config.proxy !== '') {
    if (typeof config.proxy !== 'string') throw new BadRequestError('proxy must be a string');
    if (!isTrustedProxy(config.proxy)) {
      throw new BadRequestError(
        'proxy is not accepted by the API unless it is the trusted proxy. ' +
        'Start the dashboard with --trusted-proxy <url> (or MICELIO_TRUSTED_PROXY) and send that URL, or leave proxy empty',
      );
    }
  }
  const entries: unknown[] = [];
  if (config.seedUrl) entries.push(config.seedUrl);
  if (Array.isArray(config.sitemapUrls)) entries.push(...config.sitemapUrls);
  if (Array.isArray(config.urls)) entries.push(...config.urls);
  for (const u of entries) {
    if (typeof u !== 'string') throw new BadRequestError('URLs must be strings');
    try {
      await assertPublicUrl(u);
    } catch (err) {
      throw new BadRequestError(`${u}: ${formatError(err)}`);
    }
  }
}

export function validateSettings(obj: Record<string, unknown>): { valid: Record<string, unknown>; rejected: string[] } {
  const valid: Record<string, unknown> = {};
  const rejected: string[] = [];
  for (const [key, value] of Object.entries(obj)) {
    const expected = VALID_SETTINGS[key];
    if (!expected) { rejected.push(key); continue; }
    if (typeof value !== expected) { rejected.push(key); continue; }
    // A masked secret sent back from GET /api/settings keeps the saved value
    if (SECRET_KEYS.includes(key) && value === MASKED_SECRET) continue;
    // Validate file paths: must be absolute and not contain traversal
    if (FILE_PATH_KEYS.has(key) && typeof value === 'string' && value !== '') {
      const normalized = value.replace(/\\/g, '/');
      if (!normalized.startsWith('/') || normalized.includes('..')) {
        rejected.push(key);
        continue;
      }
    }
    valid[key] = value;
  }
  return { valid, rejected };
}

export function applyEnvFallbacks(settings: Record<string, unknown>): Record<string, unknown> {
  const result = { ...settings };
  for (const [key, envVar] of Object.entries(ENV_FALLBACKS)) {
    if (!result[key] && process.env[envVar]) {
      result[key] = process.env[envVar];
    }
  }
  return result;
}

export function createApiHandler(store: UiStore, crawlManager: UiCrawlManager) {
  return async (req: IncomingMessage, res: ServerResponse): Promise<boolean> => {
    const url = parseRequestUrl(req);
    if (!url) {
      res.writeHead(400, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: { code: 'BAD_REQUEST', message: 'Malformed request URL' } }));
      return true;
    }
    const path = url.pathname;
    const method = req.method || 'GET';

    if (!path.startsWith('/api/')) return false;

    res.setHeader('Content-Type', 'application/json');

    try {
      // POST /api/crawl/start
      if (method === 'POST' && path === '/api/crawl/start') {
        const body = await readBody(req);
        const raw = JSON.parse(body);
        if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
          throw new BadRequestError('Crawl config must be a JSON object');
        }
        const config = unmaskConfig(raw as Record<string, unknown>, applyEnvFallbacks(store.getSettings()));
        await validateCrawlRequest(config);
        // Refuse before creating a job, so a second start leaves no pending job
        if (crawlManager.getActiveCrawlId()) return crawlAlreadyRunning(res);
        // The stored job keeps no credentials; the crawl gets the real config
        const job = store.createCrawlJob(redactConfig(config));
        try {
          // Awaited: a rejection (two starts racing) is handled here instead
          // of becoming an unhandled rejection that ends the process.
          await crawlManager.startCrawl(job.id, config);
        } catch (err) {
          store.deleteCrawlJob(job.id);
          if (crawlManager.getActiveCrawlId()) return crawlAlreadyRunning(res);
          throw err;
        }
        json(res, 200, { id: job.id, status: 'running' });
        return true;
      }

      // GET /api/crawl/:id/status
      const statusMatch = path.match(/^\/api\/crawl\/([^/]+)\/status$/);
      if (method === 'GET' && statusMatch) {
        const job = store.getCrawlJob(statusMatch[1]);
        if (!job) return notFound(res, 'Crawl not found');
        json(res, 200, redactJob(job));
        return true;
      }

      // POST /api/crawl/:id/pause
      const pauseMatch = path.match(/^\/api\/crawl\/([^/]+)\/pause$/);
      if (method === 'POST' && pauseMatch) {
        if (!store.getCrawlJob(pauseMatch[1])) return notFound(res, 'Crawl not found');
        if (!crawlManager.pauseCrawl(pauseMatch[1])) return notRunning(res);
        json(res, 200, { status: 'paused' });
        return true;
      }

      // POST /api/crawl/:id/resume
      const resumeMatch = path.match(/^\/api\/crawl\/([^/]+)\/resume$/);
      if (method === 'POST' && resumeMatch) {
        if (!store.getCrawlJob(resumeMatch[1])) return notFound(res, 'Crawl not found');
        if (!crawlManager.resumeCrawl(resumeMatch[1])) return notRunning(res);
        json(res, 200, { status: 'running' });
        return true;
      }

      // POST /api/crawl/:id/cancel
      const cancelMatch = path.match(/^\/api\/crawl\/([^/]+)\/cancel$/);
      if (method === 'POST' && cancelMatch) {
        if (!store.getCrawlJob(cancelMatch[1])) return notFound(res, 'Crawl not found');
        if (!crawlManager.cancelCrawl(cancelMatch[1])) return notRunning(res);
        json(res, 200, { status: 'cancelled' });
        return true;
      }

      // GET /api/crawl/:id/results
      const resultsMatch = path.match(/^\/api\/crawl\/([^/]+)\/results$/);
      if (method === 'GET' && resultsMatch) {
        const job = store.getCrawlJob(resultsMatch[1]);
        if (!job) return notFound(res, 'Crawl not found');
        const results = await crawlManager.getResults(job.id);
        json(res, 200, results || { pages: [], stats: null });
        return true;
      }

      // GET /api/crawl/:id/graph — lightweight graph-only payload
      const graphMatch = path.match(/^\/api\/crawl\/([^/]+)\/graph$/);
      if (method === 'GET' && graphMatch) {
        const job = store.getCrawlJob(graphMatch[1]);
        if (!job) return notFound(res, 'Crawl not found');
        const results = await crawlManager.getResults(job.id);
        if (!results) return notFound(res, 'Crawl results not found');
        const graphData = buildGraphResponse(results.pages as PageData[]);
        jsonCompressed(req, res, 200, graphData);
        return true;
      }

      // GET /api/crawl/:id/export/csv — download results as CSV
      const csvMatch = path.match(/^\/api\/crawl\/([^/]+)\/export\/csv$/);
      if (method === 'GET' && csvMatch) {
        const job = store.getCrawlJob(csvMatch[1]);
        if (!job) return notFound(res, 'Crawl not found');
        const results = await crawlManager.getResults(job.id);
        if (!results || !results.pages.length) return notFound(res, 'No results');
        const domain = (results.pages[0] as PageData).url.replace(/^https?:\/\//, '').split('/')[0];
        const filename = `micelio-${domain}-${new Date().toISOString().slice(0, 10)}.csv`;
        res.writeHead(200, {
          'Content-Type': 'text/csv; charset=utf-8',
          'Content-Disposition': `attachment; filename="${filename}"`,
        });
        const csvStream = csvFormat({ headers: true });
        csvStream.pipe(res);
        for (const page of results.pages as PageData[]) {
          csvStream.write(flattenPageForCsv(page));
        }
        csvStream.end();
        return true;
      }

      // DELETE /api/crawl/:id
      const deleteMatch = path.match(/^\/api\/crawl\/([^/]+)$/);
      if (method === 'DELETE' && deleteMatch) {
        const deleted = crawlManager.deleteCrawl(deleteMatch[1]);
        if (deleted === 'running') {
          json(res, 409, { error: { code: 'CRAWL_RUNNING', message: 'Cancel the crawl before deleting it' } });
          return true;
        }
        json(res, 200, { deleted });
        return true;
      }

      // GET /api/crawls
      if (method === 'GET' && path === '/api/crawls') {
        const crawls = store.listCrawlJobs().map(redactJob);
        json(res, 200, { crawls, total: crawls.length });
        return true;
      }

      // POST /api/crawl/diff
      if (method === 'POST' && path === '/api/crawl/diff') {
        const body = await readBody(req);
        const { oldId, newId, fields } = JSON.parse(body);
        if (!oldId || !newId) {
          json(res, 400, { error: { code: 'BAD_REQUEST', message: 'oldId and newId are required' } });
          return true;
        }
        if (fields !== undefined) {
          if (!Array.isArray(fields) || fields.some((f: string) => !(DIFF_FIELDS as readonly string[]).includes(f))) {
            json(res, 400, { error: { code: 'BAD_REQUEST', message: 'Invalid fields parameter' } });
            return true;
          }
        }
        const oldResults = await crawlManager.getResults(oldId);
        const newResults = await crawlManager.getResults(newId);
        if (!oldResults) {
          json(res, 404, { error: { code: 'NOT_FOUND', message: `Results not found for crawl ${oldId}` } });
          return true;
        }
        if (!newResults) {
          json(res, 404, { error: { code: 'NOT_FOUND', message: `Results not found for crawl ${newId}` } });
          return true;
        }
        const result = diffPages(
          oldResults.pages as PageData[],
          newResults.pages as PageData[],
          { fields },
        );
        json(res, 200, result);
        return true;
      }

      // GET /api/presets
      if (method === 'GET' && path === '/api/presets') {
        const presets = store.listPresets().map(redactJob);
        json(res, 200, presets);
        return true;
      }

      // POST /api/presets
      if (method === 'POST' && path === '/api/presets') {
        const body = await readBody(req);
        const { name, config } = JSON.parse(body);
        const preset = store.savePreset(name, unmaskConfig(config ?? {}, applyEnvFallbacks(store.getSettings())));
        json(res, 201, redactJob(preset));
        return true;
      }

      // DELETE /api/presets/:id
      const presetDeleteMatch = path.match(/^\/api\/presets\/([^/]+)$/);
      if (method === 'DELETE' && presetDeleteMatch) {
        const deleted = store.deletePreset(presetDeleteMatch[1]);
        json(res, 200, { deleted });
        return true;
      }

      // GET /api/settings
      if (method === 'GET' && path === '/api/settings') {
        const settings = maskSecrets(applyEnvFallbacks(store.getSettings()));
        json(res, 200, settings);
        return true;
      }

      // PUT /api/settings
      if (method === 'PUT' && path === '/api/settings') {
        const body = await readBody(req);
        const raw = JSON.parse(body);
        if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
          json(res, 400, { error: { code: 'BAD_REQUEST', message: 'Settings must be a JSON object' } });
          return true;
        }
        const { valid, rejected } = validateSettings(raw as Record<string, unknown>);
        store.updateSettings(valid);
        const result: Record<string, unknown> = maskSecrets(store.getSettings());
        if (rejected.length > 0) result._rejectedKeys = rejected;
        json(res, 200, result);
        return true;
      }

      // 404 for unmatched API routes
      json(res, 404, { error: { code: 'NOT_FOUND', message: `Unknown API route: ${method} ${path}` } });
      return true;
    } catch (err) {
      if (err instanceof SyntaxError) {
        json(res, 400, { error: { code: 'BAD_REQUEST', message: 'Invalid JSON in request body' } });
      } else if (err instanceof BadRequestError) {
        json(res, 400, { error: { code: 'BAD_REQUEST', message: err.message } });
      } else {
        const message = err instanceof Error ? err.message : 'Internal server error';
        json(res, 500, { error: { code: 'INTERNAL_ERROR', message } });
      }
      return true;
    }
  };
}

function crawlAlreadyRunning(res: ServerResponse): true {
  json(res, 409, { error: { code: 'CRAWL_RUNNING', message: 'A crawl is already running. Cancel it first.' } });
  return true;
}

function notRunning(res: ServerResponse): true {
  json(res, 409, { error: { code: 'NOT_RUNNING', message: 'Crawl is not running' } });
  return true;
}

function json(res: ServerResponse, status: number, data: unknown): void {
  res.writeHead(status);
  res.end(JSON.stringify(data));
}

function notFound(res: ServerResponse, message: string): boolean {
  json(res, 404, { error: { code: 'NOT_FOUND', message } });
  return true;
}

const MAX_BODY_SIZE = 1024 * 1024; // 1MB

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    req.on('data', (chunk: Buffer) => {
      size += chunk.length;
      if (size > MAX_BODY_SIZE) {
        req.destroy();
        reject(new Error('Request body too large (max 1MB)'));
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString()));
    req.on('error', reject);
  });
}

// ── Graph API helpers ───────────────────────────────────────

interface GraphNode {
  id: string;
  depth: number | null;
  authority: number;
  hub: number;
  centrality: number;
  closeness: number;
  inDegree: number;
  outDegree: number;
  pageRank: number;
  title: string;
}

interface GraphEdge {
  source: string;
  target: string;
}

interface GraphResponse {
  nodes: GraphNode[];
  edges: GraphEdge[];
  meta: {
    maxDepth: number;
    maxAuthority: number;
    maxHub: number;
    maxInDegree: number;
    totalEdgesBeforeSampling: number;
    samplingApplied: boolean;
  };
}

const EDGE_SAMPLING_THRESHOLD = 5000;
const MAX_EDGES_PER_NODE = 15;

function buildGraphResponse(pages: PageData[]): GraphResponse {
  const nodeMap = new Map<string, GraphNode>();
  const nodes: GraphNode[] = [];

  // Build nodes — extract only graph-relevant fields
  for (const p of pages) {
    const li = p.linkIntelligence;
    const node: GraphNode = {
      id: p.url,
      depth: li?.clickDepth ?? null,
      authority: li?.authorityScore ?? 0,
      hub: li?.hubScore ?? 0,
      centrality: li?.betweennessCentrality ?? 0,
      closeness: li?.closenessCentrality ?? 0,
      inDegree: li?.inDegree ?? 0,
      outDegree: li?.outDegree ?? 0,
      pageRank: p.pageRank ?? 0,
      title: p.title?.text ?? '',
    };
    nodeMap.set(p.url, node);
    nodes.push(node);
  }

  // Build edges with optional sampling for large crawls
  const shouldSample = pages.length > EDGE_SAMPLING_THRESHOLD;
  const edgeSet = new Set<string>();
  const edges: GraphEdge[] = [];
  let totalEdgesBeforeSampling = 0;

  for (const p of pages) {
    const internalLinks = (p.internalLinks || []).filter(url => nodeMap.has(url));
    totalEdgesBeforeSampling += internalLinks.length;

    const links = shouldSample ? internalLinks.slice(0, MAX_EDGES_PER_NODE) : internalLinks;
    for (const targetUrl of links) {
      if (targetUrl === p.url) continue; // skip self-links
      const key = `${p.url}\t${targetUrl}`;
      if (!edgeSet.has(key)) {
        edgeSet.add(key);
        edges.push({ source: p.url, target: targetUrl });
      }
    }
  }

  // Compute meta
  let maxDepth = 0;
  let maxAuthority = 0;
  let maxHub = 0;
  let maxInDegree = 0;
  for (const n of nodes) {
    if (n.depth !== null && n.depth > maxDepth) maxDepth = n.depth;
    if (n.authority > maxAuthority) maxAuthority = n.authority;
    if (n.hub > maxHub) maxHub = n.hub;
    if (n.inDegree > maxInDegree) maxInDegree = n.inDegree;
  }

  return {
    nodes,
    edges,
    meta: {
      maxDepth,
      maxAuthority,
      maxHub,
      maxInDegree,
      totalEdgesBeforeSampling,
      samplingApplied: shouldSample,
    },
  };
}

function jsonCompressed(req: IncomingMessage, res: ServerResponse, status: number, data: unknown): void {
  const body = JSON.stringify(data);
  const acceptEncoding = req.headers['accept-encoding'] || '';
  if (acceptEncoding.includes('gzip')) {
    const compressed = gzipSync(Buffer.from(body));
    res.writeHead(status, {
      'Content-Type': 'application/json',
      'Content-Encoding': 'gzip',
      'Content-Length': compressed.length,
    });
    res.end(compressed);
  } else {
    res.writeHead(status, { 'Content-Type': 'application/json' });
    res.end(body);
  }
}
