/**
 * Google Search Console data fetcher.
 *
 * Fetches URL-level search analytics (impressions, clicks, CTR, position)
 * and merges with crawl data.
 *
 * NOTE: The API is queried without URL filters, fetching all URL-level data
 * for the property. For very large properties (100K+ URLs), this may use
 * significant memory. This is by design to allow flexible URL normalization
 * and matching that GSC's server-side filters cannot provide.
 */

import { google } from 'googleapis';
import type { GscData } from './types.js';
import { formatDateYmd } from './utils.js';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AuthClient = any; // googleapis accepts OAuth2Client | GoogleAuth | JWT

interface GscFetchOptions {
  /** Authenticated OAuth2 client or GoogleAuth instance */
  auth: AuthClient;
  /** GSC property URL (e.g., 'https://example.com/' or 'sc-domain:example.com') */
  property: string;
  /** Number of days to look back (default: 90) */
  days: number;
  /** List of crawled URLs to fetch data for */
  urls: string[];
}

interface GscRawRow {
  keys?: string[];
  clicks?: number;
  impressions?: number;
  ctr?: number;
  position?: number;
}

const MAX_RETRIES = 5;

/**
 * Fetch search analytics from GSC for the given URLs.
 * Returns a Map keyed by URL with search performance data.
 *
 * The API is queried with dimension=page to get URL-level data.
 * We batch requests in chunks of 25,000 rows (API limit).
 */
export async function fetchGscData(options: GscFetchOptions): Promise<Map<string, GscData>> {
  const { auth, property, days, urls } = options;

  const searchconsole = google.searchconsole({ version: 'v1', auth });

  // Calculate date range
  const endDate = new Date();
  endDate.setDate(endDate.getDate() - 3); // GSC data has 3-day lag
  const startDate = new Date(endDate);
  startDate.setDate(startDate.getDate() - days);

  const startStr = formatDateYmd(startDate);
  const endStr = formatDateYmd(endDate);

  const results = new Map<string, GscData>();

  // Fetch in batches (API returns max 25,000 rows per request)
  let startRow = 0;
  const rowLimit = 25000;
  let hasMore = true;
  let retryCount = 0;

  while (hasMore) {
    try {
      const response = await searchconsole.searchanalytics.query({
        siteUrl: property,
        requestBody: {
          startDate: startStr,
          endDate: endStr,
          dimensions: ['page'],
          rowLimit,
          startRow,
        },
      });

      const rows = (response.data.rows || []) as GscRawRow[];

      for (const row of rows) {
        const url = row.keys?.[0];
        if (!url) continue;

        results.set(url, {
          impressions: row.impressions ?? 0,
          clicks: row.clicks ?? 0,
          ctr: row.ctr ?? 0,
          position: row.position ? Math.round(row.position * 10) / 10 : 0,
        });
      }

      hasMore = rows.length === rowLimit;
      startRow += rowLimit;
      retryCount = 0; // Reset on success
    } catch (error: unknown) {
      const err = error as { response?: { status?: number }; code?: number; message?: string };
      const status = err?.response?.status || err?.code;
      if (status === 403) {
        throw new Error(
          `GSC access denied for property "${property}". ` +
          'Make sure you have read access and the property URL is correct.\n' +
          'Run "micelio gsc-auth" to see your available properties.'
        );
      }
      if (status === 429) {
        retryCount++;
        if (retryCount > MAX_RETRIES) {
          throw new Error(
            'GSC API rate limit exceeded after ' + MAX_RETRIES + ' retries. ' +
            'Try again later or use --gsc-bq for BigQuery bulk export.'
          );
        }
        // Exponential backoff: 5s, 10s, 20s, 40s, 80s
        const backoff = 5000 * Math.pow(2, retryCount - 1);
        await sleep(backoff);
        continue;
      }
      throw new Error(`GSC API error: ${err?.message || error}`);
    }
  }

  return results;
}

/**
 * Merge GSC data into crawled page data by URL matching.
 * Handles common normalization issues:
 * - Trailing slashes
 * - Protocol variants (http vs https)
 * - www vs non-www
 */
export function mergeGscData(
  crawledUrls: string[],
  gscData: Map<string, GscData>,
): Map<string, GscData> {
  const merged = new Map<string, GscData>();

  // Build normalized lookup from GSC data, plus a protocol/www-agnostic one
  // that only answers when a single GSC URL has that key (http://a.test/x
  // and https://www.a.test/x can both be real rows: then no guess is made).
  const normalizedGsc = new Map<string, GscData>();
  const looseGsc = new Map<string, { norm: string; data: GscData } | null>();
  for (const [url, data] of gscData) {
    const norm = normalizeUrl(url);
    normalizedGsc.set(norm, data);
    const key = looseKey(norm);
    const seen = looseGsc.get(key);
    if (seen === undefined) looseGsc.set(key, { norm, data });
    else if (seen && seen.norm !== norm) looseGsc.set(key, null);
  }

  for (const url of crawledUrls) {
    const norm = normalizeUrl(url);
    // Try with/without trailing slash
    const alt = norm.endsWith('/') ? norm.slice(0, -1) : norm + '/';

    const data = normalizedGsc.get(norm)
      ?? normalizedGsc.get(alt)
      ?? looseGsc.get(looseKey(norm))?.data
      ?? looseGsc.get(looseKey(alt))?.data;
    if (data) merged.set(url, data);
  }

  return merged;
}

/** Normalized URL without its protocol and a leading "www." on the host. */
function looseKey(norm: string): string {
  return norm.replace(/^https?:\/\/(www\.)?/i, '');
}

/**
 * Percent-decode only RFC 3986 unreserved characters (letters, digits,
 * - . _ ~) and upper-case the hex of the rest: /a%2Fb stays a different
 * resource from /a/b, while /%7Euser and /~user match.
 */
function normalizePercentEncoding(path: string): string {
  return path.replace(/%[0-9a-fA-F]{2}/g, (m) => {
    const ch = String.fromCharCode(parseInt(m.slice(1), 16));
    return /[A-Za-z0-9\-._~]/.test(ch) ? ch : m.toUpperCase();
  });
}

/**
 * Normalize URL for matching: lowercase host, remove fragment, sort query params,
 * normalize percent-encoding (unreserved characters only), and remove default ports.
 */
function normalizeUrl(rawUrl: string): string {
  try {
    const u = new URL(rawUrl);
    u.hash = '';
    // Lowercase the hostname
    u.hostname = u.hostname.toLowerCase();
    // Remove default ports (:443 for HTTPS, :80 for HTTP)
    if ((u.protocol === 'https:' && u.port === '443') ||
        (u.protocol === 'http:' && u.port === '80')) {
      u.port = '';
    }
    // Same spelling for equivalent encodings; reserved ones (%2F) are kept
    u.pathname = normalizePercentEncoding(u.pathname);
    // Sort query params for consistent comparison
    const params = new URLSearchParams(u.search);
    const sorted = new URLSearchParams([...params.entries()].sort());
    u.search = sorted.toString();
    return u.toString();
  } catch {
    return rawUrl;
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

/**
 * List available GSC properties for the authenticated user.
 */
export async function listGscProperties(auth: AuthClient): Promise<{ url: string; level: string }[]> {
  const searchconsole = google.searchconsole({ version: 'v1', auth });
  const response = await searchconsole.sites.list();
  const entries = response.data.siteEntry || [];
  return entries.map(e => ({
    url: e.siteUrl || '',
    level: e.permissionLevel || 'unknown',
  }));
}

/**
 * Auto-detect the best GSC property for a given seed URL.
 * Prefers domain properties, then exact URL match.
 */
export async function autoDetectProperty(auth: AuthClient, seedUrl: string): Promise<string | null> {
  const properties = await listGscProperties(auth);
  if (properties.length === 0) return null;

  let seed: URL;
  try {
    seed = new URL(seedUrl);
  } catch {
    return null; // Malformed URL — cannot auto-detect
  }
  const domain = seed.hostname.replace(/^www\./, '');

  // 1. Try domain property
  const domainProp = properties.find(p => p.url === `sc-domain:${domain}`);
  if (domainProp) return domainProp.url;

  // 2. Try exact URL prefix match
  const origin = seed.origin + '/';
  const urlProp = properties.find(p => p.url === origin);
  if (urlProp) return urlProp.url;

  // 3. Try with/without www
  const wwwOrigin = seed.origin.includes('://www.')
    ? seed.origin.replace('://www.', '://') + '/'
    : seed.origin.replace('://', '://www.') + '/';
  const wwwProp = properties.find(p => p.url === wwwOrigin);
  if (wwwProp) return wwwProp.url;

  return null;
}
