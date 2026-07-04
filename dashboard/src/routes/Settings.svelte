<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { updates } from '../lib/stores/updates.svelte';
  import CollapsibleSection from '../lib/components/ui/CollapsibleSection.svelte';
  import Toast from '../lib/components/ui/Toast.svelte';

  interface Settings {
    defaultDepth: number;
    defaultLimit: number;
    defaultConcurrency: number;
    defaultDelay: number;
    defaultUserAgent: string;
    psiKey: string;
    aiProvider: string;
    aiModel: string;
    aiKey: string;
    gscKeyFile: string;
    gscProperty: string;
    gscDays: number;
    ga4KeyFile: string;
    ga4Property: string;
    ga4Days: number;
    cruxKey: string;
    plausibleApiKey: string;
    plausibleSiteId: string;
    plausibleHost: string;
    plausibleDays: number;
    defaultEmbeddings: boolean;
    embeddingModel: string;
    similarityThreshold: number;
    defaultNgrams: boolean;
    defaultLinkIntelligence: boolean;
    liMaxSuggestions: number;
    liNoCentrality: boolean;
    defaultSitemapOut: boolean;
    defaultOutputFormat: string;
    defaultHtmlReport: boolean;
    defaultCheckExternal: boolean;
    defaultJsRendering: boolean;
    defaultRespectRobots: boolean;
    defaultShowBlockedInternal: boolean;
    alertWebhookUrl: string;
    alertSlackUrl: string;
  }

  let loading = $state(true);
  let saving = $state(false);
  let toast = $state<{ message: string; type: 'success' | 'error' } | null>(null);

  // Settings state
  let settings = $state<Settings>({} as Settings);
  // Snapshot of last saved state for dirty tracking
  let savedSnapshot = $state('');

  const DEFAULTS: Settings = {
    defaultDepth: 3,
    defaultLimit: 500,
    defaultConcurrency: 5,
    defaultDelay: 0,
    defaultUserAgent: '',
    psiKey: '',
    aiProvider: '',
    aiModel: '',
    aiKey: '',
    gscKeyFile: '',
    gscProperty: '',
    gscDays: 7,
    ga4KeyFile: '',
    ga4Property: '',
    ga4Days: 7,
    cruxKey: '',
    plausibleApiKey: '',
    plausibleSiteId: '',
    plausibleHost: 'https://plausible.io',
    plausibleDays: 30,
    defaultEmbeddings: false,
    embeddingModel: 'text-embedding-3-small',
    similarityThreshold: 0.85,
    defaultNgrams: false,
    defaultLinkIntelligence: false,
    liMaxSuggestions: 200,
    liNoCentrality: false,
    defaultSitemapOut: false,
    defaultOutputFormat: 'jsonl',
    defaultHtmlReport: true,
    defaultCheckExternal: false,
    defaultJsRendering: false,
    defaultRespectRobots: true,
    defaultShowBlockedInternal: false,
    alertWebhookUrl: '',
    alertSlackUrl: '',
  };

  let dirty = $derived(JSON.stringify(settings) !== savedSnapshot);

  onMount(async () => {
    try {
      const data = await api.getSettings();
      settings = { ...DEFAULTS, ...data } as Settings;
    } catch {
      settings = { ...DEFAULTS };
    }
    savedSnapshot = JSON.stringify(settings);
    loading = false;
    updates.load();
  });

  async function checkForUpdates() {
    try {
      await updates.load(true);
    } catch { /* error shown via updates.error */ }
  }

  async function installUpdate() {
    if (updates.installing) return;
    try {
      const res = await updates.install();
      if (res?.installed) {
        toast = { message: `Updated to ${updates.status?.current ?? ''}. Restart Micelio to use it.`, type: 'success' };
      }
    } catch (err) {
      toast = { message: `Update failed: ${(err as Error).message}`, type: 'error' };
    }
  }

  async function rollbackUpdate() {
    if (updates.installing) return;
    try {
      await updates.rollback();
      toast = { message: 'Rolled back to previous version. Restart Micelio to use it.', type: 'success' };
    } catch (err) {
      toast = { message: `Rollback failed: ${(err as Error).message}`, type: 'error' };
    }
  }

  function formatTimestamp(ts?: string): string {
    if (!ts) return '—';
    try { return new Date(ts).toLocaleString(); } catch { return ts; }
  }

  // Warn on page/tab close when there are unsaved changes
  $effect(() => {
    const handler = (e: BeforeUnloadEvent) => {
      if (dirty) {
        e.preventDefault();
      }
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  });

  // Warn on in-app hash navigation when there are unsaved changes
  $effect(() => {
    let reverting = false;
    const handler = (e: HashChangeEvent) => {
      if (reverting) return;
      if (dirty && !confirm('You have unsaved changes. Leave this page?')) {
        reverting = true;
        window.location.hash = e.oldURL.split('#')[1] || '';
        setTimeout(() => { reverting = false; }, 0);
      }
    };
    window.addEventListener('hashchange', handler);
    return () => window.removeEventListener('hashchange', handler);
  });

  async function save() {
    if (saving) return; // Guard against concurrent saves (#10)
    saving = true;
    try {
      await api.updateSettings(settings as unknown as Record<string, unknown>);
      savedSnapshot = JSON.stringify(settings);
      toast = { message: 'Settings saved', type: 'success' };
    } catch (err) {
      toast = { message: `Failed to save: ${(err as Error).message}`, type: 'error' };
    }
    saving = false;
  }

  async function resetDefaults() {
    if (confirm('Reset all settings to defaults? This cannot be undone.')) {
      settings = { ...DEFAULTS };
      await save();
    }
  }

  // Helper for typed access
  function str(key: keyof Settings): string { return String(settings[key] ?? ''); }
  function num(key: keyof Settings): number { return Number(settings[key] ?? 0); }
  function bool(key: keyof Settings): boolean { return Boolean(settings[key]); }

  function setNum(key: keyof Settings, raw: string) {
    const v = Number(raw);
    if (!Number.isNaN(v)) settings = { ...settings, [key]: v };
  }

  function set(key: keyof Settings, value: unknown) {
    settings = { ...settings, [key]: value };
  }
</script>

{#snippet actionButtons()}
  <button
    type="button"
    class="px-4 py-2 rounded-lg text-sm font-medium bg-surface-3 hover:bg-surface-2 cursor-pointer transition-colors"
    onclick={resetDefaults}
  >Reset Defaults</button>
  <button
    type="button"
    class="px-4 py-2 rounded-lg text-sm font-medium bg-accent text-white hover:bg-accent/80 cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-default"
    disabled={saving || !dirty}
    onclick={save}
  >{saving ? 'Saving...' : 'Save Settings'}</button>
{/snippet}

{#if toast}
  <Toast message={toast.message} type={toast.type} onDismiss={() => toast = null} />
{/if}

<div class="max-w-4xl mx-auto space-y-4">
  {#if loading}
    <div class="rounded-xl border border-border bg-surface-2 p-12 text-center">
      <div class="inline-block w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
      <p class="text-fg-2 mt-3">Loading settings...</p>
    </div>
  {:else}
    <!-- Action bar -->
    <div class="flex items-center justify-between">
      <p class="text-sm text-fg-2">Configure default crawl parameters, API keys, and integrations.</p>
      <div class="flex gap-2">
        {@render actionButtons()}
      </div>
    </div>

    <!-- Updates -->
    <CollapsibleSection title="Updates">
      {#if updates.status}
        <div class="space-y-3 text-sm">
          <div class="grid grid-cols-2 gap-x-4 gap-y-2">
            <div>
              <div class="text-xs text-fg-2">Current version</div>
              <div class="font-medium font-mono">{updates.status.current || 'unknown'}</div>
            </div>
            <div>
              <div class="text-xs text-fg-2">Latest release</div>
              <div class="font-medium font-mono">
                {#if updates.status.latest}
                  {updates.status.latest}
                  {#if updates.status.releaseUrl}
                    <a href={updates.status.releaseUrl} target="_blank" rel="noopener noreferrer" class="text-accent hover:underline ml-2 text-xs">notes</a>
                  {/if}
                {:else}
                  —
                {/if}
              </div>
            </div>
            <div>
              <div class="text-xs text-fg-2">Platform</div>
              <div class="font-mono">{updates.status.platform}</div>
            </div>
            <div>
              <div class="text-xs text-fg-2">Last checked</div>
              <div>{formatTimestamp(updates.status.lastCheckedAt)}</div>
            </div>
          </div>

          {#if updates.status.notes}
            <div class="text-xs text-fg-2 italic">{updates.status.notes}</div>
          {/if}

          {#if updates.status.isDevBuild}
            <div class="text-xs text-fg-2">
              Auto-update is disabled for development builds. Build a tagged release (<span class="font-mono">v1.2.3</span>) to enable in-app updates.
            </div>
          {:else if updates.status.updateAvailable && !updates.status.downloadable}
            <div class="text-xs text-amber-400">
              A new release ({updates.status.latest}) is available, but no asset matches your platform ({updates.status.platform}). You'll need to update manually.
            </div>
          {/if}

          <div class="flex flex-wrap gap-2 pt-1">
            <button
              type="button"
              class="px-3 py-1.5 rounded-md bg-surface-3 hover:bg-surface-2 text-xs font-medium cursor-pointer disabled:opacity-50"
              onclick={checkForUpdates}
              disabled={updates.loading}
            >{updates.loading && !updates.installing ? 'Checking...' : 'Check now'}</button>
            {#if updates.status.updateAvailable && updates.status.downloadable && !updates.status.isDevBuild}
              <button
                type="button"
                class="px-3 py-1.5 rounded-md bg-accent text-white text-xs font-medium hover:brightness-110 cursor-pointer disabled:opacity-50"
                onclick={installUpdate}
                disabled={updates.installing}
              >{updates.installing ? 'Installing...' : `Install ${updates.status.latest}`}</button>
            {/if}
            {#if updates.status.canRollback}
              <button
                type="button"
                class="px-3 py-1.5 rounded-md bg-surface-3 hover:bg-surface-2 text-xs font-medium cursor-pointer disabled:opacity-50"
                onclick={rollbackUpdate}
                disabled={updates.installing}
              >Rollback to previous</button>
            {/if}
          </div>

          {#if updates.error}
            <div class="text-xs text-red-400">{updates.error}</div>
          {/if}
        </div>
      {:else if updates.loading}
        <p class="text-xs text-fg-2">Loading update status...</p>
      {:else}
        <p class="text-xs text-fg-2">Update status unavailable.</p>
      {/if}
    </CollapsibleSection>

    <!-- Default Crawl Settings -->
    <CollapsibleSection title="Default Crawl Settings" open={true}>
      <p class="text-xs text-fg-2 mb-3">These values pre-fill the New Crawl form when no preset is selected.</p>
      <div class="grid grid-cols-2 sm:grid-cols-4 gap-4">
        <label class="block">
          <span class="text-xs text-fg-2">Depth</span>
          <input
            type="number" min="1" max="20"
            value={num('defaultDepth')}
            oninput={(e) => setNum('defaultDepth', (e.target as HTMLInputElement).value)}
            class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
          />
        </label>
        <label class="block">
          <span class="text-xs text-fg-2">Page Limit</span>
          <input
            type="number" min="1" max="1000000"
            value={num('defaultLimit')}
            oninput={(e) => setNum('defaultLimit', (e.target as HTMLInputElement).value)}
            class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
          />
        </label>
        <label class="block">
          <span class="text-xs text-fg-2">Concurrency</span>
          <input
            type="number" min="1" max="50"
            value={num('defaultConcurrency')}
            oninput={(e) => setNum('defaultConcurrency', (e.target as HTMLInputElement).value)}
            class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
          />
        </label>
        <label class="block">
          <span class="text-xs text-fg-2">Delay (ms)</span>
          <input
            type="number" min="0" max="10000"
            value={num('defaultDelay')}
            oninput={(e) => setNum('defaultDelay', (e.target as HTMLInputElement).value)}
            class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
          />
        </label>
      </div>
      <label class="block mt-3">
        <span class="text-xs text-fg-2">Custom User Agent</span>
        <input
          type="text"
          value={str('defaultUserAgent')}
          placeholder="Leave empty for default"
          oninput={(e) => set('defaultUserAgent', (e.target as HTMLInputElement).value)}
          class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
        />
      </label>
      <div class="flex flex-wrap gap-4 mt-3">
        <label class="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={bool('defaultCheckExternal')} onchange={(e) => set('defaultCheckExternal', (e.target as HTMLInputElement).checked)} class="w-4 h-4 accent-accent" />
          Check External Links
        </label>
        <label class="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={bool('defaultJsRendering')} onchange={(e) => set('defaultJsRendering', (e.target as HTMLInputElement).checked)} class="w-4 h-4 accent-accent" />
          JavaScript Rendering
        </label>
      </div>
    </CollapsibleSection>

    <!-- Robots.txt Defaults -->
    <CollapsibleSection title="Robots.txt">
      <div class="space-y-3">
        <label class="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={bool('defaultRespectRobots')} onchange={(e) => set('defaultRespectRobots', (e.target as HTMLInputElement).checked)} class="w-4 h-4 accent-accent" />
          Respect robots.txt
        </label>
        <p class="text-xs text-fg-2 pl-6">When disabled, the crawler will ignore robots.txt Disallow rules.</p>
        <label class="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={bool('defaultShowBlockedInternal')} onchange={(e) => set('defaultShowBlockedInternal', (e.target as HTMLInputElement).checked)} class="w-4 h-4 accent-accent" />
          Show internal URLs blocked by robots.txt
        </label>
      </div>
    </CollapsibleSection>

    <!-- API Keys -->
    <CollapsibleSection title="API Keys">
      <p class="text-xs text-fg-2 mb-3">API keys are stored locally and never sent to external servers (except to the respective API).</p>
      <div class="space-y-3">
        <label class="block">
          <span class="text-xs text-fg-2">PageSpeed Insights API Key</span>
          <input
            type="password"
            value={str('psiKey')}
            placeholder="Enter PSI API key"
            oninput={(e) => set('psiKey', (e.target as HTMLInputElement).value)}
            class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
          />
        </label>
        <label class="block">
          <span class="text-xs text-fg-2">CrUX API Key</span>
          <input
            type="password"
            value={str('cruxKey')}
            placeholder="Enter CrUX API key"
            oninput={(e) => set('cruxKey', (e.target as HTMLInputElement).value)}
            class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
          />
        </label>
        <div class="border-t border-border pt-3 mt-3">
          <span class="text-xs text-fg-2 font-medium">AI Analysis</span>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <label class="block">
            <span class="text-xs text-fg-2">Provider</span>
            <select
              value={str('aiProvider')}
              onchange={(e) => set('aiProvider', (e.target as HTMLSelectElement).value)}
              class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
            >
              <option value="">None</option>
              <option value="openai">OpenAI</option>
              <option value="anthropic">Anthropic</option>
            </select>
          </label>
          <label class="block">
            <span class="text-xs text-fg-2">Model</span>
            <input
              type="text"
              value={str('aiModel')}
              placeholder="e.g. gpt-4o-mini"
              oninput={(e) => set('aiModel', (e.target as HTMLInputElement).value)}
              class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </label>
        </div>
        <label class="block">
          <span class="text-xs text-fg-2">AI API Key</span>
          <input
            type="password"
            value={str('aiKey')}
            placeholder="Enter API key for selected provider"
            oninput={(e) => set('aiKey', (e.target as HTMLInputElement).value)}
            class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
          />
        </label>
      </div>
    </CollapsibleSection>

    <!-- Google Integrations -->
    <CollapsibleSection title="Google Integrations">
      <p class="text-xs text-fg-2 mb-3">Configure Google Search Console, GA4, and CrUX defaults.</p>
      <div class="space-y-4">
        <div>
          <span class="text-xs text-fg-2 font-medium">Google Search Console</span>
          <div class="grid grid-cols-2 gap-3 mt-2">
            <label class="block">
              <span class="text-xs text-fg-2">Service Account Key File</span>
              <input
                type="text"
                value={str('gscKeyFile')}
                placeholder="/path/to/service-account.json"
                oninput={(e) => set('gscKeyFile', (e.target as HTMLInputElement).value)}
                class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
              />
            </label>
            <label class="block">
              <span class="text-xs text-fg-2">Default Property</span>
              <input
                type="text"
                value={str('gscProperty')}
                placeholder="sc-domain:example.com"
                oninput={(e) => set('gscProperty', (e.target as HTMLInputElement).value)}
                class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
              />
            </label>
          </div>
          <label class="block mt-2">
            <span class="text-xs text-fg-2">Days of Data</span>
            <input
              type="number" min="1" max="90"
              value={num('gscDays')}
              oninput={(e) => setNum('gscDays', (e.target as HTMLInputElement).value)}
              class="mt-1 w-28 px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </label>
        </div>
        <div class="border-t border-border pt-3">
          <span class="text-xs text-fg-2 font-medium">Google Analytics 4</span>
          <div class="grid grid-cols-2 gap-3 mt-2">
            <label class="block">
              <span class="text-xs text-fg-2">Service Account Key File</span>
              <input
                type="text"
                value={str('ga4KeyFile')}
                placeholder="/path/to/service-account.json"
                oninput={(e) => set('ga4KeyFile', (e.target as HTMLInputElement).value)}
                class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
              />
            </label>
            <label class="block">
              <span class="text-xs text-fg-2">Property ID</span>
              <input
                type="text"
                value={str('ga4Property')}
                placeholder="properties/123456789"
                oninput={(e) => set('ga4Property', (e.target as HTMLInputElement).value)}
                class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
              />
            </label>
          </div>
          <label class="block mt-2">
            <span class="text-xs text-fg-2">Days of Data</span>
            <input
              type="number" min="1" max="90"
              value={num('ga4Days')}
              oninput={(e) => setNum('ga4Days', (e.target as HTMLInputElement).value)}
              class="mt-1 w-28 px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </label>
        </div>
      </div>
    </CollapsibleSection>

    <!-- Plausible Analytics -->
    <CollapsibleSection title="Plausible Analytics">
      <p class="text-xs text-fg-2 mb-3">Privacy-friendly analytics. Works with Plausible Cloud and self-hosted instances.</p>
      <div class="space-y-4">
        <div class="grid grid-cols-2 gap-3">
          <label class="block">
            <span class="text-xs text-fg-2">API Key</span>
            <input
              type="password"
              value={str('plausibleApiKey')}
              placeholder="Your Plausible API key"
              oninput={(e) => set('plausibleApiKey', (e.target as HTMLInputElement).value)}
              class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </label>
          <label class="block">
            <span class="text-xs text-fg-2">Site ID (domain)</span>
            <input
              type="text"
              value={str('plausibleSiteId')}
              placeholder="example.com"
              oninput={(e) => set('plausibleSiteId', (e.target as HTMLInputElement).value)}
              class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </label>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <label class="block">
            <span class="text-xs text-fg-2">Instance URL</span>
            <input
              type="text"
              value={str('plausibleHost')}
              placeholder="https://plausible.io"
              oninput={(e) => set('plausibleHost', (e.target as HTMLInputElement).value)}
              class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </label>
          <label class="block">
            <span class="text-xs text-fg-2">Days of Data</span>
            <input
              type="number" min="1" max="365"
              value={num('plausibleDays')}
              oninput={(e) => setNum('plausibleDays', (e.target as HTMLInputElement).value)}
              class="mt-1 w-28 px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </label>
        </div>
      </div>
    </CollapsibleSection>

    <!-- Content & Analysis -->
    <CollapsibleSection title="Content & Analysis">
      <p class="text-xs text-fg-2 mb-3">Default content analysis features enabled for new crawls.</p>
      <div class="space-y-3">
        <div class="flex flex-wrap gap-4">
          <label class="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={bool('defaultNgrams')} onchange={(e) => set('defaultNgrams', (e.target as HTMLInputElement).checked)} class="w-4 h-4 accent-accent" />
            N-gram Analysis
          </label>
          <label class="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={bool('defaultEmbeddings')} onchange={(e) => set('defaultEmbeddings', (e.target as HTMLInputElement).checked)} class="w-4 h-4 accent-accent" />
            Semantic Embeddings
          </label>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <label class="block">
            <span class="text-xs text-fg-2">Embedding Model</span>
            <input
              type="text"
              value={str('embeddingModel')}
              placeholder="text-embedding-3-small"
              oninput={(e) => set('embeddingModel', (e.target as HTMLInputElement).value)}
              class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </label>
          <label class="block">
            <span class="text-xs text-fg-2">Similarity Threshold</span>
            <input
              type="number" min="0" max="1" step="0.05"
              value={num('similarityThreshold')}
              oninput={(e) => { const v = Number((e.target as HTMLInputElement).value); if (!Number.isNaN(v)) set('similarityThreshold', Math.round(v * 100) / 100); }}
              class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </label>
        </div>
      </div>
    </CollapsibleSection>

    <!-- Link Intelligence -->
    <CollapsibleSection title="Link Intelligence">
      <p class="text-xs text-fg-2 mb-3">Internal linking analysis and suggestion defaults.</p>
      <div class="space-y-3">
        <div class="flex flex-wrap gap-4">
          <label class="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={bool('defaultLinkIntelligence')} onchange={(e) => set('defaultLinkIntelligence', (e.target as HTMLInputElement).checked)} class="w-4 h-4 accent-accent" />
            Enable by Default
          </label>
          <label class="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={bool('liNoCentrality')} onchange={(e) => set('liNoCentrality', (e.target as HTMLInputElement).checked)} class="w-4 h-4 accent-accent" />
            Skip Centrality Calculation
          </label>
          <label class="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={bool('defaultSitemapOut')} onchange={(e) => set('defaultSitemapOut', (e.target as HTMLInputElement).checked)} class="w-4 h-4 accent-accent" />
            Generate Sitemap
          </label>
        </div>
        <label class="block">
          <span class="text-xs text-fg-2">Max Link Suggestions</span>
          <input
            type="number" min="10" max="1000"
            value={num('liMaxSuggestions')}
            oninput={(e) => setNum('liMaxSuggestions', (e.target as HTMLInputElement).value)}
            class="mt-1 w-32 px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
          />
        </label>
      </div>
    </CollapsibleSection>

    <!-- Output Defaults -->
    <CollapsibleSection title="Output Defaults">
      <p class="text-xs text-fg-2 mb-3">Default output format and reporting options.</p>
      <div class="space-y-3">
        <label class="block">
          <span class="text-xs text-fg-2">Output Format</span>
          <select
            value={str('defaultOutputFormat')}
            onchange={(e) => set('defaultOutputFormat', (e.target as HTMLSelectElement).value)}
            class="mt-1 w-48 px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
          >
            <option value="jsonl">JSONL</option>
            <option value="csv">CSV</option>
            <option value="json">JSON</option>
          </select>
        </label>
        <label class="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={bool('defaultHtmlReport')} onchange={(e) => set('defaultHtmlReport', (e.target as HTMLInputElement).checked)} class="w-4 h-4 accent-accent" />
          Generate HTML Report
        </label>
      </div>
    </CollapsibleSection>

    <!-- Alert Notifications -->
    <CollapsibleSection title="Alert Notifications">
      <p class="text-xs text-fg-2 mb-3">Receive notifications when crawl results exceed thresholds (high error rate, low indexability, slow response times, etc.).</p>
      <div class="space-y-3">
        <label class="block">
          <span class="text-xs text-fg-2">Webhook URL</span>
          <input
            type="url"
            value={str('alertWebhookUrl')}
            placeholder="https://your-server.com/webhook"
            oninput={(e) => set('alertWebhookUrl', (e.target as HTMLInputElement).value)}
            class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
          />
          <span class="text-[10px] text-fg-2 mt-1 block">POST JSON payload with triggered alerts after each crawl</span>
        </label>
        <label class="block">
          <span class="text-xs text-fg-2">Slack Webhook URL</span>
          <input
            type="url"
            value={str('alertSlackUrl')}
            placeholder="https://hooks.slack.com/services/..."
            oninput={(e) => set('alertSlackUrl', (e.target as HTMLInputElement).value)}
            class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent"
          />
          <span class="text-[10px] text-fg-2 mt-1 block">Slack incoming webhook for alert notifications</span>
        </label>
      </div>
    </CollapsibleSection>

    <!-- Bottom save bar -->
    <div class="flex justify-end gap-2 pt-2 pb-4">
      {@render actionButtons()}
    </div>
  {/if}
</div>
