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
import { formatDateYmd } from './utils.js';

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
  const endDate = new Date();
  endDate.setDate(endDate.getDate() - 1); // GA4 data has ~1 day lag
  const startDate = new Date(endDate);
  startDate.setDate(startDate.getDate() - days);

  const startStr = formatDateYmd(startDate);
  const endStr = formatDateYmd(endDate);

  // Build path-to-URL mapping for matching
  const pathToUrl = buildPathMap(urls);

  // Fetch data with pagination
  const results = new Map<string, Ga4Data>();
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

        // Match pagePath to full URLs
        const matchedUrls = pathToUrl.get(normalizePath(pagePath));
        if (matchedUrls) {
          for (const url of matchedUrls) {
            results.set(url, data);
          }
        }
      }

      hasMore = rows.length === pageSize;
      offset += pageSize;
      retryCount = 0; // Reset on success
    } catch (error: unknown) {
      const err = error as { code?: number; message?: string; details?: string };
      const code = err?.code;

      if (code === 7 || code === 8) {
        // PERMISSION_DENIED or RESOURCE_EXHAUSTED
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

  return results;
}

/**
 * Build a map from normalized page paths to full URLs.
 * Multiple URLs can map to the same path (e.g., with/without trailing slash).
 */
function buildPathMap(urls: string[]): Map<string, string[]> {
  const pathMap = new Map<string, string[]>();

  for (const url of urls) {
    try {
      const parsed = new URL(url);
      const path = normalizePath(parsed.pathname);
      const existing = pathMap.get(path) || [];
      existing.push(url);
      pathMap.set(path, existing);
    } catch {
      // Skip invalid URLs
    }
  }

  return pathMap;
}

/**
 * Normalize a URL path for matching:
 * - Decode percent-encoding
 * - Remove trailing slash (except root "/")
 * - Lowercase
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
