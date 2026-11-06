import { describe, it, expect, beforeAll, afterAll, afterEach } from 'vitest';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { setAllowPrivateNetworks } from './net-guard.js';
import { fetchPage } from './fetcher.js';
import { RobotsChecker, SITEMAP_LIMITS } from './robots.js';
import { extractPageData, JSONLD_LIMITS } from './extractor.js';
import { NgramAnalyzer, MAX_PAGE_TOKENS } from './ngrams.js';
import { findSimilarPairs, clampThreshold, SIMILARITY_LIMITS } from './embeddings.js';
import { readBodyCapped } from './body-limit.js';

// ── Local hostile server ─────────────────────────────────

let server: Server;
let base = '';
const hits: string[] = [];

const urlset = (n: number, prefix = 'u') =>
  `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${
    Array.from({ length: n }, (_, i) => `<url><loc>${base}/${prefix}${i}</loc></url>`).join('')}</urlset>`;
const index = (locs: string[]) =>
  `<?xml version="1.0"?><sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${
    locs.map((l) => `<sitemap><loc>${base}${l}</loc></sitemap>`).join('')}</sitemapindex>`;

beforeAll(async () => {
  server = createServer((req, res) => {
    const path = req.url || '/';
    hits.push(path);
    if (path === '/endless') {
      // Chunked HTML that never ends
      res.writeHead(200, { 'Content-Type': 'text/html' });
      const chunk = '<p>' + 'x'.repeat(64 * 1024) + '</p>';
      const pump = () => { while (res.write(chunk)) { /* fill */ } };
      res.on('drain', pump);
      pump();
      req.on('close', () => res.destroy());
      return;
    }
    if (path === '/big.html') {
      res.writeHead(200, { 'Content-Type': 'text/html', 'Content-Length': String(3 * 1024 * 1024) });
      res.end('a'.repeat(3 * 1024 * 1024));
      return;
    }
    if (path === '/robots.txt') {
      res.writeHead(200, { 'Content-Type': 'text/plain' });
      res.end('User-agent: *\n' + '# pad\n'.repeat(120_000) + 'Disallow: /secret\n');
      return;
    }
    let m;
    if (path === '/many.xml') return void res.end(urlset(10));
    if (path === '/huge.xml') return void res.end(urlset(200));
    if ((m = path.match(/^\/deep(\d+)\.xml$/))) return void res.end(index([`/deep${Number(m[1]) + 1}.xml`]));
    if (path === '/wide.xml') return void res.end(index(Array.from({ length: 50 }, (_, i) => `/leaf${i}.xml`)));
    if ((m = path.match(/^\/leaf(\d+)\.xml$/))) return void res.end(urlset(1, `leaf${m[1]}-`));
    res.writeHead(404);
    res.end();
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  setAllowPrivateNetworks(true);
});

afterAll(() => {
  setAllowPrivateNetworks(false);
  server.closeAllConnections();
  server.close();
});

// ── fetcher ──────────────────────────────────────────────

describe('fetchPage body cap', () => {
  it('stops reading an endless HTML stream at the cap', async () => {
    const page = await fetchPage(`${base}/endless`, { userAgent: 't', maxBodyBytes: 1024 * 1024 });
    expect(page.statusCode).toBe(200);
    expect(Buffer.byteLength(page.html)).toBeLessThanOrEqual(1024 * 1024);
    expect(page.html.length).toBeGreaterThan(500_000);
  }, 10_000);

  it('truncates a declared-large body', async () => {
    const page = await fetchPage(`${base}/big.html`, { userAgent: 't', maxBodyBytes: 100_000 });
    expect(page.html.length).toBe(100_000);
  });

  it('readBodyCapped reports truncation and keeps short bodies whole', async () => {
    async function* gen() { yield Buffer.from('abc'); yield Buffer.from('def'); }
    expect(await readBodyCapped(gen(), 4)).toEqual({ text: 'abcd', bytes: 4, truncated: true });
    expect(await readBodyCapped(gen(), 6)).toEqual({ text: 'abcdef', bytes: 6, truncated: false });
  });
});

// ── robots.txt and sitemaps ──────────────────────────────

describe('robots.txt / sitemap limits', () => {
  const saved = { ...SITEMAP_LIMITS };
  afterEach(() => Object.assign(SITEMAP_LIMITS, saved));

  const entriesFor = async (urls: string[]) => {
    const rc = new RobotsChecker();
    rc.setSitemapUrls(urls.map((u) => base + u));
    const entries = await rc.getSitemapEntries('t');
    return { entries, errors: rc.getSitemapErrors() };
  };

  it('reads at most 512 KiB of robots.txt', async () => {
    const rc = new RobotsChecker();
    await rc.init(`${base}/`, 't');
    // The Disallow line sits after ~720 KB of comments: past the cap, ignored
    expect(rc.isAllowed(`${base}/secret`)).toBe(true);
  });

  it('keeps at most maxUrlsPerFile entries of one sitemap', async () => {
    SITEMAP_LIMITS.maxUrlsPerFile = 5;
    const { entries, errors } = await entriesFor(['/many.xml']);
    expect(entries).toHaveLength(5);
    expect(errors.join()).toMatch(/50,000 URL limit/);
  });

  it('does not parse a sitemap larger than maxBytesPerFile', async () => {
    SITEMAP_LIMITS.maxBytesPerFile = 2_000;
    const { entries, errors } = await entriesFor(['/huge.xml']);
    expect(entries).toHaveLength(0);
    expect(errors.join()).toMatch(/exceeds 50MB/);
  });

  it('stops following nested sitemap indexes past maxDepth', async () => {
    hits.length = 0;
    const { errors } = await entriesFor(['/deep0.xml']);
    expect(hits.filter((h) => h.startsWith('/deep'))).toHaveLength(SITEMAP_LIMITS.maxDepth + 1);
    expect(errors.join()).toMatch(/nested more than 3 levels/);
  });

  it('fetches at most maxFiles sitemap files and at most maxTotalEntries entries', async () => {
    SITEMAP_LIMITS.maxFiles = 5;
    hits.length = 0;
    const r1 = await entriesFor(['/wide.xml']);
    expect(hits.filter((h) => h.endsWith('.xml'))).toHaveLength(5);
    expect(r1.entries).toHaveLength(4);
    expect(r1.errors.join()).toMatch(/first 5 sitemap files/);

    Object.assign(SITEMAP_LIMITS, saved, { maxTotalEntries: 7 });
    const r2 = await entriesFor(['/wide.xml']);
    expect(r2.entries).toHaveLength(7);
  });
});

// ── JSON-LD ──────────────────────────────────────────────

describe('JSON-LD limits', () => {
  const extract = (body: string) => extractPageData({
    url: 'https://a.test/', finalUrl: 'https://a.test/', statusCode: 200, redirectChain: [], headers: {},
    html: `<html><head>${body}</head><body></body></html>`, contentType: 'text/html', responseTimeMs: 1, transferSize: 1,
  }, 0, 'a.test');
  const ld = (o: unknown) => `<script type="application/ld+json">${typeof o === 'string' ? o : JSON.stringify(o)}</script>`;

  it('does not parse a block over maxParseChars', () => {
    const big = JSON.stringify({ '@type': 'Thing', pad: 'x'.repeat(JSONLD_LIMITS.maxParseChars) });
    const [entry] = extract(ld(big)).structuredData;
    expect(entry.type).toBe('TooLarge');
    expect(entry.raw.length).toBe(JSONLD_LIMITS.maxRawStored);
  });

  it('caps the @type length and the number of blocks', () => {
    const page = extract(ld({ '@type': 'T'.repeat(10_000) }) + ld({ '@type': 'Ok' }).repeat(150));
    const jsonld = page.structuredData.filter((s) => s.format === 'json-ld');
    expect(jsonld).toHaveLength(JSONLD_LIMITS.maxBlocks);
    expect(jsonld[0].type.length).toBe(JSONLD_LIMITS.maxTypeChars);
    expect(jsonld[1].type).toBe('Ok');
  });
});

// ── n-grams ──────────────────────────────────────────────

describe('n-gram memory cap', () => {
  it('never tracks more than 50,000 terms per gram size, even inside one hostile page', () => {
    const ng = new NgramAnalyzer('en');
    let maxSeen = 0;
    for (let p = 0; p < 4; p++) {
      const words = Array.from({ length: 300_000 }, (_, i) => `q${p}z${i.toString(36)}`);
      ng.addPage(words.join(' '));
      maxSeen = Math.max(maxSeen, ng.maxTrackedTerms);
    }
    expect(maxSeen).toBeLessThanOrEqual(50_000);
    expect(ng.getResults().totalTokens).toBeLessThanOrEqual(4 * MAX_PAGE_TOKENS);
  });

  it('still counts normal pages', () => {
    const ng = new NgramAnalyzer('en');
    for (let i = 0; i < 3; i++) ng.addPage('technical seo crawler audit report technical seo crawler audit report extra words here');
    const top = ng.getResults().bigrams.find((b) => b.term === 'technical seo');
    expect(top).toMatchObject({ count: 6, pages: 3 });
  });
});

// ── embeddings ───────────────────────────────────────────

describe('embedding similarity bounds', () => {
  it('clamps the threshold', () => {
    expect(clampThreshold(-1)).toBe(0);
    expect(clampThreshold(2)).toBe(1);
    expect(clampThreshold(Number.NaN)).toBe(0.85);
  });

  it('keeps at most topKPerPage neighbours per page when every pair matches', async () => {
    const n = 400;
    const urls = Array.from({ length: n }, (_, i) => `https://a.test/${i}`);
    const vectors = urls.map(() => [1, 1, 1]);
    const { pairs, compared } = await findSimilarPairs(urls, vectors, -1);
    expect(compared).toBe(n);
    // All n*(n-1)/2 = 79,800 pairs match; only the per-page best are kept
    expect(pairs.length).toBeLessThanOrEqual(n * SIMILARITY_LIMITS.topKPerPage);
    expect(pairs.length).toBeGreaterThan(0);
    expect(pairs[0].similarity).toBe(1);
  });

  it('compares at most maxPages pages and still finds real pairs', async () => {
    const saved = SIMILARITY_LIMITS.maxPages;
    SIMILARITY_LIMITS.maxPages = 3;
    try {
      const { pairs, compared } = await findSimilarPairs(
        ['a', 'b', 'c', 'd'], [[1, 0], [1, 0.01], [0, 1], [1, 0]], 0.9);
      expect(compared).toBe(3);
      expect(pairs).toHaveLength(1);
      expect(pairs[0]).toMatchObject({ url1: 'a', url2: 'b' });
      expect(pairs[0].similarity).toBeGreaterThan(0.999);
    } finally {
      SIMILARITY_LIMITS.maxPages = saved;
    }
  });
});
