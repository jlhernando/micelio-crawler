/**
 * SSRF protection shared by every request made to a crawled URL (pages,
 * HEAD checks, robots.txt, sitemaps, the headless browser).
 *
 * Mirrors the Go crawler (internal/netguard): this computer, private and
 * link-local networks (cloud metadata included), CGNAT, unspecified,
 * multicast and broadcast addresses are never reached. Host names are
 * checked after DNS resolution and the connection is pinned to the checked
 * address, so DNS rebinding cannot swap the address between check and dial.
 *
 * The CLI can opt out with --allow-private (crawl an intranet on purpose);
 * the dashboard API never does.
 */
import { lookup as dnsLookup, type LookupAddress, type LookupOptions } from 'node:dns';
import { isIP } from 'node:net';
import { Agent, buildConnector, type Dispatcher } from 'undici';

let allowPrivate = false;

/** Allow requests to private addresses (CLI --allow-private only). */
export function setAllowPrivateNetworks(allow: boolean): void {
  allowPrivate = allow;
}

export function privateNetworksAllowed(): boolean {
  return allowPrivate;
}

export class BlockedAddressError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'BlockedAddressError';
  }
}

function parseIPv4(ip: string): number[] | null {
  const parts = ip.split('.');
  if (parts.length !== 4) return null;
  const out = parts.map((p) => (/^\d{1,3}$/.test(p) ? Number(p) : NaN));
  return out.every((n) => n >= 0 && n <= 255) ? out : null;
}

/** Expand an IPv6 address to 8 hextets (handles :: and a dotted-quad tail). */
function parseIPv6(ip: string): number[] | null {
  let addr = ip.replace(/^\[|\]$/g, '').toLowerCase();
  const zone = addr.indexOf('%');
  if (zone >= 0) addr = addr.slice(0, zone);
  // Turn a dotted-quad tail (::ffff:127.0.0.1) into two hextets.
  const lastColon = addr.lastIndexOf(':');
  const last = addr.slice(lastColon + 1);
  if (last.includes('.')) {
    const v4 = parseIPv4(last);
    if (!v4) return null;
    addr = addr.slice(0, lastColon + 1) + ((v4[0] << 8) | v4[1]).toString(16) + ':' + ((v4[2] << 8) | v4[3]).toString(16);
  }
  const halves = addr.split('::');
  if (halves.length > 2) return null;
  const toHextets = (s: string) => (s === '' ? [] : s.split(':').map((h) => (/^[0-9a-f]{1,4}$/.test(h) ? parseInt(h, 16) : NaN)));
  const head = toHextets(halves[0]);
  let words = head;
  if (halves.length === 2) {
    const rest = toHextets(halves[1]);
    const fill = 8 - head.length - rest.length;
    if (fill < 1) return null;
    words = [...head, ...new Array(fill).fill(0), ...rest];
  }
  if (words.length !== 8 || words.some((w) => Number.isNaN(w))) return null;
  return words;
}

function blockedIPv4(o: number[]): boolean {
  const [a, b] = o;
  return (
    a === 0 || // "this network"
    a === 10 || // RFC 1918
    a === 127 || // loopback
    (a === 100 && b >= 64 && b <= 127) || // CGNAT / Tailscale
    (a === 169 && b === 254) || // link-local, cloud metadata
    (a === 172 && b >= 16 && b <= 31) || // RFC 1918
    (a === 192 && b === 168) || // RFC 1918
    a >= 224 // multicast, reserved, broadcast
  );
}

/**
 * Whether an IP literal is an address crawls must never reach. Anything that
 * is not a valid IP literal is reported as blocked (fail closed).
 */
export function isBlockedIp(ip: string): boolean {
  const v4 = parseIPv4(ip);
  if (v4) return blockedIPv4(v4);
  const w = parseIPv6(ip);
  if (!w) return true;
  // IPv4-mapped (::ffff:a.b.c.d), IPv4-compatible (::a.b.c.d) and NAT64
  // (64:ff9b::a.b.c.d) addresses carry an IPv4 address in the last 32 bits.
  const embeddedV4 = [w[6] >> 8, w[6] & 0xff, w[7] >> 8, w[7] & 0xff];
  const zeroPrefix = w.slice(0, 5).every((x) => x === 0);
  if (zeroPrefix && (w[5] === 0xffff || w[5] === 0)) {
    if (w[5] === 0 && w[6] === 0 && w[7] <= 1) return true; // :: and ::1
    return blockedIPv4(embeddedV4);
  }
  if (w[0] === 0x64 && w[1] === 0xff9b && w.slice(2, 6).every((x) => x === 0)) {
    return blockedIPv4(embeddedV4);
  }
  if ((w[0] & 0xfe00) === 0xfc00) return true; // unique local fc00::/7
  if ((w[0] & 0xffc0) === 0xfe80) return true; // link-local fe80::/10
  if ((w[0] & 0xff00) === 0xff00) return true; // multicast ff00::/8
  return false;
}

function bareHost(hostname: string): string {
  return hostname.replace(/^\[|\]$/g, '').toLowerCase().replace(/\.$/, '');
}

/** localhost names and literal private addresses (no DNS lookup). */
export function isPrivateHostname(hostname: string): boolean {
  const host = bareHost(hostname);
  if (host === 'localhost' || host.endsWith('.localhost')) return true;
  return isIP(host) !== 0 && isBlockedIp(host);
}

/**
 * Check a URL without DNS: scheme must be http(s), and the host must not be
 * localhost or a literal private address. The WHATWG URL parser already
 * turns decimal, octal and hex IPv4 forms (http://2130706433/) into dotted
 * quads, so they are caught here too. Returns a reason when blocked.
 */
export function urlBlockReason(rawUrl: string): string | null {
  let parsed: URL;
  try {
    parsed = new URL(rawUrl);
  } catch {
    return 'Blocked: invalid URL';
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    return `Blocked: unsupported scheme ${parsed.protocol}`;
  }
  if (!allowPrivate && isPrivateHostname(parsed.hostname)) return 'Blocked: private/loopback address';
  return null;
}

type LookupCallback = (err: NodeJS.ErrnoException | null, address: string | LookupAddress[], family?: number) => void;

/**
 * dns.lookup replacement for net/tls.connect: resolves every address and
 * refuses the connection when any of them is private. The socket then
 * connects to the address returned here, so the checked address is the one
 * dialled.
 */
export function guardedLookup(hostname: string, options: LookupOptions, callback: LookupCallback): void {
  dnsLookup(hostname, { ...options, all: true }, (err, addresses) => {
    if (err) return callback(err, '');
    const list = addresses as LookupAddress[];
    if (!allowPrivate) {
      const bad = list.find((a) => isBlockedIp(a.address));
      if (bad || list.length === 0) {
        const e = new BlockedAddressError(`Blocked: ${hostname} resolves to private address ${bad?.address ?? '(none)'}`) as NodeJS.ErrnoException;
        e.code = 'EBLOCKED';
        return callback(e, '');
      }
    }
    if (options.all) return callback(null, list);
    callback(null, list[0].address, list[0].family);
  });
}

/** Resolve a URL's host and reject it if any address is private. */
export async function assertPublicUrl(rawUrl: string): Promise<void> {
  const reason = urlBlockReason(rawUrl);
  if (reason) throw new BlockedAddressError(reason);
  if (allowPrivate) return;
  const host = bareHost(new URL(rawUrl).hostname);
  if (isIP(host)) return;
  await new Promise<void>((resolve, reject) => {
    guardedLookup(host, { all: true }, (err) => (err ? reject(err) : resolve()));
  });
}

let guardedAgent: Agent | null = null;

/**
 * Shared undici dispatcher whose connections go through guardedLookup and
 * refuse literal private addresses. Use it for every request to a crawled URL.
 */
export function guardedDispatcher(): Dispatcher {
  if (!guardedAgent) {
    const base = buildConnector({ lookup: guardedLookup } as buildConnector.BuildOptions);
    guardedAgent = new Agent({
      connect: (opts, cb) => {
        const host = bareHost(opts.hostname);
        if (!allowPrivate && isIP(host) && isBlockedIp(host)) {
          cb(new BlockedAddressError(`Blocked: private/loopback address ${host}`), null);
          return;
        }
        base(opts, cb);
      },
    });
  }
  return guardedAgent;
}

/**
 * Header names that carry secrets. User-Agent, Accept-Language and other
 * non-secret overrides do not match, so they survive a cross-site redirect.
 */
const CREDENTIAL_HEADER_RE = /^(authorization|proxy-authorization|cookie)$|auth|token|key|secret|session|passw|^x-api-/i;

export function isCredentialHeader(name: string): boolean {
  return CREDENTIAL_HEADER_RE.test(name);
}

/**
 * True when credentials sent to credentialHost may follow a redirect to
 * toHost: same host, apex <-> www move, or a subdomain of credentialHost.
 */
export function sameSiteHost(credentialHost: string, toHost: string): boolean {
  const c = credentialHost.toLowerCase().replace(/\.$/, '');
  const t = toHost.toLowerCase().replace(/\.$/, '');
  return t === c || t === `www.${c}` || c === `www.${t}` || t.endsWith(`.${c}`);
}

/**
 * Headers for the next redirect hop. Credential headers (Authorization,
 * Cookie, Proxy-Authorization and any header whose name looks like a secret:
 * *auth*, *token*, *key*, *secret*, *session*, x-api-*) are dropped when the
 * redirect leaves the credential host's site (see sameSiteHost), downgrades
 * https -> http, or changes port. An http -> https upgrade keeps them. Once
 * dropped they never come back on a later hop. Other headers, including user
 * overrides like User-Agent, are always kept.
 *
 * credentialUrl is the URL the credentials were first sent to (the request
 * URL before any redirect); it defaults to fromUrl.
 */
export function stripCredentialsOnCrossOrigin(
  headers: Record<string, string>,
  fromUrl: string,
  toUrl: string,
  credentialUrl: string = fromUrl,
): Record<string, string> {
  const from = new URL(fromUrl);
  const to = new URL(toUrl);
  const downgrade = from.protocol === 'https:' && to.protocol !== 'https:';
  if (!downgrade && from.port === to.port &&
      sameSiteHost(bareHost(new URL(credentialUrl).hostname), bareHost(to.hostname))) {
    return headers;
  }
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(headers)) {
    if (!isCredentialHeader(k)) out[k] = v;
  }
  return out;
}
