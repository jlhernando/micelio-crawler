<script lang="ts">
  interface Code { code: string; pages: number; valid: boolean; hint?: string }
  interface Sample { url: string; lang?: string; target?: string; source?: string; detail?: string }
  interface Group { id: string; label: string; severity: 'error' | 'warning' | 'info'; count: number; pages: number; samples: Sample[] }
  interface Hreflang {
    pagesWithHreflang: number; bySource: Record<string, number>; crawledPages: number; sitemapOnlyPages?: number;
    annotations: number; targets: number; codes: Code[]; xDefaultPages: number; issues: Group[]; sitemapTruncated?: boolean;
  }

  let { stats }: { stats: Record<string, unknown> } = $props();
  let h = $derived(stats.hreflang as Hreflang | undefined);

  const SOURCES: Record<string, string> = { html: 'HTML <head>', header: 'HTTP Link header', sitemap: 'XML sitemap' };
  const SEV_ORDER = { error: 0, warning: 1, info: 2 } as const;

  let issue = $state('all');
  let source = $state('all');
  let search = $state('');
  let showAllCodes = $state(false);

  let groups = $derived(h?.issues ?? []);
  let rows = $derived.by(() => {
    const q = search.trim().toLowerCase();
    const out: (Sample & { group: Group })[] = [];
    for (const g of groups) {
      if (issue !== 'all' && g.id !== issue) continue;
      for (const s of g.samples ?? []) {
        if (source !== 'all' && !(s.source ?? '').split('+').includes(source)) continue;
        if (q && !`${s.url} ${s.target ?? ''} ${s.lang ?? ''} ${s.detail ?? ''}`.toLowerCase().includes(q)) continue;
        out.push({ ...s, group: g });
      }
    }
    return out.sort((a, b) => SEV_ORDER[a.group.severity] - SEV_ORDER[b.group.severity]);
  });
  let invalid = $derived((h?.codes ?? []).filter((c) => !c.valid));
  let codes = $derived(showAllCodes ? (h?.codes ?? []) : (h?.codes ?? []).slice(0, 30));
  let truncatedGroups = $derived(groups.filter((g) => g.count > (g.samples?.length ?? 0)).length);
  let errorPages = $derived(groups.filter((g) => g.severity === 'error').reduce((n, g) => n + g.pages, 0));

  const path = (u?: string) => { if (!u) return ''; try { const x = new URL(u); return x.pathname + x.search; } catch { return u; } };
</script>

{#if !h}
  <div class="rounded-md border border-border bg-surface-2 p-5 text-sm text-fg-2">
    No page in this crawl declares hreflang, in the HTML, the HTTP Link header or the XML sitemaps. Sitemap alternates are read when the crawl uses <b>Discover sitemaps</b> or sitemap mode.
  </div>
{:else}
  <div class="space-y-4" data-testid="hreflang-view">
    <div class="rounded-md border border-border bg-surface-2 p-5">
      <div class="text-xs uppercase tracking-wider text-fg-2">Hreflang</div>
      <div class="mt-4 grid grid-cols-2 gap-3 md:grid-cols-5">
        <div><div class="text-xs text-fg-2">Pages with hreflang</div><div class="text-2xl font-bold">{h.pagesWithHreflang.toLocaleString()}</div>
          {#if h.sitemapOnlyPages}<div class="text-xs text-fg-2">{h.sitemapOnlyPages.toLocaleString()} only in the sitemap</div>{/if}</div>
        <div><div class="text-xs text-fg-2">Language/region codes</div><div class="text-2xl font-bold">{h.codes.length}</div>
          {#if invalid.length}<div class="text-xs text-danger">{invalid.length} invalid</div>{/if}</div>
        <div><div class="text-xs text-fg-2">x-default</div><div class="text-2xl font-bold">{h.xDefaultPages.toLocaleString()}</div><div class="text-xs text-fg-2">pages declare it</div></div>
        <div><div class="text-xs text-fg-2">Alternate URLs</div><div class="text-2xl font-bold">{h.targets.toLocaleString()}</div><div class="text-xs text-fg-2">{h.annotations.toLocaleString()} links checked</div></div>
        <div><div class="text-xs text-fg-2">Pages with errors</div><div class="text-2xl font-bold {errorPages ? 'text-danger' : ''}">{errorPages.toLocaleString()}</div><div class="text-xs text-fg-2">summed per issue</div></div>
      </div>
      <div class="mt-4 flex flex-wrap gap-2 text-xs">
        {#each Object.entries(SOURCES) as [k, label] (k)}
          {#if h.bySource?.[k]}<span class="hl-tag">{label} <b>{h.bySource[k].toLocaleString()}</b></span>{/if}
        {/each}
      </div>
      {#if h.sitemapTruncated}
        <p class="mt-3 text-xs text-warning">The sitemaps declare more alternates than Micelio keeps (2 million URLs); the rest were not checked.</p>
      {/if}
    </div>

    <div class="rounded-md border border-border bg-surface-2 p-5">
      <div class="mb-3 text-sm font-medium">Codes</div>
      <div class="flex flex-wrap gap-1.5 text-xs">
        {#each codes as c (c.code)}
          <span class="hl-tag {c.valid ? '' : 'bad'}" title={c.valid ? `${c.pages} pages` : (c.hint ?? 'invalid')}>{c.code} <b>{c.pages.toLocaleString()}</b></span>
        {/each}
        {#if h.codes.length > 30}
          <button type="button" class="hl-chip" onclick={() => (showAllCodes = !showAllCodes)}>{showAllCodes ? 'Fewer' : `All ${h.codes.length}`}</button>
        {/if}
      </div>
      {#if invalid.length}
        <ul class="mt-3 space-y-0.5 text-xs text-fg-2">
          {#each invalid.slice(0, 10) as c (c.code)}<li><span class="font-mono text-danger">{c.code}</span> — {c.hint}</li>{/each}
        </ul>
      {/if}
    </div>

    {#if groups.length === 0}
      <div class="rounded-md border border-border bg-surface-2 p-5 text-sm text-fg-2">No hreflang problems found.</div>
    {:else}
      <div class="flex flex-wrap gap-2">
        <button type="button" class="hl-chip {issue === 'all' ? 'on' : ''}" onclick={() => (issue = 'all')}>All issues</button>
        {#each groups as g (g.id)}
          <button type="button" class="hl-chip {issue === g.id ? 'on' : ''} {g.severity}" title="{g.count.toLocaleString()} occurrences" onclick={() => (issue = g.id)}>
            {g.label} <b>{g.pages.toLocaleString()}</b>
          </button>
        {/each}
      </div>
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <span class="text-fg-2">Source</span>
        {#each ['all', 'html', 'header', 'sitemap'] as s (s)}
          <button type="button" class="hl-chip {source === s ? 'on' : ''}" onclick={() => (source = s)}>{s === 'all' ? 'All' : SOURCES[s]}</button>
        {/each}
        <input class="ml-auto rounded-md border border-border bg-surface px-2 py-1 text-xs" placeholder="Filter by URL, code or detail" bind:value={search} />
      </div>

      <div class="rounded-md border border-border bg-surface-2 overflow-x-auto">
        <table class="w-full text-xs">
          <thead class="text-fg-2 text-left">
            <tr class="border-b border-border">
              <th class="p-2 font-medium">Issue</th>
              <th class="p-2 font-medium">Page</th>
              <th class="p-2 font-medium">Code</th>
              <th class="p-2 font-medium">Alternate URL</th>
              <th class="p-2 font-medium">Source</th>
              <th class="p-2 font-medium">Detail</th>
            </tr>
          </thead>
          <tbody>
            {#each rows as r, i (i)}
              <tr class="border-b border-border/60 hover:bg-surface-3">
                <td class="p-2 whitespace-nowrap"><span class="hl-sev {r.group.severity}"></span>{r.group.label}</td>
                <td class="p-2 font-mono max-w-[280px] truncate" title={r.url}><a href={r.url} target="_blank" rel="noreferrer" class="hover:text-fg">{path(r.url)}</a></td>
                <td class="p-2 font-mono">{r.lang ?? ''}</td>
                <td class="p-2 font-mono max-w-[280px] truncate" title={r.target}>{path(r.target)}</td>
                <td class="p-2 text-fg-2">{r.source ?? ''}</td>
                <td class="p-2 text-fg-2 max-w-[320px] truncate" title={r.detail}>{r.detail ?? ''}</td>
              </tr>
            {:else}
              <tr><td colspan="6" class="p-4 text-center text-fg-2">No examples match these filters.</td></tr>
            {/each}
          </tbody>
        </table>
        {#if truncatedGroups}
          <p class="p-2 text-xs text-fg-2">Up to 50 examples are kept per issue; the counts above include every occurrence.</p>
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .hl-chip { display: inline-flex; align-items: center; gap: 6px; padding: 4px 10px; border-radius: 8px; border: 1px solid var(--color-border); background: var(--color-surface-2); color: var(--color-fg-2); font-size: 12px; cursor: pointer; }
  .hl-chip:hover { border-color: var(--color-fg-2); color: var(--color-fg); }
  .hl-chip.on { border-color: var(--color-accent); color: var(--color-fg); background: var(--color-surface-3); }
  .hl-chip.error b { color: var(--color-danger); }
  .hl-chip.warning b { color: var(--color-warning); }
  .hl-tag { padding: 2px 7px; border-radius: 6px; background: var(--color-surface-3); color: var(--color-fg-2); white-space: nowrap; font-family: var(--font-mono, monospace); }
  .hl-tag b { color: var(--color-fg); font-weight: 600; }
  .hl-tag.bad { color: var(--color-danger); }
  .hl-sev { display: inline-block; width: 6px; height: 6px; border-radius: 50%; margin-right: 6px; vertical-align: middle; background: var(--color-fg-2); }
  .hl-sev.error { background: var(--color-danger); }
  .hl-sev.warning { background: var(--color-warning); }
</style>
