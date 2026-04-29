<script lang="ts">
  let { stats }: { stats: Record<string, unknown> } = $props();

  interface AIOverviewQuery {
    query: string;
    impressions: number;
    clicks: number;
    ctr: number;
    position: number;
  }

  interface AIVisibilityEntry {
    url: string;
    aiImpressions: number;
    aiClicks: number;
    aiCtr: number;
  }

  interface AIVisibilityStats {
    pagesInAiOverview: number;
    totalAiImpressions: number;
    totalAiClicks: number;
    avgAiCtr: number;
    topByClicks: AIVisibilityEntry[];
    topQueries: AIOverviewQuery[];
  }

  let aiStats = $derived((stats.aiVisibilityStats as AIVisibilityStats) || null);
  let activeTab = $state<'pages' | 'queries'>('pages');
</script>

{#if aiStats}
  <div class="space-y-4">
    <!-- Summary cards -->
    <div class="grid grid-cols-2 sm:grid-cols-4 gap-3">
      <div class="rounded-md border border-border bg-surface-2 p-4 text-center">
        <div class="text-[10px] text-fg-2 uppercase">Pages in AI Overviews</div>
        <div class="text-2xl font-bold mt-1 text-purple-400">{aiStats.pagesInAiOverview.toLocaleString()}</div>
      </div>
      <div class="rounded-md border border-border bg-surface-2 p-4 text-center">
        <div class="text-[10px] text-fg-2 uppercase">AI Impressions</div>
        <div class="text-2xl font-bold mt-1">{aiStats.totalAiImpressions.toLocaleString()}</div>
      </div>
      <div class="rounded-md border border-border bg-surface-2 p-4 text-center">
        <div class="text-[10px] text-fg-2 uppercase">AI Clicks</div>
        <div class="text-2xl font-bold mt-1 text-green-400">{aiStats.totalAiClicks.toLocaleString()}</div>
      </div>
      <div class="rounded-md border border-border bg-surface-2 p-4 text-center">
        <div class="text-[10px] text-fg-2 uppercase">Avg AI CTR</div>
        <div class="text-2xl font-bold mt-1 {aiStats.avgAiCtr < 0.02 ? 'text-warning' : 'text-success'}">
          {(aiStats.avgAiCtr * 100).toFixed(1)}%
        </div>
      </div>
    </div>

    <!-- Tabs -->
    <div class="flex gap-1 p-1 rounded-lg bg-surface-2 border border-border">
      {#each [
        { key: 'pages', label: `Top Pages (${aiStats.topByClicks?.length || 0})` },
        { key: 'queries', label: `Top Queries (${aiStats.topQueries?.length || 0})` },
      ] as tab}
        <button
          type="button"
          class="flex-1 px-3 py-1.5 rounded text-xs font-medium transition-colors cursor-pointer {activeTab === tab.key ? 'bg-accent text-white' : 'text-fg-2 hover:text-fg-1 hover:bg-surface-3'}"
          onclick={() => activeTab = tab.key as typeof activeTab}
        >{tab.label}</button>
      {/each}
    </div>

    {#if activeTab === 'pages'}
      <div class="rounded-md border border-border bg-surface-2 overflow-hidden">
        <table class="w-full text-xs">
          <thead>
            <tr class="border-b border-border">
              <th class="text-left px-3 py-2 text-fg-2 font-medium">URL</th>
              <th class="text-right px-3 py-2 text-fg-2 font-medium w-24">Impressions</th>
              <th class="text-right px-3 py-2 text-fg-2 font-medium w-20">Clicks</th>
              <th class="text-right px-3 py-2 text-fg-2 font-medium w-16">CTR</th>
            </tr>
          </thead>
          <tbody>
            {#each (aiStats.topByClicks || []) as entry}
              <tr class="border-b border-border/50 hover:bg-surface-3">
                <td class="px-3 py-2 font-mono text-fg-2 truncate max-w-[400px]">{entry.url}</td>
                <td class="px-3 py-2 text-right">{entry.aiImpressions.toLocaleString()}</td>
                <td class="px-3 py-2 text-right font-medium text-green-400">{entry.aiClicks.toLocaleString()}</td>
                <td class="px-3 py-2 text-right">{(entry.aiCtr * 100).toFixed(1)}%</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <div class="rounded-md border border-border bg-surface-2 overflow-hidden">
        <table class="w-full text-xs">
          <thead>
            <tr class="border-b border-border">
              <th class="text-left px-3 py-2 text-fg-2 font-medium">Query</th>
              <th class="text-right px-3 py-2 text-fg-2 font-medium w-24">Impressions</th>
              <th class="text-right px-3 py-2 text-fg-2 font-medium w-20">Clicks</th>
              <th class="text-right px-3 py-2 text-fg-2 font-medium w-16">CTR</th>
              <th class="text-right px-3 py-2 text-fg-2 font-medium w-16">Position</th>
            </tr>
          </thead>
          <tbody>
            {#each (aiStats.topQueries || []) as query}
              <tr class="border-b border-border/50 hover:bg-surface-3">
                <td class="px-3 py-2 font-medium">{query.query}</td>
                <td class="px-3 py-2 text-right">{query.impressions.toLocaleString()}</td>
                <td class="px-3 py-2 text-right font-medium text-green-400">{query.clicks.toLocaleString()}</td>
                <td class="px-3 py-2 text-right">{(query.ctr * 100).toFixed(1)}%</td>
                <td class="px-3 py-2 text-right">{query.position.toFixed(1)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
{/if}
