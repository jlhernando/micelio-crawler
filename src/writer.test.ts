import { describe, it, expect } from 'vitest';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { ResultWriter } from './writer.js';
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
});
