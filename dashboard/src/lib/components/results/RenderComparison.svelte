<script lang="ts">
  interface CriticalDiff { url: string; field: string; original: string; rendered: string; }
  interface RenderCompareStats {
    fieldDiffCounts: Record<string, number>;
    criticalDiffs: CriticalDiff[] | null;
    pagesCompared: number;
    pagesWithDiffs: number;
  }

  let { stats }: { stats: Record<string, unknown> } = $props();

  let rc = $derived((stats.renderCompareStats as RenderCompareStats | undefined) ?? null);
  let fields = $derived(rc ? Object.entries(rc.fieldDiffCounts ?? {}).sort((a, b) => b[1] - a[1]) : []);
  let maxField = $derived(Math.max(1, ...fields.map(([, n]) => n)));
  let critical = $derived(rc?.criticalDiffs ?? []);

  const FIELD_LABELS: Record<string, string> = {
    title: 'Title',
    meta_description: 'Meta description',
    canonical: 'Canonical',
    meta_robots: 'Meta robots',
    metaRobots: 'Meta robots',
    h1: 'H1',
    internal_links_count: 'Internal links',
    word_count: 'Word count',
    json_ld_count: 'Structured data blocks',
  };
  const CRITICAL = new Set(['title', 'canonical', 'meta_robots', 'metaRobots']);

  function label(field: string): string {
    return FIELD_LABELS[field] ?? field;
  }
  function show(value: string): string {
    return value === '' ? '(empty)' : value;
  }
</script>

{#if rc}
  <section class="rounded-md border border-border bg-surface-2 p-5" aria-labelledby="render-compare-heading">
    <div class="flex flex-wrap items-baseline justify-between gap-2 mb-1">
      <h3 id="render-compare-heading" class="text-sm font-medium text-fg-2">JavaScript rendering differences</h3>
      <span class="text-xs text-fg-2">
        {rc.pagesWithDiffs.toLocaleString()} of {rc.pagesCompared.toLocaleString()} rendered pages change after JavaScript runs
      </span>
    </div>
    <p class="text-xs text-fg-2 mb-4">
      Compares the HTML the server sends with the page after JavaScript runs. Search engines may index either version, so changes to title, canonical or meta robots are the riskiest.
    </p>

    {#if fields.length > 0}
      <div class="space-y-2 mb-4">
        {#each fields as [field, count] (field)}
          <div class="flex items-center gap-3 text-xs">
            <span class="w-40 shrink-0 {CRITICAL.has(field) ? 'text-danger font-medium' : 'text-fg-2'}">{label(field)}</span>
            <div class="flex-1 h-2 rounded-full bg-surface-3">
              <div class="h-2 rounded-full {CRITICAL.has(field) ? 'bg-danger' : 'bg-accent'}" style="width: {(count / maxField) * 100}%"></div>
            </div>
            <span class="w-14 text-right tabular-nums">{count.toLocaleString()}</span>
          </div>
        {/each}
      </div>
    {:else}
      <p class="text-sm text-fg-2 mb-2">No differences: JavaScript does not change the SEO elements on the rendered pages.</p>
    {/if}

    {#if critical.length > 0}
      <div class="text-xs text-fg-2 mb-2">Critical changes ({critical.length.toLocaleString()})</div>
      <div class="overflow-x-auto max-h-72 overflow-y-auto">
        <table class="w-full min-w-[640px] text-xs">
          <thead>
            <tr class="text-[11px] text-fg-2 border-b border-border">
              <th class="text-left pb-2 font-medium">Page</th>
              <th class="text-left pb-2 font-medium">Element</th>
              <th class="text-left pb-2 font-medium">Server HTML</th>
              <th class="text-left pb-2 font-medium">After JavaScript</th>
            </tr>
          </thead>
          <tbody>
            {#each critical.slice(0, 100) as d, i (d.url + d.field + i)}
              <tr class="border-b border-border last:border-0 align-top">
                <td class="py-1.5 pr-3 font-mono text-fg-2 max-w-64 truncate" title={d.url}>{d.url}</td>
                <td class="py-1.5 pr-3 text-danger whitespace-nowrap">{label(d.field)}</td>
                <td class="py-1.5 pr-3 max-w-56 truncate" title={d.original}>{show(d.original)}</td>
                <td class="py-1.5 max-w-56 truncate" title={d.rendered}>{show(d.rendered)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
{/if}
