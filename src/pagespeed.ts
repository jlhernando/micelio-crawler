import type { PageSpeedData } from './types.js';
import { formatError } from './utils.js';

const PSI_API_URL = 'https://www.googleapis.com/pagespeedonline/v5/runPagespeed';

// B1 fix: Use promise chain for concurrency-safe rate limiting
const MIN_INTERVAL_MS = 4000; // ~25 req/100s with buffer
let pending: Promise<void> = Promise.resolve();

/**
 * Fetch PageSpeed Insights data for a URL.
 * Requests are serialized via a promise chain to enforce rate limiting under concurrency.
 */
export function fetchPageSpeed(
  url: string,
  apiKey: string = '',
): Promise<PageSpeedData> {
  return new Promise<PageSpeedData>((resolve) => {
    pending = pending.then(async () => {
      await new Promise((r) => setTimeout(r, MIN_INTERVAL_MS));
      resolve(await doFetch(url, apiKey));
    });
  });
}

async function doFetch(url: string, apiKey: string): Promise<PageSpeedData> {
  const params = new URLSearchParams({
    url,
    strategy: 'mobile',
    category: 'performance',
  });
  if (apiKey) {
    params.set('key', apiKey);
  }

  try {
    const response = await fetch(`${PSI_API_URL}?${params}`, {
      signal: AbortSignal.timeout(60000),
    });

    if (!response.ok) {
      const text = await response.text();
      return makeError(`HTTP ${response.status}: ${text.substring(0, 200)}`);
    }

    const data = await response.json();
    const audit = data.lighthouseResult;

    if (!audit) {
      return makeError('No Lighthouse result in response');
    }

    const metrics = audit.audits;

    // B3 fix: Add INP alongside deprecated FID
    return {
      performanceScore: Math.round((audit.categories?.performance?.score || 0) * 100),
      lcp: metrics?.['largest-contentful-paint']?.numericValue || 0,
      fid: metrics?.['max-potential-fid']?.numericValue || 0,
      inp: metrics?.['interaction-to-next-paint']?.numericValue || 0,
      cls: metrics?.['cumulative-layout-shift']?.numericValue || 0,
      ttfb: metrics?.['server-response-time']?.numericValue || 0,
      speedIndex: metrics?.['speed-index']?.numericValue || 0,
      tbt: metrics?.['total-blocking-time']?.numericValue || 0,
    };
  } catch (err) {
    return makeError(formatError(err));
  }
}

function makeError(message: string): PageSpeedData {
  return {
    performanceScore: 0,
    lcp: 0,
    fid: 0,
    inp: 0,
    cls: 0,
    ttfb: 0,
    speedIndex: 0,
    tbt: 0,
    error: message,
  };
}
