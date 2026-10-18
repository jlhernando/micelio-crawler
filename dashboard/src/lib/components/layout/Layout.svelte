<script lang="ts">
  import { onMount } from 'svelte';
  import Sidebar from './Sidebar.svelte';
  import Header from './Header.svelte';
  import UpdateBanner from '../UpdateBanner.svelte';
  import type { Snippet } from 'svelte';

  let { currentPath, children }: { currentPath: string; children: Snippet } = $props();
  let sidebarCollapsed = $state(false);
  let mobileOpen = $state(false);
  onMount(() => {
    const query = window.matchMedia('(max-width: 767px)');
    const close = () => { mobileOpen = false; };
    query.addEventListener('change', close);
    return () => query.removeEventListener('change', close);
  });
  $effect(() => { currentPath; mobileOpen = false; });
</script>

<svelte:window onkeydown={(event) => { if (event.key === 'Escape') mobileOpen = false; }} />
<a class="skip-link" href="#main-content" onclick={(event) => { event.preventDefault(); document.getElementById('main-content')?.focus(); }}>Skip to content</a>
<div class="app-shell">
  {#if mobileOpen}
    <button class="sidebar-backdrop" aria-label="Close navigation" onclick={() => mobileOpen = false}></button>
  {/if}
  <div id="main-navigation" class:mobile-open={mobileOpen} class="sidebar-container">
    <Sidebar {currentPath} collapsed={sidebarCollapsed} onToggle={() => sidebarCollapsed = !sidebarCollapsed} />
  </div>
  <div class="app-body">
    <Header {currentPath} {mobileOpen} onMenu={() => mobileOpen = !mobileOpen} />
    <UpdateBanner />
    <main id="main-content" class="workspace-content" tabindex="-1">
      <div class="workspace-page" data-page={currentPath.split('/')[1] || 'setup'}>
        {@render children()}
      </div>
    </main>
  </div>
</div>
