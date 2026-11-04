import { describe, it, expect, afterEach, beforeAll, afterAll } from 'vitest';
import { createServer, request as httpRequest, type IncomingHttpHeaders, type Server } from 'node:http';
import { connect as netConnect, type AddressInfo } from 'node:net';
import { ProxyAgent, request } from 'undici';
import {
  isBlockedIp, urlBlockReason, guardedLookup, guardedDispatcher, assertPublicUrl,
  setAllowPrivateNetworks, stripCredentialsOnCrossOrigin, isCredentialHeader, sameSiteHost,
  setDnsLookupForTests,
} from './net-guard.js';
import { fetchPage, fetchHead, checkExternalUrl, headResourceSize } from './fetcher.js';

afterEach(() => setAllowPrivateNetworks(false));

describe('isBlockedIp', () => {
  it.each([
    '0.0.0.0', '10.1.2.3', '100.64.0.1', '100.127.255.255', '127.0.0.1', '127.255.0.9',
    '169.254.169.254', '169.254.0.1', '172.16.0.1', '172.31.255.255', '192.168.1.1',
    '224.0.0.1', '255.255.255.255',
    '::', '::1', 'fc00::1', 'fd12:3456::1', 'fe80::1', 'febf::1', 'ff02::1',
    '::ffff:127.0.0.1', '::ffff:7f00:1', '::ffff:169.254.169.254', '::ffff:10.0.0.1',
    '64:ff9b::a9fe:a9fe', '::127.0.0.1',
  ])('blocks %s', (ip) => {
    expect(isBlockedIp(ip)).toBe(true);
  });

  it.each(['8.8.8.8', '1.1.1.1', '172.32.0.1', '100.128.0.1', '2606:4700::1111', '::ffff:8.8.8.8'])(
    'allows %s', (ip) => {
      expect(isBlockedIp(ip)).toBe(false);
    },
  );

  it('fails closed on garbage', () => {
    expect(isBlockedIp('not-an-ip')).toBe(true);
  });
});

describe('urlBlockReason', () => {
  it('blocks private literals, including decimal, octal and hex forms', () => {
    for (const u of [
      'http://127.0.0.1/', 'http://localhost:3100/', 'http://[::1]/', 'http://2130706433/',
      'http://0177.0.0.1/', 'http://0x7f000001/', 'http://169.254.169.254/latest/meta-data',
      'http://[::ffff:127.0.0.1]/', 'http://100.100.100.100/',
    ]) {
      expect(urlBlockReason(u), u).not.toBeNull();
    }
  });

  it('blocks non-http schemes and invalid URLs', () => {
    expect(urlBlockReason('file:///etc/passwd')).toMatch(/scheme/);
    expect(urlBlockReason('gopher://example.com/')).toMatch(/scheme/);
    expect(urlBlockReason('nope')).toMatch(/invalid/);
  });

  it('allows public URLs, and private ones only when opted in', () => {
    expect(urlBlockReason('https://example.com/')).toBeNull();
    setAllowPrivateNetworks(true);
    expect(urlBlockReason('http://127.0.0.1/')).toBeNull();
    expect(urlBlockReason('file:///etc/passwd')).not.toBeNull();
  });
});

describe('guardedLookup / guardedDispatcher', () => {
  it('refuses a host name that resolves to loopback', async () => {
    const err = await new Promise<NodeJS.ErrnoException | null>((resolve) => {
      guardedLookup('localhost', { all: true }, (e) => resolve(e));
    });
    expect(err?.code).toBe('EBLOCKED');
    await expect(assertPublicUrl('http://localhost/')).rejects.toThrow(/Blocked/);
  });

  it('refuses the connection itself, after DNS, even without a prior URL check', async () => {
    await expect(request('http://localhost:9/', { dispatcher: guardedDispatcher() })).rejects.toThrow(/Blocked/);
    await expect(request('http://127.0.0.1:9/', { dispatcher: guardedDispatcher() })).rejects.toThrow(/Blocked/);
  });
});

describe('stripCredentialsOnCrossOrigin', () => {
  const headers = {
    'User-Agent': 'Googlebot', 'Accept-Language': 'es', 'X-Tenant': 'acme',
    Cookie: 'c=1', Authorization: 'Basic t', 'Proxy-Authorization': 'p', 'X-Api-Key': 'k',
    'X-Auth-User': 'u', 'X-CSRF-Token': 't', 'X-Client-Secret': 's', 'X-Session-Id': 'i', 'X-Api-Version': '2',
  };
  const stripped = { 'User-Agent': 'Googlebot', 'Accept-Language': 'es', 'X-Tenant': 'acme' };
  const strip = (from: string, to: string, cred?: string) => stripCredentialsOnCrossOrigin(headers, from, to, cred);

  it('keeps credentials on the same host', () => {
    expect(strip('https://acme.com/x', 'https://acme.com/y')).toEqual(headers);
  });

  it('keeps credentials on an http -> https upgrade of the same host', () => {
    expect(strip('http://staging.acme.com/', 'https://staging.acme.com/')).toEqual(headers);
  });

  it('keeps credentials on apex <-> www moves, also with an upgrade', () => {
    expect(strip('https://acme.com/', 'https://www.acme.com/')).toEqual(headers);
    expect(strip('https://www.acme.com/', 'https://acme.com/')).toEqual(headers);
    expect(strip('http://acme.com/', 'https://www.acme.com/')).toEqual(headers);
  });

  it('keeps credentials on a subdomain of the credential host', () => {
    expect(strip('https://acme.com/', 'https://shop.acme.com/')).toEqual(headers);
    // Later hop: checked against the first request URL, not the previous hop
    expect(strip('https://shop.acme.com/', 'https://acme.com/', 'https://acme.com/')).toEqual(headers);
    expect(strip('https://shop.acme.com/', 'https://evil.com/', 'https://acme.com/')).toEqual(stripped);
  });

  it('drops credentials on an https -> http downgrade', () => {
    expect(strip('https://acme.com/', 'http://acme.com/')).toEqual(stripped);
  });

  it('drops credentials on a port change', () => {
    expect(strip('https://acme.com/', 'https://acme.com:8443/')).toEqual(stripped);
  });

  it('drops credentials on other hosts, including look-alikes', () => {
    for (const to of ['https://evil.com/', 'https://wwwacme.com/', 'https://acme.com.evil.com/',
      'https://notacme.com/', 'https://www.www.acme.com.evil.com/']) {
      expect(strip('https://acme.com/', to)).toEqual(stripped);
    }
    // A parent domain is not a subdomain of the credential host
    expect(strip('https://shop.acme.com/', 'https://acme.com/')).toEqual(stripped);
  });
});

describe('isCredentialHeader / sameSiteHost', () => {
  it('flags secret-looking names only', () => {
    for (const h of ['Authorization', 'cookie', 'Proxy-Authorization', 'X-Api-Key', 'x-api-version', 'X-Auth-Token', 'Api-Key', 'X-Secret', 'X-Session']) {
      expect(isCredentialHeader(h)).toBe(true);
    }
    for (const h of ['User-Agent', 'Accept', 'Accept-Language', 'X-Tenant', 'Referer']) {
      expect(isCredentialHeader(h)).toBe(false);
    }
  });

  it('matches same site hosts', () => {
    expect(sameSiteHost('acme.com', 'ACME.com.')).toBe(true);
    expect(sameSiteHost('acme.com', 'a.b.acme.com')).toBe(true);
    expect(sameSiteHost('acme.com', 'wwwacme.com')).toBe(false);
    expect(sameSiteHost('acme.com', 'acme.com.evil.com')).toBe(false);
  });
});

describe('fetcher SSRF and credential handling (local servers)', () => {
  let target: Server;
  let origin: Server;
  let targetUrl = '';
  let originUrl = '';
  const seen: IncomingHttpHeaders[] = [];
  let hits = 0;

  beforeAll(async () => {
    target = createServer((req, res) => {
      hits++;
      seen.push(req.headers);
      res.writeHead(200, { 'Content-Type': 'text/html', 'Content-Length': '13' });
      res.end('<p>secret</p>');
    });
    origin = createServer((req, res) => {
      hits++;
      if (req.url === '/cross') {
        res.writeHead(302, { Location: `${targetUrl}/landing` });
      } else if (req.url === '/same') {
        res.writeHead(302, { Location: '/final' });
      } else {
        seen.push(req.headers);
        res.writeHead(200, { 'Content-Type': 'text/html' });
      }
      res.end();
    });
    await new Promise<void>((r) => target.listen(0, '127.0.0.1', r));
    await new Promise<void>((r) => origin.listen(0, '127.0.0.1', r));
    targetUrl = `http://127.0.0.1:${(target.address() as AddressInfo).port}`;
    originUrl = `http://127.0.0.1:${(origin.address() as AddressInfo).port}`;
  });

  afterAll(() => {
    target.close();
    origin.close();
  });

  it('blocks a private seed before any request', async () => {
    hits = 0;
    const page = await fetchPage(`${targetUrl}/`, { userAgent: 't' });
    expect(page.error).toMatch(/Blocked/);
    expect(page.html).toBe('');
    const head = await fetchHead(`${targetUrl}/`, { userAgent: 't' });
    expect(head.error).toMatch(/Blocked/);
    expect((await checkExternalUrl(`${targetUrl}/`, 't')).error).toMatch(/Blocked/);
    expect(await headResourceSize(`${targetUrl}/`, 't')).toBeNull();
    expect(hits).toBe(0);
  });

  it('crawls private hosts when the CLI opts in', async () => {
    setAllowPrivateNetworks(true);
    const page = await fetchPage(`${targetUrl}/`, { userAgent: 't' });
    expect(page.statusCode).toBe(200);
    expect(page.html).toContain('secret');
  });

  it('drops cookies and credential headers on a cross-origin redirect, keeps overrides', async () => {
    setAllowPrivateNetworks(true);
    seen.length = 0;
    const opts = {
      userAgent: 't', cookies: 'sid=1',
      customHeaders: { Authorization: 'Bearer t', 'X-Api-Key': 'k', 'X-Tenant': 'acme', 'User-Agent': 'Googlebot' },
    };
    const page = await fetchPage(`${originUrl}/cross`, opts);
    expect(page.statusCode).toBe(200);
    expect(seen).toHaveLength(1);
    expect(seen[0].cookie).toBeUndefined();
    expect(seen[0].authorization).toBeUndefined();
    expect(seen[0]['x-api-key']).toBeUndefined();
    expect(seen[0]['x-tenant']).toBe('acme');
    expect(seen[0]['user-agent']).toBe('Googlebot');

    seen.length = 0;
    await fetchHead(`${originUrl}/cross`, opts);
    expect(seen[0].cookie).toBeUndefined();
    expect(seen[0].authorization).toBeUndefined();
  });

  it('keeps credentials on a same-origin redirect, and only for allowed hosts', async () => {
    setAllowPrivateNetworks(true);
    seen.length = 0;
    const opts = { userAgent: 't', cookies: 'sid=1', customHeaders: { 'X-Tenant': 'acme' } };
    await fetchPage(`${originUrl}/same`, opts);
    expect(seen[0].cookie).toBe('sid=1');
    expect(seen[0]['x-tenant']).toBe('acme');

    seen.length = 0;
    await fetchPage(`${originUrl}/same`, { ...opts, credentialHosts: new Set(['example.com']) });
    expect(seen[0].cookie).toBeUndefined();
    expect(seen[0]['x-tenant']).toBeUndefined();
  });
});

describe('fetcher through a user proxy (CLI --proxy / trusted proxy)', () => {
  // A naive HTTP proxy that forwards everything to one local server: only
  // the fetcher's own target check stands between a target and the server
  let site: Server;
  let proxy: Server;
  let proxyUrl = '';
  const proxied: string[] = [];

  beforeAll(async () => {
    site = createServer((req, res) => {
      if (req.url === '/hop') {
        res.writeHead(302, { Location: 'http://evil.test/secret' });
        res.end();
        return;
      }
      res.writeHead(200, { 'Content-Type': 'text/html' });
      res.end('<p>page</p>');
    });
    await new Promise<void>((r) => site.listen(0, '127.0.0.1', r));
    const sitePort = (site.address() as AddressInfo).port;
    proxy = createServer((req, res) => {
      proxied.push(req.url || '');
      const out = httpRequest({ host: '127.0.0.1', port: sitePort, path: new URL(req.url || '').pathname, method: req.method }, (r) => {
        res.writeHead(r.statusCode ?? 502, r.headers);
        r.pipe(res);
      });
      req.pipe(out);
    });
    // undici's ProxyAgent tunnels plain http too
    proxy.on('connect', (req, sock, head) => {
      proxied.push(`CONNECT ${req.url}`);
      const t = netConnect(sitePort, '127.0.0.1', () => {
        sock.write('HTTP/1.1 200 Connection Established\r\n\r\n');
        if (head.length) t.write(head);
        t.pipe(sock);
        sock.pipe(t);
      });
      sock.on('error', () => t.destroy());
    });
    await new Promise<void>((r) => proxy.listen(0, '127.0.0.1', r));
    proxyUrl = `http://127.0.0.1:${(proxy.address() as AddressInfo).port}`;
    setDnsLookupForTests((hostname, _opts, cb) => {
      const ip = ({ 'evil.test': '127.0.0.1', 'public.test': '203.0.113.7' } as Record<string, string>)[hostname];
      if (!ip) return cb(Object.assign(new Error('ENOTFOUND'), { code: 'ENOTFOUND' }), []);
      cb(null, [{ address: ip, family: 4 }]);
    });
  });

  afterAll(() => {
    setDnsLookupForTests(null);
    site.close();
    proxy.close();
  });

  it('resolves and checks each target before handing it to the proxy', async () => {
    const dispatcher = new ProxyAgent(proxyUrl);
    try {
      proxied.length = 0;
      const page = await fetchPage('http://evil.test/secret', { userAgent: 't', dispatcher });
      expect(page.error).toMatch(/Blocked/);
      const head = await fetchHead('http://evil.test/secret', { userAgent: 't', dispatcher });
      expect(head.error).toMatch(/Blocked/);
      expect(proxied).toEqual([]);

      const ok = await fetchPage('http://public.test/', { userAgent: 't', dispatcher });
      expect(ok.statusCode).toBe(200);
      expect(ok.html).toContain('page');
      expect(proxied).toEqual(['CONNECT public.test:80']);

      // A redirect hop to a name that resolves to loopback is refused too
      proxied.length = 0;
      const hop = await fetchPage('http://public.test/hop', { userAgent: 't', dispatcher });
      expect(hop.error).toMatch(/Blocked/);
      expect(proxied).toEqual(['CONNECT public.test:80']);
    } finally {
      await dispatcher.close();
    }
  });
});
