/**
 * Webhook notification sender for scheduled crawl completions.
 */

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

  const body = JSON.stringify(payload);
  const requestHeaders: Record<string, string> = {
    'Content-Type': 'application/json',
    'User-Agent': 'Micelio/1.0 Scheduler',
    ...headers,
  };

  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      const response = await fetch(url, {
        method: 'POST',
        headers: requestHeaders,
        body,
        signal: AbortSignal.timeout(10_000),
      });

      if (response.ok) {
        return true;
      }

      process.stderr.write(
        `  [webhook] Attempt ${attempt + 1}: HTTP ${response.status} ${response.statusText}\n`,
      );
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      process.stderr.write(`  [webhook] Attempt ${attempt + 1} failed: ${message}\n`);
    }

    // Wait 2 seconds before retry
    if (attempt === 0) {
      await new Promise((r) => setTimeout(r, 2000));
    }
  }

  // Redact URL path/query for logging (may contain tokens)
  let safeUrl = url;
  try {
    const parsed = new URL(url);
    safeUrl = `${parsed.protocol}//${parsed.host}/...`;
  } catch { /* use original */ }
  process.stderr.write(`  [webhook] Failed to deliver notification to ${safeUrl}\n`);
  return false;
}
