<script lang="ts">
  import { onDestroy } from 'svelte';
  import { api, type BrowserPreviewFrame } from '../api';

  let { config }: { config: Record<string, unknown> } = $props();
  let frame = $state<BrowserPreviewFrame | null>(null);
  let busy = $state(false);
  let error = $state('');
  let capturedSettings = $state('');
  let controller: AbortController | null = null;
  const settings = $derived(JSON.stringify(config));
  const stale = $derived(!!frame && capturedSettings !== settings);
  const homepage = $derived.by(() => {
    try {
      const target = new URL(String(config.seedUrl ?? ''));
      return ['http:', 'https:'].includes(target.protocol) && !target.username && !target.password ? `${target.origin}/` : '';
    } catch { return ''; }
  });
  const mode = $derived(config.nativeChrome ? 'Native Chrome' : config.jsRendering ? 'JavaScript enabled' : 'HTML only · JavaScript disabled');
  const modeLabel = (value: string) => value === 'native' ? 'Native Chrome' : value === 'javascript' ? 'JavaScript enabled' : 'HTML only · JavaScript disabled';

  async function capture() {
    if (busy || !homepage) return;
    const requestSettings = settings;
    const requestConfig = JSON.parse(requestSettings);
    const current = new AbortController();
    controller = current;
    busy = true;
    error = '';
    try {
      const result = await api.openBrowserPreview(requestConfig, current.signal);
      if (controller !== current) return;
      frame = result.frame;
      capturedSettings = requestSettings;
    } catch (e) {
      if (controller === current && !current.signal.aborted) error = e instanceof Error ? e.message : 'Could not capture the homepage';
    } finally {
      if (controller === current) { busy = false; controller = null; }
    }
  }

  function close() {
    controller?.abort();
    controller = null;
    frame = null;
    error = '';
    busy = false;
    capturedSettings = '';
  }
  onDestroy(close);
</script>

<div class="mt-3 space-y-2" data-testid="browser-preview">
  <div class="flex flex-wrap items-center gap-2">
    <button type="button" onclick={capture} disabled={busy || !homepage}
      class="px-3 py-1.5 rounded-lg border border-border text-sm hover:border-accent disabled:opacity-50 cursor-pointer disabled:cursor-default">
      {busy ? 'Capturing homepage…' : frame ? 'Update preview' : 'Preview crawler view'}
    </button>
    <span class="text-xs text-fg-2" data-testid="preview-mode">{mode}</span>
    {#if frame || busy}
      <button type="button" onclick={close} class="text-xs px-2 py-1 border border-border rounded cursor-pointer">{busy ? 'Cancel preview' : 'Close preview'}</button>
    {/if}
  </div>
  <p class="text-xs text-fg-2">A still image of the homepage using your crawl settings. No clicks or navigation. Chrome must be installed on the machine running Micelio.</p>
  {#if stale}
    <p role="status" class="text-xs text-amber-600 dark:text-amber-400" data-testid="preview-stale">Crawl settings changed. Update the preview to see the current crawler view.</p>
  {/if}
  {#if frame}
    <div class="rounded-lg border border-border overflow-hidden bg-surface" aria-busy={busy}>
      <div class="p-3 border-b border-border space-y-1">
        <div class="flex flex-wrap gap-2 items-center text-xs">
          <span class="font-medium">{frame.title || 'Homepage preview'}</span>
          <span class="px-2 py-0.5 rounded bg-surface-3">HTTP {frame.statusCode}</span>
          <span class="text-fg-2">{modeLabel(frame.mode)}</span>
        </div>
        <p class="text-xs text-fg-2 break-all" data-testid="preview-url">{frame.url}</p>
        {#if frame.browserUrl && frame.browserUrl !== frame.url}
          <p class="text-xs text-fg-2 break-all">Browser URL after JavaScript · {frame.browserUrl}</p>
        {/if}
        <p class="text-xs text-fg-2 break-all">User-Agent · {frame.userAgent}</p>
        {#if frame.statusCode === 403 || frame.statusCode === 429}
          <p role="status" class="text-xs text-amber-600 dark:text-amber-400">The site blocked this request. The image shows the response received by this crawl mode.</p>
        {/if}
        {#if config.jsRendering && frame.mode === 'html' && frame.statusCode !== 200 && !stale}
          <p class="text-xs text-fg-2">The crawler runs JavaScript only on HTTP 200 pages. This response is shown without JavaScript.</p>
        {/if}
      </div>
      <img src={`data:image/jpeg;base64,${frame.jpeg}`} alt="Homepage as seen in the selected crawl mode" class="block w-full h-auto" draggable="false" />
      <p class="p-3 border-t border-border text-xs text-fg-2">The crawler reads HTML; this image helps you check it. Styles are displayed for readability. Image blocking follows your crawl settings. Capture completed at {new Date(frame.capturedAt).toLocaleTimeString()}. The preview browser is closed after capture.</p>
    </div>
  {/if}
  {#if error}<p class="text-sm text-red-500" role="alert">{error}</p>{/if}
</div>
