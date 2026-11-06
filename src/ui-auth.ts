/**
 * Access control for the legacy dashboard server (ui-server.ts).
 *
 * - Loopback-only by default; a non-loopback bind needs an auth token.
 * - With a token, /api/* needs "Authorization: Bearer <token>" and /ws
 *   needs the token as bearer or ?token=.
 * - Without a token, the Host header must name this computer, so a web page
 *   that rebinds its own DNS name to 127.0.0.1 cannot reach the API.
 * - State-changing requests and WebSocket upgrades from another origin
 *   (Origin / Sec-Fetch-Site) are refused, so a web page cannot drive crawls.
 */
import type { IncomingMessage } from 'node:http';
import { timingSafeEqual } from 'node:crypto';

export interface UiAccessOptions {
  authToken: string;
}

const LOOPBACK_HOSTS = new Set(['localhost', '127.0.0.1', '::1']);

/**
 * Request target as a URL, parsed against a constant base: the Host header is
 * client-controlled and a malformed one (e.g. "[::1") makes new URL throw.
 * Only pathname and search are meaningful. Returns null when unparsable.
 */
export function parseRequestUrl(req: { url?: string }): URL | null {
  const target = req.url || '/';
  try {
    return target.startsWith('/') ? new URL(`http://localhost${target}`) : new URL(target, 'http://localhost/');
  } catch {
    return null;
  }
}

export function isLoopbackHost(host: string): boolean {
  const h = host.replace(/^\[|\]$/g, '').toLowerCase();
  return LOOPBACK_HOSTS.has(h) || /^127\.\d+\.\d+\.\d+$/.test(h);
}

/** Hostname from a Host header value ("localhost:3100", "[::1]:3100"). */
function hostHeaderName(value: string | undefined): string {
  if (!value) return '';
  try {
    return new URL(`http://${value}`).hostname;
  } catch {
    return '';
  }
}

function tokenMatches(given: string, expected: string): boolean {
  const a = Buffer.from(given);
  const b = Buffer.from(expected);
  return a.length === b.length && timingSafeEqual(a, b);
}

function requestToken(req: IncomingMessage, allowQuery: boolean): string {
  const auth = req.headers.authorization || '';
  if (auth.startsWith('Bearer ')) return auth.slice(7);
  if (allowQuery) {
    try {
      return new URL(req.url || '/', 'http://localhost').searchParams.get('token') || '';
    } catch {
      return '';
    }
  }
  return '';
}

/** Whether the request comes from the dashboard's own origin (or no browser). */
function sameOrigin(req: IncomingMessage): boolean {
  const site = req.headers['sec-fetch-site'];
  if (site && site !== 'same-origin' && site !== 'none') return false;
  const origin = req.headers.origin;
  if (!origin) return true;
  try {
    const o = new URL(origin);
    return o.host === req.headers.host || isLoopbackHost(o.hostname);
  } catch {
    return false;
  }
}

const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS']);

/**
 * Decide whether an /api/* or /ws request may proceed. Returns null when
 * allowed, else the HTTP status and message to send back.
 */
export function checkUiAccess(
  req: IncomingMessage,
  opts: UiAccessOptions,
  kind: 'api' | 'ws',
): { status: number; message: string } | null {
  if (opts.authToken) {
    if (!tokenMatches(requestToken(req, kind === 'ws'), opts.authToken)) {
      return { status: 401, message: 'unauthorized' };
    }
  } else if (!isLoopbackHost(hostHeaderName(req.headers.host))) {
    return { status: 403, message: 'forbidden host' };
  }
  const changesState = kind === 'ws' || !SAFE_METHODS.has((req.method || 'GET').toUpperCase());
  if (changesState && !sameOrigin(req)) {
    return { status: 403, message: 'cross-origin request refused' };
  }
  return null;
}
