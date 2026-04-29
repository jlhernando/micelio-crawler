<script lang="ts">
  import { api } from '../../api';
  import { onMount } from 'svelte';

  let {
    crawlId,
    pageCount = 0,
    pagesReady = true,
    loadingProgress = 0,
    initialFilterStatus = 'all',
    initialFilterTemplate = 'all',
    initialFilterIndexable = 'all',
    initialFilterClassification = 'all',
    initialFilterRobotsBlocked = 'all',
    filterGeneration = 0,
  }: {
    crawlId: string;
    pageCount?: number;
    pagesReady?: boolean;
    loadingProgress?: number;
    initialFilterStatus?: string;
    initialFilterTemplate?: string;
    initialFilterIndexable?: string;
    initialFilterClassification?: string;
    initialFilterRobotsBlocked?: string;
    filterGeneration?: number;
  } = $props();

  // ── Constants ──
  const PAGE_SIZE = 100;
  const SCROLL_CONTAINER_HEIGHT = 650;

  // ── Sort & Filter State ──
  let sortKey = $state<string>('url');
  let sortDir = $state<'asc' | 'desc'>('asc');
  let filterStatus = $state<string>('all');
  let filterTemplate = $state<string>('all');
  let filterIndexable = $state<string>('all');
  let filterClassification = $state<string>('all');
  let filterRobotsBlocked = $state<string>('all');
  let searchInput = $state('');
  let debouncedSearch = $state('');

  // ── Data state ──
  let totalFiltered = $state(0);
  let totalAll = $state(0);
  $effect(() => { if (totalAll === 0 && pageCount > 0) totalAll = pageCount; });
  let templateTypes = $state<string[]>([]);
  let initialLoading = $state(true);
  let loadingMore = $state(false);
  let error = $state<string | null>(null);
  let rows = $state<Record<string, unknown>[]>([]);
  let loadedPages = $state(0);

  // Plain (non-reactive) fetch lock to prevent re-entrant calls
  let fetchLock = false;
  let filterSeq = 0;

  // ── Scroll container ──
  let scrollContainer: HTMLElement | undefined = $state();
  let sentinel: HTMLElement | undefined = $state();

  // ── Expanded row ──
  let expandedUrl = $state<string | null>(null);
  let inlinkSources = $state<string[]>([]);
  let inlinkLoading = $state(false);

  // Sync filters from parent (SummaryHeader clicks)
  let lastGeneration = $state(0);
  $effect(() => {
    if (filterGeneration > lastGeneration) {
      filterStatus = initialFilterStatus;
      filterTemplate = initialFilterTemplate;
      filterIndexable = initialFilterIndexable;
      filterClassification = initialFilterClassification;
      filterRobotsBlocked = initialFilterRobotsBlocked;
      lastGeneration = filterGeneration;
    }
  });

  // Debounce search input
  let searchTimer: ReturnType<typeof setTimeout> | null = null;
  $effect(() => {
    const q = searchInput;
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      debouncedSearch = q;
    }, 300);
  });

  function getFilterParams() {
    return {
      sort: sortKey,
      order: sortDir,
      search: debouncedSearch,
      status: filterStatus,
      indexable: filterIndexable,
      template: filterTemplate,
      classification: filterClassification,
      robotsBlocked: filterRobotsBlocked,
    };
  }

  // Fetch and append the next page of results
  async function fetchNextPage() {
    if (fetchLock) return;
    fetchLock = true;
    loadingMore = true;
    const seq = filterSeq;
    const offset = loadedPages;
    try {
      const data = await api.getCrawlPages(crawlId, {
        offset: offset * PAGE_SIZE,
        limit: PAGE_SIZE,
        ...getFilterParams(),
      });
      if (filterSeq !== seq) return; // stale
      const newRows = data.pages || [];
      rows = [...rows, ...newRows];
      totalFiltered = data.total;
      totalAll = data.totalAll;
      if (data.templateTypes) templateTypes = data.templateTypes;
      loadedPages = offset + 1;
    } catch (err: unknown) {
      if (filterSeq !== seq) return;
      error = (err as Error)?.message || 'Failed to load pages';
    } finally {
      fetchLock = false;
      loadingMore = false;
      initialLoading = false;
    }
  }

  // Reset and fetch first page — called by the filter effect and jumpToPage
  function resetAndFetch(startPage: number = 0) {
    filterSeq++;
    fetchLock = false;
    rows = [];
    loadedPages = startPage;
    expandedUrl = null;
    initialLoading = true;
    error = null;
    if (scrollContainer) scrollContainer.scrollTop = 0;
    fetchNextPage();
  }

  // When filters/sort change or pagesReady transitions, reset and fetch page 0.
  // We read the filter params to subscribe, then schedule the reset
  // via queueMicrotask to avoid writing reactive state inside $effect.
  $effect(() => {
    // Read all filter deps + pagesReady to subscribe to changes
    const _params = getFilterParams();
    const _ready = pagesReady;
    // Break out of the reactive tracking context before writing state
    queueMicrotask(() => resetAndFetch(0));
  });

  // ── Pagination math ──
  let totalPages = $derived(Math.max(1, Math.ceil(totalFiltered / PAGE_SIZE)));
  let hasMore = $derived(loadedPages < totalPages);

  // Jump to a specific page (replaces rows, loads from that page)
  function jumpToPage(page: number) {
    resetAndFetch(page);
  }

  // ── IntersectionObserver for infinite scroll ──
  let observer: IntersectionObserver | null = null;

  onMount(() => {
    observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (entry?.isIntersecting && !fetchLock && !initialLoading) {
          fetchNextPage();
        }
      },
      {
        root: scrollContainer,
        rootMargin: '200px',
        threshold: 0,
      }
    );

    return () => {
      observer?.disconnect();
      if (searchTimer) clearTimeout(searchTimer);
    };
  });

  // Observe/unobserve sentinel when it appears
  $effect(() => {
    if (sentinel && observer) {
      observer.observe(sentinel);
      return () => observer?.unobserve(sentinel!);
    }
  });

  // ── Sorting ──
  function setSort(key: string) {
    if (sortKey === key) {
      sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
      sortKey = key;
      sortDir = 'asc';
    }
  }

  function sortIndicator(key: string): string {
    if (sortKey !== key) return '';
    return sortDir === 'asc' ? ' ↑' : ' ↓';
  }

  function formatBytes(bytes: number): string {
    if (bytes < 1024) return `${bytes}B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
  }

  function statusColor(code: number): string {
    if (code >= 200 && code < 300) return 'text-success';
    if (code >= 300 && code < 400) return 'text-warning';
    if (code >= 400 && code < 500) return 'text-orange-500';
    if (code >= 500) return 'text-danger';
    return 'text-fg-2';
  }

  let hasActiveFilters = $derived(
    filterStatus !== 'all' || filterTemplate !== 'all' || filterIndexable !== 'all' || filterClassification !== 'all' || filterRobotsBlocked !== 'all' || debouncedSearch !== ''
  );

  function clearFilters() {
    filterStatus = 'all';
    filterTemplate = 'all';
    filterIndexable = 'all';
    filterClassification = 'all';
    filterRobotsBlocked = 'all';
    searchInput = '';
    debouncedSearch = '';
  }

  let exportUrl = $derived.by(() => {
    const params = new URLSearchParams();
    const fp = getFilterParams();
    if (fp.sort) params.set('sort', fp.sort);
    if (fp.order) params.set('order', fp.order);
    if (fp.search) params.set('search', fp.search);
    if (fp.status && fp.status !== 'all') params.set('status', fp.status);
    if (fp.indexable && fp.indexable !== 'all') params.set('indexable', fp.indexable);
    if (fp.template && fp.template !== 'all') params.set('template', fp.template);
    if (fp.classification && fp.classification !== 'all') params.set('classification', fp.classification);
    if (fp.robotsBlocked && fp.robotsBlocked !== 'all') params.set('robotsBlocked', fp.robotsBlocked);
    const qs = params.toString();
    return `/api/crawl/${crawlId}/export/filtered-csv${qs ? '?' + qs : ''}`;
  });

  function toggleRow(url: string) {
    if (expandedUrl === url) {
      expandedUrl = null;
    } else {
      expandedUrl = url;
      inlinkSources = [];
      inlinkLoading = true;
      api.getPageInlinks(crawlId, url).then((data) => {
        if (expandedUrl !== url) return;
        inlinkSources = data.sources || [];
        inlinkLoading = false;
      }).catch(() => {
        if (expandedUrl !== url) return;
        inlinkSources = [];
        inlinkLoading = false;
      });
    }
  }

  // Generate page numbers for jump pagination (max 7 visible)
  let currentDisplayPage = $derived(loadedPages > 0 ? loadedPages - 1 : 0);
  let pageNumbers = $derived.by(() => {
    const pages: (number | 'ellipsis')[] = [];
    const cp = currentDisplayPage;
    if (totalPages <= 7) {
      for (let i = 0; i < totalPages; i++) pages.push(i);
    } else {
      pages.push(0);
      if (cp > 3) pages.push('ellipsis');
      const start = Math.max(1, cp - 1);
      const end = Math.min(totalPages - 2, cp + 1);
      for (let i = start; i <= end; i++) pages.push(i);
      if (cp < totalPages - 4) pages.push('ellipsis');
      pages.push(totalPages - 1);
    }
    return pages;
  });
</script>

<div class="rounded-md border border-border bg-surface-2 overflow-hidden">
  <!-- Toolbar -->
  <div class="flex flex-wrap items-center gap-3 p-4 border-b border-border">
    <input
      type="text"
      placeholder="Search URLs..."
      class="flex-1 min-w-48 px-3 py-1.5 rounded-md bg-surface-3 border border-border text-sm text-fg-1 placeholder:text-fg-2 outline-none focus:border-accent"
      bind:value={searchInput}
    />
    <select
      class="px-3 py-1.5 rounded-md bg-surface-3 border border-border text-sm text-fg-1 outline-none cursor-pointer"
      bind:value={filterStatus}
    >
      <option value="all">All Status</option>
      <option value="2xx">2xx</option>
      <option value="3xx">3xx</option>
      <option value="4xx">4xx</option>
      <option value="5xx">5xx</option>
    </select>
    <select
      class="px-3 py-1.5 rounded-md bg-surface-3 border border-border text-sm text-fg-1 outline-none cursor-pointer"
      bind:value={filterIndexable}
    >
      <option value="all">All Indexability</option>
      <option value="yes">Indexable</option>
      <option value="no">Non-Indexable</option>
    </select>
    <select
      class="px-3 py-1.5 rounded-md bg-surface-3 border border-border text-sm text-fg-1 outline-none cursor-pointer"
      bind:value={filterRobotsBlocked}
    >
      <option value="all">All Robots</option>
      <option value="yes">Blocked</option>
      <option value="no">Not Blocked</option>
    </select>
    {#if templateTypes.length > 1}
      <select
        class="px-3 py-1.5 rounded-md bg-surface-3 border border-border text-sm text-fg-1 outline-none cursor-pointer"
        bind:value={filterTemplate}
      >
        <option value="all">All Types</option>
        {#each templateTypes as t}
          <option value={t}>{t}</option>
        {/each}
      </select>
    {/if}
    {#if hasActiveFilters}
      <button
        type="button"
        class="px-3 py-1.5 rounded-md text-sm font-medium bg-accent/15 text-accent border border-accent/30 cursor-pointer hover:bg-accent/25 transition-colors"
        onclick={clearFilters}
      >Clear Filters</button>
    {/if}
    <a
      href={exportUrl}
      download
      class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm font-medium text-fg-2 border border-border cursor-pointer hover:bg-surface-3 hover:text-fg-1 transition-colors no-underline {initialLoading || totalFiltered === 0 ? 'opacity-50 pointer-events-none' : ''}"
      title={hasActiveFilters ? `Export ${totalFiltered.toLocaleString()} filtered URLs` : `Export all ${totalFiltered.toLocaleString()} URLs`}
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/><polyline points="10 9 9 9 8 9"/></svg>
      Export CSV
    </a>
    <span class="text-xs text-fg-2">
      {#if initialLoading}
        Loading...
      {:else if error}
        <span class="text-danger">{error}</span>
      {:else}
        Showing {rows.length.toLocaleString()} of {totalFiltered.toLocaleString()} pages
        {#if !pagesReady}
          <span class="text-accent ml-1">(indexing {loadingProgress}%)</span>
        {/if}
      {/if}
    </span>
  </div>

  <!-- Scrollable table container -->
  <div
    class="overflow-y-auto overflow-x-auto"
    style="max-height: {SCROLL_CONTAINER_HEIGHT}px"
    bind:this={scrollContainer}
  >
    <table class="w-full text-sm">
      <thead class="sticky top-0 z-10 bg-surface-2">
        <tr class="text-left text-xs text-fg-2 border-b border-border">
          <th class="px-4 py-2 cursor-pointer hover:text-fg-1" onclick={() => setSort('url')}>
            URL{sortIndicator('url')}
          </th>
          <th class="px-3 py-2 w-16 cursor-pointer hover:text-fg-1" onclick={() => setSort('statusCode')}>
            Status{sortIndicator('statusCode')}
          </th>
          <th class="px-3 py-2 w-20 cursor-pointer hover:text-fg-1" onclick={() => setSort('responseTimeMs')}>
            Time{sortIndicator('responseTimeMs')}
          </th>
          <th class="px-3 py-2 w-20 cursor-pointer hover:text-fg-1 hidden md:table-cell" onclick={() => setSort('bodySize')}>
            Body{sortIndicator('bodySize')}
          </th>
          <th class="px-3 py-2 w-20 cursor-pointer hover:text-fg-1 hidden md:table-cell" onclick={() => setSort('htmlBytes')}>
            HTML{sortIndicator('htmlBytes')}
          </th>
          <th class="px-3 py-2 w-20 cursor-pointer hover:text-fg-1 hidden md:table-cell" onclick={() => setSort('totalBytes')}>
            Weight{sortIndicator('totalBytes')}
          </th>
          <th class="px-3 py-2 w-48 cursor-pointer hover:text-fg-1 hidden lg:table-cell" onclick={() => setSort('title')}>
            Title{sortIndicator('title')}
          </th>
          <th class="px-3 py-2 w-20 cursor-pointer hover:text-fg-1 hidden md:table-cell" onclick={() => setSort('wordCount')}>
            Words{sortIndicator('wordCount')}
          </th>
          <th class="px-3 py-2 w-24 hidden md:table-cell">Type</th>
          <th class="px-3 py-2 w-16 cursor-pointer hover:text-fg-1 hidden sm:table-cell" onclick={() => setSort('inlinks')}>
            Inlinks{sortIndicator('inlinks')}
          </th>
          <th class="px-3 py-2 w-16 cursor-pointer hover:text-fg-1 hidden sm:table-cell" onclick={() => setSort('pageRank')}>
            PR{sortIndicator('pageRank')}
          </th>
          <th class="px-3 py-2 w-16 cursor-pointer hover:text-fg-1 hidden sm:table-cell" onclick={() => setSort('depth')}>
            Depth{sortIndicator('depth')}
          </th>
        </tr>
      </thead>
      <tbody>
        {#if initialLoading}
          <tr>
            <td colspan="11" class="px-4 py-8 text-center">
              <div class="inline-block w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
              <span class="text-fg-2 ml-2">Loading pages...</span>
            </td>
          </tr>
        {:else if rows.length === 0}
          <tr>
            <td colspan="11" class="px-4 py-8 text-center text-fg-2">No pages match your filters.</td>
          </tr>
        {:else}
          {#each rows as page}
            {@const url = page.url as string}
            {@const titleObj = page.title as { text: string; length: number } | null}
            <tr
              class="border-b border-border/50 hover:bg-surface-3 cursor-pointer transition-colors"
              onclick={() => toggleRow(url)}
            >
              <td class="px-4 py-2">
                <div class="flex items-center gap-1.5 min-w-0">
                  <svg
                    class="w-3.5 h-3.5 flex-shrink-0 text-fg-2 transition-transform duration-150 {expandedUrl === url ? 'rotate-90' : ''}"
                    viewBox="0 0 16 16" fill="currentColor"
                  >
                    <path d="M6 4l4 4-4 4" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
                  </svg>
                  <span class="font-mono text-xs truncate max-w-xs lg:max-w-lg text-accent">{url}</span>
                  <a
                    href={url}
                    target="_blank"
                    rel="noopener noreferrer"
                    class="flex-shrink-0 text-fg-2 hover:text-accent transition-colors"
                    title="Open in new tab"
                    onclick={(e: MouseEvent) => e.stopPropagation()}
                  >
                    <svg class="w-3.5 h-3.5" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                      <path d="M6.5 3.5H3.5a1 1 0 0 0-1 1v8a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V9.5"/>
                      <path d="M9.5 2.5h4v4"/>
                      <path d="M13.5 2.5L8 8"/>
                    </svg>
                  </a>
                </div>
                {#if (page.redirectChain as unknown[])?.length > 0 && page.finalUrl !== page.url}
                  <span class="text-[10px] text-fg-2 block truncate max-w-xs lg:max-w-lg pl-5">&#8594; {page.finalUrl}</span>
                {/if}
              </td>
              <td class="px-3 py-2 font-mono {statusColor(page.statusCode as number)}">
                {page.statusCode}
              </td>
              <td class="px-3 py-2 text-fg-2">{page.responseTimeMs}ms</td>
              <td class="px-3 py-2 text-fg-2 hidden md:table-cell font-mono">
                {#if (page as any).bodySize}
                  {@const bs = (page as any).bodySize as number}
                  <span class="{bs > 2_097_152 ? 'text-error' : bs > 1_500_000 ? 'text-warning' : ''}" title="{(page as any).truncatedElements?.length ? 'Googlebot truncation risk: ' + ((page as any).truncatedElements as string[]).join(', ') : ''}">{formatBytes(bs)}{#if (page as any).truncatedElements?.length} <span class="text-error" title="SEO elements beyond Googlebot 2MB cutoff">!</span>{/if}</span>
                {:else}
                  <span class="text-fg-2">—</span>
                {/if}
              </td>
              <td class="px-3 py-2 text-fg-2 hidden md:table-cell font-mono">
                {#if (page.pageWeight as any)?.htmlBytes}
                  {formatBytes((page.pageWeight as any).htmlBytes)}
                {:else}
                  <span class="text-fg-2">—</span>
                {/if}
              </td>
              <td class="px-3 py-2 text-fg-2 hidden md:table-cell font-mono">
                {#if (page.pageWeight as any)?.totalBytes}
                  <span class="{(page.pageWeight as any).totalBytes > 3_000_000 ? 'text-error' : (page.pageWeight as any).totalBytes > 1_000_000 ? 'text-warning' : ''}">{formatBytes((page.pageWeight as any).totalBytes)}</span>
                {:else}
                  <span class="text-fg-2">—</span>
                {/if}
              </td>
              <td class="px-3 py-2 text-fg-2 truncate max-w-48 hidden lg:table-cell">
                {titleObj?.text || '—'}
              </td>
              <td class="px-3 py-2 text-fg-2 hidden md:table-cell">{page.wordCount || 0}</td>
              <td class="px-3 py-2 hidden md:table-cell">
                {#if page.templateType && page.templateType !== 'other'}
                  <span class="px-1.5 py-0.5 rounded text-xs bg-surface-3">{page.templateType}</span>
                {:else}
                  <span class="text-fg-2">—</span>
                {/if}
              </td>
              <td class="px-3 py-2 text-fg-2 hidden sm:table-cell font-mono">
                {page.inlinks || 0}
              </td>
              <td class="px-3 py-2 text-fg-2 hidden sm:table-cell font-mono">
                {#if (page.pageRank as number) > 0}
                  <span class="{(page.pageRank as number) >= 5 ? 'text-success' : (page.pageRank as number) >= 2 ? 'text-fg' : 'text-fg-2'}">{(page.pageRank as number).toFixed(1)}</span>
                {:else}
                  <span class="text-fg-2">—</span>
                {/if}
              </td>
              <td class="px-3 py-2 text-fg-2 hidden sm:table-cell">{page.depth}</td>
            </tr>
            <!-- Expanded detail -->
            {#if expandedUrl === url}
              {@const tpc = page.topicality as { titleBodyOverlap?: number; titleH1Alignment?: number; headingBodyCoverage?: number; topicalConsistency?: number } | null}
              {@const cr = page.contentRichness as { richnessScore?: number; informationDensity?: number; headingCount?: number; headingDepth?: number; listCount?: number; listItemCount?: number; tableCount?: number; imageInContent?: number; videoEmbeds?: number; codeBlocks?: number; blockquoteCount?: number } | null}
              {@const ee = page.eeat as { authorName?: string; authorSchemaType?: string; citationCount?: number; externalRefCount?: number; hasAuthor?: boolean; hasAuthorPage?: boolean; hasAboutPage?: boolean; hasContactPage?: boolean; hasEditorialPolicy?: boolean } | null}
              {@const pr = page.passageReadiness as { passageScore?: number; avgWordsPerSection?: number; sectionsCount?: number; hasDirectAnswers?: number; hasDefinitions?: boolean; hasFaqStructure?: boolean; hasHowToStructure?: boolean } | null}
              {@const ai = page.aiReadiness as { aiReadinessScore?: number; topPassageLength?: number; hasConciseDefinition?: boolean; hasStructuredAnswer?: boolean; hasFaqSchema?: boolean; hasHowToSchema?: boolean } | null}
              {@const fr = page.freshness as { contentAgeDays?: number; bylineDate?: string; modifiedDate?: string; dateConsistency?: boolean } | null}
              {@const os = page.originalityScore as number | null}
              <tr class="bg-surface-3/50">
                <td colspan="11" class="px-4 py-3">
                  <div class="grid grid-cols-2 md:grid-cols-4 gap-3 text-xs">
                    <div>
                      <span class="text-fg-2">Final URL:</span>
                      <span class="font-mono block truncate">{page.finalUrl || page.url}</span>
                    </div>
                    <div>
                      <span class="text-fg-2">Canonical:</span>
                      <span class="font-mono block truncate">{page.canonical || '—'}</span>
                    </div>
                    <div>
                      <span class="text-fg-2">Meta Robots:</span>
                      <span class="block">{page.metaRobots || '—'}</span>
                    </div>
                    <div>
                      <span class="text-fg-2">Indexable:</span>
                      <span class="block">{(page.indexability as { indexable: boolean })?.indexable ? 'Yes' : 'No'}</span>
                    </div>
                    <div>
                      <span class="text-fg-2">Title ({titleObj?.length || 0} chars):</span>
                      <span class="block truncate">{titleObj?.text || '—'}</span>
                    </div>
                    <div>
                      <span class="text-fg-2">Description:</span>
                      <span class="block truncate">{(page.metaDescription as { text: string } | null)?.text || '—'}</span>
                    </div>
                    <div>
                      <span class="text-fg-2">H1:</span>
                      <span class="block truncate">{(page.headings as { h1: string[] })?.h1?.[0] || '—'}</span>
                    </div>
                    <div>
                      <span class="text-fg-2">Outlinks:</span>
                      <span class="block">{(page.internalLinks as string[])?.length || page.internalLinkCount || 0} int / {(page.externalLinks as string[])?.length || page.externalLinkCount || 0} ext</span>
                    </div>
                  </div>
                  <!-- Signals -->
                  {#if tpc || cr || ee || pr || ai || fr || os}
                    <div class="mt-2 pt-2 border-t border-border/50 space-y-2">
                      <span class="text-xs text-fg-2 font-medium">Ranking Signals</span>
                      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2">
                        {#if tpc}
                          <div class="bg-surface-3 rounded px-2.5 py-2">
                            <span class="text-[10px] text-fg-2 font-medium block mb-1">T* Topicality</span>
                            <div class="grid grid-cols-2 gap-x-3 gap-y-0.5 text-xs">
                              <span class="text-fg-2">Consistency</span><span class="font-mono text-right {(tpc.topicalConsistency ?? 0) >= 0.7 ? 'text-success' : (tpc.topicalConsistency ?? 0) >= 0.4 ? 'text-warning' : 'text-danger'}">{tpc.topicalConsistency?.toFixed(2) ?? '—'}</span>
                              <span class="text-fg-2">Title-Body</span><span class="font-mono text-right">{tpc.titleBodyOverlap?.toFixed(2) ?? '—'}</span>
                              <span class="text-fg-2">Title-H1</span><span class="font-mono text-right">{tpc.titleH1Alignment?.toFixed(2) ?? '—'}</span>
                              <span class="text-fg-2">Heading-Body</span><span class="font-mono text-right">{tpc.headingBodyCoverage?.toFixed(2) ?? '—'}</span>
                            </div>
                          </div>
                        {/if}
                        {#if cr}
                          <div class="bg-surface-3 rounded px-2.5 py-2">
                            <span class="text-[10px] text-fg-2 font-medium block mb-1">Content Richness</span>
                            <div class="grid grid-cols-2 gap-x-3 gap-y-0.5 text-xs">
                              <span class="text-fg-2">Score</span><span class="font-mono text-right {(cr.richnessScore ?? 0) >= 0.7 ? 'text-success' : (cr.richnessScore ?? 0) >= 0.4 ? 'text-warning' : 'text-danger'}">{cr.richnessScore?.toFixed(2) ?? '—'}</span>
                              <span class="text-fg-2">Info Density</span><span class="font-mono text-right">{cr.informationDensity?.toFixed(3) ?? '—'}</span>
                              <span class="text-fg-2">Headings</span><span class="font-mono text-right">{cr.headingCount ?? 0} (depth {cr.headingDepth ?? 0})</span>
                              <span class="text-fg-2">Lists</span><span class="font-mono text-right">{cr.listCount ?? 0} ({cr.listItemCount ?? 0} items)</span>
                              <span class="text-fg-2">Tables</span><span class="font-mono text-right">{cr.tableCount ?? 0}</span>
                              <span class="text-fg-2">Images</span><span class="font-mono text-right">{cr.imageInContent ?? 0}</span>
                              {#if (cr.codeBlocks ?? 0) > 0}<span class="text-fg-2">Code Blocks</span><span class="font-mono text-right">{cr.codeBlocks}</span>{/if}
                              {#if (cr.videoEmbeds ?? 0) > 0}<span class="text-fg-2">Videos</span><span class="font-mono text-right">{cr.videoEmbeds}</span>{/if}
                              {#if (cr.blockquoteCount ?? 0) > 0}<span class="text-fg-2">Blockquotes</span><span class="font-mono text-right">{cr.blockquoteCount}</span>{/if}
                            </div>
                          </div>
                        {/if}
                        {#if pr}
                          <div class="bg-surface-3 rounded px-2.5 py-2">
                            <span class="text-[10px] text-fg-2 font-medium block mb-1">Passage Readiness</span>
                            <div class="grid grid-cols-2 gap-x-3 gap-y-0.5 text-xs">
                              <span class="text-fg-2">Score</span><span class="font-mono text-right {(pr.passageScore ?? 0) >= 0.7 ? 'text-success' : (pr.passageScore ?? 0) >= 0.4 ? 'text-warning' : 'text-danger'}">{pr.passageScore?.toFixed(2) ?? '—'}</span>
                              <span class="text-fg-2">Sections</span><span class="font-mono text-right">{pr.sectionsCount ?? 0}</span>
                              <span class="text-fg-2">Avg Words/Sec</span><span class="font-mono text-right">{pr.avgWordsPerSection?.toFixed(0) ?? '—'}</span>
                              <span class="text-fg-2">Direct Answers</span><span class="font-mono text-right">{pr.hasDirectAnswers ?? 0}</span>
                              <span class="text-fg-2">Definitions</span><span class="font-mono text-right">{pr.hasDefinitions ? 'Yes' : 'No'}</span>
                              <span class="text-fg-2">FAQ Structure</span><span class="font-mono text-right">{pr.hasFaqStructure ? 'Yes' : 'No'}</span>
                              <span class="text-fg-2">HowTo Structure</span><span class="font-mono text-right">{pr.hasHowToStructure ? 'Yes' : 'No'}</span>
                            </div>
                          </div>
                        {/if}
                        {#if ai}
                          <div class="bg-surface-3 rounded px-2.5 py-2">
                            <span class="text-[10px] text-fg-2 font-medium block mb-1">AI Readiness</span>
                            <div class="grid grid-cols-2 gap-x-3 gap-y-0.5 text-xs">
                              <span class="text-fg-2">Score</span><span class="font-mono text-right {(ai.aiReadinessScore ?? 0) >= 0.7 ? 'text-success' : (ai.aiReadinessScore ?? 0) >= 0.4 ? 'text-warning' : 'text-danger'}">{ai.aiReadinessScore?.toFixed(2) ?? '—'}</span>
                              <span class="text-fg-2">Top Passage</span><span class="font-mono text-right">{ai.topPassageLength ?? 0} words</span>
                              <span class="text-fg-2">Concise Def</span><span class="font-mono text-right">{ai.hasConciseDefinition ? 'Yes' : 'No'}</span>
                              <span class="text-fg-2">Structured Ans</span><span class="font-mono text-right">{ai.hasStructuredAnswer ? 'Yes' : 'No'}</span>
                              <span class="text-fg-2">FAQ Schema</span><span class="font-mono text-right">{ai.hasFaqSchema ? 'Yes' : 'No'}</span>
                              <span class="text-fg-2">HowTo Schema</span><span class="font-mono text-right">{ai.hasHowToSchema ? 'Yes' : 'No'}</span>
                            </div>
                          </div>
                        {/if}
                        {#if ee}
                          <div class="bg-surface-3 rounded px-2.5 py-2">
                            <span class="text-[10px] text-fg-2 font-medium block mb-1">E-E-A-T</span>
                            <div class="grid grid-cols-2 gap-x-3 gap-y-0.5 text-xs">
                              {#if ee.authorName}<span class="text-fg-2">Author</span><span class="truncate text-right">{ee.authorName}</span>{/if}
                              {#if ee.authorSchemaType}<span class="text-fg-2">Schema Type</span><span class="font-mono text-right">{ee.authorSchemaType}</span>{/if}
                              <span class="text-fg-2">Has Author</span><span class="font-mono text-right">{ee.hasAuthor ? 'Yes' : 'No'}</span>
                              <span class="text-fg-2">Author Page</span><span class="font-mono text-right">{ee.hasAuthorPage ? 'Yes' : 'No'}</span>
                              <span class="text-fg-2">About Page</span><span class="font-mono text-right">{ee.hasAboutPage ? 'Yes' : 'No'}</span>
                              <span class="text-fg-2">Contact Page</span><span class="font-mono text-right">{ee.hasContactPage ? 'Yes' : 'No'}</span>
                              <span class="text-fg-2">Editorial Policy</span><span class="font-mono text-right">{ee.hasEditorialPolicy ? 'Yes' : 'No'}</span>
                              <span class="text-fg-2">Citations</span><span class="font-mono text-right">{ee.citationCount ?? 0}</span>
                              <span class="text-fg-2">External Refs</span><span class="font-mono text-right">{ee.externalRefCount ?? 0}</span>
                            </div>
                          </div>
                        {/if}
                        {#if fr || os != null}
                          <div class="bg-surface-3 rounded px-2.5 py-2">
                            <span class="text-[10px] text-fg-2 font-medium block mb-1">Freshness & Originality</span>
                            <div class="grid grid-cols-2 gap-x-3 gap-y-0.5 text-xs">
                              {#if os != null}<span class="text-fg-2">Originality</span><span class="font-mono text-right {os >= 90 ? 'text-success' : os >= 50 ? 'text-warning' : 'text-danger'}">{os}/127</span>{/if}
                              {#if fr?.contentAgeDays != null}<span class="text-fg-2">Content Age</span><span class="font-mono text-right">{fr.contentAgeDays}d</span>{/if}
                              {#if fr?.bylineDate}<span class="text-fg-2">Published</span><span class="font-mono text-right">{fr.bylineDate.slice(0, 10)}</span>{/if}
                              {#if fr?.modifiedDate}<span class="text-fg-2">Modified</span><span class="font-mono text-right">{fr.modifiedDate.slice(0, 10)}</span>{/if}
                              {#if fr?.dateConsistency != null}<span class="text-fg-2">Date Consistent</span><span class="font-mono text-right">{fr.dateConsistency ? 'Yes' : 'No'}</span>{/if}
                            </div>
                          </div>
                        {/if}
                      </div>
                    </div>
                  {/if}
                  <!-- Inlinks -->
                  {#if inlinkLoading}
                    <div class="mt-2 pt-2 border-t border-border/50 text-xs text-fg-2">Loading inlinks...</div>
                  {:else if inlinkSources.length > 0}
                    <div class="mt-2 pt-2 border-t border-border/50">
                      <span class="text-xs text-fg-2">Inlinks ({inlinkSources.length} pages link here):</span>
                      <div class="flex flex-wrap gap-1.5 mt-1">
                        {#each inlinkSources.slice(0, 10) as src}
                          <span class="text-[11px] font-mono text-accent bg-surface-3 px-1.5 py-0.5 rounded truncate max-w-xs">{src}</span>
                        {/each}
                        {#if inlinkSources.length > 10}
                          <span class="text-[11px] text-fg-2">+{inlinkSources.length - 10} more</span>
                        {/if}
                      </div>
                    </div>
                  {/if}
                </td>
              </tr>
            {/if}
          {/each}

          <!-- Sentinel + loading indicator for infinite scroll -->
          {#if hasMore}
            <tr bind:this={sentinel}>
              <td colspan="11" class="px-4 py-4 text-center">
                {#if loadingMore}
                  <div class="flex items-center justify-center gap-2">
                    <div class="w-4 h-4 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
                    <span class="text-xs text-fg-2">Loading next {PAGE_SIZE} rows...</span>
                  </div>
                {:else}
                  <span class="text-xs text-fg-2">Scroll down to load more</span>
                {/if}
              </td>
            </tr>
          {/if}
        {/if}
      </tbody>
    </table>
  </div>

  <!-- Footer bar with pagination jump + status -->
  {#if rows.length > 0 && pagesReady}
    <div class="flex items-center justify-between px-4 py-2.5 border-t border-border">
      <span class="text-xs text-fg-2">
        {rows.length.toLocaleString()} of {totalFiltered.toLocaleString()} loaded
      </span>

      <div class="flex items-center gap-1">
        <span class="text-xs text-fg-2 mr-1">Jump to:</span>
        {#each pageNumbers as p}
          {#if p === 'ellipsis'}
            <span class="px-0.5 text-xs text-fg-2">...</span>
          {:else}
            <button
              type="button"
              class="min-w-[28px] px-1.5 py-1 rounded text-xs transition-colors cursor-pointer {p === currentDisplayPage ? 'bg-accent text-white font-medium' : 'text-fg-2 hover:text-fg-1 hover:bg-surface-3'}"
              onclick={() => jumpToPage(p)}
            >{(p + 1).toLocaleString()}</button>
          {/if}
        {/each}
      </div>

      <span class="text-xs text-fg-2">
        Page {(currentDisplayPage + 1).toLocaleString()} of {totalPages.toLocaleString()}
      </span>
    </div>
  {/if}
</div>
