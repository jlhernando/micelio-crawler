<script lang="ts">
  import PageHeading from '../lib/components/layout/PageHeading.svelte';
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
    alertEmailHost: string;
    alertEmailPort: number;
    alertEmailUser: string;
    alertEmailPassword: string;
    alertEmailFrom: string;
    alertEmailTo: string;
    alertEmailSecurity: string;
    alertEmailAlways: boolean;
    dashboardUrl: string;
    alertTelegramToken: string;
    alertTelegramChatId: string;
    alertAlways: boolean;
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
    alertEmailHost: '',
    alertEmailPort: 587,
    alertEmailUser: '',
    alertEmailPassword: '',
    alertEmailFrom: '',
    alertEmailTo: '',
    alertEmailSecurity: 'starttls',
    alertEmailAlways: false,
    dashboardUrl: '',
    alertTelegramToken: '',
    alertTelegramChatId: '',
    alertAlways: false,
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

  let emailTesting = $state(false);
  let emailTest = $state<{ ok: boolean; message: string } | null>(null);

  async function testEmail() {
    emailTesting = true;
    emailTest = null;
    try {
      if (dirty) await save(); // the test uses the saved settings
      const res = await api.testAlertChannel('email');
      emailTest = { ok: true, message: `Sent to ${res.to.join(', ')}` };
    } catch (e) {
      emailTest = { ok: false, message: e instanceof Error ? e.message : String(e) };
    } finally {
      emailTesting = false;
    }
  }

  let channelTest = $state<Record<string, { ok: boolean; message: string } | null>>({});
  async function testChannel(channel: 'telegram' | 'slack') {
    channelTest = { ...channelTest, [channel]: { ok: true, message: 'Sending…' } };
    try {
      if (dirty) await save();
      await api.testAlertChannel(channel);
      channelTest = { ...channelTest, [channel]: { ok: true, message: 'Sent' } };
    } catch (e) {
      channelTest = { ...channelTest, [channel]: { ok: false, message: e instanceof Error ? e.message : String(e) } };
    }
  }

  let telegramChats = $state<{ id: number; title: string; type: string }[] | null>(null);
  async function findTelegramChats() {
    try {
      if (dirty) await save();
      telegramChats = (await api.telegramChats()).chats;
      if (telegramChats.length === 0) channelTest = { ...channelTest, telegram: { ok: false, message: 'No chats yet: send the bot a message (or add it to a group) and try again' } };
    } catch (e) {
      channelTest = { ...channelTest, telegram: { ok: false, message: e instanceof Error ? e.message : String(e) } };
    }
  }

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
  <PageHeading title="Settings" description="Manage crawl defaults, integrations, and application updates." section="Workspace" />
  {#if loading}
    <div class="rounded-xl border border-border bg-surface-2 p-12 text-center">
      <div class="inline-block w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
      <p class="text-fg-2 mt-3">Loading settings...</p>
    </div>
  {:else}
    <!-- Action bar -->
    <div class="flex flex-wrap gap-4 items-center justify-between">
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
          <span class="text-[10px] text-fg-2 mt-1 block">Slack incoming webhook: gets the same crawl report as email (key points, alerts, changes)</span>
        </label>
        <div class="flex items-center gap-3">
          <button type="button" class="px-3 py-1.5 rounded-md border border-border bg-surface-2 text-sm hover:bg-surface-3 cursor-pointer" onclick={() => testChannel('slack')}>Send test to Slack</button>
          {#if channelTest.slack}<span class="text-xs {channelTest.slack.ok ? 'text-success' : 'text-danger'}">{channelTest.slack.message}</span>{/if}
        </div>
      </div>

      <h3 class="text-sm font-medium text-fg mt-6 mb-1">Telegram</h3>
      <p class="text-xs text-fg-2 mb-3">Create a bot with <a class="underline" href="https://t.me/BotFather" target="_blank" rel="noopener">@BotFather</a> (/newbot), paste its token, send the bot any message (or add it to a group or channel), then pick the chat.</p>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <label class="block">
          <span class="text-xs text-fg-2">Bot token</span>
          <input type="password" autocomplete="off" value={str('alertTelegramToken')} placeholder="123456:ABC…" oninput={(e) => set('alertTelegramToken', (e.target as HTMLInputElement).value)} class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent" />
        </label>
        <label class="block">
          <span class="text-xs text-fg-2">Chat ID</span>
          <input type="text" value={str('alertTelegramChatId')} placeholder="-1001234567890" oninput={(e) => set('alertTelegramChatId', (e.target as HTMLInputElement).value)} class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent" />
        </label>
      </div>
      <div class="flex flex-wrap items-center gap-3 mt-3">
        <button type="button" class="px-3 py-1.5 rounded-md border border-border bg-surface-2 text-sm hover:bg-surface-3 cursor-pointer" onclick={findTelegramChats}>Find my chat</button>
        <button type="button" class="px-3 py-1.5 rounded-md border border-border bg-surface-2 text-sm hover:bg-surface-3 cursor-pointer" onclick={() => testChannel('telegram')}>Send test to Telegram</button>
        {#if channelTest.telegram}<span class="text-xs {channelTest.telegram.ok ? 'text-success' : 'text-danger'}">{channelTest.telegram.message}</span>{/if}
      </div>
      {#if telegramChats && telegramChats.length > 0}
        <div class="mt-2 flex flex-wrap gap-2">
          {#each telegramChats as c (c.id)}
            <button type="button" class="px-2 py-1 rounded border border-border text-xs hover:bg-surface-3 cursor-pointer" onclick={() => set('alertTelegramChatId', String(c.id))}>{c.title} · {c.type}</button>
          {/each}
        </div>
      {/if}

      <h3 class="text-sm font-medium text-fg mt-6 mb-1">Email</h3>
      <p class="text-xs text-fg-2 mb-3">After each crawl, an email with its alerts and what changed since the previous crawl of the same site or schedule (pages, errors, indexable share, new soft-404 candidates, URLs no longer found). Scheduled runs that are skipped or fail are emailed too. Works with any SMTP account (Gmail or Outlook with an app password, SendGrid, Resend, your company relay).</p>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
        <label class="block md:col-span-2">
          <span class="text-xs text-fg-2">SMTP host</span>
          <input type="text" value={str('alertEmailHost')} placeholder="smtp.gmail.com" oninput={(e) => set('alertEmailHost', (e.target as HTMLInputElement).value)} class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent" />
        </label>
        <label class="block">
          <span class="text-xs text-fg-2">Port</span>
          <input type="number" value={num('alertEmailPort')} oninput={(e) => setNum('alertEmailPort', (e.target as HTMLInputElement).value)} class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent" />
        </label>
        <label class="block">
          <span class="text-xs text-fg-2">Username</span>
          <input type="text" autocomplete="off" value={str('alertEmailUser')} oninput={(e) => set('alertEmailUser', (e.target as HTMLInputElement).value)} class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent" />
        </label>
        <label class="block">
          <span class="text-xs text-fg-2">Password</span>
          <input type="password" autocomplete="new-password" value={str('alertEmailPassword')} oninput={(e) => set('alertEmailPassword', (e.target as HTMLInputElement).value)} class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent" />
        </label>
        <label class="block">
          <span class="text-xs text-fg-2">Security</span>
          <select value={str('alertEmailSecurity')} onchange={(e) => set('alertEmailSecurity', (e.target as HTMLSelectElement).value)} class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent">
            <option value="starttls">STARTTLS (port 587)</option>
            <option value="tls">TLS (port 465)</option>
            <option value="none">None (local relay only)</option>
          </select>
        </label>
        <label class="block">
          <span class="text-xs text-fg-2">From</span>
          <input type="email" value={str('alertEmailFrom')} placeholder="micelio@yourdomain.com" oninput={(e) => set('alertEmailFrom', (e.target as HTMLInputElement).value)} class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent" />
        </label>
        <label class="block md:col-span-2">
          <span class="text-xs text-fg-2">To (comma separated)</span>
          <input type="text" value={str('alertEmailTo')} placeholder="you@yourdomain.com, team@yourdomain.com" oninput={(e) => set('alertEmailTo', (e.target as HTMLInputElement).value)} class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent" />
        </label>
        <label class="block md:col-span-2">
          <span class="text-xs text-fg-2">Dashboard address for links</span>
          <input type="url" value={str('dashboardUrl')} placeholder="http://127.0.0.1:3100" oninput={(e) => set('dashboardUrl', (e.target as HTMLInputElement).value)} class="mt-1 w-full px-3 py-2 rounded-lg bg-surface-3 border border-border text-sm focus:outline-none focus:ring-1 focus:ring-accent" />
        </label>

      </div>
      <div class="flex items-center gap-3 mt-3">
        <button type="button" class="px-3 py-1.5 rounded-md border border-border bg-surface-2 text-sm hover:bg-surface-3 cursor-pointer disabled:opacity-50" disabled={emailTesting} onclick={testEmail}>
          {emailTesting ? 'Sending…' : 'Send test email'}
        </button>
        {#if emailTest}<span class="text-xs {emailTest.ok ? 'text-success' : 'text-danger'}">{emailTest.message}</span>{/if}
      </div>
      <p class="text-[10px] text-fg-2 mt-2">The password stays on this computer and is never shown again. Emails include page URLs: use your company's mail server if that matters.</p>

      <label class="flex items-center gap-2 text-sm mt-6">
        <input type="checkbox" checked={bool('alertAlways') || bool('alertEmailAlways')} onchange={(e) => { const v = (e.target as HTMLInputElement).checked; set('alertAlways', v); set('alertEmailAlways', v); }} class="w-4 h-4 accent-accent" />
        Send a report after every crawl, even when nothing changed
      </label>
      <p class="text-[10px] text-fg-2 mt-1">Otherwise email, Telegram and Slack get a report only for critical or new alerts, important changes, or a scheduled run that was skipped or failed. Passwords and tokens stay on this computer and are never shown again.</p>
    </CollapsibleSection>

    <!-- Bottom save bar -->
    <div class="flex justify-end gap-2 pt-2 pb-4">
      {@render actionButtons()}
    </div>
  {/if}
</div>
