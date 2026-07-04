<script lang="ts">
  import { untrack, type Snippet } from 'svelte';

  let { title, open = false, children }: {
    title: string;
    open?: boolean;
    children: Snippet;
  } = $props();

  // Capture only the initial `open` value; later prop changes don't reset it.
  let isOpen = $state(untrack(() => open));
</script>

<div class="rounded-lg border border-border bg-surface-2 overflow-hidden">
  <button
    class="w-full flex items-center justify-between px-4 py-3 text-sm font-medium text-fg hover:bg-surface-3 transition-colors cursor-pointer"
    onclick={() => isOpen = !isOpen}
    type="button"
    aria-expanded={isOpen}
  >
    <span>{title}</span>
    <svg
      xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24"
      fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
      class="transition-transform duration-200 {isOpen ? 'rotate-180' : ''}"
    >
      <polyline points="6 9 12 15 18 9"></polyline>
    </svg>
  </button>
  {#if isOpen}
    <div class="px-4 pb-4 pt-1 border-t border-border space-y-4">
      {@render children()}
    </div>
  {/if}
</div>
