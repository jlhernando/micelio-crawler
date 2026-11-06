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
//
// Candidates come from bands (pigeonhole): the 64 bits are cut into
// maxDistance + 1 bands, and two fingerprints at most maxDistance bits apart
// must be equal in at least one band. Only items sharing a band value are
// compared, so a 100k-page crawl no longer does ~5 billion comparisons on
// the event loop. The groups are exactly those of the all-pairs version.
// threshold: minimum similarity percentage (default 90% = max 6 bits different)
export function findNearDuplicates(
  items: { url: string; fingerprint: bigint }[],
  threshold = 90,
): { urls: string[]; similarity: number }[] {
  const maxDistance = Math.floor(64 * (1 - threshold / 100));
  const n = items.length;
  if (n < 2 || maxDistance < 0) return [];

  // 64-bit fingerprints as two 32-bit halves: popcount without BigInt
  const hi = new Uint32Array(n);
  const lo = new Uint32Array(n);
  for (let i = 0; i < n; i++) {
    const f = BigInt.asUintN(64, items[i].fingerprint);
    hi[i] = Number(f >> 32n);
    lo[i] = Number(f & 0xffffffffn);
  }
  const distance = (a: number, b: number) => popcount32(hi[a] ^ hi[b]) + popcount32(lo[a] ^ lo[b]);

  // Bands narrower than 4 bits filter nothing: compare all pairs then
  const bandCount = maxDistance + 1;
  const buckets: Map<number, number[]>[] = [];
  const bandOf: number[][] = [];
  if (bandCount <= 16) {
    for (let b = 0; b < bandCount; b++) {
      const start = Math.floor((b * 64) / bandCount);
      const end = Math.floor(((b + 1) * 64) / bandCount);
      const mask = (1n << BigInt(end - start)) - 1n;
      const map = new Map<number, number[]>();
      const values: number[] = new Array(n);
      for (let i = 0; i < n; i++) {
        const v = Number((BigInt.asUintN(64, items[i].fingerprint) >> BigInt(start)) & mask);
        values[i] = v;
        const list = map.get(v);
        if (list) list.push(i); else map.set(v, [i]);
      }
      buckets.push(map);
      bandOf.push(values);
    }
  }

  // "Grouped" is tracked per URL (as before), via a numeric id per URL
  const urlIds = new Map<string, number>();
  const urlId = new Int32Array(n);
  for (let i = 0; i < n; i++) {
    let id = urlIds.get(items[i].url);
    if (id === undefined) { id = urlIds.size; urlIds.set(items[i].url, id); }
    urlId[i] = id;
  }
  const assigned = new Uint8Array(urlIds.size);
  const seenFor = new Int32Array(n).fill(-1);
  const groups: { urls: Set<string>; similarity: number }[] = [];

  for (let i = 0; i < n; i++) {
    if (assigned[urlId[i]]) continue;

    // Later items (j > i) within maxDistance, found through the bands
    const matches: { j: number; dist: number }[] = [];
    const consider = (j: number) => {
      if (assigned[urlId[j]]) return;
      const dist = distance(i, j);
      if (dist <= maxDistance) matches.push({ j, dist });
    };
    if (buckets.length === 0) {
      for (let j = i + 1; j < n; j++) consider(j);
    } else {
      for (let b = 0; b < buckets.length; b++) {
        const list = buckets[b].get(bandOf[b][i])!;
        // Drop entries that can never be a candidate again (already passed
        // or grouped), so a crowded bucket is not walked again and again
        let keep = 0;
        for (let k = 0; k < list.length; k++) {
          const j = list[k];
          if (j <= i || assigned[urlId[j]]) continue;
          list[keep++] = j;
          if (seenFor[j] !== i) {
            seenFor[j] = i;
            consider(j);
          }
        }
        list.length = keep;
      }
      matches.sort((a, b) => a.j - b.j);
    }
    if (matches.length === 0) continue;

    const group = new Set<string>([items[i].url]);
    let minSimilarity = 100;
    for (const { j, dist } of matches) {
      group.add(items[j].url);
      const sim = Math.round(((64 - dist) / 64) * 100);
      if (sim < minSimilarity) minSimilarity = sim;
    }

    if (group.size >= 2) {
      for (const url of group) {
        assigned[urlIds.get(url)!] = 1;
      }
      groups.push({ urls: group, similarity: minSimilarity });
    }
  }

  return groups.map((g) => ({ urls: [...g.urls], similarity: g.similarity }));
}

function popcount32(x: number): number {
  x -= (x >>> 1) & 0x55555555;
  x = (x & 0x33333333) + ((x >>> 2) & 0x33333333);
  x = (x + (x >>> 4)) & 0x0f0f0f0f;
  return Math.imul(x, 0x01010101) >>> 24;
}
