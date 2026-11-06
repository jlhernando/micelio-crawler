/**
 * Webhook notification sender for scheduled crawl completions.
 */

import { request, type Dispatcher } from 'undici';
import { BlockedAddressError, guardedDispatcher, stripCredentialsOnCrossOrigin, urlBlockReason } from './net-guard.js';
import { redactUrl } from './utils.js';

export interface WebhookPayload {
  event: 'crawl_complete' | 'crawl_failed';
  url: string;
  timestamp: string;
  duration: string;
  pages: number;
  errors: number;
  outputFile: string;
  htmlReport?: string;
  schedule?: {
    cron: string;
    runNumber: number;
    nextRun: string;
  };
}

export interface WebhookOptions {
  url: string;
  headers?: Record<string, string>;
  /** Test seam / custom transport. Default: the SSRF-guarded dispatcher. */
  dispatcher?: Dispatcher;
}

/** Redirects followed per delivery (307/308 only, each hop checked). */
export const MAX_WEBHOOK_REDIRECTS = 3;

class WebhookBlockedError extends Error {}

/**
 * POST once, following at most MAX_WEBHOOK_REDIRECTS 307/308 redirects by
 * hand. Every hop goes through the SSRF guard (literal check here, resolved
 * addresses in the guarded dispatcher), so a webhook server cannot bounce
 * the POST into loopback, the LAN or cloud metadata. Custom headers are
 * dropped when a hop changes host, port or downgrades to http.
 * Other 3xx answers are not followed and count as a failed delivery.
 */
async function postOnce(
  url: string,
  headers: Record<string, string>,
  body: string,
  dispatcher: Dispatcher,
): Promise<{ statusCode: number }> {
  let current = url;
  let hopHeaders = headers;
  for (let hop = 0; ; hop++) {
    const blocked = urlBlockReason(current);
    if (blocked) throw new WebhookBlockedError(`${blocked} (${redactUrl(current, 'origin')})`);
    const res = await request(current, {
      method: 'POST',
      headers: hopHeaders,
      body,
      dispatcher,
      signal: AbortSignal.timeout(10_000),
    });
    await res.body.dump();
    const location = res.headers.location;
    if ((res.statusCode === 307 || res.statusCode === 308) && typeof location === 'string') {
      if (hop >= MAX_WEBHOOK_REDIRECTS) {
        throw new Error(`too many redirects (max ${MAX_WEBHOOK_REDIRECTS})`);
      }
      const next = new URL(location, current).toString();
      // Custom headers may hold tokens: they stay with the configured host
      hopHeaders = {
        ...stripCredentialsOnCrossOrigin(hopHeaders, current, next, url),
        'Content-Type': 'application/json',
      };
      current = next;
      continue;
    }
    return { statusCode: res.statusCode };
  }
}

/**
 * Send a webhook notification. Retries once on failure.
 * Logs warnings on error but never throws.
 */
export async function sendWebhook(
  options: WebhookOptions,
  payload: WebhookPayload,
): Promise<boolean> {
  const { url, headers = {} } = options;
  const dispatcher = options.dispatcher ?? guardedDispatcher();
  // Log only scheme + host: webhook paths and queries often hold the token
  const safeUrl = redactUrl(url, 'origin');

  const body = JSON.stringify(payload);
  const requestHeaders: Record<string, string> = {
    'Content-Type': 'application/json',
    'User-Agent': 'Micelio/1.0 Scheduler',
    ...headers,
  };

  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      const { statusCode } = await postOnce(url, requestHeaders, body, dispatcher);
      if (statusCode >= 200 && statusCode < 300) {
        return true;
      }

      process.stderr.write(`  [webhook] Attempt ${attempt + 1}: HTTP ${statusCode}\n`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      process.stderr.write(`  [webhook] Attempt ${attempt + 1} failed: ${message}\n`);
      // A blocked target stays blocked: do not retry
      if (err instanceof WebhookBlockedError || err instanceof BlockedAddressError
        || (err as NodeJS.ErrnoException)?.code === 'EBLOCKED') break;
    }

    // Wait 2 seconds before retry
    if (attempt === 0) {
      await new Promise((r) => setTimeout(r, options.dispatcher ? 0 : 2000));
    }
  }

  process.stderr.write(`  [webhook] Failed to deliver notification to ${safeUrl}\n`);
  return false;
}
