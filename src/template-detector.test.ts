import { describe, it, expect } from 'vitest';
import { detectTemplateType, buildTemplateStats } from './template-detector.js';
import type { TemplateDetectionInput } from './template-detector.js';
import type { PageData } from './types.js';

function makeInput(overrides: Partial<TemplateDetectionInput> = {}): TemplateDetectionInput {
  return {
    url: 'https://example.com/page',
    finalUrl: 'https://example.com/page',
    depth: 1,
    wordCount: 200,
    internalLinks: [],
    externalLinks: [],
    images: [],
    headings: { h1: [], h2: [], h3: [] },
    anchors: [],
    structuredData: [],
    schemaValidation: [],
    openGraph: {},
    ...overrides,
  };
}

const SEED = 'https://example.com';

describe('detectTemplateType', () => {
  it('detects homepage by URL', () => {
    const input = makeInput({ url: 'https://example.com/', finalUrl: 'https://example.com/', depth: 0 });
    expect(detectTemplateType(input, SEED)).toBe('homepage');
  });

  it('detects article by URL pattern', () => {
    const input = makeInput({
      url: 'https://example.com/blog/my-article',
      finalUrl: 'https://example.com/blog/my-article',
      wordCount: 900,
      headings: { h1: ['My Article'], h2: ['Section 1', 'Section 2', 'Section 3'], h3: [] },
    });
    expect(detectTemplateType(input, SEED)).toBe('article');
  });

  it('detects article by date-based URL', () => {
    const input = makeInput({
      url: 'https://example.com/2024/03/my-post',
      finalUrl: 'https://example.com/2024/03/my-post',
      wordCount: 1000,
    });
    expect(detectTemplateType(input, SEED)).toBe('article');
  });

  it('detects product by URL pattern', () => {
    const input = makeInput({
      url: 'https://example.com/product/widget-123',
      finalUrl: 'https://example.com/product/widget-123',
      images: [{ src: 'a.jpg' }, { src: 'b.jpg' }] as any,
    });
    expect(detectTemplateType(input, SEED)).toBe('product');
  });

  it('detects product by schema.org structured data', () => {
    const input = makeInput({
      url: 'https://example.com/item/abc',
      finalUrl: 'https://example.com/item/abc',
      structuredData: [{ type: 'Product', properties: {} }] as any,
    });
    expect(detectTemplateType(input, SEED)).toBe('product');
  });

  it('detects listing by URL pattern', () => {
    const input = makeInput({
      url: 'https://example.com/category/electronics',
      finalUrl: 'https://example.com/category/electronics',
      internalLinks: Array(20).fill('https://example.com/page'),
    });
    expect(detectTemplateType(input, SEED)).toBe('listing');
  });

  it('detects listing by content (short text, many links)', () => {
    const input = makeInput({
      url: 'https://example.com/browse',
      finalUrl: 'https://example.com/browse',
      wordCount: 100,
      internalLinks: Array(35).fill('https://example.com/item'),
    });
    expect(detectTemplateType(input, SEED)).toBe('listing');
  });

  it('detects legal page by URL', () => {
    const input = makeInput({
      url: 'https://example.com/privacy-policy',
      finalUrl: 'https://example.com/privacy-policy',
      wordCount: 500,
    });
    expect(detectTemplateType(input, SEED)).toBe('legal');
  });

  it('detects contact page by URL', () => {
    const input = makeInput({
      url: 'https://example.com/contact',
      finalUrl: 'https://example.com/contact',
    });
    expect(detectTemplateType(input, SEED)).toBe('contact');
  });

  it('detects FAQ by URL', () => {
    const input = makeInput({
      url: 'https://example.com/faq',
      finalUrl: 'https://example.com/faq',
    });
    expect(detectTemplateType(input, SEED)).toBe('faq');
  });

  it('detects FAQ by schema type', () => {
    // Use a neutral URL that doesn't trigger /help → contact pattern
    const input = makeInput({
      url: 'https://example.com/info',
      finalUrl: 'https://example.com/info',
      structuredData: [{ type: 'FAQPage', properties: {} }] as any,
    });
    expect(detectTemplateType(input, SEED)).toBe('faq');
  });

  it('detects search page by URL', () => {
    const input = makeInput({
      url: 'https://example.com/search?q=test',
      finalUrl: 'https://example.com/search?q=test',
    });
    expect(detectTemplateType(input, SEED)).toBe('search');
  });

  it('detects login page by URL', () => {
    const input = makeInput({
      url: 'https://example.com/login',
      finalUrl: 'https://example.com/login',
    });
    expect(detectTemplateType(input, SEED)).toBe('login');
  });

  it('detects FAQ by H1 text', () => {
    const input = makeInput({
      headings: { h1: ['Frequently Asked Questions'], h2: ['Q1', 'Q2', 'Q3', 'Q4', 'Q5'], h3: [] },
    });
    expect(detectTemplateType(input, SEED)).toBe('faq');
  });

  it('detects contact by H1 text', () => {
    const input = makeInput({
      headings: { h1: ['Get in Touch'], h2: [], h3: [] },
    });
    expect(detectTemplateType(input, SEED)).toBe('contact');
  });

  it('detects article by OG type', () => {
    const input = makeInput({
      url: 'https://example.com/post/123',
      finalUrl: 'https://example.com/post/123',
      openGraph: { 'og:type': 'article' },
      wordCount: 800,
    });
    expect(detectTemplateType(input, SEED)).toBe('article');
  });

  it('returns "other" for ambiguous pages', () => {
    const input = makeInput({
      url: 'https://example.com/something',
      finalUrl: 'https://example.com/something',
      wordCount: 150,
    });
    expect(detectTemplateType(input, SEED)).toBe('other');
  });

  it('returns "other" when no score reaches threshold', () => {
    const input = makeInput();
    expect(detectTemplateType(input, SEED)).toBe('other');
  });
});

describe('buildTemplateStats', () => {
  it('counts template type distribution', () => {
    const pages = [
      { templateType: 'article' },
      { templateType: 'article' },
      { templateType: 'product' },
      { templateType: 'homepage' },
    ] as PageData[];

    const stats = buildTemplateStats(pages);
    expect(stats.article).toBe(2);
    expect(stats.product).toBe(1);
    expect(stats.homepage).toBe(1);
  });

  it('counts pages without templateType as "other"', () => {
    const pages = [
      { templateType: '' },
      { templateType: undefined },
    ] as unknown as PageData[];

    const stats = buildTemplateStats(pages);
    expect(stats.other).toBe(2);
  });

  it('returns empty object for empty array', () => {
    expect(buildTemplateStats([])).toEqual({});
  });
});
