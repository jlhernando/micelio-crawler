<script lang="ts">
  // Stops the work that runs after the results are shown: the external link
  // check and page weight sizing (network) and link intelligence /
  // embeddings (analysis).
  // "stop" = POST /stop: ends only the network work, the analysis goes on.
  //   Offered only while network work runs AND link analysis follows it;
  //   otherwise stopping and cancelling would do the same thing.
  // "cancel" = POST /cancel: ends all background work, keeps what finished.
  // onSettled runs after every answer (success or error) so the parent can
  // re-read /stats even if a WebSocket event was missed.
  import { tick } from 'svelte';
  import { api } from '../../api';

  let {
    crawlId,
    externalRunning = false,
    pageWeightRunning = false,
    linkAnalysisPending = false,
    onSettled,
  }: {
    crawlId: string;
    externalRunning?: boolean;
    pageWeightRunning?: boolean;
    linkAnalysisPending?: boolean;
    onSettled?: () => void;
  } = $props();

  type Action = 'stop' | 'cancel';
  let confirming = $state<Action | null>(null);
  let busy = $state<Action | null>(null);
  let done = $state<Action | null>(null);
  let error = $state<string | null>(null);
  let failedAction = $state<Action | null>(null);
  let confirmButton = $state<HTMLButtonElement | null>(null);

  // What Stop ends, in words, from what is running right now.
  let networkWork = $derived(
    externalRunning && pageWeightRunning ? 'external link check and page weight check'
    : pageWeightRunning ? 'page weight check'
    : 'external link check'
  );
  let stopLabel = $derived(`Stop ${networkWork}`);
  let explain = $derived<Record<Action, string>>({
    stop: `Stops the ${networkWork} now. What was checked so far is kept, the rest stays unchecked. Link analysis keeps running.`,
    cancel: 'Stops all background work: external link check, page weight check, link intelligence and embeddings. Your results stay; finished parts are kept, the rest is skipped.',
  });
  const confirmText: Record<Action, string> = { stop: 'Stop check', cancel: 'Cancel analysis' };
  const doneText: Record<Action, string> = {
    stop: 'Network checks stopped. Link analysis goes on with what was fetched.',
    cancel: 'Background analysis cancelled. Finished parts are kept.',
  };

  // Cancel supersedes stop: nothing is left to stop once it went through.
  let canStop = $derived((externalRunning || pageWeightRunning) && linkAnalysisPending && done === null);
  let canCancel = $derived(done !== 'cancel');

  // The work the confirm talks about ended on its own: close it, so no
  // "stopped" message or crawl event is produced for work that is done.
  $effect(() => {
    if (busy === null && ((confirming === 'stop' && !canStop) || (confirming === 'cancel' && !canCancel))) confirming = null;
  });

  async function open(action: Action) {
    confirming = action;
    error = null;
    await tick();
    confirmButton?.focus();
  }

  async function run(action: Action) {
    if (action === 'stop' && !canStop) { confirming = null; return; }
    busy = action;
    error = null;
    failedAction = null;
    try {
      await (action === 'stop' ? api.stopCrawl(crawlId) : api.cancelCrawl(crawlId));
      done = action;
      confirming = null;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      failedAction = action;
    } finally {
      busy = null;
      onSettled?.();
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && busy === null) {
      e.stopPropagation();
      confirming = null;
      error = null;
    }
  }
</script>

<div class="flex flex-wrap items-center gap-2 text-xs" data-testid="background-work-control">
  {#if confirming}
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
      class="flex w-full flex-wrap items-center gap-2 rounded-md border border-yellow-500/30 bg-yellow-500/5 px-3 py-2"
      role="group"
      aria-label={confirming === 'stop' ? stopLabel : 'Cancel background analysis'}
      aria-describedby="bg-work-explain-{crawlId}"
      onkeydown={onKeydown}
    >
      <span id="bg-work-explain-{crawlId}" class="text-fg-1 flex-1 min-w-[16rem]">{explain[confirming]}</span>
      <button
        bind:this={confirmButton}
        type="button"
        class="px-3 py-1 rounded-lg font-medium transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed {confirming === 'cancel' ? 'bg-red-500/20 text-red-400 hover:bg-red-500/30' : 'bg-accent/20 text-accent hover:bg-accent/30'}"
        disabled={busy !== null}
        aria-busy={busy !== null}
        aria-describedby="bg-work-explain-{crawlId}"
        onclick={() => confirming && run(confirming)}
      >{busy ? 'Working…' : confirmText[confirming]}</button>
      <button
        type="button"
        class="px-3 py-1 rounded-lg font-medium bg-surface-3 text-fg-1 hover:bg-surface-3/70 transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
        disabled={busy !== null}
        onclick={() => { confirming = null; error = null; }}
      >Keep running</button>
    </div>
  {:else}
    {#if done}
      <span class="text-fg-1" role="status">{doneText[done]}</span>
    {/if}
    {#if canStop}
      <button
        type="button"
        class="px-3 py-1 rounded-lg font-medium bg-accent/20 text-accent hover:bg-accent/30 transition-colors cursor-pointer"
        title="Stop the {networkWork}; link analysis keeps running"
        onclick={() => open('stop')}
      >{stopLabel}</button>
    {/if}
    {#if canCancel}
      <button
        type="button"
        class="px-3 py-1 rounded-lg font-medium bg-red-500/20 text-red-400 hover:bg-red-500/30 transition-colors cursor-pointer"
        title="Stop all background work; results stay"
        onclick={() => open('cancel')}
      >Cancel background analysis</button>
    {/if}
  {/if}
  {#if error}
    <span class="text-danger" role="alert">Could not {failedAction === 'stop' ? 'stop' : 'cancel'}: {error}</span>
  {/if}
</div>
