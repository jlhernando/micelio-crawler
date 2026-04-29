<script lang="ts">
  let { stats, pageCount }: { stats: Record<string, unknown>; pageCount: number } = $props();

  // Stat accessors
  let idx = $derived(stats.indexabilityStats as { indexable: number; nonIndexable: number } | null);
  let orphans = $derived((stats.orphanPages as string[]) || []);
  let redirects = $derived(stats.redirectStats as { navBoostAtRisk?: number } | null);
  let topo = $derived(stats.topicalityStats as { avgTitleBodyOverlap: number; avgTitleH1Alignment: number; avgTopicalConsistency: number; lowAlignmentPages: number } | null);
  let read = $derived(stats.readabilityStats as { avgScore: number; qualityTier1: number; qualityTier2: number; qualityTier3: number } | null);
  let rich = $derived(stats.contentRichnessStats as { avgRichnessScore: number; lowRichnessPages: number } | null);
  let eeat = $derived(stats.eeatStats as { authorCoverage: number; pagesWithAuthor: number; hasAboutPage: boolean; hasContactPage: boolean; hasEditorialPolicy: boolean } | null);
  let li = $derived(stats.linkIntelligenceStats as { avgClickDepth: number; nearOrphansCount: number; dilutionWarningsCount: number } | null);
  let anchor = $derived(stats.anchorHealthStats as { avgInboundDiversity: number; pagesOverOptimized: number } | null);
  let passage = $derived(stats.passageReadinessStats as { avgPassageScore: number; pagesWithFaq: number; pagesWithHowTo: number } | null);
  let ai = $derived(stats.aiReadinessStats as { avgAiReadinessScore: number; pagesWithStructuredAnswer: number } | null);
  let fresh = $derived(stats.freshnessStats as { pagesWithDate: number; pagesWithoutDate: number; inconsistentDates: number } | null);
  let funnel = $derived(stats.seoFunnelStats as { crawled: number; indexable: number; visible: number; active: number } | null);
  let broken = $derived((stats.brokenLinks as unknown[]) || []);

  function pct(n: number, d: number): string { return d > 0 ? (n / d * 100).toFixed(1) + '%' : '0%'; }
  function fmt(n: number, decimals = 2): string { return n.toFixed(decimals); }
</script>

<div class="space-y-4">

  <!-- 1. Crawl & Index -->
  <div class="rounded-md border border-border bg-surface-2 p-5">
    <h3 class="text-sm font-semibold text-accent mb-1">Crawl & Index (Trawler / Alexandria)</h3>
    <p class="text-[10px] text-fg-2 mb-4">Google's Trawler crawls and Alexandria indexes pages. Only indexed pages can rank. Crawl budget wasted on non-indexable URLs reduces the rate at which Googlebot discovers and refreshes your content. <span class="text-fg-2/60">Rankpedia: doj:trawler, doj:caffeine-indexing</span></p>
    <div class="grid gap-3 grid-cols-2 md:grid-cols-4">
      {#if idx}
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Index Rate</div>
          <div class="text-xl font-bold text-success mt-1">{pct(idx.indexable, pageCount)}</div>
          <div class="text-[10px] text-fg-2">{idx.indexable.toLocaleString()} of {pageCount.toLocaleString()} pages indexable (no noindex, canonical self-referencing, 200 status)</div>
          <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
            <div class="h-full rounded bg-success" style="width: {(idx.indexable / Math.max(pageCount, 1)) * 100}%"></div>
          </div>
        </div>
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Crawl Waste</div>
          <div class="text-xl font-bold {idx.nonIndexable + broken.length > 0 ? 'text-warning' : 'text-success'} mt-1">{pct(idx.nonIndexable + broken.length, pageCount)}</div>
          <div class="text-[10px] text-fg-2">{(idx.nonIndexable + broken.length).toLocaleString()} URLs consuming crawl budget without entering the index (redirects, 4xx/5xx, noindex, duplicate)</div>
        </div>
      {/if}
      <div class="rounded bg-surface-3 p-3">
        <div class="text-[10px] text-fg-2 uppercase tracking-wide">Orphan Pages</div>
        <div class="text-xl font-bold {orphans.length > 0 ? 'text-warning' : 'text-success'} mt-1">{orphans.length.toLocaleString()}</div>
        <div class="text-[10px] text-fg-2">Pages with no internal link path from seed. Googlebot relies on links for discovery; orphans depend entirely on sitemaps or external links</div>
      </div>
      {#if redirects}
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">NavBoost at Risk</div>
          <div class="text-xl font-bold {(redirects.navBoostAtRisk ?? 0) > 0 ? 'text-warning' : 'text-success'} mt-1">{(redirects.navBoostAtRisk ?? 0).toLocaleString()}</div>
          <div class="text-[10px] text-fg-2">Pages behind 302/307 redirects. NavBoost click signals accumulate on the redirect URL instead of the destination, diluting ranking power. <span class="text-fg-2/60">Rankpedia: doj:navboost</span></div>
        </div>
      {/if}
    </div>
  </div>

  <!-- 2. Relevance (T*) -->
  {#if topo}
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-semibold text-accent mb-1">Relevance (T* Topicality)</h3>
      <p class="text-[10px] text-fg-2 mb-4">T* is Google's query-dependent topicality score combining three sub-signals: Anchors (A) what the web says about you, Body (B) what your page says about itself, and Clicks (C) what users say about you. Our proxy measures the B component (on-page alignment). Confirmed by Pandu Nayak (sworn testimony). <span class="text-fg-2/60">Rankpedia: doj:t-star, doj:abc-body, leak:Ascorer</span></p>
      <div class="grid gap-3 grid-cols-2 md:grid-cols-4">
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Title-Body Overlap</div>
          <div class="text-xl font-bold mt-1">{fmt(topo.avgTitleBodyOverlap)}</div>
          <div class="text-[10px] text-fg-2">Fraction of title terms found in body text. Proxy for titlematchScore: the higher, the more Google trusts the title reflects the content</div>
          <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
            <div class="h-full rounded bg-accent" style="width: {topo.avgTitleBodyOverlap * 100}%"></div>
          </div>
        </div>
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Title-H1 Alignment</div>
          <div class="text-xl font-bold mt-1">{fmt(topo.avgTitleH1Alignment)}</div>
          <div class="text-[10px] text-fg-2">Jaccard similarity between title tag and H1. Misalignment signals inconsistent topic targeting, weakening the Body (B) sub-signal</div>
          <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
            <div class="h-full rounded bg-accent" style="width: {topo.avgTitleH1Alignment * 100}%"></div>
          </div>
        </div>
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Topical Consistency</div>
          <div class="text-xl font-bold mt-1">{fmt(topo.avgTopicalConsistency)}</div>
          <div class="text-[10px] text-fg-2">Weighted composite of title-body, title-H1, and heading-body coverage. Proxy for the overall T* Body sub-signal strength</div>
          <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
            <div class="h-full rounded bg-accent" style="width: {topo.avgTopicalConsistency * 100}%"></div>
          </div>
        </div>
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Low Alignment</div>
          <div class="text-xl font-bold {topo.lowAlignmentPages > 0 ? 'text-warning' : 'text-success'} mt-1">{topo.lowAlignmentPages.toLocaleString()}</div>
          <div class="text-[10px] text-fg-2">Pages with T* proxy &lt; 0.3, where title, headings, and body discuss different topics. Likely demoted in initial retrieval</div>
        </div>
      </div>
    </div>
  {/if}

  <!-- 3. Quality (Q*) -->
  {#if read || rich || eeat}
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-semibold text-accent mb-1">Quality (Q* Score)</h3>
      <p class="text-[10px] text-fg-2 mb-4">Q* is Google's site-wide, query-independent quality score on a 0-1 scale. Pages below 0.4 are ineligible for featured snippets and rich results. Hand-crafted (not ML), operates at subdomain level, tied to E-E-A-T. Confirmed by Pandu Nayak. Candour Agency independently verified the 0.4 threshold across 800K domains. <span class="text-fg-2/60">Rankpedia: doj:q-star, leak:contentEffort, patent:US8682892</span></p>
      <div class="grid gap-3 grid-cols-2 md:grid-cols-4">
        {#if read}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Q* High</div>
            <div class="text-xl font-bold text-success mt-1">{read.qualityTier1.toLocaleString()} <span class="text-xs font-normal text-fg-2">pages</span></div>
            <div class="text-[10px] text-fg-2">Q* proxy &gt; 0.7. Eligible for all SERP features including AI Overviews and featured snippets</div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Q* Acceptable</div>
            <div class="text-xl font-bold mt-1">{read.qualityTier2.toLocaleString()} <span class="text-xs font-normal text-fg-2">pages</span></div>
            <div class="text-[10px] text-fg-2">Q* proxy 0.4-0.7. Above the rich result threshold but may lose out on premium placements</div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Q* Below Threshold</div>
            <div class="text-xl font-bold {read.qualityTier3 > 0 ? 'text-danger' : 'text-success'} mt-1">{read.qualityTier3.toLocaleString()} <span class="text-xs font-normal text-fg-2">pages</span></div>
            <div class="text-[10px] text-fg-2">Q* proxy &lt; 0.4. Below the gate for rich results. These pages are limited to standard blue links</div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Avg Readability</div>
            <div class="text-xl font-bold mt-1">{fmt(read.avgScore, 1)}</div>
            <div class="text-[10px] text-fg-2">Flesch-Kincaid Grade Level. Lower is more accessible. Google's quality rater guidelines emphasize content should match audience reading level</div>
          </div>
        {/if}
        {#if rich}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Content Richness</div>
            <div class="text-xl font-bold mt-1">{fmt(rich.avgRichnessScore)}</div>
            <div class="text-[10px] text-fg-2">Proxy for contentEffort (leaked LLM signal). Measures structural diversity: headings, lists, tables, images, code blocks. <span class="text-fg-2/60">Rankpedia: leak:contentEffort</span></div>
            <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
              <div class="h-full rounded bg-success" style="width: {rich.avgRichnessScore * 100}%"></div>
            </div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Low Richness</div>
            <div class="text-xl font-bold {rich.lowRichnessPages > 0 ? 'text-warning' : 'text-success'} mt-1">{rich.lowRichnessPages.toLocaleString()} <span class="text-xs font-normal text-fg-2">pages</span></div>
            <div class="text-[10px] text-fg-2">Score &lt; 0.2. Thin content with minimal structural elements, likely scored low on contentEffort</div>
          </div>
        {/if}
        {#if eeat}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Author Coverage</div>
            <div class="text-xl font-bold mt-1">{fmt(eeat.authorCoverage, 0)}%</div>
            <div class="text-[10px] text-fg-2">{eeat.pagesWithAuthor.toLocaleString()} pages with author markup. Google's authorshipMarkup patent (US9697259) uses structured author data for expertise signals. <span class="text-fg-2/60">Rankpedia: patent:US9697259</span></div>
            <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
              <div class="h-full rounded bg-accent" style="width: {eeat.authorCoverage}%"></div>
            </div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Trust Pages</div>
            <div class="text-xl font-bold mt-1">{[eeat.hasAboutPage ? 'About' : '', eeat.hasContactPage ? 'Contact' : '', eeat.hasEditorialPolicy ? 'Editorial' : ''].filter(Boolean).join(', ') || 'None'}</div>
            <div class="text-[10px] text-fg-2">E-E-A-T trust signals. About, Contact, and Editorial Policy pages strengthen domain trustworthiness, a direct input to Q*</div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- 4. Authority -->
  {#if li || anchor}
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-semibold text-accent mb-1">Authority (NSR / PageRank<sub class="text-[9px]">NS</sub>)</h3>
      <p class="text-[10px] text-fg-2 mb-4">NSR (Normalized Site Rank) is a 46-signal composite measuring site-wide authority. PageRank<sub>NS</sub> (Nearest Seed) computes link authority based on proximity to trusted seed pages, confirmed still active in the leaked API. Internal link structure directly controls how authority flows across your site. <span class="text-fg-2/60">Rankpedia: leak:pagerank-ns, leak:site-pr, doj:nsr, patent:US9165040</span></p>
      <div class="grid gap-3 grid-cols-2 md:grid-cols-4">
        {#if li}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Avg Click Depth</div>
            <div class="text-xl font-bold mt-1">{fmt(li.avgClickDepth, 1)}</div>
            <div class="text-[10px] text-fg-2">Average hops from seed URL. Pages deeper than 3-4 clicks receive exponentially less crawl frequency and PageRank flow</div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Near Orphans</div>
            <div class="text-xl font-bold {li.nearOrphansCount > 0 ? 'text-warning' : 'text-success'} mt-1">{li.nearOrphansCount.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">Pages with only 1-2 inlinks. Minimal internal linking starves these pages of PageRank and makes them fragile to link loss</div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Link Dilution</div>
            <div class="text-xl font-bold {li.dilutionWarningsCount > 0 ? 'text-warning' : 'text-success'} mt-1">{li.dilutionWarningsCount.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">Pages with excess outlinks. PageRank is divided equally among outlinks; too many dilutes the authority passed to each target</div>
          </div>
        {/if}
        {#if anchor}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Anchor Diversity</div>
            <div class="text-xl font-bold mt-1">{fmt(anchor.avgInboundDiversity)}</div>
            <div class="text-[10px] text-fg-2">Anchor text variation in inbound links. Low diversity (same text everywhere) triggers anchorMismatch spam signals. Natural profiles mix brand, keyword, and generic anchors. <span class="text-fg-2/60">Rankpedia: patent:US7533092, doj:BayesSpam</span></div>
            <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
              <div class="h-full rounded bg-success" style="width: {anchor.avgInboundDiversity * 100}%"></div>
            </div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- 5. Engagement Readiness (P*) -->
  {#if passage || ai || fresh}
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-semibold text-accent mb-1">Engagement Readiness (P* / NavBoost Proxy)</h3>
      <p class="text-[10px] text-fg-2 mb-4">P* is Google's dynamic engagement metric combining NavBoost click data with anchor signals. NavBoost is "just a big table" of 13 months of click statistics (not ML), achieving 91% accuracy. It tracks goodClicks, badClicks, and lastLongestClicks, segmented by location and device. This section measures how well your content is structured to earn those positive signals. <span class="text-fg-2/60">Rankpedia: doj:p-star, doj:navboost, doj:good-clicks, doj:last-longest-clicks, patent:US20160078102</span></p>
      <div class="grid gap-3 grid-cols-2 md:grid-cols-4">
        {#if passage}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Passage Score</div>
            <div class="text-xl font-bold mt-1">{fmt(passage.avgPassageScore)}</div>
            <div class="text-[10px] text-fg-2">Passage ranking readiness (0-1). Google can rank individual passages within a page (patent US9940367). Well-sectioned content with clear headings is more likely to be selected</div>
            <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
              <div class="h-full rounded bg-accent" style="width: {passage.avgPassageScore * 100}%"></div>
            </div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">FAQ Pages</div>
            <div class="text-xl font-bold mt-1">{passage.pagesWithFaq.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">Pages with FAQ structure (question-answer pairs). Eligible for FAQ rich results and passage extraction by AI Overviews</div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">HowTo Pages</div>
            <div class="text-xl font-bold mt-1">{passage.pagesWithHowTo.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">Pages with step-by-step structure. Structured procedural content earns HowTo rich results and increased SERP real estate</div>
          </div>
        {/if}
        {#if ai}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">AI Readiness</div>
            <div class="text-xl font-bold mt-1">{fmt(ai.avgAiReadinessScore)}</div>
            <div class="text-[10px] text-fg-2">AI Overview citation readiness (0-1). Measures concise definitions, structured answers, and schema markup that FastSearch/SnippetBrain can extract. <span class="text-fg-2/60">Rankpedia: doj:fastsearch, doj:snippetbrain</span></div>
            <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
              <div class="h-full rounded bg-accent" style="width: {ai.avgAiReadinessScore * 100}%"></div>
            </div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Structured Answers</div>
            <div class="text-xl font-bold mt-1">{ai.pagesWithStructuredAnswer.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">Pages with a concise definition or answer near the top. These are prime candidates for featured snippets and AI Overview citations</div>
          </div>
        {/if}
        {#if fresh}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Dated Content</div>
            <div class="text-xl font-bold mt-1">{pct(fresh.pagesWithDate, fresh.pagesWithDate + fresh.pagesWithoutDate)}</div>
            <div class="text-[10px] text-fg-2">{fresh.pagesWithDate.toLocaleString()} pages with date signals (byline, schema, meta). Google uses semanticDateInfo and bylineDate for freshness scoring. <span class="text-fg-2/60">Rankpedia: patent:US8924379, patent:US8583617</span></div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Inconsistent Dates</div>
            <div class="text-xl font-bold {fresh.inconsistentDates > 0 ? 'text-warning' : 'text-success'} mt-1">{fresh.inconsistentDates.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">Schema datePublished vs meta/byline date mismatch. Inconsistent dates confuse freshness signals and can trigger the wrong QDF (Query Deserves Freshness) treatment</div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- 6. Visibility Funnel -->
  {#if funnel}
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-semibold text-accent mb-1">Visibility Funnel (SuperRoot / SERP)</h3>
      <p class="text-[10px] text-fg-2 mb-4">End-to-end pipeline: Trawler crawls, Alexandria indexes, Ascorer scores T*, Mustang retrieves candidates, SuperRoot assembles the SERP. Each stage filters pages. This funnel shows how many of your pages survive each gate. GSC/GA4 integration adds Visible (impressions) and Active (clicks) stages. <span class="text-fg-2/60">Rankpedia: doj:superroot, doj:mustang, doj:ascorer</span></p>
      <div class="grid gap-3 grid-cols-2 md:grid-cols-4">
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Crawled</div>
          <div class="text-xl font-bold mt-1">{funnel.crawled.toLocaleString()}</div>
          <div class="text-[10px] text-fg-2">Total URLs successfully fetched by the crawler</div>
        </div>
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Indexable</div>
          <div class="text-xl font-bold mt-1">{funnel.indexable.toLocaleString()}</div>
          <div class="text-[10px] text-fg-2">{pct(funnel.indexable, funnel.crawled)} pass rate. Pages eligible for Alexandria's index (200, no noindex, self-canonical)</div>
          <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
            <div class="h-full rounded bg-success" style="width: {(funnel.indexable / Math.max(funnel.crawled, 1)) * 100}%"></div>
          </div>
        </div>
        {#if funnel.visible > 0}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Visible</div>
            <div class="text-xl font-bold mt-1">{funnel.visible.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">{pct(funnel.visible, funnel.crawled)} of crawled. Pages with GSC impressions (appeared in SERPs)</div>
          </div>
        {/if}
        {#if funnel.active > 0}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Active</div>
            <div class="text-xl font-bold mt-1">{funnel.active.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">{pct(funnel.active, funnel.crawled)} of crawled. Pages with organic clicks (contributing to NavBoost signals)</div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

</div>
