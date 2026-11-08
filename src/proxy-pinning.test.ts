/**
 * With a user proxy (CLI --proxy, dashboard --trusted-proxy) the proxy must
 * get the IP address that passed the SSRF check, never the host name: a
 * proxy that resolves the name again can be sent into its own network by
 * DNS that answers differently the second time (rebinding).
 *
 * Covers the crawler's undici dispatcher (proxy-dispatcher.ts) and the
 * guarded proxy in front of Chromium (guard-proxy.ts), against fake HTTP,
 * SOCKS5 and SOCKS4 upstreams that record what they were asked to dial.
 */
import { describe, it, expect, beforeAll, afterAll, beforeEach } from 'vitest';
import { createServer, request as httpRequest, type IncomingMessage, type Server } from 'node:http';
import { createServer as createHttpsServer, type Server as HttpsServer } from 'node:https';
import { createServer as createNetServer, connect as netConnect, isIP, type AddressInfo, type Server as NetServer, type Socket } from 'node:net';
import type { TLSSocket } from 'node:tls';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { LookupAddress } from 'node:dns';
import { request } from 'undici';
import { setDnsLookupForTests } from './net-guard.js';
import { pinnedProxyDispatcher } from './proxy-dispatcher.js';
import { startGuardedProxy } from './guard-proxy.js';
import { fetchPage } from './fetcher.js';

/** Public-looking address (TEST-NET-3) that the fake upstreams route to 127.0.0.1. */
const PUBLIC_IP = '93.184.215.14';
/** Address inside the "proxy's network" a rebinding answer points at. */
const INTERNAL_IP = '10.0.0.5';

/** Fake DNS: name -> answers per lookup (the last one repeats). */
let dns: Record<string, string[]> = {};
const lookups: Record<string, number> = {};

function answer(name: string): string | null {
  const answers = dns[name];
  if (!answers) return null;
  const n = lookups[name] = (lookups[name] ?? 0) + 1;
  return answers[Math.min(n, answers.length) - 1];
}

/** Every connection an upstream made to the internal network. */
const internalHits: string[] = [];
let internalPort = 0;

/**
 * Where a fake upstream proxy really connects for a target it was given.
 * A host name is resolved again (as a real proxy would), with the same fake
 * DNS, so a rebinding answer reaches the "internal" server.
 */
function route(host: string, port: number): { host: string; port: number } {
  const address = isIP(host) ? host : answer(host);
  if (address === PUBLIC_IP) return { host: '127.0.0.1', port };
  internalHits.push(`${host}:${port}`);
  return { host: '127.0.0.1', port: internalPort };
}

function listen(server: Server | HttpsServer | NetServer): Promise<number> {
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve((server.address() as AddressInfo).port)));
}

function close(server: Server | HttpsServer | NetServer): Promise<void> {
  (server as Server).closeAllConnections?.();
  return new Promise((resolve) => server.close(() => resolve()));
}

/** Requests the plain-http site got: "host path". */
const siteHits: string[] = [];
/** Requests the https site got: "sni host path". */
const tlsHits: string[] = [];

let site: Server;
let sitePort = 0;
let internal: Server;
let tlsSite: HttpsServer | null = null;
let tlsPort = 0;
let testCa = '';
let certDir = '';

const opensslAvailable = (() => {
  try {
    execFileSync('openssl', ['version'], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
})();

beforeAll(async () => {
  setDnsLookupForTests((hostname, _opts, cb) => {
    const address = answer(hostname);
    if (!address) return cb(Object.assign(new Error(`getaddrinfo ENOTFOUND ${hostname}`), { code: 'ENOTFOUND' }), []);
    const list: LookupAddress[] = [{ address, family: isIP(address) }];
    cb(null, list);
  });
  site = createServer((req, res) => {
    siteHits.push(`${req.headers.host} ${req.url}`);
    res.writeHead(200, { 'Content-Type': 'text/html' });
    res.end('<p>public page</p>');
  });
  sitePort = await listen(site);
  internal = createServer((_req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/html' });
    res.end('<p>internal secret</p>');
  });
  internalPort = await listen(internal);

  if (opensslAvailable) {
    // Self-signed certificate for secure.test, made per run (no key in git)
    certDir = mkdtempSync(join(tmpdir(), 'micelio-pin-'));
    execFileSync('openssl', [
      'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '2',
      '-keyout', join(certDir, 'key.pem'), '-out', join(certDir, 'cert.pem'),
      '-subj', '/CN=secure.test', '-addext', 'subjectAltName=DNS:secure.test',
      '-addext', 'basicConstraints=critical,CA:TRUE',
    ], { stdio: 'ignore' });
    testCa = readFileSync(join(certDir, 'cert.pem'), 'utf8');
    tlsSite = createHttpsServer({ key: readFileSync(join(certDir, 'key.pem')), cert: testCa }, (req, res) => {
      tlsHits.push(`${(req.socket as TLSSocket).servername} ${req.headers.host} ${req.url}`);
      res.writeHead(200, { 'Content-Type': 'text/html' });
      res.end('<p>secure page</p>');
    });
    tlsPort = await listen(tlsSite);
  }
});

afterAll(async () => {
  setDnsLookupForTests(null);
  await close(site);
  await close(internal);
  if (tlsSite) await close(tlsSite);
  if (certDir) rmSync(certDir, { recursive: true, force: true });
});

beforeEach(() => {
  dns = {
    'site.test': [PUBLIC_IP],
    'secure.test': [PUBLIC_IP],
    // Public for our check, internal for anyone who asks again
    'rebind.test': [PUBLIC_IP, INTERNAL_IP],
    'v6.test': ['2606:4700::1'],
  };
  for (const k of Object.keys(lookups)) delete lookups[k];
  siteHits.length = 0;
  tlsHits.length = 0;
  internalHits.length = 0;
});

// --- fake upstream proxies ------------------------------------------------

interface HttpUpstream {
  server: Server;
  url: string;
  /** "CONNECT authority" or "METHOD request-target host=<Host>". */
  seen: string[];
  auth: (string | undefined)[];
}

/** A naive HTTP proxy: resolves names itself and forwards anything. */
async function startHttpUpstream(opts: { refuseIpLiterals?: boolean; user?: string } = {}): Promise<HttpUpstream> {
  const seen: string[] = [];
  const auth: (string | undefined)[] = [];
  const server = createServer((req: IncomingMessage, res) => {
    seen.push(`${req.method} ${req.url} host=${req.headers.host}`);
    auth.push(req.headers['proxy-authorization']);
    const u = new URL(req.url || '');
    const to = route(u.hostname.replace(/^\[|\]$/g, ''), Number(u.port || 80));
    const headers = { ...req.headers };
    delete headers['proxy-authorization'];
    const out = httpRequest({ host: to.host, port: to.port, method: req.method, path: u.pathname + u.search, headers }, (r) => {
      res.writeHead(r.statusCode ?? 502, r.headers);
      r.pipe(res);
    });
    out.on('error', () => res.destroy());
    req.pipe(out);
  });
  server.on('connect', (req: IncomingMessage, sock: Socket, head: Buffer) => {
    seen.push(`CONNECT ${req.url}`);
    auth.push(req.headers['proxy-authorization']);
    sock.on('error', () => sock.destroy());
    const u = new URL(`http://${req.url}`);
    const host = u.hostname.replace(/^\[|\]$/g, '');
    if (opts.refuseIpLiterals && isIP(host)) {
      sock.end('HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n');
      return;
    }
    const to = route(host, Number(u.port));
    const t = netConnect(to.port, to.host, () => {
      sock.write('HTTP/1.1 200 Connection Established\r\n\r\n');
      if (head.length) t.write(head);
      t.pipe(sock);
      sock.pipe(t);
    });
    t.on('error', () => sock.destroy());
  });
  const port = await listen(server);
  const cred = opts.user ? `${opts.user}@` : '';
  return { server, url: `http://${cred}127.0.0.1:${port}`, seen, auth };
}

interface SocksRequest {
  /** SOCKS5 address type (1 IPv4, 3 name, 4 IPv6) or 'socks4'. */
  atyp: number | 'socks4';
  host: string;
  port: number;
}

interface SocksUpstream {
  server: NetServer;
  url: string;
  requests: SocksRequest[];
  credentials: string[];
}

/** Read exactly n bytes from a socket. */
function readBytes(sock: Socket, n: number): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const tryRead = () => {
      const b = sock.read(n) as Buffer | null;
      if (b) {
        sock.removeListener('readable', tryRead);
        sock.removeListener('error', reject);
        resolve(b);
      }
    };
    sock.on('readable', tryRead);
    sock.once('error', reject);
    tryRead();
  });
}

/** Read up to and including a NUL byte. */
async function readCString(sock: Socket): Promise<string> {
  const bytes: number[] = [];
  for (;;) {
    const [b] = await readBytes(sock, 1);
    if (b === 0) return Buffer.from(bytes).toString();
    bytes.push(b);
  }
}

function formatIPv6(b: Buffer): string {
  const words: string[] = [];
  for (let i = 0; i < 16; i += 2) words.push(b.readUInt16BE(i).toString(16));
  return words.join(':');
}

/** Splice a SOCKS client to where route() sends the target. */
function splice(sock: Socket, target: SocksRequest, ok: Buffer): void {
  const to = route(target.host, target.port);
  const t = netConnect(to.port, to.host, () => {
    sock.write(ok);
    t.pipe(sock);
    sock.pipe(t);
  });
  t.on('error', () => sock.destroy());
}

/** A SOCKS5 proxy with username/password auth (RFC 1928/1929). */
async function startSocks5Upstream(user: string, pass: string): Promise<SocksUpstream> {
  const requests: SocksRequest[] = [];
  const credentials: string[] = [];
  const server = createNetServer((sock) => {
    sock.on('error', () => sock.destroy());
    (async () => {
      const [, nMethods] = await readBytes(sock, 2);
      const methods = [...await readBytes(sock, nMethods)];
      if (!methods.includes(2)) {
        sock.end(Buffer.from([5, 0xff]));
        return;
      }
      sock.write(Buffer.from([5, 2]));
      const [, ulen] = await readBytes(sock, 2);
      const u = (await readBytes(sock, ulen)).toString();
      const [plen] = await readBytes(sock, 1);
      const p = (await readBytes(sock, plen)).toString();
      credentials.push(`${u}:${p}`);
      if (u !== user || p !== pass) {
        sock.end(Buffer.from([1, 1]));
        return;
      }
      sock.write(Buffer.from([1, 0]));
      const [, , , atyp] = await readBytes(sock, 4);
      let host: string;
      if (atyp === 1) host = [...await readBytes(sock, 4)].join('.');
      else if (atyp === 4) host = formatIPv6(await readBytes(sock, 16));
      else host = (await readBytes(sock, (await readBytes(sock, 1))[0])).toString();
      const port = (await readBytes(sock, 2)).readUInt16BE(0);
      const req: SocksRequest = { atyp, host, port };
      requests.push(req);
      if (atyp === 4) {
        sock.end(Buffer.from([5, 4, 0, 1, 0, 0, 0, 0, 0, 0])); // host unreachable
        return;
      }
      splice(sock, req, Buffer.from([5, 0, 0, 1, 0, 0, 0, 0, 0, 0]));
    })().catch(() => sock.destroy());
  });
  const port = await listen(server);
  return { server, url: `socks5://${user}:${pass}@127.0.0.1:${port}`, requests, credentials };
}

/** A SOCKS4/4a proxy (resolves the name itself for 4a requests). */
async function startSocks4Upstream(): Promise<SocksUpstream> {
  const requests: SocksRequest[] = [];
  const credentials: string[] = [];
  const server = createNetServer((sock) => {
    sock.on('error', () => sock.destroy());
    (async () => {
      const head = await readBytes(sock, 8);
      const port = head.readUInt16BE(2);
      let host = [...head.subarray(4, 8)].join('.');
      credentials.push(await readCString(sock));
      if (host.startsWith('0.0.0.') && host !== '0.0.0.0') host = await readCString(sock); // SOCKS4a
      const req: SocksRequest = { atyp: 'socks4', host, port };
      requests.push(req);
      splice(sock, req, Buffer.from([0, 0x5a, 0, 0, 0, 0, 0, 0]));
    })().catch(() => sock.destroy());
  });
  const port = await listen(server);
  return { server, url: `socks4://127.0.0.1:${port}`, requests, credentials };
}

/** GET an absolute URL through an HTTP proxy (Chromium-style). */
function getViaProxy(proxyPort: number, url: string): Promise<{ status: number; body: string }> {
  return new Promise((resolve, reject) => {
    const u = new URL(url);
    const req = httpRequest({ host: '127.0.0.1', port: proxyPort, path: url, headers: { host: u.host } }, (res) => {
      let body = '';
      res.on('data', (c) => { body += c; });
      res.on('end', () => resolve({ status: res.statusCode ?? 0, body }));
    });
    req.on('error', reject);
    req.end();
  });
}

/** CONNECT through a proxy, then a plain GET inside the tunnel. */
function connectViaProxy(proxyPort: number, authority: string): Promise<{ status: number; body: string }> {
  return new Promise((resolve, reject) => {
    const req = httpRequest({ host: '127.0.0.1', port: proxyPort, method: 'CONNECT', path: authority });
    req.on('connect', (res, socket) => {
      if (res.statusCode !== 200) {
        socket.destroy();
        resolve({ status: res.statusCode ?? 0, body: '' });
        return;
      }
      let data = '';
      socket.on('data', (c) => { data += c; });
      socket.on('end', () => resolve({ status: 200, body: data }));
      socket.write(`GET /tunnel HTTP/1.1\r\nHost: ${authority}\r\nConnection: close\r\n\r\n`);
    });
    req.on('error', reject);
    req.end();
  });
}

// --- crawler dispatcher (fetchPage / fetchHead) ------------------------------

describe('crawler proxy dispatcher pins the checked IP', () => {
  it('HTTP proxy, http target: absolute URI with the IP, Host header and auth kept', async () => {
    const up = await startHttpUpstream({ user: 'user:p%40ss' });
    const dispatcher = pinnedProxyDispatcher(up.url);
    try {
      const page = await fetchPage(`http://site.test:${sitePort}/page?q=1`, { userAgent: 't', dispatcher });
      expect(page.statusCode).toBe(200);
      expect(page.html).toContain('public page');
      expect(page.finalUrl).toBe(`http://site.test:${sitePort}/page?q=1`);
      expect(up.seen).toEqual([`GET http://${PUBLIC_IP}:${sitePort}/page?q=1 host=site.test:${sitePort}`]);
      expect(up.auth).toEqual([`Basic ${Buffer.from('user:p@ss').toString('base64')}`]);
      expect(siteHits).toEqual([`site.test:${sitePort} /page?q=1`]);
      expect(lookups['site.test']).toBe(1);
    } finally {
      await dispatcher.close();
      await close(up.server);
    }
  });

  it('a DNS answer that flips after the check cannot change the dialled address', async () => {
    const up = await startHttpUpstream();
    const dispatcher = pinnedProxyDispatcher(up.url);
    try {
      const page = await fetchPage(`http://rebind.test:${sitePort}/x`, { userAgent: 't', dispatcher });
      expect(page.statusCode).toBe(200);
      expect(page.html).not.toContain('internal secret');
      expect(up.seen).toEqual([`GET http://${PUBLIC_IP}:${sitePort}/x host=rebind.test:${sitePort}`]);
      expect(internalHits).toEqual([]);
      expect(lookups['rebind.test']).toBe(1);
    } finally {
      await dispatcher.close();
      await close(up.server);
    }
  });

  it('refuses a private target before the proxy is contacted', async () => {
    const up = await startHttpUpstream();
    const dispatcher = pinnedProxyDispatcher(up.url);
    try {
      dns['evil.test'] = ['127.0.0.1'];
      const page = await fetchPage(`http://evil.test:${sitePort}/`, { userAgent: 't', dispatcher });
      expect(page.error).toMatch(/Blocked/);
      const literal = await request(`http://${INTERNAL_IP}:${sitePort}/`, { dispatcher }).then(() => 'ok', (e: Error) => e.message);
      expect(literal).toMatch(/Blocked/);
      expect(up.seen).toEqual([]);
    } finally {
      await dispatcher.close();
      await close(up.server);
    }
  });

  it('fails closed with a clear error when the proxy refuses IP-address targets', async () => {
    const up = await startHttpUpstream({ refuseIpLiterals: true });
    const dispatcher = pinnedProxyDispatcher(up.url);
    try {
      const err = await request(`https://secure.test:${tlsPort || 443}/`, { dispatcher }).then(() => null, (e: Error) => e);
      expect(err?.message).toMatch(new RegExp(`refused CONNECT to ${PUBLIC_IP.replace(/\./g, '\\.')}:\\d+ \\(403\\).*IP-address targets`));
      expect(up.seen).toEqual([`CONNECT ${PUBLIC_IP}:${tlsPort || 443}`]);
      expect(internalHits).toEqual([]);
    } finally {
      await dispatcher.close();
      await close(up.server);
    }
  });

  describe.skipIf(!opensslAvailable)('https targets (TLS through the tunnel)', () => {
    it('HTTP proxy: CONNECT to the IP, SNI and Host stay the original host name', async () => {
      const up = await startHttpUpstream();
      const dispatcher = pinnedProxyDispatcher(up.url, { tls: { ca: testCa } });
      try {
        const page = await fetchPage(`https://secure.test:${tlsPort}/a`, { userAgent: 't', dispatcher });
        expect(page.error).toBeUndefined();
        expect(page.statusCode).toBe(200);
        expect(page.html).toContain('secure page');
        expect(up.seen).toEqual([`CONNECT ${PUBLIC_IP}:${tlsPort}`]);
        expect(tlsHits).toEqual([`secure.test secure.test:${tlsPort} /a`]);
      } finally {
        await dispatcher.close();
        await close(up.server);
      }
    });

    it('rebinding name over https: CONNECT gets the first (checked) answer only', async () => {
      dns['secure.test'] = [PUBLIC_IP, INTERNAL_IP];
      const up = await startHttpUpstream();
      const dispatcher = pinnedProxyDispatcher(up.url, { tls: { ca: testCa } });
      try {
        const page = await fetchPage(`https://secure.test:${tlsPort}/b`, { userAgent: 't', dispatcher });
        expect(page.statusCode).toBe(200);
        expect(up.seen).toEqual([`CONNECT ${PUBLIC_IP}:${tlsPort}`]);
        expect(internalHits).toEqual([]);
        expect(lookups['secure.test']).toBe(1);
      } finally {
        await dispatcher.close();
        await close(up.server);
      }
    });

    it('still verifies the certificate against the original host name', async () => {
      const up = await startHttpUpstream();
      const dispatcher = pinnedProxyDispatcher(up.url); // no test CA: self-signed must fail
      try {
        const err = await request(`https://secure.test:${tlsPort}/`, { dispatcher }).then(() => null, (e: Error) => e);
        expect(err).not.toBeNull();
        expect(tlsHits).toEqual([]);
      } finally {
        await dispatcher.close();
        await close(up.server);
      }
    });

    it('SOCKS5 with auth: IPv4 address type, SNI is the original host name', async () => {
      const up = await startSocks5Upstream('crawler', 's3cret');
      const dispatcher = pinnedProxyDispatcher(up.url, { tls: { ca: testCa } });
      try {
        const page = await fetchPage(`https://secure.test:${tlsPort}/s5`, { userAgent: 't', dispatcher });
        expect(page.statusCode).toBe(200);
        expect(up.credentials).toEqual(['crawler:s3cret']);
        expect(up.requests).toEqual([{ atyp: 1, host: PUBLIC_IP, port: tlsPort }]);
        expect(tlsHits).toEqual([`secure.test secure.test:${tlsPort} /s5`]);
      } finally {
        await dispatcher.close();
        await close(up.server);
      }
    });
  });

  it('SOCKS5 with auth, http target: IPv4 address type, Host header kept', async () => {
    const up = await startSocks5Upstream('crawler', 's3cret');
    const dispatcher = pinnedProxyDispatcher(up.url);
    try {
      const page = await fetchPage(`http://rebind.test:${sitePort}/s5`, { userAgent: 't', dispatcher });
      expect(page.statusCode).toBe(200);
      expect(up.requests).toEqual([{ atyp: 1, host: PUBLIC_IP, port: sitePort }]);
      expect(siteHits).toEqual([`rebind.test:${sitePort} /s5`]);
      expect(internalHits).toEqual([]);
    } finally {
      await dispatcher.close();
      await close(up.server);
    }
  });
});

// --- guarded proxy in front of Chromium --------------------------------------

describe('guarded proxy (Chromium) pins the checked IP for its upstream', () => {
  it('SOCKS5 with auth: CONNECT and plain HTTP send the IP, never the name', async () => {
    const up = await startSocks5Upstream('crawler', 's3cret');
    const front = await startGuardedProxy({ upstream: up.url });
    try {
      const t = await connectViaProxy(front.port, `rebind.test:${sitePort}`);
      expect(t.status).toBe(200);
      expect(t.body).toContain('public page');
      const r = await getViaProxy(front.port, `http://site.test:${sitePort}/plain`);
      expect(r).toEqual({ status: 200, body: '<p>public page</p>' });
      expect(up.requests).toEqual([
        { atyp: 1, host: PUBLIC_IP, port: sitePort },
        { atyp: 1, host: PUBLIC_IP, port: sitePort },
      ]);
      expect(up.credentials).toEqual(['crawler:s3cret', 'crawler:s3cret']);
      expect(siteHits).toEqual([`rebind.test:${sitePort} /tunnel`, `site.test:${sitePort} /plain`]);
      expect(internalHits).toEqual([]);
    } finally {
      await front.close();
      await close(up.server);
    }
  });

  it('SOCKS5: an IPv6 answer goes out as address type 4', async () => {
    const up = await startSocks5Upstream('crawler', 's3cret');
    const front = await startGuardedProxy({ upstream: up.url });
    try {
      const t = await connectViaProxy(front.port, `v6.test:${sitePort}`);
      expect(t.status).toBe(502); // the fake upstream cannot reach it
      expect(up.requests).toEqual([{ atyp: 4, host: '2606:4700:0:0:0:0:0:1', port: sitePort }]);
    } finally {
      await front.close();
      await close(up.server);
    }
  });

  it('SOCKS4: plain SOCKS4 with the IPv4 address (no SOCKS4a name), IPv6-only targets refused', async () => {
    const up = await startSocks4Upstream();
    const front = await startGuardedProxy({ upstream: up.url });
    try {
      const t = await connectViaProxy(front.port, `rebind.test:${sitePort}`);
      expect(t.status).toBe(200);
      expect(up.requests).toEqual([{ atyp: 'socks4', host: PUBLIC_IP, port: sitePort }]);
      expect(internalHits).toEqual([]);

      const v6 = await connectViaProxy(front.port, `v6.test:${sitePort}`);
      expect(v6.status).toBe(403);
      expect(up.requests).toHaveLength(1);
    } finally {
      await front.close();
      await close(up.server);
    }
  });

  it('HTTP upstream: a DNS flip after the check cannot reach the internal network', async () => {
    const up = await startHttpUpstream();
    const front = await startGuardedProxy({ upstream: up.url });
    try {
      const r = await getViaProxy(front.port, `http://rebind.test:${sitePort}/p`);
      expect(r.status).toBe(200);
      expect(r.body).not.toContain('internal secret');
      // Our next lookup gets the internal answer: the guard refuses it here
      expect((await connectViaProxy(front.port, `rebind.test:${sitePort}`)).status).toBe(403);
      lookups['rebind.test'] = 0; // fresh name: public first, internal after
      const t = await connectViaProxy(front.port, `rebind.test:${sitePort}`);
      expect(t.status).toBe(200);
      expect(up.seen).toEqual([
        `GET http://${PUBLIC_IP}:${sitePort}/p host=rebind.test:${sitePort}`,
        `CONNECT ${PUBLIC_IP}:${sitePort}`,
      ]);
      expect(siteHits).toEqual([`rebind.test:${sitePort} /p`, `rebind.test:${sitePort} /tunnel`]);
      expect(internalHits).toEqual([]);
    } finally {
      await front.close();
      await close(up.server);
    }
  });

  it('fails closed when the HTTP upstream refuses IP-address CONNECT targets', async () => {
    const up = await startHttpUpstream({ refuseIpLiterals: true });
    const front = await startGuardedProxy({ upstream: up.url });
    try {
      const t = await connectViaProxy(front.port, `site.test:${sitePort}`);
      expect(t.status).toBe(502);
      expect(up.seen).toEqual([`CONNECT ${PUBLIC_IP}:${sitePort}`]);
      expect(siteHits).toEqual([]);
    } finally {
      await front.close();
      await close(up.server);
    }
  });
});
