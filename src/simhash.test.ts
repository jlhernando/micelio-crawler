import { describe, it, expect } from 'vitest';
import { simhash, hammingDistance, similarity, findNearDuplicates } from './simhash.js';

describe('simhash', () => {
  it('returns 0n for empty text', () => {
    expect(simhash('')).toBe(0n);
  });

  it('returns a bigint fingerprint', () => {
    const fp = simhash('the quick brown fox jumps over the lazy dog');
    expect(typeof fp).toBe('bigint');
    expect(fp).not.toBe(0n);
  });

  it('produces identical fingerprints for identical text', () => {
    const text = 'this is a test document about search engine optimization and technical seo';
    expect(simhash(text)).toBe(simhash(text));
  });

  it('produces similar fingerprints for similar text', () => {
    const text1 = 'this is a comprehensive guide about search engine optimization and best practices for seo';
    const text2 = 'this is a comprehensive guide about search engine optimization and best techniques for seo';
    const dist = hammingDistance(simhash(text1), simhash(text2));
    expect(dist).toBeLessThan(20); // Similar texts → lower distance than random
  });

  it('produces different fingerprints for very different text', () => {
    const text1 = 'the quick brown fox jumps over the lazy dog near the river bank today';
    const text2 = 'blockchain cryptocurrency decentralized finance smart contracts ethereum solidity development';
    const dist = hammingDistance(simhash(text1), simhash(text2));
    expect(dist).toBeGreaterThan(5);
  });

  it('handles short text (fewer than 5 words)', () => {
    const fp = simhash('hello world');
    expect(typeof fp).toBe('bigint');
    expect(fp).not.toBe(0n);
  });
});

describe('hammingDistance', () => {
  it('returns 0 for identical values', () => {
    expect(hammingDistance(0n, 0n)).toBe(0);
    expect(hammingDistance(123n, 123n)).toBe(0);
  });

  it('counts differing bits', () => {
    // 0b01 vs 0b10 → 2 bits differ
    expect(hammingDistance(1n, 2n)).toBe(2);
    // 0b111 vs 0b000 → 3 bits differ
    expect(hammingDistance(7n, 0n)).toBe(3);
  });

  it('handles large bigint values', () => {
    const a = (1n << 63n);
    const b = 0n;
    const dist = hammingDistance(a, b);
    expect(dist).toBe(1);
  });

  it('is commutative', () => {
    const a = 42n;
    const b = 100n;
    expect(hammingDistance(a, b)).toBe(hammingDistance(b, a));
  });
});

describe('similarity', () => {
  it('returns 100 for identical fingerprints', () => {
    expect(similarity(123n, 123n)).toBe(100);
  });

  it('returns 0 for maximally different fingerprints', () => {
    // All 64 bits different
    const a = 0n;
    const b = (1n << 64n) - 1n;
    expect(similarity(a, b)).toBe(0);
  });

  it('returns values between 0 and 100', () => {
    const s = similarity(42n, 100n);
    expect(s).toBeGreaterThanOrEqual(0);
    expect(s).toBeLessThanOrEqual(100);
  });
});

describe('findNearDuplicates', () => {
  it('returns empty array when no duplicates', () => {
    const items = [
      { url: 'a', fingerprint: 0n },
      { url: 'b', fingerprint: (1n << 64n) - 1n }, // maximally different
    ];
    const groups = findNearDuplicates(items, 90);
    expect(groups).toEqual([]);
  });

  it('groups identical fingerprints', () => {
    const items = [
      { url: 'a', fingerprint: 123n },
      { url: 'b', fingerprint: 123n },
      { url: 'c', fingerprint: 123n },
    ];
    const groups = findNearDuplicates(items, 90);
    expect(groups.length).toBe(1);
    expect(groups[0].urls).toContain('a');
    expect(groups[0].urls).toContain('b');
    expect(groups[0].urls).toContain('c');
    expect(groups[0].similarity).toBe(100);
  });

  it('groups similar fingerprints within threshold', () => {
    // 1 bit different → 98.4% similarity
    const items = [
      { url: 'a', fingerprint: 0n },
      { url: 'b', fingerprint: 1n }, // 1 bit different
    ];
    const groups = findNearDuplicates(items, 90);
    expect(groups.length).toBe(1);
    expect(groups[0].similarity).toBeGreaterThanOrEqual(90);
  });

  it('does not group items below threshold', () => {
    // 10 bits different → 84.4% similarity
    const fp1 = 0n;
    const fp2 = (1n << 10n) - 1n; // 10 bits set
    const items = [
      { url: 'a', fingerprint: fp1 },
      { url: 'b', fingerprint: fp2 },
    ];
    const groups = findNearDuplicates(items, 90);
    expect(groups).toEqual([]);
  });

  it('creates multiple groups', () => {
    // Use maximally different fingerprints so they can't be grouped together
    const items = [
      { url: 'a1', fingerprint: 0n },
      { url: 'a2', fingerprint: 0n },
      { url: 'b1', fingerprint: (1n << 64n) - 1n }, // all bits set
      { url: 'b2', fingerprint: (1n << 64n) - 1n },
    ];
    const groups = findNearDuplicates(items, 90);
    expect(groups.length).toBe(2);
  });

  it('handles single item (no group possible)', () => {
    const items = [{ url: 'a', fingerprint: 123n }];
    const groups = findNearDuplicates(items, 90);
    expect(groups).toEqual([]);
  });
});
