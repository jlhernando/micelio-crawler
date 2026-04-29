# Internal Link Intelligence -- Feature Specification

> **Phase**: Post-Phase 13 | **CLI Flag**: `--link-intelligence` | **Short alias**: `--li`
> **Effort**: High | **Impact**: Very High (unique differentiator)
> **Dependencies**: Existing PageRank, embeddings, anchor extraction infrastructure

---

## Executive Summary

Transform Micelio from a diagnostic crawler into a **prescriptive SEO tool** by adding advanced internal linking metrics and, critically, **automated internal linking suggestions**. No other CLI crawler offers actionable "add a link from A to B" recommendations. This feature alone positions Micelio as the first open-source crawler that tells you *what to fix*, not just *what's broken*.

---

## Architecture Overview

```
src/
  link-intelligence.ts    New file -- core engine (all graph algorithms + suggestion logic)
  link-intelligence.d.ts  Types for LinkIntelligenceData and LinkIntelligenceStats

Modified files:
  types.ts               Add LinkIntelligenceData to PageData, LinkIntelligenceStats to CrawlStats
  extractor.ts           Add link position classification (container detection)
  orchestrator.ts        Wire up link-intelligence post-crawl computation
  reporter.ts            Terminal report for link intelligence metrics
  html-report.ts         Dashboard panels for link intelligence
  writer.ts              CSV columns for link intelligence metrics
  cli.ts                 --link-intelligence / --li flag
  config.ts              linkIntelligence config field
```

---

## Step 1: Foundation Metrics (Low effort, High value)

### 1.1 -- Click Depth from Homepage

**What**: Minimum number of clicks from the homepage to reach each page. Different from crawl depth (which measures from the seed URL, which may not be the homepage).

**Algorithm**: BFS from the homepage URL through the internal link graph.

**Implementation**:

```typescript
// In link-intelligence.ts

export function computeClickDepth(
  pages: PageData[],
  homepageUrl: string,
): Map<string, number> {
  const urlIndex = new Map<string, number>();
  for (let i = 0; i < pages.length; i++) urlIndex.set(pages[i].url, i);

  // Build adjacency list (outlinks)
  const adj: number[][] = pages.map(p =>
    p.internalLinks
      .map(link => urlIndex.get(link))
      .filter((idx): idx is number => idx !== undefined)
  );

  // BFS from homepage
  const depth = new Map<string, number>();
  const homeIdx = urlIndex.get(homepageUrl);
  if (homeIdx === undefined) {
    // Fallback: try normalizing (strip trailing slash, try www/non-www)
    // If still not found, use first page at depth 0
    return depth;
  }

  const queue: number[] = [homeIdx];
  const visited = new Set<number>([homeIdx]);
  depth.set(pages[homeIdx].url, 0);
  let d = 0;

  while (queue.length > 0) {
    const nextQueue: number[] = [];
    d++;
    for (const idx of queue) {
      for (const neighbor of adj[idx]) {
        if (!visited.has(neighbor)) {
          visited.add(neighbor);
          depth.set(pages[neighbor].url, d);
          nextQueue.push(neighbor);
        }
      }
    }
    queue.length = 0;
    queue.push(...nextQueue);
  }

  return depth;
}
```

**Data model**:
```typescript
// Added to PageData (or as part of LinkIntelligenceData)
clickDepth: number | null;  // null = unreachable from homepage
```

**Reporting**:
- Pages with clickDepth > 3: warning "Deep page -- may have low crawl priority"
- Pages with clickDepth === null: error "Unreachable from homepage"
- Distribution chart: click depth histogram (similar to crawl depth chart)
- Issue panel: "Pages >3 clicks from homepage (N pages)"

**CSV columns**: `click_depth`

---

### 1.2 -- Near-Orphan Detection

**What**: Pages with In-Degree = 1 where the single linking page is itself deep (high click depth). These pages are technically linked but practically invisible.

**Algorithm**: Combine In-Degree count with Click Depth of the linking page.

**Implementation**:

```typescript
export interface NearOrphanInfo {
  url: string;
  inDegree: number;
  linkingSources: { url: string; clickDepth: number | null }[];
  worstSourceDepth: number | null;  // max click depth of linking pages
}

export function detectNearOrphans(
  pages: PageData[],
  clickDepths: Map<string, number>,
  threshold: { maxInDegree: number; minSourceDepth: number },
): NearOrphanInfo[] {
  // Build reverse link map (inlinks)
  const inlinks = new Map<string, string[]>();
  for (const page of pages) {
    for (const link of page.internalLinks) {
      if (!inlinks.has(link)) inlinks.set(link, []);
      inlinks.get(link)!.push(page.url);
    }
  }

  const nearOrphans: NearOrphanInfo[] = [];
  for (const page of pages) {
    const sources = inlinks.get(page.url) || [];
    if (sources.length > threshold.maxInDegree) continue;
    if (sources.length === 0) continue; // true orphans handled elsewhere

    const sourcesWithDepth = sources.map(s => ({
      url: s,
      clickDepth: clickDepths.get(s) ?? null,
    }));

    const worstDepth = Math.max(
      ...sourcesWithDepth.map(s => s.clickDepth ?? 999)
    );

    if (worstDepth >= threshold.minSourceDepth) {
      nearOrphans.push({
        url: page.url,
        inDegree: sources.length,
        linkingSources: sourcesWithDepth,
        worstSourceDepth: worstDepth,
      });
    }
  }

  return nearOrphans.sort((a, b) =>
    (b.worstSourceDepth ?? 999) - (a.worstSourceDepth ?? 999)
  );
}
```

**Default thresholds**: `maxInDegree: 2`, `minSourceDepth: 4`

**Reporting**:
- Issue panel: "Near-Orphan Pages (N)" -- pages with 1-2 inlinks all from deep pages
- Detail: shows the page, its inlink count, and the depth of its linking sources
- Severity: warning (orange)

**CSV columns**: `near_orphan` (boolean), `inlink_sources_max_depth`

---

### 1.3 -- Link Equity Dilution Score

**What**: Pages with excessive outgoing links dilute the equity passed per link. Flag pages with high Out-Degree.

**Algorithm**: Simple calculation: `dilutionFactor = 1 / outDegree`. Lower = more diluted.

**Implementation**:

```typescript
export interface LinkDilutionInfo {
  url: string;
  outDegree: number;
  dilutionFactor: number;  // 1/outDegree, lower = more diluted
  warning: 'excessive' | 'high' | 'normal';
}

export function computeLinkDilution(
  pages: PageData[],
): LinkDilutionInfo[] {
  return pages
    .filter(p => p.statusCode === 200)
    .map(p => {
      const outDegree = p.internalLinks.length;
      return {
        url: p.url,
        outDegree,
        dilutionFactor: outDegree > 0 ? Math.round((1 / outDegree) * 10000) / 10000 : 1,
        warning: outDegree > 200 ? 'excessive' : outDegree > 100 ? 'high' : 'normal',
      };
    })
    .filter(d => d.warning !== 'normal')
    .sort((a, b) => b.outDegree - a.outDegree);
}
```

**Reporting**:
- Issue panel: "Excessive Outgoing Links (>200)" (error severity)
- Issue panel: "High Outgoing Links (>100)" (warning severity)
- Detail: page URL, outDegree, dilution factor

**CSV columns**: `out_degree`, `link_dilution_factor`

---

### 1.4 -- Link Position Classification ("Reasonable Surfer")

**What**: Classify each internal link by its HTML container position. Links in `<main>` or `<article>` content pass more equity (per Google's Reasonable Surfer patent) than links in `<nav>`, `<footer>`, `<aside>`, or `<header>`.

**Algorithm**: During anchor extraction, detect the closest semantic parent element.

**Implementation** (modify `extractor.ts`):

```typescript
// New type for link position
export type LinkPosition = 'content' | 'navigation' | 'footer' | 'sidebar' | 'header' | 'other';

// Extended AnchorData
export interface AnchorData {
  href: string;
  text: string;
  isInternal: boolean;
  isNonDescriptive: boolean;
  rel: string | null;
  position: LinkPosition;  // NEW
}

// In extractAnchors(), add position detection:
function detectLinkPosition($: cheerio.CheerioAPI, el: cheerio.Element): LinkPosition {
  let current = el;
  while (current) {
    const parent = $(current).parent();
    if (!parent.length) break;
    const tagName = parent.prop('tagName')?.toLowerCase();
    const role = parent.attr('role')?.toLowerCase();

    // Check semantic elements
    if (tagName === 'main' || tagName === 'article' || role === 'main') return 'content';
    if (tagName === 'nav' || role === 'navigation') return 'navigation';
    if (tagName === 'footer' || role === 'contentinfo') return 'footer';
    if (tagName === 'aside' || role === 'complementary') return 'sidebar';
    if (tagName === 'header' || role === 'banner') return 'header';

    // Check common class/id patterns
    const classList = (parent.attr('class') || '').toLowerCase();
    const id = (parent.attr('id') || '').toLowerCase();
    const combined = classList + ' ' + id;

    if (/\b(nav|menu|navbar|navigation)\b/.test(combined)) return 'navigation';
    if (/\b(footer|foot)\b/.test(combined)) return 'footer';
    if (/\b(sidebar|aside|widget)\b/.test(combined)) return 'sidebar';
    if (/\b(header|masthead|top-bar)\b/.test(combined)) return 'header';
    if (/\b(content|article|post|entry|main|body-content)\b/.test(combined)) return 'content';

    current = parent.get(0)!;
  }
  return 'other';
}
```

**Data model** (added to AnchorData):
```typescript
position: LinkPosition;
```

**Reporting**:
- Summary stats: "Content links: 65% | Navigation: 20% | Footer: 10% | Sidebar: 5%"
- Pages where all internal links are in navigation/footer (no content links)
- Issue: "Pages with 0 content-area internal links" (warning)

**CSV columns**: `content_links_count`, `nav_links_count`, `footer_links_count`, `sidebar_links_count`

**Performance note**: The position detection walks up the DOM tree. For typical pages this adds ~1-2ms. The cheerio DOM is already loaded, so no extra parsing cost.

---

## Step 2: Graph Algorithms (Medium effort, High value)

### 2.1 -- HITS Hub/Authority Scores

**What**: The HITS (Hyperlink-Induced Topic Search) algorithm computes two scores per page:
- **Authority Score**: High when many good hubs link to it (important destination pages, "pillar" content)
- **Hub Score**: High when it links to many good authorities (navigation pages, resource lists)

Complements PageRank by providing a different signal about page importance.

**Algorithm**: Iterative mutual reinforcement (Kleinberg's algorithm).

**Implementation**:

```typescript
// In link-intelligence.ts

export interface HitsScores {
  hubScore: number;      // 0-10 normalized
  authorityScore: number; // 0-10 normalized
}

export function computeHits(
  pages: PageData[],
  maxIterations = 50,
  epsilon = 0.0001,
): Map<string, HitsScores> {
  const n = pages.length;
  if (n === 0) return new Map();

  const urlIndex = new Map<string, number>();
  for (let i = 0; i < n; i++) urlIndex.set(pages[i].url, i);

  // Build adjacency lists
  const outlinks: number[][] = new Array(n);
  const inlinks: number[][] = new Array(n);
  for (let i = 0; i < n; i++) {
    outlinks[i] = [];
    inlinks[i] = [];
  }

  for (let i = 0; i < n; i++) {
    const seen = new Set<number>();
    for (const link of pages[i].internalLinks) {
      const j = urlIndex.get(link);
      if (j !== undefined && j !== i && !seen.has(j)) {
        seen.add(j);
        outlinks[i].push(j);
        inlinks[j].push(i);
      }
    }
  }

  // Initialize scores
  let auth = new Float64Array(n).fill(1);
  let hub = new Float64Array(n).fill(1);

  for (let iter = 0; iter < maxIterations; iter++) {
    // Update authority scores: auth(i) = sum of hub(j) for all j linking to i
    const newAuth = new Float64Array(n);
    for (let i = 0; i < n; i++) {
      for (const j of inlinks[i]) {
        newAuth[i] += hub[j];
      }
    }

    // Update hub scores: hub(i) = sum of auth(j) for all j that i links to
    const newHub = new Float64Array(n);
    for (let i = 0; i < n; i++) {
      for (const j of outlinks[i]) {
        newHub[i] += auth[j];
      }
    }

    // Normalize
    let authNorm = 0, hubNorm = 0;
    for (let i = 0; i < n; i++) {
      authNorm += newAuth[i] * newAuth[i];
      hubNorm += newHub[i] * newHub[i];
    }
    authNorm = Math.sqrt(authNorm);
    hubNorm = Math.sqrt(hubNorm);

    if (authNorm > 0) for (let i = 0; i < n; i++) newAuth[i] /= authNorm;
    if (hubNorm > 0) for (let i = 0; i < n; i++) newHub[i] /= hubNorm;

    // Check convergence
    let delta = 0;
    for (let i = 0; i < n; i++) {
      delta += Math.abs(newAuth[i] - auth[i]) + Math.abs(newHub[i] - hub[i]);
    }

    auth = newAuth;
    hub = newHub;
    if (delta < epsilon) break;
  }

  // Normalize to 0-10 scale
  let maxAuth = 0, maxHub = 0;
  for (let i = 0; i < n; i++) {
    if (auth[i] > maxAuth) maxAuth = auth[i];
    if (hub[i] > maxHub) maxHub = hub[i];
  }

  const result = new Map<string, HitsScores>();
  for (let i = 0; i < n; i++) {
    result.set(pages[i].url, {
      authorityScore: maxAuth > 0 ? Math.round((auth[i] / maxAuth) * 1000) / 100 : 0,
      hubScore: maxHub > 0 ? Math.round((hub[i] / maxHub) * 1000) / 100 : 0,
    });
  }

  return result;
}
```

**Performance**: O(E * iterations) where E = total links. Same complexity as PageRank. Uses Float64Array for memory efficiency. Typically converges in 15-30 iterations.

**Reporting**:
- Top 10 Authority pages (pillar content)
- Top 10 Hub pages (navigation/resource pages)
- Pages with high authority but low hub score = strong content destinations
- Pages with high hub but low authority = good navigational pages
- Issue: "High-authority pages with low In-Degree" (missed opportunity)

**CSV columns**: `hits_authority`, `hits_hub`

---

### 2.2 -- Internal Linking Suggestions Engine (THE KILLER FEATURE)

**What**: Automatically suggest WHERE to add internal links based on multiple signals:
1. Semantic similarity between pages (leverages existing embedding infrastructure)
2. Pages with low In-Degree that deserve more links
3. Link position awareness (suggest adding to content, not nav)
4. Click depth reduction potential

**Algorithm**: Multi-signal scoring.

**Prerequisite**: Requires `--embeddings` to be enabled (for semantic matching). Without embeddings, falls back to URL-path-based heuristics only.

**Implementation**:

```typescript
// In link-intelligence.ts

export interface LinkSuggestion {
  sourceUrl: string;       // Page that should ADD a link
  targetUrl: string;       // Page that should RECEIVE the link
  score: number;           // 0-100 composite suggestion score
  reason: string;          // Human-readable explanation
  signals: {
    semanticSimilarity: number | null;  // 0-1 cosine similarity
    targetInDegree: number;             // current inlink count
    targetClickDepth: number | null;    // current click depth
    depthReduction: number | null;      // how much click depth would improve
    targetPageRank: number;             // target page value (higher = more worth linking)
    alreadyLinked: boolean;             // sanity check
  };
}

export function generateLinkSuggestions(
  pages: PageData[],
  embeddings: Map<string, number[]> | null,
  clickDepths: Map<string, number>,
  pageRanks: Map<string, number>,
  options: {
    maxSuggestions: number;           // default: 50
    minSemanticSimilarity: number;    // default: 0.3 (lower than cannibalization threshold)
    maxSemanticSimilarity: number;    // default: 0.85 (above this = cannibalization, not linking opportunity)
    targetMinInDegree: number;        // default: 3 (only suggest for pages with fewer inlinks)
    sourceMinWordCount: number;       // default: 200 (only suggest adding links to content pages)
  },
): LinkSuggestion[] {
  const urlIndex = new Map<string, number>();
  for (let i = 0; i < pages.length; i++) urlIndex.set(pages[i].url, i);

  // Build existing link set for fast lookup
  const existingLinks = new Set<string>();
  for (const page of pages) {
    for (const link of page.internalLinks) {
      existingLinks.add(`${page.url}|${link}`);
    }
  }

  // Build inlink counts
  const inlinkCount = new Map<string, number>();
  for (const page of pages) {
    for (const link of page.internalLinks) {
      inlinkCount.set(link, (inlinkCount.get(link) || 0) + 1);
    }
  }

  // Find candidate target pages (low In-Degree, indexable, 200 OK)
  const targetCandidates = pages.filter(p =>
    p.statusCode === 200 &&
    p.indexability.indexable &&
    (inlinkCount.get(p.url) || 0) <= options.targetMinInDegree
  );

  // Find candidate source pages (content pages with enough text)
  const sourceCandidates = pages.filter(p =>
    p.statusCode === 200 &&
    p.wordCount >= options.sourceMinWordCount
  );

  const suggestions: LinkSuggestion[] = [];

  for (const target of targetCandidates) {
    for (const source of sourceCandidates) {
      if (source.url === target.url) continue;
      if (existingLinks.has(`${source.url}|${target.url}`)) continue;

      // Compute semantic similarity if embeddings available
      let semanticSim: number | null = null;
      if (embeddings) {
        const srcVec = embeddings.get(source.url);
        const tgtVec = embeddings.get(target.url);
        if (srcVec && tgtVec) {
          semanticSim = cosineSimilarity(srcVec, tgtVec);
          // Skip if below minimum or above maximum (cannibalization range)
          if (semanticSim < options.minSemanticSimilarity) continue;
          if (semanticSim > options.maxSemanticSimilarity) continue;
        }
      }

      // Compute composite score
      const targetInDeg = inlinkCount.get(target.url) || 0;
      const targetPR = pageRanks.get(target.url) || 0;
      const targetCD = clickDepths.get(target.url) ?? null;
      const sourceCD = clickDepths.get(source.url) ?? null;

      // Depth reduction: if source is shallower, linking would reduce target's click depth
      const depthReduction = (targetCD !== null && sourceCD !== null)
        ? Math.max(0, targetCD - sourceCD - 1)
        : null;

      // Score components (all normalized to 0-25 range, total max 100):
      let score = 0;

      // 1. Semantic relevance (0-25 points) -- most important
      if (semanticSim !== null) {
        // Sweet spot: 0.4-0.75 is ideal (related but not duplicate)
        const idealRange = semanticSim >= 0.4 && semanticSim <= 0.75;
        score += idealRange ? semanticSim * 33 : semanticSim * 20;
      }

      // 2. Target needs links (0-25 points) -- lower inDegree = higher score
      const inDegScore = Math.max(0, 25 - (targetInDeg * 5));
      score += inDegScore;

      // 3. Target is valuable (0-25 points) -- higher PageRank = more worth linking
      score += Math.min(25, targetPR * 2.5);

      // 4. Depth improvement potential (0-25 points)
      if (depthReduction !== null && depthReduction > 0) {
        score += Math.min(25, depthReduction * 8);
      }

      if (score >= 20) {  // Minimum threshold
        suggestions.push({
          sourceUrl: source.url,
          targetUrl: target.url,
          score: Math.round(score * 10) / 10,
          reason: buildSuggestionReason(semanticSim, targetInDeg, targetCD, depthReduction, targetPR),
          signals: {
            semanticSimilarity: semanticSim,
            targetInDegree: targetInDeg,
            targetClickDepth: targetCD,
            depthReduction,
            targetPageRank: targetPR,
            alreadyLinked: false,
          },
        });
      }
    }
  }

  // Sort by score descending and cap
  suggestions.sort((a, b) => b.score - a.score);
  return suggestions.slice(0, options.maxSuggestions);
}

function buildSuggestionReason(
  similarity: number | null,
  inDegree: number,
  clickDepth: number | null,
  depthReduction: number | null,
  pageRank: number,
): string {
  const parts: string[] = [];
  if (similarity !== null && similarity >= 0.4) {
    parts.push(`topically related (${Math.round(similarity * 100)}% similarity)`);
  }
  if (inDegree <= 1) {
    parts.push(`target has only ${inDegree} inlink${inDegree === 1 ? '' : 's'}`);
  }
  if (depthReduction !== null && depthReduction >= 2) {
    parts.push(`would reduce click depth by ${depthReduction}`);
  }
  if (pageRank >= 5) {
    parts.push(`high-value target (PR ${pageRank})`);
  }
  return parts.join('; ') || 'potential linking opportunity';
}
```

**Performance considerations**:
- For N source pages and M target pages, this is O(N*M) for the pairwise check.
- Optimization: pre-filter targets aggressively (only low In-Degree pages). Typical sites have <10% of pages qualifying.
- For very large sites (>10k pages): limit source candidates to top 500 by PageRank, targets to bottom 200 by In-Degree.
- Cap at `--li-max-suggestions` (default 50).

**Reporting**:
- Issue panel: "Internal Linking Suggestions (N)" with score, source, target, reason
- Each suggestion shows: "Add link from /blog/seo-guide -> /services/seo-audit (score: 78)"
  - "Reason: topically related (62% similarity); target has 1 inlink; would reduce click depth by 3"
- HTML dashboard: sortable table of suggestions with all signals

**CSV output** (separate CSV file `*-link-suggestions.csv`):
```
source_url, target_url, score, semantic_similarity, target_in_degree, target_click_depth, depth_reduction, target_pagerank, reason
```

---

## Step 3: Advanced Graph Analysis (Medium effort, Medium value)

### 3.1 -- Betweenness Centrality

**What**: Measures how often a page lies on the shortest path between any two other pages. High betweenness = critical "bridge" page. If removed, it would disconnect parts of the site.

**Algorithm**: Brandes' algorithm, O(N*M) time, O(N+M) space.

**Performance safeguard**: For sites >5,000 pages, use sampling (random 500 source nodes) to approximate. For sites >20,000 pages, skip and warn.

**Implementation**:

```typescript
export function computeBetweennessCentrality(
  pages: PageData[],
  maxNodes = 5000,
): Map<string, number> {
  const n = pages.length;
  if (n === 0) return new Map();

  // For large sites, use sampling
  const useSampling = n > maxNodes;
  const sampleSize = useSampling ? Math.min(500, n) : n;

  const urlIndex = new Map<string, number>();
  for (let i = 0; i < n; i++) urlIndex.set(pages[i].url, i);

  // Build adjacency
  const adj: number[][] = pages.map(p => {
    const seen = new Set<number>();
    return p.internalLinks
      .map(link => urlIndex.get(link))
      .filter((idx): idx is number => idx !== undefined && !seen.has(idx) && (seen.add(idx), true));
  });

  const bc = new Float64Array(n); // betweenness centrality scores

  // Brandes' algorithm
  const sourceNodes = useSampling
    ? Array.from({ length: sampleSize }, () => Math.floor(Math.random() * n))
    : Array.from({ length: n }, (_, i) => i);

  for (const s of sourceNodes) {
    const stack: number[] = [];
    const pred: number[][] = new Array(n);
    for (let i = 0; i < n; i++) pred[i] = [];

    const sigma = new Float64Array(n); // # shortest paths through node
    sigma[s] = 1;

    const dist = new Int32Array(n).fill(-1);
    dist[s] = 0;

    const queue: number[] = [s];
    let qi = 0;

    // BFS
    while (qi < queue.length) {
      const v = queue[qi++];
      stack.push(v);
      for (const w of adj[v]) {
        if (dist[w] < 0) {
          dist[w] = dist[v] + 1;
          queue.push(w);
        }
        if (dist[w] === dist[v] + 1) {
          sigma[w] += sigma[v];
          pred[w].push(v);
        }
      }
    }

    // Back-propagation
    const delta = new Float64Array(n);
    while (stack.length > 0) {
      const w = stack.pop()!;
      for (const v of pred[w]) {
        delta[v] += (sigma[v] / sigma[w]) * (1 + delta[w]);
      }
      if (w !== s) {
        bc[w] += delta[w];
      }
    }
  }

  // If sampled, scale up
  if (useSampling) {
    const scale = n / sampleSize;
    for (let i = 0; i < n; i++) bc[i] *= scale;
  }

  // Normalize to 0-10 scale
  let maxBC = 0;
  for (let i = 0; i < n; i++) if (bc[i] > maxBC) maxBC = bc[i];

  const result = new Map<string, number>();
  for (let i = 0; i < n; i++) {
    result.set(pages[i].url, maxBC > 0 ? Math.round((bc[i] / maxBC) * 1000) / 100 : 0);
  }

  return result;
}
```

**Reporting**:
- Top 10 bridge pages (highest betweenness)
- Interpretation: "These pages are critical connectors. Breaking their links would isolate sections."
- Issue: "Single point of failure pages" (pages where betweenness > 8.0)

**CSV columns**: `betweenness_centrality`

---

### 3.2 -- Closeness Centrality

**What**: Average shortest-path distance from a page to all other pages. High closeness = page can reach all others quickly = well-connected.

**Algorithm**: BFS from each node. O(N * (N + M)).

**Performance safeguard**: Same sampling strategy as betweenness for large sites.

**Implementation**:

```typescript
export function computeClosenessCentrality(
  pages: PageData[],
  maxNodes = 5000,
): Map<string, number> {
  const n = pages.length;
  if (n === 0) return new Map();

  const useSampling = n > maxNodes;
  const urlIndex = new Map<string, number>();
  for (let i = 0; i < n; i++) urlIndex.set(pages[i].url, i);

  const adj: number[][] = pages.map(p =>
    p.internalLinks
      .map(link => urlIndex.get(link))
      .filter((idx): idx is number => idx !== undefined)
  );

  const closeness = new Map<string, number>();

  for (let s = 0; s < n; s++) {
    // BFS to compute shortest paths from s
    const dist = new Int32Array(n).fill(-1);
    dist[s] = 0;
    const queue: number[] = [s];
    let qi = 0;
    let totalDist = 0;
    let reachable = 0;

    while (qi < queue.length) {
      const v = queue[qi++];
      for (const w of adj[v]) {
        if (dist[w] < 0) {
          dist[w] = dist[v] + 1;
          totalDist += dist[w];
          reachable++;
          queue.push(w);
        }
      }
    }

    // Closeness = reachable / totalDist (Wasserman-Faust normalization)
    const cc = reachable > 0 ? reachable / totalDist : 0;
    closeness.set(pages[s].url, Math.round(cc * 10000) / 10000);
  }

  // Normalize to 0-10 scale
  let maxCC = 0;
  for (const v of closeness.values()) if (v > maxCC) maxCC = v;

  for (const [url, v] of closeness) {
    closeness.set(url, maxCC > 0 ? Math.round((v / maxCC) * 1000) / 100 : 0);
  }

  return closeness;
}
```

**Reporting**:
- Top 10 most connected pages (highest closeness)
- Bottom 10 most isolated pages (lowest closeness, excluding orphans)

**CSV columns**: `closeness_centrality`

---

### 3.3 -- Semantic Link Distance

**What**: For each internal link, measure the topical relevance between source and target using embeddings. Flag links between topically unrelated pages.

**Prerequisite**: Requires `--embeddings`.

**Implementation**:

```typescript
export interface SemanticLinkAnalysis {
  totalLinks: number;
  avgSemDistance: number;
  weakLinks: { source: string; target: string; similarity: number }[];
  strongLinks: { source: string; target: string; similarity: number }[];
}

export function analyzeSemanticLinkDistance(
  pages: PageData[],
  embeddings: Map<string, number[]>,
  weakThreshold = 0.15,  // Below this = topically unrelated
  strongThreshold = 0.6, // Above this = strong topical match
): SemanticLinkAnalysis {
  let totalLinks = 0;
  let totalSim = 0;
  const weakLinks: { source: string; target: string; similarity: number }[] = [];
  const strongLinks: { source: string; target: string; similarity: number }[] = [];

  for (const page of pages) {
    const srcVec = embeddings.get(page.url);
    if (!srcVec) continue;

    for (const link of page.internalLinks) {
      const tgtVec = embeddings.get(link);
      if (!tgtVec) continue;

      const sim = cosineSimilarity(srcVec, tgtVec);
      totalLinks++;
      totalSim += sim;

      if (sim < weakThreshold) {
        weakLinks.push({ source: page.url, target: link, similarity: Math.round(sim * 1000) / 1000 });
      } else if (sim > strongThreshold) {
        strongLinks.push({ source: page.url, target: link, similarity: Math.round(sim * 1000) / 1000 });
      }
    }
  }

  // Sort weak links by similarity ascending (weakest first)
  weakLinks.sort((a, b) => a.similarity - b.similarity);
  // Sort strong links by similarity descending
  strongLinks.sort((a, b) => b.similarity - a.similarity);

  return {
    totalLinks,
    avgSemDistance: totalLinks > 0 ? Math.round((totalSim / totalLinks) * 1000) / 1000 : 0,
    weakLinks: weakLinks.slice(0, 50),
    strongLinks: strongLinks.slice(0, 50),
  };
}
```

**Reporting**:
- "Average semantic link relevance: 0.42"
- Issue panel: "Topically Unrelated Links (N)" -- links between pages with <15% similarity
- Info panel: "Strong Topical Links (N)" -- links between pages with >60% similarity

---

## Data Model Changes

### PageData additions (types.ts)

```typescript
// Add to PageData interface:
linkIntelligence: LinkIntelligenceData | null;

// New interface:
export interface LinkIntelligenceData {
  clickDepth: number | null;      // null = unreachable from homepage
  inDegree: number;               // total unique internal inlinks
  outDegree: number;              // total unique internal outlinks
  isNearOrphan: boolean;
  hubScore: number;               // HITS hub score (0-10)
  authorityScore: number;         // HITS authority score (0-10)
  betweennessCentrality: number;  // 0-10 normalized
  closenessCentrality: number;    // 0-10 normalized
  linkDilutionFactor: number;     // 1/outDegree
  contentLinksCount: number;      // links from content area
  navLinksCount: number;          // links from navigation
  footerLinksCount: number;       // links from footer
  sidebarLinksCount: number;      // links from sidebar
}
```

### CrawlStats additions (types.ts)

```typescript
// Add to CrawlStats interface:
linkIntelligenceStats: {
  avgClickDepth: number;
  maxClickDepth: number;
  unreachablePages: string[];
  nearOrphans: NearOrphanInfo[];
  dilutionWarnings: LinkDilutionInfo[];
  topAuthorities: { url: string; score: number }[];
  topHubs: { url: string; score: number }[];
  topBridges: { url: string; score: number }[];     // betweenness
  mostConnected: { url: string; score: number }[];   // closeness
  linkSuggestions: LinkSuggestion[];
  semanticLinkAnalysis: SemanticLinkAnalysis | null;
  linkPositionDistribution: Record<LinkPosition, number>;
} | null;
```

---

## CLI Options

```
--link-intelligence, --li     Enable internal link intelligence analysis
--li-max-suggestions <n>      Max linking suggestions (default: 50)
--li-no-centrality            Skip centrality metrics (faster for large sites)
```

The `--link-intelligence` flag triggers ALL Step 1 and Step 2 features. Step 3 (centrality) runs unless `--li-no-centrality` is specified or the site has >20,000 pages.

Link suggestions require `--embeddings` to be enabled. Without it, only graph-based features (HITS, centrality, click depth, etc.) are computed.

---

## Execution Order in orchestrator.ts

Link intelligence runs as a post-crawl computation (like PageRank, embeddings):

```
1. Crawl completes -> pages[] available
2. computePageRank(pages)           -- existing
3. computeEmbeddings(pages)         -- existing (if --embeddings)
4. computeLinkIntelligence(pages, embeddings, pageRanks)  -- NEW
   4a. computeClickDepth(pages, homepageUrl)
   4b. computeHits(pages)
   4c. computeLinkDilution(pages)
   4d. detectNearOrphans(pages, clickDepths)
   4e. computeBetweennessCentrality(pages)    -- if not skipped
   4f. computeClosenessCentrality(pages)      -- if not skipped
   4g. generateLinkSuggestions(pages, embeddings, clickDepths, pageRanks)
   4h. analyzeSemanticLinkDistance(pages, embeddings)  -- if embeddings available
5. generateReport(pages) with linkIntelligenceStats
6. generateHtmlReport(pages, stats)
```

The link position classification (Step 1.4) runs during extraction (in `extractor.ts`), not post-crawl.

---

## HTML Dashboard Additions

### New Tab: "Link Intelligence"

Placed after the existing tabs (or as sub-section of the main dashboard):

1. **Click Depth Distribution** -- bar chart showing pages per click depth level
2. **HITS Scores** -- dual-axis chart (hub vs authority) for top pages
3. **Link Suggestions Table** -- sortable table with: score, source URL, target URL, similarity, target In-Degree, reason
4. **Link Position Breakdown** -- pie/donut chart: content/nav/footer/sidebar/other
5. **Bridge Pages** -- top betweenness centrality pages (table)

### New Issue Panels

| Panel | Severity | Content |
|-------|----------|---------|
| Pages >3 clicks from homepage | warning | URL + click depth |
| Unreachable from homepage | error | URL list |
| Near-orphan pages | warning | URL + inDegree + source depths |
| Excessive outgoing links (>200) | error | URL + outDegree |
| High outgoing links (>100) | warning | URL + outDegree |
| Pages with 0 content-area links | warning | URL (all links in nav/footer) |
| Internal linking suggestions | info | Source -> Target (score, reason) |
| Topically unrelated links | info | Source -> Target (similarity) |

---

## CSV Columns Added

```
click_depth, in_degree, out_degree, is_near_orphan,
hits_authority, hits_hub,
betweenness_centrality, closeness_centrality,
link_dilution_factor,
content_links, nav_links, footer_links, sidebar_links
```

Plus separate file `*-link-suggestions.csv` for suggestions.

---

## Performance Budget

| Feature | Complexity | 1K pages | 10K pages | 50K pages |
|---------|-----------|----------|-----------|-----------|
| Click Depth (BFS) | O(N+E) | <10ms | <100ms | <500ms |
| Near-Orphan | O(N) | <5ms | <50ms | <200ms |
| Link Dilution | O(N) | <5ms | <20ms | <100ms |
| Link Position | O(A) per page | In extraction (0 extra) | In extraction | In extraction |
| HITS | O(E*iter) | <50ms | <500ms | <2s |
| Betweenness | O(N*M) | <200ms | <30s | Skip (warn) |
| Closeness | O(N*(N+M)) | <500ms | <60s | Skip (warn) |
| Link Suggestions | O(S*T) | <100ms | <2s | <10s (capped) |
| Semantic Distance | O(E) | <50ms | <500ms | <2s |

Total for 1K pages: <1 second. For 10K: <~90 seconds. For 50K: centrality skipped, rest <15 seconds.

---

## Implementation Order

1. **Step 1.4** (Link Position) -- modify `extractor.ts` first since it runs during crawl
2. **Step 1.1** (Click Depth) -- foundation for near-orphans and suggestions
3. **Step 1.2** (Near-Orphans) -- depends on click depth
4. **Step 1.3** (Link Dilution) -- independent, quick win
5. **Step 2.1** (HITS) -- independent graph algorithm
6. **Step 2.2** (Link Suggestions) -- depends on everything above + embeddings
7. **Step 3.1** (Betweenness) -- independent but expensive
8. **Step 3.2** (Closeness) -- independent but expensive
9. **Step 3.3** (Semantic Distance) -- depends on embeddings

Steps 1-4 can ship as a first PR. Steps 5-6 as a second PR. Steps 7-9 as a third PR.

---

## Testing Strategy

1. **Unit tests** for each algorithm with known small graphs
2. **Snapshot tests** for reporter/HTML output
3. **Performance benchmark** with 1K, 5K, 10K page synthetic datasets
4. **Integration test**: crawl a real site with `--link-intelligence --embeddings` and verify output

---

## Example Output

```
Internal Link Intelligence
======================================

  Click Depth Distribution:
    Depth 0:     1 page  (homepage)
    Depth 1:    23 pages
    Depth 2:    89 pages
    Depth 3:   156 pages
    Depth 4+:   31 pages  ⚠️
    Unreachable: 4 pages  ❌

  HITS Analysis:
    Top Authorities (pillar content):
      1. /services/seo-audit         Authority: 9.45  Hub: 1.2
      2. /blog/complete-seo-guide    Authority: 8.72  Hub: 2.1
      3. /pricing                    Authority: 7.91  Hub: 0.5

    Top Hubs (navigation/resource pages):
      1. /blog                       Hub: 9.80  Authority: 3.1
      2. /resources                  Hub: 8.45  Authority: 2.3
      3. /sitemap                    Hub: 7.90  Authority: 0.8

  Link Position Breakdown:
    Content:    2,340 links (45%)
    Navigation: 1,560 links (30%)
    Footer:       780 links (15%)
    Sidebar:      312 links (6%)
    Other:        208 links (4%)

  🔗 Internal Linking Suggestions (top 10):

    1. Score: 82  /blog/seo-guide → /services/seo-audit
       ↳ topically related (62% similarity); target has 1 inlink; would reduce click depth by 3

    2. Score: 75  /blog/content-marketing → /blog/keyword-research
       ↳ topically related (58% similarity); target has 0 inlinks; high-value target (PR 6.2)

    3. Score: 71  /case-studies/client-a → /services/web-design
       ↳ topically related (54% similarity); target has 2 inlinks; would reduce click depth by 2
```

---

## Summary

| Step | Feature | Effort | Value | Dependencies |
|------|---------|--------|-------|--------------|
| 1.1 | Click Depth | Low | High | None |
| 1.2 | Near-Orphans | Low | High | Click Depth |
| 1.3 | Link Dilution | Low | Medium | None |
| 1.4 | Link Position | Medium | High | Cheerio (existing) |
| 2.1 | HITS Hub/Authority | Medium | High | PageRank graph (existing) |
| 2.2 | **Link Suggestions** | Medium-High | **Very High** | Embeddings, Click Depth, PageRank |
| 3.1 | Betweenness Centrality | Medium | Medium | None |
| 3.2 | Closeness Centrality | Medium | Medium | None |
| 3.3 | Semantic Link Distance | Low | Medium | Embeddings |

**Total estimated effort**: 3-5 days for a developer familiar with the codebase.
**Total new/modified files**: 3 new (link-intelligence.ts, types, tests), 7 modified.
**External dependencies**: None (all algorithms implemented in-house using typed arrays for performance).
