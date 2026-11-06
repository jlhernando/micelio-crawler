import { describe, it, expect, afterEach, beforeAll, afterAll } from 'vitest';
import { createServer, request as httpRequest, type IncomingHttpHeaders, type Server } from 'node:http';
import { connect as netConnect, type AddressInfo } from 'node:net';
import { request } from 'undici';
import { pinnedProxyDispatcher } from './proxy-dispatcher.js';
import {
  isBlockedIp, urlBlockReason, guardedLookup, guardedDispatcher, assertPublicUrl,
  setAllowPrivateNetworks, stripCredentialsOnCrossOrigin, isCredentialHeader, credentialHostAllowed,
  credentialHostSet, setDnsLookupForTests,
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

  // IPv6 transition and site-local ranges (mirrors Go internal/netguard)
  it.each([
    'fec0::1', 'feff:ffff::1', // deprecated site-local fec0::/10
    '64:ff9b:1::1', '64:ff9b:1:ffff::8.8.8.8', // local-use NAT64 64:ff9b:1::/48, even with a public tail
    '64:ff9b::127.0.0.1', '64:ff9b::10.0.0.1', // NAT64 well-known prefix, private IPv4
    '2002:7f00:1::', '2002:a00:1::1', '2002:a9fe:a9fe::', '2002:c0a8:101::', // 6to4 of 127/8, 10/8, 169.254/16, 192.168/16
    '2001:0:4136:e378:8000:63bf:80ff:fffe', // Teredo, client 127.0.0.1 (inverted 80ff:fffe)
    '2001::f5ff:fffe', // Teredo, client 10.0.0.1
  ])('blocks transition/site-local %s', (ip) => {
    expect(isBlockedIp(ip)).toBe(true);
  });

  it.each([
    '64:ff9b::8.8.8.8', '2002:808:808::1', // NAT64 / 6to4 of 8.8.8.8
    '2001:0:4136:e378:8000:63bf:f7f7:f7f7', // Teredo, client 8.8.8.8
    '2001:db8::1', '2001:4860:4860::8888', // 2001::/32 is only Teredo, not all of 2001::/16
  ])('allows public transition address %s', (ip) => {
    expect(isBlockedIp(ip)).toBe(false);
  });

  it('blocks transition literals in URLs too', () => {
    for (const u of ['http://[fec0::1]/', 'http://[2002:7f00:1::]/', 'http://[64:ff9b:1::1]/', 'http://[2001::f5ff:fffe]/']) {
      expect(urlBlockReason(u), u).not.toBeNull();
    }
  });

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

  it('refuses a DNS answer in a site-local or transition range that hides a private address', async () => {
    try {
      for (const address of ['fec0::1', '2002:7f00:1::', '64:ff9b:1::1', '2001::f5ff:fffe']) {
        setDnsLookupForTests((_h, _o, cb) => cb(null, [{ address, family: 6 }]));
        await expect(assertPublicUrl('http://sneaky.test/'), address).rejects.toThrow(/Blocked/);
      }
    } finally {
      setDnsLookupForTests(null);
    }
  });

  it('refuses the connection itself, after DNS, even without a prior URL check', async () => {
    await expect(request('http://localhost:9/', { dispatcher: guardedDispatcher() })).rejects.toThrow(/Blocked/);
    await expect(request('http://127.0.0.1:9/', { dispatcher: guardedDispatcher() })).rejects.toThrow(/Blocked/);
  });
});

describe('stripCredentialsOnCrossOrigin', () => {
  const headers = {
    'User-Agent': 'Googlebot', 'Accept-Language': 'es', Accept: '*/*', 'X-Tenant': 'acme',
    Cookie: 'c=1', Authorization: 'Basic t', 'Proxy-Authorization': 'p', 'X-Api-Key': 'k',
    'X-Signature': 's', 'X-Credential': 'c', 'X-License': 'l', 'X-Api-Version': '2',
  };
  // Only non-secret headers survive; every other user header is a credential
  const stripped = { 'User-Agent': 'Googlebot', 'Accept-Language': 'es', Accept: '*/*' };
  const strip = (from: string, to: string, cred?: string, hosts?: ReadonlySet<string>) =>
    stripCredentialsOnCrossOrigin(headers, from, to, cred, hosts);

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

  it('drops credentials on a subdomain of the credential host', () => {
    expect(strip('https://acme.com/', 'https://shop.acme.com/')).toEqual(stripped);
    expect(strip('https://acme.com/', 'https://attacker.acme.com/')).toEqual(stripped);
    expect(strip('https://www.acme.com/', 'https://a.b.acme.com/')).toEqual(stripped);
    // Later hop: checked against the first request URL, not the previous hop
    expect(strip('https://shop.acme.com/', 'https://acme.com/', 'https://acme.com/')).toEqual(headers);
    expect(strip('https://shop.acme.com/', 'https://evil.com/', 'https://acme.com/')).toEqual(stripped);
  });

  it('keeps credentials only for the named hosts (and their www twin) when given', () => {
    const hosts = credentialHostSet(['acme.com', 'Partner.example.']);
    expect(strip('https://acme.com/', 'https://partner.example/', undefined, hosts)).toEqual(headers);
    expect(strip('https://acme.com/', 'https://www.partner.example/', undefined, hosts)).toEqual(headers);
    expect(strip('https://acme.com/', 'https://shop.acme.com/', undefined, hosts)).toEqual(stripped);
    expect(strip('https://acme.com/', 'https://evil.com/', undefined, hosts)).toEqual(stripped);
  });

  it('drops credentials on an https -> http downgrade', () => {
    expect(strip('https://acme.com/', 'http://acme.com/')).toEqual(stripped);
    expect(strip('https://acme.com/', 'http://www.acme.com/')).toEqual(stripped);
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

describe('isCredentialHeader / credentialHostAllowed', () => {
  it('treats every header but a short non-secret list as a credential', () => {
    for (const h of ['Authorization', 'cookie', 'Proxy-Authorization', 'X-Api-Key', 'X-Signature',
      'X-Credential', 'X-License', 'X-Tenant', 'Referer', 'X-Whatever']) {
      expect(isCredentialHeader(h)).toBe(true);
    }
    for (const h of ['User-Agent', 'user-agent', 'Accept', 'Accept-Language', 'Accept-Encoding', 'Cache-Control']) {
      expect(isCredentialHeader(h)).toBe(false);
    }
  });

  it('allows named hosts and their www/apex twin only', () => {
    const hosts = credentialHostSet(['acme.com', 'www.shop.example']);
    expect(credentialHostAllowed('ACME.com.', hosts)).toBe(true);
    expect(credentialHostAllowed('www.acme.com', hosts)).toBe(true);
    expect(credentialHostAllowed('shop.example', hosts)).toBe(true);
    expect(credentialHostAllowed('a.b.acme.com', hosts)).toBe(false);
    expect(credentialHostAllowed('wwwacme.com', hosts)).toBe(false);
    expect(credentialHostAllowed('acme.com.evil.com', hosts)).toBe(false);
    expect(credentialHostAllowed('', hosts)).toBe(false);
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

  it('drops cookies and every custom header on a cross-origin redirect, keeps the User-Agent override', async () => {
    setAllowPrivateNetworks(true);
    seen.length = 0;
    const opts = {
      userAgent: 't', cookies: 'sid=1',
      customHeaders: {
        Authorization: 'Bearer t', 'X-Api-Key': 'k', 'X-Tenant': 'acme', 'X-Signature': 'sig',
        'X-License': 'lic', 'User-Agent': 'Googlebot',
      },
    };
    const page = await fetchPage(`${originUrl}/cross`, opts);
    expect(page.statusCode).toBe(200);
    expect(seen).toHaveLength(1);
    expect(seen[0].cookie).toBeUndefined();
    expect(seen[0].authorization).toBeUndefined();
    expect(seen[0]['x-api-key']).toBeUndefined();
    expect(seen[0]['x-signature']).toBeUndefined();
    expect(seen[0]['x-license']).toBeUndefined();
    expect(seen[0]['x-tenant']).toBeUndefined();
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

describe('fetcher credentials across hosts of one site (fake DNS)', () => {
  // One server answers every name; fake DNS points acme.test and its
  // subdomains at it, so the redirect keeps the port and only the host moves.
  let server: Server;
  let port = 0;
  const seen: { host: string; headers: IncomingHttpHeaders }[] = [];

  beforeAll(async () => {
    server = createServer((req, res) => {
      const host = (req.headers.host || '').split(':')[0];
      seen.push({ host, headers: req.headers });
      const to = new URL(req.url || '/', 'http://x').searchParams.get('to');
      if (to) {
        res.writeHead(302, { Location: `http://${to}:${port}/landing` });
      } else {
        res.writeHead(200, { 'Content-Type': 'text/html' });
      }
      res.end();
    });
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    port = (server.address() as AddressInfo).port;
    setDnsLookupForTests((hostname, _opts, cb) => {
      if (hostname === 'acme.test' || hostname.endsWith('.acme.test') || hostname === 'other.test') {
        return cb(null, [{ address: '127.0.0.1', family: 4 }]);
      }
      cb(Object.assign(new Error('ENOTFOUND'), { code: 'ENOTFOUND' }), []);
    });
  });

  afterAll(() => {
    setDnsLookupForTests(null);
    server.close();
  });

  const opts = (credentialHosts?: ReadonlySet<string>) => ({
    userAgent: 't', cookies: 'sid=1', credentialHosts,
    customHeaders: { Authorization: 'Bearer t', 'X-Signature': 'sig' },
  });
  const landing = () => seen[seen.length - 1];

  it('does not forward credentials to a subdomain (tenant / takeover) of the credential host', async () => {
    setAllowPrivateNetworks(true);
    for (const hosts of [undefined, credentialHostSet(['acme.test'])]) {
      seen.length = 0;
      const page = await fetchPage(`http://acme.test:${port}/?to=attacker.acme.test`, opts(hosts));
      expect(page.statusCode).toBe(200);
      expect(seen[0].headers.cookie).toBe('sid=1');
      expect(landing().host).toBe('attacker.acme.test');
      expect(landing().headers.cookie).toBeUndefined();
      expect(landing().headers.authorization).toBeUndefined();
      expect(landing().headers['x-signature']).toBeUndefined();

      seen.length = 0;
      await fetchHead(`http://acme.test:${port}/?to=attacker.acme.test`, opts(hosts));
      expect(landing().headers.cookie).toBeUndefined();
      expect(landing().headers['x-signature']).toBeUndefined();
    }
  });

  it('keeps credentials on an apex -> www move and to another host the user named', async () => {
    setAllowPrivateNetworks(true);
    seen.length = 0;
    await fetchPage(`http://acme.test:${port}/?to=www.acme.test`, opts(credentialHostSet(['acme.test'])));
    expect(landing().host).toBe('www.acme.test');
    expect(landing().headers.cookie).toBe('sid=1');
    expect(landing().headers['x-signature']).toBe('sig');

    seen.length = 0;
    await fetchPage(`http://acme.test:${port}/?to=other.test`, opts(credentialHostSet(['acme.test', 'other.test'])));
    expect(landing().headers.cookie).toBe('sid=1');

    seen.length = 0;
    await fetchPage(`http://acme.test:${port}/?to=other.test`, opts(credentialHostSet(['acme.test'])));
    expect(landing().headers.cookie).toBeUndefined();
  });

  it('never sends credentials to a first-hop host outside the named ones, www twin aside', async () => {
    setAllowPrivateNetworks(true);
    seen.length = 0;
    await fetchPage(`http://shop.acme.test:${port}/`, opts(credentialHostSet(['acme.test'])));
    expect(seen[0].headers.cookie).toBeUndefined();
    seen.length = 0;
    await fetchPage(`http://www.acme.test:${port}/`, opts(credentialHostSet(['acme.test'])));
    expect(seen[0].headers.cookie).toBe('sid=1');
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
      proxied.push(`${req.method} ${req.url}`);
      const out = httpRequest({ host: '127.0.0.1', port: sitePort, path: new URL(req.url || '').pathname, method: req.method }, (r) => {
        res.writeHead(r.statusCode ?? 502, r.headers);
        r.pipe(res);
      });
      req.pipe(out);
    });
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

  it('resolves and checks each target, then hands the proxy only the checked IP', async () => {
    const dispatcher = pinnedProxyDispatcher(proxyUrl);
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
      expect(proxied).toEqual(['GET http://203.0.113.7:80/']);

      // A redirect hop to a name that resolves to loopback is refused too
      proxied.length = 0;
      const hop = await fetchPage('http://public.test/hop', { userAgent: 't', dispatcher });
      expect(hop.error).toMatch(/Blocked/);
      expect(proxied).toEqual(['GET http://203.0.113.7:80/hop']);
    } finally {
      await dispatcher.close();
    }
  });
});
