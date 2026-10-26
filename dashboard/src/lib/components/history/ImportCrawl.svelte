<script lang="ts">
  import { api } from '../../api';

  // Imports a crawl made elsewhere (e.g. `micelio crawl` on a server): the
  // crawl database or its JSONL output. The server analyses it in the
  // background; onImported lets History refresh until it is done.
  let { onImported }: { onImported: (id: string) => void } = $props();

  let input: HTMLInputElement;
  let linkIntelligence = $state(true);
  let progress = $state<number | null>(null);
  let message = $state<string | null>(null);
  let error = $state<string | null>(null);

  async function upload(file: File) {
    error = null;
    message = null;
    progress = 0;
    try {
      const job = await api.importCrawl(file, linkIntelligence, (p) => (progress = p));
      message = `Imported ${job.seedUrl} · analysing on this computer`;
      onImported(job.id);
    } catch (err) {
      error = (err as Error).message;
    } finally {
      progress = null;
      input.value = '';
    }
  }
</script>

<div class="flex flex-wrap items-center gap-3 text-sm">
  <input
    bind:this={input}
    type="file"
    accept=".db,.jsonl,.gz,.zst"
    class="hidden"
    onchange={() => { const f = input.files?.[0]; if (f) upload(f); }}
  />
  <button
    type="button"
    class="px-3 py-1.5 rounded-md border border-border bg-surface-2 text-fg hover:bg-surface-3 transition-colors cursor-pointer disabled:opacity-50"
    disabled={progress !== null}
    onclick={() => input.click()}
    title="Open a crawl made with `micelio crawl` (its .db file or JSONL output)"
  >
    {progress === null ? 'Import crawl…' : progress < 100 ? `Uploading ${progress}%` : 'Preparing…'}
  </button>
  <label class="flex items-center gap-1.5 text-xs text-fg-2 cursor-pointer">
    <input type="checkbox" bind:checked={linkIntelligence} />
    Link analysis
  </label>
  {#if message}<span class="text-xs text-success">{message}</span>{/if}
  {#if error}<span class="text-xs text-danger">{error}</span>{/if}
</div>
