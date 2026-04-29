<script lang="ts">
  import { untrack } from 'svelte';

  let { message, type = 'success', onDismiss }: {
    message: string;
    type?: 'success' | 'error';
    onDismiss: () => void;
  } = $props();

  $effect(() => {
    const dismiss = untrack(() => onDismiss);
    const timer = setTimeout(dismiss, 3500);
    return () => clearTimeout(timer);
  });
</script>

<div
  role="alert"
  class="fixed top-4 right-4 z-50 max-w-sm px-4 py-3 rounded-xl border shadow-lg text-sm font-medium animate-slide-in
    {type === 'success' ? 'bg-green-500/15 border-green-500/30 text-green-400' : 'bg-red-500/15 border-red-500/30 text-red-400'}"
>
  <div class="flex items-center gap-2">
    {#if type === 'success'}
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M20 6 9 17l-5-5"></path>
      </svg>
    {:else}
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <circle cx="12" cy="12" r="10"></circle>
        <line x1="15" y1="9" x2="9" y2="15"></line>
        <line x1="9" y1="9" x2="15" y2="15"></line>
      </svg>
    {/if}
    <span>{message}</span>
    <button
      type="button"
      class="ml-auto text-current opacity-60 hover:opacity-100 cursor-pointer"
      onclick={onDismiss}
      aria-label="Close"
    >
      <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <line x1="18" y1="6" x2="6" y2="18"></line>
        <line x1="6" y1="6" x2="18" y2="18"></line>
      </svg>
    </button>
  </div>
</div>

<style>
  @keyframes slide-in {
    from { transform: translateX(100%); opacity: 0; }
    to { transform: translateX(0); opacity: 1; }
  }
  .animate-slide-in {
    animation: slide-in 0.25s ease-out;
  }
</style>
