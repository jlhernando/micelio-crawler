/**
 * CrUX (Chrome User Experience Report) API integration.
 *
 * Fetches real-user Core Web Vitals per URL from the CrUX API.
 * Rate limit: 150 requests/minute with an API key, 25/day without.
 * Requires a Google API key with CrUX API enabled.
 *
 * Docs: https://developer.chrome.com/docs/crux/api
 */

export interface CruxData {
  /** Largest Contentful Paint in ms (p75) */
  lcpMs: number | null;
  /** First Input Delay in ms (p75) — deprecated, kept for historical data */
  fidMs: number | null;
  /** Interaction to Next Paint in ms (p75) */
  inpMs: number | null;
  /** Cumulative Layout Shift (p75) */
  cls: number | null;
  /** Time to First Byte in ms (p75) */
  ttfbMs: number | null;
  /** First Contentful Paint in ms (p75) */
  fcpMs: number | null;
  /** Form factor used for the query */
  formFactor: 'PHONE' | 'DESKTOP' | 'ALL';
}

export interface CruxOptions {
  apiKey: string;
  urls: string[];
  formFactor?: 'PHONE' | 'DESKTOP';
  /** Max concurrent requests (default: 10) */
  concurrency?: number;
}

interface CruxMetric {
  percentiles?: { p75: number };
}

interface CruxApiResponse {
  record?: {
    metrics?: {
      largest_contentful_paint?: CruxMetric;
      first_input_delay?: CruxMetric;
      interaction_to_next_paint?: CruxMetric;
      cumulative_layout_shift?: CruxMetric;
      experimental_time_to_first_byte?: CruxMetric;
      first_contentful_paint?: CruxMetric;
    };
  };
  error?: { message: string; code: number };
}

const CRUX_API_URL = 'https://chromeuxreport.googleapis.com/v1/records:queryRecord';

/**
 * Fetch CrUX data for a batch of URLs.
 * Handles rate limiting with a sliding window (150 req/min).
 */
export async function fetchCruxData(options: CruxOptions): Promise<Map<string, CruxData>> {
  const { apiKey, urls, formFactor, concurrency = 10 } = options;
  const results = new Map<string, CruxData>();

  if (!apiKey) {
    throw new Error('CrUX API requires a Google API key (--crux-key). Enable CrUX API at https://console.cloud.google.com');
  }

  // Rate limiting: 150 req/min = ~2.5 req/sec
  const RATE_LIMIT = 150;
  const WINDOW_MS = 60_000;
  const timestamps: number[] = [];

  async function waitForSlot(): Promise<void> {
    while (true) {
      const now = Date.now();
      // Remove timestamps outside the window
      while (timestamps.length > 0 && timestamps[0] < now - WINDOW_MS) {
        timestamps.shift();
      }
      if (timestamps.length < RATE_LIMIT) {
        timestamps.push(now);
        return;
      }
      // Wait until the oldest request exits the window
      const waitMs = timestamps[0] + WINDOW_MS - now + 10;
      await new Promise((resolve) => setTimeout(resolve, waitMs));
    }
  }

  async function fetchSingle(url: string, retries = 0): Promise<void> {
    await waitForSlot();

    const body: Record<string, unknown> = { url };
    if (formFactor) {
      body.formFactor = formFactor;
    }

    try {
      const response = await fetch(`${CRUX_API_URL}?key=${apiKey}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });

      if (response.status === 404) {
        // No CrUX data for this URL (not enough traffic)
        return;
      }

      if (!response.ok) {
        const text = await response.text();
        process.stderr.write(`CrUX warning: ${response.status} for ${url}: ${text.substring(0, 200)}\n`);
        return;
      }

      const data = (await response.json()) as CruxApiResponse;

      if (data.error) {
        // API error (e.g., quota exceeded)
        if (data.error.code === 429 && retries < 1) {
          process.stderr.write('CrUX rate limit hit, waiting 60s...\n');
          await new Promise((resolve) => setTimeout(resolve, 60_000));
          return fetchSingle(url, retries + 1);
        }
        return;
      }

      const metrics = data.record?.metrics;
      if (!metrics) return;

      results.set(url, {
        lcpMs: metrics.largest_contentful_paint?.percentiles?.p75 ?? null,
        fidMs: metrics.first_input_delay?.percentiles?.p75 ?? null,
        inpMs: metrics.interaction_to_next_paint?.percentiles?.p75 ?? null,
        cls: metrics.cumulative_layout_shift?.percentiles?.p75 ?? null,
        ttfbMs: metrics.experimental_time_to_first_byte?.percentiles?.p75 ?? null,
        fcpMs: metrics.first_contentful_paint?.percentiles?.p75 ?? null,
        formFactor: formFactor || 'ALL',
      });
    } catch (err) {
      process.stderr.write(`CrUX fetch error for ${url}: ${(err as Error).message}\n`);
    }
  }

  // Process URLs with bounded concurrency
  const queue = [...urls];
  const active: Promise<void>[] = [];

  while (queue.length > 0 || active.length > 0) {
    while (active.length < concurrency && queue.length > 0) {
      const url = queue.shift()!;
      const task = fetchSingle(url).then(() => {
        const idx = active.indexOf(task);
        if (idx !== -1) active.splice(idx, 1);
      });
      active.push(task);
    }
    if (active.length > 0) {
      await Promise.race(active);
    }
  }

  return results;
}

/**
 * Get CrUX assessment for a given metric value.
 * Uses Google's "good", "needs improvement", "poor" thresholds.
 */
export function getCruxAssessment(metric: string, value: number): 'good' | 'needs-improvement' | 'poor' {
  switch (metric) {
    case 'lcp':
      return value <= 2500 ? 'good' : value <= 4000 ? 'needs-improvement' : 'poor';
    case 'fid':
      return value <= 100 ? 'good' : value <= 300 ? 'needs-improvement' : 'poor';
    case 'inp':
      return value <= 200 ? 'good' : value <= 500 ? 'needs-improvement' : 'poor';
    case 'cls':
      return value <= 0.1 ? 'good' : value <= 0.25 ? 'needs-improvement' : 'poor';
    case 'ttfb':
      return value <= 800 ? 'good' : value <= 1800 ? 'needs-improvement' : 'poor';
    case 'fcp':
      return value <= 1800 ? 'good' : value <= 3000 ? 'needs-improvement' : 'poor';
    default:
      return 'needs-improvement';
  }
}
