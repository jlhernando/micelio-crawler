/**
 * Access control for the legacy dashboard server (ui-server.ts).
 *
 * - Loopback-only by default; a non-loopback bind needs an auth token.
 * - With a token, /api/* needs "Authorization: Bearer <token>" (CLI,
 *   scripts) or the login cookie (browser), and /ws also takes ?token=.
 *   The dashboard's login screen posts the token to POST /login
 *   (handleLogin), which sets an HttpOnly, SameSite=Strict cookie holding
 *   an HMAC of the token, never the token itself (same contract as the Go
 *   dashboard, internal/server/guard.go). Wrong tokens are throttled per
 *   client address (AuthLimiter).
 * - Without a token, the Host header must name this computer, so a web page
 *   that rebinds its own DNS name to 127.0.0.1 cannot reach the API.
 * - State-changing requests and WebSocket upgrades from another origin
 *   (Origin / Sec-Fetch-Site) are refused, so a web page cannot drive crawls.
 */
import type { IncomingMessage, ServerResponse } from 'node:http';
import type { TLSSocket } from 'node:tls';
import { createHmac, timingSafeEqual } from 'node:crypto';

export interface UiAccessOptions {
  authToken: string;
  /** Name of the login cookie (sessionCookieName); default for port 0. */
  cookieName?: string;
  /** Throttles wrong tokens per client address. */
  limiter?: AuthLimiter;
}

/**
 * Login cookie name. Cookies ignore ports, so the port is in the name: two
 * dashboards on one host keep separate logins.
 */
export function sessionCookieName(port: number): string {
  return port > 0 ? `micelio_session_${port}` : 'micelio_session';
}

/** What the login cookie stores: derived from the token, never the token. */
export function sessionValue(token: string): string {
  return createHmac('sha256', token).update('micelio-session').digest('hex');
}

function cookieValue(req: IncomingMessage, name: string): string {
  for (const part of (req.headers.cookie || '').split(';')) {
    const eq = part.indexOf('=');
    if (eq > 0 && part.slice(0, eq).trim() === name) return part.slice(eq + 1).trim();
  }
  return '';
}

/**
 * Wrong tokens per client address: the first FREE_FAILS cost nothing
 * (typos); each further one blocks token attempts from that address twice
 * as long as the last, from 1 s up to 5 min, during which wrong tokens get
 * 429. The right token (Bearer, login form or cookie) is never counted nor
 * blocked, so shared addresses (reverse proxy, IPv6 /64, the overflow
 * bucket) cannot lock the user out.
 */
export class AuthLimiter {
  static readonly FREE_FAILS = 5;
  static readonly MAX_BACKOFF_MS = 5 * 60_000;
  static readonly FORGET_MS = 30 * 60_000;
  static readonly MAX_CLIENTS = 10_000;
  private clients = new Map<string, { fails: number; until: number; last: number }>();
  constructor(private now: () => number = Date.now) {}

  private bucket(key: string): string {
    return this.clients.has(key) || this.clients.size < AuthLimiter.MAX_CLIENTS ? key : 'overflow';
  }

  /** Milliseconds token attempts from key are still refused (0 = allowed). */
  blockedMs(key: string): number {
    const s = this.clients.get(this.bucket(key));
    return s ? Math.max(0, s.until - this.now()) : 0;
  }

  fail(key: string): void {
    const now = this.now();
    if (this.clients.size >= AuthLimiter.MAX_CLIENTS) {
      for (const [k, v] of this.clients) {
        if (now > v.until && now - v.last > AuthLimiter.FORGET_MS) this.clients.delete(k);
      }
    }
    key = this.bucket(key);
    let s = this.clients.get(key);
    if (!s || now - s.last > AuthLimiter.FORGET_MS) {
      s = { fails: 0, until: 0, last: now };
      this.clients.set(key, s);
    }
    s.fails++;
    s.last = now;
    if (s.fails <= AuthLimiter.FREE_FAILS) return;
    const n = s.fails - AuthLimiter.FREE_FAILS - 1;
    s.until = now + (n < 20 ? Math.min(1000 * 2 ** n, AuthLimiter.MAX_BACKOFF_MS) : AuthLimiter.MAX_BACKOFF_MS);
  }
}

/** Address wrong tokens are counted for: the remote IP, or its /64 for IPv6. */
export function clientKey(req: IncomingMessage): string {
  const ip = (req.socket?.remoteAddress || '').replace(/^::ffff:(?=\d+\.)/, '');
  if (!ip.includes(':')) return ip;
  // Expand "::" and keep the first four groups
  const [head, tail = ''] = ip.split('::');
  const h = head ? head.split(':') : [];
  const t = tail ? tail.split(':') : [];
  const groups = ip.includes('::') ? [...h, ...Array(Math.max(0, 8 - h.length - t.length)).fill('0'), ...t] : h;
  return `${groups.slice(0, 4).map((g) => g.toLowerCase().replace(/^0+(?=.)/, '')).join(':')}::/64`;
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

/** Shortest dashboard token accepted. Wrong guesses are slowed, not stopped, so the token itself must be hard to guess. */
export const MIN_AUTH_TOKEN_LENGTH = 32;

/** Why a dashboard token is refused at startup, or null when it is long enough. */
export function authTokenProblem(token: string): string | null {
  if (!token) return null;
  if (token.length < MIN_AUTH_TOKEN_LENGTH) {
    return `--auth-token must be at least ${MIN_AUTH_TOKEN_LENGTH} characters (got ${token.length}). Generate one with: openssl rand -hex 32`;
  }
  return null;
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

/** Token the request offers (bearer header, or ?token= when allowed); null when none. */
function requestToken(req: IncomingMessage, allowQuery: boolean): string | null {
  const auth = req.headers.authorization || '';
  if (auth.startsWith('Bearer ')) return auth.slice(7);
  if (allowQuery) {
    try {
      return new URL(req.url || '/', 'http://localhost').searchParams.get('token');
    } catch {
      return null;
    }
  }
  return null;
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
    const cookie = cookieValue(req, opts.cookieName ?? sessionCookieName(0));
    const loggedIn = cookie !== '' && tokenMatches(cookie, sessionValue(opts.authToken));
    if (!loggedIn) {
      const given = requestToken(req, kind === 'ws');
      if (given === null) return { status: 401, message: 'unauthorized' };
      // The right token always gets in: only wrong tokens are counted and
      // throttled, so a flood of bad guesses from a shared address (reverse
      // proxy, one IPv6 /64, the overflow bucket) never locks out the user.
      if (!tokenMatches(given, opts.authToken)) {
        const key = clientKey(req);
        if (opts.limiter && opts.limiter.blockedMs(key) > 0) {
          return { status: 429, message: 'too many wrong tokens' };
        }
        opts.limiter?.fail(key);
        return { status: 401, message: 'unauthorized' };
      }
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

/**
 * POST /login from the dashboard's login screen: form field `token` in the
 * body (never in a URL). A right token answers 204 with the login cookie;
 * a wrong one 401; too many wrong ones 429 with Retry-After. Cross-origin
 * posts are refused. Without a configured token there is nothing to log
 * into (404).
 */
export async function handleLogin(req: IncomingMessage, res: ServerResponse, opts: UiAccessOptions): Promise<void> {
  const send = (status: number, message: string, headers: Record<string, string> = {}) => {
    res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', ...headers });
    res.end(JSON.stringify({ error: { message } }));
  };
  if (!opts.authToken) return send(404, 'no login on this dashboard');
  if (!sameOrigin(req)) return send(403, 'cross-origin request refused');
  const key = clientKey(req);
  // Wrong tokens only: 429 while the address is blocked, else 401 and a strike.
  const refuse = (status: number, message: string) => {
    const wait = opts.limiter?.blockedMs(key) ?? 0;
    if (wait > 0) {
      const secs = Math.ceil(wait / 1000);
      return send(429, `too many wrong tokens; try again in ${secs} s`, { 'Retry-After': String(secs) });
    }
    opts.limiter?.fail(key);
    return send(status, message);
  };
  let body = '';
  try {
    body = await readSmallBody(req, 4096);
  } catch {
    return refuse(400, 'bad login form');
  }
  const token = new URLSearchParams(body).get('token') ?? '';
  // The right token is never throttled (see checkUiAccess)
  if (!tokenMatches(token, opts.authToken)) return refuse(401, 'invalid token');
  const secure = (req.socket as TLSSocket | undefined)?.encrypted === true || req.headers['x-forwarded-proto'] === 'https';
  const cookie = `${opts.cookieName ?? sessionCookieName(0)}=${sessionValue(opts.authToken)}; Path=/; HttpOnly; SameSite=Strict${secure ? '; Secure' : ''}`;
  res.writeHead(204, { 'Set-Cookie': cookie, 'Cache-Control': 'no-store' });
  res.end();
}

function readSmallBody(req: IncomingMessage, max: number): Promise<string> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    req.on('data', (c: Buffer) => {
      size += c.length;
      if (size > max) {
        req.destroy();
        reject(new Error('body too large'));
        return;
      }
      chunks.push(c);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf-8')));
    req.on('error', reject);
  });
}
