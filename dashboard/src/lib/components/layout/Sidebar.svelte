<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../../api';

  let { currentPath, collapsed, onToggle }: {
    currentPath: string;
    collapsed: boolean;
    onToggle: () => void;
  } = $props();

  interface ActiveCrawl {
    id: string;
    seedUrl: string;
    status: string;
    pageCount: number;
  }

  let activeCrawls = $state<ActiveCrawl[]>([]);
  let pollInterval: ReturnType<typeof setInterval> | null = null;

  async function fetchActiveCrawls() {
    try {
      const data = await api.listCrawls();
      activeCrawls = (data.crawls || [])
        .filter((c: { status: string }) => c.status === 'running' || c.status === 'paused')
        .map((c: { id: string; seedUrl: string; status: string; pageCount: number }) => ({
          id: c.id,
          seedUrl: c.seedUrl,
          status: c.status,
          pageCount: c.pageCount,
        }));
    } catch {
      // silent — sidebar should never break
    }
  }

  onMount(() => {
    fetchActiveCrawls();
    pollInterval = setInterval(fetchActiveCrawls, 10_000);
    // Instant refresh when a crawl state changes (stop/cancel/complete)
    window.addEventListener('crawl-state-change', fetchActiveCrawls);
  });

  onDestroy(() => {
    if (pollInterval) clearInterval(pollInterval);
    window.removeEventListener('crawl-state-change', fetchActiveCrawls);
  });

  function extractDomain(url: string): string {
    try {
      return new URL(url).hostname.replace(/^www\./, '');
    } catch {
      return url;
    }
  }

  const navItems = [
    { path: '/setup', label: 'New Crawl', icon: 'M12 4v16m8-8H4' },
    { path: '/history', label: 'History', icon: 'M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z' },
    { path: '/schedules', label: 'Schedules', icon: 'M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z' },
    { path: '/logs', label: 'Log Analysis', icon: 'M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z' },
    { path: '/settings', label: 'Settings', icon: 'M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z M15 12a3 3 0 11-6 0 3 3 0 016 0z' },
  ];

  function isActive(itemPath: string, current: string): boolean {
    if (itemPath === '/setup') return current === '/setup' || current === '/';
    if (itemPath === '/history') return current === '/history' || current.startsWith('/results') || current.startsWith('/monitor');
    return current.startsWith(itemPath);
  }
</script>

<aside class="h-full flex flex-col border-r border-border bg-surface-2 transition-all duration-200 {collapsed ? 'w-16' : 'w-56'}">
  <!-- Logo -->
  <div class="flex items-center gap-3 px-4 h-14 border-b border-border">
    <button onclick={onToggle} class="text-fg-2 hover:text-fg transition-colors cursor-pointer" aria-label="Toggle sidebar">
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <line x1="3" y1="12" x2="21" y2="12"></line>
        <line x1="3" y1="6" x2="21" y2="6"></line>
        <line x1="3" y1="18" x2="21" y2="18"></line>
      </svg>
    </button>
    {#if !collapsed}
      <img src="/micelio-icon.jpg" alt="Micelio" width="28" height="28" class="rounded-md" />
      <span class="text-lg font-bold text-accent">Micelio</span>
    {/if}
  </div>

  <!-- Active Crawls -->
  {#if activeCrawls.length > 0}
    <div class="px-2 pt-3 pb-1 border-b border-border">
      {#if !collapsed}
        <div class="flex items-center gap-2 px-2 mb-2">
          <span class="relative flex h-2 w-2">
            <span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-blue-400 opacity-75"></span>
            <span class="relative inline-flex rounded-full h-2 w-2 bg-blue-500"></span>
          </span>
          <span class="text-xs font-medium text-fg-2 uppercase tracking-wider">Active ({activeCrawls.length})</span>
        </div>
        {#each activeCrawls as crawl}
          <a
            href="#/monitor/{crawl.id}"
            class="flex flex-col gap-1 px-2 py-2 rounded-md text-sm transition-colors no-underline hover:bg-surface-3
              {currentPath === `/monitor/${crawl.id}` ? 'bg-surface-3' : ''}"
          >
            <div class="flex items-center justify-between">
              <span class="font-medium text-fg truncate text-xs">{extractDomain(crawl.seedUrl)}</span>
              <span class="px-1.5 py-0.5 rounded text-[10px] font-medium {crawl.status === 'running' ? 'bg-blue-500/20 text-blue-400' : 'bg-yellow-500/20 text-yellow-400'}">
                {crawl.status === 'running' ? 'Live' : 'Paused'}
              </span>
            </div>
            <span class="text-[11px] text-fg-2 font-mono">{crawl.pageCount.toLocaleString()} pages</span>
          </a>
        {/each}
      {:else}
        <a
          href="#/history"
          class="flex flex-col items-center gap-0.5 px-1 py-2 rounded-md hover:bg-surface-3 transition-colors no-underline"
          title="Active crawls: {activeCrawls.length}"
        >
          <span class="relative flex h-2.5 w-2.5">
            <span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-blue-400 opacity-75"></span>
            <span class="relative inline-flex rounded-full h-2.5 w-2.5 bg-blue-500"></span>
          </span>
          <span class="text-[10px] font-bold text-blue-400 mt-0.5">{activeCrawls.length}</span>
        </a>
      {/if}
    </div>
  {/if}

  <!-- Navigation -->
  <nav class="flex-1 py-3 flex flex-col gap-1 px-2">
    {#each navItems as item}
      <a
        href="#{item.path}"
        class="flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors no-underline
          {isActive(item.path, currentPath) ? 'bg-surface-3 text-fg font-medium border-l-2 border-accent' : 'text-fg-2 hover:text-fg hover:bg-surface-3 border-l-2 border-transparent'}"
        title={collapsed ? item.label : undefined}
      >
        <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0">
          <path d={item.icon}></path>
        </svg>
        {#if !collapsed}
          <span>{item.label}</span>
        {/if}
      </a>
    {/each}
  </nav>

  <!-- Version -->
  {#if !collapsed}
    <div class="px-4 py-3 text-xs text-fg-2 border-t border-border">
      Micelio v1.0
    </div>
  {/if}
</aside>
