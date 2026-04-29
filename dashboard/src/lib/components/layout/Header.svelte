<script lang="ts">
  let { currentPath }: { currentPath: string } = $props();

  function getTitle(path: string): string {
    if (path === '/setup' || path === '/') return 'New Crawl';
    if (path.startsWith('/monitor')) return 'Crawl Monitor';
    if (path.startsWith('/results')) return 'Results';
    if (path.startsWith('/diff')) return 'Diff Comparison';
    if (path === '/history') return 'Crawl History';
    if (path === '/logs') return 'Log Analysis';
    if (path.startsWith('/logs/')) return 'Log Results';
    if (path === '/settings') return 'Settings';
    return 'Micelio';
  }

  let title = $derived(getTitle(currentPath));
  let crawlId = $derived(currentPath.startsWith('/results/') ? currentPath.split('/')[2] : null);

  // Fetch domain and config for results pages
  let domain = $state<string | null>(null);
  let crawlConfig = $state<Record<string, unknown> | null>(null);
  let lastFetchedId = $state<string | null>(null);
  let recrawling = $state(false);
  $effect(() => {
    if (crawlId && crawlId !== lastFetchedId) {
      lastFetchedId = crawlId;
      fetch(`/api/crawl/${crawlId}/status`)
        .then(r => r.json())
        .then(job => {
          try { domain = new URL(job.seedUrl).hostname; } catch { domain = job.seedUrl; }
          crawlConfig = job.config;
        })
        .catch(() => { domain = null; crawlConfig = null; });
    } else if (!crawlId) {
      domain = null;
      crawlConfig = null;
    }
  });

  async function recrawl() {
    if (!crawlConfig || recrawling) return;
    recrawling = true;
    try {
      const res = await fetch('/api/crawl/start', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(crawlConfig),
      });
      const { id } = await res.json();
      window.location.hash = `/monitor/${id}`;
    } catch {
      recrawling = false;
    }
  }
</script>

<header class="h-14 flex items-center justify-between px-6 border-b border-border bg-surface-2 shrink-0">
  <div class="flex items-center gap-2">
    <h1 class="text-lg font-semibold">{title}</h1>
    {#if domain}
      <span class="text-fg-2">/</span>
      <span class="text-sm font-medium text-fg-2">{domain}</span>
    {/if}
  </div>
  <div class="flex items-center gap-3">
    {#if crawlId}
      <a
        href="/api/crawl/{crawlId}/export/csv"
        download
        class="inline-flex items-center gap-2 px-4 py-2 rounded-md border border-border bg-surface-3 text-fg text-sm font-medium hover:bg-surface-2 transition-all no-underline"
      >
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"></path>
          <polyline points="7 10 12 15 17 10"></polyline>
          <line x1="12" y1="15" x2="12" y2="3"></line>
        </svg>
        Export CSV
      </a>
    {/if}
    {#if crawlId && crawlConfig}
      <button
        type="button"
        class="inline-flex items-center gap-2 px-4 py-2 rounded-md bg-success/90 text-white text-sm font-medium hover:bg-success transition-all cursor-pointer disabled:opacity-50"
        onclick={recrawl}
        disabled={recrawling}
      >
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M1 4v6h6"></path>
          <path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"></path>
        </svg>
        {recrawling ? 'Starting...' : 'Recrawl'}
      </button>
    {/if}
    <a
      href="#/setup"
      class="inline-flex items-center gap-2 px-4 py-2 rounded-md bg-accent-emphasis text-white text-sm font-medium hover:brightness-110 transition-all no-underline"
    >
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M12 4v16m8-8H4"></path>
      </svg>
      New Crawl
    </a>
  </div>
</header>
