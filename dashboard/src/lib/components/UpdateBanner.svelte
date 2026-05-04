<script lang="ts">
  import { onMount } from 'svelte';
  import { updates } from '../stores/updates.svelte';
  import Toast from './ui/Toast.svelte';

  let toast = $state<{ message: string; type: 'success' | 'error' } | null>(null);
  let installed = $state(false);

  onMount(() => { updates.load(); });

  async function install() {
    if (updates.installing || !updates.status?.downloadable) return;
    try {
      const res = await updates.install();
      if (res?.installed) {
        installed = true;
        toast = {
          message: `Updated to ${updates.status?.current ?? ''}. Restart Micelio to use it.`,
          type: 'success',
        };
      }
    } catch (err) {
      toast = { message: `Update failed: ${(err as Error).message}`, type: 'error' };
    }
  }

  async function rollback() {
    try {
      await updates.rollback();
      installed = false;
      toast = { message: 'Rolled back to previous version. Restart Micelio to use it.', type: 'success' };
    } catch (err) {
      toast = { message: `Rollback failed: ${(err as Error).message}`, type: 'error' };
    }
  }
</script>

{#if toast}
  <Toast message={toast.message} type={toast.type} onDismiss={() => toast = null} />
{/if}

{#if installed && updates.status?.canRollback}
  <div
    role="status"
    class="flex items-center gap-3 px-6 py-2 border-b border-green-500/30 bg-green-500/10 text-sm text-fg shrink-0"
  >
    <span class="flex-1 min-w-0 truncate">
      Updated to <span class="font-medium">{updates.status?.current}</span>. Restart Micelio to use it.
    </span>
    <button
      type="button"
      class="px-3 py-1 rounded-md bg-surface-3 hover:bg-surface-2 text-xs font-medium cursor-pointer disabled:opacity-50"
      onclick={rollback}
      disabled={updates.installing}
    >{updates.installing ? 'Rolling back...' : 'Rollback'}</button>
  </div>
{:else if updates.shouldShowBanner}
  <div
    role="status"
    class="flex items-center gap-3 px-6 py-2 border-b border-accent/30 bg-accent/10 text-sm text-fg shrink-0"
  >
    <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="text-accent shrink-0" aria-hidden="true">
      <path d="M12 5v14"></path>
      <path d="m19 12-7 7-7-7"></path>
    </svg>
    <span class="flex-1 min-w-0 truncate">
      Update available
      <span class="text-fg-2 mx-1">&middot;</span>
      <span class="font-medium">{updates.status?.current}</span>
      <span class="text-fg-2 mx-1">&rarr;</span>
      <span class="font-medium">{updates.status?.latest}</span>
      {#if updates.status?.releaseUrl}
        <a
          href={updates.status.releaseUrl}
          target="_blank"
          rel="noopener noreferrer"
          class="text-accent hover:underline ml-2"
        >Release notes</a>
      {/if}
    </span>
    <button
      type="button"
      class="px-3 py-1 rounded-md bg-accent text-white text-xs font-medium hover:brightness-110 cursor-pointer disabled:opacity-50 disabled:cursor-default"
      onclick={install}
      disabled={updates.installing || !updates.status?.downloadable}
      title={updates.status?.downloadable ? 'Download, verify checksum, and install' : 'No matching binary in this release'}
    >{updates.installing ? 'Installing...' : 'Install update'}</button>
    <button
      type="button"
      class="text-fg-2 hover:text-fg cursor-pointer"
      onclick={() => updates.dismiss()}
      aria-label="Dismiss"
      title="Dismiss until next session"
    >
      <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <line x1="18" y1="6" x2="6" y2="18"></line>
        <line x1="6" y1="6" x2="18" y2="18"></line>
      </svg>
    </button>
  </div>
{/if}
