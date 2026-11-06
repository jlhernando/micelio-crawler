import { describe, it, expect } from 'vitest';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { ResultWriter, csvDynamicColumns } from './writer.js';
import type { PageData } from './types.js';

const page = (url: string, extra = ''): PageData => ({ url, title: extra } as unknown as PageData);

describe('ResultWriter', () => {
  it('rejects instead of crashing when the output cannot be opened', async () => {
    // No listener before close() used to turn this into an uncaught exception
    const writer = new ResultWriter(join(tmpdir(), 'micelio-no-such-dir', 'x', 'out.jsonl'), 'jsonl');
    await new Promise((r) => setTimeout(r, 50)); // let the open fail
    expect(writer.failed).toBe(true);
    await expect(writer.write(page('https://a.test/'))).rejects.toThrow(/ENOENT/);
    await expect(writer.close()).rejects.toThrow(/ENOENT/);
  });

  it('rejects CSV output errors too', async () => {
    const writer = new ResultWriter(join(tmpdir(), 'micelio-no-such-dir', 'y', 'out.csv'), 'csv');
    await expect(writer.close()).rejects.toThrow(/ENOENT/);
  });

  it('waits for drain when the stream buffer is full', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'micelio-writer-'));
    try {
      const out = join(dir, 'out.jsonl');
      const writer = new ResultWriter(out, 'jsonl');
      const stream = (writer as unknown as { stream: import('node:fs').WriteStream }).stream;
      const big = 'x'.repeat(64 * 1024); // above the 16 KB highWaterMark
      for (let i = 0; i < 20; i++) {
        await writer.write(page(`https://a.test/${i}`, big));
        // After each awaited write the buffer is below the high-water mark
        expect(stream.writableNeedDrain).toBe(false);
      }
      await writer.close();
      expect(readFileSync(out, 'utf8').trim().split('\n')).toHaveLength(20);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it('writes CSV with backpressure', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'micelio-writer-'));
    try {
      const out = join(dir, 'out.csv');
      const writer = new ResultWriter(out, 'csv');
      const full = {
        url: 'https://a.test/', finalUrl: 'https://a.test/', statusCode: 200, redirectChain: [],
        headings: { h1: [], h2: [] }, internalLinks: [], externalLinks: [], images: [], hreflang: [],
        structuredData: [], openGraph: {}, twitterCard: {}, anchors: [],
        security: {}, customExtractions: {}, customSearches: {}, snippetResults: {}, segments: [],
      } as unknown as PageData;
      for (let i = 0; i < 50; i++) await writer.write({ ...full, url: `https://a.test/${i}` });
      await writer.close();
      expect(readFileSync(out, 'utf8').trim().split('\n')).toHaveLength(51);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it('CSV keeps custom columns when an error page is written first', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'micelio-writer-'));
    try {
      const out = join(dir, 'out.csv');
      const config = {
        customExtractions: [{ name: 'price', type: 'css' as const, selector: '.p' }],
        customSearches: [{ name: 'promo', pattern: 'promo', isRegex: false }],
        snippetPaths: ['checks.mjs'],
      };
      const writer = new ResultWriter(out, 'csv', csvDynamicColumns(config));
      const base = {
        finalUrl: 'https://a.test/', statusCode: 200, redirectChain: [],
        headings: { h1: [], h2: [] }, internalLinks: [], externalLinks: [], images: [], hreflang: [],
        structuredData: [], openGraph: {}, twitterCard: {}, anchors: [], security: {}, segments: [],
      };
      // Concurrent crawls finish in any order: the error page can come first
      await writer.write({ ...base, url: 'https://a.test/broken', statusCode: 500, customExtractions: {}, customSearches: {}, snippetResults: {} } as unknown as PageData);
      await writer.write({
        ...base, url: 'https://a.test/ok',
        customExtractions: { price: ['9.99'] }, customSearches: { promo: true },
        snippetResults: { checks: { a: 1 } },
      } as unknown as PageData);
      await writer.write({ ...base, url: 'https://a.test/ok2', customExtractions: {}, customSearches: {}, snippetResults: { checks: { b: 2 } } } as unknown as PageData);
      await writer.close();
      const [header, , ok, ok2] = readFileSync(out, 'utf8').trim().split('\n');
      const cols = header.split(',');
      expect(cols).toEqual(expect.arrayContaining(['extract_price', 'search_promo', 'snippet_checks']));
      const cell = (line: string, col: string) => line.split(/,(?=(?:[^"]*"[^"]*")*[^"]*$)/)[cols.indexOf(col)];
      expect(cell(ok, 'extract_price')).toBe('9.99');
      expect(cell(ok, 'search_promo')).toBe('true');
      expect(cell(ok, 'snippet_checks')).toBe('"{""a"":1}"');
      // A snippet key the first rows never had is not lost either
      expect(cell(ok2, 'snippet_checks')).toBe('"{""b"":2}"');
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
