/**
 * Local guarded forward proxy for headless Chromium (mirrors the Go
 * crawler's netguard.StartProxy, used by internal/browser GuardNetwork).
 *
 * Chromium resolves host names and opens connections on its own, so a check
 * made before one of its requests (Playwright route interception) can be
 * fooled by DNS that answers differently the second time, and a browser
 * proxy resolves names itself. Every Chromium connection goes through this
 * proxy instead (--proxy-server, loopback included): each target is checked
 * and resolved here with guardedLookup, and the checked address is dialled.
 *
 * Serves plain HTTP (absolute-URI requests) and CONNECT tunnels (HTTPS,
 * WebSockets). With a trusted upstream proxy (CLI --proxy), targets are
 * resolved and checked here too, and the upstream gets the checked IP
 * address, never the host name: a CONNECT to <ip>:<port>, a SOCKS5 IPv4/IPv6
 * address, a SOCKS4 IPv4 address, or an absolute URI with the IP plus the
 * original Host header. The upstream never resolves the name, so DNS that
 * answers differently the second time cannot send it into its own network.
 * TLS stays end to end (Chromium <-> origin inside the opaque tunnel), so SNI
 * and certificate checks still use the original host name.
 */
import { createServer, request as httpRequest, type IncomingMessage, type OutgoingHttpHeaders, type ServerResponse } from 'node:http';
import { connect as netConnect, isIP, type AddressInfo, type Socket } from 'node:net';
import { connect as tlsConnect } from 'node:tls';
import type { Duplex } from 'node:stream';
import { bareHost, BlockedAddressError, parseIPv6, resolvePublicAddress } from './net-guard.js';

const CONNECT_TIMEOUT_MS = 30_000;

export interface GuardedProxyOptions {
  /** Trusted proxy to chain to (http, https, socks4, socks5). */
  upstream?: string;
  /**
   * Opens the TCP connection to an address that already passed the guard.
   * Tests replace it to reach a local server; the default is net.connect.
   */
  connectAddress?: (address: string, port: number) => Socket;
}

export interface GuardedProxy {
  port: number;
  /** http://127.0.0.1:<port>, for --proxy-server. */
  url: string;
  close(): Promise<void>;
}

export interface Target {
  host: string; // bare host name or IP literal
  port: number;
}

/** host:port of a CONNECT request, or the host of an absolute URL. */
function parseAuthority(authority: string, defaultPort: number): Target | null {
  try {
    const u = new URL(`http://${authority}`);
    if (!u.hostname || u.username || u.password || u.pathname !== '/') return null;
    return { host: bareHost(u.hostname), port: u.port ? Number(u.port) : defaultPort };
  } catch {
    return null;
  }
}

export function hostForUrl(host: string): string {
  return host.includes(':') ? `[${host}]` : host;
}

/** Resolves once the socket is connected; rejects on error or timeout. */
export function whenConnected(socket: Socket, event: 'connect' | 'secureConnect' = 'connect'): Promise<Socket> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      socket.destroy();
      reject(new Error('connect timeout'));
    }, CONNECT_TIMEOUT_MS);
    socket.once(event, () => {
      clearTimeout(timer);
      socket.removeListener('error', onError);
      resolve(socket);
    });
    const onError = (err: Error) => {
      clearTimeout(timer);
      reject(err);
    };
    socket.once('error', onError);
  });
}

/** Read from a socket until `done` says how many bytes form the reply. */
function readReply(socket: Socket, done: (buf: Buffer) => number | null): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    let buf = Buffer.alloc(0);
    const cleanup = () => {
      clearTimeout(timer);
      socket.removeListener('data', onData);
      socket.removeListener('error', onError);
      socket.removeListener('close', onClose);
    };
    const onData = (chunk: Buffer) => {
      buf = Buffer.concat([buf, chunk]);
      if (buf.length > 64 * 1024) {
        cleanup();
        reject(new Error('upstream proxy reply too large'));
        return;
      }
      let n: number | null;
      try {
        n = done(buf);
      } catch (err) {
        cleanup();
        reject(err);
        return;
      }
      if (n === null) return;
      cleanup();
      if (n < buf.length) socket.unshift(buf.subarray(n));
      resolve(buf.subarray(0, n));
    };
    const onError = (err: Error) => { cleanup(); reject(err); };
    const onClose = () => { cleanup(); reject(new Error('upstream proxy closed the connection')); };
    const timer = setTimeout(() => { cleanup(); reject(new Error('upstream proxy timeout')); }, CONNECT_TIMEOUT_MS);
    socket.on('data', onData);
    socket.once('error', onError);
    socket.once('close', onClose);
  });
}

export function basicAuth(u: URL): string {
  return Buffer.from(`${decodeURIComponent(u.username)}:${decodeURIComponent(u.password)}`).toString('base64');
}

/** Connect to the trusted upstream proxy itself (user-configured address). */
export async function dialUpstream(upstream: URL): Promise<Socket> {
  const host = bareHost(upstream.hostname);
  const defaults: Record<string, number> = { 'http:': 80, 'https:': 443, 'socks4:': 1080, 'socks4a:': 1080, 'socks5:': 1080, 'socks5h:': 1080 };
  const port = upstream.port ? Number(upstream.port) : defaults[upstream.protocol] ?? 1080;
  if (upstream.protocol === 'https:') {
    return whenConnected(tlsConnect({ host, port, servername: host }), 'secureConnect');
  }
  return whenConnected(netConnect({ host, port }));
}

function isSocks4(upstream: URL): boolean {
  return upstream.protocol === 'socks4:' || upstream.protocol === 'socks4a:';
}

/** Network-order bytes of an IP literal (4 for IPv4, 16 for IPv6). */
function ipBytes(ip: string): Buffer {
  if (isIP(ip) === 4) return Buffer.from(ip.split('.').map(Number));
  const words = parseIPv6(ip);
  if (!words) throw new Error(`not an IP address: ${ip}`);
  const out = Buffer.alloc(16);
  words.forEach((w, i) => out.writeUInt16BE(w, i * 2));
  return out;
}

/**
 * A socket tunnelled through the upstream proxy to an IP address that has
 * already passed the guard. The proxy only ever sees that IP (CONNECT
 * authority, SOCKS address), so it has no name to resolve. A proxy that
 * refuses IP targets makes the request fail; it never falls back to a name.
 */
export async function tunnelThroughUpstream(upstream: URL, target: Target): Promise<Socket> {
  const family = isIP(target.host);
  if (!family) throw new Error(`upstream proxy target must be a checked IP address, got ${target.host}`);
  if (family === 6 && isSocks4(upstream)) {
    throw new BlockedAddressError(`SOCKS4 proxy cannot reach IPv6 address ${target.host}; use an HTTP or SOCKS5 proxy`);
  }
  const authority = `${hostForUrl(target.host)}:${target.port}`;
  const socket = await dialUpstream(upstream);
  try {
    switch (upstream.protocol) {
      case 'http:':
      case 'https:': {
        let head = `CONNECT ${authority} HTTP/1.1\r\nHost: ${authority}\r\n`;
        if (upstream.username) head += `Proxy-Authorization: Basic ${basicAuth(upstream)}\r\n`;
        socket.write(head + '\r\n');
        const reply = await readReply(socket, (b) => {
          const i = b.indexOf('\r\n\r\n');
          return i < 0 ? null : i + 4;
        });
        const status = /^HTTP\/1\.[01] (\d{3})/.exec(reply.toString('latin1'));
        if (!status || status[1] !== '200') {
          throw new Error(`upstream proxy refused CONNECT to ${authority} (${status?.[1] ?? 'bad reply'}); the proxy must accept IP-address targets`);
        }
        return socket;
      }
      case 'socks5:':
      case 'socks5h:': {
        const auth = !!upstream.username;
        socket.write(Buffer.from(auth ? [5, 2, 0, 2] : [5, 1, 0]));
        const method = await readReply(socket, (b) => (b.length >= 2 ? 2 : null));
        if (method[0] !== 5) throw new Error('upstream is not a SOCKS5 proxy');
        if (method[1] === 2) {
          const user = Buffer.from(decodeURIComponent(upstream.username));
          const pass = Buffer.from(decodeURIComponent(upstream.password));
          socket.write(Buffer.concat([Buffer.from([1, user.length]), user, Buffer.from([pass.length]), pass]));
          const ok = await readReply(socket, (b) => (b.length >= 2 ? 2 : null));
          if (ok[1] !== 0) throw new Error('SOCKS5 authentication failed');
        } else if (method[1] !== 0) {
          throw new Error('SOCKS5 proxy accepts no supported auth method');
        }
        // Address type 1 (IPv4) or 4 (IPv6), never 3 (domain name)
        const addr = ipBytes(target.host);
        const port = Buffer.from([target.port >> 8, target.port & 0xff]);
        socket.write(Buffer.concat([Buffer.from([5, 1, 0, family === 4 ? 1 : 4]), addr, port]));
        await readReply(socket, (b) => {
          if (b.length < 5) return null;
          if (b[1] !== 0) throw new Error(`SOCKS5 connect to ${authority} failed (code ${b[1]}); the proxy must accept IP-address targets`);
          const len = b[3] === 1 ? 4 : b[3] === 4 ? 16 : b[3] === 3 ? 1 + b[4] : -1;
          if (len < 0) throw new Error('SOCKS5 bad reply');
          return b.length >= 4 + len + 2 ? 4 + len + 2 : null;
        });
        return socket;
      }
      case 'socks4:':
      case 'socks4a:': {
        // Plain SOCKS4 with the checked IPv4 address (never SOCKS4a + name)
        const user = Buffer.from(decodeURIComponent(upstream.username || ''));
        const req = Buffer.concat([
          Buffer.from([4, 1, target.port >> 8, target.port & 0xff]), ipBytes(target.host),
          user, Buffer.from([0]),
        ]);
        socket.write(req);
        const reply = await readReply(socket, (b) => (b.length >= 8 ? 8 : null));
        if (reply[1] !== 0x5a) throw new Error(`SOCKS4 connect to ${authority} failed (code ${reply[1]})`);
        return socket;
      }
      default:
        throw new Error(`unsupported upstream proxy scheme ${upstream.protocol}`);
    }
  } catch (err) {
    socket.destroy();
    throw err;
  }
}

/**
 * Resolve a target with the guard and tunnel to the checked address through
 * the upstream. SOCKS4 asks DNS for IPv4 only, since it cannot carry IPv6.
 */
export async function openUpstreamTunnel(upstream: URL, target: Target): Promise<Socket> {
  const { address } = await resolvePublicAddress(target.host, isSocks4(upstream) ? 4 : undefined);
  return tunnelThroughUpstream(upstream, { host: address, port: target.port });
}

const HOP_BY_HOP = new Set([
  'connection', 'proxy-connection', 'keep-alive', 'proxy-authorization', 'proxy-authenticate', 'te', 'trailer', 'upgrade',
]);

function forwardHeaders(raw: string[]): OutgoingHttpHeaders {
  const out: Record<string, string | string[]> = {};
  for (let i = 0; i + 1 < raw.length; i += 2) {
    const k = raw[i].toLowerCase();
    if (HOP_BY_HOP.has(k)) continue;
    const prev = out[k];
    out[k] = prev === undefined ? raw[i + 1] : ([] as string[]).concat(prev, raw[i + 1]);
  }
  return out;
}

function errorStatus(err: unknown): number {
  return err instanceof BlockedAddressError || (err as NodeJS.ErrnoException)?.code === 'EBLOCKED' ? 403 : 502;
}

/** Start the guarded proxy on 127.0.0.1, on a free port. */
export async function startGuardedProxy(options: GuardedProxyOptions = {}): Promise<GuardedProxy> {
  const upstream = options.upstream ? new URL(options.upstream) : null;
  const connectAddress = options.connectAddress ?? ((address: string, port: number) => netConnect({ host: address, port }));
  const sockets = new Set<Duplex>();
  const track = (s: Duplex) => {
    sockets.add(s);
    s.once('close', () => sockets.delete(s));
  };

  /** A connected socket to target, after the guard. */
  async function openTarget(target: Target): Promise<Socket> {
    // Resolve here and hand the upstream only the checked address
    if (upstream) return openUpstreamTunnel(upstream, target);
    // Direct: resolve here and dial exactly the checked address
    const { address } = await resolvePublicAddress(target.host);
    return whenConnected(connectAddress(address, target.port));
  }

  function fail(res: ServerResponse, err: unknown): void {
    if (res.headersSent) {
      res.destroy();
      return;
    }
    res.writeHead(errorStatus(err), { 'Content-Type': 'text/plain', Connection: 'close' });
    res.end((err as Error)?.message ?? 'proxy error');
  }

  async function handleRequest(req: IncomingMessage, res: ServerResponse): Promise<void> {
    let url: URL;
    try {
      url = new URL(req.url || '');
    } catch {
      res.writeHead(400, { 'Content-Type': 'text/plain' }).end('not a proxy request');
      return;
    }
    if (url.protocol !== 'http:') {
      res.writeHead(400, { 'Content-Type': 'text/plain' }).end('only http:// is proxied without CONNECT');
      return;
    }
    const target = parseAuthority(url.host, 80);
    if (!target) {
      res.writeHead(400, { 'Content-Type': 'text/plain' }).end('bad target');
      return;
    }
    const headers = forwardHeaders(req.rawHeaders);
    headers.connection = 'close';
    let out: ReturnType<typeof httpRequest>;
    try {
      if (upstream && (upstream.protocol === 'http:' || upstream.protocol === 'https:')) {
        // Forward an absolute-URI request with the checked IP to the upstream
        // HTTP proxy; the Host header keeps the original name for the origin
        const { address } = await resolvePublicAddress(target.host);
        const socket = await dialUpstream(upstream);
        track(socket);
        headers.host = url.host;
        if (upstream.username) headers['proxy-authorization'] = `Basic ${basicAuth(upstream)}`;
        const path = `http://${hostForUrl(address)}:${target.port}${url.pathname}${url.search}`;
        out = httpRequest({ method: req.method, path, headers, setHost: false, createConnection: () => socket });
      } else {
        const socket = await openTarget(target);
        track(socket);
        out = httpRequest({ method: req.method, path: url.pathname + url.search, headers, createConnection: () => socket });
      }
    } catch (err) {
      fail(res, err);
      return;
    }
    out.on('response', (upRes) => {
      res.writeHead(upRes.statusCode ?? 502, upRes.statusMessage, forwardHeaders(upRes.rawHeaders));
      upRes.pipe(res);
      upRes.on('error', () => res.destroy());
    });
    out.on('error', (err) => fail(res, err));
    req.pipe(out);
  }

  const server = createServer((req, res) => {
    handleRequest(req, res).catch((err) => fail(res, err));
  });
  server.on('connection', track);

  server.on('connect', (req: IncomingMessage, client: Duplex, head: Buffer) => {
    track(client);
    client.on('error', () => client.destroy());
    const target = parseAuthority(req.url || '', 443);
    if (!target) {
      client.end('HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n');
      return;
    }
    openTarget(target).then((remote) => {
      track(remote);
      remote.on('error', () => client.destroy());
      client.on('close', () => remote.destroy());
      remote.on('close', () => client.destroy());
      client.write('HTTP/1.1 200 Connection Established\r\n\r\n');
      if (head.length) remote.write(head);
      remote.pipe(client);
      client.pipe(remote);
    }, (err) => {
      const status = errorStatus(err) === 403 ? '403 Forbidden' : '502 Bad Gateway';
      client.end(`HTTP/1.1 ${status}\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\n${(err as Error)?.message ?? ''}`);
    });
  });

  // Plain-HTTP WebSocket upgrades are not proxied (Chromium tunnels them with CONNECT)
  server.on('upgrade', (_req, socket: Duplex) => socket.destroy());

  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      server.removeListener('error', reject);
      resolve();
    });
  });
  const port = (server.address() as AddressInfo).port;

  let closed: Promise<void> | null = null;
  return {
    port,
    url: `http://127.0.0.1:${port}`,
    close() {
      if (!closed) {
        closed = new Promise<void>((resolve) => {
          server.close(() => resolve());
          for (const s of sockets) s.destroy();
        });
      }
      return closed;
    },
  };
}
