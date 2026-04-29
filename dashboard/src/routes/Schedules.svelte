<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type ScheduleState } from '../lib/api';

  let loading = $state(true);
  let schedules = $state<ScheduleState[]>([]);
  let error = $state<string | null>(null);
  let showForm = $state(false);
  let creating = $state(false);
  let deleting = $state<string | null>(null);

  // Form state
  let formUrl = $state('');
  let formCron = $state('@daily');
  let formCustomCron = $state('');
  let formDepth = $state(10);
  let formLimit = $state(1000);
  let formConcurrency = $state(5);
  let formDelay = $state(200);
  let formWebhook = $state('');
  let formError = $state<string | null>(null);

  const cronPresets = [
    { label: 'Every hour', value: '@hourly' },
    { label: 'Every day at midnight', value: '@daily' },
    { label: 'Every Monday at midnight', value: '0 9 * * 1' },
    { label: 'First of every month', value: '@monthly' },
    { label: 'Custom', value: 'custom' },
  ];

  onMount(() => { loadSchedules(); });

  async function loadSchedules() {
    try {
      schedules = await api.listSchedules();
      loading = false;
    } catch (err) {
      error = (err as Error).message;
      loading = false;
    }
  }

  async function createSchedule() {
    formError = null;
    if (!formUrl) { formError = 'URL is required'; return; }

    const cron = formCron === 'custom' ? formCustomCron : formCron;
    if (!cron) { formError = 'Cron expression is required'; return; }

    creating = true;
    try {
      await api.createSchedule({
        url: formUrl,
        cron,
        depth: formDepth,
        limit: formLimit,
        concurrency: formConcurrency,
        delay: formDelay,
        webhook: formWebhook || undefined,
      });
      showForm = false;
      formUrl = '';
      formCron = '@daily';
      formCustomCron = '';
      formWebhook = '';
      await loadSchedules();
    } catch (err) {
      formError = (err as Error).message;
    } finally {
      creating = false;
    }
  }

  async function deleteSchedule(id: string) {
    deleting = id;
    try {
      await api.deleteSchedule(id);
      schedules = schedules.filter(s => s.id !== id);
    } catch (err) {
      error = (err as Error).message;
    } finally {
      deleting = null;
    }
  }

  function formatDate(iso: string | undefined): string {
    if (!iso) return '—';
    const d = new Date(iso);
    return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  }

  function formatDuration(ms: number): string {
    if (!ms) return '—';
    if (ms < 1000) return `${ms}ms`;
    const s = Math.floor(ms / 1000);
    if (s < 60) return `${s}s`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ${s % 60}s`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ${m % 60}m`;
    const d = Math.floor(h / 24);
    return `${d}d ${h % 24}h ${m % 60}m`;
  }

  function statusBadge(status: string | undefined): string {
    if (status === 'success') return 'bg-success/20 text-success';
    if (status === 'failed') return 'bg-danger/20 text-danger';
    return 'bg-surface-3 text-fg-2';
  }
</script>

<div class="p-6 max-w-5xl mx-auto">
  <div class="flex items-center justify-between mb-6">
    <div>
      <h1 class="text-2xl font-bold text-fg">Schedules</h1>
      <p class="text-sm text-fg-2 mt-1">Recurring crawls that run automatically on a schedule.</p>
    </div>
    <button
      class="px-4 py-2 bg-accent text-white rounded-md text-sm font-medium hover:bg-accent/90 transition-colors cursor-pointer"
      onclick={() => { showForm = !showForm; }}
    >
      {showForm ? 'Cancel' : 'New Schedule'}
    </button>
  </div>

  {#if error}
    <div class="mb-4 px-4 py-3 rounded-md bg-danger/10 text-danger text-sm">{error}</div>
  {/if}

  <!-- Create form -->
  {#if showForm}
    <div class="mb-6 p-5 rounded-lg border border-border bg-surface-2">
      <h2 class="text-lg font-semibold text-fg mb-4">Create Schedule</h2>

      {#if formError}
        <div class="mb-4 px-4 py-2 rounded-md bg-danger/10 text-danger text-sm">{formError}</div>
      {/if}

      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div class="md:col-span-2">
          <label for="sched-url" class="block text-sm text-fg-2 mb-1">URL</label>
          <input
            id="sched-url"
            type="url"
            bind:value={formUrl}
            placeholder="https://example.com"
            class="w-full px-3 py-2 rounded-md border border-border bg-surface-1 text-fg text-sm focus:outline-none focus:border-accent"
          />
        </div>

        <div>
          <label for="sched-cron" class="block text-sm text-fg-2 mb-1">Schedule</label>
          <select
            id="sched-cron"
            bind:value={formCron}
            class="w-full px-3 py-2 rounded-md border border-border bg-surface-1 text-fg text-sm focus:outline-none focus:border-accent"
          >
            {#each cronPresets as preset}
              <option value={preset.value}>{preset.label}</option>
            {/each}
          </select>
        </div>

        {#if formCron === 'custom'}
          <div>
            <label for="sched-custom-cron" class="block text-sm text-fg-2 mb-1">Cron Expression</label>
            <input
              id="sched-custom-cron"
              type="text"
              bind:value={formCustomCron}
              placeholder="0 9 * * 1"
              class="w-full px-3 py-2 rounded-md border border-border bg-surface-1 text-fg text-sm font-mono focus:outline-none focus:border-accent"
            />
            <p class="text-xs text-fg-2 mt-1">Format: minute hour day-of-month month day-of-week</p>
          </div>
        {/if}

        <div>
          <label for="sched-depth" class="block text-sm text-fg-2 mb-1">Depth</label>
          <input id="sched-depth" type="number" bind:value={formDepth} min="1" max="100"
            class="w-full px-3 py-2 rounded-md border border-border bg-surface-1 text-fg text-sm focus:outline-none focus:border-accent" />
        </div>

        <div>
          <label for="sched-limit" class="block text-sm text-fg-2 mb-1">Page Limit</label>
          <input id="sched-limit" type="number" bind:value={formLimit} min="1"
            class="w-full px-3 py-2 rounded-md border border-border bg-surface-1 text-fg text-sm focus:outline-none focus:border-accent" />
        </div>

        <div>
          <label for="sched-concurrency" class="block text-sm text-fg-2 mb-1">Concurrency</label>
          <input id="sched-concurrency" type="number" bind:value={formConcurrency} min="1" max="50"
            class="w-full px-3 py-2 rounded-md border border-border bg-surface-1 text-fg text-sm focus:outline-none focus:border-accent" />
        </div>

        <div>
          <label for="sched-delay" class="block text-sm text-fg-2 mb-1">Delay (ms)</label>
          <input id="sched-delay" type="number" bind:value={formDelay} min="0"
            class="w-full px-3 py-2 rounded-md border border-border bg-surface-1 text-fg text-sm focus:outline-none focus:border-accent" />
        </div>

        <div class="md:col-span-2">
          <label for="sched-webhook" class="block text-sm text-fg-2 mb-1">Webhook URL <span class="text-fg-2/50">(optional)</span></label>
          <input
            id="sched-webhook"
            type="url"
            bind:value={formWebhook}
            placeholder="https://hooks.slack.com/services/..."
            class="w-full px-3 py-2 rounded-md border border-border bg-surface-1 text-fg text-sm focus:outline-none focus:border-accent"
          />
        </div>
      </div>

      <div class="flex justify-end mt-5">
        <button
          class="px-4 py-2 bg-accent text-white rounded-md text-sm font-medium hover:bg-accent/90 transition-colors disabled:opacity-50 cursor-pointer"
          disabled={creating}
          onclick={createSchedule}
        >
          {creating ? 'Creating...' : 'Create Schedule'}
        </button>
      </div>
    </div>
  {/if}

  <!-- Schedule list -->
  {#if loading}
    <div class="flex items-center justify-center py-12">
      <div class="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
      <span class="text-fg-2 ml-3">Loading schedules...</span>
    </div>
  {:else if schedules.length === 0}
    <div class="text-center py-16">
      <div class="text-fg-2 text-4xl mb-3">&#128197;</div>
      <p class="text-fg-2">No scheduled crawls yet.</p>
      <p class="text-fg-2 text-sm mt-1">Create a schedule to automatically crawl a site on a recurring basis.</p>
    </div>
  {:else}
    <div class="rounded-lg border border-border overflow-hidden">
      <table class="w-full text-sm">
        <thead class="bg-surface-2">
          <tr class="text-left text-xs text-fg-2 border-b border-border">
            <th class="px-4 py-3">URL</th>
            <th class="px-3 py-3 hidden sm:table-cell">Schedule</th>
            <th class="px-3 py-3 hidden md:table-cell">Last Run</th>
            <th class="px-3 py-3 hidden md:table-cell">Next Run</th>
            <th class="px-3 py-3 hidden sm:table-cell">Runs</th>
            <th class="px-3 py-3 w-20"></th>
          </tr>
        </thead>
        <tbody>
          {#each schedules as schedule}
            <tr class="border-b border-border/50 hover:bg-surface-3/50 transition-colors">
              <td class="px-4 py-3">
                <div class="font-mono text-xs text-accent truncate max-w-xs">{schedule.url}</div>
              </td>
              <td class="px-3 py-3 text-fg-2 hidden sm:table-cell">
                <span class="text-fg text-xs">{schedule.description || schedule.cron}</span>
              </td>
              <td class="px-3 py-3 hidden md:table-cell">
                {#if schedule.lastRun}
                  <div class="flex items-center gap-2">
                    <span class="inline-block px-1.5 py-0.5 rounded text-xs {statusBadge(schedule.lastStatus)}">
                      {schedule.lastStatus || 'unknown'}
                    </span>
                    <span class="text-fg-2 text-xs">{formatDate(schedule.lastRun)}</span>
                  </div>
                  <div class="text-xs text-fg-2 mt-0.5">
                    {schedule.lastPages.toLocaleString()} pages in {formatDuration(schedule.lastDurationMs)}
                  </div>
                {:else}
                  <span class="text-fg-2 text-xs">Never</span>
                {/if}
              </td>
              <td class="px-3 py-3 text-fg-2 text-xs hidden md:table-cell">
                {formatDate(schedule.nextRun)}
              </td>
              <td class="px-3 py-3 text-fg-2 font-mono text-xs hidden sm:table-cell">
                {schedule.totalRuns}
              </td>
              <td class="px-3 py-3">
                <button
                  class="px-2 py-1 text-xs text-danger hover:bg-danger/10 rounded transition-colors cursor-pointer disabled:opacity-50"
                  disabled={deleting === schedule.id}
                  onclick={() => deleteSchedule(schedule.id)}
                >
                  {deleting === schedule.id ? '...' : 'Delete'}
                </button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
