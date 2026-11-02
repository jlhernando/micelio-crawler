<script lang="ts">
  import { api } from '../../api.js';
  import { DEVICES } from '../../devices.js';

  interface Parity {
    primary: string; secondary: string; rendered: boolean; compared: number; identical: number;
    issues: Record<string, number>; linksOnlyPrimary: number; linksOnlySecondary: number; linksParamOnly?: number; avgSimilarity: number;
  }
  interface Snap {
    status: number; finalUrl?: string; title?: string; canonical?: string; metaRobots?: string; h1?: string[];
    wordCount: number; internalLinks: number; schemaTypes?: string[]; viewport: boolean; error?: string;
  }
  interface Cmp {
    device: string; primary: Snap; secondary: Snap; diffs?: string[]; textSimilarity: number;
    linksOnlyPrimary?: string[]; linksOnlySecondary?: string[]; linksOnlyPrimaryCount?: number; linksOnlySecondaryCount?: number;
  }

  let { crawlId, stats }: { crawlId: string; stats: Record<string, unknown> } = $props();
  let parity = $derived(stats.deviceParity as Parity | undefined);

  const LABELS: Record<string, string> = {
    'device-links': 'Internal links only one device sees',
    'device-content': 'Different main content',
    'device-status': 'Different HTTP status',
    'device-redirect': 'Redirects to a different URL',
    'device-canonical': 'Different canonical',
    'device-robots': 'Different meta robots',
    'device-title': 'Different title',
    'device-description': 'Different meta description',
    'device-h1': 'Different h1',
    'device-schema': 'Different structured data',
    'device-hreflang': 'Different hreflang',
    'device-no-viewport': 'Mobile page without meta viewport',
    'device-failed': 'Compare device could not fetch the page',
  };
  // Differences that hurt mobile-first indexing come first.
  const SEVERE = new Set(['device-links', 'device-content', 'device-status', 'device-redirect', 'device-canonical', 'device-robots', 'device-schema', 'device-no-viewport']);

  const name = (id: string) => (id ? DEVICES.find((d) => d.id === id)?.label ?? id : 'Micelio bot');

  let filter = $state('device-any');
  let rows = $state<Record<string, unknown>[]>([]);
  let total = $state(0);
  let offset = $state(0);
  let loading = $state(false);
  let open = $state<string | null>(null);
  const LIMIT = 50;

  async function load() {
    loading = true;
    try {
      const r = await api.getCrawlPages(crawlId, { issue: filter, offset, limit: LIMIT });
      rows = r.pages;
      total = r.total;
    } catch {
      rows = [];
      total = 0;
    } finally {
      loading = false;
    }
  }
  $effect(() => {
    void filter; void offset; void crawlId;
    load();
  });

  let issues = $derived(parity ? Object.entries(parity.issues ?? {}).sort((a, b) => (SEVERE.has(b[0]) ? 1 : 0) - (SEVERE.has(a[0]) ? 1 : 0) || b[1] - a[1]) : []);
  const pct = (n: number, d: number) => (d ? Math.round((n / d) * 100) : 0);
  const path = (u: string) => { try { const x = new URL(u); return x.pathname + x.search; } catch { return u; } };
</script>

{#if !parity}
  <div class="rounded-md border border-border bg-surface-2 p-5 text-sm text-fg-2">
    This crawl did not compare devices. On New Crawl, pick a Device and turn on <b>Compare with another device</b>.
  </div>
{:else}
  <div class="space-y-4" data-testid="devices-view">
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <div class="text-xs uppercase tracking-wider text-fg-2">Device comparison{parity.rendered ? ' · rendered with JavaScript' : ''}</div>
      <div class="mt-1 text-lg font-semibold">{name(parity.primary)} <span class="text-fg-2 font-normal">vs</span> {name(parity.secondary)}</div>
      <div class="mt-4 grid grid-cols-2 gap-3 md:grid-cols-5">
        <div><div class="text-xs text-fg-2">Pages compared</div><div class="text-2xl font-bold">{parity.compared.toLocaleString()}</div></div>
        <div><div class="text-xs text-fg-2">Identical</div><div class="text-2xl font-bold">{pct(parity.identical, parity.compared)}%</div><div class="text-xs text-fg-2">{parity.identical.toLocaleString()} pages</div></div>
        <div><div class="text-xs text-fg-2">Text similarity</div><div class="text-2xl font-bold">{parity.avgSimilarity}%</div><div class="text-xs text-fg-2">average</div></div>
        <div><div class="text-xs text-fg-2">Links only on {name(parity.secondary)}</div><div class="text-2xl font-bold {parity.linksOnlySecondary ? 'text-warning' : ''}">{parity.linksOnlySecondary.toLocaleString()}</div></div>
        <div><div class="text-xs text-fg-2">Links only on {name(parity.primary)}</div><div class="text-2xl font-bold">{parity.linksOnlyPrimary.toLocaleString()}</div></div>
      </div>
      {#if parity.linksParamOnly}
        <p class="mt-3 text-xs text-fg-2">{parity.linksParamOnly.toLocaleString()} of the one-sided links point to a page the other device also links, with other query parameters (for example tracking or filter values).</p>
      {/if}
      {#if parity.primary && DEVICES.find((d) => d.id === parity.primary)?.kind !== 'desktop' && parity.linksOnlySecondary}
        <p class="mt-3 text-xs text-fg-2">Google indexes the mobile version. Links and content that only {name(parity.secondary)} sees may not count.</p>
      {/if}
    </div>

    <div class="flex flex-wrap gap-2">
      <button type="button" class="dv-chip {filter === 'device-any' ? 'on' : ''}" onclick={() => { filter = 'device-any'; offset = 0; }}>
        Any difference <b>{(parity.compared - parity.identical).toLocaleString()}</b>
      </button>
      {#each issues as [id, n] (id)}
        <button type="button" class="dv-chip {filter === id ? 'on' : ''} {SEVERE.has(id) ? 'sev' : ''}" onclick={() => { filter = id; offset = 0; }}>
          {LABELS[id] ?? id} <b>{n.toLocaleString()}</b>
        </button>
      {/each}
    </div>

    <div class="rounded-md border border-border bg-surface-2 overflow-hidden">
      <table class="w-full text-xs">
        <thead class="text-fg-2 text-left">
          <tr class="border-b border-border">
            <th class="p-2 font-medium">Page</th>
            <th class="p-2 font-medium">Differences</th>
            <th class="p-2 font-medium text-right">Words</th>
            <th class="p-2 font-medium text-right">Links</th>
            <th class="p-2 font-medium text-right">Similar</th>
          </tr>
        </thead>
        <tbody>
          {#each rows as p (p.url)}
            {@const c = p.deviceCompare as Cmp | undefined}
            {#if c}
              <tr class="border-b border-border/60 hover:bg-surface-3 cursor-pointer" onclick={() => (open = open === p.url ? null : (p.url as string))}>
                <td class="p-2 font-mono max-w-[360px] truncate" title={p.url as string}>{path(p.url as string)}</td>
                <td class="p-2"><div class="flex flex-wrap gap-1">{#each c.diffs ?? [] as d}<span class="dv-tag {SEVERE.has(d) ? 'sev' : ''}">{(LABELS[d] ?? d).replace(/^Different /, '')}</span>{/each}</div></td>
                <td class="p-2 text-right tabular-nums">{c.primary.wordCount} / {c.secondary.wordCount}</td>
                <td class="p-2 text-right tabular-nums">{c.primary.internalLinks} / {c.secondary.internalLinks}</td>
                <td class="p-2 text-right tabular-nums">{c.textSimilarity}%</td>
              </tr>
              {#if open === p.url}
                <tr class="border-b border-border/60 bg-surface">
                  <td colspan="5" class="p-3">
                    <div class="grid gap-4 md:grid-cols-2">
                      {#each [[parity.primary, c.primary, c.linksOnlyPrimary, c.linksOnlyPrimaryCount], [parity.secondary, c.secondary, c.linksOnlySecondary, c.linksOnlySecondaryCount]] as [dev, s, only, n]}
                        {@const snap = s as Snap}
                        <div class="space-y-1">
                          <div class="font-medium text-fg">{name(dev as string)}</div>
                          {#if snap.error}<div class="text-danger">{snap.error}</div>{/if}
                          <div><span class="text-fg-2">Status</span> {snap.status}{snap.finalUrl && snap.finalUrl !== p.url ? ` → ${path(snap.finalUrl)}` : ''}</div>
                          <div class="truncate"><span class="text-fg-2">Title</span> {snap.title ?? '—'}</div>
                          <div class="truncate"><span class="text-fg-2">H1</span> {snap.h1?.join(' · ') || '—'}</div>
                          <div class="truncate"><span class="text-fg-2">Canonical</span> {snap.canonical ? path(snap.canonical) : '—'}</div>
                          {#if snap.metaRobots}<div><span class="text-fg-2">Robots</span> {snap.metaRobots}</div>{/if}
                          <div><span class="text-fg-2">Schema</span> {snap.schemaTypes?.join(', ') || '—'} · <span class="text-fg-2">viewport</span> {snap.viewport ? 'yes' : 'no'}</div>
                          {#if n}
                            <div class="pt-1 text-fg-2">Links only here ({(n as number).toLocaleString()}):</div>
                            <ul class="font-mono">{#each (only as string[]) ?? [] as l}<li class="truncate"><a href={l} target="_blank" rel="noreferrer" class="text-fg-2 hover:text-fg">{path(l)}</a></li>{/each}</ul>
                          {/if}
                        </div>
                      {/each}
                    </div>
                  </td>
                </tr>
              {/if}
            {/if}
          {:else}
            <tr><td colspan="5" class="p-4 text-center text-fg-2">{loading ? 'Loading…' : 'No pages with this difference.'}</td></tr>
          {/each}
        </tbody>
      </table>
      {#if total > LIMIT}
        <div class="flex items-center justify-between p-2 text-xs text-fg-2">
          <span>{offset + 1}–{Math.min(offset + LIMIT, total)} of {total.toLocaleString()}</span>
          <span class="flex gap-2">
            <button type="button" class="dv-chip" disabled={offset === 0} onclick={() => (offset = Math.max(0, offset - LIMIT))}>Previous</button>
            <button type="button" class="dv-chip" disabled={offset + LIMIT >= total} onclick={() => (offset += LIMIT)}>Next</button>
          </span>
        </div>
      {/if}
    </div>
  </div>
{/if}

<style>
  .dv-chip { display: inline-flex; align-items: center; gap: 6px; padding: 4px 10px; border-radius: 8px; border: 1px solid var(--color-border); background: var(--color-surface-2); color: var(--color-fg-2); font-size: 12px; cursor: pointer; }
  .dv-chip:hover:not(:disabled) { border-color: var(--color-fg-2); color: var(--color-fg); }
  .dv-chip.on { border-color: var(--color-accent); color: var(--color-fg); background: var(--color-surface-3); }
  .dv-chip.sev b { color: var(--color-warning); }
  .dv-chip:disabled { opacity: .4; cursor: default; }
  .dv-tag { padding: 1px 6px; border-radius: 6px; background: var(--color-surface-3); color: var(--color-fg-2); white-space: nowrap; }
  .dv-tag.sev { color: var(--color-warning); }
</style>
