import { createHash } from 'node:crypto';

/**
 * SimHash implementation for near-duplicate text detection.
 * Produces a 64-bit fingerprint from text content.
 * Near-duplicate texts will have fingerprints with small Hamming distance.
 */

// Generate a 64-bit SimHash fingerprint for a text
export function simhash(text: string): bigint {
  if (!text) return 0n;

  // Tokenize into shingles (3-word n-grams for better accuracy)
  const words = text.toLowerCase().split(/\s+/).filter((w) => w.length > 0);
  if (words.length < 5) return hashToken(text);

  const shingles: string[] = [];
  for (let i = 0; i <= words.length - 3; i++) {
    shingles.push(words.slice(i, i + 3).join(' '));
  }

  // Compute weighted bit vector
  const v = new Float64Array(64);

  for (const shingle of shingles) {
    const hash = hashToken(shingle);
    for (let i = 0; i < 64; i++) {
      if ((hash >> BigInt(i)) & 1n) {
        v[i] += 1;
      } else {
        v[i] -= 1;
      }
    }
  }

  // Reduce to 64-bit fingerprint
  let fingerprint = 0n;
  for (let i = 0; i < 64; i++) {
    if (v[i] > 0) {
      fingerprint |= 1n << BigInt(i);
    }
  }

  return fingerprint;
}

// Hash a token string to a 64-bit value
function hashToken(token: string): bigint {
  const md5 = createHash('md5').update(token).digest('hex');
  // Use first 16 hex chars (64 bits)
  return BigInt('0x' + md5.substring(0, 16));
}

// Compute Hamming distance between two 64-bit fingerprints
export function hammingDistance(a: bigint, b: bigint): number {
  // Mask to 64 bits to handle sign bit edge case
  let xor = (a ^ b) & ((1n << 64n) - 1n);
  let distance = 0;
  while (xor !== 0n) {
    distance += Number(xor & 1n);
    xor >>= 1n;
  }
  return distance;
}

// Compute similarity (0-100%) from Hamming distance
export function similarity(a: bigint, b: bigint): number {
  const dist = hammingDistance(a, b);
  return Math.round(((64 - dist) / 64) * 100);
}

// Find groups of near-duplicate fingerprints.
// Uses greedy grouping: each group is built relative to a seed URL.
// URLs within a group are all similar to the seed, but may not be similar to each other.
// The reported similarity is the minimum similarity to the seed, not between all pairs.
// Complexity: O(n^2) — suitable for up to a few thousand items. For larger sets,
// consider bit-sampling or LSH-based approaches.
// threshold: minimum similarity percentage (default 90% = max 6 bits different)
export function findNearDuplicates(
  items: { url: string; fingerprint: bigint }[],
  threshold = 90,
): { urls: string[]; similarity: number }[] {
  const maxDistance = Math.floor(64 * (1 - threshold / 100));
  const groups: { urls: Set<string>; similarity: number }[] = [];
  const assigned = new Set<string>();

  for (let i = 0; i < items.length; i++) {
    if (assigned.has(items[i].url)) continue;

    const group = new Set<string>();
    let minSimilarity = 100;

    for (let j = i + 1; j < items.length; j++) {
      if (assigned.has(items[j].url)) continue;

      const dist = hammingDistance(items[i].fingerprint, items[j].fingerprint);
      if (dist <= maxDistance) {
        if (group.size === 0) {
          group.add(items[i].url);
        }
        group.add(items[j].url);
        const sim = Math.round(((64 - dist) / 64) * 100);
        if (sim < minSimilarity) minSimilarity = sim;
      }
    }

    if (group.size >= 2) {
      for (const url of group) {
        assigned.add(url);
      }
      groups.push({ urls: group, similarity: minSimilarity });
    }
  }

  return groups.map((g) => ({ urls: [...g.urls], similarity: g.similarity }));
}
