import { describe, it, expect } from 'vitest';
import { createHash } from 'node:crypto';
import * as cheerio from 'cheerio';
import { generateDiffHtml } from './diff-report.js';
import { jsonForScript } from './utils.js';
import type { DiffResult } from './types.js';

// Values a hostile crawled site controls: URLs, titles, headings
const hostile = [
  '</ScRiPt><img src=x onerror=alert(1)>',
  '</script ><svg onload=alert(2)>',
  '<!--<script>',
  '-->&lt;&amp;\u2028\u2029',
];

const result: DiffResult = {
  oldFile: `/tmp/${hostile[0]}.jsonl`,
  newFile: '/tmp/new.jsonl',
  oldCount: 2,
  newCount: 3,
  addedUrls: [`https://evil.test/${hostile[0]}`, `https://evil.test/${hostile[2]}`],
  removedUrls: [`https://evil.test/${hostile[1]}`],
  changedUrls: [{
    url: `https://evil.test/${hostile[3]}`,
    changes: [{ field: 'title', oldValue: hostile[2], newValue: hostile[0] }],
  }],
  unchangedCount: 0,
  urlMappingsApplied: 0,
  fieldSummary: { [hostile[1]]: 1 },
} as DiffResult;

describe('generateDiffHtml', () => {
  const html = generateDiffHtml(result);
  const $ = cheerio.load(html);

  it('keeps hostile crawl values inside the one data script', () => {
    expect($('script')).toHaveLength(1);
    expect($('img, svg, [onerror], [onload]')).toHaveLength(0);
    // No end tag in any case and no comment opener can reach the HTML parser
    const script = $('script').text();
    expect(script).not.toMatch(/<\/script/i);
    expect(script).not.toContain('<!--');
    const dataLine = script.split('\n').find((l) => l.startsWith('const R = '))!;
    expect(dataLine).not.toMatch(/[<>&]/);
    expect(html.match(/<\/script/gi)).toHaveLength(1);
  });

  it('embeds data that parses back to exactly the input', () => {
    const line = $('script').text().split('\n').find((l) => l.startsWith('const R = '))!;
    expect(JSON.parse(line.slice('const R = '.length, -1))).toEqual(result);
  });

  it('allows only its own script through a hash-based Content-Security-Policy', () => {
    const csp = $('meta[http-equiv="Content-Security-Policy"]').attr('content') ?? '';
    const hash = createHash('sha256').update($('script').text(), 'utf8').digest('base64');
    expect(csp).toContain(`script-src 'sha256-${hash}'`);
    expect(csp).toContain("default-src 'none'");
    expect(csp).not.toContain('unsafe-eval');
    expect(csp).not.toMatch(/script-src[^;]*unsafe-inline/);
  });
});

describe('jsonForScript', () => {
  it('escapes <, >, &, U+2028 and U+2029 and round-trips', () => {
    const value = { a: '</ScRiPt><!--&\u2028\u2029', n: 1, list: [null, true] };
    const out = jsonForScript(value);
    expect(out).not.toMatch(/[<>&\u2028\u2029]/);
    expect(out).toContain('\\u003c/ScRiPt\\u003e\\u003c!--\\u0026\\u2028\\u2029');
    expect(JSON.parse(out)).toEqual(value);
  });
});
