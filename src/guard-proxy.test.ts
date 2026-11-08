import { describe, it, expect, beforeAll, afterAll, afterEach } from 'vitest';
import { createServer, request as httpRequest, type Server } from 'node:http';
import { connect as netConnect, type AddressInfo, type Socket } from 'node:net';
import type { LookupAddress } from 'node:dns';
import { setAllowPrivateNetworks, setDnsLookupForTests } from './net-guard.js';
import { startGuardedProxy, type GuardedProxy } from './guard-proxy.js';
import { closeBrowser, getBrowser, renderPage, setGuardedProxyOptionsForTests } from './browser.js';

/** A public-looking address (TEST-NET-3) that tests route to the local server. */
const PUBLIC_IP = '93.184.215.14';

let server: Server;
let serverPort = 0;
/** Requests the local server received, as "host path". */
const hits: string[] = [];

/** Fake DNS: name -> answers per lookup (the last answer repeats). */
let dns: Record<string, string[]> = {};
const lookups: Record<string, number> = {};

function stubDns(): void {
  setDnsLookupForTests((hostname, _opts, cb) => {
    const answers = dns[hostname];
    if (!answers) {
      const err = Object.assign(new Error(`getaddrinfo ENOTFOUND ${hostname}`), { code: 'ENOTFOUND' });
      return cb(err, []);
    }
    const n = lookups[hostname] = (lookups[hostname] ?? 0) + 1;
    const address = answers[Math.min(n, answers.length) - 1];
    const list: LookupAddress[] = [{ address, family: address.includes(':') ? 6 : 4 }];
    cb(null, list);
  });
}

/** Dial the checked address; PUBLIC_IP goes to the local server. */
function connectAddress(address: string, port: number): Socket {
  return address === PUBLIC_IP ? netConnect({ host: '127.0.0.1', port: serverPort }) : netConnect({ host: address, port });
}

const PAGE = () => `<!doctype html><html><head><title>static</title></head><body>
<div id="root"></div>
<img src="http://public.test:${serverPort}/ok.png">
<img src="http://evil.test:${serverPort}/evil.png">
<img src="http://rebind.test:${serverPort}/rebind.png">
<img src="http://127.0.0.1:${serverPort}/direct.png">
<img src="http://localhost:${serverPort}/localhost.png">
<script>
  document.title = 'rendered';
  document.getElementById('root').textContent = 'rendered-by-js';
  fetch('http://evil.test:${serverPort}/evil-fetch').catch(() => {});
</script>
</body></html>`;

beforeAll(async () => {
  server = createServer((req, res) => {
    hits.push(`${req.headers.host?.replace(/:\d+$/, '')} ${req.url}`);
    if (req.url === '/') {
      res.writeHead(200, { 'Content-Type': 'text/html' });
      res.end(PAGE());
    } else if (req.url?.endsWith('.png')) {
      res.writeHead(200, { 'Content-Type': 'image/png' });
      res.end();
    } else {
      res.writeHead(200, { 'Content-Type': 'text/plain' });
      res.end('internal secret');
    }
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  serverPort = (server.address() as AddressInfo).port;
});

afterAll(async () => {
  setDnsLookupForTests(null);
  await new Promise((r) => server.close(r));
});

afterEach(() => {
  hits.length = 0;
  for (const k of Object.keys(lookups)) delete lookups[k];
  setAllowPrivateNetworks(false);
});

/** GET an absolute URL through an HTTP proxy. */
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

/** CONNECT through the proxy, then send a plain GET inside the tunnel. */
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

describe('guarded proxy', () => {
  let proxy: GuardedProxy;
  beforeAll(async () => {
    stubDns();
    proxy = await startGuardedProxy({ connectAddress });
  });
  afterAll(() => proxy.close());

  it('listens on loopback only', () => {
    expect(proxy.url).toBe(`http://127.0.0.1:${proxy.port}`);
  });

  it('refuses a host name that resolves to 127.0.0.1 (HTTP and CONNECT)', async () => {
    dns = { 'evil.test': ['127.0.0.1'] };
    const r = await getViaProxy(proxy.port, `http://evil.test:${serverPort}/secret`);
    expect(r.status).toBe(403);
    expect(r.body).toMatch(/Blocked/);
    expect((await connectViaProxy(proxy.port, `evil.test:${serverPort}`)).status).toBe(403);
    expect(hits).toEqual([]);
  });

  it('refuses loopback literals and localhost', async () => {
    for (const host of ['127.0.0.1', 'localhost', '[::1]', '169.254.169.254', '2130706433']) {
      expect((await getViaProxy(proxy.port, `http://${host}:${serverPort}/x`)).status, host).toBe(403);
      expect((await connectViaProxy(proxy.port, `${host}:${serverPort}`)).status, host).toBe(403);
    }
    expect(hits).toEqual([]);
  });

  it('dials the address it checked, so a rebinding name cannot switch to loopback', async () => {
    // A first lookup elsewhere sees a public address; the proxy's own lookup
    // gets loopback and refuses.
    dns = { 'rebind.test': [PUBLIC_IP, '127.0.0.1'] };
    lookups['rebind.test'] = 1; // the first (public) answer was already given out
    expect((await getViaProxy(proxy.port, `http://rebind.test:${serverPort}/x`)).status).toBe(403);
    expect(hits).toEqual([]);
  });

  it('allows a public-looking target, over HTTP and CONNECT', async () => {
    dns = { 'public.test': [PUBLIC_IP] };
    const r = await getViaProxy(proxy.port, `http://public.test:${serverPort}/data`);
    expect(r).toEqual({ status: 200, body: 'internal secret' });
    const t = await connectViaProxy(proxy.port, `public.test:${serverPort}`);
    expect(t.status).toBe(200);
    expect(t.body).toMatch(/^HTTP\/1\.1 200/);
    expect(hits).toEqual(['public.test /data', 'public.test /tunnel']);
  });

  it('reaches private hosts only when the CLI opted in (--allow-private)', async () => {
    setAllowPrivateNetworks(true);
    expect((await getViaProxy(proxy.port, `http://127.0.0.1:${serverPort}/x`)).status).toBe(200);
  });

  it('chains to a trusted upstream proxy with the checked IP, never the host name', async () => {
    // A naive upstream that forwards anything to the local server, so only
    // the front proxy's check stands between a target and the server
    const seen: string[] = [];
    const auth: (string | undefined)[] = [];
    const upstream = createServer((req, res) => {
      seen.push(`GET ${req.url}`);
      auth.push(req.headers['proxy-authorization']);
      const out = httpRequest({ host: '127.0.0.1', port: serverPort, path: new URL(req.url || '').pathname, headers: req.headers }, (r) => {
        res.writeHead(r.statusCode ?? 502, r.headers);
        r.pipe(res);
      });
      req.pipe(out);
    });
    upstream.on('connect', (req, sock, head) => {
      seen.push(`CONNECT ${req.url}`);
      auth.push(req.headers['proxy-authorization']);
      const t = netConnect(serverPort, '127.0.0.1', () => {
        sock.write('HTTP/1.1 200 Connection Established\r\n\r\n');
        if (head.length) t.write(head);
        t.pipe(sock);
        sock.pipe(t);
      });
    });
    await new Promise<void>((r) => upstream.listen(0, '127.0.0.1', r));
    const upPort = (upstream.address() as AddressInfo).port;
    const front = await startGuardedProxy({ upstream: `http://user:p%40ss@127.0.0.1:${upPort}` });
    try {
      dns = { 'evil.test': ['127.0.0.1'], 'public.test': [PUBLIC_IP] };
      expect((await getViaProxy(front.port, `http://evil.test:${serverPort}/x`)).status).toBe(403);
      expect((await connectViaProxy(front.port, `evil.test:${serverPort}`)).status).toBe(403);
      expect((await getViaProxy(front.port, `http://127.0.0.1:${serverPort}/x`)).status).toBe(403);
      expect(seen).toEqual([]);
      expect(hits).toEqual([]);
      expect((await getViaProxy(front.port, `http://public.test:${serverPort}/via`)).status).toBe(200);
      expect((await connectViaProxy(front.port, `public.test:${serverPort}`)).status).toBe(200);
      expect(seen).toEqual([`GET http://${PUBLIC_IP}:${serverPort}/via`, `CONNECT ${PUBLIC_IP}:${serverPort}`]);
      const basic = `Basic ${Buffer.from('user:p@ss').toString('base64')}`;
      expect(auth).toEqual([basic, basic]);
      expect(hits).toEqual(['public.test /via', 'public.test /tunnel']);
    } finally {
      await front.close();
      upstream.closeAllConnections();
      await new Promise((r) => upstream.close(r));
    }
  });
});

// Real Chromium, when Playwright has one installed
const chromiumAvailable = await (async () => {
  try {
    const { chromium } = await import('playwright');
    const b = await chromium.launch({ headless: true });
    await b.close();
    return true;
  } catch {
    return false;
  }
})();

describe.skipIf(!chromiumAvailable)('JS rendering through the guarded proxy (Chromium)', () => {
  beforeAll(() => {
    stubDns();
    setGuardedProxyOptionsForTests({ connectAddress });
  });
  afterAll(async () => {
    await closeBrowser();
    setGuardedProxyOptionsForTests({});
  });

  it('renders a public page and never loads a subresource from a host that resolves to loopback', async () => {
    dns = { 'public.test': [PUBLIC_IP], 'evil.test': ['127.0.0.1'], 'rebind.test': [PUBLIC_IP, '127.0.0.1'] };
    const { html, statusCode } = await renderPage(`http://public.test:${serverPort}/`, 'MicelioTest/1.0');
    expect(statusCode).toBe(200);
    expect(html).toContain('rendered-by-js');
    expect(html).toContain('<title>rendered</title>');
    expect(hits).toContain('public.test /');
    expect(hits).toContain('public.test /ok.png');
    // evil.test, the rebinding name (public for the route check, loopback
    // for the proxy), 127.0.0.1 and localhost never reach the server
    expect(hits.filter((h) => !h.startsWith('public.test '))).toEqual([]);
    expect(lookups['rebind.test']).toBeGreaterThanOrEqual(2);
  }, 60_000);

  it('blocks loopback at the connection even without route interception', async () => {
    dns = { 'public.test': [PUBLIC_IP], 'evil.test': ['127.0.0.1'], 'rebind.test': ['127.0.0.1'] };
    const b = await getBrowser();
    const context = await b.newContext();
    try {
      const page = await context.newPage();
      const res = await page.goto(`http://public.test:${serverPort}/`, { waitUntil: 'networkidle', timeout: 30_000 });
      expect(res?.status()).toBe(200);
      expect(await page.content()).toContain('rendered-by-js');
      const evil = await page.goto(`http://evil.test:${serverPort}/secret`).then((r) => r?.status() ?? 0, () => -1);
      expect(evil).not.toBe(200);
      expect(await page.content()).not.toContain('internal secret');
      // Loopback literals are proxied too (--proxy-bypass-list=<-loopback>)
      const direct = await page.goto(`http://127.0.0.1:${serverPort}/secret`).then((r) => r?.status() ?? 0, () => -1);
      expect(direct).not.toBe(200);
    } finally {
      await context.close();
    }
    expect(hits.filter((h) => !h.startsWith('public.test '))).toEqual([]);
  }, 60_000);
});
