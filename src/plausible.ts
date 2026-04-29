/**
 * Plausible Analytics data fetcher.
 *
 * Uses the Stats API v2 (POST /api/v2/query) to fetch per-URL metrics:
 * visitors, visits, pageviews, bounce rate, visit duration, views per visit,
 * time on page, and scroll depth.
 *
 * Auth: Bearer token via API key.
 * Supports self-hosted instances via configurable host URL.
 *
 * Docs: https://plausible.io/docs/stats-api
 */

export interface PlausibleData {
  visitors: number;
  visits: number;
  pageviews: number;
  bounceRate: number;
  visitDuration: number;
  viewsPerVisit: number;
  timeOnPage: number | null;
  scrollDepth: number | null;
  conversions: number;
  conversionRate: number;
}

export interface PlausibleOptions {
  /** Site domain as registered in Plausible (e.g., "example.com") */
  siteId: string;
  /** Plausible API key (Bearer token) */
  apiKey: string;
  /** Number of days to look back (default: 30) */
  days: number;
  /** List of crawled URLs to match against */
  urls: string[];
  /** Plausible instance URL (default: "https://plausible.io") */
  host?: string;
}

interface PlausibleQueryResponse {
  results: Array<{
    dimensions: string[];
    metrics: number[];
  }>;
  meta?: {
    metric_warnings?: Record<string, { code: string; warning: string }>;
  };
  query?: Record<string, unknown>;
  error?: string;
}

const MAX_RETRIES = 3;
const CHUNK_SIZE = 100; // Max URL filter values per request

/**
 * Fetch Plausible analytics data for the given URLs.
 * Returns a Map keyed by URL with analytics metrics.
 *
 * Uses the event:page dimension to match against crawled URL paths.
 */
export async function fetchPlausibleData(options: PlausibleOptions): Promise<Map<string, PlausibleData>> {
  const { siteId, apiKey, days, urls, host = 'https://plausible.io' } = options;

  if (urls.length === 0) return new Map();

  if (!apiKey) {
    throw new Error(
      'Plausible API requires an API key (--plausible-api-key). ' +
      'Generate one at: Settings > API Keys in your Plausible dashboard.'
    );
  }

  if (!siteId) {
    throw new Error(
      'Plausible requires a site ID (--plausible-site-id). ' +
      'Use the domain registered in Plausible (e.g., example.com).'
    );
  }

  // Build two maps:
  // 1. originalPaths: original-case paths for the API filter (Plausible API is case-sensitive)
  // 2. pathToUrls: normalized paths for matching API results back to crawled URLs
  const pathToUrls = new Map<string, string[]>();
  const originalPaths: string[] = [];

  for (const url of urls) {
    try {
      const parsed = new URL(url);
      const rawPath = extractPath(parsed.pathname);
      const normPath = normalizePath(rawPath);

      // Collect original-case paths for API filter (deduplicated)
      if (!originalPaths.includes(rawPath)) {
        originalPaths.push(rawPath);
      }

      // Map normalized path → full URLs
      const existing = pathToUrls.get(normPath) || [];
      existing.push(url);
      pathToUrls.set(normPath, existing);
    } catch {
      // Skip invalid URLs
    }
  }

  const results = new Map<string, PlausibleData>();

  // Chunk paths to avoid oversized filter arrays
  for (let i = 0; i < originalPaths.length; i += CHUNK_SIZE) {
    const pathChunk = originalPaths.slice(i, i + CHUNK_SIZE);
    const chunkData = await fetchChunk(host, siteId, apiKey, days, pathChunk);

    for (const [normPath, data] of chunkData) {
      const matchedUrls = pathToUrls.get(normPath);
      if (matchedUrls) {
        for (const url of matchedUrls) {
          results.set(url, { ...data }); // Copy to avoid shared references
        }
      }
    }
  }

  return results;
}

async function fetchChunk(
  host: string,
  siteId: string,
  apiKey: string,
  days: number,
  paths: string[],
): Promise<Map<string, PlausibleData>> {
  const results = new Map<string, PlausibleData>();
  const endpoint = `${host.replace(/\/+$/, '')}/api/v2/query`;

  // Primary query: page-compatible traffic metrics
  // Note: views_per_visit is session-level and cannot be filtered by event:page
  const trafficBody = {
    site_id: siteId,
    date_range: `${days}d`,
    metrics: [
      'visitors', 'visits', 'pageviews', 'bounce_rate',
      'visit_duration', 'time_on_page', 'scroll_depth',
    ],
    dimensions: ['event:page'],
    filters: [['is', 'event:page', paths]],
  };

  const trafficData = await queryPlausible(endpoint, apiKey, trafficBody);

  if (trafficData?.results) {
    for (const row of trafficData.results) {
      const pagePath = row.dimensions[0];
      if (!pagePath) continue;

      const m = row.metrics;
      const pageviews = m[2] ?? 0;
      const visits = m[1] ?? 0;
      // Key by normalized path for consistent matching
      results.set(normalizePath(pagePath), {
        visitors: m[0] ?? 0,
        visits,
        pageviews,
        bounceRate: m[3] ?? 0,
        visitDuration: m[4] ?? 0,
        viewsPerVisit: visits > 0 ? Math.round((pageviews / visits) * 10) / 10 : 0,
        timeOnPage: m[5] ?? null,
        scrollDepth: m[6] ?? null,
        // Conversions require goals to be configured in Plausible and a goal filter in the query.
        // Without explicit goal configuration, these remain 0.
        conversions: 0,
        conversionRate: 0,
      });
    }
  }

  return results;
}

async function queryPlausible(
  endpoint: string,
  apiKey: string,
  body: Record<string, unknown>,
  retries = 0,
): Promise<PlausibleQueryResponse> {
  try {
    const response = await fetch(endpoint, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${apiKey}`,
      },
      body: JSON.stringify(body),
    });

    if (response.status === 401) {
      throw new Error(
        'Plausible API authentication failed. Check your API key (--plausible-api-key).'
      );
    }

    if (response.status === 404) {
      throw new Error(
        `Plausible site "${(body as { site_id?: string }).site_id}" not found. ` +
        'Check your site ID (--plausible-site-id) matches your Plausible domain.'
      );
    }

    if (response.status === 429) {
      if (retries < MAX_RETRIES) {
        const backoff = 5000 * Math.pow(2, retries);
        process.stderr.write(`Plausible rate limit hit, waiting ${backoff / 1000}s...\n`);
        await sleep(backoff);
        return queryPlausible(endpoint, apiKey, body, retries + 1);
      }
      throw new Error('Plausible API rate limit exceeded after ' + MAX_RETRIES + ' retries.');
    }

    if (!response.ok) {
      const text = await response.text();
      throw new Error(`Plausible API error (${response.status}): ${text.substring(0, 300)}`);
    }

    return (await response.json()) as PlausibleQueryResponse;
  } catch (err) {
    if ((err as Error).message.startsWith('Plausible')) throw err;

    if (retries < MAX_RETRIES) {
      const backoff = 3000 * Math.pow(2, retries);
      await sleep(backoff);
      return queryPlausible(endpoint, apiKey, body, retries + 1);
    }

    throw new Error(`Plausible API request failed: ${(err as Error).message}`);
  }
}

/**
 * Extract the path from a URL pathname:
 * - Decode percent-encoding
 * - Remove trailing slash (except root "/")
 * Preserves original casing for the API filter.
 */
function extractPath(pathname: string): string {
  let path = pathname;
  try {
    path = decodeURIComponent(path);
  } catch {
    // Keep as-is if decoding fails
  }
  if (path.length > 1 && path.endsWith('/')) {
    path = path.slice(0, -1);
  }
  return path;
}

/**
 * Normalize a URL path for local matching:
 * - Decode percent-encoding
 * - Remove trailing slash (except root "/")
 * - Lowercase (for case-insensitive local matching)
 */
function normalizePath(path: string): string {
  let normalized = path;
  try {
    normalized = decodeURIComponent(normalized);
  } catch {
    // Keep as-is if decoding fails
  }
  normalized = normalized.toLowerCase();
  if (normalized.length > 1 && normalized.endsWith('/')) {
    normalized = normalized.slice(0, -1);
  }
  return normalized;
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}
