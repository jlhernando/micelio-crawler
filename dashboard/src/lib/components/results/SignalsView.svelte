<script lang="ts">
  let { stats, pageCount }: { stats: Record<string, unknown>; pageCount: number } = $props();

  // Stat accessors
  let idx = $derived(stats.indexabilityStats as { indexable: number; nonIndexable: number } | null);
  let orphans = $derived((stats.orphanPages as string[]) || []);
  let orphanTotal = $derived(Number(stats.totalOrphanPages) || orphans.length); // list is capped at 1,000
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
    <h3 class="text-sm font-semibold text-accent mb-1">Crawl & Index</h3>
    <p class="text-[10px] text-fg-2 mb-4">Search engines must crawl and index a page before it can rank. Crawl budget spent on non-indexable URLs can slow how fast new and updated pages are found. Check index coverage in Search Console.</p>
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
        <div class="text-xl font-bold {orphanTotal > 0 ? 'text-warning' : 'text-success'} mt-1">{orphanTotal.toLocaleString()}</div>
        <div class="text-[10px] text-fg-2">Pages with no internal link path from seed. Googlebot relies on links for discovery; orphans depend entirely on sitemaps or external links</div>
      </div>
      {#if redirects}
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Temporary Redirects</div>
          <div class="text-xl font-bold {(redirects.navBoostAtRisk ?? 0) > 0 ? 'text-warning' : 'text-success'} mt-1">{(redirects.navBoostAtRisk ?? 0).toLocaleString()}</div>
          <div class="text-[10px] text-fg-2">Pages behind 302/307 redirects. Temporary redirects may split signals between the old and new URL. Use 301 for permanent moves.</div>
        </div>
      {/if}
    </div>
  </div>

  <!-- 2. Relevance (topicality proxy) -->
  {#if topo}
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-semibold text-accent mb-1">Relevance (topicality proxy)</h3>
      <p class="text-[10px] text-fg-2 mb-4">Crawl-time proxy for how clearly each page states its topic: title, headings and body text should agree, and inbound anchor text should match. It is a Micelio heuristic, not a Google score. Validate with Search Console queries.</p>
      <div class="grid gap-3 grid-cols-2 md:grid-cols-4">
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Title-Body Overlap</div>
          <div class="text-xl font-bold mt-1">{fmt(topo.avgTitleBodyOverlap)}</div>
          <div class="text-[10px] text-fg-2">Fraction of title terms found in body text. Higher means the title better reflects the content</div>
          <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
            <div class="h-full rounded bg-accent" style="width: {topo.avgTitleBodyOverlap * 100}%"></div>
          </div>
        </div>
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Title-H1 Alignment</div>
          <div class="text-xl font-bold mt-1">{fmt(topo.avgTitleH1Alignment)}</div>
          <div class="text-[10px] text-fg-2">Jaccard similarity between title tag and H1. Misalignment signals inconsistent topic targeting, which can make the page topic less clear</div>
          <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
            <div class="h-full rounded bg-accent" style="width: {topo.avgTitleH1Alignment * 100}%"></div>
          </div>
        </div>
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Topical Consistency</div>
          <div class="text-xl font-bold mt-1">{fmt(topo.avgTopicalConsistency)}</div>
          <div class="text-[10px] text-fg-2">Weighted composite of title-body, title-H1, and heading-body coverage. Overall on-page topic alignment</div>
          <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
            <div class="h-full rounded bg-accent" style="width: {topo.avgTopicalConsistency * 100}%"></div>
          </div>
        </div>
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Low Alignment</div>
          <div class="text-xl font-bold {topo.lowAlignmentPages > 0 ? 'text-warning' : 'text-success'} mt-1">{topo.lowAlignmentPages.toLocaleString()}</div>
          <div class="text-[10px] text-fg-2">Pages with topicality proxy &lt; 0.3, where title, headings, and body discuss different topics. Review these first</div>
        </div>
      </div>
    </div>
  {/if}

  <!-- 3. Quality (proxy score) -->
  {#if read || rich || eeat}
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-semibold text-accent mb-1">Quality (proxy score)</h3>
      <p class="text-[10px] text-fg-2 mb-4">Micelio's 0-1 quality proxy built from readability, content structure and trust pages. 0.4 and 0.7 are Micelio cut-offs, not Google thresholds. Use it to find pages to review, then check performance in Search Console and analytics.</p>
      <div class="grid gap-3 grid-cols-2 md:grid-cols-4">
        {#if read}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">High quality (proxy)</div>
            <div class="text-xl font-bold text-success mt-1">{read.qualityTier1.toLocaleString()} <span class="text-xs font-normal text-fg-2">pages</span></div>
            <div class="text-[10px] text-fg-2">Proxy &gt; 0.7. Strong structure and readability signals</div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Acceptable (proxy)</div>
            <div class="text-xl font-bold mt-1">{read.qualityTier2.toLocaleString()} <span class="text-xs font-normal text-fg-2">pages</span></div>
            <div class="text-[10px] text-fg-2">Proxy 0.4-0.7. Reasonable, with room to improve</div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Low quality (proxy)</div>
            <div class="text-xl font-bold {read.qualityTier3 > 0 ? 'text-danger' : 'text-success'} mt-1">{read.qualityTier3.toLocaleString()} <span class="text-xs font-normal text-fg-2">pages</span></div>
            <div class="text-[10px] text-fg-2">Proxy &lt; 0.4. Review these pages for thin or weak content</div>
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
            <div class="text-[10px] text-fg-2">Proxy for content effort. Measures structural diversity: headings, lists, tables, images, code blocks.</div>
            <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
              <div class="h-full rounded bg-success" style="width: {rich.avgRichnessScore * 100}%"></div>
            </div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Low Richness</div>
            <div class="text-xl font-bold {rich.lowRichnessPages > 0 ? 'text-warning' : 'text-success'} mt-1">{rich.lowRichnessPages.toLocaleString()} <span class="text-xs font-normal text-fg-2">pages</span></div>
            <div class="text-[10px] text-fg-2">Score &lt; 0.2. Thin content with minimal structural elements, worth reviewing for depth</div>
          </div>
        {/if}
        {#if eeat}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Author Coverage</div>
            <div class="text-xl font-bold mt-1">{fmt(eeat.authorCoverage, 0)}%</div>
            <div class="text-[10px] text-fg-2">{eeat.pagesWithAuthor.toLocaleString()} pages with author markup. Clear authorship helps users and search engines judge expertise (see patent US9697259).</div>
            <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
              <div class="h-full rounded bg-accent" style="width: {eeat.authorCoverage}%"></div>
            </div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Trust Pages</div>
            <div class="text-xl font-bold mt-1">{[eeat.hasAboutPage ? 'About' : '', eeat.hasContactPage ? 'Contact' : '', eeat.hasEditorialPolicy ? 'Editorial' : ''].filter(Boolean).join(', ') || 'None'}</div>
            <div class="text-[10px] text-fg-2">E-E-A-T trust signals. About, Contact, and Editorial Policy pages help users judge who runs the site</div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- 4. Authority -->
  {#if li || anchor}
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-semibold text-accent mb-1">Authority (internal links)</h3>
      <p class="text-[10px] text-fg-2 mb-4">Internal links decide how link equity (PageRank) flows across your site and how easily crawlers find pages. These metrics are computed from your crawl graph.</p>
      <div class="grid gap-3 grid-cols-2 md:grid-cols-4">
        {#if li}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Avg Click Depth</div>
            <div class="text-xl font-bold mt-1">{fmt(li.avgClickDepth, 1)}</div>
            <div class="text-[10px] text-fg-2">Average hops from seed URL. Pages deeper than 3-4 clicks tend to be crawled less often and receive less internal PageRank</div>
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
            <div class="text-[10px] text-fg-2">Anchor text variation in inbound links. Very low diversity (same text everywhere) can look manipulative. Natural profiles mix brand, keyword, and generic anchors.</div>
            <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
              <div class="h-full rounded bg-success" style="width: {anchor.avgInboundDiversity * 100}%"></div>
            </div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- 5. Engagement Readiness -->
  {#if passage || ai || fresh}
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-semibold text-accent mb-1">Engagement Readiness</h3>
      <p class="text-[10px] text-fg-2 mb-4">Crawl-time checks for content that answers questions clearly: passages, FAQs, step-by-step guides, concise answers and dates. These are readiness heuristics. Real engagement comes from users, so validate with Search Console and analytics.</p>
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
            <div class="text-[10px] text-fg-2">Pages with FAQ structure (question-answer pairs). Clear question-answer pairs are easy to quote in search features</div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">HowTo Pages</div>
            <div class="text-xl font-bold mt-1">{passage.pagesWithHowTo.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">Pages with step-by-step structure. Clear procedural content is easier for users to follow</div>
          </div>
        {/if}
        {#if ai}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">AI Readiness</div>
            <div class="text-xl font-bold mt-1">{fmt(ai.avgAiReadinessScore)}</div>
            <div class="text-[10px] text-fg-2">AI answer readiness proxy (0-1). Measures concise definitions, structured answers, and schema markup that are easy to quote.</div>
            <div class="mt-2 h-1.5 rounded bg-surface-1 overflow-hidden">
              <div class="h-full rounded bg-accent" style="width: {ai.avgAiReadinessScore * 100}%"></div>
            </div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Structured Answers</div>
            <div class="text-xl font-bold mt-1">{ai.pagesWithStructuredAnswer.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">Pages with a concise definition or answer near the top. Candidates for featured snippets and AI answers; validate in Search Console</div>
          </div>
        {/if}
        {#if fresh}
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Dated Content</div>
            <div class="text-xl font-bold mt-1">{pct(fresh.pagesWithDate, fresh.pagesWithDate + fresh.pagesWithoutDate)}</div>
            <div class="text-[10px] text-fg-2">{fresh.pagesWithDate.toLocaleString()} pages with date signals (byline, schema, meta). Clear dates help users and search engines judge freshness.</div>
          </div>
          <div class="rounded bg-surface-3 p-3">
            <div class="text-[10px] text-fg-2 uppercase tracking-wide">Inconsistent Dates</div>
            <div class="text-xl font-bold {fresh.inconsistentDates > 0 ? 'text-warning' : 'text-success'} mt-1">{fresh.inconsistentDates.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">Schema datePublished vs meta/byline date mismatch. Inconsistent dates can confuse freshness signals</div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- 6. Visibility Funnel -->
  {#if funnel}
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-semibold text-accent mb-1">Visibility Funnel</h3>
      <p class="text-[10px] text-fg-2 mb-4">How many pages pass each stage: crawled, indexable, visible and active. Crawl data covers the first two stages. GSC/GA4 integration adds Visible (impressions) and Active (clicks) stages.</p>
      <div class="grid gap-3 grid-cols-2 md:grid-cols-4">
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Crawled</div>
          <div class="text-xl font-bold mt-1">{funnel.crawled.toLocaleString()}</div>
          <div class="text-[10px] text-fg-2">Total URLs successfully fetched by the crawler</div>
        </div>
        <div class="rounded bg-surface-3 p-3">
          <div class="text-[10px] text-fg-2 uppercase tracking-wide">Indexable</div>
          <div class="text-xl font-bold mt-1">{funnel.indexable.toLocaleString()}</div>
          <div class="text-[10px] text-fg-2">{pct(funnel.indexable, funnel.crawled)} pass rate. Pages eligible for indexing (200, no noindex, self-canonical)</div>
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
            <div class="text-[10px] text-fg-2">{pct(funnel.active, funnel.crawled)} of crawled. Pages with organic clicks</div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

</div>
