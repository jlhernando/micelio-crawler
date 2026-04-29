<script lang="ts">
  import { onMount } from 'svelte';
  import { createWsClient, type WsMessage } from '../lib/ws';
  import { api } from '../lib/api';
  import { navigate } from '../lib/router';

  let { id }: { id: string } = $props();

  // ── Crawl state ──
  let status = $state<'running' | 'paused' | 'stopping' | 'completed' | 'failed' | 'cancelled'>('running');
  let completed = $state(0);
  let failed = $state(0);
  let pending = $state(0);
  let excluded = $state(0);
  let excludedStats = $state<{ pattern: string; count: number }[]>([]);
  let total = $state(0);
  let elapsedMs = $state(0);
  let startedAt = $state(Date.now());
  let phase = $state<string | null>(null);
  let phaseLoaded = $state(0);
  let phaseTotal = $state(0);

  // Per-page tracking — batch updates to reduce GC pressure
  let recentPages = $state<{ url: string; statusCode: number; responseTimeMs: number; error?: string }[]>([]);
  let errors = $state<{ url: string; error: string }[]>([]);

  // Status code distribution — mutated in place, batch-flushed
  let statusCodes = $state<Record<string, number>>({});

  // Response times — circular buffer
  const RT_BUFFER_SIZE = 200;
  let rtBuffer: number[] = new Array(RT_BUFFER_SIZE).fill(0);
  let rtHead = 0;
  let rtCount = 0;
  let responseTimes = $state<number[]>([]);

  // Batching: accumulate incoming messages, flush on rAF
  let pendingPages: typeof recentPages = [];
  let pendingErrors: typeof errors = [];
  let pendingStatusCodes: Record<string, number> = {};
  let pendingRTs: number[] = [];
  let flushScheduled = false;
  let rafId: number | null = null;

  function scheduleFlush() {
    if (flushScheduled) return;
    flushScheduled = true;
    rafId = requestAnimationFrame(flushBatch);
  }

  function flushBatch() {
    flushScheduled = false;

    // Flush recent pages (prepend batch, keep 50)
    if (pendingPages.length > 0) {
      recentPages = [...pendingPages.reverse(), ...recentPages].slice(0, 50);
      pendingPages = [];
    }

    // Flush errors (append, cap 200)
    if (pendingErrors.length > 0) {
      const remaining = 200 - errors.length;
      if (remaining > 0) {
        errors = [...errors, ...pendingErrors.slice(0, remaining)];
      }
      pendingErrors = [];
    }

    // Flush status codes
    if (Object.keys(pendingStatusCodes).length > 0) {
      const next = { ...statusCodes };
      for (const [g, c] of Object.entries(pendingStatusCodes)) {
        next[g] = (next[g] || 0) + c;
      }
      statusCodes = next;
      pendingStatusCodes = {};
    }

    // Flush response times from circular buffer
    if (pendingRTs.length > 0) {
      for (const rt of pendingRTs) {
        rtBuffer[rtHead] = rt;
        rtHead = (rtHead + 1) % RT_BUFFER_SIZE;
        if (rtCount < RT_BUFFER_SIZE) rtCount++;
      }
      pendingRTs = [];
      // Reconstruct ordered array from circular buffer
      const arr = new Array(rtCount);
      const start = rtCount < RT_BUFFER_SIZE ? 0 : rtHead;
      for (let i = 0; i < rtCount; i++) {
        arr[i] = rtBuffer[(start + i) % RT_BUFFER_SIZE];
      }
      responseTimes = arr;
    }
  }

  // Completion data
  let completionData = $state<{ totalPages: number; durationMs: number } | null>(null);
  let autoRedirectTimer: ReturnType<typeof setTimeout> | null = null;

  // ── Derived ──
  let progressPct = $derived(total > 0 ? Math.round(((completed + failed) / total) * 100) : 0);
  let pagesPerSecond = $derived(elapsedMs > 1000 ? ((completed + failed) / (elapsedMs / 1000)).toFixed(1) : '—');
  let estimatedRemaining = $derived.by(() => {
    if (elapsedMs < 2000 || completed + failed === 0 || pending === 0) return '—';
    const rate = (completed + failed) / (elapsedMs / 1000);
    const remainingSec = pending / rate;
    if (remainingSec < 60) return `${Math.round(remainingSec)}s`;
    const m = Math.floor(remainingSec / 60);
    if (m < 60) return `${m}m ${Math.round(remainingSec % 60)}s`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ${m % 60}m`;
    const d = Math.floor(h / 24);
    return `${d}d ${h % 24}h ${m % 60}m`;
  });

  function formatDuration(ms: number): string {
    const s = Math.floor(ms / 1000);
    if (s < 60) return `${s}s`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ${s % 60}s`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ${m % 60}m`;
    const d = Math.floor(h / 24);
    return `${d}d ${h % 24}h ${m % 60}m`;
  }

  function statusColor(code: number): string {
    if (code >= 200 && code < 300) return 'bg-green-500/20 text-green-400';
    if (code >= 300 && code < 400) return 'bg-yellow-500/20 text-yellow-400';
    if (code >= 400 && code < 500) return 'bg-orange-500/20 text-orange-400';
    if (code >= 500) return 'bg-red-500/20 text-red-400';
    return 'bg-gray-500/20 text-gray-400';
  }

  function statusGroupColor(group: string): string {
    if (group === '2xx') return '#22c55e';
    if (group === '3xx') return '#eab308';
    if (group === '4xx') return '#f97316';
    if (group === '5xx') return '#ef4444';
    return '#6b7280';
  }

  // ── WebSocket handler ──
  function handleMessage(msg: WsMessage) {
    // Filter messages to only this crawl
    if (msg.crawlId && msg.crawlId !== id) return;

    if (msg.type === 'progress') {
      // Check for phase progress (resume loading, analysis loading)
      if (msg.data.phase) {
        phase = msg.data.phase as string;
        phaseLoaded = (msg.data.loaded as number) || 0;
        phaseTotal = (msg.data.total as number) || 0;
        return;
      }
      // Normal crawl progress — clear any phase indicator
      phase = null;
      completed = (msg.data.completed as number) || 0;
      failed = (msg.data.failed as number) || 0;
      pending = (msg.data.pending as number) || 0;
      excluded = (msg.data.excluded as number) || 0;
      excludedStats = (msg.data.excludedStats as typeof excludedStats) || [];
      total = (msg.data.total as number) || 0;
      elapsedMs = (msg.data.elapsedMs as number) || (Date.now() - startedAt);
    } else if (msg.type === 'page' || msg.type === 'error') {
      const url = msg.data.url as string;
      const statusCode = (msg.data.statusCode as number) || 0;
      const responseTimeMs = (msg.data.responseTimeMs as number) || 0;
      const error = msg.data.error as string | undefined;

      // Accumulate into batch buffers (flushed on rAF)
      pendingPages.push({ url, statusCode, responseTimeMs, error });

      const group = statusCode >= 500 ? '5xx' : statusCode >= 400 ? '4xx' : statusCode >= 300 ? '3xx' : statusCode >= 200 ? '2xx' : 'err';
      pendingStatusCodes[group] = (pendingStatusCodes[group] || 0) + 1;

      pendingRTs.push(responseTimeMs);

      if (error) {
        pendingErrors.push({ url, error });
      }

      scheduleFlush();
    } else if (msg.type === 'complete') {
      status = 'completed';
      completionData = {
        totalPages: (msg.data.totalPages as number) || completed,
        durationMs: (msg.data.durationMs as number) || elapsedMs,
      };
      window.dispatchEvent(new Event('crawl-state-change'));
      // Auto-navigate to results after 3 seconds
      autoRedirectTimer = setTimeout(() => {
        if (status === 'completed') navigate(`/results/${id}`);
      }, 3000);
    } else if (msg.type === 'paused') {
      status = 'paused';
      window.dispatchEvent(new Event('crawl-state-change'));
    } else if (msg.type === 'resumed') {
      status = 'running';
      window.dispatchEvent(new Event('crawl-state-change'));
    } else if (msg.type === 'stopping') {
      status = 'stopping';
    } else if (msg.type === 'cancelled') {
      status = 'cancelled';
      window.dispatchEvent(new Event('crawl-state-change'));
    }
  }

  // ── Controls ──
  async function pauseCrawl() {
    await api.pauseCrawl(id);
  }

  async function resumeCrawl() {
    await api.resumeCrawl(id);
  }

  async function stopCrawl() {
    status = 'stopping';
    try {
      await api.stopCrawl(id);
      window.dispatchEvent(new Event('crawl-state-change'));
    } catch { status = 'running'; }
  }

  async function cancelCrawl() {
    try {
      await api.cancelCrawl(id);
      status = 'cancelled';
      window.dispatchEvent(new Event('crawl-state-change'));
    } catch { /* keep current status on failure */ }
  }

  // ── Lifecycle ──
  let wsClient: { close: () => void } | null = null;
  let elapsedTimer: ReturnType<typeof setInterval> | null = null;

  function resetState() {
    status = 'running';
    completed = 0;
    failed = 0;
    pending = 0;
    excluded = 0;
    excludedStats = [];
    total = 0;
    elapsedMs = 0;
    startedAt = Date.now();
    recentPages = [];
    errors = [];
    statusCodes = {};
    rtBuffer = new Array(RT_BUFFER_SIZE).fill(0);
    rtHead = 0;
    rtCount = 0;
    responseTimes = [];
    pendingPages = [];
    pendingErrors = [];
    pendingStatusCodes = {};
    pendingRTs = [];
    flushScheduled = false;
    completionData = null;
    if (autoRedirectTimer) { clearTimeout(autoRedirectTimer); autoRedirectTimer = null; }
    if (rafId !== null) { cancelAnimationFrame(rafId); rafId = null; }
  }

  function hydrateFromJob(job: Record<string, unknown>) {
    if (job.status === 'completed') {
      status = 'completed';
      const pc = (job.pageCount as number) || 0;
      const ec = (job.errorCount as number) || 0;
      const dur = (job.durationMs as number) || 0;
      completed = pc - ec;
      failed = ec;
      total = pc;
      pending = 0;
      elapsedMs = dur;
      completionData = { totalPages: pc, durationMs: dur };
    } else if (job.status === 'failed' || job.status === 'cancelled') {
      status = job.status as typeof status;
      const pc = (job.pageCount as number) || 0;
      const ec = (job.errorCount as number) || 0;
      completed = pc - ec;
      failed = ec;
      total = pc;
    } else {
      status = (job.status as typeof status) || 'running';
      const pc = (job.pageCount as number) || 0;
      const ec = (job.errorCount as number) || 0;
      completed = pc - ec;
      failed = ec;
    }
    startedAt = new Date(job.startedAt as string).getTime();
    if (status !== 'completed') {
      elapsedMs = Date.now() - startedAt;
    }
    // Restore status code counts from active crawl data
    if (job.statusCodes && typeof job.statusCodes === 'object') {
      statusCodes = { ...(job.statusCodes as Record<string, number>) };
    }
  }

  // Re-initialize when crawl ID changes (handles switching between active crawls)
  $effect(() => {
    const currentId = id; // track reactivity on id prop
    resetState();

    api.getCrawlStatus(currentId).then((job) => {
      if (id !== currentId) return; // stale response from a previous crawl switch
      hydrateFromJob(job);
    }).catch(() => {});

    if (!wsClient) {
      wsClient = createWsClient(handleMessage);
    }

    if (elapsedTimer) clearInterval(elapsedTimer);
    elapsedTimer = setInterval(() => {
      if (status === 'running') {
        elapsedMs = Date.now() - startedAt;
      }
    }, 1000);

    return () => {
      if (elapsedTimer) clearInterval(elapsedTimer);
    };
  });

  onMount(() => {
    return () => {
      wsClient?.close();
      if (autoRedirectTimer) clearTimeout(autoRedirectTimer);
      if (rafId !== null) cancelAnimationFrame(rafId);
    };
  });

  // ── Status code chart bars ──
  let statusChartData = $derived.by(() => {
    const groups = ['2xx', '3xx', '4xx', '5xx', 'err'];
    const maxCount = Math.max(...groups.map(g => statusCodes[g] || 0), 1);
    return groups
      .filter(g => statusCodes[g])
      .map(g => ({
        group: g,
        count: statusCodes[g] || 0,
        pct: ((statusCodes[g] || 0) / maxCount) * 100,
        color: statusGroupColor(g),
      }));
  });

  // ── Response time chart ──
  let rtChartPath = $derived.by(() => {
    if (responseTimes.length < 2) return '';
    const maxRt = Math.max(...responseTimes, 100);
    const width = 100;
    const height = 100;
    return responseTimes.map((rt, i) => {
      const x = (i / (responseTimes.length - 1)) * width;
      const y = height - (rt / maxRt) * height;
      return `${i === 0 ? 'M' : 'L'} ${x.toFixed(1)} ${y.toFixed(1)}`;
    }).join(' ');
  });

  let rtMax = $derived(responseTimes.length > 0 ? Math.max(...responseTimes) : 0);
  let rtAvg = $derived(responseTimes.length > 0 ? Math.round(responseTimes.reduce((a, b) => a + b, 0) / responseTimes.length) : 0);
</script>

<div class="max-w-7xl mx-auto space-y-4">
  <!-- Completion Banner -->
  {#if status === 'completed' && completionData}
    <div class="rounded-xl border border-green-500/30 bg-green-500/10 p-6 text-center">
      <h2 class="text-xl font-semibold text-green-400 mb-2">Crawl Complete</h2>
      <p class="text-fg-2">
        {completionData.totalPages.toLocaleString()} pages crawled in {formatDuration(completionData.durationMs)}
      </p>
      <button
        type="button"
        class="mt-3 px-6 py-2 rounded-lg bg-accent text-white font-medium hover:bg-accent/90 transition-colors cursor-pointer"
        onclick={() => navigate(`/results/${id}`)}
      >
        View Results
      </button>
    </div>
  {/if}

  {#if status === 'cancelled'}
    <div class="rounded-xl border border-orange-500/30 bg-orange-500/10 p-6 text-center">
      <h2 class="text-xl font-semibold text-orange-400 mb-2">Crawl Cancelled</h2>
      <p class="text-fg-2">{(completed + failed).toLocaleString()} pages crawled before cancellation</p>
    </div>
  {/if}

  {#if status === 'failed'}
    <div class="rounded-xl border border-red-500/30 bg-red-500/10 p-6 text-center">
      <h2 class="text-xl font-semibold text-red-400 mb-2">Crawl Failed</h2>
      <p class="text-fg-2">The crawl encountered a fatal error.</p>
    </div>
  {/if}

  <!-- Progress Bar -->
  <div class="rounded-xl border border-border bg-surface-2 p-5">
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-3">
        <span class="text-2xl font-bold">{progressPct}%</span>
        {#if status === 'running'}
          <span class="flex items-center gap-1.5 text-xs text-green-400">
            <span class="w-2 h-2 bg-green-400 rounded-full animate-pulse"></span>
            Running
          </span>
        {:else if status === 'paused'}
          <span class="flex items-center gap-1.5 text-xs text-yellow-400">
            <span class="w-2 h-2 bg-yellow-400 rounded-full"></span>
            Paused
          </span>
        {:else if status === 'stopping'}
          <span class="flex items-center gap-1.5 text-xs text-orange-400">
            <span class="w-2 h-2 bg-orange-400 rounded-full animate-pulse"></span>
            Stopping — analyzing...
          </span>
        {/if}
        {#if phase}
          <span class="ml-3 text-xs text-fg-2 flex items-center gap-1.5">
            <span class="w-3 h-3 border-2 border-accent border-t-transparent rounded-full animate-spin"></span>
            {#if phase === 'resume_loading'}
              Resuming: loading {phaseLoaded.toLocaleString()} / {phaseTotal.toLocaleString()} pages...
            {:else if phase === 'analysis_loading'}
              Loading {phaseLoaded.toLocaleString()} / {phaseTotal.toLocaleString()} pages for analysis...
            {:else}
              {phase}...
            {/if}
          </span>
        {/if}
      </div>
      <!-- Controls -->
      {#if status === 'running' || status === 'paused'}
        <div class="flex gap-2">
          {#if status === 'running'}
            <button
              type="button"
              class="px-4 py-1.5 rounded-lg text-sm font-medium bg-yellow-500/20 text-yellow-400 hover:bg-yellow-500/30 transition-colors cursor-pointer"
              onclick={pauseCrawl}
            >Pause</button>
          {:else}
            <button
              type="button"
              class="px-4 py-1.5 rounded-lg text-sm font-medium bg-green-500/20 text-green-400 hover:bg-green-500/30 transition-colors cursor-pointer"
              onclick={resumeCrawl}
            >Resume</button>
          {/if}
          {#if completed + failed > 0}
            <button
              type="button"
              class="px-4 py-1.5 rounded-lg text-sm font-medium bg-accent/20 text-accent hover:bg-accent/30 transition-colors cursor-pointer"
              onclick={stopCrawl}
              title="Stop crawling and run full analysis on pages crawled so far"
            >Stop & Analyze</button>
          {/if}
          <button
            type="button"
            class="px-4 py-1.5 rounded-lg text-sm font-medium bg-red-500/20 text-red-400 hover:bg-red-500/30 transition-colors cursor-pointer"
            onclick={cancelCrawl}
          >Cancel</button>
        </div>
      {/if}
    </div>

    <!-- Bar -->
    <div class="h-3 rounded-full bg-surface-3 overflow-hidden">
      {#if phase && phaseTotal > 0}
        <div
          class="h-full rounded-full bg-blue-500/60 transition-all duration-300"
          style="width: {Math.round((phaseLoaded / phaseTotal) * 100)}%"
        ></div>
      {:else}
        <div
          class="h-full rounded-full bg-accent transition-all duration-300"
          style="width: {progressPct}%"
        ></div>
      {/if}
    </div>

    <!-- Stats Row -->
    <div class="grid grid-cols-2 sm:grid-cols-6 gap-4 mt-4">
      <div>
        <div class="text-xs text-fg-2">Pages</div>
        <div class="text-lg font-semibold">{(completed + failed).toLocaleString()}</div>
      </div>
      <div>
        <div class="text-xs text-fg-2">Success</div>
        <div class="text-lg font-semibold text-green-400">{completed.toLocaleString()}</div>
      </div>
      <div>
        <div class="text-xs text-fg-2">Errors</div>
        <div class="text-lg font-semibold text-red-400">{failed.toLocaleString()}</div>
      </div>
      <div>
        <div class="text-xs text-fg-2">Pending</div>
        <div class="text-lg font-semibold text-fg-2">{pending.toLocaleString()}</div>
      </div>
      {#if excluded > 0}
      <div>
        <div class="text-xs text-fg-2">Excluded</div>
        <div class="text-lg font-semibold text-fg-2">{excluded.toLocaleString()}</div>
        {#if excludedStats.length > 0}
          <div class="mt-1 space-y-0.5">
            {#each excludedStats as stat}
              <div class="text-[10px] text-fg-2 font-mono truncate" title={stat.pattern}>
                {stat.pattern} <span class="text-fg">{stat.count.toLocaleString()}</span>
              </div>
            {/each}
          </div>
        {/if}
      </div>
      {/if}
      <div>
        <div class="text-xs text-fg-2">Speed</div>
        <div class="text-lg font-semibold">{pagesPerSecond} <span class="text-xs text-fg-2">p/s</span></div>
      </div>
      <div>
        <div class="text-xs text-fg-2">Elapsed</div>
        <div class="text-lg font-semibold">{formatDuration(elapsedMs)}</div>
        {#if estimatedRemaining !== '—' && status === 'running'}
          <div class="text-xs text-fg-2">ETA: {estimatedRemaining}</div>
        {/if}
      </div>
    </div>
  </div>

  <!-- Charts Row -->
  <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
    <!-- Status Code Distribution -->
    <div class="rounded-xl border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-medium text-fg-2 mb-3">Status Codes</h3>
      {#if statusChartData.length === 0}
        <p class="text-fg-2 text-sm">Waiting for data...</p>
      {:else}
        <div class="space-y-2">
          {#each statusChartData as item}
            <div class="flex items-center gap-3">
              <span class="w-8 text-xs font-mono text-right" style="color: {item.color}">{item.group}</span>
              <div class="flex-1 h-5 rounded bg-surface-3 overflow-hidden">
                <div
                  class="h-full rounded transition-all duration-300"
                  style="width: {item.pct}%; background: {item.color}"
                ></div>
              </div>
              <span class="w-10 text-xs text-right font-mono">{item.count.toLocaleString()}</span>
            </div>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Response Time Chart -->
    <div class="rounded-xl border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-medium text-fg-2 mb-3">
        Response Times
        {#if rtAvg > 0}
          <span class="text-xs font-normal ml-2">avg {rtAvg}ms / max {rtMax}ms</span>
        {/if}
      </h3>
      {#if responseTimes.length < 2}
        <p class="text-fg-2 text-sm">Waiting for data...</p>
      {:else}
        <div class="h-32">
          <svg viewBox="0 0 100 100" preserveAspectRatio="none" class="w-full h-full">
            <!-- Threshold lines -->
            {#if rtMax > 1000}
              <line x1="0" y1={100 - (1000 / Math.max(rtMax, 100)) * 100} x2="100" y2={100 - (1000 / Math.max(rtMax, 100)) * 100} stroke="#eab308" stroke-width="0.3" stroke-dasharray="2,2" />
            {/if}
            {#if rtMax > 3000}
              <line x1="0" y1={100 - (3000 / Math.max(rtMax, 100)) * 100} x2="100" y2={100 - (3000 / Math.max(rtMax, 100)) * 100} stroke="#ef4444" stroke-width="0.3" stroke-dasharray="2,2" />
            {/if}
            <!-- Line chart -->
            <path d={rtChartPath} fill="none" stroke="#6366f1" stroke-width="0.8" vector-effect="non-scaling-stroke" />
          </svg>
        </div>
      {/if}
    </div>
  </div>

  <!-- Live Activity -->
  <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
    <!-- Recent Pages -->
    <div class="rounded-xl border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-medium text-fg-2 mb-3">Recent Pages</h3>
      <div class="space-y-1 max-h-72 overflow-y-auto">
        {#if recentPages.length === 0}
          <p class="text-fg-2 text-sm">Waiting for pages...</p>
        {:else}
          {#each recentPages as page}
            <div class="flex items-center gap-2 py-1 px-2 rounded hover:bg-surface-3 text-xs">
              <span class="px-1.5 py-0.5 rounded font-mono {statusColor(page.statusCode)}">{page.statusCode}</span>
              <span class="flex-1 truncate font-mono text-fg-2">{page.url}</span>
              <span class="text-fg-2 shrink-0">{page.responseTimeMs}ms</span>
            </div>
          {/each}
        {/if}
      </div>
    </div>

    <!-- Error Log -->
    <div class="rounded-xl border border-border bg-surface-2 p-5">
      <h3 class="text-sm font-medium text-fg-2 mb-3">
        Errors
        {#if errors.length > 0}
          <span class="ml-1 px-1.5 py-0.5 rounded-full bg-red-500/20 text-red-400 text-xs">{errors.length}</span>
        {/if}
      </h3>
      <div class="space-y-1 max-h-72 overflow-y-auto">
        {#if errors.length === 0}
          <p class="text-fg-2 text-sm">No errors yet.</p>
        {:else}
          {#each errors as err}
            <div class="py-1.5 px-2 rounded bg-red-500/5 text-xs">
              <div class="font-mono text-red-400 truncate">{err.url}</div>
              <div class="text-fg-2 mt-0.5">{err.error}</div>
            </div>
          {/each}
        {/if}
      </div>
    </div>
  </div>
</div>
