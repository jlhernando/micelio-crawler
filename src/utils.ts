/**
 * Extract a human-readable error message from an unknown caught value.
 */
export function formatError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
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
 * Check whether a URL points to a private/internal network address.
 * Used for SSRF protection. Covers IPv4 RFC 1918, IPv6 loopback/link-local/ULA,
 * and cloud metadata endpoints.
 * Note: DNS rebinding attacks (e.g. localtest.me) are out of scope for string-based checks.
 */
export function isPrivateUrl(url: string): boolean {
  try {
    const parsed = new URL(url);
    const host = parsed.hostname;
    // IPv4 loopback and common aliases
    if (host === 'localhost' || host === '127.0.0.1' || host === '0.0.0.0') return true;
    // IPv6 loopback — URL parser keeps brackets: hostname is "[::1]"
    if (host === '::1' || host === '[::1]') return true;
    // IPv4 private ranges (RFC 1918)
    if (host.startsWith('10.')) return true;
    if (host.startsWith('192.168.')) return true;
    if (/^172\.(1[6-9]|2\d|3[01])\./.test(host)) return true;
    // Cloud metadata endpoints
    if (host === '169.254.169.254') return true;
    // IPv6 link-local (fe80::/10) and unique-local (fc00::/7, includes fd00::)
    const hostLower = host.replace(/^\[|\]$/g, '').toLowerCase();
    if (hostLower.startsWith('fe80:') || hostLower.startsWith('fc') || hostLower.startsWith('fd')) return true;
    return false;
  } catch {
    return false;
  }
}
