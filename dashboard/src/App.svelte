<script lang="ts">
  import { onRouteChange, getCurrentRoute, matchRoute, type Route } from './lib/router';
  import Layout from './lib/components/layout/Layout.svelte';
  import Setup from './routes/Setup.svelte';
  import Monitor from './routes/Monitor.svelte';
  import Results from './routes/Results.svelte';

  // Setup, Monitor and Results ship in the main bundle; the other routes are
  // split into their own chunks and fetched on first visit.
  const loadHistory = () => import('./routes/History.svelte');
  const loadSettings = () => import('./routes/Settings.svelte');
  const loadSchedules = () => import('./routes/Schedules.svelte');
  const loadDiff = () => import('./routes/Diff.svelte');
  const loadLogs = () => import('./routes/Logs.svelte');
  const loadLogResults = () => import('./routes/LogResults.svelte');

  let currentPath = $state(getCurrentRoute().path);

  onRouteChange((route: Route) => {
    currentPath = route.path;
  });

  // Derive both page name and params from the path (no state mutation)
  let routeInfo = $derived.by(() => {
    const path = currentPath;
    if (path === '/setup' || path === '/') return { page: 'setup', params: {} };

    const monitorMatch = matchRoute('/monitor/:id', path);
    if (monitorMatch) return { page: 'monitor', params: monitorMatch };

    const resultsMatch = matchRoute('/results/:id', path);
    if (resultsMatch) return { page: 'results', params: resultsMatch };

    const diffMatch = matchRoute('/diff/:oldId/:newId', path);
    if (diffMatch) return { page: 'diff', params: diffMatch };

    if (path === '/history') return { page: 'history', params: {} };
    if (path === '/logs') return { page: 'logs', params: {} };

    const logResultMatch = matchRoute('/logs/:id', path);
    if (logResultMatch) return { page: 'log-results', params: logResultMatch };

    if (path === '/schedules') return { page: 'schedules', params: {} };
    if (path === '/settings') return { page: 'settings', params: {} };
    return { page: 'setup', params: {} };
  });

  let page = $derived(routeInfo.page);
  let routeParams = $derived(routeInfo.params);
</script>

{#snippet loadFailed(err: unknown)}
  <div class="max-w-xl mx-auto mt-16 rounded-md border border-border bg-surface-2 p-5 text-sm">
    <p class="text-danger mb-2">This page failed to load.</p>
    <p class="text-fg-2 mb-4">{(err as Error)?.message || 'Network error'}. The dashboard may have been updated; reload to fetch the new version.</p>
    <button type="button" class="px-3 py-1.5 rounded-lg bg-accent text-surface font-medium cursor-pointer" onclick={() => location.reload()}>Reload</button>
  </div>
{/snippet}

<Layout {currentPath}>
  {#if page === 'setup'}
    <Setup />
  {:else if page === 'monitor'}
    <Monitor id={routeParams.id || ''} />
  {:else if page === 'results'}
    <Results id={routeParams.id || ''} />
  {:else if page === 'diff'}
    {#await loadDiff() then { default: Diff }}
      <Diff oldId={routeParams.oldId || ''} newId={routeParams.newId || ''} />
    {:catch err}{@render loadFailed(err)}{/await}
  {:else if page === 'history'}
    {#await loadHistory() then { default: History }}<History />{:catch err}{@render loadFailed(err)}{/await}
  {:else if page === 'logs'}
    {#await loadLogs() then { default: Logs }}<Logs />{:catch err}{@render loadFailed(err)}{/await}
  {:else if page === 'log-results'}
    {#await loadLogResults() then { default: LogResults }}
      <LogResults id={routeParams.id || ''} />
    {:catch err}{@render loadFailed(err)}{/await}
  {:else if page === 'schedules'}
    {#await loadSchedules() then { default: Schedules }}<Schedules />{:catch err}{@render loadFailed(err)}{/await}
  {:else if page === 'settings'}
    {#await loadSettings() then { default: Settings }}<Settings />{:catch err}{@render loadFailed(err)}{/await}
  {/if}
</Layout>
