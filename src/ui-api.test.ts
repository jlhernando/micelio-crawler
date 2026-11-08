import type { PageData } from './types.js';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  validateSettings, applyEnvFallbacks, maskSecrets, redactConfig, unmaskConfig, validateCrawlRequest,
  MASKED_SECRET, BadRequestError, buildGraphResponse, MAX_GRAPH_EDGES,
} from './ui-api.js';
import { setTrustedProxy } from './net-guard.js';

describe('validateSettings', () => {
  it('accepts valid settings with correct types', () => {
    const { valid, rejected } = validateSettings({
      defaultDepth: 5,
      defaultLimit: 1000,
      defaultUserAgent: 'MyBot/1.0',
      defaultEmbeddings: true,
    });
    expect(valid).toEqual({
      defaultDepth: 5,
      defaultLimit: 1000,
      defaultUserAgent: 'MyBot/1.0',
      defaultEmbeddings: true,
    });
    expect(rejected).toEqual([]);
  });

  it('rejects unknown keys', () => {
    const { valid, rejected } = validateSettings({
      defaultDepth: 5,
      unknownKey: 'value',
      anotherBadKey: 42,
    });
    expect(valid).toEqual({ defaultDepth: 5 });
    expect(rejected).toContain('unknownKey');
    expect(rejected).toContain('anotherBadKey');
  });

  it('rejects values with wrong type', () => {
    const { valid, rejected } = validateSettings({
      defaultDepth: 'not a number',  // should be number
      defaultUserAgent: 42,          // should be string
      defaultEmbeddings: 'yes',      // should be boolean
    });
    expect(valid).toEqual({});
    expect(rejected).toContain('defaultDepth');
    expect(rejected).toContain('defaultUserAgent');
    expect(rejected).toContain('defaultEmbeddings');
  });

  it('validates all expected number fields', () => {
    const numberFields = [
      'defaultDepth', 'defaultLimit', 'defaultConcurrency', 'defaultDelay',
      'gscDays', 'ga4Days', 'similarityThreshold', 'liMaxSuggestions',
    ];
    const input: Record<string, unknown> = {};
    for (const field of numberFields) input[field] = 42;
    const { valid, rejected } = validateSettings(input);
    expect(rejected).toEqual([]);
    for (const field of numberFields) {
      expect(valid[field]).toBe(42);
    }
  });

  it('validates all expected string fields', () => {
    // File path fields require absolute paths, so use special values
    const stringFields = [
      'defaultUserAgent', 'psiKey', 'aiProvider', 'aiModel', 'aiKey',
      'gscProperty', 'ga4Property',
      'cruxKey', 'embeddingModel', 'defaultOutputFormat',
    ];
    const input: Record<string, unknown> = {};
    for (const field of stringFields) input[field] = 'test';
    // File path fields need absolute paths
    input.gscKeyFile = '/path/to/gsc.json';
    input.ga4KeyFile = '/path/to/ga4.json';
    const { valid, rejected } = validateSettings(input);
    expect(rejected).toEqual([]);
    for (const field of stringFields) {
      expect(valid[field]).toBe('test');
    }
    expect(valid.gscKeyFile).toBe('/path/to/gsc.json');
    expect(valid.ga4KeyFile).toBe('/path/to/ga4.json');
  });

  it('validates all expected boolean fields', () => {
    const boolFields = [
      'defaultEmbeddings', 'defaultNgrams', 'defaultLinkIntelligence',
      'liNoCentrality', 'defaultSitemapOut', 'defaultHtmlReport',
      'defaultCheckExternal', 'defaultJsRendering',
    ];
    const input: Record<string, unknown> = {};
    for (const field of boolFields) input[field] = true;
    const { valid, rejected } = validateSettings(input);
    expect(rejected).toEqual([]);
    for (const field of boolFields) {
      expect(valid[field]).toBe(true);
    }
  });

  // File path validation
  it('accepts absolute file paths', () => {
    const { valid, rejected } = validateSettings({
      gscKeyFile: '/home/user/credentials.json',
      ga4KeyFile: '/etc/secrets/ga4.json',
    });
    expect(rejected).toEqual([]);
    expect(valid.gscKeyFile).toBe('/home/user/credentials.json');
    expect(valid.ga4KeyFile).toBe('/etc/secrets/ga4.json');
  });

  it('rejects relative file paths', () => {
    const { valid, rejected } = validateSettings({
      gscKeyFile: 'relative/path.json',
    });
    expect(rejected).toContain('gscKeyFile');
    expect(valid.gscKeyFile).toBeUndefined();
  });

  it('rejects file paths with directory traversal', () => {
    const { valid, rejected } = validateSettings({
      gscKeyFile: '/home/user/../../../etc/passwd',
    });
    expect(rejected).toContain('gscKeyFile');
    expect(valid.gscKeyFile).toBeUndefined();
  });

  it('rejects file paths with backslash traversal', () => {
    const { valid, rejected } = validateSettings({
      ga4KeyFile: '/home\\..\\etc\\passwd',
    });
    // Backslashes are normalized to / before checking
    expect(rejected).toContain('ga4KeyFile');
  });

  it('accepts empty file path strings', () => {
    const { valid, rejected } = validateSettings({
      gscKeyFile: '',
    });
    // Empty string is OK (means not configured)
    expect(rejected).toEqual([]);
    expect(valid.gscKeyFile).toBe('');
  });

  it('handles empty input', () => {
    const { valid, rejected } = validateSettings({});
    expect(valid).toEqual({});
    expect(rejected).toEqual([]);
  });
});

describe('applyEnvFallbacks', () => {
  const originalEnv = process.env;

  beforeEach(() => {
    process.env = { ...originalEnv };
  });

  afterEach(() => {
    process.env = originalEnv;
  });

  it('does nothing when no env vars are set', () => {
    delete process.env.MICELIO_PSI_KEY;
    delete process.env.MICELIO_CRUX_KEY;
    delete process.env.MICELIO_AI_KEY;

    const result = applyEnvFallbacks({ defaultDepth: 5 });
    expect(result).toEqual({ defaultDepth: 5 });
  });

  it('fills missing keys from env vars', () => {
    process.env.MICELIO_PSI_KEY = 'env-psi-key';
    process.env.MICELIO_CRUX_KEY = 'env-crux-key';
    process.env.MICELIO_AI_KEY = 'env-ai-key';

    const result = applyEnvFallbacks({});
    expect(result.psiKey).toBe('env-psi-key');
    expect(result.cruxKey).toBe('env-crux-key');
    expect(result.aiKey).toBe('env-ai-key');
  });

  it('does not override existing values', () => {
    process.env.MICELIO_PSI_KEY = 'env-psi-key';

    const result = applyEnvFallbacks({ psiKey: 'user-psi-key' });
    expect(result.psiKey).toBe('user-psi-key');
  });

  it('does not mutate the input object', () => {
    process.env.MICELIO_PSI_KEY = 'env-psi-key';

    const input = { defaultDepth: 5 };
    const result = applyEnvFallbacks(input);
    expect(input).toEqual({ defaultDepth: 5 }); // unchanged
    expect(result.psiKey).toBe('env-psi-key');
  });

  it('skips falsy env var values', () => {
    process.env.MICELIO_PSI_KEY = '';

    const result = applyEnvFallbacks({});
    expect(result.psiKey).toBeUndefined();
  });
});

describe('secret masking', () => {
  it('masks API keys in settings and keeps them on a masked round trip', () => {
    const masked = maskSecrets({ psiKey: 'real', aiKey: '', defaultDepth: 3 });
    expect(masked).toEqual({ psiKey: MASKED_SECRET, aiKey: '', defaultDepth: 3 });
    const { valid, rejected } = validateSettings({ psiKey: MASKED_SECRET, cruxKey: 'new' });
    expect(valid).toEqual({ cruxKey: 'new' });
    expect(rejected).toEqual([]);
  });

  it('redacts credentials in crawl and preset configs', () => {
    const red = redactConfig({
      seedUrl: 'https://a.com', cookies: 'sid=1', customHeaders: { Authorization: 'Bearer t' },
      proxy: 'http://user:pw@proxy:8080', aiKey: 'k', depth: 2,
    });
    expect(red.cookies).toBe(MASKED_SECRET);
    expect(red.customHeaders).toEqual({ Authorization: MASKED_SECRET });
    expect(red.aiKey).toBe(MASKED_SECRET);
    expect(String(red.proxy)).not.toContain(':pw@');
    expect(red.seedUrl).toBe('https://a.com');
    expect(red.depth).toBe(2);
  });

  it('unmasks API keys from settings and drops masked cookies/headers', () => {
    const cfg = unmaskConfig(
      { psiKey: MASKED_SECRET, aiKey: MASKED_SECRET, cookies: MASKED_SECRET, customHeaders: { A: MASKED_SECRET, B: 'b' } },
      { psiKey: 'saved' },
    );
    expect(cfg).toEqual({ psiKey: 'saved', customHeaders: { B: 'b' } });
    expect(() => unmaskConfig(redactConfig({ proxy: 'http://u:p@h:1' }), {})).toThrow(BadRequestError);
  });
});

describe('validateCrawlRequest', () => {
  it('rejects snippet paths from the API', async () => {
    await expect(validateCrawlRequest({ seedUrl: 'https://93.184.215.14/', snippetPaths: ['/tmp/x.mjs'] }))
      .rejects.toThrow(/snippetPaths/);
  });

  it('rejects private and non-http entry URLs', async () => {
    for (const cfg of [
      { seedUrl: 'http://127.0.0.1:8080/' },
      { seedUrl: 'http://localhost/' },
      { seedUrl: 'http://169.254.169.254/latest/meta-data/' },
      { sitemapUrls: ['https://93.184.215.14/s.xml', 'http://10.0.0.1/s.xml'] },
      { seedUrl: 'file:///etc/passwd' },
    ]) {
      await expect(validateCrawlRequest(cfg), JSON.stringify(cfg)).rejects.toThrow(BadRequestError);
    }
  });

  it('accepts public literal URLs', async () => {
    await expect(validateCrawlRequest({ seedUrl: 'https://93.184.215.14/' })).resolves.toBeUndefined();
  });

  it('list mode: urlList URLs are checked, an empty list is refused', async () => {
    await expect(validateCrawlRequest({ mode: 'list', urlList: 'https://93.184.215.14/a\n\nhttps://93.184.215.14/b' }))
      .resolves.toBeUndefined();
    await expect(validateCrawlRequest({ mode: 'list', urlList: 'https://93.184.215.14/a\nhttp://127.0.0.1/' }))
      .rejects.toThrow(BadRequestError);
    await expect(validateCrawlRequest({ mode: 'list', urlList: '  \n' })).rejects.toThrow(/at least one URL/);
    await expect(validateCrawlRequest({ mode: 'list', urlList: 42 })).rejects.toThrow(BadRequestError);
  });
});

describe('proxy from the API (trusted proxy only)', () => {
  afterEach(() => setTrustedProxy(''));

  it('rejects any proxy when no trusted proxy is configured', async () => {
    for (const proxy of ['http://10.0.0.5:3128', 'http://proxy.example.com:8080', 'socks5://127.0.0.1:9050']) {
      await expect(validateCrawlRequest({ seedUrl: 'https://93.184.215.14/', proxy }), proxy)
        .rejects.toThrow(/trusted proxy/);
    }
    await expect(validateCrawlRequest({ seedUrl: 'https://93.184.215.14/', proxy: 42 })).rejects.toThrow(BadRequestError);
  });

  it('accepts only the trusted proxy, and no proxy', async () => {
    setTrustedProxy('http://user:pw@proxy.example.com:8080');
    await expect(validateCrawlRequest({ seedUrl: 'https://93.184.215.14/', proxy: 'http://user:pw@proxy.example.com:8080' }))
      .resolves.toBeUndefined();
    await expect(validateCrawlRequest({ seedUrl: 'https://93.184.215.14/', proxy: '' })).resolves.toBeUndefined();
    await expect(validateCrawlRequest({ seedUrl: 'https://93.184.215.14/' })).resolves.toBeUndefined();
    await expect(validateCrawlRequest({ seedUrl: 'https://93.184.215.14/', proxy: 'http://user:pw@10.0.0.5:8080' }))
      .rejects.toThrow(/trusted proxy/);
  });

  it('turns the masked trusted proxy back into the real one', () => {
    setTrustedProxy('http://user:pw@proxy.example.com:8080');
    const cfg = unmaskConfig(redactConfig({ proxy: 'http://user:pw@proxy.example.com:8080' }), {});
    expect(cfg.proxy).toBe('http://user:pw@proxy.example.com:8080');
    expect(() => unmaskConfig(redactConfig({ proxy: 'http://user:pw@other:1' }), {})).toThrow(BadRequestError);
  });
});

describe('buildGraphResponse', () => {
  const pagesWithLinks = (n: number, linksOf: (i: number, urls: string[]) => string[]) => {
    const urls = Array.from({ length: n }, (_, i) => `https://a.test/${i}`);
    return urls.map((url, i) => ({ url, internalLinks: linksOf(i, urls) }) as unknown as PageData);
  };

  it('keeps every edge of a small sparse crawl', () => {
    const pages = pagesWithLinks(10, (i, urls) => [urls[(i + 1) % 10], urls[i]]);
    const g = buildGraphResponse(pages);
    expect(g.edges).toHaveLength(10); // self-links skipped
    expect(g.meta.samplingApplied).toBe(false);
  });

  it('caps the edges of a small but dense crawl', () => {
    // 1,500 pages, every page links to every page: 2.25M candidate edges
    const n = 1500;
    const pages = pagesWithLinks(n, (_, urls) => urls);
    const g = buildGraphResponse(pages);
    expect(g.meta.totalEdgesBeforeSampling).toBe(n * n);
    expect(g.meta.samplingApplied).toBe(true);
    expect(g.edges.length).toBeLessThanOrEqual(MAX_GRAPH_EDGES);
    expect(g.edges.length).toBeLessThanOrEqual(n * 15);
    expect(g.nodes).toHaveLength(n);
  });

  it('spreads the edge budget so the last pages keep links too', () => {
    // 20,000 pages x 20 links: 15 per page would hit the cap after 6,667 pages
    const n = 20_000;
    const pages = pagesWithLinks(n, (i, urls) => Array.from({ length: 20 }, (_, k) => urls[(i + k + 1) % n]));
    const g = buildGraphResponse(pages);
    expect(g.edges.length).toBeLessThanOrEqual(MAX_GRAPH_EDGES);
    const perSource = new Map<string, number>();
    for (const e of g.edges) perSource.set(e.source, (perSource.get(e.source) ?? 0) + 1);
    expect(perSource.size).toBe(n); // every page keeps outgoing edges
    expect(perSource.get(`https://a.test/${n - 1}`)).toBe(Math.floor(MAX_GRAPH_EDGES / n));
  });

  it('keeps real edges when self-links precede a one-edge sampling budget', () => {
    const n = 50_001;
    const pages = pagesWithLinks(n, (i, urls) => [urls[i], urls[(i + 1) % n]]);
    const g = buildGraphResponse(pages);
    expect(g.meta.samplingApplied).toBe(true);
    expect(g.edges).toHaveLength(n);
    expect(new Set(g.edges.map((e) => e.source)).size).toBe(n);
    expect(g.edges.every((e) => e.source !== e.target)).toBe(true);
    expect(g.edges.length).toBeLessThanOrEqual(MAX_GRAPH_EDGES);
  });

  it('does not spend the sampling budget on duplicate links', () => {
    const n = 6000;
    const pages = pagesWithLinks(n, (i, urls) => [
      ...Array(15).fill(urls[(i + 1) % n]),
      urls[(i + 2) % n],
    ]);
    const g = buildGraphResponse(pages);
    expect(g.meta.samplingApplied).toBe(true);
    expect(g.edges).toHaveLength(2 * n);
    expect(new Set(g.edges.map((e) => `${e.source}\t${e.target}`)).size).toBe(2 * n);
  });
});
