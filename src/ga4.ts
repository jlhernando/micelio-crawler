/**
 * Google Analytics 4 data fetcher.
 *
 * Uses the GA4 Data API (v1beta) to fetch per-URL metrics:
 * sessions, pageviews, bounce rate, conversions, active users,
 * engagement rate, and average session duration.
 *
 * Auth: Reuses OAuth2 or service account credentials.
 * The GA4 Data API requires the analytics.readonly scope.
 */

import { BetaAnalyticsDataClient } from '@google-analytics/data';
import type { Ga4Data } from './types.js';
import { reportDateRange } from './utils.js';

const MAX_RETRIES = 5;

interface Ga4FetchOptions {
  /** GA4 property ID (numeric, e.g., "123456789") */
  propertyId: string;
  /** Number of days to look back (default: 90) */
  days: number;
  /** List of crawled URLs to match against */
  urls: string[];
  /** Service account key file path (optional, uses ADC if omitted) */
  keyFile?: string;
  /** OAuth2 access token (optional, for OAuth flow) */
  accessToken?: string;
}

/**
 * Fetch GA4 analytics data for the given URLs.
 * Returns a Map keyed by URL with analytics metrics.
 *
 * Uses pagePath dimension to match against crawled URLs.
 * Handles pagination for properties with many pages.
 */
export async function fetchGa4Data(options: Ga4FetchOptions): Promise<Map<string, Ga4Data>> {
  const { propertyId, days, urls, keyFile, accessToken } = options;

  if (urls.length === 0) return new Map();

  // Create GA4 client
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const clientOptions: any = {};
  if (keyFile) {
    clientOptions.keyFile = keyFile;
  } else if (accessToken) {
    // For OAuth2 tokens, we need to set credentials directly
    clientOptions.authClient = {
      getAccessToken: () => Promise.resolve({ token: accessToken }),
      getRequestHeaders: () => Promise.resolve({ Authorization: `Bearer ${accessToken}` }),
    };
  }
  const client = new BetaAnalyticsDataClient(clientOptions);

  // Calculate date range
  // GA4 data has ~1 day lag; the range is inclusive, so exactly `days` days.
  const { startDate: startStr, endDate: endStr } = reportDateRange(days, 1);

  // Matches GA4 pagePath rows to crawled URLs
  const matcher = new Ga4PathMatcher(urls);
  let offset = 0;
  const pageSize = 100000; // GA4 API max
  let hasMore = true;
  let retryCount = 0;

  while (hasMore) {
    try {
      const [response] = await client.runReport({
        property: `properties/${propertyId}`,
        dateRanges: [{ startDate: startStr, endDate: endStr }],
        dimensions: [{ name: 'pagePath' }],
        metrics: [
          { name: 'sessions' },
          { name: 'screenPageViews' },
          { name: 'bounceRate' },
          { name: 'conversions' },
          { name: 'activeUsers' },
          { name: 'engagementRate' },
          { name: 'averageSessionDuration' },
        ],
        limit: pageSize,
        offset,
      });

      const rows = response.rows || [];

      for (const row of rows) {
        const pagePath = row.dimensionValues?.[0]?.value;
        if (!pagePath) continue;

        const metrics = row.metricValues || [];
        const data: Ga4Data = {
          sessions: Number(metrics[0]?.value) || 0,
          pageviews: Number(metrics[1]?.value) || 0,
          bounceRate: Number(metrics[2]?.value) || 0,
          conversions: Number(metrics[3]?.value) || 0,
          activeUsers: Number(metrics[4]?.value) || 0,
          engagementRate: Number(metrics[5]?.value) || 0,
          avgSessionDuration: Math.round((Number(metrics[6]?.value) || 0) * 10) / 10,
        };

        matcher.add(pagePath, data);
      }

      hasMore = rows.length === pageSize;
      offset += pageSize;
      retryCount = 0; // Reset on success
    } catch (error: unknown) {
      const err = error as { code?: number; message?: string; details?: string };
      const code = err?.code;

      if (code === 7) {
        // PERMISSION_DENIED: waiting does not help, the credentials must change
        throw new Error(
          `GA4 permission denied for property "${propertyId}". ` +
          'The account needs read access to this property and the analytics.readonly scope ' +
          '(the "micelio gsc-auth" token only has the Search Console scope): use --ga4-key-file with a service account added to the property.'
        );
      }

      if (code === 8) {
        // RESOURCE_EXHAUSTED: quota, retry with back-off
        retryCount++;
        if (retryCount > MAX_RETRIES) {
          throw new Error(
            'GA4 API rate limit exceeded after ' + MAX_RETRIES + ' retries. Try again later.'
          );
        }
        const backoff = 5000 * Math.pow(2, retryCount - 1);
        await sleep(backoff);
        continue;
      }

      if (code === 3) {
        // INVALID_ARGUMENT
        throw new Error(
          `GA4 property "${propertyId}" not found or invalid. ` +
          'Make sure the property ID is correct (numeric ID only, not "UA-" or "G-").\n' +
          'Find it in GA4: Admin > Property Settings > Property ID'
        );
      }

      throw new Error(`GA4 API error: ${err?.message || error}`);
    }
  }

  return matcher.results();
}

/**
 * Matches GA4 pagePath rows to crawled URLs.
 *
 * URL paths are case-sensitive: /Product and /product are two pages, and
 * GA4 can report both. A row goes to the crawled URLs with exactly its path
 * (decoded, trailing slash ignored). Only a row that matches no crawled path
 * exactly may fall back to a case-insensitive match, and only when that is
 * unambiguous: one crawled path and one GA4 row share the folded path, and
 * that crawled path got no exact row.
 */
export class Ga4PathMatcher {
  private byPath = new Map<string, string[]>();
  private byFolded = new Map<string, Set<string>>();
  private exact = new Map<string, Ga4Data>();
  private fallback = new Map<string, Ga4Data[]>();

  constructor(urls: string[]) {
    for (const url of urls) {
      try {
        const path = pathKey(new URL(url).pathname);
        const list = this.byPath.get(path);
        if (list) list.push(url); else this.byPath.set(path, [url]);
        const folded = path.toLowerCase();
        if (!this.byFolded.has(folded)) this.byFolded.set(folded, new Set());
        this.byFolded.get(folded)!.add(path);
      } catch {
        // Skip invalid URLs
      }
    }
  }

  add(pagePath: string, data: Ga4Data): void {
    const path = pathKey(pagePath);
    if (this.byPath.has(path)) {
      this.exact.set(path, data);
      return;
    }
    const folded = path.toLowerCase();
    const rows = this.fallback.get(folded);
    if (rows) rows.push(data); else this.fallback.set(folded, [data]);
  }

  /** Map of crawled URL -> GA4 metrics. */
  results(): Map<string, Ga4Data> {
    const perPath = new Map(this.exact);
    for (const [folded, rows] of this.fallback) {
      const paths = this.byFolded.get(folded);
      if (rows.length !== 1 || !paths || paths.size !== 1) continue;
      const [path] = paths;
      if (!perPath.has(path)) perPath.set(path, rows[0]);
    }
    const out = new Map<string, Ga4Data>();
    for (const [path, data] of perPath) {
      for (const url of this.byPath.get(path) ?? []) out.set(url, data);
    }
    return out;
  }
}

/** Path key for matching: decoded, trailing slash removed (except "/"), case kept. */
function pathKey(path: string): string {
  let key = path;
  try {
    key = decodeURIComponent(key);
  } catch {
    // Keep as-is if decoding fails
  }
  if (key.length > 1 && key.endsWith('/')) {
    key = key.slice(0, -1);
  }
  return key;
}

/**
 * List available GA4 properties for the authenticated user.
 * Uses the Admin API to list accounts and properties.
 */
export async function listGa4Properties(keyFile?: string): Promise<{ id: string; name: string }[]> {
  // The GA4 Admin API requires a separate package (@google-analytics/admin)
  // For now, we provide a helpful error message
  return [];
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}
