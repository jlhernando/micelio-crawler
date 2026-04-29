<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { createWsClient } from '../lib/ws';
  import SummaryHeader from '../lib/components/results/SummaryHeader.svelte';
  import IssueCards from '../lib/components/results/IssueCards.svelte';
  import PagesTable from '../lib/components/results/PagesTable.svelte';
  import LinkGraph from '../lib/components/results/LinkGraph.svelte';
  import DirectoryTree from '../lib/components/results/DirectoryTree.svelte';
  import AnchorCloud from '../lib/components/results/AnchorCloud.svelte';
  import SeoFunnel from '../lib/components/results/SeoFunnel.svelte';
  import AIVisibility from '../lib/components/results/AIVisibility.svelte';
  import AlertPanel from '../lib/components/results/AlertPanel.svelte';
  import TagsHealthBar from '../lib/components/results/TagsHealthBar.svelte';
  import StatusCodePie from '../lib/components/results/StatusCodePie.svelte';
  import IndexabilityReasonsPie from '../lib/components/results/IndexabilityReasonsPie.svelte';
  import ResponseTimeChart from '../lib/components/results/ResponseTimeChart.svelte';
  import InlinkInsights from '../lib/components/results/InlinkInsights.svelte';
  import CanonicalHealthPie from '../lib/components/results/CanonicalHealthPie.svelte';
  import ImageAuditBar from '../lib/components/results/ImageAuditBar.svelte';
  import PageRankHistogram from '../lib/components/results/PageRankHistogram.svelte';
  import UrlStructureChart from '../lib/components/results/UrlStructureChart.svelte';
  import ContentQualityGrid from '../lib/components/results/ContentQualityGrid.svelte';
  import UrlIssuesCard from '../lib/components/results/UrlIssuesCard.svelte';
  import StructuredDataCard from '../lib/components/results/StructuredDataCard.svelte';
  import SecurityOverview from '../lib/components/results/SecurityOverview.svelte';
  import WordCountHistogram from '../lib/components/results/WordCountHistogram.svelte';
  import SignalsView from '../lib/components/results/SignalsView.svelte';

  let { id }: { id: string } = $props();

  let loading = $state(true);
  let error = $state<string | null>(null);
  let stats = $state<Record<string, unknown> | null>(null);
  let pageCount = $state(0);
  let analysisPending = $state(false);
  let pagesReady = $state(false);
  let loadingProgress = $state(0);

  // Active tab
  let activeTab = $state<'pages' | 'issues' | 'performance' | 'content' | 'links' | 'signals' | 'ai-visibility'>('pages');

  // Filters passed from SummaryHeader clicks
  let initialFilterStatus = $state('all');
  let initialFilterTemplate = $state('all');
  let initialFilterIndexable = $state('all');
  let initialFilterClassification = $state('all');
  let initialFilterRobotsBlocked = $state('all');
  let filterGeneration = $state(0);

  // Store original unfiltered stats for restoring when filter is cleared
  let baseStats = $state<Record<string, unknown> | null>(null);
  let basePageCount = $state(0);
  let filteredStatsLoading = $state(false);
  // Preserve original template distribution so pills don't disappear when filtered
  let originalTemplateDist = $state<Record<string, number> | null>(null);

  function handleSummaryFilter(filter: { status?: string; template?: string; indexable?: string; classification?: string; robotsBlocked?: string }) {
    initialFilterStatus = filter.status || 'all';
    initialFilterTemplate = filter.template || 'all';
    initialFilterIndexable = filter.indexable || 'all';
    initialFilterClassification = filter.classification || 'all';
    initialFilterRobotsBlocked = filter.robotsBlocked || 'all';
    filterGeneration++;
    // Only switch to pages tab if a non-template filter was applied
    if (filter.status || filter.indexable || filter.classification || filter.robotsBlocked) {
      activeTab = 'pages';
    }

    const template = filter.template || 'all';
    if (template === 'all') {
      // Restore original stats
      if (baseStats) {
        stats = baseStats;
        pageCount = basePageCount;
      }
    } else {
      // Save original stats if not saved yet
      if (!baseStats && stats) {
        baseStats = stats;
        basePageCount = pageCount;
      }
      filteredStatsLoading = true;
      api.getFilteredStats(id, template).then((data) => {
        stats = data.stats as Record<string, unknown>;
        pageCount = data.pageCount as number;
        filteredStatsLoading = false;
      }).catch(() => {
        filteredStatsLoading = false;
      });
    }
  }

  // ── Performance derived data ──
  let slowPages = $derived((stats?.slowPages as { url: string; responseTimeMs: number }[]) || []);
  let depthDist = $derived((stats?.depthDistribution as Record<string, number>) || {});
  // ── Content derived data ──
  let readabilityStats = $derived((stats?.readabilityStats as { avgScore: number; difficult: { url: string; score: number }[]; veryEasy: { url: string; score: number }[] }) || null);
  let ngramStats = $derived((stats?.ngramStats as { unigrams: { term: string; count: number; tfidf: number }[]; bigrams: { term: string; count: number; tfidf: number }[]; totalPages: number; totalTokens: number }) || null);
  let nearDuplicates = $derived((stats?.nearDuplicateGroups as { urls: string[]; similarity: number }[]) || []);
  let wordCountPages = $state<{ wordCount?: number }[]>([]);
  let wordCountLoaded = $state(false);
  let textToCode = $derived((stats?.textToCodeStats as { avgRatio: number; contentPoor: { url: string; ratio: number }[] }) || null);

  // ── Link derived data ──
  let linkAnalysis = $derived((stats?.linkAnalysis as { deadEndPages: string[]; nofollowedInternalLinks: number; followedInternalLinks: number; linksToNonIndexable: { from: string; to: string }[] }) || null);
  let liStats = $derived((stats?.linkIntelligenceStats as Record<string, unknown>) || null);

  onMount(() => {
    api.getCrawlStats(id).then((data: Record<string, unknown>) => {
      stats = data.stats as Record<string, unknown>;
      pageCount = data.pageCount as number;
      analysisPending = (data.analysisPending as boolean) || false;
      // Preserve original template distribution for pills
      const s = data.stats as Record<string, unknown>;
      if (s?.templateTypeDistribution) {
        originalTemplateDist = s.templateTypeDistribution as Record<string, number>;
      }
      pagesReady = (data.pagesReady as boolean) ?? true;
      loadingProgress = (data.loadingProgress as number) ?? 100;
      loading = false;
    }).catch((err) => {
      error = err.message;
      loading = false;
    });

    // Listen for analysis_complete to refresh stats when deferred analysis finishes
    const ws = createWsClient((msg) => {
      if (msg.type === 'analysis_complete' && msg.data?.crawlId === id) {
        analysisPending = false;
        // Re-fetch stats with link intelligence data
        api.getCrawlStats(id).then((data: Record<string, unknown>) => {
          stats = data.stats as Record<string, unknown>;
          pageCount = data.pageCount as number;
        });
      }
    });

    return () => ws.close();
  });

  // Poll loading progress when pages aren't ready yet
  $effect(() => {
    if (pagesReady || loading) return;
    let errorCount = 0;
    const interval = setInterval(async () => {
      try {
        const data = await api.getLoadingProgress(id);
        errorCount = 0; // reset on success
        loadingProgress = data.loadingProgress;
        if (data.pagesReady) {
          pagesReady = true;
          loadingProgress = 100;
          clearInterval(interval);
        }
      } catch {
        errorCount++;
        if (errorCount >= 10) {
          clearInterval(interval);
          // Results evicted from cache — trigger a full reload
          pagesReady = true;
          loadingProgress = 100;
        }
      }
    }, 500);
    return () => clearInterval(interval);
  });

  // Lazy-load word counts when Content tab is opened
  $effect(() => {
    if (activeTab === 'content' && !wordCountLoaded && pagesReady) {
      wordCountLoaded = true;
      api.getCrawlPages(id, { limit: 50000 }).then((data: Record<string, unknown>) => {
        wordCountPages = ((data.pages as { wordCount?: number }[]) || []);
      }).catch((err: Error) => { console.warn('Failed to load word count data:', err.message); });
    }
  });

  function formatMs(ms: number): string {
    if (ms < 1000) return `${Math.round(ms)}ms`;
    return `${(ms / 1000).toFixed(1)}s`;
  }
</script>

<div class="max-w-7xl mx-auto space-y-4">
  {#if loading}
    <div class="rounded-md border border-border bg-surface-2 p-12 text-center">
      <div class="inline-block w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
      <p class="text-fg-2 mt-3">Loading results...</p>
    </div>
  {:else if error}
    <div class="rounded-md border border-red-500/30 bg-red-500/10 p-8 text-center">
      <h2 class="text-xl font-semibold text-danger mb-2">Error Loading Results</h2>
      <p class="text-fg-2">{error}</p>
    </div>
  {:else if stats}
    <!-- Summary Header -->
    <SummaryHeader {stats} {pageCount} onFilter={handleSummaryFilter} activeTemplate={initialFilterTemplate} templateDistribution={originalTemplateDist} />

    <!-- SEO Funnel + Tags Health -->
    <div class="grid gap-3 grid-cols-1 md:grid-cols-2">
      <SeoFunnel {stats} onFilter={handleSummaryFilter} />
      <TagsHealthBar {stats} {pageCount} />
    </div>

    <!-- Alerts -->
    {#if stats.alertSummary}
      <AlertPanel {stats} />
    {/if}

    <!-- Chart Row: Status Code + Indexability Reasons + Load Time -->
    <div class="grid gap-3 grid-cols-1 md:grid-cols-3">
      <StatusCodePie {stats} onFilter={handleSummaryFilter} />
      <IndexabilityReasonsPie {stats} onFilter={handleSummaryFilter} />
      <ResponseTimeChart {stats} />
    </div>

    <!-- Chart Row 2: Canonical + Image Audit + PageRank -->
    <div class="grid gap-3 grid-cols-1 md:grid-cols-3">
      <CanonicalHealthPie {stats} />
      <ImageAuditBar {stats} />
      <PageRankHistogram {stats} />
    </div>

    <!-- Chart Row 3: URL Structure + Content Quality -->
    <div class="grid gap-3 grid-cols-1 md:grid-cols-2">
      <UrlStructureChart {stats} />
      <ContentQualityGrid {stats} />
    </div>

    <!-- Chart Row 4: URL Issues + Structured Data + Security -->
    <div class="grid gap-3 grid-cols-1 md:grid-cols-3">
      <UrlIssuesCard {stats} />
      <StructuredDataCard {stats} {pageCount} />
      <SecurityOverview {stats} {pageCount} onFilter={handleSummaryFilter} />
    </div>

    <!-- Tab Navigation -->
    <div class="flex gap-1 p-1 rounded-md bg-surface-2 border border-border">
      {#each [
        { key: 'pages', label: 'All Pages' },
        { key: 'issues', label: 'Issues' },
        { key: 'performance', label: 'Performance' },
        { key: 'content', label: 'Content' },
        { key: 'links', label: 'Links' },
        { key: 'signals', label: 'Signals' },
        ...(stats.aiVisibilityStats ? [{ key: 'ai-visibility', label: 'AI Visibility' }] : []),
      ] as tab}
        <button
          type="button"
          class="flex-1 px-4 py-2 rounded-md text-sm font-medium transition-colors cursor-pointer {activeTab === tab.key ? 'bg-accent text-white' : 'text-fg-2 hover:text-fg-1 hover:bg-surface-3'}"
          onclick={() => activeTab = tab.key as typeof activeTab}
        >{tab.label}</button>
      {/each}
    </div>

    <!-- Tab Content -->
    {#if activeTab === 'issues'}
      <IssueCards {stats} crawlId={id} />

    {:else if activeTab === 'performance'}
      <div class="space-y-4">
        <!-- Depth Distribution -->
        {#if Object.keys(depthDist).length > 0}
          {@const depthColors = ['#3fb950', '#58a6ff', '#d29922', '#f97316', '#f85149', '#f85149']}
          {@const maxCount = Math.max(...Object.values(depthDist) as number[])}
          <div class="rounded-md border border-border bg-surface-2 p-5">
            <h3 class="text-sm font-medium text-fg-2 mb-4">Depth Distribution</h3>
            <div class="space-y-2">
              {#each Object.entries(depthDist).sort((a, b) => parseInt(a[0]) - parseInt(b[0])) as [depth, count], i}
                <div class="flex items-center gap-3">
                  <span class="w-16 text-xs text-fg-2 text-right">Depth {depth}</span>
                  <div class="flex-1 h-5 rounded bg-surface-3 overflow-hidden">
                    <div class="h-full rounded transition-all" style="width: {((count as number) / maxCount) * 100}%; background: {depthColors[Math.min(i, depthColors.length - 1)]}"></div>
                  </div>
                  <span class="w-16 text-xs text-right font-mono">{(count as number).toLocaleString()}</span>
                </div>
              {/each}
            </div>
          </div>
        {/if}

        <!-- Slow Pages -->
        {#if slowPages.length > 0}
          <div class="rounded-md border border-border bg-surface-2 p-5">
            <h3 class="text-sm font-medium text-fg-2 mb-3">Slowest Pages</h3>
            <div class="space-y-1 max-h-64 overflow-y-auto">
              {#each slowPages.slice(0, 20) as page}
                <div class="flex items-center gap-2 py-1 px-2 rounded hover:bg-surface-3 text-xs">
                  <span class="flex-1 truncate font-mono text-fg-2">{page.url}</span>
                  <span class="shrink-0 font-mono {page.responseTimeMs > 3000 ? 'text-danger' : page.responseTimeMs > 1000 ? 'text-warning' : 'text-fg-2'}">{formatMs(page.responseTimeMs)}</span>
                </div>
              {/each}
            </div>
          </div>
        {/if}
      </div>

    {:else if activeTab === 'content'}
      <div class="space-y-4">
        <!-- Word Count Distribution -->
        <WordCountHistogram pages={wordCountPages} />

        <!-- Text-to-Code Ratio -->
        {#if textToCode}
          <div class="rounded-md border border-border bg-surface-2 p-5">
            <h3 class="text-sm font-medium text-fg-2 mb-3">Text-to-Code Ratio</h3>
            <div class="text-lg font-bold mb-2">{textToCode.avgRatio.toFixed(1)}% <span class="text-sm text-fg-2 font-normal">average</span></div>
            {#if (textToCode.contentPoor?.length ?? 0) > 0}
              <div class="text-xs text-fg-2 mb-2">{textToCode.contentPoor.length.toLocaleString()} pages with poor ratio (&lt;10%)</div>
              <div class="space-y-1 max-h-40 overflow-y-auto">
                {#each (textToCode.contentPoor ?? []).slice(0, 15) as page}
                  <div class="flex items-center gap-2 py-1 px-2 rounded hover:bg-surface-3 text-xs">
                    <span class="flex-1 truncate font-mono text-fg-2">{page.url}</span>
                    <span class="text-danger shrink-0">{page.ratio.toFixed(1)}%</span>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        {/if}

        <!-- Readability -->
        {#if readabilityStats}
          <div class="rounded-md border border-border bg-surface-2 p-5">
            <h3 class="text-sm font-medium text-fg-2 mb-3">Readability</h3>
            <div class="text-lg font-bold mb-2">{readabilityStats.avgScore.toFixed(1)} <span class="text-sm text-fg-2 font-normal">avg Flesch-Kincaid</span></div>
            <div class="grid grid-cols-2 gap-4 mt-3">
              {#if (readabilityStats.difficult?.length ?? 0) > 0}
                <div>
                  <div class="text-xs text-fg-2 mb-1">Difficult to read ({readabilityStats.difficult.length.toLocaleString()})</div>
                  <div class="space-y-1 max-h-32 overflow-y-auto">
                    {#each (readabilityStats.difficult ?? []).slice(0, 10) as page}
                      <div class="text-xs font-mono text-fg-2 truncate">{page.url}</div>
                    {/each}
                  </div>
                </div>
              {/if}
              {#if (readabilityStats.veryEasy?.length ?? 0) > 0}
                <div>
                  <div class="text-xs text-fg-2 mb-1">Very easy ({readabilityStats.veryEasy.length.toLocaleString()})</div>
                  <div class="space-y-1 max-h-32 overflow-y-auto">
                    {#each (readabilityStats.veryEasy ?? []).slice(0, 10) as page}
                      <div class="text-xs font-mono text-fg-2 truncate">{page.url}</div>
                    {/each}
                  </div>
                </div>
              {/if}
            </div>
          </div>
        {/if}

        <!-- N-gram Analysis -->
        {#if ngramStats}
          <div class="rounded-md border border-border bg-surface-2 p-5">
            <h3 class="text-sm font-medium text-fg-2 mb-3">
              Top Terms
              <span class="text-xs font-normal ml-2">{ngramStats.totalTokens.toLocaleString()} tokens across {ngramStats.totalPages} pages</span>
            </h3>
            <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <div class="text-xs text-fg-2 mb-2">Top Unigrams</div>
                <div class="space-y-1">
                  {#each (ngramStats.unigrams ?? []).slice(0, 10) as term}
                    <div class="flex items-center gap-2 text-xs">
                      <span class="font-mono flex-1">{term.term}</span>
                      <span class="text-fg-2">{term.count.toLocaleString()}</span>
                    </div>
                  {/each}
                </div>
              </div>
              <div>
                <div class="text-xs text-fg-2 mb-2">Top Bigrams</div>
                <div class="space-y-1">
                  {#each (ngramStats.bigrams ?? []).slice(0, 10) as term}
                    <div class="flex items-center gap-2 text-xs">
                      <span class="font-mono flex-1">{term.term}</span>
                      <span class="text-fg-2">{term.count.toLocaleString()}</span>
                    </div>
                  {/each}
                </div>
              </div>
            </div>
          </div>
        {/if}

        <!-- Anchor Text Cloud -->
        <AnchorCloud crawlId={id} />

        <!-- Near Duplicates -->
        {#if nearDuplicates.length > 0}
          <div class="rounded-md border border-border bg-surface-2 p-5">
            <h3 class="text-sm font-medium text-fg-2 mb-3">Near-Duplicate Groups ({nearDuplicates.length.toLocaleString()})</h3>
            <div class="space-y-3 max-h-64 overflow-y-auto">
              {#each nearDuplicates.slice(0, 10) as group}
                <div class="rounded-md bg-surface-3 p-3">
                  <div class="text-xs text-fg-2 mb-1">{group.similarity.toFixed(0)}% similar — {group.urls.length.toLocaleString()} pages</div>
                  {#each group.urls as url}
                    <div class="text-xs font-mono text-fg-2 truncate">{url}</div>
                  {/each}
                </div>
              {/each}
            </div>
          </div>
        {/if}
      </div>

    {:else if activeTab === 'links'}
      <div class="space-y-4">
        <!-- Inlink Distribution Insights -->
        <InlinkInsights {stats} />

        <!-- Link Summary -->
        <div class="grid grid-cols-2 md:grid-cols-4 gap-3">
          <div class="rounded-md border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2">Internal Links</div>
            <div class="text-2xl font-bold mt-1">{((stats?.totalInternalLinks as number) || 0).toLocaleString()}</div>
          </div>
          <div class="rounded-md border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2">External Links</div>
            <div class="text-2xl font-bold mt-1">{((stats?.totalExternalLinks as number) || 0).toLocaleString()}</div>
          </div>
          {#if linkAnalysis}
            <div class="rounded-md border border-border bg-surface-2 p-4">
              <div class="text-xs text-fg-2">Dead-End Pages</div>
              <div class="text-2xl font-bold mt-1 {(linkAnalysis.deadEndPages?.length ?? 0) > 0 ? 'text-warning' : ''}">{(linkAnalysis.deadEndPages?.length ?? 0).toLocaleString()}</div>
            </div>
            <div class="rounded-md border border-border bg-surface-2 p-4">
              <div class="text-xs text-fg-2">Nofollow Internal</div>
              <div class="text-2xl font-bold mt-1">{linkAnalysis.nofollowedInternalLinks.toLocaleString()}</div>
            </div>
          {/if}
        </div>

        <!-- Analysis Pending Banner -->
        {#if analysisPending}
          <div class="rounded-md border border-accent/30 bg-accent/5 p-4 flex items-center gap-3">
            <div class="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin shrink-0"></div>
            <div>
              <div class="text-sm font-medium text-fg-1">Link analysis in progress</div>
              <div class="text-xs text-fg-2">Link intelligence, centrality, and suggestions will appear automatically when complete.</div>
            </div>
          </div>
        {/if}

        <!-- Link Intelligence -->
        {#if liStats}
          <div class="rounded-md border border-border bg-surface-2 p-5">
            <h3 class="text-sm font-medium text-fg-2 mb-4">Link Intelligence</h3>
            <div class="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
              <div>
                <div class="text-xs text-fg-2">Avg Click Depth</div>
                <div class="text-xl font-bold">{((liStats.avgClickDepth as number) || 0).toFixed(1)}</div>
              </div>
              <div>
                <div class="text-xs text-fg-2">Max Click Depth</div>
                <div class="text-xl font-bold">{(liStats.maxClickDepth as number) || 0}</div>
              </div>
              <div>
                <div class="text-xs text-fg-2">Unreachable Pages</div>
                <div class="text-xl font-bold {(liStats.unreachablePagesCount as number) > 0 ? 'text-danger' : ''}">{((liStats.unreachablePagesCount as number) || 0).toLocaleString()}</div>
              </div>
              <div>
                <div class="text-xs text-fg-2">Link Suggestions</div>
                <div class="text-xl font-bold text-accent">{((liStats.linkSuggestionsCount as number) || 0).toLocaleString()}</div>
              </div>
            </div>

            <!-- Top Link Suggestions -->
            {#if (liStats.linkSuggestions as unknown[])?.length}
              <div class="mt-3">
                <div class="text-xs text-fg-2 mb-2">Top Suggestions</div>
                <div class="space-y-1 max-h-40 overflow-y-auto">
                  {#each ((liStats.linkSuggestions as { sourceUrl: string; targetUrl: string; score: number }[]) || []).slice(0, 10) as sug}
                    <div class="flex items-center gap-2 py-1 px-2 rounded hover:bg-surface-3 text-xs">
                      <span class="font-mono text-fg-2 truncate flex-1">{sug.sourceUrl}</span>
                      <span class="text-accent shrink-0">→</span>
                      <span class="font-mono text-fg-2 truncate flex-1">{sug.targetUrl}</span>
                      <span class="text-fg-2 shrink-0">{sug.score.toFixed(1)}</span>
                    </div>
                  {/each}
                </div>
              </div>
            {/if}
          </div>
        {/if}

        <!-- Directory Tree -->
        <DirectoryTree crawlId={id} />

        <!-- Internal Link Graph -->
        <LinkGraph crawlId={id} />

        <!-- Dead End Pages Detail -->
        {#if linkAnalysis && (linkAnalysis.deadEndPages?.length ?? 0) > 0}
          <div class="rounded-md border border-border bg-surface-2 p-5">
            <h3 class="text-sm font-medium text-fg-2 mb-3">Dead-End Pages ({linkAnalysis.deadEndPages.length})</h3>
            <div class="space-y-1 max-h-48 overflow-y-auto">
              {#each (linkAnalysis.deadEndPages ?? []).slice(0, 30) as url}
                <div class="text-xs font-mono text-fg-2 py-1 px-2 rounded hover:bg-surface-3">{url}</div>
              {/each}
            </div>
          </div>
        {/if}
      </div>

    {:else if activeTab === 'signals'}
      <SignalsView {stats} {pageCount} />

    {:else if activeTab === 'ai-visibility'}
      <AIVisibility {stats} />

    {:else if activeTab === 'pages'}
      <PagesTable crawlId={id} {pageCount} {pagesReady} {loadingProgress} {initialFilterStatus} {initialFilterTemplate} {initialFilterIndexable} {initialFilterClassification} {initialFilterRobotsBlocked} {filterGeneration} />
    {/if}
  {:else}
    <div class="rounded-md border border-border bg-surface-2 p-8 text-center">
      <h2 class="text-xl font-semibold mb-2">No Results</h2>
      <p class="text-fg-2">No results found for this crawl. The crawl may still be running.</p>
    </div>
  {/if}
</div>
