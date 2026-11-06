import { describe, it, expect } from 'vitest';
import { compareRender } from './render-compare.js';

const html = (links: string[]) => `<html><body>${links.map((h) => `<a href="${h}">x</a>`).join('')}</body></html>`;
const linkDiff = (raw: string[], rendered: string[], pageUrl?: string) =>
  compareRender(html(raw), html(rendered), pageUrl).find((d) => d.field === 'internal_links_count');

describe('compareRender internal links', () => {
  it('a relative link rewritten as an absolute same-origin URL is the same link', () => {
    expect(linkDiff(['/a', 'b'], ['https://site.test/a', 'https://site.test/dir/b'], 'https://site.test/dir/page')).toBeUndefined();
  });

  it('protocol-relative and other-origin links are not internal', () => {
    const d = linkDiff(['/a'], ['/a', '//evil.test/x', 'https://other.test/y', 'mailto:a@b.c'], 'https://site.test/');
    expect(d).toBeUndefined();
  });

  it('counts a real change in internal links', () => {
    const d = linkDiff(['/a'], ['/a', 'https://site.test/new'], 'https://site.test/');
    expect(d).toEqual({ field: 'internal_links_count', original: '1', rendered: '2' });
  });

  it('without the page URL, //host links are still not internal', () => {
    expect(linkDiff(['/a'], ['/a', '//evil.test/x'])).toBeUndefined();
  });
});
