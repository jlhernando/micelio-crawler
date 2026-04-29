<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { navigate } from '../lib/router';
  import SignalsView from '../lib/components/results/SignalsView.svelte';

  interface CrawlEvent {
    type: string;
    at: string;
    note?: string;
  }

  interface CrawlJob {
    id: string;
    seedUrl: string;
    mode: string;
    status: string;
    startedAt: string;
    completedAt: string | null;
    pageCount: number;
    errorCount: number;
    durationMs: number | null;
    events?: CrawlEvent[];
    stats?: Record<string, unknown>;
  }

  let loading = $state(true);
  let crawls = $state<CrawlJob[]>([]);
  let error = $state<string | null>(null);
  let expanded = $state<Set<string>>(new Set());
  let statsCache = $state<Record<string, Record<string, unknown>>>({});
  let statsLoading = $state<Set<string>>(new Set());

  // Selection for diff comparison
  let selected = $state<Set<string>>(new Set());
  let deleting = $state<string | null>(null);
  let statusFilter = $state<string>('all');

  // Status counts
  let statusCounts = $derived.by(() => {
    const counts: Record<string, number> = { all: crawls.length, running: 0, completed: 0, failed: 0, cancelled: 0, paused: 0 };
    for (const c of crawls) {
      counts[c.status] = (counts[c.status] || 0) + 1;
    }
    // Merge paused into running for display
    counts.running = (counts.running || 0) + (counts.paused || 0);
    return counts;
  });

  // Filtered and sorted crawls: running/paused always first in "all" view
  let filteredCrawls = $derived.by(() => {
    let list = crawls;
    if (statusFilter === 'running') {
      list = crawls.filter(c => c.status === 'running' || c.status === 'paused');
    } else if (statusFilter !== 'all') {
      list = crawls.filter(c => c.status === statusFilter);
    } else {
      // "all" view: sort running/paused to top
      list = [...crawls].sort((a, b) => {
        const aActive = a.status === 'running' || a.status === 'paused' ? 0 : 1;
        const bActive = b.status === 'running' || b.status === 'paused' ? 0 : 1;
        return aActive - bActive;
      });
    }
    return list;
  });

  onMount(() => {
    loadCrawls();
  });

  async function loadCrawls() {
    try {
      const data = await api.listCrawls();
      crawls = (data.crawls as CrawlJob[]) || [];
      loading = false;
    } catch (err) {
      error = (err as Error).message;
      loading = false;
    }
  }

  function toggleExpand(id: string) {
    const next = new Set(expanded);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
      // Lazy-load stats on expand if not cached
      if (!statsCache[id] && !statsLoading.has(id)) {
        statsLoading = new Set([...statsLoading, id]);
        api.getCrawlStats(id).then((data) => {
          statsCache = { ...statsCache, [id]: data.stats };
          statsLoading = new Set([...statsLoading].filter(x => x !== id));
        }).catch(() => {
          statsLoading = new Set([...statsLoading].filter(x => x !== id));
        });
      }
    }
    expanded = next;
  }

  // Get the domain of the first selected crawl (for same-domain enforcement)
  let selectedDomain = $derived.by(() => {
    if (selected.size === 0) return null;
    const firstId = [...selected][0];
    const crawl = crawls.find(c => c.id === firstId);
    return crawl ? extractDomain(crawl.seedUrl) : null;
  });

  function canSelect(crawl: CrawlJob): boolean {
    if (crawl.status !== 'completed') return false;
    if (selected.has(crawl.id)) return true; // can always deselect
    if (selectedDomain && extractDomain(crawl.seedUrl) !== selectedDomain) return false;
    return true;
  }

  function toggleSelect(id: string) {
    const next = new Set(selected);
    if (next.has(id)) {
      next.delete(id);
    } else {
      if (next.size >= 2) {
        const first = [...next][0];
        next.delete(first);
      }
      next.add(id);
    }
    selected = next;
  }

  function compareSelected() {
    const ids = [...selected];
    if (ids.length !== 2) return;
    const a = crawls.find(c => c.id === ids[0]);
    const b = crawls.find(c => c.id === ids[1]);
    if (!a || !b) return;
    const [oldId, newId] = new Date(a.startedAt) < new Date(b.startedAt)
      ? [a.id, b.id] : [b.id, a.id];
    navigate(`/diff/${oldId}/${newId}`);
  }

  function resumeCrawl(id: string) {
    navigate(`/setup?resumeId=${id}`);
  }

  async function deleteCrawl(id: string) {
    deleting = id;
    try {
      await api.deleteCrawl(id);
      crawls = crawls.filter(c => c.id !== id);
      selected.delete(id);
      selected = new Set(selected);
    } catch (err) {
      error = `Failed to delete crawl: ${(err as Error).message}`;
    } finally {
      deleting = null;
    }
  }

  function formatDate(iso: string): string {
    const d = new Date(iso);
    return d.toLocaleDateString('en-GB', { day: '2-digit', month: 'short', year: 'numeric' }) +
      ' ' + d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
  }

  function formatDuration(ms: number | null): string {
    if (ms == null) return '—';
    const s = Math.floor(ms / 1000);
    if (s < 60) return `${s}s`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ${s % 60}s`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ${m % 60}m`;
    const d = Math.floor(h / 24);
    return `${d}d ${h % 24}h ${m % 60}m`;
  }

  function statusBadge(status: string): { color: string; label: string } {
    switch (status) {
      case 'completed': return { color: 'bg-green-500/20 text-green-400', label: 'Completed' };
      case 'running': return { color: 'bg-blue-500/20 text-blue-400', label: 'Running' };
      case 'failed': return { color: 'bg-red-500/20 text-red-400', label: 'Failed' };
      case 'cancelled': return { color: 'bg-gray-500/20 text-gray-400', label: 'Cancelled' };
      case 'paused': return { color: 'bg-yellow-500/20 text-yellow-400', label: 'Paused' };
      default: return { color: 'bg-gray-500/20 text-gray-400', label: status };
    }
  }

  function extractDomain(url: string): string {
    try {
      return new URL(url).hostname;
    } catch {
      return url;
    }
  }

  const colCount = 8;
</script>

<div class="max-w-7xl mx-auto space-y-4">
  {#if loading}
    <div class="rounded-xl border border-border bg-surface-2 p-12 text-center">
      <div class="inline-block w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
      <p class="text-fg-2 mt-3">Loading crawl history...</p>
    </div>
  {:else if error}
    <div class="rounded-xl border border-red-500/30 bg-red-500/10 p-8 text-center">
      <h2 class="text-xl font-semibold text-red-400 mb-2">Error</h2>
      <p class="text-fg-2">{error}</p>
    </div>
  {:else if crawls.length === 0}
    <div class="rounded-xl border border-border bg-surface-2 p-12 text-center">
      <h2 class="text-xl font-semibold mb-2">No Crawls Yet</h2>
      <p class="text-fg-2 mb-4">Start your first crawl to see it here.</p>
      <a href="#/setup" class="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-white text-sm font-medium hover:bg-accent/80 transition-colors">
        + New Crawl
      </a>
    </div>
  {:else}
    <!-- Compare toolbar -->
    {#if selected.size === 2}
      <div class="rounded-xl border border-accent/50 bg-accent/10 p-4 flex items-center justify-between">
        <span class="text-sm">2 crawls selected for comparison</span>
        <div class="flex gap-2">
          <button
            type="button"
            class="px-4 py-2 rounded-lg text-sm font-medium bg-surface-3 hover:bg-surface-2 cursor-pointer transition-colors"
            onclick={() => { selected = new Set(); }}
          >Clear</button>
          <button
            type="button"
            class="px-4 py-2 rounded-lg text-sm font-medium bg-accent text-white hover:bg-accent/80 cursor-pointer transition-colors"
            onclick={compareSelected}
          >Compare</button>
        </div>
      </div>
    {:else if selected.size === 1}
      <div class="rounded-xl border border-border bg-surface-2 p-3 text-center text-sm text-fg-2">
        Select one more crawl to compare
      </div>
    {/if}

    <!-- Status filter tabs -->
    <div class="flex gap-1 flex-wrap">
      {#each [
        { key: 'all', label: 'All' },
        { key: 'running', label: 'Running' },
        { key: 'completed', label: 'Completed' },
        { key: 'failed', label: 'Failed' },
        { key: 'cancelled', label: 'Cancelled' },
      ] as tab}
        {@const count = statusCounts[tab.key] || 0}
        {#if tab.key === 'all' || count > 0}
          <button
            type="button"
            class="px-3 py-1.5 rounded-lg text-xs font-medium transition-colors cursor-pointer
              {statusFilter === tab.key
                ? tab.key === 'running' ? 'bg-blue-500/20 text-blue-400 ring-1 ring-blue-500/30'
                : 'bg-surface-3 text-fg ring-1 ring-border'
                : 'text-fg-2 hover:text-fg hover:bg-surface-3'}"
            onclick={() => statusFilter = tab.key}
          >
            {tab.label}
            <span class="ml-1 opacity-60">{count}</span>
          </button>
        {/if}
      {/each}
    </div>

    <!-- Crawl list -->
    <div class="rounded-xl border border-border bg-surface-2 overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="text-left text-xs text-fg-2 border-b border-border">
            <th class="px-4 py-3 w-10"></th>
            <th class="px-3 py-3">Site</th>
            <th class="px-3 py-3 w-24">Status</th>
            <th class="px-3 py-3 w-20 hidden sm:table-cell">Pages</th>
            <th class="px-3 py-3 w-20 hidden sm:table-cell">Errors</th>
            <th class="px-3 py-3 w-20 hidden md:table-cell">Duration</th>
            <th class="px-3 py-3 w-40 hidden lg:table-cell">Started</th>
            <th class="px-3 py-3 w-48">Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each filteredCrawls as crawl}
            {@const badge = statusBadge(crawl.status)}
            {@const hasStats = crawl.status === 'completed'}
            {@const isExpanded = expanded.has(crawl.id)}
            <tr
              class="border-b border-border/50 hover:bg-surface-3 transition-colors {selected.has(crawl.id) ? 'bg-accent/5' : ''} {hasStats ? 'cursor-pointer' : ''}"
              onclick={(e) => { if (hasStats && !(e.target as HTMLElement).closest('input, a, button')) toggleExpand(crawl.id); }}
            >
              <td class="px-4 py-3">
                <input
                  type="checkbox"
                  checked={selected.has(crawl.id)}
                  onchange={() => toggleSelect(crawl.id)}
                  class="w-4 h-4 rounded border-border bg-surface-3 accent-accent cursor-pointer"
                  disabled={!canSelect(crawl)}
                  title={crawl.status !== 'completed' ? 'Only completed crawls can be compared' : !canSelect(crawl) ? 'Different domain - select same domain crawls' : 'Select for comparison'}
                />
              </td>
              <td class="px-3 py-3">
                <div class="flex items-center gap-2">
                  {#if hasStats}
                    <span class="text-fg-2 text-xs transition-transform {isExpanded ? 'rotate-90' : ''}">&#9654;</span>
                  {/if}
                  <div>
                    <div class="font-medium">{extractDomain(crawl.seedUrl)}</div>
                    <div class="text-xs text-fg-2 font-mono truncate max-w-xs">{crawl.seedUrl}</div>
                  </div>
                </div>
              </td>
              <td class="px-3 py-3">
                <span class="px-2 py-0.5 rounded-full text-xs font-medium {badge.color}">{badge.label}</span>
              </td>
              <td class="px-3 py-3 hidden sm:table-cell font-mono">{crawl.pageCount.toLocaleString()}</td>
              <td class="px-3 py-3 hidden sm:table-cell font-mono {crawl.errorCount > 0 ? 'text-red-400' : ''}">{crawl.errorCount.toLocaleString()}</td>
              <td class="px-3 py-3 hidden md:table-cell text-fg-2">{formatDuration(crawl.durationMs)}</td>
              <td class="px-3 py-3 hidden lg:table-cell text-fg-2 text-xs">{formatDate(crawl.startedAt)}</td>
              <td class="px-3 py-3">
                <div class="flex gap-1">
                  {#if crawl.status === 'completed'}
                    <a
                      href="#/results/{crawl.id}"
                      class="px-3 py-1 rounded text-xs font-medium bg-accent/20 text-accent hover:bg-accent/30 transition-colors"
                    >Results</a>
                  {:else if crawl.status === 'running' || crawl.status === 'paused'}
                    <a
                      href="#/monitor/{crawl.id}"
                      class="px-3 py-1 rounded text-xs font-medium bg-blue-500/20 text-blue-400 hover:bg-blue-500/30 transition-colors"
                    >Monitor</a>
                  {/if}
                  {#if crawl.status === 'paused' || crawl.status === 'failed' || crawl.status === 'cancelled'}
                    <button
                      type="button"
                      class="px-3 py-1 rounded text-xs font-medium bg-green-500/10 text-green-400 hover:bg-green-500/20 transition-colors cursor-pointer"
                      onclick={() => resumeCrawl(crawl.id)}
                    >Resume</button>
                  {/if}
                  <button
                    type="button"
                    class="px-3 py-1 rounded text-xs font-medium bg-red-500/10 text-red-400 hover:bg-red-500/20 transition-colors cursor-pointer disabled:opacity-50"
                    disabled={deleting === crawl.id}
                    onclick={() => { if (confirm(`Delete crawl of ${extractDomain(crawl.seedUrl)}? This cannot be undone.`)) deleteCrawl(crawl.id); }}
                  >{deleting === crawl.id ? '...' : 'Delete'}</button>
                </div>
              </td>
            </tr>
            {#if isExpanded}
              <tr class="border-b border-border/50 bg-surface-3/50">
                <td colspan={colCount} class="px-4 py-4">
                  {#if crawl.events && crawl.events.length > 0}
                    <div class="mb-3">
                      <div class="text-[10px] uppercase text-fg-2/60 font-medium mb-1.5">Timeline</div>
                      <div class="flex flex-wrap gap-1.5 items-center text-xs">
                        {#each crawl.events as event, i}
                          {@const color = event.type === 'completed' ? 'text-green-400' : event.type === 'failed' ? 'text-red-400' : event.type === 'paused' || event.type === 'cancelled' ? 'text-yellow-400' : 'text-fg-2'}
                          {#if i > 0}<span class="text-fg-2/30">&rarr;</span>{/if}
                          <span class="{color}">
                            {event.type}
                            <span class="text-fg-2/50">{new Date(event.at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</span>
                            {#if event.note}<span class="text-fg-2/40">({event.note})</span>{/if}
                          </span>
                        {/each}
                      </div>
                    </div>
                  {/if}
                  {#if statsLoading.has(crawl.id)}
                    <div class="flex items-center gap-2 py-4 justify-center">
                      <div class="w-4 h-4 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
                      <span class="text-sm text-fg-2">Loading stats...</span>
                    </div>
                  {:else if statsCache[crawl.id]}
                    <SignalsView stats={statsCache[crawl.id]} pageCount={crawl.pageCount} />
                  {:else if crawl.status !== 'completed'}
                    <p class="text-sm text-fg-2 text-center py-2">No stats available.</p>
                  {/if}
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>

    <div class="text-xs text-fg-2 text-center">
      {filteredCrawls.length}{statusFilter !== 'all' ? ` ${statusFilter}` : ''} crawl{filteredCrawls.length !== 1 ? 's' : ''}{statusFilter !== 'all' ? ` of ${crawls.length} total` : ' total'} — select two completed same-domain crawls to compare
    </div>
  {/if}
</div>
