<script lang="ts">
  import Sidebar from './Sidebar.svelte';
  import Header from './Header.svelte';
  import type { Snippet } from 'svelte';

  let { currentPath, children }: { currentPath: string; children: Snippet } = $props();
  let sidebarCollapsed = $state(false);
</script>

<div class="flex h-screen overflow-hidden">
  <Sidebar {currentPath} collapsed={sidebarCollapsed} onToggle={() => sidebarCollapsed = !sidebarCollapsed} />
  <div class="flex-1 flex flex-col min-w-0">
    <Header {currentPath} />
    <main class="flex-1 overflow-auto p-6">
      {@render children()}
    </main>
  </div>
</div>
