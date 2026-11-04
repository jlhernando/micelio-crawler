<script lang="ts">
  import { api } from '../../api.js';
  import { DEVICES } from '../../devices.js';

  interface Parity {
    primary: string; secondary: string; rendered: boolean; compared: number; identical: number;
    issues: Record<string, number>; linksOnlyPrimary: number; linksOnlySecondary: number; linksParamOnly?: number; avgSimilarity: number;
    rawCompared?: number; rawIssues?: Record<string, number>; rawIdentical?: number; jsChanged?: number; primaryJsPages?: number; secondaryJsPages?: number; primaryJs?: Record<string, number>; secondaryJs?: Record<string, number>;
  }
  interface Snap {
    status: number; finalUrl?: string; title?: string; canonical?: string; metaRobots?: string; h1?: string[];
    wordCount: number; internalLinks: number; schemaTypes?: string[]; viewport: boolean; error?: string;
    description?: string; hreflang?: number; raw?: Snap;
  }
  interface Cmp {
    device: string; primary: Snap; secondary: Snap; diffs?: string[]; textSimilarity: number;
    linksOnlyPrimary?: string[]; linksOnlySecondary?: string[]; linksOnlyPrimaryCount?: number; linksOnlySecondaryCount?: number;
    rawDiffs?: string[]; primaryJs?: string[]; secondaryJs?: string[]; primaryJsLinks?: number; secondaryJsLinks?: number;
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
  const kindOf = (id: string) => (id ? DEVICES.find((d) => d.id === id)?.kind ?? 'desktop' : 'desktop'); // Micelio bot renders a desktop screen
  const isBot = (id: string) => id.startsWith('googlebot') || id === 'bingbot';
  // Width matters for rendering; the bots' slightly shorter screens do not.
  let sameScreen = $derived(!!parity && (DEVICES.find((d) => d.id === parity!.primary)?.width ?? 1920) === (DEVICES.find((d) => d.id === parity!.secondary)?.width ?? 1920));

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
  // Battle grid: one row per SEO element, raw HTML and rendered DOM of each device.
  const FIELDS: { label: string; get: (s: Snap) => string }[] = [
    { label: 'Status', get: (s) => String(s.status) },
    { label: 'Title', get: (s) => s.title ?? '' },
    { label: 'Description', get: (s) => s.description ?? '' },
    { label: 'H1', get: (s) => s.h1?.join(' · ') ?? '' },
    { label: 'Canonical', get: (s) => (s.canonical ? path(s.canonical) : '') },
    { label: 'Robots', get: (s) => s.metaRobots ?? '' },
    { label: 'Words', get: (s) => String(s.wordCount) },
    { label: 'Internal links', get: (s) => String(s.internalLinks) },
    { label: 'Schema', get: (s) => s.schemaTypes?.join(', ') ?? '' },
    { label: 'Hreflang', get: (s) => String(s.hreflang ?? 0) },
    { label: 'Viewport', get: (s) => (s.viewport ? 'yes' : 'no') },
  ];
  const jsList = (m?: Record<string, number>) => Object.entries(m ?? {}).sort((a, b) => b[1] - a[1]);
  const short = (id: string) => (LABELS[id] ?? id).replace(/^Different /, '');
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
      {#if kindOf(parity.primary) !== 'desktop' && kindOf(parity.secondary) === 'desktop' && parity.linksOnlySecondary}
        <p class="mt-3 text-xs text-fg-2">Google indexes the mobile version. Links and content that only {name(parity.secondary)} sees may not count.</p>
      {/if}
      {#if isBot(parity.primary) !== isBot(parity.secondary)}
        <p class="mt-3 text-xs text-fg-2">
          {#if kindOf(parity.primary) === kindOf(parity.secondary) && (!parity.rendered || sameScreen)}
            Same screen, different agent: differences show what the site serves to {name(isBot(parity.secondary) ? parity.secondary : parity.primary)} (prerendering for bots, dynamic rendering or cloaking).
          {:else}
            This compares a bot with a browser <b>and</b> two kinds of device or screen, so each difference can come from either. To see only what the site serves to the bot, compare the same kind of device, such as Mobile with Googlebot Smartphone.
          {/if}
        </p>
      {/if}
    </div>

    {#if parity.rawCompared}
      <div class="rounded-md border border-border bg-surface-2 p-5" data-testid="devices-js">
        <div class="text-xs uppercase tracking-wider text-fg-2">Raw HTML vs rendered</div>
        <div class="mt-3 grid gap-4 md:grid-cols-3">
          <div>
            <div class="text-xs text-fg-2">Raw HTML identical on both devices</div>
            <div class="text-2xl font-bold">{pct(parity.rawIdentical ?? 0, parity.rawCompared)}%</div>
            <div class="text-xs text-fg-2">{(parity.rawIdentical ?? 0).toLocaleString()} pages, before JavaScript</div>
            {#each jsList(parity.rawIssues) as [id, n] (id)}<div class="text-xs"><span class="text-fg-2">{short(id)}</span> {n.toLocaleString()}</div>{/each}
          </div>
          {#each [[parity.primary, parity.primaryJs, parity.primaryJsPages], [parity.secondary, parity.secondaryJs, parity.secondaryJsPages]] as [dev, m, n]}
            {@const list = jsList(m as Record<string, number> | undefined)}
            {@const pages = (n as number | undefined) ?? 0}
            <div>
              <div class="text-xs text-fg-2">JavaScript changes SEO elements on {name(dev as string)}</div>
              <div class="text-2xl font-bold {pages ? 'text-warning' : ''}">{pct(pages, parity.compared)}%</div>
              <div class="text-xs text-fg-2">{pages.toLocaleString()} pages</div>
              {#each list as [id, n] (id)}<div class="text-xs"><span class="text-fg-2">{short(id)}</span> {n.toLocaleString()}</div>{/each}
            </div>
          {/each}
        </div>
        <p class="mt-3 text-xs text-fg-2">Search engines read the raw HTML first and the rendered page later, if at all. Elements that only appear after JavaScript are a risk; if the raw HTML differs between devices, the site serves them different pages.</p>
      </div>
    {:else}
      <p class="text-xs text-fg-2">Turn on JavaScript rendering to compare the raw HTML with the rendered page on each device.</p>
    {/if}

    <div class="flex flex-wrap gap-2">
      <button type="button" class="dv-chip {filter === 'device-any' ? 'on' : ''}" onclick={() => { filter = 'device-any'; offset = 0; }}>
        Any difference <b>{(parity.compared - parity.identical).toLocaleString()}</b>
      </button>
      {#if parity.rawCompared}
        <button type="button" class="dv-chip {filter === 'device-js' ? 'on' : ''}" onclick={() => { filter = 'device-js'; offset = 0; }}>
          JavaScript changes SEO elements <b>{(parity.jsChanged ?? 0).toLocaleString()}</b>
        </button>
        <button type="button" class="dv-chip {filter === 'device-raw' ? 'on' : ''}" onclick={() => { filter = 'device-raw'; offset = 0; }}>
          Raw HTML differs <b>{(parity.rawCompared - (parity.rawIdentical ?? 0)).toLocaleString()}</b>
        </button>
      {/if}
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
                <td class="p-2"><div class="flex flex-wrap gap-1">{#each c.diffs ?? [] as d}<span class="dv-tag {SEVERE.has(d) ? 'sev' : ''}">{short(d)}</span>{/each}{#if c.primaryJs?.length || c.secondaryJs?.length}<span class="dv-tag js" title="JavaScript changes SEO elements">JS</span>{/if}</div></td>
                <td class="p-2 text-right tabular-nums">{c.primary.wordCount} / {c.secondary.wordCount}</td>
                <td class="p-2 text-right tabular-nums">{c.primary.internalLinks} / {c.secondary.internalLinks}</td>
                <td class="p-2 text-right tabular-nums">{c.textSimilarity}%</td>
              </tr>
              {#if open === p.url}
                <tr class="border-b border-border/60 bg-surface">
                  <td colspan="5" class="p-3">
                    {#if c.primary.raw && c.secondary.raw}
                      {@const cols = [
                        { dev: parity.primary, kind: 'Raw HTML', s: c.primary.raw },
                        { dev: parity.primary, kind: 'Rendered', s: c.primary },
                        { dev: parity.secondary, kind: 'Raw HTML', s: c.secondary.raw },
                        { dev: parity.secondary, kind: 'Rendered', s: c.secondary },
                      ]}
                      <div class="mb-4 overflow-x-auto" data-testid="devices-battle">
                        <table class="w-full table-fixed text-xs">
                          <thead>
                            <tr class="text-left text-fg-2"><th class="w-28 p-1"></th>{#each cols as col}<th class="p-1 font-medium"><span class="text-fg">{name(col.dev)}</span> · {col.kind}</th>{/each}</tr>
                          </thead>
                          <tbody>
                            {#each FIELDS as f (f.label)}
                              {@const v = cols.map((col) => f.get(col.s))}
                              <tr class="border-t border-border/60 align-top">
                                <td class="p-1 text-fg-2">{f.label}</td>
                                {#each v as val, i}
                                  {@const jsChanged = i % 2 === 1 && val !== v[i - 1]}
                                  {@const devDiff = i >= 2 && val !== v[i - 2]}
                                  <td class="p-1 break-words {jsChanged ? 'dv-js' : ''} {devDiff ? 'dv-dev' : ''}" title={jsChanged ? 'Changed by JavaScript' : devDiff ? 'Differs from ' + name(parity.primary) : ''}>{val || '—'}</td>
                                {/each}
                              </tr>
                            {/each}
                          </tbody>
                        </table>
                        <p class="mt-1 text-[11px] text-fg-2"><span class="dv-js px-1">blue</span> changed by JavaScript · <span class="dv-dev px-1">orange</span> differs between devices{c.primaryJsLinks || c.secondaryJsLinks ? ` · links added by JavaScript: ${c.primaryJsLinks ?? 0} on ${name(parity.primary)}, ${c.secondaryJsLinks ?? 0} on ${name(parity.secondary)}` : ''}</p>
                      </div>
                    {/if}
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
  .dv-tag.js { color: var(--color-accent); }
  .dv-js { background: color-mix(in srgb, var(--color-accent) 14%, transparent); }
  .dv-dev { box-shadow: inset 0 -2px 0 var(--color-warning); }
</style>
