<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';

  let { oldId, newId }: { oldId: string; newId: string } = $props();

  // --- Types ---

  interface DiffFieldChange {
    field: string;
    oldValue: string | number | boolean;
    newValue: string | number | boolean;
  }

  interface DiffUrlChange {
    url: string;
    changes: DiffFieldChange[];
  }

  interface URLTrafficInfo {
    url: string;
    clicks: number;
    impressions: number;
    sessions: number;
    visitors: number;
    statusCode: number;
    reason: string;
  }

  interface LifecycleSummary {
    disappearedWithTraffic: URLTrafficInfo[];
    newWithTraffic: URLTrafficInfo[];
    disappearedReasons: Record<string, number>;
  }

  interface DiffResult {
    oldCount: number;
    newCount: number;
    addedUrls: string[];
    removedUrls: string[];
    changedUrls: DiffUrlChange[];
    unchangedCount: number;
    fieldSummary: Record<string, number>;
    lifecycle?: LifecycleSummary;
  }

  interface IntDelta { old: number; new: number; delta: number; }
  interface MetricDelta { old: number; new: number; delta: number; }
  interface URLMetricChange { url: string; old: number; new: number; delta: number; }
  interface Finding { section: string; message: string; severity: string; impact: number; }
  interface ScoreItem { name: string; weight: number; oldScore: number; newScore: number; }
  interface StatusMigration { from: string; to: string; count: number; urls?: string[]; }
  interface NgramChange { term: string; count: number; pages: number; }
  interface SegmentComparison {
    name: string; pages: IntDelta; indexable: IntDelta;
    avgWordCount: MetricDelta; avgResponseMs: MetricDelta; clicks: IntDelta;
  }
  interface SeoFunnel {
    crawled: number; indexable: number; visible: number; active: number;
    pctIndexable: number; pctVisible: number; pctActive: number;
  }

  interface ComparisonReport {
    healthScore: { oldScore: number; newScore: number; delta: number; details: ScoreItem[]; };
    coverage: { totalPages: IntDelta; newUrls: number; disappearedUrls: number; persistedUrls: number; robotsBlocked: IntDelta; };
    funnel?: { oldFunnel?: SeoFunnel; newFunnel?: SeoFunnel; };
    httpStatus: { oldStatusCodes: Record<number, number>; newStatusCodes: Record<number, number>; migrations: StatusMigration[]; brokenLinks: IntDelta; soft404s: IntDelta; };
    indexability: { indexable: IntDelta; nonIndexable: IntDelta; becameNonIndexable?: string[]; becameIndexable?: string[]; };
    onPageSeo: { withoutTitle: IntDelta; withoutDescription: IntDelta; withoutH1: IntDelta; duplicateTitles: IntDelta; duplicateDescs: IntDelta; titleTooLong: IntDelta; titleTooShort: IntDelta; descTooLong: IntDelta; multipleH1: IntDelta; withoutOg: IntDelta; };
    content: { thinContentPages: IntDelta; duplicateGroups: IntDelta; nearDuplicateGroups: IntDelta; readability: MetricDelta; textToCode: MetricDelta; modifiedPages: number; unchangedPages: number; };
    links: { orphanPages: IntDelta; nearOrphans: IntDelta; dilutionWarnings: IntDelta; pageRankWinners?: URLMetricChange[]; pageRankLosers?: URLMetricChange[]; };
    internalLink: { totalInternal: IntDelta; totalExternal: IntDelta; deadEnds: IntDelta; };
    redirects: { redirectChains: IntDelta; longChains: IntDelta; };
    performance: { responseP50: MetricDelta; responseP90: MetricDelta; responseP99: MetricDelta; slowPages: IntDelta; avgWeight?: MetricDelta; };
    schema?: { pagesWithSchema: IntDelta; pagesWithErrors: IntDelta; richEligible: IntDelta; };
    images: { totalImages: IntDelta; missingAlt: IntDelta; };
    security: { nonHttps: IntDelta; mixedContent: IntDelta; };
    hreflang: { issues: IntDelta; };
    sitemap?: { totalSitemapUrls: IntDelta; missingFromSitemap: IntDelta; nonIndexableInSitemap: IntDelta; };
    visibility?: { impressions: IntDelta; clicks: IntDelta; avgCtr: MetricDelta; avgPosition: MetricDelta; zombiePages: IntDelta; winners?: URLMetricChange[]; losers?: URLMetricChange[]; };
    ngrams?: { emergingTopics: NgramChange[]; decliningTopics: NgramChange[]; };
    embeddings?: { similarPairs: IntDelta; cannibalizationGroups: IntDelta; };
    segments?: SegmentComparison[];
    findings: Finding[];
    urls: DiffResult;
  }

  // --- State ---

  type TabKey = 'overview' | 'urls' | 'seo' | 'content' | 'links' | 'technical' | 'performance' | 'visibility' | 'segments';

  let loading = $state(true);
  let error = $state<string | null>(null);
  let comparison = $state<ComparisonReport | null>(null);
  let activeTab = $state<TabKey>('overview');
  let urlsTab = $state<'changed' | 'added' | 'removed'>('changed');
  let removedFilter = $state<'all' | 'with-traffic'>('all');

  let oldCrawl = $state<Record<string, unknown> | null>(null);
  let newCrawl = $state<Record<string, unknown> | null>(null);

  onMount(async () => {
    try {
      const [rawData, oldData, newData] = await Promise.all([
        api.compareCrawls(oldId, newId) as unknown as Promise<{ comparison: ComparisonReport }>,
        api.getCrawlStatus(oldId),
        api.getCrawlStatus(newId),
      ]);
      comparison = rawData.comparison;
      oldCrawl = oldData;
      newCrawl = newData;
      loading = false;
    } catch (err) {
      error = (err as Error).message;
      loading = false;
    }
  });

  // --- Derived ---

  let diff = $derived(comparison?.urls ?? null);

  let trafficByUrl = $derived.by(() => {
    const map = new Map<string, URLTrafficInfo>();
    if (diff?.lifecycle?.disappearedWithTraffic) {
      for (const info of diff.lifecycle.disappearedWithTraffic) map.set(info.url, info);
    }
    return map;
  });

  let newTrafficByUrl = $derived.by(() => {
    const map = new Map<string, URLTrafficInfo>();
    if (diff?.lifecycle?.newWithTraffic) {
      for (const info of diff.lifecycle.newWithTraffic) map.set(info.url, info);
    }
    return map;
  });

  let filteredRemovedUrls = $derived.by(() => {
    if (!diff) return [];
    if (removedFilter === 'with-traffic') return diff.removedUrls.filter(url => trafficByUrl.has(url));
    return diff.removedUrls;
  });

  let criticalCount = $derived((comparison?.findings ?? []).filter(f => f.severity === 'critical').length);
  let warningCount = $derived((comparison?.findings ?? []).filter(f => f.severity === 'warning').length);
  let infoCount = $derived((comparison?.findings ?? []).filter(f => f.severity === 'info').length);

  let availableTabs = $derived.by(() => {
    const tabs: { key: TabKey; label: string; severity?: string }[] = [
      { key: 'overview', label: 'Overview' },
      { key: 'urls', label: 'URLs' },
      { key: 'seo', label: 'SEO' },
      { key: 'content', label: 'Content' },
      { key: 'links', label: 'Links' },
      { key: 'technical', label: 'Technical' },
      { key: 'performance', label: 'Performance' },
    ];
    if (comparison?.visibility) tabs.push({ key: 'visibility', label: 'Visibility' });
    if (comparison?.segments?.length) tabs.push({ key: 'segments', label: 'Segments' });

    // Assign worst severity per tab
    if (comparison) {
      const sectionToTab: Record<string, TabKey> = {
        healthScore: 'overview', coverage: 'overview',
        httpStatus: 'technical', indexability: 'technical', security: 'technical',
        seo: 'seo', content: 'content', links: 'links', performance: 'performance',
        visibility: 'visibility',
      };
      const tabSeverity: Record<string, number> = {};
      for (const f of comparison.findings) {
        const tab = sectionToTab[f.section] || 'overview';
        const rank = f.severity === 'critical' ? 0 : f.severity === 'warning' ? 1 : 2;
        if (tabSeverity[tab] === undefined || rank < tabSeverity[tab]) tabSeverity[tab] = rank;
      }
      for (const t of tabs) {
        const rank = tabSeverity[t.key];
        if (rank === 0) t.severity = 'critical';
        else if (rank === 1) t.severity = 'warning';
      }
    }

    return tabs;
  });

  // --- Helpers ---

  function extractDomain(url: string): string {
    try { return new URL(url).hostname; } catch { return url; }
  }

  function formatDate(iso: string): string {
    const d = new Date(iso);
    return d.toLocaleDateString('en-GB', { day: '2-digit', month: 'short' }) +
      ' ' + d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
  }

  // When invert=true, lower is better (e.g. response time, error counts),
  // so a negative delta is "good" and a positive delta is "bad".
  function deltaBadge(delta: number, invert = false): string {
    const isGood = invert ? delta < 0 : delta > 0;
    const isBad = invert ? delta > 0 : delta < 0;
    if (isGood) return 'text-green-400';
    if (isBad) return 'text-red-400';
    return 'text-fg-2';
  }

  function formatDelta(d: number, suffix = ''): string {
    if (d > 0) return `+${d.toLocaleString()}${suffix}`;
    return `${d.toLocaleString()}${suffix}`;
  }

  function sevDot(sev?: string): string {
    if (sev === 'critical') return 'bg-red-400';
    if (sev === 'warning') return 'bg-yellow-400';
    return '';
  }

  const FIELD_LABELS: Record<string, string> = {
    statusCode: 'Status Code', title: 'Title', metaDescription: 'Description',
    canonical: 'Canonical', metaRobots: 'Meta Robots', h1: 'H1',
    wordCount: 'Word Count', indexable: 'Indexable', redirectChainLength: 'Redirect Hops',
    internalLinks: 'Internal Links', externalLinks: 'External Links', depth: 'Depth',
  };

  const REASON_LABELS: Record<string, { label: string; color: string }> = {
    'removed': { label: 'Removed', color: 'text-red-400 bg-red-500/10' },
    'redirected': { label: 'Redirected', color: 'text-yellow-400 bg-yellow-500/10' },
    'client-error': { label: '4xx Error', color: 'text-red-400 bg-red-500/10' },
    'server-error': { label: '5xx Error', color: 'text-red-400 bg-red-500/10' },
    'non-indexable': { label: 'Non-Indexable', color: 'text-fg-2 bg-surface-3' },
  };

  function reasonLabel(r: string): string { return REASON_LABELS[r]?.label || r; }
  function reasonColor(r: string): string { return REASON_LABELS[r]?.color || 'text-fg-2 bg-surface-3'; }
</script>

<div class="max-w-7xl mx-auto space-y-4">
  {#if loading}
    <div class="rounded-xl border border-border bg-surface-2 p-12 text-center">
      <div class="inline-block w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
      <p class="text-fg-2 mt-3">Computing comparison...</p>
    </div>
  {:else if error}
    <div class="rounded-xl border border-red-500/30 bg-red-500/10 p-8 text-center">
      <h2 class="text-xl font-semibold text-red-400 mb-2">Error</h2>
      <p class="text-fg-2">{error}</p>
      <a href="#/history" class="inline-block mt-4 text-sm text-accent hover:underline">Back to History</a>
    </div>
  {:else if comparison && diff}
    <!-- Header -->
    <div class="rounded-xl border border-border bg-surface-2 p-4">
      <div class="flex items-center justify-between flex-wrap gap-2">
        <div>
          <div class="text-lg font-semibold">{oldCrawl?.seedUrl ? extractDomain(oldCrawl.seedUrl as string) : 'Crawl Comparison'}</div>
          <div class="text-xs text-fg-2">
            {#if oldCrawl?.startedAt}{formatDate(oldCrawl.startedAt as string)}{/if}
            ({diff.oldCount.toLocaleString()} pages) →
            {#if newCrawl?.startedAt}{formatDate(newCrawl.startedAt as string)}{/if}
            ({diff.newCount.toLocaleString()} pages)
          </div>
        </div>
        <a href="#/history" class="text-xs text-accent hover:underline">Back to History</a>
      </div>
    </div>

    <!-- Summary cards -->
    <div class="grid grid-cols-2 sm:grid-cols-4 gap-3">
      <div class="rounded-xl border border-border bg-surface-2 p-4 text-center">
        <div class="text-xs text-fg-2">Health Score</div>
        <div class="text-2xl font-bold mt-1">{comparison.healthScore.oldScore} → {comparison.healthScore.newScore}</div>
        <div class="text-xs font-medium mt-1 {comparison.healthScore.delta >= 0 ? 'text-green-400' : 'text-red-400'}">
          {comparison.healthScore.delta > 0 ? '+' : ''}{comparison.healthScore.delta}
        </div>
      </div>
      <div class="rounded-xl border border-border bg-surface-2 p-4 text-center">
        <div class="text-xs text-fg-2">Critical</div>
        <div class="text-2xl font-bold mt-1 {criticalCount > 0 ? 'text-red-400' : 'text-green-400'}">{criticalCount.toLocaleString()}</div>
      </div>
      <div class="rounded-xl border border-border bg-surface-2 p-4 text-center">
        <div class="text-xs text-fg-2">Warnings</div>
        <div class="text-2xl font-bold mt-1 {warningCount > 0 ? 'text-yellow-400' : 'text-fg-1'}">{warningCount.toLocaleString()}</div>
      </div>
      <div class="rounded-xl border border-border bg-surface-2 p-4 text-center">
        <div class="text-xs text-fg-2">Improvements</div>
        <div class="text-2xl font-bold mt-1 text-green-400">{infoCount.toLocaleString()}</div>
      </div>
    </div>

    <!-- Tab bar -->
    <div class="flex gap-1 p-1 rounded-xl bg-surface-2 border border-border overflow-x-auto">
      {#each availableTabs as tab}
        <button
          type="button"
          class="relative px-3 py-2 rounded-lg text-xs font-medium transition-colors cursor-pointer whitespace-nowrap {activeTab === tab.key ? 'bg-accent text-white' : 'text-fg-2 hover:text-fg-1 hover:bg-surface-3'}"
          onclick={() => activeTab = tab.key}
        >
          {#if tab.severity && activeTab !== tab.key}
            <span class="absolute top-1 right-1 w-1.5 h-1.5 rounded-full {sevDot(tab.severity)}"></span>
          {/if}
          {tab.label}
        </button>
      {/each}
    </div>

    <!-- Tab content -->

    <!-- OVERVIEW TAB -->
    {#if activeTab === 'overview'}
      <div class="space-y-4">
        <!-- Key metrics -->
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-3">
          <div class="rounded-xl border border-border bg-surface-2 p-3">
            <div class="text-[10px] text-fg-2">Pages</div>
            <div class="text-lg font-bold">{comparison.coverage.totalPages.new.toLocaleString()}</div>
            <div class="text-xs {deltaBadge(comparison.coverage.totalPages.delta)}">{formatDelta(comparison.coverage.totalPages.delta)}</div>
          </div>
          <div class="rounded-xl border border-border bg-surface-2 p-3">
            <div class="text-[10px] text-fg-2">Indexable</div>
            <div class="text-lg font-bold">{comparison.indexability.indexable.new.toLocaleString()}</div>
            <div class="text-xs {deltaBadge(comparison.indexability.indexable.delta)}">{formatDelta(comparison.indexability.indexable.delta)}</div>
          </div>
          <div class="rounded-xl border border-border bg-surface-2 p-3">
            <div class="text-[10px] text-fg-2">p90 Response</div>
            <div class="text-lg font-bold">{comparison.performance.responseP90.new.toFixed(0)}ms</div>
            <div class="text-xs {deltaBadge(comparison.performance.responseP90.delta, true)}">{formatDelta(comparison.performance.responseP90.delta, 'ms')}</div>
          </div>
          {#if comparison.visibility}
            <div class="rounded-xl border border-border bg-surface-2 p-3">
              <div class="text-[10px] text-fg-2">Clicks</div>
              <div class="text-lg font-bold">{comparison.visibility.clicks.new.toLocaleString()}</div>
              <div class="text-xs {deltaBadge(comparison.visibility.clicks.delta)}">{formatDelta(comparison.visibility.clicks.delta)}</div>
            </div>
          {:else}
            <div class="rounded-xl border border-border bg-surface-2 p-3">
              <div class="text-[10px] text-fg-2">Broken Links</div>
              <div class="text-lg font-bold">{comparison.httpStatus.brokenLinks.new.toLocaleString()}</div>
              <div class="text-xs {deltaBadge(comparison.httpStatus.brokenLinks.delta, true)}">{formatDelta(comparison.httpStatus.brokenLinks.delta)}</div>
            </div>
          {/if}
        </div>

        <!-- SEO Funnel -->
        {#if comparison.funnel?.oldFunnel && comparison.funnel?.newFunnel}
          <div class="rounded-xl border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2 mb-3 font-medium">SEO Funnel</div>
            <div class="grid grid-cols-4 gap-3 text-center text-xs">
              {#each [
                { label: 'Crawled', old: comparison.funnel.oldFunnel.crawled, new: comparison.funnel.newFunnel.crawled },
                { label: 'Indexable', old: comparison.funnel.oldFunnel.indexable, new: comparison.funnel.newFunnel.indexable },
                { label: 'Visible', old: comparison.funnel.oldFunnel.visible, new: comparison.funnel.newFunnel.visible },
                { label: 'Active', old: comparison.funnel.oldFunnel.active, new: comparison.funnel.newFunnel.active },
              ] as stage}
                <div>
                  <div class="text-fg-2 mb-1">{stage.label}</div>
                  <div class="font-bold">{stage.old.toLocaleString()} → {stage.new.toLocaleString()}</div>
                  <div class="{deltaBadge(stage.new - stage.old)}">{formatDelta(stage.new - stage.old)}</div>
                </div>
              {/each}
            </div>
          </div>
        {/if}

        <!-- Top Findings -->
        {#if comparison.findings.length > 0}
          <div class="rounded-xl border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2 mb-3 font-medium">Top Findings</div>
            <div class="space-y-1.5">
              {#each comparison.findings.slice(0, 10) as finding}
                <div class="flex items-start gap-2 text-xs py-1">
                  {#if finding.severity === 'critical'}
                    <span class="shrink-0 mt-0.5 px-1.5 py-0.5 rounded text-[10px] font-bold bg-red-500/15 text-red-400">!!</span>
                  {:else if finding.severity === 'warning'}
                    <span class="shrink-0 mt-0.5 px-1.5 py-0.5 rounded text-[10px] font-bold bg-yellow-500/15 text-yellow-400">!</span>
                  {:else}
                    <span class="shrink-0 mt-0.5 px-1.5 py-0.5 rounded text-[10px] font-bold bg-green-500/15 text-green-400">+</span>
                  {/if}
                  <span class="text-fg-1">{finding.message}</span>
                </div>
              {/each}
            </div>
          </div>
        {/if}
      </div>

    <!-- URLS TAB -->
    {:else if activeTab === 'urls'}
      <div class="space-y-4">
        <!-- Summary cards -->
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-3">
          <div class="rounded-xl border border-border bg-surface-2 p-4 text-center">
            <div class="text-xs text-fg-2">New URLs</div>
            <div class="text-2xl font-bold mt-1 text-green-400">+{diff.addedUrls.length.toLocaleString()}</div>
          </div>
          <div class="rounded-xl border border-border bg-surface-2 p-4 text-center">
            <div class="text-xs text-fg-2">Disappeared</div>
            <div class="text-2xl font-bold mt-1 text-red-400">-{diff.removedUrls.length.toLocaleString()}</div>
          </div>
          <div class="rounded-xl border border-border bg-surface-2 p-4 text-center">
            <div class="text-xs text-fg-2">Changed</div>
            <div class="text-2xl font-bold mt-1 text-yellow-400">{diff.changedUrls.length.toLocaleString()}</div>
          </div>
          <div class="rounded-xl border border-border bg-surface-2 p-4 text-center">
            <div class="text-xs text-fg-2">Unchanged</div>
            <div class="text-2xl font-bold mt-1">{diff.unchangedCount.toLocaleString()}</div>
          </div>
        </div>

        <!-- Disappeared reasons -->
        {#if diff.lifecycle && Object.keys(diff.lifecycle.disappearedReasons).length > 0}
          <div class="rounded-xl border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2 mb-3">Disappeared URL Reasons</div>
            <div class="flex flex-wrap gap-2">
              {#each Object.entries(diff.lifecycle.disappearedReasons).sort((a, b) => b[1] - a[1]) as [reason, count]}
                <span class="px-2.5 py-1 rounded-lg text-xs font-medium {reasonColor(reason)}">
                  {reasonLabel(reason)} <span class="opacity-60 ml-1">{count.toLocaleString()}</span>
                </span>
              {/each}
            </div>
          </div>
        {/if}

        <!-- Disappeared with traffic alert -->
        {#if diff.lifecycle?.disappearedWithTraffic?.length}
          <div class="rounded-xl border border-red-500/30 bg-red-500/5 p-4">
            <div class="flex items-center gap-2 mb-3">
              <span class="text-xs font-medium text-red-400">Disappeared URLs with Traffic</span>
              <span class="px-1.5 py-0.5 rounded text-[10px] font-bold bg-red-500/15 text-red-400">{diff.lifecycle.disappearedWithTraffic.length.toLocaleString()}</span>
            </div>
            <div class="space-y-1 max-h-48 overflow-y-auto">
              {#each diff.lifecycle.disappearedWithTraffic.slice(0, 20) as info}
                <div class="flex items-center gap-3 py-1.5 px-2 rounded hover:bg-red-500/5 text-xs">
                  <span class="flex-1 font-mono text-fg-2 truncate">{info.url}</span>
                  {#if info.clicks > 0}<span class="shrink-0 text-red-400 font-medium">{info.clicks.toLocaleString()} clicks</span>{/if}
                  {#if info.impressions > 0}<span class="shrink-0 text-fg-2">{info.impressions.toLocaleString()} imp</span>{/if}
                  <span class="shrink-0 px-1.5 py-0.5 rounded text-[10px] {reasonColor(info.reason)}">{reasonLabel(info.reason)}</span>
                </div>
              {/each}
            </div>
          </div>
        {/if}

        <!-- Field change summary -->
        {#if Object.keys(diff.fieldSummary).length > 0}
          <div class="rounded-xl border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2 mb-3">Fields Changed</div>
            <div class="flex flex-wrap gap-2">
              {#each Object.entries(diff.fieldSummary).sort((a, b) => b[1] - a[1]) as [field, count]}
                <span class="px-2.5 py-1 rounded-lg text-xs bg-yellow-500/10 text-yellow-400 font-medium">
                  {FIELD_LABELS[field] || field} <span class="text-yellow-400/60 ml-1">{count.toLocaleString()}</span>
                </span>
              {/each}
            </div>
          </div>
        {/if}

        <!-- URLs sub-tabs -->
        <div class="flex gap-1 p-1 rounded-xl bg-surface-2 border border-border">
          {#each [
            { key: 'changed', label: `Changed (${diff.changedUrls.length.toLocaleString()})` },
            { key: 'added', label: `New (${diff.addedUrls.length.toLocaleString()})` },
            { key: 'removed', label: `Disappeared (${diff.removedUrls.length.toLocaleString()})` },
          ] as tab}
            <button type="button"
              class="flex-1 px-4 py-2 rounded-lg text-sm font-medium transition-colors cursor-pointer {urlsTab === tab.key ? 'bg-accent text-white' : 'text-fg-2 hover:text-fg-1 hover:bg-surface-3'}"
              onclick={() => urlsTab = tab.key as typeof urlsTab}
            >{tab.label}</button>
          {/each}
        </div>

        {#if urlsTab === 'changed'}
          <div class="rounded-xl border border-border bg-surface-2 overflow-hidden">
            <div class="max-h-[600px] overflow-y-auto">
              {#each diff.changedUrls as change}
                <div class="border-b border-border/50 p-4 hover:bg-surface-3 transition-colors">
                  <div class="font-mono text-xs text-fg-2 mb-2 truncate">{change.url}</div>
                  <div class="space-y-1">
                    {#each change.changes as fc}
                      <div class="flex items-start gap-3 text-xs">
                        <span class="w-24 shrink-0 text-fg-2 font-medium">{FIELD_LABELS[fc.field] || fc.field}</span>
                        <span class="flex-1 text-red-400 line-through truncate">{fc.oldValue}</span>
                        <span class="text-fg-2 shrink-0">→</span>
                        <span class="flex-1 text-green-400 truncate">{fc.newValue}</span>
                      </div>
                    {/each}
                  </div>
                </div>
              {/each}
              {#if diff.changedUrls.length === 0}
                <div class="p-8 text-center text-fg-2 text-sm">No field changes detected.</div>
              {/if}
            </div>
          </div>
        {:else if urlsTab === 'added'}
          <div class="rounded-xl border border-border bg-surface-2 overflow-hidden">
            <div class="max-h-[600px] overflow-y-auto">
              {#each diff.addedUrls as url}
                {@const t = newTrafficByUrl.get(url)}
                <div class="flex items-center gap-2 px-4 py-2 border-b border-border/50 hover:bg-surface-3">
                  <span class="w-5 text-center text-green-400 font-bold">+</span>
                  <span class="font-mono text-xs text-fg-2 truncate flex-1">{url}</span>
                  {#if t}<span class="shrink-0 px-1.5 py-0.5 rounded text-[10px] font-medium bg-green-500/10 text-green-400">{t.clicks} clicks</span>{/if}
                </div>
              {/each}
              {#if diff.addedUrls.length === 0}
                <div class="p-8 text-center text-fg-2 text-sm">No new URLs.</div>
              {/if}
            </div>
          </div>
        {:else if urlsTab === 'removed'}
          {#if diff.lifecycle?.disappearedWithTraffic?.length}
            <div class="flex gap-2">
              <button type="button" class="px-3 py-1.5 rounded-lg text-xs font-medium cursor-pointer {removedFilter === 'all' ? 'bg-accent text-white' : 'text-fg-2 bg-surface-2 border border-border'}" onclick={() => removedFilter = 'all'}>All ({diff.removedUrls.length})</button>
              <button type="button" class="px-3 py-1.5 rounded-lg text-xs font-medium cursor-pointer {removedFilter === 'with-traffic' ? 'bg-red-500 text-white' : 'text-fg-2 bg-surface-2 border border-border'}" onclick={() => removedFilter = 'with-traffic'}>With Traffic ({diff.lifecycle?.disappearedWithTraffic?.length || 0})</button>
            </div>
          {/if}
          <div class="rounded-xl border border-border bg-surface-2 overflow-hidden">
            <div class="max-h-[600px] overflow-y-auto">
              {#each filteredRemovedUrls as url}
                {@const traffic = trafficByUrl.get(url)}
                <div class="flex items-center gap-2 px-4 py-2 border-b border-border/50 hover:bg-surface-3">
                  <span class="w-5 text-center text-red-400 font-bold">-</span>
                  <span class="font-mono text-xs text-fg-2 truncate flex-1">{url}</span>
                  {#if traffic}
                    {#if traffic.clicks > 0}<span class="shrink-0 text-[10px] text-red-400 font-medium">{traffic.clicks} clicks</span>{/if}
                    {#if traffic.impressions > 0}<span class="shrink-0 text-[10px] text-fg-2">{traffic.impressions.toLocaleString()} imp</span>{/if}
                    <span class="shrink-0 px-1.5 py-0.5 rounded text-[10px] {reasonColor(traffic.reason)}">{reasonLabel(traffic.reason)}</span>
                  {/if}
                </div>
              {/each}
              {#if diff.removedUrls.length === 0}
                <div class="p-8 text-center text-fg-2 text-sm">No disappeared URLs.</div>
              {/if}
            </div>
          </div>
        {/if}
      </div>

    <!-- SEO TAB -->
    {:else if activeTab === 'seo'}
      <div class="space-y-4">
        <div class="rounded-xl border border-border bg-surface-2 p-4">
          <div class="text-xs text-fg-2 mb-3 font-medium">On-Page SEO Elements</div>
          <div class="space-y-2">
            {#each [
              { label: 'Pages without Title', d: comparison.onPageSeo.withoutTitle, inv: true },
              { label: 'Pages without Description', d: comparison.onPageSeo.withoutDescription, inv: true },
              { label: 'Pages without H1', d: comparison.onPageSeo.withoutH1, inv: true },
              { label: 'Duplicate Titles', d: comparison.onPageSeo.duplicateTitles, inv: true },
              { label: 'Duplicate Descriptions', d: comparison.onPageSeo.duplicateDescs, inv: true },
              { label: 'Title too long', d: comparison.onPageSeo.titleTooLong, inv: true },
              { label: 'Title too short', d: comparison.onPageSeo.titleTooShort, inv: true },
              { label: 'Description too long', d: comparison.onPageSeo.descTooLong, inv: true },
              { label: 'Multiple H1s', d: comparison.onPageSeo.multipleH1, inv: true },
              { label: 'Pages without OG tags', d: comparison.onPageSeo.withoutOg, inv: true },
            ] as row}
              <div class="flex items-center justify-between text-xs py-1 border-b border-border/30">
                <span class="text-fg-2">{row.label}</span>
                <div class="flex items-center gap-3">
                  <span class="text-fg-2 w-14 text-right">{row.d.old.toLocaleString()}</span>
                  <span class="text-fg-2">→</span>
                  <span class="w-14 text-right font-medium">{row.d.new.toLocaleString()}</span>
                  <span class="w-16 text-right font-medium {deltaBadge(row.d.delta, row.inv)}">{row.d.delta !== 0 ? formatDelta(row.d.delta) : '—'}</span>
                </div>
              </div>
            {/each}
          </div>
        </div>

        {#if comparison.schema}
          <div class="rounded-xl border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2 mb-3 font-medium">Structured Data</div>
            <div class="grid grid-cols-3 gap-3 text-center text-xs">
              <div>
                <div class="text-fg-2">With Schema</div>
                <div class="font-bold mt-1">{comparison.schema.pagesWithSchema.old.toLocaleString()} → {comparison.schema.pagesWithSchema.new.toLocaleString()}</div>
                <div class="{deltaBadge(comparison.schema.pagesWithSchema.delta)}">{formatDelta(comparison.schema.pagesWithSchema.delta)}</div>
              </div>
              <div>
                <div class="text-fg-2">Errors</div>
                <div class="font-bold mt-1">{comparison.schema.pagesWithErrors.old.toLocaleString()} → {comparison.schema.pagesWithErrors.new.toLocaleString()}</div>
                <div class="{deltaBadge(comparison.schema.pagesWithErrors.delta, true)}">{formatDelta(comparison.schema.pagesWithErrors.delta)}</div>
              </div>
              <div>
                <div class="text-fg-2">Rich Eligible</div>
                <div class="font-bold mt-1">{comparison.schema.richEligible.old.toLocaleString()} → {comparison.schema.richEligible.new.toLocaleString()}</div>
                <div class="{deltaBadge(comparison.schema.richEligible.delta)}">{formatDelta(comparison.schema.richEligible.delta)}</div>
              </div>
            </div>
          </div>
        {/if}

        <div class="rounded-xl border border-border bg-surface-2 p-4">
          <div class="text-xs text-fg-2 mb-3 font-medium">Images</div>
          <div class="grid grid-cols-2 gap-3 text-center text-xs">
            <div>
              <div class="text-fg-2">Total Images</div>
              <div class="font-bold mt-1">{comparison.images.totalImages.old.toLocaleString()} → {comparison.images.totalImages.new.toLocaleString()}</div>
            </div>
            <div>
              <div class="text-fg-2">Missing Alt</div>
              <div class="font-bold mt-1">{comparison.images.missingAlt.old.toLocaleString()} → {comparison.images.missingAlt.new.toLocaleString()}</div>
              <div class="{deltaBadge(comparison.images.missingAlt.delta, true)}">{formatDelta(comparison.images.missingAlt.delta)}</div>
            </div>
          </div>
        </div>
      </div>

    <!-- CONTENT TAB -->
    {:else if activeTab === 'content'}
      <div class="space-y-4">
        <div class="rounded-xl border border-border bg-surface-2 p-4">
          <div class="text-xs text-fg-2 mb-3 font-medium">Content Quality</div>
          <div class="space-y-2">
            {#each [
              { label: 'Thin content pages', d: comparison.content.thinContentPages, inv: true },
              { label: 'Duplicate groups', d: comparison.content.duplicateGroups, inv: true },
              { label: 'Near-duplicate groups', d: comparison.content.nearDuplicateGroups, inv: true },
            ] as row}
              <div class="flex items-center justify-between text-xs py-1 border-b border-border/30">
                <span class="text-fg-2">{row.label}</span>
                <div class="flex items-center gap-3">
                  <span class="text-fg-2 w-14 text-right">{row.d.old.toLocaleString()}</span>
                  <span class="text-fg-2">→</span>
                  <span class="w-14 text-right font-medium">{row.d.new.toLocaleString()}</span>
                  <span class="w-16 text-right font-medium {deltaBadge(row.d.delta, row.inv)}">{row.d.delta !== 0 ? formatDelta(row.d.delta) : '—'}</span>
                </div>
              </div>
            {/each}
          </div>
        </div>

        {#if comparison.content.modifiedPages > 0 || comparison.content.unchangedPages > 0}
          <div class="rounded-xl border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2 mb-3 font-medium">Content Changes (hash-based)</div>
            <div class="grid grid-cols-2 gap-3 text-center text-xs">
              <div>
                <div class="text-fg-2">Modified Pages</div>
                <div class="text-lg font-bold text-yellow-400">{comparison.content.modifiedPages.toLocaleString()}</div>
              </div>
              <div>
                <div class="text-fg-2">Unchanged Pages</div>
                <div class="text-lg font-bold">{comparison.content.unchangedPages.toLocaleString()}</div>
              </div>
            </div>
          </div>
        {/if}

        {#if comparison.content.readability.old > 0 || comparison.content.readability.new > 0}
          <div class="rounded-xl border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2 mb-3 font-medium">Readability & Ratio</div>
            <div class="grid grid-cols-2 gap-3 text-center text-xs">
              <div>
                <div class="text-fg-2">Avg Readability (Flesch-Kincaid)</div>
                <div class="font-bold mt-1">{comparison.content.readability.old.toFixed(1)} → {comparison.content.readability.new.toFixed(1)}</div>
              </div>
              <div>
                <div class="text-fg-2">Avg Text-to-Code Ratio</div>
                <div class="font-bold mt-1">{comparison.content.textToCode.old.toFixed(1)}% → {comparison.content.textToCode.new.toFixed(1)}%</div>
              </div>
            </div>
          </div>
        {/if}

        {#if comparison.ngrams}
          <div class="rounded-xl border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2 mb-3 font-medium">Topic Evolution (Bigrams)</div>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
              {#if comparison.ngrams.emergingTopics.length > 0}
                <div>
                  <div class="text-[10px] text-green-400 font-medium mb-2">Emerging Topics</div>
                  {#each comparison.ngrams.emergingTopics.slice(0, 10) as topic}
                    <div class="flex items-center justify-between text-xs py-0.5">
                      <span class="text-fg-1">{topic.term}</span>
                      <span class="text-fg-2">{topic.count}x on {topic.pages} pages</span>
                    </div>
                  {/each}
                </div>
              {/if}
              {#if comparison.ngrams.decliningTopics.length > 0}
                <div>
                  <div class="text-[10px] text-red-400 font-medium mb-2">Declining Topics</div>
                  {#each comparison.ngrams.decliningTopics.slice(0, 10) as topic}
                    <div class="flex items-center justify-between text-xs py-0.5">
                      <span class="text-fg-1">{topic.term}</span>
                      <span class="text-fg-2">{topic.count}x on {topic.pages} pages</span>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          </div>
        {/if}

        {#if comparison.embeddings}
          <div class="rounded-xl border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2 mb-3 font-medium">Cannibalization</div>
            <div class="grid grid-cols-2 gap-3 text-center text-xs">
              <div>
                <div class="text-fg-2">Similar Pairs</div>
                <div class="font-bold mt-1">{comparison.embeddings.similarPairs.old} → {comparison.embeddings.similarPairs.new}</div>
                <div class="{deltaBadge(comparison.embeddings.similarPairs.delta, true)}">{formatDelta(comparison.embeddings.similarPairs.delta)}</div>
              </div>
              <div>
                <div class="text-fg-2">Cannibalization Groups</div>
                <div class="font-bold mt-1">{comparison.embeddings.cannibalizationGroups.old} → {comparison.embeddings.cannibalizationGroups.new}</div>
                <div class="{deltaBadge(comparison.embeddings.cannibalizationGroups.delta, true)}">{formatDelta(comparison.embeddings.cannibalizationGroups.delta)}</div>
              </div>
            </div>
          </div>
        {/if}
      </div>

    <!-- LINKS TAB -->
    {:else if activeTab === 'links'}
      <div class="space-y-4">
        <div class="rounded-xl border border-border bg-surface-2 p-4">
          <div class="text-xs text-fg-2 mb-3 font-medium">Link Health</div>
          <div class="space-y-2">
            {#each [
              { label: 'Total Internal Links', d: comparison.internalLink.totalInternal, inv: false },
              { label: 'Total External Links', d: comparison.internalLink.totalExternal, inv: false },
              { label: 'Dead-end Pages', d: comparison.internalLink.deadEnds, inv: true },
              { label: 'Orphan Pages', d: comparison.links.orphanPages, inv: true },
              { label: 'Near-orphan Pages', d: comparison.links.nearOrphans, inv: true },
              { label: 'Link Dilution Warnings', d: comparison.links.dilutionWarnings, inv: true },
            ] as row}
              <div class="flex items-center justify-between text-xs py-1 border-b border-border/30">
                <span class="text-fg-2">{row.label}</span>
                <div class="flex items-center gap-3">
                  <span class="text-fg-2 w-16 text-right">{row.d.old.toLocaleString()}</span>
                  <span class="text-fg-2">→</span>
                  <span class="w-16 text-right font-medium">{row.d.new.toLocaleString()}</span>
                  <span class="w-16 text-right font-medium {deltaBadge(row.d.delta, row.inv)}">{row.d.delta !== 0 ? formatDelta(row.d.delta) : '—'}</span>
                </div>
              </div>
            {/each}
          </div>
        </div>

        {#if comparison.links.pageRankWinners?.length || comparison.links.pageRankLosers?.length}
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
            {#if comparison.links.pageRankWinners?.length}
              <div class="rounded-xl border border-border bg-surface-2 p-4">
                <div class="text-xs text-green-400 font-medium mb-3">PageRank Winners</div>
                <div class="space-y-1 max-h-64 overflow-y-auto">
                  {#each comparison.links.pageRankWinners as mover}
                    <div class="flex items-center gap-2 text-xs py-1">
                      <span class="font-mono text-fg-2 truncate flex-1">{mover.url}</span>
                      <span class="shrink-0 text-fg-2">{mover.old}</span>
                      <span class="shrink-0 text-fg-2">→</span>
                      <span class="shrink-0 font-medium">{mover.new}</span>
                      <span class="shrink-0 text-green-400 font-medium">+{mover.delta}</span>
                    </div>
                  {/each}
                </div>
              </div>
            {/if}
            {#if comparison.links.pageRankLosers?.length}
              <div class="rounded-xl border border-border bg-surface-2 p-4">
                <div class="text-xs text-red-400 font-medium mb-3">PageRank Losers</div>
                <div class="space-y-1 max-h-64 overflow-y-auto">
                  {#each comparison.links.pageRankLosers as mover}
                    <div class="flex items-center gap-2 text-xs py-1">
                      <span class="font-mono text-fg-2 truncate flex-1">{mover.url}</span>
                      <span class="shrink-0 text-fg-2">{mover.old}</span>
                      <span class="shrink-0 text-fg-2">→</span>
                      <span class="shrink-0 font-medium">{mover.new}</span>
                      <span class="shrink-0 text-red-400 font-medium">{mover.delta}</span>
                    </div>
                  {/each}
                </div>
              </div>
            {/if}
          </div>
        {/if}
      </div>

    <!-- TECHNICAL TAB -->
    {:else if activeTab === 'technical'}
      <div class="space-y-4">
        <!-- Status Migrations -->
        {#if comparison.httpStatus.migrations.length > 0}
          <div class="rounded-xl border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2 mb-3 font-medium">Status Code Migrations</div>
            <div class="space-y-1">
              {#each comparison.httpStatus.migrations as mig}
                <div class="flex items-center gap-3 text-xs py-1 border-b border-border/30">
                  <span class="w-16 text-fg-2 font-mono">{mig.from}</span>
                  <span class="text-fg-2">→</span>
                  <span class="w-16 font-mono {mig.to === '4xx' || mig.to === '5xx' ? 'text-red-400' : mig.to === '2xx' ? 'text-green-400' : 'text-fg-1'}">{mig.to}</span>
                  <span class="font-medium">{mig.count} pages</span>
                </div>
              {/each}
            </div>
          </div>
        {/if}

        <!-- Indexability -->
        <div class="rounded-xl border border-border bg-surface-2 p-4">
          <div class="text-xs text-fg-2 mb-3 font-medium">Indexability</div>
          <div class="grid grid-cols-2 gap-3 text-center text-xs">
            <div>
              <div class="text-fg-2">Indexable</div>
              <div class="font-bold mt-1">{comparison.indexability.indexable.old} → {comparison.indexability.indexable.new}</div>
              <div class="{deltaBadge(comparison.indexability.indexable.delta)}">{formatDelta(comparison.indexability.indexable.delta)}</div>
            </div>
            <div>
              <div class="text-fg-2">Non-Indexable</div>
              <div class="font-bold mt-1">{comparison.indexability.nonIndexable.old} → {comparison.indexability.nonIndexable.new}</div>
              <div class="{deltaBadge(comparison.indexability.nonIndexable.delta, true)}">{formatDelta(comparison.indexability.nonIndexable.delta)}</div>
            </div>
          </div>
          {#if comparison.indexability.becameNonIndexable?.length}
            <div class="mt-3 pt-3 border-t border-border/30">
              <div class="text-[10px] text-red-400 font-medium mb-1">Became Non-Indexable ({comparison.indexability.becameNonIndexable.length})</div>
              <div class="max-h-32 overflow-y-auto space-y-0.5">
                {#each comparison.indexability.becameNonIndexable.slice(0, 20) as url}
                  <div class="font-mono text-[10px] text-fg-2 truncate">{url}</div>
                {/each}
              </div>
            </div>
          {/if}
        </div>

        <!-- Redirects & Security -->
        <div class="rounded-xl border border-border bg-surface-2 p-4">
          <div class="text-xs text-fg-2 mb-3 font-medium">Redirects, Links & Security</div>
          <div class="space-y-2">
            {#each [
              { label: 'Redirect Chains', d: comparison.redirects.redirectChains, inv: true },
              { label: 'Long Chains (>2 hops)', d: comparison.redirects.longChains, inv: true },
              { label: 'Broken Links', d: comparison.httpStatus.brokenLinks, inv: true },
              { label: 'Soft 404s', d: comparison.httpStatus.soft404s, inv: true },
              { label: 'Non-HTTPS Pages', d: comparison.security.nonHttps, inv: true },
              { label: 'Mixed Content Pages', d: comparison.security.mixedContent, inv: true },
              { label: 'Hreflang Issues', d: comparison.hreflang.issues, inv: true },
            ] as row}
              <div class="flex items-center justify-between text-xs py-1 border-b border-border/30">
                <span class="text-fg-2">{row.label}</span>
                <div class="flex items-center gap-3">
                  <span class="text-fg-2 w-14 text-right">{row.d.old.toLocaleString()}</span>
                  <span class="text-fg-2">→</span>
                  <span class="w-14 text-right font-medium">{row.d.new.toLocaleString()}</span>
                  <span class="w-16 text-right font-medium {deltaBadge(row.d.delta, row.inv)}">{row.d.delta !== 0 ? formatDelta(row.d.delta) : '—'}</span>
                </div>
              </div>
            {/each}
          </div>
        </div>

        {#if comparison.sitemap}
          <div class="rounded-xl border border-border bg-surface-2 p-4">
            <div class="text-xs text-fg-2 mb-3 font-medium">Sitemap Health</div>
            <div class="space-y-2">
              {#each [
                { label: 'Total Sitemap URLs', d: comparison.sitemap.totalSitemapUrls, inv: false },
                { label: 'Missing from Sitemap', d: comparison.sitemap.missingFromSitemap, inv: true },
                { label: 'Non-Indexable in Sitemap', d: comparison.sitemap.nonIndexableInSitemap, inv: true },
              ] as row}
                <div class="flex items-center justify-between text-xs py-1 border-b border-border/30">
                  <span class="text-fg-2">{row.label}</span>
                  <div class="flex items-center gap-3">
                    <span class="text-fg-2 w-12 text-right">{row.d.old}</span>
                    <span class="text-fg-2">→</span>
                    <span class="w-12 text-right font-medium">{row.d.new}</span>
                    <span class="w-16 text-right font-medium {deltaBadge(row.d.delta, row.inv)}">{row.d.delta !== 0 ? formatDelta(row.d.delta) : '—'}</span>
                  </div>
                </div>
              {/each}
            </div>
          </div>
        {/if}
      </div>

    <!-- PERFORMANCE TAB -->
    {:else if activeTab === 'performance'}
      <div class="space-y-4">
        <div class="rounded-xl border border-border bg-surface-2 p-4">
          <div class="text-xs text-fg-2 mb-3 font-medium">Response Time</div>
          <div class="grid grid-cols-3 gap-3 text-center text-xs">
            {#each [
              { label: 'p50', d: comparison.performance.responseP50 },
              { label: 'p90', d: comparison.performance.responseP90 },
              { label: 'p99', d: comparison.performance.responseP99 },
            ] as metric}
              <div>
                <div class="text-fg-2">{metric.label}</div>
                <div class="font-bold mt-1">{metric.d.old.toFixed(0)}ms → {metric.d.new.toFixed(0)}ms</div>
                <div class="{deltaBadge(metric.d.delta, true)}">{formatDelta(Math.round(metric.d.delta), 'ms')}</div>
              </div>
            {/each}
          </div>
        </div>

        <div class="rounded-xl border border-border bg-surface-2 p-4">
          <div class="text-xs text-fg-2 mb-3 font-medium">Page Speed</div>
          <div class="space-y-2">
            <div class="flex items-center justify-between text-xs py-1 border-b border-border/30">
              <span class="text-fg-2">Slow Pages (>3s)</span>
              <div class="flex items-center gap-3">
                <span class="text-fg-2 w-12 text-right">{comparison.performance.slowPages.old}</span>
                <span class="text-fg-2">→</span>
                <span class="w-12 text-right font-medium">{comparison.performance.slowPages.new}</span>
                <span class="w-16 text-right font-medium {deltaBadge(comparison.performance.slowPages.delta, true)}">{comparison.performance.slowPages.delta !== 0 ? formatDelta(comparison.performance.slowPages.delta) : '—'}</span>
              </div>
            </div>
            {#if comparison.performance.avgWeight}
              <div class="flex items-center justify-between text-xs py-1 border-b border-border/30">
                <span class="text-fg-2">Avg Page Weight</span>
                <div class="flex items-center gap-3">
                  <span class="text-fg-2 w-16 text-right">{(comparison.performance.avgWeight.old / 1024).toFixed(0)}KB</span>
                  <span class="text-fg-2">→</span>
                  <span class="w-16 text-right font-medium">{(comparison.performance.avgWeight.new / 1024).toFixed(0)}KB</span>
                  <span class="w-16 text-right font-medium {deltaBadge(comparison.performance.avgWeight.delta, true)}">{comparison.performance.avgWeight.delta !== 0 ? formatDelta(Math.round(comparison.performance.avgWeight.delta / 1024), 'KB') : '—'}</span>
                </div>
              </div>
            {/if}
          </div>
        </div>
      </div>

    <!-- VISIBILITY TAB -->
    {:else if activeTab === 'visibility' && comparison.visibility}
      <div class="space-y-4">
        <div class="rounded-xl border border-border bg-surface-2 p-4">
          <div class="text-xs text-fg-2 mb-3 font-medium">Search Visibility (GSC)</div>
          <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 text-center text-xs">
            <div>
              <div class="text-fg-2">Impressions</div>
              <div class="font-bold mt-1">{comparison.visibility.impressions.new.toLocaleString()}</div>
              <div class="{deltaBadge(comparison.visibility.impressions.delta)}">{formatDelta(comparison.visibility.impressions.delta)}</div>
            </div>
            <div>
              <div class="text-fg-2">Clicks</div>
              <div class="font-bold mt-1">{comparison.visibility.clicks.new.toLocaleString()}</div>
              <div class="{deltaBadge(comparison.visibility.clicks.delta)}">{formatDelta(comparison.visibility.clicks.delta)}</div>
            </div>
            <div>
              <div class="text-fg-2">Avg CTR</div>
              <div class="font-bold mt-1">{(comparison.visibility.avgCtr.new * 100).toFixed(1)}%</div>
              <div class="{deltaBadge(comparison.visibility.avgCtr.delta)}">{comparison.visibility.avgCtr.delta > 0 ? '+' : ''}{(comparison.visibility.avgCtr.delta * 100).toFixed(2)}%</div>
            </div>
            <div>
              <div class="text-fg-2">Zombie Pages</div>
              <div class="font-bold mt-1">{comparison.visibility.zombiePages.new.toLocaleString()}</div>
              <div class="{deltaBadge(comparison.visibility.zombiePages.delta, true)}">{formatDelta(comparison.visibility.zombiePages.delta)}</div>
            </div>
          </div>
        </div>

        {#if comparison.visibility.winners?.length || comparison.visibility.losers?.length}
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
            {#if comparison.visibility.winners?.length}
              <div class="rounded-xl border border-border bg-surface-2 p-4">
                <div class="text-xs text-green-400 font-medium mb-3">Traffic Winners (Clicks)</div>
                <div class="space-y-1 max-h-64 overflow-y-auto">
                  {#each comparison.visibility.winners as w}
                    <div class="flex items-center gap-2 text-xs py-1">
                      <span class="font-mono text-fg-2 truncate flex-1">{w.url}</span>
                      <span class="shrink-0 text-green-400 font-medium">+{w.delta}</span>
                    </div>
                  {/each}
                </div>
              </div>
            {/if}
            {#if comparison.visibility.losers?.length}
              <div class="rounded-xl border border-border bg-surface-2 p-4">
                <div class="text-xs text-red-400 font-medium mb-3">Traffic Losers (Clicks)</div>
                <div class="space-y-1 max-h-64 overflow-y-auto">
                  {#each comparison.visibility.losers as l}
                    <div class="flex items-center gap-2 text-xs py-1">
                      <span class="font-mono text-fg-2 truncate flex-1">{l.url}</span>
                      <span class="shrink-0 text-red-400 font-medium">{l.delta}</span>
                    </div>
                  {/each}
                </div>
              </div>
            {/if}
          </div>
        {/if}
      </div>

    <!-- SEGMENTS TAB -->
    {:else if activeTab === 'segments' && comparison.segments?.length}
      <div class="rounded-xl border border-border bg-surface-2 overflow-hidden">
        <div class="overflow-x-auto">
          <table class="w-full text-xs">
            <thead>
              <tr class="border-b border-border bg-surface-3">
                <th class="text-left p-3 font-medium text-fg-2">Segment</th>
                <th class="text-right p-3 font-medium text-fg-2">Pages</th>
                <th class="text-right p-3 font-medium text-fg-2">Indexable</th>
                <th class="text-right p-3 font-medium text-fg-2">Avg Words</th>
                <th class="text-right p-3 font-medium text-fg-2">Avg Response</th>
                <th class="text-right p-3 font-medium text-fg-2">Clicks</th>
              </tr>
            </thead>
            <tbody>
              {#each comparison.segments as seg}
                <tr class="border-b border-border/30 hover:bg-surface-3">
                  <td class="p-3 font-medium">{seg.name}</td>
                  <td class="p-3 text-right">
                    {seg.pages.new}
                    <span class="ml-1 {deltaBadge(seg.pages.delta)}">{seg.pages.delta !== 0 ? formatDelta(seg.pages.delta) : ''}</span>
                  </td>
                  <td class="p-3 text-right">
                    {seg.indexable.new}
                    <span class="ml-1 {deltaBadge(seg.indexable.delta)}">{seg.indexable.delta !== 0 ? formatDelta(seg.indexable.delta) : ''}</span>
                  </td>
                  <td class="p-3 text-right">
                    {seg.avgWordCount.new.toFixed(0)}
                    <span class="ml-1 {deltaBadge(seg.avgWordCount.delta)}">{seg.avgWordCount.delta !== 0 ? formatDelta(Math.round(seg.avgWordCount.delta)) : ''}</span>
                  </td>
                  <td class="p-3 text-right">
                    {seg.avgResponseMs.new.toFixed(0)}ms
                    <span class="ml-1 {deltaBadge(seg.avgResponseMs.delta, true)}">{seg.avgResponseMs.delta !== 0 ? formatDelta(Math.round(seg.avgResponseMs.delta), 'ms') : ''}</span>
                  </td>
                  <td class="p-3 text-right">
                    {seg.clicks.new.toLocaleString()}
                    <span class="ml-1 {deltaBadge(seg.clicks.delta)}">{seg.clicks.delta !== 0 ? formatDelta(seg.clicks.delta) : ''}</span>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}
  {/if}
</div>
