import { describe, it, expect } from 'vitest';
import { NgramAnalyzer, SUPPORTED_LANGUAGES } from './ngrams.js';

describe('NgramAnalyzer', () => {
  it('extracts unigrams, bigrams, and trigrams from a single page', () => {
    const analyzer = new NgramAnalyzer('en');
    // Need enough text to pass the 50-char and 3-token thresholds
    analyzer.addPage(
      'technical seo audit crawling performance optimization technical seo audit performance crawling'
    );
    const results = analyzer.getResults();
    expect(results.totalPages).toBe(1);
    expect(results.totalTokens).toBeGreaterThan(0);
    expect(results.unigrams.length).toBeGreaterThan(0);
    expect(results.bigrams.length).toBeGreaterThan(0);
    expect(results.trigrams.length).toBeGreaterThan(0);
  });

  it('filters English stopwords', () => {
    const analyzer = new NgramAnalyzer('en');
    analyzer.addPage(
      'the quick brown fox jumps over the lazy dog quick brown fox jumps lazy brown dog'
    );
    const results = analyzer.getResults();
    const unigramTerms = results.unigrams.map(u => u.term);
    expect(unigramTerms).not.toContain('the');
    expect(unigramTerms).not.toContain('over');
    expect(unigramTerms).toContain('quick');
    expect(unigramTerms).toContain('brown');
    expect(unigramTerms).toContain('fox');
  });

  it('skips pages with very short text', () => {
    const analyzer = new NgramAnalyzer('en');
    analyzer.addPage('hello');
    expect(analyzer.getResults().totalPages).toBe(0);
  });

  it('skips pages with fewer than 3 tokens after filtering', () => {
    const analyzer = new NgramAnalyzer('en');
    // All stopwords — "the is a" → nothing after filtering
    analyzer.addPage('the is a and but or not the is a and or but not the is a');
    expect(analyzer.getResults().totalPages).toBe(0);
  });

  it('tracks page counts correctly across multiple pages', () => {
    const analyzer = new NgramAnalyzer('en');
    analyzer.addPage(
      'technical seo crawling performance optimization keywords rankings metrics analysis'
    );
    analyzer.addPage(
      'technical seo analysis indexing rendering javascript pages content structure'
    );
    const results = analyzer.getResults();
    expect(results.totalPages).toBe(2);

    // "technical" and "seo" appear on both pages
    const technical = results.unigrams.find(u => u.term === 'technical');
    expect(technical?.pages).toBe(2);
    const seo = results.unigrams.find(u => u.term === 'seo');
    expect(seo?.pages).toBe(2);
  });

  it('computes TF-IDF scores', () => {
    const analyzer = new NgramAnalyzer('en');
    analyzer.addPage(
      'crawling crawling crawling performance optimization technical seo analysis metrics reporting'
    );
    analyzer.addPage(
      'performance technical seo metrics rendering javascript content analysis indexing structure'
    );
    const results = analyzer.getResults();

    // "crawling" appears on 1 page with high count → higher TF-IDF than common terms
    const crawling = results.unigrams.find(u => u.term === 'crawling');
    expect(crawling).toBeDefined();
    expect(crawling!.tfidf).toBeGreaterThan(0);
  });

  it('sorts results by count descending', () => {
    const analyzer = new NgramAnalyzer('en');
    analyzer.addPage(
      'seo seo seo crawl crawl performance seo crawl seo performance performance crawl crawl performance'
    );
    const results = analyzer.getResults();
    for (let i = 1; i < results.unigrams.length; i++) {
      expect(results.unigrams[i - 1].count).toBeGreaterThanOrEqual(results.unigrams[i].count);
    }
  });

  it('respects topN limit', () => {
    const analyzer = new NgramAnalyzer('en');
    const words = Array.from({ length: 100 }, (_, i) => `word${i}`);
    // Repeat enough for them all to pass 2-page threshold
    analyzer.addPage(words.join(' ') + ' ' + words.join(' '));
    const results = analyzer.getResults(5);
    expect(results.unigrams.length).toBeLessThanOrEqual(5);
  });

  it('supports language switching', () => {
    const analyzer = new NgramAnalyzer('en');
    expect(analyzer.getLanguage()).toBe('en');
    analyzer.setLanguage('es');
    expect(analyzer.getLanguage()).toBe('es');
  });

  it('clears data and resets counters', () => {
    const analyzer = new NgramAnalyzer('en');
    analyzer.addPage(
      'technical seo crawling performance optimization keywords rankings metrics analysis'
    );
    expect(analyzer.getResults().totalTokens).toBeGreaterThan(0);
    analyzer.clear();
    const results = analyzer.getResults();
    expect(results.unigrams.length).toBe(0);
    expect(results.bigrams.length).toBe(0);
    expect(results.trigrams.length).toBe(0);
    expect(results.totalPages).toBe(0);
    expect(results.totalTokens).toBe(0);
  });

  it('supports multilingual stopwords', () => {
    expect(SUPPORTED_LANGUAGES).toContain('en');
    expect(SUPPORTED_LANGUAGES).toContain('es');
    expect(SUPPORTED_LANGUAGES).toContain('fr');
    expect(SUPPORTED_LANGUAGES).toContain('de');
    // CJK languages have empty stopword lists and should not be in SUPPORTED_LANGUAGES
    expect(SUPPORTED_LANGUAGES).not.toContain('ja');
    expect(SUPPORTED_LANGUAGES).not.toContain('zh');
    expect(SUPPORTED_LANGUAGES).not.toContain('ko');
  });

  it('filters Spanish stopwords when language is es', () => {
    const analyzer = new NgramAnalyzer('es');
    analyzer.addPage(
      'optimización técnica posicionamiento rastreo análisis rendimiento optimización técnica posicionamiento'
    );
    const results = analyzer.getResults();
    const terms = results.unigrams.map(u => u.term);
    // Spanish stopwords like "de", "la", "el" should not appear
    expect(terms).not.toContain('de');
    expect(terms).not.toContain('la');
    expect(terms).not.toContain('el');
  });

  it('handles unicode/accented characters', () => {
    const analyzer = new NgramAnalyzer('fr');
    analyzer.addPage(
      'référencement naturel optimisation moteur recherche référencement naturel optimisation moteur'
    );
    const results = analyzer.getResults();
    const terms = results.unigrams.map(u => u.term);
    expect(terms).toContain('référencement');
    expect(terms).toContain('naturel');
  });

  it('filters pure numeric tokens', () => {
    const analyzer = new NgramAnalyzer('en');
    analyzer.addPage(
      'page 2024 results 100 percent optimization 2024 results 100 optimization percent'
    );
    const results = analyzer.getResults();
    const terms = results.unigrams.map(u => u.term);
    expect(terms).not.toContain('2024');
    expect(terms).not.toContain('100');
  });
});
