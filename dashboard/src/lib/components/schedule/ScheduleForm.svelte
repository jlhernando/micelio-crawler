<script lang="ts">
  import { api, type PrecheckResult, type SchedulePrecheck } from '../../api';

  // config: a crawl config in the startCrawl format. The caller builds it.
  let { config, onCreated, onCancel }: {
    config: () => Record<string, unknown>;
    onCreated?: (id: string) => void;
    onCancel?: () => void;
  } = $props();

  const presets = [
    { label: 'Every day at 02:00', value: '0 2 * * *' },
    { label: 'Every day at 03:00', value: '0 3 * * *' },
    { label: 'Every Monday at 03:00', value: '0 3 * * 1' },
    { label: 'Every hour', value: '@hourly' },
    { label: 'First of every month', value: '@monthly' },
    { label: 'Custom', value: 'custom' },
  ];

  let name = $state('');
  let cron = $state('0 2 * * *');
  let customCron = $state('');
  let webhook = $state('');
  let egress = $state('');
  let saving = $state(false);
  let testing = $state(false);
  let error = $state<string | null>(null);
  let check = $state<PrecheckResult | null>(null);

  function precheck(): SchedulePrecheck | undefined {
    const egressCidrs = egress.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean);
    if (!egressCidrs.length) return undefined;
    return { egressCidrs };
  }

  async function test() {
    const pc = precheck();
    if (!pc) return;
    testing = true;
    error = null;
    try {
      check = await api.testPrecheck(pc);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      testing = false;
    }
  }

  async function save() {
    const expr = cron === 'custom' ? customCron.trim() : cron;
    if (!expr) { error = 'Cron expression is required'; return; }
    saving = true;
    error = null;
    try {
      const res = await api.createSchedule({ cron: expr, name: name.trim() || undefined, webhook: webhook.trim() || undefined, config: config(), precheck: precheck() });
      onCreated?.(res.id);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      saving = false;
    }
  }

  const field = 'w-full px-3 py-2 rounded-md border border-border bg-surface-1 text-fg text-sm focus:outline-none focus:border-accent';
</script>

<div class="space-y-4">
  {#if error}<div class="px-4 py-2 rounded-md bg-danger/10 text-danger text-sm">{error}</div>{/if}
  <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
    <div>
      <label for="sf-name" class="block text-sm text-fg-2 mb-1">Name <span class="text-fg-2/50">(optional)</span></label>
      <input id="sf-name" class={field} bind:value={name} placeholder="Category sitemaps" />
    </div>
    <div>
      <label for="sf-cron" class="block text-sm text-fg-2 mb-1">When</label>
      <select id="sf-cron" class={field} bind:value={cron}>
        {#each presets as p}<option value={p.value}>{p.label}</option>{/each}
      </select>
    </div>
    {#if cron === 'custom'}
      <div class="md:col-span-2">
        <label for="sf-custom" class="block text-sm text-fg-2 mb-1">Cron expression</label>
        <input id="sf-custom" class="{field} font-mono" bind:value={customCron} placeholder="30 1 * * 1-5" />
        <p class="text-xs text-fg-2 mt-1">minute hour day-of-month month day-of-week, in this machine's time zone</p>
      </div>
    {/if}
    <div class="md:col-span-2">
      <label for="sf-webhook" class="block text-sm text-fg-2 mb-1">Webhook <span class="text-fg-2/50">(optional: Slack or any URL; also notified when a run is skipped)</span></label>
      <input id="sf-webhook" class={field} type="url" bind:value={webhook} placeholder="https://hooks.slack.com/services/..." />
    </div>
  </div>

  <fieldset class="rounded-md border border-border p-4">
    <legend class="px-1 text-sm text-fg">Only run when…</legend>
    <label for="sf-egress" class="block text-sm text-fg-2 mb-1">…the public IP is in these ranges <span class="text-fg-2/50">(CIDR or IP, one per line; leave empty to always run)</span></label>
    <textarea id="sf-egress" rows="2" class="{field} font-mono" bind:value={egress} placeholder="203.0.113.0/24"></textarea>
    <p class="text-xs text-fg-2 mt-2">Checked before each run (skipped if it fails) and every minute while crawling: the crawl pauses until it passes again, and stops after 6 hours paused.</p>
    <div class="mt-3 flex items-center gap-3">
      <button type="button" class="px-3 py-1.5 rounded-md border border-border text-xs text-fg-1 hover:text-fg disabled:opacity-50 cursor-pointer" disabled={testing || !precheck()} onclick={test}>{testing ? 'Checking…' : 'Test check now'}</button>
      {#if check}
        <span class="text-xs {check.ok ? 'text-success' : 'text-danger'}">
          {check.ok ? 'Passes' : check.reason}{check.egressIp ? ` · IP: ${check.egressIp}` : ''}
        </span>
      {/if}
    </div>
  </fieldset>

  <div class="flex justify-end gap-2">
    {#if onCancel}<button type="button" class="px-4 py-2 rounded-md text-sm text-fg-2 hover:text-fg cursor-pointer" onclick={onCancel}>Cancel</button>{/if}
    <button type="button" class="px-4 py-2 bg-accent text-white rounded-md text-sm font-medium hover:bg-accent/90 disabled:opacity-50 cursor-pointer" disabled={saving} onclick={save}>{saving ? 'Saving…' : 'Create schedule'}</button>
  </div>
</div>
