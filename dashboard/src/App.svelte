<script lang="ts">
  import { onRouteChange, getCurrentRoute, matchRoute, type Route } from './lib/router';
  import Layout from './lib/components/layout/Layout.svelte';
  import Setup from './routes/Setup.svelte';
  import Monitor from './routes/Monitor.svelte';
  import Results from './routes/Results.svelte';
  import History from './routes/History.svelte';
  import Settings from './routes/Settings.svelte';
  import Schedules from './routes/Schedules.svelte';
  import Diff from './routes/Diff.svelte';
  import Logs from './routes/Logs.svelte';
  import LogResults from './routes/LogResults.svelte';

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

<Layout {currentPath}>
  {#if page === 'setup'}
    <Setup />
  {:else if page === 'monitor'}
    <Monitor id={routeParams.id || ''} />
  {:else if page === 'results'}
    <Results id={routeParams.id || ''} />
  {:else if page === 'diff'}
    <Diff oldId={routeParams.oldId || ''} newId={routeParams.newId || ''} />
  {:else if page === 'history'}
    <History />
  {:else if page === 'logs'}
    <Logs />
  {:else if page === 'log-results'}
    <LogResults id={routeParams.id || ''} />
  {:else if page === 'schedules'}
    <Schedules />
  {:else if page === 'settings'}
    <Settings />
  {/if}
</Layout>
