/**
 * Byte-capped body reading. Crawled servers are untrusted: a page, robots.txt
 * or sitemap may be endless or huge. These helpers stop reading at a cap
 * instead of buffering the whole response (undici's body.text() has no cap).
 */

/** Default cap for one HTML page (same as the Go crawler's maxBodySize). */
export const DEFAULT_MAX_HTML_BYTES = 10 * 1024 * 1024;
/** robots.txt: Google reads at most 500 KiB; we allow a little more. */
export const MAX_ROBOTS_BYTES = 512 * 1024;
/** One sitemap file, uncompressed (sitemaps.org protocol limit). */
export const MAX_SITEMAP_BYTES = 50 * 1024 * 1024;

export interface CappedBody {
  text: string;
  /** Bytes read (at most maxBytes). */
  bytes: number;
  /** True when the body was longer than maxBytes and was cut. */
  truncated: boolean;
}

/** Minimal shape of an undici response body (async iterable of chunks). */
interface ReadableBody extends AsyncIterable<Uint8Array | string> {
  destroy?: (err?: Error) => unknown;
}

/**
 * Read at most maxBytes from a body and decode it as UTF-8. When the body is
 * longer, reading stops, the stream is destroyed (the connection is closed,
 * no more data is downloaded) and `truncated` is set.
 */
export async function readBodyCapped(body: ReadableBody, maxBytes: number): Promise<CappedBody> {
  const chunks: Buffer[] = [];
  let bytes = 0;
  let truncated = false;
  for await (const raw of body) {
    const chunk = typeof raw === 'string' ? Buffer.from(raw) : Buffer.from(raw.buffer, raw.byteOffset, raw.byteLength);
    const room = maxBytes - bytes;
    if (chunk.length > room) {
      if (room > 0) chunks.push(chunk.subarray(0, room));
      bytes += Math.max(room, 0);
      truncated = true;
      break;
    }
    chunks.push(chunk);
    bytes += chunk.length;
  }
  if (truncated) {
    try { body.destroy?.(); } catch { /* already closed */ }
  }
  // TextDecoder without `stream` drops a multi-byte character cut at the end
  // as U+FFFD, which is fine for a truncated document.
  return { text: new TextDecoder('utf-8').decode(Buffer.concat(chunks, bytes)), bytes, truncated };
}

/** Content-Length as a number, or null when absent or not a number. */
export function declaredLength(header: string | string[] | undefined): number | null {
  const v = Array.isArray(header) ? header[0] : header;
  if (typeof v !== 'string') return null;
  const n = Number.parseInt(v, 10);
  return Number.isFinite(n) && n >= 0 ? n : null;
}
