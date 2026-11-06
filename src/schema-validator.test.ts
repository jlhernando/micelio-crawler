import { describe, it, expect } from 'vitest';
import { validateStructuredData } from './schema-validator.js';

const jsonLd = (value: unknown, raw = JSON.stringify(value)) =>
  validateStructuredData([{ type: 'x', format: 'json-ld', raw }]);

const product = { '@type': 'Product', name: 'Shoe', offers: { '@type': 'Offer', price: '10', priceCurrency: 'EUR', availability: 'InStock' } };

describe('validateStructuredData JSON-LD', () => {
  it('JSON null, numbers and strings give an issue instead of throwing', () => {
    for (const raw of ['null', '42', '"text"']) {
      const [r] = jsonLd(null, raw);
      expect(r.richResultEligible).toBe(false);
      expect(r.issues.length).toBeGreaterThan(0);
    }
  });

  it('values whose toString is not a function do not throw', () => {
    const hostile = { toString: 1, valueOf: 1 };
    expect(() => jsonLd({ '@context': hostile, '@type': 'Product', name: 'x' })).not.toThrow();
    expect(() => jsonLd({ '@context': 'https://schema.org', '@type': hostile })).not.toThrow();
    expect(() => jsonLd({ '@context': 'https://schema.org', ...product, offers: { '@type': hostile } })).not.toThrow();
    const [r] = jsonLd({ '@context': 'https://schema.org', '@type': hostile });
    expect(r.issues.some((i) => i.severity === 'error')).toBe(true);
  });

  it('@type names that exist on Object.prototype are just unknown types', () => {
    expect(() => jsonLd({ '@context': 'https://schema.org', '@type': 'constructor' })).not.toThrow();
    expect(jsonLd({ '@context': 'https://schema.org', '@type': '__proto__' })[0].richResultEligible).toBe(false);
  });

  it('validates each node of a top-level array', () => {
    const results = jsonLd([
      { '@context': 'https://schema.org', ...product },
      { '@context': 'https://schema.org', '@type': 'BreadcrumbList', itemListElement: [{ position: 1, name: 'Home', item: 'https://a.test/' }] },
    ]);
    expect(results.map((r) => r.type)).toEqual(['Product', 'BreadcrumbList']);
    expect(results.every((r) => r.richResultEligible)).toBe(true);
    expect(results.some((r) => r.issues.some((i) => i.message === 'Missing @type'))).toBe(false);
  });

  it('@graph children inherit a schema.org context from the container', () => {
    const [r] = jsonLd({ '@context': 'https://schema.org', '@graph': [product] });
    expect(r.richResultEligible).toBe(true);
    expect(r.issues.some((i) => i.message.includes('@context'))).toBe(false);
  });

  it('@graph without a schema.org context does not make its children eligible', () => {
    for (const container of [{ '@graph': [product] }, { '@context': 'https://example.org/vocab', '@graph': [product] }]) {
      const [r] = jsonLd(container);
      expect(r.richResultEligible).toBe(false);
      expect(r.issues.some((i) => i.message.includes('@context'))).toBe(true);
    }
  });

  it('a Product with only a name is not eligible', () => {
    const [r] = jsonLd({ '@context': 'https://schema.org', '@type': 'Product', name: 'Shoe' });
    expect(r.richResultEligible).toBe(false);
    expect(r.issues.some((i) => i.severity === 'error' && i.message.includes('offers'))).toBe(true);
  });

  it('a Product with a review or rating instead of offers stays eligible', () => {
    const [r] = jsonLd({ '@context': 'https://schema.org', '@type': 'Product', name: 'Shoe', aggregateRating: { ratingValue: 4, reviewCount: 3 } });
    expect(r.richResultEligible).toBe(true);
  });
});
