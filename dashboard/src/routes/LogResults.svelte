<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { api } from '../lib/api';
  import BotActivityChart from '../lib/components/results/BotActivityChart.svelte';
  import { createWsClient, type WsMessage } from '../lib/ws';

  let { id }: { id: string } = $props();

  let loading = $state(true);
  let error = $state<string | null>(null);
  let tab = $state<'overview' | 'bots' | 'urls' | 'dirs' | 'hourly' | 'trends' | 'google' | 'waste' | 'merge' | 'heatmap' | 'ai'>('overview');
  let stats = $state<Record<string, unknown> | null>(null);
  let job = $state<Record<string, unknown> | null>(null);
  let botData = $state<Record<string, unknown> | null>(null);

  // Sort state
  let botSort = $state<{ col: string; asc: boolean }>({ col: 'hits', asc: false });
  let urlSort = $state<{ col: string; asc: boolean }>({ col: 'hits', asc: false });
  let urlSearch = $state('');
  let expandedBot = $state<string | null>(null);
  let trendMode = $state<'total' | 'category' | 'botvshuman'>('total');
  let statusFilter = $state<number | null>(null);

  async function load() {
    try {
      const [overview, status, bots] = await Promise.all([
        api.getLogOverview(id),
        api.getLogStatus(id),
        api.getLogBots(id),
      ]);
      stats = overview;
      job = status;
      botData = bots;
      loading = false;
    } catch (err) {
      error = (err as Error).message;
      loading = false;
    }
  }

  let wsClient: { close: () => void } | null = null;
  onMount(() => {
    load();
    wsClient = createWsClient(handleWsMessage);
    return () => {
      if (mergeTickTimer) { clearInterval(mergeTickTimer); mergeTickTimer = null; }
      wsClient?.close();
    };
  });

  // Derived data
  let totalHits = $derived((stats?.totalHits as number) || 0);
  let humanHits = $derived((stats?.humanHits as number) || 0);
  let botHitsTotal = $derived(totalHits - humanHits);
  let dateRange = $derived((stats?.dateRange as [string, string]) || ['', '']);
  let statusGroups = $derived((stats?.statusGroups as Record<string, number>) || {});
  let statusCodes = $derived((stats?.statusCodes as Record<string, number>) || {});
  let hourlyHits = $derived((stats?.hourlyHits as number[]) || new Array(24).fill(0));
  let crawlBudget = $derived((stats?.crawlBudget as Record<string, unknown>) || {});
  let topURLs = $derived((stats?.topUrls as Array<{ path: string; hits: number; botHits: number; humanHits: number; topBot: string; status: number; bytes?: number }>) || []);
  let dailyHits = $derived((stats?.dailyHits as Record<string, number>) || {});
  let botDailyHits = $derived((botData?.botDailyHits as Record<string, Record<string, number>>) || {});
  let botHourlyHits = $derived((stats?.botHourlyHits as Record<string, Record<string, number>>) || {});

  let botEntries = $derived.by(() => {
    const bots = (stats?.botHits as Record<string, { hits: number; uniqueUrls: number; category: string; mobile: boolean; firstSeen: string; lastSeen: string; bytes: number; statusCodes?: Record<string, number> }>) || {};
    let arr = Object.entries(bots).map(([name, b]) => ({ name, ...b }));
    arr.sort((a, b) => {
      const av = a[botSort.col as keyof typeof a] as number;
      const bv = b[botSort.col as keyof typeof b] as number;
      return botSort.asc ? (av > bv ? 1 : -1) : (av < bv ? 1 : -1);
    });
    return arr;
  });

  let filteredURLs = $derived.by(() => {
    let arr = topURLs;
    if (urlSearch) arr = arr.filter(u => u.path.toLowerCase().includes(urlSearch.toLowerCase()));
    if (statusFilter !== null) arr = arr.filter(u => u.status === statusFilter);
    arr = [...arr].sort((a, b) => {
      const av = a[urlSort.col as keyof typeof a] as number;
      const bv = b[urlSort.col as keyof typeof b] as number;
      return urlSort.asc ? (av > bv ? 1 : -1) : (av < bv ? 1 : -1);
    });
    return arr.slice(0, 200);
  });

  let maxHourly = $derived(Math.max(...hourlyHits, 1));

  // Hourly: compute bot vs human per hour (approximate from ratio)
  let hourlyBotRatio = $derived(totalHits > 0 ? botHitsTotal / totalHits : 0);
  let avgHourly = $derived(hourlyHits.reduce((s, v) => s + v, 0) / 24);
  let peakHour = $derived(hourlyHits.indexOf(Math.max(...hourlyHits)));

  // Daily trends: sorted dates
  let sortedDays = $derived(Object.keys(dailyHits).sort());
  let maxDaily = $derived(sortedDays.length > 0 ? Math.max(...sortedDays.map(d => dailyHits[d])) : 1);

  // Category aggregation for daily trends
  let categoryDailyHits = $derived.by(() => {
    const cats: Record<string, Record<string, number>> = {};
    const bots = (stats?.botHits as Record<string, { category: string }>) || {};
    for (const [botName, dailyMap] of Object.entries(botDailyHits)) {
      const cat = bots[botName]?.category || 'other';
      if (!cats[cat]) cats[cat] = {};
      for (const [day, hits] of Object.entries(dailyMap)) {
        cats[cat][day] = (cats[cat][day] || 0) + hits;
      }
    }
    return cats;
  });

  // Bot vs human daily
  let humanDailyHits = $derived.by(() => {
    const result: Record<string, number> = {};
    for (const day of sortedDays) {
      let botTotal = 0;
      for (const dailyMap of Object.values(botDailyHits)) {
        botTotal += (dailyMap[day] || 0);
      }
      result[day] = Math.max(0, dailyHits[day] - botTotal);
    }
    return result;
  });

  // Status codes sorted
  let sortedStatusCodes = $derived(
    Object.entries(statusCodes)
      .map(([code, count]) => ({ code: parseInt(code), count: count as number }))
      .sort((a, b) => b.count - a.count)
  );

  // Top bots by bandwidth
  let topBotsByBytes = $derived.by(() => {
    return [...botEntries].sort((a, b) => (b.bytes || 0) - (a.bytes || 0)).slice(0, 10);
  });

  // Bot category distribution for donut chart
  let categoryStats = $derived.by(() => {
    const cats: Record<string, number> = {};
    for (const bot of botEntries) {
      const cat = bot.category || 'other';
      cats[cat] = (cats[cat] || 0) + bot.hits;
    }
    return Object.entries(cats).map(([cat, hits]) => ({ cat, hits })).sort((a, b) => b.hits - a.hits);
  });

  // Crawl rate per bot (hits/hour based on active time window)
  let botCrawlRates = $derived.by(() => {
    const rates: Record<string, number> = {};
    for (const bot of botEntries) {
      if (bot.firstSeen && bot.lastSeen) {
        const ms = new Date(bot.lastSeen).getTime() - new Date(bot.firstSeen).getTime();
        const hours = Math.max(ms / 3600000, 1);
        rates[bot.name] = bot.hits / hours;
      }
    }
    return rates;
  });

  // Directory breakdown: group URLs by first path segment (strip query strings)
  let dirStats = $derived.by(() => {
    const dirs: Record<string, { total: number; bot: number; human: number; urls: number; bytes: number }> = {};
    for (const url of topURLs) {
      const pathOnly = url.path.split('?')[0];
      const parts = pathOnly.replace(/^\//, '').split('/');
      const dir = '/' + (parts[0] || '(root)');
      if (!dirs[dir]) dirs[dir] = { total: 0, bot: 0, human: 0, urls: 0, bytes: 0 };
      dirs[dir].total += url.hits;
      dirs[dir].bot += url.botHits;
      dirs[dir].human += url.humanHits;
      dirs[dir].urls++;
      dirs[dir].bytes += url.bytes || 0;
    }
    return Object.entries(dirs)
      .map(([dir, v]) => ({ dir, ...v }))
      .sort((a, b) => b.total - a.total);
  });

  // Query parameter analysis: group by parameter name
  let paramStats = $derived.by(() => {
    const params: Record<string, { total: number; bot: number; human: number; urls: number }> = {};
    for (const url of topURLs) {
      const qIdx = url.path.indexOf('?');
      if (qIdx === -1) continue;
      const qs = url.path.substring(qIdx + 1);
      const seen = new Set<string>();
      for (const part of qs.split('&')) {
        const key = decodeURIComponent(part.split('=')[0] || '').substring(0, 80);
        if (!key || seen.has(key)) continue;
        seen.add(key);
        if (!params[key]) params[key] = { total: 0, bot: 0, human: 0, urls: 0 };
        params[key].total += url.hits;
        params[key].bot += url.botHits;
        params[key].human += url.humanHits;
        params[key].urls++;
      }
    }
    return Object.entries(params)
      .map(([param, v]) => ({ param, ...v }))
      .sort((a, b) => b.total - a.total);
  });

  // Path depth analysis
  let pathDepthStats = $derived.by(() => {
    const depths: Record<number, { total: number; bot: number; human: number }> = {};
    for (const url of topURLs) {
      const depth = url.path === '/' ? 0 : url.path.replace(/\/$/, '').split('/').length - 1;
      if (!depths[depth]) depths[depth] = { total: 0, bot: 0, human: 0 };
      depths[depth].total += url.hits;
      depths[depth].bot += url.botHits;
      depths[depth].human += url.humanHits;
    }
    return Object.entries(depths).map(([d, v]) => ({ depth: parseInt(d), ...v })).sort((a, b) => a.depth - b.depth);
  });

  // Googlebot variants
  let googlebotVariants = $derived.by(() => {
    const variants: Array<{ name: string; hits: number; uniqueUrls: number; bytes: number; mobile: boolean; category: string; firstSeen: string; lastSeen: string }> = [];
    for (const bot of botEntries) {
      if (bot.name.toLowerCase().includes('google')) {
        variants.push(bot);
      }
    }
    return variants.sort((a, b) => b.hits - a.hits);
  });

  let googlebotTotalHits = $derived(googlebotVariants.reduce((s, v) => s + v.hits, 0));
  let googlebotMobileHits = $derived(googlebotVariants.filter(v => v.mobile).reduce((s, v) => s + v.hits, 0));
  let googlebotDesktopHits = $derived(googlebotTotalHits - googlebotMobileHits);

  // Anomaly detection: bots with days >2 stddev from their mean
  let anomalies = $derived.by(() => {
    if (sortedDays.length < 3) return [];
    const results: Array<{ bot: string; day: string; hits: number; mean: number; stddev: number; severity: 'warning' | 'critical' }> = [];
    for (const [botName, daily] of Object.entries(botDailyHits)) {
      const vals = sortedDays.map(d => daily[d] || 0);
      const mean = vals.reduce((s, v) => s + v, 0) / vals.length;
      if (mean < 1) continue;
      const variance = vals.reduce((s, v) => s + (v - mean) ** 2, 0) / vals.length;
      const stddev = Math.sqrt(variance);
      if (stddev < 1) continue;
      for (let i = 0; i < sortedDays.length; i++) {
        const z = (vals[i] - mean) / stddev;
        if (z > 2) {
          results.push({ bot: botName, day: sortedDays[i], hits: vals[i], mean, stddev, severity: z > 3 ? 'critical' : 'warning' });
        }
      }
    }
    // Also check total daily hits for spikes
    const totalVals = sortedDays.map(d => dailyHits[d] || 0);
    const totalMean = totalVals.reduce((s, v) => s + v, 0) / totalVals.length;
    const totalVariance = totalVals.reduce((s, v) => s + (v - totalMean) ** 2, 0) / totalVals.length;
    const totalStddev = Math.sqrt(totalVariance);
    if (totalStddev >= 1) {
      for (let i = 0; i < sortedDays.length; i++) {
        const z = (totalVals[i] - totalMean) / totalStddev;
        if (z > 2) {
          results.push({ bot: 'All Traffic', day: sortedDays[i], hits: totalVals[i], mean: totalMean, stddev: totalStddev, severity: z > 3 ? 'critical' : 'warning' });
        }
      }
    }
    return results.sort((a, b) => b.hits - a.hits).slice(0, 10);
  });

  // Heatmap data
  let heatmapData = $derived((stats?.heatmap as number[][]) || Array(7).fill(null).map(() => Array(24).fill(0)));
  let botHeatmaps = $derived((stats?.botHeatmap as Record<string, number[][]>) || {});
  let maxHeatmapVal = $derived(Math.max(...heatmapData.flat(), 1));
  let selectedHeatmapBot = $state('all');
  const dayNames = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

  // Warm gradient (cool gray → amber → orange → red) for the heatmap.
  // Uses √intensity to spread the low/mid range so 5–40% values are visibly
  // distinct instead of all looking the same shade.
  const HEAT_STOPS: Array<{ t: number; c: [number, number, number] }> = [
    { t: 0.00, c: [55, 65, 81] },    // slate-600 (just visible against dark bg)
    { t: 0.15, c: [202, 138, 4] },   // amber-600
    { t: 0.35, c: [234, 179, 8] },   // yellow-500
    { t: 0.55, c: [249, 115, 22] },  // orange-500
    { t: 0.75, c: [239, 68, 68] },   // red-500
    { t: 1.00, c: [153, 27, 27] },   // red-800
  ];
  function heatColor(intensity: number, hasData: boolean): string {
    if (!hasData) return 'rgba(255,255,255,0.02)'; // empty cell
    const t = Math.sqrt(Math.max(0, Math.min(1, intensity)));
    let i = 0;
    while (i < HEAT_STOPS.length - 1 && HEAT_STOPS[i + 1].t < t) i++;
    if (i >= HEAT_STOPS.length - 1) {
      const [r, g, b] = HEAT_STOPS[HEAT_STOPS.length - 1].c;
      return `rgb(${r},${g},${b})`;
    }
    const a = HEAT_STOPS[i];
    const b = HEAT_STOPS[i + 1];
    const span = b.t - a.t;
    const mix = span > 0 ? (t - a.t) / span : 0;
    const r = Math.round(a.c[0] + (b.c[0] - a.c[0]) * mix);
    const g = Math.round(a.c[1] + (b.c[1] - a.c[1]) * mix);
    const bl = Math.round(a.c[2] + (b.c[2] - a.c[2]) * mix);
    return `rgb(${r},${g},${bl})`;
  }

  // AI bot trends
  let aiBotTrends = $derived((stats?.aiBotTrends as Record<string, Record<string, number>>) || {});
  let aiBotEntries = $derived.by(() => {
    return botEntries.filter(b => b.category === 'ai_training' || b.category === 'ai_search' || b.category === 'ai_user');
  });
  let aiBotDays = $derived.by(() => {
    const days = new Set<string>();
    for (const daily of Object.values(aiBotTrends)) {
      for (const day of Object.keys(daily)) days.add(day);
    }
    return [...days].sort();
  });

  // Merge state
  let mergeData = $state<Record<string, unknown> | null>(null);
  let merging = $state(false);
  let mergeError = $state<string | null>(null);

  type MergePhase = 'idle' | 'loading_log_stats' | 'loading_crawl_pages' | 'preparing' | 'merging' | 'persisting' | 'done' | 'error';
  const MERGE_PHASE_LABELS: Record<MergePhase, string> = {
    idle: '',
    loading_log_stats: 'Loading log analysis…',
    loading_crawl_pages: 'Loading crawl pages from disk…',
    preparing: 'Indexing crawl pages…',
    merging: 'Cross-referencing logs with crawl…',
    persisting: 'Persisting merged pages to cache…',
    done: 'Done',
    error: 'Error',
  };
  // Phase index drives the determinate-looking progress bar; the merge handler
  // is synchronous server-side so we don't get a real % — we step through phases.
  const PHASE_ORDER: MergePhase[] = ['loading_log_stats', 'loading_crawl_pages', 'preparing', 'merging', 'persisting', 'done'];

  let mergePhase = $state<MergePhase>('idle');
  let mergePageCount = $state(0);
  let mergeLogUrls = $state(0);
  let mergeElapsedMs = $state(0);

  let mergePct = $derived.by(() => {
    if (mergePhase === 'idle') return 0;
    if (mergePhase === 'done') return 100;
    if (mergePhase === 'error') return 100;
    const i = PHASE_ORDER.indexOf(mergePhase);
    if (i < 0) return 5;
    // Each phase covers an equal slice; show the leading edge so it visibly progresses.
    return Math.round(((i + 0.5) / PHASE_ORDER.length) * 100);
  });

  function resetMergeProgress() {
    mergePhase = 'idle';
    mergePageCount = 0;
    mergeLogUrls = 0;
    mergeElapsedMs = 0;
  }

  // ── Paginated merged-pages table state ───────────────────────────────────
  type MergedPageRow = {
    url: string;
    segment: string;
    reason?: string;
    logHits?: number;
    logBotHits?: number;
    logHumanHits?: number;
    logTopBot?: string;
    logStatus?: number;
    crawlStatus?: number;
    crawlIndexable?: boolean;
    crawlCanonical?: string;
    crawlDepth?: number;
    crawlInlinks?: number;
    crawlWordCount?: number;
    hasNoindex?: boolean;
    inCrawl?: boolean;
    inLogs?: boolean;
    logBytes?: number;
  };

  type MergeSortCol = 'url' | 'segment' | 'logHits' | 'logBotHits' | 'logBytes' | 'logStatus' | 'crawlStatus' | 'crawlDepth' | 'crawlInlinks';

  let mergedRows = $state<MergedPageRow[]>([]);
  let mergedTotal = $state(0);
  let mergedPage = $state(1);
  let mergedPageSize = $state(50);
  let mergedSort = $state<MergeSortCol>('logBotHits');
  let mergedOrder = $state<'asc' | 'desc'>('desc');
  let mergedSegmentFilter = $state('');
  let mergedSearch = $state('');
  let mergedLoading = $state(false);
  let mergedSearchDebounce: ReturnType<typeof setTimeout> | null = null;

  // Per-column filters. Empty string = no filter for that column.
  let filterUrl = $state('');
  let filterReason = $state('');
  let filterLogStatus = $state('');
  let filterCrawlStatus = $state('');
  let filterIndexable = $state(''); // '' | 'yes' | 'no'

  // Coverage filter — driven by clicking Venn diagram regions.
  type CoverageRegion = '' | 'crawl_only' | 'both' | 'logs_only';
  let coverageFilter = $state<CoverageRegion>('');
  const coverageLabels: Record<CoverageRegion, string> = {
    '': '',
    crawl_only: 'Crawl only (ghosts)',
    both: 'In both (matched)',
    logs_only: 'Logs only (orphans)',
  };

  let mergedPagesTotal = $derived(Math.max(1, Math.ceil(mergedTotal / mergedPageSize)));

  async function fetchMergedPages() {
    if (!selectedCrawlId) return;
    mergedLoading = true;
    try {
      const res = await api.getLogMergedPages(id, {
        crawlId: selectedCrawlId,
        page: mergedPage,
        pageSize: mergedPageSize,
        sort: mergedSort,
        order: mergedOrder,
        segment: mergedSegmentFilter || undefined,
        // Use the per-column filter inputs; mergedSearch (top toolbar) is
        // kept as a quick global URL search fallback that ORs into filterUrl.
        search: filterUrl || mergedSearch || undefined,
        reason: filterReason || undefined,
        logStatus: filterLogStatus || undefined,
        crawlStatus: filterCrawlStatus || undefined,
        indexable: filterIndexable || undefined,
        coverage: coverageFilter || undefined,
      });
      mergedRows = (res.pages as MergedPageRow[]) || [];
      mergedTotal = res.total || 0;
    } catch (err) {
      mergeError = (err as Error).message || 'Failed to load merged pages';
    } finally {
      mergedLoading = false;
    }
  }

  // Shared debounced refetch for per-column inputs (text fields).
  function onColumnFilterChange() {
    if (mergedSearchDebounce) clearTimeout(mergedSearchDebounce);
    mergedSearchDebounce = setTimeout(() => {
      mergedPage = 1;
      void fetchMergedPages();
    }, 250);
  }
  // Immediate refetch for select-style filters.
  function onColumnFilterChangeImmediate() {
    mergedPage = 1;
    void fetchMergedPages();
  }
  function clearAllFilters() {
    filterUrl = '';
    filterReason = '';
    filterLogStatus = '';
    filterCrawlStatus = '';
    filterIndexable = '';
    mergedSearch = '';
    mergedSegmentFilter = '';
    coverageFilter = '';
    mergedPage = 1;
    void fetchMergedPages();
  }

  // Click handler for the Venn diagram regions. Sets the coverage filter,
  // clears the conflicting segment dropdown (coverage supersedes it), and
  // scrolls the merge table into view so the result is visible.
  function clickVennRegion(region: CoverageRegion) {
    // Toggle off if same region clicked again.
    coverageFilter = coverageFilter === region ? '' : region;
    mergedSegmentFilter = '';
    mergedPage = 1;
    void fetchMergedPages();
    // Defer scroll so the table re-renders first.
    setTimeout(() => {
      document.getElementById('merge-table-anchor')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }, 60);
  }

  function toggleSort(col: MergeSortCol) {
    if (mergedSort === col) {
      mergedOrder = mergedOrder === 'asc' ? 'desc' : 'asc';
    } else {
      mergedSort = col;
      mergedOrder = 'desc';
    }
    mergedPage = 1;
    void fetchMergedPages();
  }

  function onSearchInput() {
    if (mergedSearchDebounce) clearTimeout(mergedSearchDebounce);
    mergedSearchDebounce = setTimeout(() => {
      mergedPage = 1;
      void fetchMergedPages();
    }, 250);
  }

  function onSegmentFilterChange() {
    mergedPage = 1;
    void fetchMergedPages();
  }

  function gotoPage(p: number) {
    if (p < 1 || p > mergedPagesTotal) return;
    mergedPage = p;
    void fetchMergedPages();
  }

  function sortIndicator(col: MergeSortCol): string {
    if (mergedSort !== col) return '';
    return mergedOrder === 'desc' ? ' ↓' : ' ↑';
  }

  // Fetch first page automatically once a merge completes. Only depend on
  // mergeData + selectedCrawlId; the inner state writes (page/sort/filter)
  // must be untracked or this effect ping-pongs and resets pagination on
  // every gotoPage call.
  $effect(() => {
    const hasResult = mergeData != null;
    const cid = selectedCrawlId;
    if (hasResult && cid) {
      untrack(() => {
        mergedPage = 1;
        void fetchMergedPages();
      });
    }
  });

  let crawlJobs = $state<Array<{ id: string; seedUrl: string; status: string; startedAt: string; pageCount: number; mode: string }>>([]);
  let selectedCrawlId = $state('');

  async function loadCrawlJobs() {
    mergeError = null;
    try {
      const data = await api.listCrawls();
      const all = data.crawls || [];
      crawlJobs = all
        .filter(j => j.status === 'completed')
        // Newest first, so the most recent crawl is the default option.
        .sort((a, b) => (b.startedAt || '').localeCompare(a.startedAt || ''));
      if (crawlJobs.length === 0) {
        mergeError = all.length === 0
          ? 'No crawls found. Run a crawl first, then come back to merge it with this log analysis.'
          : `Found ${all.length} crawl(s) but none are completed yet.`;
      }
    } catch (err) {
      mergeError = (err as Error).message || 'Failed to load crawls';
    }
  }

  function formatStartedAt(ts: string): string {
    if (!ts) return '';
    const d = new Date(ts);
    if (isNaN(d.getTime())) return ts;
    // "2026-04-09 14:32" — locale-friendly, full datetime, no seconds.
    const pad = (n: number) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
  }

  function formatCrawlOption(cj: { seedUrl: string; pageCount: number; startedAt: string }): string {
    const pages = (cj.pageCount || 0).toLocaleString();
    return `${cj.seedUrl} — ${pages} URLs — ${formatStartedAt(cj.startedAt)}`;
  }

  let mergeStartedAt = 0;
  let mergeTickTimer: ReturnType<typeof setInterval> | null = null;

  async function runMerge() {
    if (!selectedCrawlId) return;
    merging = true;
    mergeError = null;
    resetMergeProgress();
    // Optimistic first phase so the bar shows up instantly without waiting
    // for the first WS event.
    mergePhase = 'loading_log_stats';
    // Pre-fill expected page count from the dropdown so the UI shows scale
    // immediately, instead of waiting for the server's "preparing" event.
    const cj = crawlJobs.find(c => c.id === selectedCrawlId);
    if (cj && cj.pageCount) mergePageCount = cj.pageCount;

    // Client-side ticker: keep elapsed time live during long server phases
    // (especially loading_crawl_pages, which can take 30s-2min for a 500K-page cold crawl).
    mergeStartedAt = Date.now();
    if (mergeTickTimer) clearInterval(mergeTickTimer);
    mergeTickTimer = setInterval(() => {
      mergeElapsedMs = Date.now() - mergeStartedAt;
    }, 100);

    try {
      mergeData = await api.mergeLogWithCrawl(id, selectedCrawlId);
      mergePhase = 'done';
    } catch (err) {
      mergeError = (err as Error).message;
      mergePhase = 'error';
    } finally {
      if (mergeTickTimer) { clearInterval(mergeTickTimer); mergeTickTimer = null; }
    }
    merging = false;
  }

  function handleWsMessage(msg: WsMessage) {
    if (msg.type !== 'merge_progress') return;
    const data = msg.data as {
      logId?: string; crawlId?: string; phase?: MergePhase;
      pageCount?: number; logUrls?: number; elapsedMs?: number; error?: string;
    };
    // Only react to events for the currently-running merge.
    if (data.logId !== id || data.crawlId !== selectedCrawlId) return;
    if (data.phase) mergePhase = data.phase;
    // Server pageCount is authoritative once it arrives; until then we keep
    // the optimistic value pre-filled from the dropdown.
    if (typeof data.pageCount === 'number' && data.pageCount > 0) mergePageCount = data.pageCount;
    if (typeof data.logUrls === 'number') mergeLogUrls = data.logUrls;
    // Server elapsed is authoritative when it's larger than our local ticker
    // (handles network delays); never go backwards.
    if (typeof data.elapsedMs === 'number') mergeElapsedMs = Math.max(mergeElapsedMs, data.elapsedMs);
    if (data.phase === 'error' && data.error) mergeError = data.error;
  }


  const segmentLabels: Record<string, string> = {
    healthy: 'Healthy',
    crawled_not_indexed: 'Crawled, Not Indexed',
    uncrawled_indexable: 'Uncrawled (Ghost)',
    orphan_crawled: 'Orphan (Logs Only)',
    crawl_waste: 'Crawl Waste',
    redirect_waste: 'Redirect Waste',
    error_pages: 'Error Pages',
  };

  const segmentColors: Record<string, string> = {
    healthy: '#22c55e',
    crawled_not_indexed: '#f59e0b',
    uncrawled_indexable: '#6b7280',
    orphan_crawled: '#8b5cf6',
    crawl_waste: '#ef4444',
    redirect_waste: '#f97316',
    error_pages: '#dc2626',
  };

  // Crawl waste data
  let wasteData = $derived((stats?.waste as { totalBotHits: number; wasteHits: number; wasteRatio: number; byType: Record<string, { type: string; hits: number; urls: number; topUrls: string[]; botHits: number }> }) || null);

  const wasteTypeLabels: Record<string, string> = {
    faceted_nav: 'Faceted Navigation',
    pagination: 'Pagination',
    session_ids: 'Session/Tracking IDs',
    search: 'Internal Search',
    calendar: 'Calendar',
    resources: 'Resource Files',
    api: 'API Endpoints',
    taxonomy: 'Tags/Categories',
  };

  const wasteTypeColors: Record<string, string> = {
    faceted_nav: '#f59e0b',
    pagination: '#3b82f6',
    session_ids: '#ef4444',
    search: '#8b5cf6',
    calendar: '#ec4899',
    resources: '#6b7280',
    api: '#14b8a6',
    taxonomy: '#f97316',
  };

  // Bot verification
  let verificationData = $state<Record<string, { totalIps: number; verified: number; spoofed: number; unverified: number }> | null>(null);
  let spoofedIPs = $state<Array<{ ip: string; hostname?: string; claimedBot: string }> | null>(null);
  let verifying = $state(false);
  let verifyError = $state<string | null>(null);

  async function verifyBots() {
    verifying = true;
    verifyError = null;
    try {
      const result = await api.verifyLogBots(id);
      verificationData = result.verification;
      spoofedIPs = result.spoofed;
    } catch (err) {
      verifyError = (err as Error).message;
    }
    verifying = false;
  }

  // SVG donut chart helpers
  function donutPath(startAngle: number, endAngle: number, r: number, cx: number, cy: number): string {
    const start = { x: cx + r * Math.cos(startAngle), y: cy + r * Math.sin(startAngle) };
    const end = { x: cx + r * Math.cos(endAngle), y: cy + r * Math.sin(endAngle) };
    const large = endAngle - startAngle > Math.PI ? 1 : 0;
    return `M ${start.x} ${start.y} A ${r} ${r} 0 ${large} 1 ${end.x} ${end.y}`;
  }

  function formatNum(n: number): string {
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
    if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
    return n.toLocaleString();
  }

  function formatBytes(bytes: number): string {
    if (bytes >= 1073741824) return (bytes / 1073741824).toFixed(1) + ' GB';
    if (bytes >= 1048576) return (bytes / 1048576).toFixed(1) + ' MB';
    if (bytes >= 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return bytes + ' B';
  }

  function pct(n: number): string { return (n * 100).toFixed(1) + '%'; }

  function sortBots(col: string) {
    if (botSort.col === col) botSort.asc = !botSort.asc;
    else { botSort.col = col; botSort.asc = false; }
  }

  function sortURLs(col: string) {
    if (urlSort.col === col) urlSort.asc = !urlSort.asc;
    else { urlSort.col = col; urlSort.asc = false; }
  }

  function categoryColor(cat: string): string {
    if (cat.startsWith('search')) return 'text-blue-400';
    if (cat.startsWith('ai')) return 'text-purple-400';
    if (cat === 'seo_tool') return 'text-yellow-400';
    if (cat === 'social_media') return 'text-pink-400';
    if (cat === 'feed_crawler') return 'text-orange-400';
    if (cat === 'monitoring') return 'text-cyan-400';
    return 'text-fg-2';
  }

  function categoryBg(cat: string): string {
    if (cat.startsWith('search')) return '#60a5fa';
    if (cat.startsWith('ai')) return '#c084fc';
    if (cat === 'seo_tool') return '#facc15';
    if (cat === 'social_media') return '#f472b6';
    if (cat === 'feed_crawler') return '#fb923c';
    if (cat === 'monitoring') return '#22d3ee';
    return '#94a3b8';
  }

  function statusColor(code: number): string {
    if (code >= 500) return 'bg-red-500/20 text-red-400';
    if (code >= 400) return 'bg-orange-500/20 text-orange-400';
    if (code >= 300) return 'bg-yellow-500/20 text-yellow-400';
    if (code >= 200) return 'bg-green-500/20 text-green-400';
    return 'bg-surface-3 text-fg-2';
  }

  function formatDate(s: string): string {
    if (!s) return '-';
    return new Date(s).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
  }

  function formatShortDate(s: string): string {
    if (!s) return '';
    const d = new Date(s + 'T00:00:00');
    return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
  }

  // Export CSV
  function downloadCSV(filename: string, headers: string[], rows: (string | number)[][]) {
    const csv = [headers.join(','), ...rows.map(r => r.map(c => typeof c === 'string' && c.includes(',') ? `"${c}"` : c).join(','))].join('\n');
    const blob = new Blob([csv], { type: 'text/csv' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = filename;
    a.click();
    URL.revokeObjectURL(a.href);
  }

  function exportBots() {
    downloadCSV('bot-activity.csv',
      ['Bot', 'Category', 'Hits', 'Unique URLs', '% of Total', 'Bytes', 'Mobile', 'First Seen', 'Last Seen'],
      botEntries.map(b => [b.name, b.category, b.hits, b.uniqueUrls, totalHits > 0 ? ((b.hits / totalHits) * 100).toFixed(1) : '0', b.bytes || 0, b.mobile ? 'Yes' : 'No', b.firstSeen, b.lastSeen])
    );
  }

  function exportURLs() {
    downloadCSV('url-analysis.csv',
      ['Path', 'Total Hits', 'Bot Hits', 'Human Hits', 'Top Bot', 'Status'],
      filteredURLs.map(u => [u.path, u.hits, u.botHits, u.humanHits, u.topBot || '', u.status])
    );
  }

  function exportDailyTrends() {
    downloadCSV('daily-trends.csv',
      ['Date', 'Total Hits'],
      sortedDays.map(d => [d, dailyHits[d]])
    );
  }

  function exportHourly() {
    downloadCSV('hourly-distribution.csv',
      ['Hour', 'Hits'],
      hourlyHits.map((h, i) => [`${i}:00`, h])
    );
  }

  // Sparkline for bot daily
  function sparklinePath(botName: string): string {
    const daily = botDailyHits[botName] || {};
    if (sortedDays.length === 0) return '';
    const vals = sortedDays.map(d => daily[d] || 0);
    const max = Math.max(...vals, 1);
    return vals.map((v, i) => {
      const x = sortedDays.length === 1 ? 50 : (i / (sortedDays.length - 1)) * 100;
      const y = 20 - (v / max) * 18;
      return `${i === 0 ? 'M' : 'L'}${x},${y}`;
    }).join(' ');
  }
</script>

{#if loading}
  <div class="text-fg-2 text-sm py-8 text-center">Loading analysis...</div>
{:else if error}
  <div class="p-4 rounded-lg bg-red-500/10 border border-red-500/20 text-red-400">{error}</div>
{:else if stats}
  <div class="space-y-6">
    <!-- Tabs -->
    <div class="flex gap-1 border-b border-border">
      {#each [['overview', 'Overview'], ['bots', 'Bot Activity'], ['urls', 'URL Analysis'], ['dirs', 'Directories'], ['hourly', 'Hourly'], ['trends', 'Daily Trends'], ['heatmap', 'Heatmap'], ['google', 'Googlebot'], ['ai', 'AI Bots'], ['waste', 'Crawl Budget'], ['merge', 'Merge']] as [key, label]}
        <button
          class="px-4 py-2 text-sm font-medium border-b-2 transition-colors cursor-pointer
            {tab === key ? 'border-accent text-accent' : 'border-transparent text-fg-2 hover:text-fg'}"
          onclick={() => tab = key as typeof tab}
        >{label}</button>
      {/each}
    </div>

    {#if tab === 'overview'}
      <!-- Summary Cards -->
      <div class="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-4">
        {#each [
          ['Total Hits', formatNum(totalHits)],
          ['Bot Hits', formatNum(botHitsTotal)],
          ['Human Hits', formatNum(humanHits)],
          ['Date Range', dateRange[0] ? formatDate(dateRange[0]) + ' - ' + formatDate(dateRange[1]) : '-'],
          ['Unique URLs', formatNum((crawlBudget.uniqueUrlsCrawled as number) || 0)],
          ['Total Bytes', formatBytes((stats?.totalBytes as number) || 0)],
        ] as [label, value]}
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">{label}</div>
            <div class="text-lg font-semibold text-fg">{value}</div>
          </div>
        {/each}
      </div>

      <!-- Bot Activity Timeline (adaptive: hourly ≤48h, daily >48h) -->
      <div class="rounded-xl bg-surface-1 border border-border p-5">
        <BotActivityChart
          botDailyHits={botDailyHits}
          botHourlyHits={botHourlyHits}
          dateRange={dateRange}
          height={280}
        />
      </div>

      <!-- Anomaly Alerts -->
      {#if anomalies.length > 0}
        <div class="rounded-xl bg-surface-1 border border-orange-500/30 p-5">
          <h3 class="text-sm font-medium text-orange-400 mb-3">Anomalies Detected</h3>
          <div class="space-y-2">
            {#each anomalies as a}
              <div class="flex items-center gap-3 text-sm">
                <span class="w-5 h-5 rounded-full flex items-center justify-center text-xs font-bold shrink-0
                  {a.severity === 'critical' ? 'bg-red-500/20 text-red-400' : 'bg-orange-500/20 text-orange-400'}">!</span>
                <span class="text-fg">{a.bot}</span>
                <span class="text-fg-2">spiked on {a.day}:</span>
                <span class="font-mono text-fg">{a.hits.toLocaleString()} hits</span>
                <span class="text-fg-2">(avg {Math.round(a.mean)}, +{((a.hits - a.mean) / a.stddev).toFixed(1)} sigma)</span>
              </div>
            {/each}
          </div>
        </div>
      {/if}

      <!-- Status Code Distribution: individual codes -->
      <div class="rounded-xl bg-surface-1 border border-border p-5">
        <div class="flex items-center justify-between mb-3">
          <h3 class="text-sm font-medium text-fg">Status Code Distribution</h3>
          {#if statusFilter !== null}
            <button class="text-xs text-accent hover:underline cursor-pointer" onclick={() => { statusFilter = null; }}>Clear filter</button>
          {/if}
        </div>
        <!-- Groups -->
        <div class="space-y-2 mb-4">
          {#each Object.entries(statusGroups).sort(([a], [b]) => a.localeCompare(b)) as [group, count]}
            {@const barPct = totalHits > 0 ? (count / totalHits) * 100 : 0}
            <div class="flex items-center gap-3">
              <span class="w-8 text-xs font-mono text-fg-2">{group}</span>
              <div class="flex-1 h-5 rounded bg-surface-3 overflow-hidden">
                <div class="h-full rounded transition-all duration-300
                  {group === '2xx' ? 'bg-green-500' : group === '3xx' ? 'bg-yellow-500' : group === '4xx' ? 'bg-orange-500' : group === '5xx' ? 'bg-red-500' : 'bg-gray-500'}"
                  style="width: {barPct}%"
                ></div>
              </div>
              <span class="w-16 text-xs text-fg-2 text-right">{count.toLocaleString()}</span>
            </div>
          {/each}
        </div>
        <!-- Individual codes -->
        {#if sortedStatusCodes.length > 0}
          <div class="border-t border-border pt-3">
            <div class="text-xs text-fg-2 mb-2">Individual Codes</div>
            <div class="flex flex-wrap gap-2">
              {#each sortedStatusCodes as { code, count }}
                <button
                  class="px-2.5 py-1 rounded text-xs font-mono cursor-pointer transition-colors
                    {statusFilter === code ? 'ring-2 ring-accent' : ''}
                    {statusColor(code)}"
                  onclick={() => { statusFilter = statusFilter === code ? null : code; tab = 'urls'; }}
                  title="Click to filter URLs by {code}"
                >{code}: {formatNum(count)}</button>
              {/each}
            </div>
          </div>
        {/if}
      </div>

      <!-- Crawl Budget KPIs -->
      <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
        {#each [
          ['Crawl Efficiency', pct((crawlBudget.crawlEfficiency as number) || 0), (crawlBudget.crawlEfficiency as number) >= 0.7 ? 'text-green-400' : 'text-orange-400'],
          ['Error Rate', pct((crawlBudget.errorRate as number) || 0), (crawlBudget.errorRate as number) < 0.05 ? 'text-green-400' : 'text-red-400'],
          ['Mobile Crawl Share', pct((crawlBudget.mobileCrawlShare as number) || 0), 'text-blue-400'],
          ['AI Bot Share', pct((crawlBudget.aiBotShare as number) || 0), 'text-purple-400'],
        ] as [label, value, color]}
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">{label}</div>
            <div class="text-xl font-bold {color}">{value}</div>
          </div>
        {/each}
      </div>

      <!-- Bandwidth by Bot -->
      {#if topBotsByBytes.length > 0 && topBotsByBytes[0].bytes > 0}
        <div class="rounded-xl bg-surface-1 border border-border p-5">
          <h3 class="text-sm font-medium text-fg mb-3">Top Bots by Bandwidth</h3>
          <div class="space-y-2">
            {#each topBotsByBytes.filter(b => b.bytes > 0) as bot}
              {@const barPct = topBotsByBytes[0].bytes > 0 ? (bot.bytes / topBotsByBytes[0].bytes) * 100 : 0}
              <div class="flex items-center gap-3">
                <span class="w-36 text-xs text-fg truncate" title={bot.name}>{bot.name}</span>
                <div class="flex-1 h-4 rounded bg-surface-3 overflow-hidden">
                  <div class="h-full rounded transition-all duration-300" style="width: {barPct}%; background: {categoryBg(bot.category)}"></div>
                </div>
                <span class="w-20 text-xs text-fg-2 text-right">{formatBytes(bot.bytes)}</span>
                <span class="w-16 text-xs text-fg-2 text-right">{formatNum(bot.hits)} hits</span>
              </div>
            {/each}
          </div>
        </div>
      {/if}

      <!-- Bot Category Distribution -->
      {#if categoryStats.length > 0}
        <div class="rounded-xl bg-surface-1 border border-border p-5">
          <h3 class="text-sm font-medium text-fg mb-4">Bot Category Distribution</h3>
          <div class="flex items-center gap-8">
            <svg viewBox="0 0 120 120" class="w-40 h-40 shrink-0">
              {#each categoryStats as seg, i}
                {@const startPct = categoryStats.slice(0, i).reduce((s, c) => s + c.hits, 0) / botHitsTotal}
                {@const endPct = startPct + seg.hits / botHitsTotal}
                {@const startAngle = startPct * Math.PI * 2 - Math.PI / 2}
                {@const endAngle = Math.min(endPct * Math.PI * 2 - Math.PI / 2, startAngle + Math.PI * 1.999)}
                <path d={donutPath(startAngle, endAngle, 50, 60, 60)} fill="none" stroke={categoryBg(seg.cat)} stroke-width="16" />
              {/each}
              <text x="60" y="56" text-anchor="middle" class="text-[11px] fill-fg font-semibold">{botEntries.length}</text>
              <text x="60" y="70" text-anchor="middle" class="text-[9px] fill-fg-2">bots</text>
            </svg>
            <div class="flex flex-col gap-2">
              {#each categoryStats as seg}
                <div class="flex items-center gap-2">
                  <span class="inline-block w-3 h-3 rounded" style="background: {categoryBg(seg.cat)}"></span>
                  <span class="text-xs text-fg w-28">{seg.cat.replace(/_/g, ' ')}</span>
                  <span class="text-xs text-fg-2">{formatNum(seg.hits)}</span>
                  <span class="text-xs text-fg-2">({botHitsTotal > 0 ? ((seg.hits / botHitsTotal) * 100).toFixed(0) : 0}%)</span>
                </div>
              {/each}
            </div>
          </div>
        </div>
      {/if}
    {/if}

    {#if tab === 'bots'}
      <div class="flex justify-end gap-2 mb-2">
        <button
          class="px-3 py-1.5 rounded-lg text-xs font-medium bg-surface-2 text-fg-2 hover:text-fg border border-border cursor-pointer disabled:opacity-50"
          onclick={verifyBots}
          disabled={verifying}
        >{verifying ? 'Verifying...' : verificationData ? 'Re-verify Bots' : 'Verify Bot IPs (FCrDNS)'}</button>
        <button class="px-3 py-1.5 rounded-lg text-xs font-medium bg-surface-2 text-fg-2 hover:text-fg border border-border cursor-pointer" onclick={exportBots}>Export CSV</button>
      </div>
      {#if verifyError}
        <div class="p-2 mb-2 rounded-lg bg-red-500/10 border border-red-500/20 text-red-400 text-xs">{verifyError}</div>
      {/if}
      {#if spoofedIPs && spoofedIPs.length > 0}
        <div class="p-3 mb-3 rounded-lg bg-red-500/10 border border-red-500/20">
          <div class="text-red-400 text-xs font-medium mb-1">Spoofed Bot IPs Detected ({spoofedIPs.length})</div>
          <div class="space-y-1 max-h-32 overflow-y-auto">
            {#each spoofedIPs.slice(0, 20) as spoof}
              <div class="text-xs text-fg-2">{spoof.ip} claiming <span class="text-fg font-medium">{spoof.claimedBot}</span>{spoof.hostname ? ` (resolved: ${spoof.hostname})` : ''}</div>
            {/each}
            {#if spoofedIPs.length > 20}
              <div class="text-xs text-fg-2">... and {spoofedIPs.length - 20} more</div>
            {/if}
          </div>
        </div>
      {/if}
      <div class="rounded-xl border border-border overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="bg-surface-2 text-fg-2 text-left text-xs uppercase tracking-wider">
              <th class="px-4 py-3">Bot</th>
              <th class="px-4 py-3">Category</th>
              <th class="px-4 py-3 cursor-pointer" onclick={() => sortBots('hits')}>Hits {botSort.col === 'hits' ? (botSort.asc ? '↑' : '↓') : ''}</th>
              <th class="px-4 py-3 cursor-pointer" onclick={() => sortBots('uniqueUrls')}>Unique URLs {botSort.col === 'uniqueUrls' ? (botSort.asc ? '↑' : '↓') : ''}</th>
              <th class="px-4 py-3">% of Total</th>
              <th class="px-4 py-3 cursor-pointer" onclick={() => sortBots('bytes')}>Bytes {botSort.col === 'bytes' ? (botSort.asc ? '↑' : '↓') : ''}</th>
              <th class="px-4 py-3">Rate/h</th>
              <th class="px-4 py-3">Trend</th>
              <th class="px-4 py-3">Mobile</th>
              <th class="px-4 py-3">First Seen</th>
              <th class="px-4 py-3">Last Seen</th>
            </tr>
          </thead>
          <tbody>
            {#each botEntries as bot}
              <tr
                class="border-t border-border hover:bg-surface-2/50 transition-colors cursor-pointer"
                onclick={() => expandedBot = expandedBot === bot.name ? null : bot.name}
              >
                <td class="px-4 py-3 font-medium text-fg">
                  {bot.name}
                  {#if verificationData?.[bot.name]}
                    {@const v = verificationData[bot.name]}
                    {#if v.spoofed > 0}
                      <span class="ml-1 px-1.5 py-0.5 rounded text-[10px] bg-red-500/20 text-red-400" title="{v.spoofed} spoofed of {v.totalIps} IPs">SPOOF</span>
                    {:else if v.verified > 0}
                      <span class="ml-1 px-1.5 py-0.5 rounded text-[10px] bg-green-500/20 text-green-400" title="{v.verified}/{v.totalIps} IPs verified">VERIFIED</span>
                    {/if}
                  {/if}
                </td>
                <td class="px-4 py-3 {categoryColor(bot.category)}">
                  <span class="px-2 py-0.5 rounded text-xs bg-surface-3">{bot.category.replace('_', ' ')}</span>
                </td>
                <td class="px-4 py-3 text-fg">{bot.hits.toLocaleString()}</td>
                <td class="px-4 py-3 text-fg-2">{bot.uniqueUrls.toLocaleString()}</td>
                <td class="px-4 py-3 text-fg-2">{totalHits > 0 ? ((bot.hits / totalHits) * 100).toFixed(1) + '%' : '-'}</td>
                <td class="px-4 py-3 text-fg-2">{bot.bytes ? formatBytes(bot.bytes) : '-'}</td>
                <td class="px-4 py-3 text-xs {(botCrawlRates[bot.name] || 0) > 60 ? 'text-red-400 font-medium' : 'text-fg-2'}">
                  {botCrawlRates[bot.name] ? botCrawlRates[bot.name].toLocaleString(undefined, { maximumFractionDigits: 1 }) : '-'}
                </td>
                <td class="px-4 py-3">
                  {#if sortedDays.length > 1 && botDailyHits[bot.name]}
                    <svg viewBox="0 0 100 20" class="w-24 h-5" preserveAspectRatio="none">
                      <path d={sparklinePath(bot.name)} fill="none" stroke={categoryBg(bot.category)} stroke-width="1.5" vector-effect="non-scaling-stroke" />
                    </svg>
                  {:else}
                    <span class="text-fg-2 text-xs">-</span>
                  {/if}
                </td>
                <td class="px-4 py-3 text-fg-2">{bot.mobile ? 'Yes' : '-'}</td>
                <td class="px-4 py-3 text-fg-2 text-xs">{formatDate(bot.firstSeen)}</td>
                <td class="px-4 py-3 text-fg-2 text-xs">{formatDate(bot.lastSeen)}</td>
              </tr>
              {#if expandedBot === bot.name && sortedDays.length > 1 && botDailyHits[bot.name]}
                {@const daily = botDailyHits[bot.name] || {}}
                {@const vals = sortedDays.map(d => daily[d] || 0)}
                {@const maxVal = Math.max(...vals, 1)}
                <tr class="border-t border-border bg-surface-1">
                  <td colspan="11" class="px-4 py-4">
                    <div class="text-xs text-fg-2 mb-2">Daily activity for {bot.name}</div>
                    <div class="flex items-end gap-px" style="height: 80px;">
                      {#each sortedDays as day, i}
                        {@const v = daily[day] || 0}
                        {@const h = Math.max(v > 0 ? 2 : 0, (v / maxVal) * 72)}
                        <div class="flex-1 flex flex-col items-center justify-end" style="height: 80px;">
                          <div class="w-full rounded-t transition-all" style="height: {h}px; background: {categoryBg(bot.category)}" title="{day}: {v.toLocaleString()} hits"></div>
                        </div>
                      {/each}
                    </div>
                    <div class="flex justify-between text-[9px] text-fg-2 mt-1">
                      <span>{formatShortDate(sortedDays[0])}</span>
                      <span>{formatShortDate(sortedDays[sortedDays.length - 1])}</span>
                    </div>
                  </td>
                </tr>
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    {#if tab === 'urls'}
      <div class="flex items-center justify-between gap-3 mb-3">
        <div class="flex items-center gap-2">
          <input
            type="text"
            placeholder="Filter URLs..."
            class="w-full max-w-md px-3 py-2 rounded-lg bg-surface-2 border border-border text-fg text-sm placeholder-fg-2/50 focus:outline-none focus:border-accent"
            bind:value={urlSearch}
          />
          {#if statusFilter !== null}
            <span class="px-2 py-1 rounded text-xs font-mono {statusColor(statusFilter)}">
              {statusFilter}
              <button class="ml-1 cursor-pointer" onclick={() => statusFilter = null}>x</button>
            </span>
          {/if}
        </div>
        <button class="px-3 py-1.5 rounded-lg text-xs font-medium bg-surface-2 text-fg-2 hover:text-fg border border-border cursor-pointer shrink-0" onclick={exportURLs}>Export CSV</button>
      </div>
      <div class="rounded-xl border border-border overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="bg-surface-2 text-fg-2 text-left text-xs uppercase tracking-wider">
              <th class="px-4 py-3">URL Path</th>
              <th class="px-4 py-3 cursor-pointer" onclick={() => sortURLs('hits')}>Total {urlSort.col === 'hits' ? (urlSort.asc ? '↑' : '↓') : ''}</th>
              <th class="px-4 py-3 cursor-pointer" onclick={() => sortURLs('botHits')}>Bot {urlSort.col === 'botHits' ? (urlSort.asc ? '↑' : '↓') : ''}</th>
              <th class="px-4 py-3 cursor-pointer" onclick={() => sortURLs('humanHits')}>Human {urlSort.col === 'humanHits' ? (urlSort.asc ? '↑' : '↓') : ''}</th>
              <th class="px-4 py-3">Top Bot</th>
              <th class="px-4 py-3">Status</th>
            </tr>
          </thead>
          <tbody>
            {#each filteredURLs as url}
              <tr class="border-t border-border hover:bg-surface-2/50 transition-colors">
                <td class="px-4 py-3 text-fg font-mono text-xs max-w-md truncate" title={url.path}>{url.path}</td>
                <td class="px-4 py-3 text-fg">{url.hits.toLocaleString()}</td>
                <td class="px-4 py-3 text-fg-2">{url.botHits.toLocaleString()}</td>
                <td class="px-4 py-3 text-fg-2">{url.humanHits.toLocaleString()}</td>
                <td class="px-4 py-3 text-fg-2 text-xs">{url.topBot || '-'}</td>
                <td class="px-4 py-3">
                  <span class="px-2 py-0.5 rounded text-xs font-mono {statusColor(url.status)}">{url.status}</span>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <div class="text-xs text-fg-2">Showing top {filteredURLs.length} URLs{statusFilter !== null ? ` with status ${statusFilter}` : ''}</div>

      <!-- Path Depth Analysis -->
      {#if pathDepthStats.length > 0}
        {@const maxDepthHits = Math.max(...pathDepthStats.map(d => d.total), 1)}
        <div class="rounded-xl bg-surface-1 border border-border p-5 mt-4">
          <h3 class="text-sm font-medium text-fg mb-3">Hits by URL Depth</h3>
          <div class="space-y-2">
            {#each pathDepthStats as d}
              {@const barPct = (d.total / maxDepthHits) * 100}
              {@const botPct = d.total > 0 ? (d.bot / d.total) * 100 : 0}
              <div class="flex items-center gap-3">
                <span class="w-6 text-xs font-mono text-fg-2 text-right">/{d.depth > 0 ? '*'.repeat(d.depth) : ''}</span>
                <span class="w-12 text-xs text-fg-2">d={d.depth}</span>
                <div class="flex-1 h-5 rounded bg-surface-3 overflow-hidden">
                  <div class="h-full rounded flex overflow-hidden" style="width: {barPct}%">
                    <div class="bg-accent" style="width: {botPct}%"></div>
                    <div class="bg-accent/40" style="width: {100 - botPct}%"></div>
                  </div>
                </div>
                <span class="w-16 text-xs text-fg-2 text-right">{formatNum(d.total)}</span>
                <span class="w-20 text-xs text-fg-2 text-right">{formatNum(d.bot)} bot</span>
              </div>
            {/each}
          </div>
          <div class="flex items-center justify-end gap-4 text-xs text-fg-2 mt-2">
            <span class="flex items-center gap-1"><span class="inline-block w-3 h-3 rounded bg-accent"></span> Bot</span>
            <span class="flex items-center gap-1"><span class="inline-block w-3 h-3 rounded bg-accent/40"></span> Human</span>
          </div>
        </div>
      {/if}
    {/if}

    {#if tab === 'dirs'}
      {#if dirStats.length > 0}
        {@const maxDirHits = Math.max(...dirStats.map(d => d.total), 1)}
        {@const totalDirHits = dirStats.reduce((s, d) => s + d.total, 0)}
        {@const top10 = dirStats.slice(0, 10)}
        {@const othersTotal = dirStats.slice(10).reduce((s, d) => s + d.total, 0)}
        {@const othersBot = dirStats.slice(10).reduce((s, d) => s + d.bot, 0)}
        {@const othersURLs = dirStats.slice(10).reduce((s, d) => s + d.urls, 0)}

        <!-- Summary cards -->
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-4">
          {#each [
            ['Directories', dirStats.length.toLocaleString()],
            ['Total Hits', formatNum(totalDirHits)],
            ['Top Directory', dirStats[0]?.dir || '-'],
            ['Top Dir Hits', formatNum(dirStats[0]?.total || 0) + ' (' + (totalDirHits > 0 ? ((dirStats[0]?.total || 0) / totalDirHits * 100).toFixed(0) : '0') + '%)'],
          ] as [label, value]}
            <div class="rounded-lg bg-surface-1 border border-border px-4 py-3">
              <div class="text-[10px] text-fg-2 uppercase tracking-wider">{label}</div>
              <div class="text-lg font-semibold text-fg mt-0.5">{value}</div>
            </div>
          {/each}
        </div>

        <!-- Horizontal bar chart: Top directories -->
        <div class="rounded-xl bg-surface-1 border border-border p-5 mb-4">
          <h3 class="text-sm font-medium text-fg mb-4">Hit Distribution by Directory</h3>
          <div class="space-y-2">
            {#each top10 as d}
              {@const barPct = (d.total / maxDirHits) * 100}
              {@const botPct = d.total > 0 ? (d.bot / d.total) * 100 : 0}
              {@const sharePct = totalDirHits > 0 ? (d.total / totalDirHits * 100) : 0}
              <div class="flex items-center gap-3">
                <span class="w-28 text-xs font-mono text-fg truncate" title={d.dir}>{d.dir}</span>
                <div class="flex-1 h-6 rounded bg-surface-3 overflow-hidden">
                  <div class="h-full rounded flex overflow-hidden" style="width: {barPct}%">
                    <div class="bg-accent" style="width: {botPct}%"></div>
                    <div class="bg-accent/40" style="width: {100 - botPct}%"></div>
                  </div>
                </div>
                <span class="w-16 text-xs text-fg text-right">{formatNum(d.total)}</span>
                <span class="w-12 text-xs text-fg-2 text-right">{sharePct.toFixed(1)}%</span>
              </div>
            {/each}
            {#if othersTotal > 0}
              {@const othersPct = (othersTotal / maxDirHits) * 100}
              {@const othersBotPct = othersTotal > 0 ? (othersBot / othersTotal * 100) : 0}
              <div class="flex items-center gap-3 opacity-60">
                <span class="w-28 text-xs text-fg-2 italic">others ({dirStats.length - 10})</span>
                <div class="flex-1 h-6 rounded bg-surface-3 overflow-hidden">
                  <div class="h-full rounded flex overflow-hidden" style="width: {othersPct}%">
                    <div class="bg-accent" style="width: {othersBotPct}%"></div>
                    <div class="bg-accent/40" style="width: {100 - othersBotPct}%"></div>
                  </div>
                </div>
                <span class="w-16 text-xs text-fg-2 text-right">{formatNum(othersTotal)}</span>
                <span class="w-12 text-xs text-fg-2 text-right">{(othersTotal / totalDirHits * 100).toFixed(1)}%</span>
              </div>
            {/if}
          </div>
          <div class="flex items-center justify-end gap-4 text-xs text-fg-2 mt-3">
            <span class="flex items-center gap-1"><span class="inline-block w-3 h-3 rounded bg-accent"></span> Bot</span>
            <span class="flex items-center gap-1"><span class="inline-block w-3 h-3 rounded bg-accent/40"></span> Human</span>
          </div>
        </div>

        <!-- Donut chart: share of hits -->
        {@const donutColors = ['#58a6ff', '#60a5fa', '#818cf8', '#a78bfa', '#c084fc', '#e879f9', '#f472b6', '#fb923c', '#facc15', '#4ade80', '#94a3b8']}
        {@const donutData = [...top10.map((d, i) => ({ label: d.dir, value: d.total, color: donutColors[i] })), ...(othersTotal > 0 ? [{ label: 'others', value: othersTotal, color: donutColors[10] }] : [])]}
        {@const donutTotal = donutData.reduce((s, d) => s + d.value, 0)}
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-4">
          <div class="rounded-xl bg-surface-1 border border-border p-5">
            <h3 class="text-sm font-medium text-fg mb-4">Share of Total Hits</h3>
            <div class="flex items-center gap-6">
              <svg viewBox="0 0 100 100" class="w-40 h-40 shrink-0">
                {#each donutData as seg, i}
                  {@const startAngle = donutData.slice(0, i).reduce((s, d) => s + d.value / donutTotal * 360, 0)}
                  {@const angle = seg.value / donutTotal * 360}
                  {@const startRad = (startAngle - 90) * Math.PI / 180}
                  {@const endRad = (startAngle + angle - 90) * Math.PI / 180}
                  {@const largeArc = angle > 180 ? 1 : 0}
                  {@const x1 = 50 + 40 * Math.cos(startRad)}
                  {@const y1 = 50 + 40 * Math.sin(startRad)}
                  {@const x2 = 50 + 40 * Math.cos(endRad)}
                  {@const y2 = 50 + 40 * Math.sin(endRad)}
                  <path d="M 50 50 L {x1} {y1} A 40 40 0 {largeArc} 1 {x2} {y2} Z" fill={seg.color} opacity="0.85">
                    <title>{seg.label}: {formatNum(seg.value)} ({(seg.value / donutTotal * 100).toFixed(1)}%)</title>
                  </path>
                {/each}
                <circle cx="50" cy="50" r="22" fill="var(--surface-1)" />
                <text x="50" y="50" text-anchor="middle" dominant-baseline="central" fill="var(--fg)" font-size="8" font-weight="600">{dirStats.length}</text>
                <text x="50" y="58" text-anchor="middle" fill="var(--fg-2)" font-size="4">dirs</text>
              </svg>
              <div class="flex flex-col gap-1 text-xs overflow-hidden">
                {#each donutData as seg}
                  <div class="flex items-center gap-2 truncate">
                    <span class="inline-block w-2.5 h-2.5 rounded-sm shrink-0" style="background: {seg.color}"></span>
                    <span class="text-fg truncate">{seg.label}</span>
                    <span class="text-fg-2 ml-auto shrink-0">{(seg.value / donutTotal * 100).toFixed(1)}%</span>
                  </div>
                {/each}
              </div>
            </div>
          </div>

          <!-- Hit Concentration: avg hits per URL per directory -->
          <div class="rounded-xl bg-surface-1 border border-border p-5">
            <h3 class="text-sm font-medium text-fg mb-1">Hit Concentration</h3>
            <p class="text-xs text-fg-2 mb-4">Average hits per URL — high values mean bots hammer a few URLs repeatedly</p>
            <div class="space-y-2">
              {#each top10 as d}
                {@const avg = d.urls > 0 ? d.total / d.urls : 0}
                {@const topMaxAvg = Math.max(...top10.map(x => x.urls > 0 ? x.total / x.urls : 0), 1)}
                {@const barPct = (avg / topMaxAvg) * 100}
                {@const isHot = avg > 1000}
                <div class="flex items-center gap-3">
                  <span class="w-28 text-xs font-mono text-fg truncate" title={d.dir}>{d.dir}</span>
                  <div class="flex-1 h-4 rounded bg-surface-3 overflow-hidden">
                    <div class="h-full rounded {isHot ? 'bg-orange-500/70' : 'bg-accent/60'}" style="width: {barPct}%"></div>
                  </div>
                  <span class="w-20 text-xs text-right {isHot ? 'text-orange-400' : 'text-fg-2'}">{formatNum(Math.round(avg))} avg</span>
                </div>
              {/each}
            </div>
            <div class="flex items-center justify-end gap-4 text-xs text-fg-2 mt-3">
              <span class="flex items-center gap-1"><span class="inline-block w-3 h-3 rounded bg-orange-500/70"></span> &gt;1K hits/URL</span>
              <span class="flex items-center gap-1"><span class="inline-block w-3 h-3 rounded bg-accent/60"></span> Normal</span>
            </div>
          </div>
        </div>

        <!-- Full directory table -->
        <div class="rounded-xl bg-surface-1 border border-border p-5">
          <h3 class="text-sm font-medium text-fg mb-3">All Directories</h3>
          <div class="rounded-lg border border-border overflow-hidden">
            <table class="w-full text-sm">
              <thead>
                <tr class="bg-surface-2 text-fg-2 text-left text-xs uppercase tracking-wider">
                  <th class="px-4 py-2">Directory</th>
                  <th class="px-4 py-2" style="min-width: 180px;"></th>
                  <th class="px-4 py-2 text-right">Total</th>
                  <th class="px-4 py-2 text-right">Bot</th>
                  <th class="px-4 py-2 text-right">Human</th>
                  <th class="px-4 py-2 text-right">URLs</th>
                  <th class="px-4 py-2 text-right">Share</th>
                  <th class="px-4 py-2 text-right">Bot %</th>
                </tr>
              </thead>
              <tbody>
                {#each dirStats as d}
                  {@const barPct = (d.total / maxDirHits) * 100}
                  {@const botPct = d.total > 0 ? (d.bot / d.total) * 100 : 0}
                  {@const sharePct = totalDirHits > 0 ? (d.total / totalDirHits * 100) : 0}
                  <tr class="border-t border-border hover:bg-surface-2/50 transition-colors">
                    <td class="px-4 py-2 font-mono text-xs text-fg">{d.dir}</td>
                    <td class="px-4 py-2">
                      <div class="h-4 rounded bg-surface-3 overflow-hidden">
                        <div class="h-full rounded flex overflow-hidden" style="width: {barPct}%">
                          <div class="bg-accent" style="width: {botPct}%"></div>
                          <div class="bg-accent/40" style="width: {100 - botPct}%"></div>
                        </div>
                      </div>
                    </td>
                    <td class="px-4 py-2 text-xs text-fg text-right">{formatNum(d.total)}</td>
                    <td class="px-4 py-2 text-xs text-fg-2 text-right">{formatNum(d.bot)}</td>
                    <td class="px-4 py-2 text-xs text-fg-2 text-right">{formatNum(d.human)}</td>
                    <td class="px-4 py-2 text-xs text-fg-2 text-right">{d.urls.toLocaleString()}</td>
                    <td class="px-4 py-2 text-xs text-fg-2 text-right">{sharePct.toFixed(1)}%</td>
                    <td class="px-4 py-2 text-xs text-right {botPct > 90 ? 'text-red-400' : botPct > 50 ? 'text-yellow-400' : 'text-green-400'}">{botPct.toFixed(0)}%</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </div>
        <!-- Query Parameters -->
        {#if paramStats.length > 0}
          {@const maxParamHits = Math.max(...paramStats.map(p => p.total), 1)}
          {@const totalParamHits = paramStats.reduce((s, p) => s + p.total, 0)}
          <div class="rounded-xl bg-surface-1 border border-border p-5 mt-4">
            <h3 class="text-sm font-medium text-fg mb-3">Most Used Query Parameters</h3>
            <p class="text-xs text-fg-2 mb-3">URLs with query strings grouped by parameter name. High bot traffic on specific parameters may indicate crawl waste.</p>
            <div class="rounded-lg border border-border overflow-hidden">
              <table class="w-full text-sm">
                <thead>
                  <tr class="bg-surface-2 text-fg-2 text-left text-xs uppercase tracking-wider">
                    <th class="px-4 py-2">Parameter</th>
                    <th class="px-4 py-2" style="min-width: 180px;"></th>
                    <th class="px-4 py-2 text-right">Total</th>
                    <th class="px-4 py-2 text-right">Bot</th>
                    <th class="px-4 py-2 text-right">Human</th>
                    <th class="px-4 py-2 text-right">URLs</th>
                    <th class="px-4 py-2 text-right">Bot %</th>
                  </tr>
                </thead>
                <tbody>
                  {#each paramStats.slice(0, 30) as p}
                    {@const barPct = (p.total / maxParamHits) * 100}
                    {@const botPct = p.total > 0 ? (p.bot / p.total) * 100 : 0}
                    <tr class="border-t border-border hover:bg-surface-2/50 transition-colors">
                      <td class="px-4 py-2 font-mono text-xs text-fg">{p.param}</td>
                      <td class="px-4 py-2">
                        <div class="h-4 rounded bg-surface-3 overflow-hidden">
                          <div class="h-full rounded flex overflow-hidden" style="width: {barPct}%">
                            <div class="bg-accent" style="width: {botPct}%"></div>
                            <div class="bg-accent/40" style="width: {100 - botPct}%"></div>
                          </div>
                        </div>
                      </td>
                      <td class="px-4 py-2 text-xs text-fg text-right">{formatNum(p.total)}</td>
                      <td class="px-4 py-2 text-xs text-fg-2 text-right">{formatNum(p.bot)}</td>
                      <td class="px-4 py-2 text-xs text-fg-2 text-right">{formatNum(p.human)}</td>
                      <td class="px-4 py-2 text-xs text-fg-2 text-right">{p.urls.toLocaleString()}</td>
                      <td class="px-4 py-2 text-xs text-right {botPct > 90 ? 'text-red-400' : botPct > 50 ? 'text-yellow-400' : 'text-green-400'}">{botPct.toFixed(0)}%</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
            {#if paramStats.length > 30}
              <div class="text-xs text-fg-2 mt-2">Showing top 30 of {paramStats.length} parameters</div>
            {/if}
          </div>
        {/if}
      {:else}
        <div class="text-center text-fg-2 text-sm py-8">No URL data available for directory analysis.</div>
      {/if}
    {/if}

    {#if tab === 'hourly'}
      <div class="flex justify-end mb-2">
        <button class="px-3 py-1.5 rounded-lg text-xs font-medium bg-surface-2 text-fg-2 hover:text-fg border border-border cursor-pointer" onclick={exportHourly}>Export CSV</button>
      </div>
      <div class="rounded-xl bg-surface-1 border border-border p-5">
        <h3 class="text-sm font-medium text-fg mb-4">Crawl Activity by Hour of Day</h3>
        <div class="relative flex items-end gap-1" style="height: 192px;">
          <!-- Average line -->
          {#if avgHourly > 0}
            {@const avgY = 180 - (avgHourly / maxHourly) * 180}
            <div class="absolute left-0 right-0 border-t border-dashed border-fg-2/30" style="bottom: {180 - avgY}px;">
              <span class="absolute -top-4 right-0 text-[9px] text-fg-2">avg {formatNum(Math.round(avgHourly))}</span>
            </div>
          {/if}
          {#each hourlyHits as hits, hour}
            {@const barH = maxHourly > 0 ? Math.max(hits > 0 ? 4 : 0, (hits / maxHourly) * 180) : 0}
            {@const botH = barH * hourlyBotRatio}
            {@const humanH = barH - botH}
            {@const isPeak = hour === peakHour}
            <div class="flex-1 flex flex-col items-center justify-end" style="height: 192px;">
              <div class="w-full rounded-t overflow-hidden transition-all duration-300 {isPeak ? 'ring-1 ring-accent' : ''}"
                style="height: {barH}px;"
                title="{hour}:00 - {hits.toLocaleString()} hits ({Math.round(totalHits > 0 ? (hits / totalHits) * 100 : 0)}%)"
              >
                <div class="w-full bg-accent/50" style="height: {humanH}px;"></div>
                <div class="w-full bg-accent" style="height: {botH}px;"></div>
              </div>
              <span class="text-[10px] text-fg-2 mt-1 shrink-0 {isPeak ? 'text-accent font-bold' : ''}">{hour}</span>
            </div>
          {/each}
        </div>
        <div class="flex items-center justify-center gap-4 text-xs text-fg-2 mt-3">
          <span>Hour of day (UTC)</span>
          <span class="flex items-center gap-1"><span class="inline-block w-3 h-3 rounded bg-accent"></span> Bot</span>
          <span class="flex items-center gap-1"><span class="inline-block w-3 h-3 rounded bg-accent/50"></span> Human</span>
        </div>
      </div>
    {/if}

    {#if tab === 'trends'}
      <div class="flex items-center justify-between mb-3">
        <div class="flex gap-1">
          {#each [['total', 'Total'], ['category', 'By Category'], ['botvshuman', 'Bot vs Human']] as [key, label]}
            <button
              class="px-3 py-1.5 rounded-lg text-xs font-medium cursor-pointer transition-colors
                {trendMode === key ? 'bg-accent/20 text-accent' : 'bg-surface-2 text-fg-2 hover:text-fg border border-border'}"
              onclick={() => trendMode = key as typeof trendMode}
            >{label}</button>
          {/each}
        </div>
        <button class="px-3 py-1.5 rounded-lg text-xs font-medium bg-surface-2 text-fg-2 hover:text-fg border border-border cursor-pointer" onclick={exportDailyTrends}>Export CSV</button>
      </div>

      {#if sortedDays.length === 0}
        <div class="text-fg-2 text-sm py-8 text-center">No daily data available (log may cover a single day)</div>
      {:else}
        <div class="rounded-xl bg-surface-1 border border-border p-5">
          <h3 class="text-sm font-medium text-fg mb-4">
            {trendMode === 'total' ? 'Total Hits per Day' : trendMode === 'category' ? 'Hits by Bot Category per Day' : 'Bot vs Human Hits per Day'}
          </h3>

          {#if trendMode === 'total'}
            <!-- Total hits bar chart -->
            <div class="flex items-end gap-px" style="height: 200px;">
              {#each sortedDays as day}
                {@const v = dailyHits[day] || 0}
                {@const h = Math.max(v > 0 ? 2 : 0, (v / maxDaily) * 190)}
                <div class="flex-1 flex flex-col items-center justify-end" style="height: 200px;">
                  <div class="w-full rounded-t bg-accent/70 hover:bg-accent transition-all"
                    style="height: {h}px;"
                    title="{day}: {v.toLocaleString()} hits"
                  ></div>
                </div>
              {/each}
            </div>
          {:else if trendMode === 'category'}
            <!-- Stacked by category -->
            {@const categories = Object.keys(categoryDailyHits).sort()}
            {@const maxStacked = Math.max(...sortedDays.map(d => {
              let sum = 0;
              for (const cat of categories) sum += (categoryDailyHits[cat]?.[d] || 0);
              return sum;
            }), 1)}
            <div class="flex items-end gap-px" style="height: 200px;">
              {#each sortedDays as day}
                {@const segments = categories.map(cat => ({ cat, val: categoryDailyHits[cat]?.[day] || 0 }))}
                {@const total = segments.reduce((s, seg) => s + seg.val, 0)}
                {@const barH = Math.max(total > 0 ? 2 : 0, (total / maxStacked) * 190)}
                <div class="flex-1 flex flex-col justify-end rounded-t overflow-hidden" style="height: 200px;" title="{day}: {total.toLocaleString()} bot hits">
                  {#each segments as seg}
                    {@const segH = total > 0 ? (seg.val / total) * barH : 0}
                    {#if segH > 0}
                      <div style="height: {segH}px; background: {categoryBg(seg.cat)}"></div>
                    {/if}
                  {/each}
                </div>
              {/each}
            </div>
            <!-- Legend -->
            <div class="flex flex-wrap gap-3 mt-3">
              {#each categories as cat}
                <span class="flex items-center gap-1 text-xs text-fg-2">
                  <span class="inline-block w-3 h-3 rounded" style="background: {categoryBg(cat)}"></span>
                  {cat.replace('_', ' ')}
                </span>
              {/each}
            </div>
          {:else}
            <!-- Bot vs Human stacked -->
            {@const maxBH = Math.max(...sortedDays.map(d => dailyHits[d] || 0), 1)}
            <div class="flex items-end gap-px" style="height: 200px;">
              {#each sortedDays as day}
                {@const total = dailyHits[day] || 0}
                {@const human = humanDailyHits[day] || 0}
                {@const bot = total - human}
                {@const barH = Math.max(total > 0 ? 2 : 0, (total / maxBH) * 190)}
                {@const botH = total > 0 ? (bot / total) * barH : 0}
                {@const humanH = barH - botH}
                <div class="flex-1 flex flex-col justify-end rounded-t overflow-hidden" style="height: 200px;" title="{day}: {bot.toLocaleString()} bot, {human.toLocaleString()} human">
                  {#if humanH > 0}
                    <div class="bg-accent/40" style="height: {humanH}px;"></div>
                  {/if}
                  {#if botH > 0}
                    <div class="bg-accent" style="height: {botH}px;"></div>
                  {/if}
                </div>
              {/each}
            </div>
            <div class="flex items-center justify-center gap-4 text-xs text-fg-2 mt-3">
              <span class="flex items-center gap-1"><span class="inline-block w-3 h-3 rounded bg-accent"></span> Bot</span>
              <span class="flex items-center gap-1"><span class="inline-block w-3 h-3 rounded bg-accent/40"></span> Human</span>
            </div>
          {/if}

          <!-- X axis labels -->
          {#if sortedDays.length > 1}
            <div class="flex justify-between text-[10px] text-fg-2 mt-1">
              <span>{formatShortDate(sortedDays[0])}</span>
              {#if sortedDays.length > 4}
                <span>{formatShortDate(sortedDays[Math.floor(sortedDays.length / 2)])}</span>
              {/if}
              <span>{formatShortDate(sortedDays[sortedDays.length - 1])}</span>
            </div>
          {/if}
        </div>

        <!-- Top movers: bots with biggest daily change -->
        {#if sortedDays.length > 2}
          {@const movers = botEntries
            .filter(b => botDailyHits[b.name] && Object.keys(botDailyHits[b.name]).length > 1)
            .map(b => {
              const daily = botDailyHits[b.name];
              const first3 = sortedDays.slice(0, Math.min(3, sortedDays.length));
              const last3 = sortedDays.slice(-Math.min(3, sortedDays.length));
              const avgFirst = first3.reduce((s, d) => s + (daily[d] || 0), 0) / first3.length;
              const avgLast = last3.reduce((s, d) => s + (daily[d] || 0), 0) / last3.length;
              const change = avgFirst > 0 ? ((avgLast - avgFirst) / avgFirst) * 100 : avgLast > 0 ? 100 : 0;
              return { name: b.name, category: b.category, change, avgFirst: Math.round(avgFirst), avgLast: Math.round(avgLast) };
            })
            .filter(m => Math.abs(m.change) > 10)
            .sort((a, b) => Math.abs(b.change) - Math.abs(a.change))
            .slice(0, 5)}
          {#if movers.length > 0}
            <div class="rounded-xl bg-surface-1 border border-border p-5">
              <h3 class="text-sm font-medium text-fg mb-3">Biggest Movers</h3>
              <div class="space-y-2">
                {#each movers as m}
                  <div class="flex items-center gap-3">
                    <span class="w-40 text-xs text-fg truncate">{m.name}</span>
                    <span class="w-20 text-xs {categoryColor(m.category)}">{m.category.replace('_', ' ')}</span>
                    <span class="text-xs font-mono {m.change > 0 ? 'text-green-400' : 'text-red-400'}">
                      {m.change > 0 ? '+' : ''}{m.change.toFixed(0)}%
                    </span>
                    <span class="text-xs text-fg-2">{m.avgFirst}/day -> {m.avgLast}/day</span>
                  </div>
                {/each}
              </div>
            </div>
          {/if}
        {/if}
      {/if}
    {/if}

    {#if tab === 'google'}
      {#if googlebotVariants.length === 0}
        <div class="text-fg-2 text-sm py-8 text-center">No Googlebot activity detected in this log.</div>
      {:else}
        <!-- Googlebot Summary -->
        <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">Total Googlebot Hits</div>
            <div class="text-lg font-semibold text-fg">{formatNum(googlebotTotalHits)}</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">% of All Bot Hits</div>
            <div class="text-lg font-semibold text-blue-400">{botHitsTotal > 0 ? ((googlebotTotalHits / botHitsTotal) * 100).toFixed(1) : 0}%</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">Mobile-First Ratio</div>
            <div class="text-lg font-semibold {googlebotTotalHits > 0 && googlebotMobileHits / googlebotTotalHits > 0.5 ? 'text-green-400' : 'text-orange-400'}">
              {googlebotTotalHits > 0 ? ((googlebotMobileHits / googlebotTotalHits) * 100).toFixed(0) : 0}% mobile
            </div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">Variants Detected</div>
            <div class="text-lg font-semibold text-fg">{googlebotVariants.length}</div>
          </div>
        </div>

        <!-- Mobile vs Desktop donut -->
        {#if googlebotTotalHits > 0}
          <div class="rounded-xl bg-surface-1 border border-border p-5">
            <h3 class="text-sm font-medium text-fg mb-4">Mobile vs Desktop Crawl</h3>
            <div class="flex items-center gap-8">
              <svg viewBox="0 0 120 120" class="w-32 h-32 shrink-0">
                {#if googlebotMobileHits > 0}
                  {@const mobileAngle = (googlebotMobileHits / googlebotTotalHits) * Math.PI * 2}
                  <path d={donutPath(-Math.PI / 2, -Math.PI / 2 + Math.min(mobileAngle, Math.PI * 1.999), 45, 60, 60)} fill="none" stroke="#60a5fa" stroke-width="14" />
                  {#if googlebotDesktopHits > 0}
                    <path d={donutPath(-Math.PI / 2 + mobileAngle, -Math.PI / 2 + Math.PI * 1.999, 45, 60, 60)} fill="none" stroke="#94a3b8" stroke-width="14" />
                  {/if}
                {:else}
                  <circle cx="60" cy="60" r="45" fill="none" stroke="#94a3b8" stroke-width="14" />
                {/if}
              </svg>
              <div class="flex flex-col gap-3">
                <div class="flex items-center gap-2">
                  <span class="inline-block w-3 h-3 rounded bg-blue-400"></span>
                  <span class="text-sm text-fg">Mobile: {formatNum(googlebotMobileHits)} ({((googlebotMobileHits / googlebotTotalHits) * 100).toFixed(0)}%)</span>
                </div>
                <div class="flex items-center gap-2">
                  <span class="inline-block w-3 h-3 rounded bg-gray-400"></span>
                  <span class="text-sm text-fg">Desktop: {formatNum(googlebotDesktopHits)} ({((googlebotDesktopHits / googlebotTotalHits) * 100).toFixed(0)}%)</span>
                </div>
              </div>
            </div>
          </div>
        {/if}

        <!-- Variant breakdown table -->
        <div class="rounded-xl border border-border overflow-hidden">
          <table class="w-full text-sm">
            <thead>
              <tr class="bg-surface-2 text-fg-2 text-left text-xs uppercase tracking-wider">
                <th class="px-4 py-3">Variant</th>
                <th class="px-4 py-3">Hits</th>
                <th class="px-4 py-3">Unique URLs</th>
                <th class="px-4 py-3">% of Googlebot</th>
                <th class="px-4 py-3">Bandwidth</th>
                <th class="px-4 py-3">Mobile</th>
                <th class="px-4 py-3">Rate/h</th>
                <th class="px-4 py-3">Trend</th>
              </tr>
            </thead>
            <tbody>
              {#each googlebotVariants as v}
                <tr class="border-t border-border hover:bg-surface-2/50 transition-colors">
                  <td class="px-4 py-3 font-medium text-fg">{v.name}</td>
                  <td class="px-4 py-3 text-fg">{v.hits.toLocaleString()}</td>
                  <td class="px-4 py-3 text-fg-2">{v.uniqueUrls.toLocaleString()}</td>
                  <td class="px-4 py-3 text-fg-2">{googlebotTotalHits > 0 ? ((v.hits / googlebotTotalHits) * 100).toFixed(1) + '%' : '-'}</td>
                  <td class="px-4 py-3 text-fg-2">{v.bytes ? formatBytes(v.bytes) : '-'}</td>
                  <td class="px-4 py-3 text-fg-2">{v.mobile ? 'Yes' : '-'}</td>
                  <td class="px-4 py-3 text-fg-2 text-xs">{botCrawlRates[v.name] ? botCrawlRates[v.name].toLocaleString(undefined, { maximumFractionDigits: 1 }) : '-'}</td>
                  <td class="px-4 py-3">
                    {#if sortedDays.length > 1 && botDailyHits[v.name]}
                      <svg viewBox="0 0 100 20" class="w-24 h-5" preserveAspectRatio="none">
                        <path d={sparklinePath(v.name)} fill="none" stroke="#60a5fa" stroke-width="1.5" vector-effect="non-scaling-stroke" />
                      </svg>
                    {:else}
                      <span class="text-fg-2 text-xs">-</span>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>

        <!-- Googlebot daily trend -->
        {#if sortedDays.length > 1}
          {@const gbDaily = sortedDays.map(d => {
            let total = 0;
            for (const v of googlebotVariants) {
              total += (botDailyHits[v.name]?.[d] || 0);
            }
            return total;
          })}
          {@const maxGb = Math.max(...gbDaily, 1)}
          <div class="rounded-xl bg-surface-1 border border-border p-5">
            <h3 class="text-sm font-medium text-fg mb-3">Googlebot Daily Activity</h3>
            <div class="flex items-end gap-px" style="height: 120px;">
              {#each sortedDays as day, i}
                {@const v = gbDaily[i]}
                {@const h = Math.max(v > 0 ? 2 : 0, (v / maxGb) * 110)}
                <div class="flex-1 flex flex-col items-center justify-end" style="height: 120px;">
                  <div class="w-full rounded-t bg-blue-400/70 hover:bg-blue-400 transition-all"
                    style="height: {h}px;"
                    title="{day}: {v.toLocaleString()} Googlebot hits"
                  ></div>
                </div>
              {/each}
            </div>
            <div class="flex justify-between text-[10px] text-fg-2 mt-1">
              <span>{formatShortDate(sortedDays[0])}</span>
              <span>{formatShortDate(sortedDays[sortedDays.length - 1])}</span>
            </div>
          </div>
        {/if}
      {/if}
    {/if}

    {#if tab === 'heatmap'}
      <div class="rounded-xl bg-surface-1 border border-border p-5">
        <div class="flex justify-between items-center mb-4">
          <h3 class="text-sm font-medium text-fg">Crawl Intensity Heatmap (Hour x Day of Week)</h3>
          <select bind:value={selectedHeatmapBot} class="px-2 py-1 rounded text-xs bg-surface-2 border border-border text-fg">
            <option value="all">All Traffic</option>
            {#each Object.keys(botHeatmaps) as botName}
              <option value={botName}>{botName}</option>
            {/each}
          </select>
        </div>
        {#if true}
        {@const activeHeatmap = selectedHeatmapBot === 'all' ? heatmapData : (botHeatmaps[selectedHeatmapBot] || heatmapData)}
        {@const maxVal = Math.max(...activeHeatmap.flat(), 1)}
        <div class="overflow-x-auto">
          <table class="w-full text-xs">
            <thead>
              <tr>
                <th class="px-2 py-1 text-fg-2"></th>
                {#each Array(24) as _, h}
                  <th class="px-1 py-1 text-fg-2 text-center">{h}</th>
                {/each}
              </tr>
            </thead>
            <tbody>
              {#each dayNames as day, d}
                <tr>
                  <td class="px-2 py-1 text-fg-2 font-medium">{day}</td>
                  {#each Array(24) as _, h}
                    {@const val = activeHeatmap[d]?.[h] || 0}
                    {@const intensity = val / maxVal}
                    <td class="px-0.5 py-0.5">
                      <div
                        class="w-full h-6 rounded-sm transition-colors"
                        style="background-color: {heatColor(intensity, val > 0)}"
                        title="{day} {h}:00 — {val.toLocaleString()} hits ({(intensity * 100).toFixed(1)}% of peak)"
                      ></div>
                    </td>
                  {/each}
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        <div class="flex items-center justify-between mt-3 text-[10px] text-fg-2">
          <div class="flex items-center gap-2">
            <span>Low</span>
            <div class="flex gap-0.5">
              {#each [0.05, 0.15, 0.35, 0.55, 0.75, 1.0] as t}
                <div class="w-5 h-3 rounded-sm" style="background-color: {heatColor(t, true)}"></div>
              {/each}
            </div>
            <span>High</span>
          </div>
          <span class="text-fg-2">Peak: {maxVal.toLocaleString()} hits</span>
        </div>
        {/if}
      </div>
    {/if}

    {#if tab === 'ai'}
      {#if aiBotEntries.length > 0}
        <!-- AI Bot summary -->
        <div class="grid grid-cols-3 gap-4 mb-4">
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">AI Bots Detected</div>
            <div class="text-2xl font-bold text-fg">{aiBotEntries.length}</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">AI Bot Hits</div>
            <div class="text-2xl font-bold text-fg">{aiBotEntries.reduce((s, b) => s + b.hits, 0).toLocaleString()}</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">AI Bot Share</div>
            <div class="text-2xl font-bold text-fg">{((crawlBudget.aiBotShare as number) * 100).toFixed(1)}%</div>
          </div>
        </div>

        <!-- AI bot table -->
        <div class="rounded-xl border border-border overflow-hidden mb-4">
          <table class="w-full text-sm">
            <thead>
              <tr class="bg-surface-2 text-fg-2 text-left text-xs uppercase tracking-wider">
                <th class="px-4 py-3">Bot</th>
                <th class="px-4 py-3">Type</th>
                <th class="px-4 py-3">Hits</th>
                <th class="px-4 py-3">Unique URLs</th>
                <th class="px-4 py-3">Bandwidth</th>
                <th class="px-4 py-3">Rate/h</th>
                <th class="px-4 py-3">First Seen</th>
                <th class="px-4 py-3">Last Seen</th>
                <th class="px-4 py-3">Trend</th>
              </tr>
            </thead>
            <tbody>
              {#each aiBotEntries.sort((a, b) => b.hits - a.hits) as bot}
                <tr class="border-t border-border hover:bg-surface-2/50 transition-colors">
                  <td class="px-4 py-3 font-medium text-fg">{bot.name}</td>
                  <td class="px-4 py-3 text-fg-2 text-xs">{bot.category.replace('_', ' ')}</td>
                  <td class="px-4 py-3 text-fg">{bot.hits.toLocaleString()}</td>
                  <td class="px-4 py-3 text-fg-2">{bot.uniqueUrls.toLocaleString()}</td>
                  <td class="px-4 py-3 text-fg-2">{bot.bytes ? formatBytes(bot.bytes) : '-'}</td>
                  <td class="px-4 py-3 text-fg-2 text-xs">{botCrawlRates[bot.name] ? botCrawlRates[bot.name].toLocaleString(undefined, { maximumFractionDigits: 1 }) : '-'}</td>
                  <td class="px-4 py-3 text-fg-2 text-xs">{formatDate(bot.firstSeen)}</td>
                  <td class="px-4 py-3 text-fg-2 text-xs">{formatDate(bot.lastSeen)}</td>
                  <td class="px-4 py-3">
                    {#if sortedDays.length > 1 && botDailyHits[bot.name]}
                      <svg viewBox="0 0 100 20" class="w-24 h-5" preserveAspectRatio="none">
                        <path d={sparklinePath(bot.name)} fill="none" stroke="#8b5cf6" stroke-width="1.5" vector-effect="non-scaling-stroke" />
                      </svg>
                    {:else}
                      <span class="text-fg-2 text-xs">-</span>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>

        <!-- AI Bot daily trends stacked chart -->
        {#if aiBotDays.length > 1}
          {@const aiColors = ['#8b5cf6', '#a78bfa', '#c4b5fd', '#ddd6fe', '#ede9fe', '#6d28d9', '#5b21b6']}
          {@const aiBotNames = Object.keys(aiBotTrends).sort((a, b) => {
            const aTotal = Object.values(aiBotTrends[a]).reduce((s, v) => s + v, 0);
            const bTotal = Object.values(aiBotTrends[b]).reduce((s, v) => s + v, 0);
            return bTotal - aTotal;
          })}
          {@const maxAIDaily = Math.max(...aiBotDays.map(d => aiBotNames.reduce((s, b) => s + (aiBotTrends[b]?.[d] || 0), 0)), 1)}
          <div class="rounded-xl bg-surface-1 border border-border p-5">
            <h3 class="text-sm font-medium text-fg mb-3">AI Bot Activity Over Time</h3>
            <div class="flex items-end gap-px" style="height: 120px;">
              {#each aiBotDays as day}
                {@const dayTotal = aiBotNames.reduce((s, b) => s + (aiBotTrends[b]?.[day] || 0), 0)}
                <div class="flex-1 flex flex-col justify-end" style="height: 120px;" title="{day}: {dayTotal.toLocaleString()} AI bot hits">
                  {#each aiBotNames as botName, bi}
                    {@const val = aiBotTrends[botName]?.[day] || 0}
                    {#if val > 0}
                      <div class="w-full" style="height: {Math.max((val / maxAIDaily) * 110, 1)}px; background-color: {aiColors[bi % aiColors.length]}"></div>
                    {/if}
                  {/each}
                </div>
              {/each}
            </div>
            <div class="flex justify-between text-[10px] text-fg-2 mt-1">
              <span>{formatShortDate(aiBotDays[0])}</span>
              <span>{formatShortDate(aiBotDays[aiBotDays.length - 1])}</span>
            </div>
            <div class="flex flex-wrap gap-3 mt-3">
              {#each aiBotNames as botName, bi}
                <div class="flex items-center gap-1">
                  <span class="inline-block w-3 h-3 rounded" style="background-color: {aiColors[bi % aiColors.length]}"></span>
                  <span class="text-xs text-fg-2">{botName}</span>
                </div>
              {/each}
            </div>
          </div>
        {/if}
      {:else}
        <div class="text-center text-fg-2 text-sm py-12">No AI bots detected in this log file.</div>
      {/if}
    {/if}

    {#if tab === 'merge'}
      {#if !mergeData}
        <div class="rounded-xl bg-surface-1 border border-border p-6 text-center">
          <h3 class="text-sm font-medium text-fg mb-2">Merge Log Data with Crawl Results</h3>
          <p class="text-xs text-fg-2 mb-4">Cross-reference bot activity from logs with crawl data to identify orphan pages, ghost pages, crawl waste, and indexability mismatches.</p>
          {#if crawlJobs.length === 0}
            <button class="px-4 py-2 rounded-lg text-sm bg-accent/20 text-accent hover:bg-accent/30 cursor-pointer" onclick={loadCrawlJobs}>
              Load Available Crawls
            </button>
          {:else}
            <div class="flex items-center justify-center gap-3 flex-wrap">
              <select bind:value={selectedCrawlId} class="px-3 py-1.5 rounded-lg text-sm bg-surface-2 border border-border text-fg focus:outline-none focus:ring-1 focus:ring-accent min-w-[28rem] max-w-full">
                <option value="">Select a crawl...</option>
                {#each crawlJobs as cj}
                  <option value={cj.id}>{formatCrawlOption(cj)}</option>
                {/each}
              </select>
              <button
                class="px-4 py-2 rounded-lg text-sm bg-accent/20 text-accent hover:bg-accent/30 cursor-pointer disabled:opacity-50"
                onclick={runMerge}
                disabled={!selectedCrawlId || merging}
              >{merging ? 'Merging...' : 'Run Merge'}</button>
            </div>
          {/if}
          {#if merging}
            <div class="mt-4 max-w-md mx-auto text-left">
              <div class="flex items-center justify-between mb-2">
                <div class="text-xs text-fg flex items-center gap-2">
                  <span class="inline-block w-3 h-3 rounded-full border-2 border-accent border-t-transparent animate-spin"></span>
                  {MERGE_PHASE_LABELS[mergePhase] || 'Working…'}
                </div>
                <div class="text-xs text-fg-2 font-mono">{(mergeElapsedMs / 1000).toFixed(1)}s</div>
              </div>
              <div class="h-1.5 rounded-full bg-surface-3 overflow-hidden">
                <div class="h-full bg-accent transition-all duration-300" style="width: {mergePct}%"></div>
              </div>
              {#if mergePageCount > 0 || mergeLogUrls > 0}
                <div class="text-[10px] text-fg-2 mt-2 flex justify-between">
                  {#if mergePageCount > 0}<span>{mergePageCount.toLocaleString()} crawl pages</span>{/if}
                  {#if mergeLogUrls > 0}<span>{mergeLogUrls.toLocaleString()} log URLs</span>{/if}
                </div>
              {/if}
              {#if mergePhase === 'loading_crawl_pages' && mergeElapsedMs > 5000}
                <div class="text-[10px] text-fg-2 mt-2 italic">
                  Cold-loading {mergePageCount > 0 ? mergePageCount.toLocaleString() + ' ' : ''}pages from disk and building inlink/anchor indices.
                  This can take 30 s – 2 min for large crawls; subsequent merges of the same crawl are instant (cached in memory).
                </div>
              {/if}
            </div>
          {/if}
          {#if mergeError}
            <div class="mt-3 p-2 rounded-lg bg-red-500/10 border border-red-500/20 text-red-400 text-xs">{mergeError}</div>
          {/if}
        </div>
      {:else}
        {@const segments = (mergeData.segments as Record<string, number>) || {}}
        {@const segEntries = Object.entries(segments).sort((a, b) => b[1] - a[1])}
        {@const totalSegmented = segEntries.reduce((s, [, v]) => s + v, 0)}
        <!-- All counts come from the server-side MergeSummary (true totals). -->
        {@const orphanTotal = (mergeData.orphanCount as number) || segments['orphan_crawled'] || 0}
        {@const ghostTotal = (mergeData.ghostCount as number) || segments['uncrawled_indexable'] || 0}
        {@const mismatchTotal = (mergeData.mismatchCount as number) || 0}
        {@const healthyTotal = (mergeData.healthyCount as number) || segments['healthy'] || 0}
        {@const logUrlsTotal = (mergeData.logUrlsTotal as number) || 0}
        {@const crawlPagesTotal = (mergeData.crawlPages as number) || 0}
        {@const crawlWasteTotal = segments['crawl_waste'] || 0}

        <button class="mb-3 px-3 py-1 rounded text-xs bg-surface-2 text-fg-2 hover:text-fg border border-border cursor-pointer" onclick={() => { mergeData = null; }}>New Merge</button>

        <!-- Segment summary cards -->
        <div class="grid grid-cols-4 gap-3 mb-4">
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2">Total Pages</div>
            <div class="text-xl font-bold text-fg">{totalSegmented.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">union of crawl + logs</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2">URLs in Logs</div>
            <div class="text-xl font-bold text-blue-400">{logUrlsTotal.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">distinct URLs from logs</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2">URLs in Crawl</div>
            <div class="text-xl font-bold text-cyan-400">{crawlPagesTotal.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">pages from crawl data</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2">Healthy Pages</div>
            <div class="text-xl font-bold text-emerald-400">{healthyTotal.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">indexable + bot crawled</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2">Orphan Pages</div>
            <div class="text-xl font-bold text-purple-400">{orphanTotal.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">in logs, not in crawl</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2">Ghost Pages</div>
            <div class="text-xl font-bold text-fg-2">{ghostTotal.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">in crawl, no bot traffic</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2">Crawl Waste</div>
            <div class="text-xl font-bold text-orange-400">{crawlWasteTotal.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">non-indexable, still crawled</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2">Status Mismatches</div>
            <div class="text-xl font-bold text-yellow-400">{mismatchTotal.toLocaleString()}</div>
            <div class="text-[10px] text-fg-2">log vs crawl differ</div>
          </div>
        </div>

        {#if mergeData.cappedUrls}
        <div class="rounded-lg bg-amber-500/10 border border-amber-500/20 text-amber-300 text-xs p-3 mb-4">
          <strong>Warning — capped URL data:</strong>
          This log job only has the top 500 URLs stored (older analysis format). The merge compared only {logUrlsTotal.toLocaleString()} log URLs against {crawlPagesTotal.toLocaleString()} crawl pages, so most crawl pages appear as "Ghost Pages" incorrectly.
          <strong>Fix:</strong> re-upload the log files to re-analyze with full URL coverage, then merge again.
        </div>
        {:else}
        <!-- Coverage note: full streaming merge persisted to SQLite cache.
             The table below is server-paginated/sortable/filterable. -->
        <div class="rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-emerald-300 text-xs p-3 mb-4">
          <strong>Full coverage:</strong>
          all {totalSegmented.toLocaleString()} pages cross-referenced and persisted. Use the table below to search, sort, and filter — every row is queryable, not capped.
        </div>
        {/if}

        <!-- URL coverage Venn (crawl ∪ logs ∪ both) — regions are clickable
             and filter the merged-pages table below. -->
        <!-- Use crawlPages from merge summary for accurate crawl-side total.
             Multiple log URLs (with query params) can match the same crawl page,
             so computing inBoth from segments inflates the crawl side. -->
        {@const inBothCrawlSide = Math.max(0, crawlPagesTotal - ghostTotal)}
        {@const inBothLogSide = Math.max(0, logUrlsTotal - orphanTotal)}
        {@const inBoth = inBothLogSide}
        {@const totalCrawlSide = crawlPagesTotal}
        {@const totalLogsSide = logUrlsTotal}
        <div class="rounded-xl bg-surface-1 border border-border p-5 mb-4">
          <div class="flex items-center justify-between mb-3">
            <h3 class="text-sm font-medium text-fg">URL Coverage Overlap</h3>
            <span class="text-[10px] text-fg-2 italic">Click a region to filter the table below</span>
          </div>
          <div class="flex items-center gap-6 flex-wrap">
            <svg viewBox="0 0 420 220" class="w-full max-w-md flex-shrink-0">
              <!-- Region 1: Crawl-only (left lune). Approximated as a clip mask
                   over the crawl circle minus the logs circle. We render an
                   invisible-but-clickable rectangle wedge first, then visible
                   circles on top — the order matters for hit testing. -->

              <!-- Crawl circle (blue) — clickable as a whole, but the inner
                   region click is handled by the "both" hotspot which sits
                   on top in the SVG order. -->
              <circle cx="150" cy="120" r="95"
                fill={coverageFilter === 'crawl_only' ? 'rgba(88,166,255,0.55)' : 'rgba(88,166,255,0.32)'}
                stroke="#58a6ff"
                stroke-width={coverageFilter === 'crawl_only' ? '4' : '2'}
                class="cursor-pointer transition-all"
                onclick={() => clickVennRegion('crawl_only')}
                onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); clickVennRegion('crawl_only'); } }}
                role="button"
                tabindex="0"
                aria-label="Filter table to crawl-only URLs"
              >
                <title>Click to filter: Crawl only ({ghostTotal.toLocaleString()} pages)</title>
              </circle>
              <!-- Logs circle (orange) -->
              <circle cx="270" cy="120" r="95"
                fill={coverageFilter === 'logs_only' ? 'rgba(251,146,60,0.55)' : 'rgba(251,146,60,0.32)'}
                stroke="#fb923c"
                stroke-width={coverageFilter === 'logs_only' ? '4' : '2'}
                class="cursor-pointer transition-all"
                onclick={() => clickVennRegion('logs_only')}
                onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); clickVennRegion('logs_only'); } }}
                role="button"
                tabindex="0"
                aria-label="Filter table to logs-only URLs"
              >
                <title>Click to filter: Logs only ({orphanTotal.toLocaleString()} pages)</title>
              </circle>
              <!-- "Both" hotspot — small ellipse covering the lens area where
                   the two circles overlap, on top so it captures clicks first. -->
              <ellipse cx="210" cy="120" rx="35" ry="80"
                fill={coverageFilter === 'both' ? 'rgba(167,139,250,0.55)' : 'rgba(167,139,250,0.18)'}
                stroke={coverageFilter === 'both' ? '#a78bfa' : 'transparent'}
                stroke-width="2"
                class="cursor-pointer transition-all"
                onclick={() => clickVennRegion('both')}
                onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); clickVennRegion('both'); } }}
                role="button"
                tabindex="0"
                aria-label="Filter table to URLs in both crawl and logs"
              >
                <title>Click to filter: In both ({inBoth.toLocaleString()} pages)</title>
              </ellipse>

              <!-- Header labels above each circle -->
              <text x="100" y="22" text-anchor="middle" fill="#58a6ff" font-size="11" font-weight="600" pointer-events="none">Crawled URLs</text>
              <text x="100" y="36" text-anchor="middle" fill="#8b949e" font-size="10" pointer-events="none">{totalCrawlSide.toLocaleString()}</text>
              <text x="320" y="22" text-anchor="middle" fill="#fb923c" font-size="11" font-weight="600" pointer-events="none">Bot-Hit URLs</text>
              <text x="320" y="36" text-anchor="middle" fill="#8b949e" font-size="10" pointer-events="none">{totalLogsSide.toLocaleString()}</text>

              <!-- Counts inside each region (pointer-events:none so clicks pass
                   through to the underlying shapes) -->
              <text x="80" y="120" text-anchor="middle" fill="#c9d1d9" font-size="16" font-weight="700" pointer-events="none">{ghostTotal.toLocaleString()}</text>
              <text x="80" y="140" text-anchor="middle" fill="#8b949e" font-size="10" pointer-events="none">crawl only</text>
              <text x="80" y="154" text-anchor="middle" fill="#8b949e" font-size="9" pointer-events="none">(ghosts)</text>

              <text x="210" y="120" text-anchor="middle" fill="#c9d1d9" font-size="16" font-weight="700" pointer-events="none">{inBoth.toLocaleString()}</text>
              <text x="210" y="140" text-anchor="middle" fill="#8b949e" font-size="10" pointer-events="none">in both</text>
              <text x="210" y="154" text-anchor="middle" fill="#8b949e" font-size="9" pointer-events="none">(matched)</text>

              <text x="340" y="120" text-anchor="middle" fill="#c9d1d9" font-size="16" font-weight="700" pointer-events="none">{orphanTotal.toLocaleString()}</text>
              <text x="340" y="140" text-anchor="middle" fill="#8b949e" font-size="10" pointer-events="none">logs only</text>
              <text x="340" y="154" text-anchor="middle" fill="#8b949e" font-size="9" pointer-events="none">(orphans)</text>
            </svg>

            <!-- Side panel with derived ratios -->
            <div class="text-xs text-fg-2 space-y-2 flex-1 min-w-[220px]">
              <div>
                <div class="text-fg-2/70 mb-0.5">Bot coverage of crawled site</div>
                <div class="text-fg font-mono">
                  {totalCrawlSide > 0 ? ((inBoth / totalCrawlSide) * 100).toFixed(2) : '0.00'}%
                </div>
                <div class="text-[10px]">
                  {inBothCrawlSide.toLocaleString()} of {totalCrawlSide.toLocaleString()} crawled pages got bot traffic
                </div>
              </div>
              <div>
                <div class="text-fg-2/70 mb-0.5">Crawl coverage of bot universe</div>
                <div class="text-fg font-mono">
                  {totalLogsSide > 0 ? ((inBothLogSide / totalLogsSide) * 100).toFixed(2) : '0.00'}%
                </div>
                <div class="text-[10px]">
                  {inBothLogSide.toLocaleString()} of {totalLogsSide.toLocaleString()} bot-visited URLs matched a crawled page
                </div>
              </div>
              {#if orphanTotal > inBoth * 2}
                <div class="text-[10px] text-purple-300 pt-2 border-t border-border">
                  Bots discover {(orphanTotal / Math.max(inBoth, 1)).toFixed(1)}× more URLs than your internal linking surfaces — sitemap and external links may be the source.
                </div>
              {/if}
              {#if ghostTotal > inBoth * 2}
                <div class="text-[10px] text-amber-300">
                  {((ghostTotal / totalCrawlSide) * 100).toFixed(0)}% of your crawled pages weren't visited in this period — likely deep / low-PageRank tail.
                </div>
              {/if}
            </div>
          </div>
        </div>

        <!-- Segment donut + breakdown -->
        <div class="grid grid-cols-2 gap-4 mb-4">
          <div class="rounded-xl bg-surface-1 border border-border p-5">
            <h3 class="text-sm font-medium text-fg mb-3">Page Segmentation</h3>
            <div class="flex items-center gap-6">
              <svg viewBox="0 0 120 120" class="w-32 h-32">
                {#if totalSegmented > 0}
                  {#each segEntries as [seg, count], i}
                    {@const startAngle = -Math.PI / 2 + segEntries.slice(0, i).reduce((s, [, c]) => s + (c / totalSegmented) * Math.PI * 2, 0)}
                    {@const angle = (count / totalSegmented) * Math.PI * 2}
                    <path d={donutPath(startAngle, startAngle + Math.min(angle, Math.PI * 1.999), 45, 60, 60)} fill="none" stroke={segmentColors[seg] || '#6b7280'} stroke-width="14" />
                  {/each}
                {/if}
              </svg>
              <div class="flex flex-col gap-2">
                {#each segEntries as [seg, count]}
                  <div class="flex items-center gap-2">
                    <span class="inline-block w-3 h-3 rounded" style="background-color: {segmentColors[seg] || '#6b7280'}"></span>
                    <span class="text-xs text-fg">{segmentLabels[seg] || seg}: {count}</span>
                  </div>
                {/each}
              </div>
            </div>
          </div>

          <!-- Key insights -->
          <div class="rounded-xl bg-surface-1 border border-border p-5">
            <h3 class="text-sm font-medium text-fg mb-3">Key Insights</h3>
            <div class="space-y-2 text-xs">
              {#if (segments['crawl_waste'] || 0) > 0}
                <div class="p-2 rounded bg-red-500/10 text-red-400">{(segments['crawl_waste'] || 0).toLocaleString()} non-indexable pages consuming crawl budget</div>
              {/if}
              {#if (segments['redirect_waste'] || 0) > 0}
                <div class="p-2 rounded bg-orange-500/10 text-orange-400">{(segments['redirect_waste'] || 0).toLocaleString()} redirects wasting bot hits</div>
              {/if}
              {#if orphanTotal > 0}
                <div class="p-2 rounded bg-purple-500/10 text-purple-400">{orphanTotal.toLocaleString()} orphan pages found only in logs (not in site structure)</div>
              {/if}
              {#if ghostTotal > 0}
                <div class="p-2 rounded bg-surface-3 text-fg-2">{ghostTotal.toLocaleString()} ghost pages never visited by bots</div>
              {/if}
              {#if mismatchTotal > 0}
                <div class="p-2 rounded bg-yellow-500/10 text-yellow-400">{mismatchTotal.toLocaleString()} pages with status code mismatches between logs and crawl</div>
              {/if}
              {#if healthyTotal > 0}
                <div class="p-2 rounded bg-green-500/10 text-green-400">{healthyTotal.toLocaleString()} healthy pages (indexed and crawled)</div>
              {/if}
            </div>
          </div>
        </div>

        <!-- Unified paginated/sortable/filterable merge table -->
        <div id="merge-table-anchor" class="rounded-xl border border-border overflow-hidden">
          {#if coverageFilter}
            <div class="px-4 py-2 bg-purple-500/10 border-b border-purple-500/20 flex items-center justify-between text-xs">
              <span class="text-purple-300">
                <strong>Filtering:</strong> {coverageLabels[coverageFilter]} — {mergedTotal.toLocaleString()} pages
              </span>
              <button class="px-2 py-0.5 rounded bg-surface-2 border border-border text-fg-2 hover:text-fg cursor-pointer text-[10px]"
                onclick={() => clickVennRegion(coverageFilter)}>Clear</button>
            </div>
          {/if}
          <div class="bg-surface-2 px-4 py-3 flex items-center justify-between gap-3 flex-wrap">
            <div class="text-xs text-fg-2 font-medium uppercase tracking-wider">All Merged Pages — {mergedTotal.toLocaleString()} total</div>
            <div class="flex items-center gap-2 flex-wrap">
              <input
                type="text"
                placeholder="Search URL…"
                bind:value={mergedSearch}
                oninput={onSearchInput}
                class="px-2 py-1 rounded text-xs bg-surface-1 border border-border text-fg w-48 focus:outline-none focus:ring-1 focus:ring-accent"
              />
              <select
                bind:value={mergedSegmentFilter}
                onchange={onSegmentFilterChange}
                class="px-2 py-1 rounded text-xs bg-surface-1 border border-border text-fg focus:outline-none focus:ring-1 focus:ring-accent"
              >
                <option value="">All segments ({totalSegmented.toLocaleString()})</option>
                {#each segEntries as [seg, count]}
                  <option value={seg}>{segmentLabels[seg] || seg} ({count.toLocaleString()})</option>
                {/each}
              </select>
              <select
                bind:value={mergedPageSize}
                onchange={() => { mergedPage = 1; void fetchMergedPages(); }}
                class="px-2 py-1 rounded text-xs bg-surface-1 border border-border text-fg focus:outline-none focus:ring-1 focus:ring-accent"
              >
                {#each [25, 50, 100, 250, 500] as n}
                  <option value={n}>{n}/page</option>
                {/each}
              </select>
            </div>
          </div>

          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="bg-surface-2 text-fg-2 text-left text-xs uppercase tracking-wider select-none">
                  <th class="px-4 py-2 cursor-pointer hover:text-fg" onclick={() => toggleSort('url')}>URL{sortIndicator('url')}</th>
                  <th class="px-4 py-2 cursor-pointer hover:text-fg" onclick={() => toggleSort('segment')}>Segment{sortIndicator('segment')}</th>
                  <th class="px-4 py-2">Reason</th>
                  <th class="px-4 py-2 cursor-pointer hover:text-fg" onclick={() => toggleSort('logBotHits')}>Bot Hits{sortIndicator('logBotHits')}</th>
                  <th class="px-4 py-2 cursor-pointer hover:text-fg" onclick={() => toggleSort('logHits')}>Total Hits{sortIndicator('logHits')}</th>
                  <th class="px-4 py-2 cursor-pointer hover:text-fg" onclick={() => toggleSort('logBytes')}>Transfer{sortIndicator('logBytes')}</th>
                  <th class="px-4 py-2 cursor-pointer hover:text-fg" onclick={() => toggleSort('logStatus')}>Log Status{sortIndicator('logStatus')}</th>
                  <th class="px-4 py-2 cursor-pointer hover:text-fg" onclick={() => toggleSort('crawlStatus')}>Crawl Status{sortIndicator('crawlStatus')}</th>
                  <th class="px-4 py-2">Indexable</th>
                  <th class="px-4 py-2 cursor-pointer hover:text-fg" onclick={() => toggleSort('crawlDepth')}>Depth{sortIndicator('crawlDepth')}</th>
                  <th class="px-4 py-2 cursor-pointer hover:text-fg" onclick={() => toggleSort('crawlInlinks')}>Inlinks{sortIndicator('crawlInlinks')}</th>
                </tr>
                <!-- Per-column filter row -->
                <tr class="bg-surface-1 border-t border-border">
                  <th class="px-2 py-1">
                    <input type="text" placeholder="Contains…" bind:value={filterUrl} oninput={onColumnFilterChange}
                      class="w-full px-2 py-1 rounded text-xs bg-surface-2 border border-border text-fg focus:outline-none focus:ring-1 focus:ring-accent" />
                  </th>
                  <th class="px-2 py-1">
                    <select bind:value={mergedSegmentFilter} onchange={onColumnFilterChangeImmediate}
                      class="w-full px-2 py-1 rounded text-xs bg-surface-2 border border-border text-fg focus:outline-none focus:ring-1 focus:ring-accent">
                      <option value="">all</option>
                      {#each segEntries as [seg]}
                        <option value={seg}>{segmentLabels[seg] || seg}</option>
                      {/each}
                    </select>
                  </th>
                  <th class="px-2 py-1">
                    <input type="text" placeholder="Contains…" bind:value={filterReason} oninput={onColumnFilterChange}
                      class="w-full px-2 py-1 rounded text-xs bg-surface-2 border border-border text-fg focus:outline-none focus:ring-1 focus:ring-accent" />
                  </th>
                  <th class="px-2 py-1"></th>
                  <th class="px-2 py-1"></th>
                  <th class="px-2 py-1"></th>
                  <th class="px-2 py-1">
                    <input type="text" placeholder="200, 4xx…" bind:value={filterLogStatus} oninput={onColumnFilterChange}
                      class="w-full px-2 py-1 rounded text-xs bg-surface-2 border border-border text-fg focus:outline-none focus:ring-1 focus:ring-accent" />
                  </th>
                  <th class="px-2 py-1">
                    <input type="text" placeholder="200, 4xx…" bind:value={filterCrawlStatus} oninput={onColumnFilterChange}
                      class="w-full px-2 py-1 rounded text-xs bg-surface-2 border border-border text-fg focus:outline-none focus:ring-1 focus:ring-accent" />
                  </th>
                  <th class="px-2 py-1">
                    <select bind:value={filterIndexable} onchange={onColumnFilterChangeImmediate}
                      class="w-full px-2 py-1 rounded text-xs bg-surface-2 border border-border text-fg focus:outline-none focus:ring-1 focus:ring-accent">
                      <option value="">any</option>
                      <option value="yes">yes</option>
                      <option value="no">no</option>
                    </select>
                  </th>
                  <th class="px-2 py-1"></th>
                  <th class="px-2 py-1">
                    <button class="w-full px-2 py-1 rounded text-[10px] bg-surface-2 border border-border text-fg-2 hover:text-fg cursor-pointer"
                      onclick={clearAllFilters} title="Clear all filters">Reset</button>
                  </th>
                </tr>
              </thead>
              <tbody>
                {#if mergedLoading && mergedRows.length === 0}
                  <tr><td colspan="11" class="px-4 py-6 text-center text-fg-2 text-xs">Loading…</td></tr>
                {:else if mergedRows.length === 0}
                  <tr><td colspan="11" class="px-4 py-6 text-center text-fg-2 text-xs">No pages match the current filters.</td></tr>
                {:else}
                  {#each mergedRows as page}
                    <tr class="border-t border-border hover:bg-surface-2/50 transition-colors">
                      <td class="px-4 py-2 text-fg text-xs max-w-xs truncate" title={page.url}>{page.url}</td>
                      <td class="px-4 py-2">
                        <span class="px-2 py-0.5 rounded text-[10px] font-medium whitespace-nowrap"
                              style="background-color: {segmentColors[page.segment] || '#6b7280'}20; color: {segmentColors[page.segment] || '#6b7280'}">
                          {segmentLabels[page.segment] || page.segment}
                        </span>
                      </td>
                      <td class="px-4 py-2 text-fg-2 text-[11px] max-w-sm" title={page.reason || ''}>{page.reason || '—'}</td>
                      <td class="px-4 py-2 text-fg">{(page.logBotHits || 0).toLocaleString()}</td>
                      <td class="px-4 py-2 text-fg-2">{(page.logHits || 0).toLocaleString()}</td>
                      <td class="px-4 py-2 text-fg-2">{page.logBytes ? formatBytes(page.logBytes) : '—'}</td>
                      <td class="px-4 py-2 text-fg-2">{page.logStatus || '—'}</td>
                      <td class="px-4 py-2 text-fg-2">{page.crawlStatus || '—'}</td>
                      <td class="px-4 py-2 text-fg-2">{page.inCrawl ? (page.crawlIndexable ? 'Yes' : 'No') : '—'}</td>
                      <td class="px-4 py-2 text-fg-2">{page.crawlDepth ?? '—'}</td>
                      <td class="px-4 py-2 text-fg-2">{page.crawlInlinks ?? '—'}</td>
                    </tr>
                  {/each}
                {/if}
              </tbody>
            </table>
          </div>

          <!-- Pagination -->
          <div class="bg-surface-2 px-4 py-2 flex items-center justify-between text-xs text-fg-2">
            <div>
              Page {mergedPage.toLocaleString()} of {mergedPagesTotal.toLocaleString()} · showing {mergedRows.length} of {mergedTotal.toLocaleString()} {mergedSegmentFilter || mergedSearch ? 'filtered' : ''} rows
            </div>
            <div class="flex items-center gap-1">
              <button class="px-2 py-1 rounded bg-surface-1 border border-border hover:text-fg cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                onclick={() => gotoPage(1)} disabled={mergedPage === 1 || mergedLoading}>« First</button>
              <button class="px-2 py-1 rounded bg-surface-1 border border-border hover:text-fg cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                onclick={() => gotoPage(mergedPage - 1)} disabled={mergedPage === 1 || mergedLoading}>‹ Prev</button>
              <input
                type="number" min="1" max={mergedPagesTotal} bind:value={mergedPage}
                onchange={() => gotoPage(mergedPage)}
                class="w-16 px-2 py-1 rounded bg-surface-1 border border-border text-center text-fg focus:outline-none focus:ring-1 focus:ring-accent"
              />
              <button class="px-2 py-1 rounded bg-surface-1 border border-border hover:text-fg cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                onclick={() => gotoPage(mergedPage + 1)} disabled={mergedPage >= mergedPagesTotal || mergedLoading}>Next ›</button>
              <button class="px-2 py-1 rounded bg-surface-1 border border-border hover:text-fg cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                onclick={() => gotoPage(mergedPagesTotal)} disabled={mergedPage >= mergedPagesTotal || mergedLoading}>Last »</button>
            </div>
          </div>
        </div>
      {/if}
    {/if}

    {#if tab === 'waste'}
      {#if wasteData && Object.keys(wasteData.byType).length > 0}
        {@const wasteEntries = Object.values(wasteData.byType).sort((a, b) => b.botHits - a.botHits)}
        {@const totalWaste = wasteEntries.reduce((s, e) => s + e.botHits, 0)}

        <!-- Waste summary cards -->
        <div class="grid grid-cols-3 gap-4 mb-4">
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">Waste Ratio</div>
            <div class="text-2xl font-bold {wasteData.wasteRatio > 0.15 ? 'text-red-400' : wasteData.wasteRatio > 0.05 ? 'text-yellow-400' : 'text-green-400'}">
              {(wasteData.wasteRatio * 100).toFixed(1)}%
            </div>
            <div class="text-xs text-fg-2 mt-1">Target: &lt;15%</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">Wasted Bot Hits</div>
            <div class="text-2xl font-bold text-fg">{wasteData.wasteHits.toLocaleString()}</div>
            <div class="text-xs text-fg-2 mt-1">of {wasteData.totalBotHits.toLocaleString()} total</div>
          </div>
          <div class="rounded-xl bg-surface-1 border border-border p-4">
            <div class="text-xs text-fg-2 mb-1">Waste Categories</div>
            <div class="text-2xl font-bold text-fg">{wasteEntries.length}</div>
            <div class="text-xs text-fg-2 mt-1">types detected</div>
          </div>
        </div>

        <!-- Waste donut + breakdown -->
        <div class="grid grid-cols-2 gap-4 mb-4">
          <div class="rounded-xl bg-surface-1 border border-border p-5">
            <h3 class="text-sm font-medium text-fg mb-3">Waste Breakdown</h3>
            <div class="flex items-center gap-6">
              <svg viewBox="0 0 120 120" class="w-32 h-32">
                {#if totalWaste > 0}
                  {@const entries = wasteEntries}
                  {#each entries as entry, i}
                    {@const startAngle = -Math.PI / 2 + entries.slice(0, i).reduce((s, e) => s + (e.botHits / totalWaste) * Math.PI * 2, 0)}
                    {@const angle = (entry.botHits / totalWaste) * Math.PI * 2}
                    <path d={donutPath(startAngle, startAngle + Math.min(angle, Math.PI * 1.999), 45, 60, 60)} fill="none" stroke={wasteTypeColors[entry.type] || '#6b7280'} stroke-width="14" />
                  {/each}
                {:else}
                  <circle cx="60" cy="60" r="45" fill="none" stroke="#94a3b8" stroke-width="14" />
                {/if}
              </svg>
              <div class="flex flex-col gap-2">
                {#each wasteEntries as entry}
                  <div class="flex items-center gap-2">
                    <span class="inline-block w-3 h-3 rounded" style="background-color: {wasteTypeColors[entry.type] || '#6b7280'}"></span>
                    <span class="text-xs text-fg">{wasteTypeLabels[entry.type] || entry.type}: {entry.botHits.toLocaleString()}</span>
                  </div>
                {/each}
              </div>
            </div>
          </div>

          <!-- Efficiency funnel -->
          <div class="rounded-xl bg-surface-1 border border-border p-5">
            <h3 class="text-sm font-medium text-fg mb-3">Crawl Efficiency Funnel</h3>
            {#if true}
            {@const totalBot = wasteData.totalBotHits}
            {@const nonWaste = totalBot - wasteData.wasteHits}
            {@const uniqueURLs = (crawlBudget.uniqueUrlsCrawled as number) || 0}
            <div class="space-y-2">
              {#each [
                { label: 'Total Bot Hits', value: totalBot, width: 100, color: 'bg-fg-2/30' },
                { label: 'Non-Waste Hits', value: nonWaste, width: totalBot > 0 ? (nonWaste / totalBot) * 100 : 0, color: 'bg-blue-400/50' },
                { label: 'Unique URLs Crawled', value: uniqueURLs, width: totalBot > 0 ? Math.min((uniqueURLs / totalBot) * 100, 100) : 0, color: 'bg-green-400/50' },
              ] as step}
                <div>
                  <div class="flex justify-between text-xs mb-1">
                    <span class="text-fg-2">{step.label}</span>
                    <span class="text-fg font-medium">{step.value.toLocaleString()}</span>
                  </div>
                  <div class="h-6 rounded {step.color}" style="width: {Math.max(step.width, 2)}%"></div>
                </div>
              {/each}
            </div>
            {/if}
          </div>
        </div>

        <!-- Waste details table -->
        <div class="rounded-xl border border-border overflow-hidden">
          <table class="w-full text-sm">
            <thead>
              <tr class="bg-surface-2 text-fg-2 text-left text-xs uppercase tracking-wider">
                <th class="px-4 py-3">Waste Type</th>
                <th class="px-4 py-3">Bot Hits</th>
                <th class="px-4 py-3">Total Hits</th>
                <th class="px-4 py-3">URLs</th>
                <th class="px-4 py-3">% of Waste</th>
                <th class="px-4 py-3">Top URLs</th>
              </tr>
            </thead>
            <tbody>
              {#each wasteEntries as entry}
                <tr class="border-t border-border hover:bg-surface-2/50 transition-colors">
                  <td class="px-4 py-3">
                    <div class="flex items-center gap-2">
                      <span class="inline-block w-2 h-2 rounded" style="background-color: {wasteTypeColors[entry.type] || '#6b7280'}"></span>
                      <span class="font-medium text-fg">{wasteTypeLabels[entry.type] || entry.type}</span>
                    </div>
                  </td>
                  <td class="px-4 py-3 text-fg">{entry.botHits.toLocaleString()}</td>
                  <td class="px-4 py-3 text-fg-2">{entry.hits.toLocaleString()}</td>
                  <td class="px-4 py-3 text-fg-2">{entry.urls}</td>
                  <td class="px-4 py-3 text-fg-2">{totalWaste > 0 ? ((entry.botHits / totalWaste) * 100).toFixed(1) + '%' : '-'}</td>
                  <td class="px-4 py-3 text-fg-2 text-xs max-w-xs truncate">{entry.topUrls?.slice(0, 3).join(', ') || '-'}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <div class="text-center text-fg-2 text-sm py-12">No crawl waste detected in the analyzed URLs.</div>
      {/if}
    {/if}
  </div>
{/if}
