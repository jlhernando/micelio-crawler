<script lang="ts">
  import { formatDateTime, formatDuration } from '../lib/format';
  import PageHeading from '../lib/components/layout/PageHeading.svelte';
  import ScheduleForm from '../lib/components/schedule/ScheduleForm.svelte';
  import { onMount } from 'svelte';
  import { api, type ScheduleState, type PrecheckResult } from '../lib/api';

  let loading = $state(true);
  let schedules = $state<ScheduleState[]>([]);
  let error = $state<string | null>(null);
  let showForm = $state(false);
  let busy = $state<string | null>(null);
  let checks = $state<Record<string, PrecheckResult>>({});

  // Quick form: the basics. "Schedule…" on New Crawl schedules any crawl config.
  let formMode = $state<'spider' | 'sitemap'>('spider');
  let formUrl = $state('');
  let formLimit = $state(1000);
  let formConcurrency = $state(4);
  let formDelay = $state(500);

  function quickConfig(): Record<string, unknown> {
    const u = formUrl.trim();
    if (!u) throw new Error('URL is required');
    const base = { mode: formMode, limit: formLimit, depth: 20, concurrency: formConcurrency, delay: formDelay, adaptiveRate: true, respectRobots: true, linkIntelligence: true };
    return formMode === 'sitemap' ? { ...base, sitemapUrls: u.split(/\s+/).filter(Boolean) } : { ...base, seedUrl: u };
  }

  onMount(() => {
    loadSchedules();
    const t = setInterval(loadSchedules, 15000); // runs and checks change in the background
    return () => clearInterval(t);
  });

  async function loadSchedules() {
    try {
      schedules = await api.listSchedules();
    } catch (err) {
      error = (err as Error).message;
    } finally {
      loading = false;
    }
  }

  async function act(id: string, fn: () => Promise<unknown>) {
    busy = id;
    error = null;
    try {
      await fn();
      await loadSchedules();
    } catch (err) {
      error = (err as Error).message;
    } finally {
      busy = null;
    }
  }

  const runNow = (id: string) => act(id, () => api.runSchedule(id));
  const checkNow = (id: string) => act(id, async () => { checks[id] = await api.checkSchedule(id); });
  const remove = (id: string) => act(id, () => api.deleteSchedule(id));

  function statusBadge(s: ScheduleState): string {
    if (s.running) return s.paused ? 'bg-warning/20 text-warning' : 'bg-accent/20 text-accent';
    if (s.lastStatus === 'success') return 'bg-success/20 text-success';
    if (s.lastStatus === 'failed') return 'bg-danger/20 text-danger';
    if (s.lastStatus === 'skipped' || s.lastStatus === 'stopped') return 'bg-warning/20 text-warning';
    return 'bg-surface-3 text-fg-2';
  }
  function statusLabel(s: ScheduleState): string {
    if (s.running) return s.paused ? 'paused' : 'running';
    return s.lastStatus || 'never run';
  }
  function modeOf(s: ScheduleState): string {
    const m = (s.crawlConfig?.mode as string) || 'spider';
    const limit = s.crawlConfig?.limit ?? s.crawlConfig?.maxPages;
    return limit ? `${m} · ${Number(limit).toLocaleString()} pages` : m;
  }
  function checkLabel(s: ScheduleState): string {
    const p = s.precheck;
    if (!p || (!p.egressCidrs?.length && !(p as { command?: string }).command)) return '';
    const parts = [];
    if (p.egressCidrs?.length) parts.push(`IP in ${p.egressCidrs.length} range${p.egressCidrs.length > 1 ? 's' : ''}`);
    if ((p as { command?: string }).command) parts.push('command');
    return parts.join(' + ');
  }
  const field = 'w-full px-3 py-2 rounded-md border border-border bg-surface-1 text-fg text-sm focus:outline-none focus:border-accent';
</script>

<div class="max-w-6xl mx-auto">
  <PageHeading title="Schedules" description="Recurring crawls. Each run appears in History, so you can compare runs and get alerts." section="Automation" />
  <div class="flex items-center justify-between mb-6">
    <p class="text-xs text-fg-2">To schedule any crawl setup (sitemaps, filters, extractions…), use <a class="underline hover:text-fg" href="#/setup">New Crawl</a> → Schedule….</p>
    <button class="px-4 py-2 bg-accent text-white rounded-md text-sm font-medium hover:bg-accent/90 transition-colors cursor-pointer" onclick={() => { showForm = !showForm; }}>
      {showForm ? 'Cancel' : 'New Schedule'}
    </button>
  </div>

  {#if error}<div class="mb-4 px-4 py-3 rounded-md bg-danger/10 text-danger text-sm">{error}</div>{/if}

  {#if !loading && schedules.length > 0}
    <dl class="metric-strip">
      <div><dt>Schedules</dt><dd>{schedules.length}</dd><small>{schedules.filter((s) => s.active).length} active</small></div>
      <div><dt>Total runs</dt><dd>{schedules.reduce((sum, s) => sum + s.totalRuns, 0).toLocaleString()}</dd><small>Across all schedules</small></div>
      <div><dt>Last run succeeded</dt><dd class="text-success">{schedules.filter((s) => s.lastStatus === 'success').length}</dd><small>Schedules</small></div>
      <div><dt>Need attention</dt><dd class="text-danger">{schedules.filter((s) => s.lastStatus && s.lastStatus !== 'success').length}</dd><small>Failed, skipped or stopped</small></div>
    </dl>
  {/if}

  {#if showForm}
    <div class="mb-6 p-5 rounded-lg border border-border bg-surface-2 space-y-4">
      <h2 class="text-lg font-semibold text-fg">Create schedule</h2>
      <div class="grid grid-cols-1 md:grid-cols-4 gap-4">
        <div>
          <label for="q-mode" class="block text-sm text-fg-2 mb-1">Mode</label>
          <select id="q-mode" class={field} bind:value={formMode}>
            <option value="spider">Spider from a URL</option>
            <option value="sitemap">Sitemap URLs</option>
          </select>
        </div>
        <div class="md:col-span-3">
          <label for="q-url" class="block text-sm text-fg-2 mb-1">{formMode === 'sitemap' ? 'Sitemap URLs (space or line separated)' : 'Start URL'}</label>
          <input id="q-url" class={field} bind:value={formUrl} placeholder={formMode === 'sitemap' ? 'https://example.com/sitemap.xml' : 'https://example.com'} />
        </div>
        <div>
          <label for="q-limit" class="block text-sm text-fg-2 mb-1">Page limit</label>
          <input id="q-limit" type="number" min="1" class={field} bind:value={formLimit} />
        </div>
        <div>
          <label for="q-conc" class="block text-sm text-fg-2 mb-1">Concurrency</label>
          <input id="q-conc" type="number" min="1" max="50" class={field} bind:value={formConcurrency} />
        </div>
        <div>
          <label for="q-delay" class="block text-sm text-fg-2 mb-1">Delay (ms)</label>
          <input id="q-delay" type="number" min="0" class={field} bind:value={formDelay} />
        </div>
        <div class="text-xs text-fg-2 self-end pb-2">Adaptive rate, robots.txt and link intelligence on.</div>
      </div>
      <ScheduleForm config={quickConfig} onCreated={() => { showForm = false; formUrl = ''; loadSchedules(); }} />
    </div>
  {/if}

  {#if loading}
    <div class="flex items-center justify-center py-12">
      <div class="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
      <span class="text-fg-2 ml-3">Loading schedules...</span>
    </div>
  {:else if schedules.length === 0}
    <div class="text-center py-16">
      <p class="text-fg-2">No scheduled crawls yet.</p>
      <p class="text-fg-2 text-sm mt-1">Create a schedule to crawl a site on a recurring basis.</p>
    </div>
  {:else}
    <div class="rounded-lg border border-border overflow-x-auto">
      <table class="w-full text-sm">
        <thead class="bg-surface-2">
          <tr class="text-left text-xs text-fg-2 border-b border-border">
            <th class="px-4 py-3">Schedule</th>
            <th class="px-3 py-3">When</th>
            <th class="px-3 py-3">Only when</th>
            <th class="px-3 py-3">Last run</th>
            <th class="px-3 py-3">Next run</th>
            <th class="px-3 py-3"></th>
          </tr>
        </thead>
        <tbody>
          {#each schedules as s (s.id)}
            {@const ck = checks[s.id] ?? s.lastCheck}
            <tr class="border-b border-border/50 align-top hover:bg-surface-3/50 transition-colors">
              <td class="px-4 py-3 max-w-xs">
                <div class="text-fg text-sm truncate">{s.name || s.url}</div>
                {#if s.name}<div class="font-mono text-[11px] text-fg-2 truncate">{s.url}</div>{/if}
                <div class="text-[11px] text-fg-2 mt-0.5">{modeOf(s)}{s.source === 'cli' ? ' · CLI' : ''}{s.active ? '' : ' · inactive'}</div>
              </td>
              <td class="px-3 py-3 text-xs text-fg">{s.description || s.cron}</td>
              <td class="px-3 py-3 text-xs">
                {#if checkLabel(s)}
                  <div class="text-fg-1">{checkLabel(s)}</div>
                  {#if ck}
                    <div class="mt-0.5 {ck.ok ? 'text-success' : 'text-danger'}" title={ck.at}>{ck.ok ? 'passing' : ck.reason}</div>
                  {/if}
                {:else}
                  <span class="text-fg-2">—</span>
                {/if}
              </td>
              <td class="px-3 py-3 text-xs">
                <span class="inline-block px-1.5 py-0.5 rounded {statusBadge(s)}">{statusLabel(s)}</span>
                {#if s.lastRun}<span class="text-fg-2 ml-1">{formatDateTime(s.lastRun)}</span>{/if}
                {#if s.lastReason}<div class="text-fg-2 mt-0.5 max-w-[260px]">{s.lastReason}</div>{/if}
                {#if s.lastRun && s.lastStatus !== 'skipped'}
                  <div class="text-fg-2 mt-0.5">{s.lastPages.toLocaleString()} pages in {formatDuration(s.lastDurationMs)}</div>
                {/if}
                {#if s.lastCrawlId}<a class="text-accent hover:underline" href="#/results/{s.lastCrawlId}">Open crawl →</a>{/if}
              </td>
              <td class="px-3 py-3 text-xs text-fg-2">{formatDateTime(s.nextRun)}</td>
              <td class="px-3 py-3 whitespace-nowrap text-right">
                {#if s.active}
                  <button class="px-2 py-1 text-xs text-fg-1 hover:text-fg rounded cursor-pointer disabled:opacity-50" disabled={busy === s.id || s.running} onclick={() => runNow(s.id)}>Run now</button>
                  {#if checkLabel(s)}
                    <button class="px-2 py-1 text-xs text-fg-1 hover:text-fg rounded cursor-pointer disabled:opacity-50" disabled={busy === s.id} onclick={() => checkNow(s.id)}>Test check</button>
                  {/if}
                {/if}
                <button class="px-2 py-1 text-xs text-danger hover:bg-danger/10 rounded cursor-pointer disabled:opacity-50" disabled={busy === s.id} onclick={() => remove(s.id)}>Delete</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
