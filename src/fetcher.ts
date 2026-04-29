import { request, type Dispatcher } from 'undici';
import type { FetchResult, HeadResult, RedirectHop } from './types.js';
import { renderPage } from './browser.js';
import { formatError, isPrivateUrl } from './utils.js';

const MAX_RETRIES = 3;
const MAX_REDIRECTS = 10;
const BACKOFF_BASE_MS = 1000;
const EXTERNAL_CHECK_TIMEOUT_MS = 10_000;
const MAX_RETRY_AFTER_MS = 60_000; // Cap Retry-After at 60s

/** Parse Retry-After header (seconds or HTTP-date) with a safety cap. */
function parseRetryAfter(header: string | undefined, fallback: number): number {
  if (!header) return fallback;
  const secs = parseInt(header, 10);
  if (!isNaN(secs)) return Math.min(secs * 1000, MAX_RETRY_AFTER_MS);
  const date = new Date(header).getTime();
  if (!isNaN(date)) return Math.max(1000, Math.min(date - Date.now(), MAX_RETRY_AFTER_MS));
  return fallback;
}

export interface FetchOptions {
  userAgent: string;
  jsRendering?: boolean;
  customHeaders?: Record<string, string>;
  cookies?: string;
  dispatcher?: Dispatcher;
}

export async function fetchPage(
  url: string,
  options: FetchOptions,
): Promise<FetchResult> {
  const { userAgent, jsRendering = false, customHeaders = {}, cookies = '', dispatcher } = options;
  const start = Date.now();
  const redirectChain: RedirectHop[] = [];
  let currentUrl = url;
  let retries = 0;

  const requestHeaders: Record<string, string> = {
    'User-Agent': userAgent,
    'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8',
    'Accept-Language': 'en-US,en;q=0.5',
    ...customHeaders,
  };
  if (cookies) {
    requestHeaders['Cookie'] = cookies;
  }

  while (true) {
    try {
      const { statusCode, headers, body } = await request(currentUrl, {
        headers: requestHeaders,
        maxRedirections: 0,
        dispatcher,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      } as any);

      if (statusCode >= 300 && statusCode < 400) {
        const location = headers.location as string | undefined;
        if (location) {
          await body.dump();

          if (redirectChain.length >= MAX_REDIRECTS) {
            return {
              url, finalUrl: currentUrl, statusCode, redirectChain,
              headers: {}, html: '', contentType: '',
              responseTimeMs: Date.now() - start,
              transferSize: 0,
              error: `Max redirects (${MAX_REDIRECTS}) exceeded`,
            };
          }

          redirectChain.push({ url: currentUrl, statusCode });
          const resolvedLocation = new URL(location, currentUrl).toString();

          if (isPrivateUrl(resolvedLocation)) {
            return {
              url, finalUrl: currentUrl, statusCode, redirectChain,
              headers: {}, html: '', contentType: '',
              responseTimeMs: Date.now() - start,
              transferSize: 0,
              error: `Redirect to private/loopback address blocked: ${resolvedLocation}`,
            };
          }

          currentUrl = resolvedLocation;
          continue;
        }
      }

      if (statusCode === 429 && retries < MAX_RETRIES) {
        await body.dump();
        const delay = parseRetryAfter(
          headers['retry-after'] as string | undefined,
          BACKOFF_BASE_MS * Math.pow(2, retries),
        );
        retries++;
        await sleep(delay);
        continue;
      }

      const contentType = (headers['content-type'] as string) || '';
      const isHtml = contentType.includes('text/html') || contentType.includes('application/xhtml+xml');

      let html = '';
      let transferSize = 0;
      if (isHtml) {
        html = await body.text();
        // Estimate transfer size from content-length header or byte length of body
        const clHeader = headers['content-length'];
        if (typeof clHeader === 'string') {
          transferSize = parseInt(clHeader, 10) || 0;
        }
        if (transferSize === 0 && html) {
          transferSize = Buffer.byteLength(html, 'utf-8');
        }
      } else {
        await body.dump();
        const clHeader = headers['content-length'];
        if (typeof clHeader === 'string') {
          transferSize = parseInt(clHeader, 10) || 0;
        }
      }

      const responseHeaders: Record<string, string> = {};
      for (const [key, value] of Object.entries(headers)) {
        if (typeof value === 'string') {
          responseHeaders[key] = value;
        } else if (Array.isArray(value)) {
          responseHeaders[key] = value.join(', ');
        }
      }

      let rawHtml: string | undefined;
      if (jsRendering && isHtml && html) {
        try {
          process.stderr.write(`  [js] Re-rendering with Playwright: ${currentUrl}\n`);
          rawHtml = html; // Preserve original HTML before rendering
          const rendered = await renderPage(currentUrl, userAgent);
          html = rendered.html;
        } catch {
          rawHtml = undefined; // Fall back to static HTML, no comparison
        }
      }

      return {
        url, finalUrl: currentUrl, statusCode, redirectChain,
        headers: responseHeaders, html, rawHtml, contentType,
        responseTimeMs: Date.now() - start,
        transferSize,
      };
    } catch (err) {
      if (retries < MAX_RETRIES) {
        const delay = BACKOFF_BASE_MS * Math.pow(2, retries);
        retries++;
        await sleep(delay);
        continue;
      }

      return {
        url, finalUrl: currentUrl, statusCode: 0, redirectChain,
        headers: {}, html: '', contentType: '',
        responseTimeMs: Date.now() - start,
        transferSize: 0,
        error: formatError(err),
      };
    }
  }
}

// HEAD-check an external URL and return its status code
export async function checkExternalUrl(
  url: string,
  userAgent: string,
): Promise<{ statusCode: number; error?: string }> {
  // SEC-1: SSRF protection for external link checking
  if (isPrivateUrl(url)) {
    return { statusCode: 0, error: 'Blocked: private/loopback address' };
  }
  try {
    const { statusCode, body } = await request(url, {
      method: 'HEAD',
      headers: { 'User-Agent': userAgent },
      maxRedirections: 5,
      headersTimeout: EXTERNAL_CHECK_TIMEOUT_MS,
      bodyTimeout: EXTERNAL_CHECK_TIMEOUT_MS,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
    await body.dump();
    return { statusCode };
  } catch (err) {
    return { statusCode: 0, error: formatError(err) };
  }
}

// Phase 6: HEAD-check a resource URL to get its content-length
export async function headResourceSize(
  url: string,
  userAgent: string,
): Promise<number | null> {
  if (isPrivateUrl(url)) return null;
  try {
    const { headers, body } = await request(url, {
      method: 'HEAD',
      headers: { 'User-Agent': userAgent },
      maxRedirections: 5,
      headersTimeout: EXTERNAL_CHECK_TIMEOUT_MS,
      bodyTimeout: EXTERNAL_CHECK_TIMEOUT_MS,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
    await body.dump();
    const cl = headers['content-length'];
    if (typeof cl === 'string') {
      const size = parseInt(cl, 10);
      return isNaN(size) ? null : size;
    }
    return null;
  } catch {
    return null;
  }
}

/**
 * HEAD-only fetch: lightweight request that skips body download.
 * Tracks redirects and extracts all response headers including SEO directives.
 */
export async function fetchHead(
  url: string,
  options: FetchOptions,
): Promise<HeadResult> {
  // SSRF protection: block requests to private/loopback addresses
  if (isPrivateUrl(url)) {
    return {
      url, finalUrl: url, statusCode: 0, redirectChain: [],
      responseTimeMs: 0, contentType: '', contentLength: null, server: '',
      xRobotsTag: null, linkCanonical: null, hsts: false,
      csp: false, xFrameOptions: null, referrerPolicy: null,
      cacheControl: null, headers: {},
      error: 'Blocked: private/loopback address',
    };
  }

  const { userAgent, customHeaders = {}, cookies = '', dispatcher } = options;
  const start = Date.now();
  const redirectChain: RedirectHop[] = [];
  let currentUrl = url;
  let retries = 0;

  const requestHeaders: Record<string, string> = {
    'User-Agent': userAgent,
    'Accept': '*/*',
    ...customHeaders,
  };
  if (cookies) {
    requestHeaders['Cookie'] = cookies;
  }

  while (true) {
    try {
      const { statusCode, headers, body } = await request(currentUrl, {
        method: 'HEAD',
        headers: requestHeaders,
        maxRedirections: 0,
        headersTimeout: EXTERNAL_CHECK_TIMEOUT_MS,
        bodyTimeout: EXTERNAL_CHECK_TIMEOUT_MS,
        dispatcher,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      } as any);
      await body.dump();

      // Follow redirects manually to track chain
      if (statusCode >= 300 && statusCode < 400) {
        const location = (headers.location as string | undefined)?.trim();
        if (location) {
          if (redirectChain.length >= MAX_REDIRECTS) {
            return {
              url, finalUrl: currentUrl, statusCode, redirectChain,
              responseTimeMs: Date.now() - start,
              contentType: '', contentLength: null, server: '',
              xRobotsTag: null, linkCanonical: null, hsts: false,
              csp: false, xFrameOptions: null, referrerPolicy: null,
              cacheControl: null, headers: {},
              error: `Max redirects (${MAX_REDIRECTS}) exceeded`,
            };
          }
          redirectChain.push({ url: currentUrl, statusCode });
          const resolved = new URL(location, currentUrl).toString();
          if (isPrivateUrl(resolved)) {
            return {
              url, finalUrl: currentUrl, statusCode, redirectChain,
              responseTimeMs: Date.now() - start,
              contentType: '', contentLength: null, server: '',
              xRobotsTag: null, linkCanonical: null, hsts: false,
              csp: false, xFrameOptions: null, referrerPolicy: null,
              cacheControl: null, headers: {},
              error: `Redirect to private address blocked: ${resolved}`,
            };
          }
          currentUrl = resolved;
          continue;
        }
      }

      // Retry on 429 with Retry-After support
      if (statusCode === 429 && retries < MAX_RETRIES) {
        const delay = parseRetryAfter(
          headers['retry-after'] as string | undefined,
          BACKOFF_BASE_MS * Math.pow(2, retries),
        );
        retries++;
        await sleep(delay);
        continue;
      }

      // Flatten headers
      const responseHeaders: Record<string, string> = {};
      for (const [key, value] of Object.entries(headers)) {
        if (typeof value === 'string') {
          responseHeaders[key] = value;
        } else if (Array.isArray(value)) {
          responseHeaders[key] = value.join(', ');
        }
      }

      // Extract SEO-relevant headers
      const contentType = responseHeaders['content-type'] || '';
      const clHeader = responseHeaders['content-length'];
      const contentLength = clHeader ? (parseInt(clHeader, 10) || null) : null;

      // Parse Link header for canonical: Link: <url>; rel="canonical"
      let linkCanonical: string | null = null;
      const linkHeader = responseHeaders['link'];
      if (linkHeader) {
        const canonicalMatch = linkHeader.match(/<([^>]+)>;\s*rel="canonical"/i);
        if (canonicalMatch) {
          linkCanonical = canonicalMatch[1];
        }
      }

      return {
        url,
        finalUrl: currentUrl,
        statusCode,
        redirectChain,
        responseTimeMs: Date.now() - start,
        contentType,
        contentLength,
        server: responseHeaders['server'] || '',
        xRobotsTag: responseHeaders['x-robots-tag'] || null,
        linkCanonical,
        hsts: !!responseHeaders['strict-transport-security'],
        csp: !!responseHeaders['content-security-policy'],
        xFrameOptions: responseHeaders['x-frame-options'] || null,
        referrerPolicy: responseHeaders['referrer-policy'] || null,
        cacheControl: responseHeaders['cache-control'] || null,
        headers: responseHeaders,
      };
    } catch (err) {
      if (retries < MAX_RETRIES) {
        const delay = BACKOFF_BASE_MS * Math.pow(2, retries);
        retries++;
        await sleep(delay);
        continue;
      }
      return {
        url, finalUrl: currentUrl, statusCode: 0, redirectChain,
        responseTimeMs: Date.now() - start,
        contentType: '', contentLength: null, server: '',
        xRobotsTag: null, linkCanonical: null, hsts: false,
        csp: false, xFrameOptions: null, referrerPolicy: null,
        cacheControl: null, headers: {},
        error: formatError(err),
      };
    }
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
