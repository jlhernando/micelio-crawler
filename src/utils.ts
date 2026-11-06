import { isPrivateHostname } from './net-guard.js';

/**
 * Extract a human-readable error message from an unknown caught value.
 */
export function formatError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** Name a snippet file's results are stored under (path without extension, separators as _). */
export function snippetName(snippetPath: string): string {
  return snippetPath.replace(/\.(js|mjs|cjs)$/, '').replace(/[/\\]/g, '_');
}

/**
 * Map every form of a crawled page's URL (its URL, its final URL after
 * redirects, and the trailing-slash variants of both) to the page's own URL.
 *
 * Every page owns its own URL first; aliases only fill keys no page owns.
 * So a page that redirects to D never takes D away from the crawled page D,
 * whatever order the pages arrived in (concurrent crawls finish in any order).
 */
export function buildPageUrlResolver(pages: readonly { url: string; finalUrl?: string }[]): Map<string, string> {
  const resolver = new Map<string, string>();
  for (const page of pages) resolver.set(page.url, page.url);
  const alias = (form: string | undefined, owner: string) => {
    if (form && !resolver.has(form)) resolver.set(form, owner);
  };
  // Weaker forms later: final URLs, then slash variants of own URLs, then
  // slash variants of final URLs
  for (const page of pages) alias(page.finalUrl, page.url);
  for (const page of pages) alias(slashVariant(page.url), page.url);
  for (const page of pages) if (page.finalUrl) alias(slashVariant(page.finalUrl), page.url);
  return resolver;
}

function slashVariant(url: string): string {
  return url.endsWith('/') && url.length > 1 ? url.slice(0, -1) : url + '/';
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
    // A non-default port is another origin (example.com:8443 is not
    // example.com); URL.port is already '' for the scheme's default port
    const host = parsed.hostname.replace(/^www\./, '') + (parsed.port ? ':' + parsed.port : '');
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
