<script lang="ts">
  interface ScoreEntry { url: string; score: number; }

  let { liStats }: { liStats: Record<string, unknown> } = $props();

  let positions = $derived((liStats.linkPositionDistribution as Record<string, number> | null) ?? {});
  let mostConnected = $derived((liStats.mostConnected as ScoreEntry[] | null) ?? []);
  let mostIsolated = $derived((liStats.mostIsolated as ScoreEntry[] | null) ?? []);
  let noContent = $derived((liStats.pagesWithNoContentLinks as string[] | null) ?? []);
  let noContentCount = $derived((liStats.pagesWithNoContentLinksCount as number | undefined) ?? noContent.length);

  // Content-area zones first, then site chrome.
  const ZONES: { key: string; label: string; color: string }[] = [
    { key: 'content', label: 'Content', color: '#3fb950' },
    { key: 'related', label: 'Related', color: '#2ea043' },
    { key: 'comments', label: 'Comments', color: '#56d364' },
    { key: 'breadcrumb', label: 'Breadcrumb', color: '#58a6ff' },
    { key: 'navigation', label: 'Navigation', color: '#d29922' },
    { key: 'header', label: 'Header', color: '#e3b341' },
    { key: 'sidebar', label: 'Sidebar', color: '#f97316' },
    { key: 'footer', label: 'Footer', color: '#db6d28' },
    { key: 'other', label: 'Other', color: '#8b949e' },
  ];
  let zones = $derived(ZONES.map((z) => ({ ...z, count: positions[z.key] ?? 0 })).filter((z) => z.count > 0));
  let totalLinks = $derived(zones.reduce((sum, z) => sum + z.count, 0));
  let contentShare = $derived(
    totalLinks ? ((positions.content ?? 0) + (positions.related ?? 0) + (positions.comments ?? 0)) / totalLinks : 0,
  );

  function pct(n: number): string {
    return totalLinks ? `${((n / totalLinks) * 100).toFixed(1)}%` : '0%';
  }
</script>

{#if totalLinks > 0 || mostConnected.length > 0 || mostIsolated.length > 0}
  <section class="rounded-md border border-border bg-surface-2 p-5 space-y-5" aria-labelledby="link-structure-heading">
    <h3 id="link-structure-heading" class="text-sm font-medium text-fg-2">Link Structure</h3>

    {#if totalLinks > 0}
      <div>
        <div class="flex flex-wrap items-baseline justify-between gap-2 mb-2">
          <div class="text-xs text-fg-2">Where internal links sit on the page</div>
          <div class="text-xs text-fg-2">
            <span class="font-semibold text-fg">{(contentShare * 100).toFixed(0)}%</span> in content, related posts or comments
          </div>
        </div>
        <div class="flex h-3 w-full overflow-hidden rounded-full bg-surface-3" role="img" aria-label="Internal links by page zone">
          {#each zones as z (z.key)}
            <div style="width: {(z.count / totalLinks) * 100}%; background: {z.color}" title="{z.label}: {z.count.toLocaleString()} ({pct(z.count)})"></div>
          {/each}
        </div>
        <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-fg-2">
          {#each zones as z (z.key)}
            <span class="inline-flex items-center gap-1.5">
              <span class="inline-block h-2 w-2 rounded-full" style="background: {z.color}"></span>
              {z.label} <span class="tabular-nums text-fg">{z.count.toLocaleString()}</span> ({pct(z.count)})
            </span>
          {/each}
        </div>
      </div>
    {/if}

    {#if noContentCount > 0}
      <div>
        <div class="text-xs text-fg-2 mb-1">
          <span class="font-semibold text-warning">{noContentCount.toLocaleString()}</span>
          pages only get links from navigation, header, footer or sidebar, never from page content
        </div>
        <div class="space-y-0.5 max-h-32 overflow-y-auto">
          {#each noContent.slice(0, 50) as url (url)}
            <div class="font-mono text-xs text-fg-2 truncate px-2 py-0.5 rounded hover:bg-surface-3" title={url}>{url}</div>
          {/each}
        </div>
      </div>
    {/if}

    {#if mostConnected.length > 0 || mostIsolated.length > 0}
      <div class="grid gap-4 md:grid-cols-2">
        {#each [{ title: 'Most connected', hint: 'Most links in and out', list: mostConnected }, { title: 'Least connected', hint: 'Fewest links in and out (orphans excluded)', list: mostIsolated }] as col (col.title)}
          <div>
            <div class="flex items-baseline justify-between mb-1">
              <div class="text-xs text-fg-2">{col.title}</div>
              <div class="text-[11px] text-fg-2">{col.hint}</div>
            </div>
            <div class="space-y-0.5 max-h-64 overflow-y-auto">
              {#each col.list as e (e.url)}
                <div class="flex items-center gap-2 px-2 py-0.5 rounded hover:bg-surface-3 text-xs">
                  <span class="flex-1 truncate font-mono text-fg-2" title={e.url}>{e.url}</span>
                  <span class="shrink-0 tabular-nums">{e.score.toLocaleString()} links</span>
                </div>
              {/each}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </section>
{/if}
