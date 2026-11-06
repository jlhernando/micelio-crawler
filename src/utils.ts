import { isPrivateHostname } from './net-guard.js';

/**
 * Extract a human-readable error message from an unknown caught value.
 */
export function formatError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/**
 * JSON safe to place inside an HTML <script> element: `<`, `>` and `&` become
 * <, > and &, so no end tag (any case), comment opener or
 * entity can appear, and U+2028/U+2029 are escaped for older JavaScript
 * parsers. The value parses back to exactly the same data.
 */
export function jsonForScript(value: unknown): string {
  return (JSON.stringify(value) ?? 'null')
    .replace(/</g, '\\u003c')
    .replace(/>/g, '\\u003e')
    .replace(/&/g, '\\u0026')
    .replace(/\u2028/g, '\\u2028')
    .replace(/\u2029/g, '\\u2029');
}

/**
 * URL safe to write to logs. Credentials can live in the userinfo, the path
 * (Slack/Discord webhook tokens) and the query (signed URLs, API keys).
 * - 'origin': scheme + host only, path replaced by "/..." when not empty
 *   (for webhook targets, where the path itself is the secret).
 * - 'path' (default): userinfo removed, path kept, every query value masked.
 * Not a URL: "[invalid URL]".
 */
export function redactUrl(raw: string, mode: 'origin' | 'path' = 'path'): string {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return '[invalid URL]';
  }
  const origin = `${u.protocol}//${u.host}`;
  if (mode === 'origin') {
    return u.pathname !== '/' || u.search || u.hash ? `${origin}/...` : origin;
  }
  let out = origin + u.pathname;
  if (u.search) {
    const keys = [...new Set([...u.searchParams.keys()])];
    out += '?' + keys.map((k) => `${encodeURIComponent(k)}=***`).join('&');
  }
  return out;
}

/**
 * Format a Date as YYYY-MM-DD string for API queries.
 */
export function formatDateYmd(date: Date): string {
  return date.toISOString().split('T')[0];
}

/**
 * Normalize a URL for consistent comparison: strip hash, trailing slash,
 * normalize default ports, and sort query parameters.
 */
export function normalizeUrl(url: string): string | null {
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return null;

    parsed.hash = '';

    if ((parsed.protocol === 'http:' && parsed.port === '80') ||
        (parsed.protocol === 'https:' && parsed.port === '443')) {
      parsed.port = '';
    }

    let pathname = parsed.pathname;
    if (pathname.length > 1 && pathname.endsWith('/')) {
      pathname = pathname.slice(0, -1);
    }
    parsed.pathname = pathname;

    if (parsed.search) {
      const params = new URLSearchParams(parsed.search);
      const sorted = new URLSearchParams([...params.entries()].sort());
      parsed.search = sorted.toString();
    }

    return parsed.toString();
  } catch {
    return null;
  }
}

/**
 * Normalize a URL for canonical/self-referencing comparison:
 * strips www prefix, trailing slash, protocol, and sorts query parameters.
 * Returns a protocol-agnostic key like "example.com/path?a=1&b=2".
 */
export function normalizeUrlForComparison(url: string): string {
  try {
    const parsed = new URL(url);
    const host = parsed.hostname.replace(/^www\./, '');
    const path = parsed.pathname.replace(/\/$/, '') || '/';
    if (parsed.search) {
      const params = new URLSearchParams(parsed.search);
      const sorted = new URLSearchParams([...params.entries()].sort());
      return host + path + '?' + sorted.toString();
    }
    return host + path;
  } catch {
    return url.replace(/\/$/, '');
  }
}

/**
 * Check whether a URL's host is localhost or a literal private address
 * (loopback, RFC 1918, link-local/metadata, CGNAT, IPv6 ULA/link-local,
 * IPv4-mapped IPv6, multicast). Fails closed: invalid URLs count as private.
 * Host names are checked again after DNS resolution by the guarded
 * dispatcher in net-guard.ts, which also pins the dialled address.
 */
export function isPrivateUrl(url: string): boolean {
  try {
    return isPrivateHostname(new URL(url).hostname);
  } catch {
    return true;
  }
}
