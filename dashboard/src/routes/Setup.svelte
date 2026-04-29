<script lang="ts">
  import { onMount } from 'svelte';
  import CollapsibleSection from '../lib/components/ui/CollapsibleSection.svelte';
  import NumberInput from '../lib/components/ui/NumberInput.svelte';
  import { api } from '../lib/api';
  import { navigate, getCurrentRoute } from '../lib/router';

  // ── Crawl configuration state ──
  let mode = $state<'spider' | 'list' | 'sitemap'>('spider');
  let url = $state('');
  let urlList = $state('');
  let sitemapUrls = $state('');

  // Basic options
  let depth = $state(20);
  let limit = $state(1000000);
  let concurrency = $state(3);
  let delay = $state(1000);
  let userAgent = $state('');

  // Include/Exclude
  let includePatterns = $state('');
  let excludePatterns = $state('');
  let pathOnlyFilters = $state(true);
  let patternTestURLs = $state('');

  function testPatternMatch(url: string, include: string, exclude: string): { pass: boolean; reason: string; matchTarget: string } {
    const incPats = include.split('\n').filter(Boolean);
    const excPats = exclude.split('\n').filter(Boolean);
    let matchTarget = url;
    try {
      const u = new URL(url);
      matchTarget = pathOnlyFilters ? u.pathname : u.pathname + u.search;
    } catch { /* use as-is */ }
    if (incPats.length > 0) {
      let matched = false;
      for (const p of incPats) {
        try { if (new RegExp(p).test(matchTarget)) { matched = true; break; } } catch { /* skip invalid */ }
      }
      if (!matched) return { pass: false, reason: 'no include match', matchTarget };
    }
    for (const p of excPats) {
      try { if (new RegExp(p).test(matchTarget)) return { pass: false, reason: `excluded: ${p}`, matchTarget }; } catch { /* skip invalid */ }
    }
    return { pass: true, reason: incPats.length > 0 ? 'include matched' : '', matchTarget };
  }

  // HTTP
  let customHeaders = $state('');
  let cookies = $state('');
  let checkExternal = $state(false);
  let proxy = $state('');

  // Robots.txt & Sitemaps
  let respectRobots = $state(true);
  let showBlockedInternal = $state(false);
  let discoverSitemaps = $state(false);

  // JavaScript
  let jsRendering = $state(false);
  let renderBlockResources = $state(true);
  let renderTimeoutSec = $state(0);
  let snippets = $state('');

  // Extraction
  let extractions = $state('');
  let searches = $state('');
  let pageWeight = $state(false);

  // AI
  let aiPrompt = $state('');
  let aiProvider = $state<'openai' | 'anthropic' | 'ollama' | ''>('');
  let aiModel = $state('');
  let aiKey = $state('');

  // Performance
  let psi = $state(false);
  let psiKey = $state('');
  let ngrams = $state(false);

  // Semantic
  let embeddings = $state(false);
  let embeddingModel = $state('');
  let similarityThreshold = $state(0.85);

  // Integrations
  let gsc = $state(false);
  let gscProperty = $state('');
  let gscDays = $state(90);
  let gscKeyFile = $state('');
  let ga4 = $state(false);
  let ga4Property = $state('');
  let ga4Days = $state(90);
  let ga4KeyFile = $state('');
  let crux = $state(false);
  let cruxKey = $state('');

  // Link Intelligence
  let linkIntelligence = $state(false);
  let liMaxSuggestions = $state(50);
  let liNoCentrality = $state(false);
  let sitemapOut = $state(false);

  // Segmentation
  let segments = $state('');

  // Output
  let outputFormat = $state<'jsonl' | 'csv'>('jsonl');
  let htmlReport = $state(true);
  let saveHtml = $state(false);
  let saveRendered = $state(false);

  // Rate Limiting & Safety
  let adaptiveRate = $state(false);
  let delayFactor = $state(0);
  let stealth = $state(false);
  let maxErrors = $state(0);
  let timeoutSeconds = $state(0);

  // Database
  let dbPath = $state('');
  let resume = $state(false);

  // Resume from history
  let resumeId = $state<string | null>(null);
  let resumePageCount = $state(0);

  // User Agent presets (inspired by Chrome DevTools Network Conditions)
  const uaPresets = [
    { group: 'Default', label: 'Micelio/1.0', value: '' },
    { group: 'Desktop', label: 'Chrome — Mac', value: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36' },
    { group: 'Desktop', label: 'Chrome — Windows', value: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36' },
    { group: 'Desktop', label: 'Firefox — Mac', value: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:131.0) Gecko/20100101 Firefox/131.0' },
    { group: 'Desktop', label: 'Firefox — Windows', value: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:131.0) Gecko/20100101 Firefox/131.0' },
    { group: 'Desktop', label: 'Safari — Mac', value: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15' },
    { group: 'Desktop', label: 'Edge — Windows', value: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0' },
    { group: 'Mobile', label: 'Chrome — Android', value: 'Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36' },
    { group: 'Mobile', label: 'Chrome — iPhone', value: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/131.0.0.0 Mobile/15E148 Safari/604.1' },
    { group: 'Mobile', label: 'Safari — iPhone', value: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1' },
    { group: 'Mobile', label: 'Safari — iPad', value: 'Mozilla/5.0 (iPad; CPU OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1' },
    { group: 'Bots', label: 'Googlebot', value: 'Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)' },
    { group: 'Bots', label: 'Googlebot Desktop', value: 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Googlebot/2.1; +http://www.google.com/bot.html) Chrome/131.0.0.0 Safari/537.36' },
    { group: 'Bots', label: 'Googlebot Smartphone', value: 'Mozilla/5.0 (Linux; Android 6.0.1; Nexus 5X Build/MMB29P) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)' },
    { group: 'Bots', label: 'Bingbot', value: 'Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)' },
  ];
  const uaGroups = [...new Set(uaPresets.map(p => p.group))];
  let selectedUaPreset = $state('__default');

  function onUaPresetChange(e: Event) {
    const val = (e.target as HTMLSelectElement).value;
    selectedUaPreset = val;
    if (val === '__default') {
      userAgent = '';
    } else if (val !== '__custom') {
      const preset = uaPresets.find(p => p.label === val);
      if (preset) userAgent = preset.value;
    }
  }

  function onUaManualEdit() {
    // Check if current value matches any preset
    const match = uaPresets.find(p => p.value === userAgent);
    selectedUaPreset = match ? (match.value === '' ? '__default' : match.label) : '__custom';
  }

  // Recent sites (from crawl history)
  let recentSites = $state<{ domain: string; url: string }[]>([]);

  onMount(() => {
    // Check for resume from history
    const route = getCurrentRoute();
    if (route.params.resumeId) {
      resumeId = route.params.resumeId;
      api.getCrawlStatus(resumeId).then(job => {
        const config = (job as Record<string, unknown>).config as Record<string, unknown>;
        if (config) {
          loadConfigFromJob(config);
          // Auto-enable resume with the original DB path
          resume = true;
          dbPath = (job as Record<string, unknown>).dbPath as string || '';
          resumePageCount = (job as Record<string, unknown>).pageCount as number || 0;
        }
      }).catch(() => {
        error = 'Failed to load crawl config for resuming';
        resumeId = null;
      });
    }

    api.listCrawls().then(data => {
      const seen = new Set<string>();
      const sites: { domain: string; url: string }[] = [];
      for (const c of data.crawls) {
        if (!c.seedUrl) continue;
        try {
          const d = new URL(c.seedUrl).hostname.replace(/^www\./, '');
          if (!seen.has(d)) {
            seen.add(d);
            sites.push({ domain: d, url: c.seedUrl });
          }
        } catch { /* skip invalid URLs */ }
        if (sites.length >= 8) break;
      }
      recentSites = sites;
    }).catch(() => {});
  });

  // UI state
  let selectedPreset = $state<string | null>('Quick Scan');
  let starting = $state(false);
  let error = $state('');

  // ── Presets ──
  const presets = [
    { name: 'Quick Scan', desc: 'Fast overview, depth 1, 100 pages', icon: '⚡', config: { depth: 1, limit: 100, concurrency: 10, htmlReport: true } },
    { name: 'Full Audit', desc: 'Enterprise: all analyses, 1M pages', icon: '🔍', config: { depth: 30, limit: 1000000, concurrency: 5, delay: 1000, adaptiveRate: true, discoverSitemaps: true, ngrams: true, embeddings: true, linkIntelligence: true, liMaxSuggestions: 100, checkExternal: true, htmlReport: true } },
    { name: 'Content Audit', desc: 'Content focus: readability, duplicates, n-grams', icon: '📝', config: { depth: 3, limit: 2000, ngrams: true, embeddings: true, htmlReport: true } },
    { name: 'Technical Audit', desc: 'Technical SEO: redirects, canonicals, security', icon: '🔧', config: { depth: 3, limit: 2000, checkExternal: true, linkIntelligence: true, pageWeight: true, htmlReport: true } },
    { name: 'Internal Linking', desc: 'Full link graph, orphans, 200 suggestions, n-grams', icon: '🔗', config: { depth: 10, limit: 50000, concurrency: 10, linkIntelligence: true, liMaxSuggestions: 200, embeddings: true, ngrams: true, sitemapOut: true, checkExternal: true, htmlReport: true } },
  ];

  function applyPreset(preset: typeof presets[0]) {
    selectedPreset = preset.name;
    const c = preset.config as Record<string, unknown>;
    if (c.depth !== undefined) depth = c.depth as number;
    if (c.limit !== undefined) limit = c.limit as number;
    if (c.concurrency !== undefined) concurrency = c.concurrency as number;
    if (c.psi !== undefined) psi = c.psi as boolean;
    if (c.ngrams !== undefined) ngrams = c.ngrams as boolean;
    if (c.embeddings !== undefined) embeddings = c.embeddings as boolean;
    if (c.linkIntelligence !== undefined) linkIntelligence = c.linkIntelligence as boolean;
    if (c.checkExternal !== undefined) checkExternal = c.checkExternal as boolean;
    if (c.pageWeight !== undefined) pageWeight = c.pageWeight as boolean;
    if (c.htmlReport !== undefined) htmlReport = c.htmlReport as boolean;
    if (c.liMaxSuggestions !== undefined) liMaxSuggestions = c.liMaxSuggestions as number;
    if (c.sitemapOut !== undefined) sitemapOut = c.sitemapOut as boolean;
    if (c.adaptiveRate !== undefined) adaptiveRate = c.adaptiveRate as boolean;
    if (c.delayFactor !== undefined) delayFactor = c.delayFactor as number;
    if (c.stealth !== undefined) stealth = c.stealth as boolean;
    if (c.discoverSitemaps !== undefined) discoverSitemaps = c.discoverSitemaps as boolean;
    if (c.maxErrors !== undefined) maxErrors = c.maxErrors as number;
    if (c.timeoutSeconds !== undefined) timeoutSeconds = c.timeoutSeconds as number;
    if (c.delay !== undefined) delay = c.delay as number;
  }

  function loadConfigFromJob(c: Record<string, unknown>) {
    selectedPreset = null;
    if (c.mode) mode = c.mode as 'spider' | 'list' | 'sitemap';
    if (c.seedUrl) url = c.seedUrl as string;
    if (c.depth !== undefined) depth = c.depth as number;
    if (c.limit !== undefined) limit = c.limit as number;
    if (c.concurrency !== undefined) concurrency = c.concurrency as number;
    if (c.delay !== undefined) delay = c.delay as number;
    if (c.userAgent) userAgent = c.userAgent as string;
    if (c.proxy) proxy = c.proxy as string;
    if (c.cookies) cookies = c.cookies as string;
    if (c.includePatterns) includePatterns = (c.includePatterns as string[]).join('\n');
    if (c.excludePatterns) excludePatterns = (c.excludePatterns as string[]).join('\n');
    if (c.pathOnlyFilters !== undefined) pathOnlyFilters = c.pathOnlyFilters as boolean;
    if (c.customHeaders && typeof c.customHeaders === 'object') {
      customHeaders = Object.entries(c.customHeaders as Record<string, string>).map(([k, v]) => `${k}: ${v}`).join('\n');
    }
    if (c.checkExternal !== undefined) checkExternal = c.checkExternal as boolean;
    if (c.respectRobots !== undefined) respectRobots = c.respectRobots as boolean;
    if (c.showBlockedInternal !== undefined) showBlockedInternal = c.showBlockedInternal as boolean;
    if (c.discoverSitemaps !== undefined) discoverSitemaps = c.discoverSitemaps as boolean;
    if (c.jsRendering !== undefined) jsRendering = c.jsRendering as boolean;
    if (c.renderBlockResources !== undefined) renderBlockResources = c.renderBlockResources as boolean;
    if (c.renderTimeoutSec !== undefined) renderTimeoutSec = c.renderTimeoutSec as number;
    if (c.pageWeight !== undefined) pageWeight = c.pageWeight as boolean;
    if (c.psi !== undefined) psi = c.psi as boolean;
    if (c.psiKey) psiKey = c.psiKey as string;
    if (c.ngrams !== undefined) ngrams = c.ngrams as boolean;
    if (c.embeddings !== undefined) embeddings = c.embeddings as boolean;
    if (c.embeddingModel) embeddingModel = c.embeddingModel as string;
    if (c.similarityThreshold !== undefined) similarityThreshold = c.similarityThreshold as number;
    if (c.aiPrompt) aiPrompt = c.aiPrompt as string;
    if (c.aiProvider) aiProvider = c.aiProvider as 'openai' | 'anthropic' | 'ollama' | '';
    if (c.aiModel) aiModel = c.aiModel as string;
    if (c.aiKey) aiKey = c.aiKey as string;
    if (c.gsc !== undefined) gsc = c.gsc as boolean;
    if (c.gscProperty) gscProperty = c.gscProperty as string;
    if (c.gscDays !== undefined) gscDays = c.gscDays as number;
    if (c.gscKeyFile) gscKeyFile = c.gscKeyFile as string;
    if (c.ga4 !== undefined) ga4 = c.ga4 as boolean;
    if (c.ga4Property) ga4Property = c.ga4Property as string;
    if (c.ga4Days !== undefined) ga4Days = c.ga4Days as number;
    if (c.ga4KeyFile) ga4KeyFile = c.ga4KeyFile as string;
    if (c.crux !== undefined) crux = c.crux as boolean;
    if (c.cruxKey) cruxKey = c.cruxKey as string;
    if (c.linkIntelligence !== undefined) linkIntelligence = c.linkIntelligence as boolean;
    if (c.liMaxSuggestions !== undefined) liMaxSuggestions = c.liMaxSuggestions as number;
    if (c.liNoCentrality !== undefined) liNoCentrality = c.liNoCentrality as boolean;
    if (c.sitemapOut !== undefined) sitemapOut = c.sitemapOut as boolean;
    if (c.segmentRules && Array.isArray(c.segmentRules)) {
      segments = (c.segmentRules as Array<{ name: string; pattern: string }>).map(s => `${s.name}:${s.pattern}`).join('\n');
    }
    if (c.outputFormat) outputFormat = c.outputFormat as 'jsonl' | 'csv';
    if (c.htmlReport !== undefined) htmlReport = c.htmlReport as boolean;
    if (c.saveHtml !== undefined) saveHtml = c.saveHtml as boolean;
    if (c.saveRendered !== undefined) saveRendered = c.saveRendered as boolean;
    if (c.adaptiveRate !== undefined) adaptiveRate = c.adaptiveRate as boolean;
    if (c.delayFactor !== undefined) delayFactor = c.delayFactor as number;
    if (c.stealth !== undefined) stealth = c.stealth as boolean;
    if (c.maxErrors !== undefined) maxErrors = c.maxErrors as number;
    if (c.timeoutSeconds !== undefined) timeoutSeconds = c.timeoutSeconds as number;
    if (c.customExtractions && Array.isArray(c.customExtractions)) {
      extractions = (c.customExtractions as Array<{ name: string; selector: string }>).map(e => `${e.name}:${e.selector}`).join('\n');
    }
    if (c.customSearches && Array.isArray(c.customSearches)) {
      searches = (c.customSearches as Array<{ name: string }>).map(s => s.name).join('\n');
    }
    if (c.snippetPaths && Array.isArray(c.snippetPaths)) {
      snippets = (c.snippetPaths as string[]).join('\n');
    }
    // Update UA preset selector
    const match = uaPresets.find(p => p.value === userAgent);
    selectedUaPreset = match ? (match.value === '' ? '__default' : match.label) : '__custom';
  }

  function buildConfig(): Record<string, unknown> {
    const config: Record<string, unknown> = {
      mode,
      depth,
      limit,
      concurrency,
      outputFormat,
      htmlReport,
    };

    if (mode === 'spider') config.seedUrl = url;
    if (mode === 'list') config.urlList = urlList;
    if (mode === 'sitemap') config.sitemapUrls = sitemapUrls.split('\n').map(s => s.trim()).filter(Boolean);

    if (delay > 0) config.delay = delay;
    if (userAgent) config.userAgent = userAgent;
    if (proxy) config.proxy = proxy;
    if (cookies) config.cookies = cookies;
    if (includePatterns) config.includePatterns = includePatterns.split('\n').filter(Boolean);
    if (excludePatterns) config.excludePatterns = excludePatterns.split('\n').filter(Boolean);
    if (includePatterns || excludePatterns) config.pathOnlyFilters = pathOnlyFilters;
    if (customHeaders) {
      const headers: Record<string, string> = {};
      for (const line of customHeaders.split('\n')) {
        const [k, ...v] = line.split(':');
        if (k && v.length) headers[k.trim()] = v.join(':').trim();
      }
      config.customHeaders = headers;
    }

    config.checkExternal = checkExternal;
    config.respectRobots = respectRobots;
    config.showBlockedInternal = showBlockedInternal;
    config.discoverSitemaps = discoverSitemaps;
    config.adaptiveRate = adaptiveRate;
    if (adaptiveRate && delayFactor > 0) config.delayFactor = delayFactor;
    config.stealth = stealth;
    if (maxErrors > 0) config.maxErrors = maxErrors;
    if (timeoutSeconds > 0) config.timeoutSeconds = timeoutSeconds;
    config.jsRendering = jsRendering;
    config.renderBlockResources = renderBlockResources;
    if (renderTimeoutSec > 0) config.renderTimeoutSec = renderTimeoutSec;
    if (snippets) config.snippetPaths = snippets.split('\n').filter(Boolean);

    if (extractions) {
      config.customExtractions = extractions.split('\n').filter(Boolean).map(e => {
        const [name, ...sel] = e.split(':');
        return { name: name.trim(), type: 'css', selector: sel.join(':').trim() };
      });
    }
    if (searches) config.customSearches = searches.split('\n').filter(Boolean).map(s => ({
      name: s, pattern: s, isRegex: s.startsWith('/') && s.endsWith('/'),
    }));

    config.pageWeight = pageWeight;
    config.psi = psi;
    if (psiKey) config.psiKey = psiKey;
    config.ngrams = ngrams;

    if (aiPrompt) {
      config.aiPrompt = aiPrompt;
      config.aiProvider = aiProvider;
      if (aiModel) config.aiModel = aiModel;
      if (aiKey) config.aiKey = aiKey;
    }

    config.embeddings = embeddings;
    if (embeddingModel) config.embeddingModel = embeddingModel;
    config.similarityThreshold = similarityThreshold;

    config.gsc = gsc;
    if (gscProperty) config.gscProperty = gscProperty;
    if (gscDays !== 90) config.gscDays = gscDays;
    if (gscKeyFile) config.gscKeyFile = gscKeyFile;

    config.ga4 = ga4;
    if (ga4Property) config.ga4Property = ga4Property;
    if (ga4Days !== 90) config.ga4Days = ga4Days;
    if (ga4KeyFile) config.ga4KeyFile = ga4KeyFile;

    config.crux = crux;
    if (cruxKey) config.cruxKey = cruxKey;

    config.linkIntelligence = linkIntelligence;
    if (liMaxSuggestions !== 50) config.liMaxSuggestions = liMaxSuggestions;
    config.liNoCentrality = liNoCentrality;
    config.sitemapOut = sitemapOut;

    if (segments) {
      config.segmentRules = segments.split('\n').filter(Boolean).map(s => {
        const [name, ...pat] = s.split(':');
        return { name: name.trim(), pattern: pat.join(':').trim() };
      });
    }

    if (dbPath) config.dbPath = dbPath;
    config.resume = resume;
    config.saveHtml = saveHtml;
    config.saveRendered = saveRendered;

    return config;
  }

  async function startCrawl() {
    const target = mode === 'spider' ? url : mode === 'list' ? urlList : sitemapUrls;
    if (!target.trim()) {
      error = 'Please enter a URL or file path';
      return;
    }

    starting = true;
    error = '';
    try {
      const config = buildConfig();
      const result = resumeId
        ? await api.restartCrawl(resumeId, config)
        : await api.startCrawl(config);
      navigate(`/monitor/${result.id}`);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to start crawl';
      starting = false;
    }
  }
</script>

<div class="max-w-5xl mx-auto space-y-6">
  <!-- Presets -->
  <div>
    <h2 class="text-sm font-medium text-fg-2 mb-3">Quick Start Presets</h2>
    <div class="grid grid-cols-2 lg:grid-cols-5 gap-3">
      {#each presets as preset}
        <button
          type="button"
          class="text-left rounded-lg border p-4 transition-colors cursor-pointer
            {selectedPreset === preset.name ? 'border-accent bg-accent/10' : 'border-border bg-surface-2 hover:border-accent/50 hover:bg-surface-3'}"
          onclick={() => applyPreset(preset)}
        >
          <div class="text-xl mb-1">{preset.icon}</div>
          <div class="text-sm font-medium">{preset.name}</div>
          <div class="text-xs text-fg-2 mt-1">{preset.desc}</div>
        </button>
      {/each}
    </div>
  </div>

  <!-- Mode & URL -->
  <div class="rounded-xl border border-border bg-surface-2 p-5 space-y-4">
    <div class="flex gap-2">
      {#each ['spider', 'list', 'sitemap'] as m}
        <button
          type="button"
          class="px-4 py-2 rounded-lg text-sm font-medium transition-colors cursor-pointer
            {mode === m ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:text-fg'}"
          onclick={() => mode = m as typeof mode}
        >
          {m === 'spider' ? 'Spider' : m === 'list' ? 'URL List' : 'Sitemap'}
        </button>
      {/each}
    </div>

    {#if mode === 'spider'}
      <div>
        <label for="url" class="block text-sm font-medium mb-1">Seed URL</label>
        <input
          id="url"
          type="url"
          bind:value={url}
          placeholder="https://example.com"
          class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg placeholder:text-fg-2/50 text-sm focus:outline-none focus:border-accent"
        />
        {#if recentSites.length > 0}
          <div class="flex flex-wrap gap-1.5 mt-2">
            {#each recentSites as site}
              <button
                type="button"
                class="px-2.5 py-1 rounded-md text-xs bg-surface-3 text-fg-2 hover:text-accent hover:bg-surface-3/80 transition-colors cursor-pointer"
                onclick={() => url = site.url}
              >
                {site.domain}
              </button>
            {/each}
          </div>
        {/if}
      </div>
    {:else if mode === 'list'}
      <div>
        <label for="urlList" class="block text-sm font-medium mb-1">URLs (one per line)</label>
        <textarea
          id="urlList"
          bind:value={urlList}
          placeholder="https://example.com/page1&#10;https://example.com/page2"
          rows="4"
          class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg placeholder:text-fg-2/50 text-sm font-mono focus:outline-none focus:border-accent resize-y"
        ></textarea>
      </div>
    {:else}
      <div>
        <label for="sitemapUrls" class="block text-sm font-medium mb-1">Sitemap URLs (one per line)</label>
        <textarea
          id="sitemapUrls"
          bind:value={sitemapUrls}
          placeholder="https://example.com/sitemap.xml"
          rows="3"
          class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg placeholder:text-fg-2/50 text-sm font-mono focus:outline-none focus:border-accent resize-y"
        ></textarea>
      </div>
    {/if}

    <!-- Basic Options (always visible) -->
    <div class="grid grid-cols-2 sm:grid-cols-4 gap-4">
      <div>
        <label for="depth" class="block text-xs font-medium text-fg-2 mb-1">Depth</label>
        <NumberInput id="depth" min={0} max={10} bind:value={depth}
          className="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
      </div>
      <div>
        <label for="limit" class="block text-xs font-medium text-fg-2 mb-1">Page Limit</label>
        <NumberInput id="limit" min={1} bind:value={limit}
          className="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
      </div>
      <div>
        <label for="concurrency" class="block text-xs font-medium text-fg-2 mb-1">Concurrency</label>
        <NumberInput id="concurrency" min={1} max={20} bind:value={concurrency}
          className="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
      </div>
      <div>
        <label for="delay" class="block text-xs font-medium text-fg-2 mb-1">Delay (ms)</label>
        <NumberInput id="delay" min={0} bind:value={delay}
          className="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
      </div>
    </div>
  </div>

  <!-- Advanced Options -->
  <div class="space-y-3">
    <!-- Filtering -->
    <CollapsibleSection title="URL Filtering">
      <div class="space-y-3">
        <div>
          <label for="include" class="block text-xs font-medium text-fg-2 mb-1">Include Patterns (regex, one per line)</label>
          <textarea id="include" bind:value={includePatterns} rows="2" placeholder="/blog/.*&#10;/products/.*"
            class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm font-mono focus:outline-none focus:border-accent resize-y"></textarea>
        </div>
        <div>
          <label for="exclude" class="block text-xs font-medium text-fg-2 mb-1">Exclude Patterns (regex, one per line)</label>
          <textarea id="exclude" bind:value={excludePatterns} rows="2" placeholder="/admin/.*&#10;/wp-json/.*"
            class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm font-mono focus:outline-none focus:border-accent resize-y"></textarea>
        </div>

        <label class="flex items-center gap-2 text-sm cursor-pointer mt-1">
          <input type="checkbox" bind:checked={pathOnlyFilters} class="rounded accent-accent" />
          Match path only
        </label>
        <p class="text-[10px] text-fg-2/50 pl-6 -mt-1">When on, patterns match against the URL path only (e.g., <code>/products/123</code>). When off, patterns also match query parameters (e.g., <code>/search?q=shoes</code>).</p>

        <!-- Regex validation -->
        {#each [
          { label: 'Include', val: includePatterns },
          { label: 'Exclude', val: excludePatterns }
        ] as { label, val }}
          {#if val.trim()}
            {#each val.split('\n').filter(Boolean) as pat}
              {@const valid = (() => { try { new RegExp(pat); return true; } catch { return false; } })()}
              {#if !valid}
                <div class="text-[10px] text-danger">{label}: <code class="bg-danger/10 px-1 rounded">{pat}</code> — invalid regex</div>
              {/if}
            {/each}
          {/if}
        {/each}

        <!-- Pattern tester -->
        <details class="mt-2">
          <summary class="text-xs text-accent cursor-pointer select-none hover:underline">Test patterns against URLs</summary>
          <div class="mt-2 space-y-2">
            <textarea
              bind:value={patternTestURLs}
              rows="3"
              placeholder="Paste URLs to test (one per line)&#10;https://example.com/blog/post-1&#10;https://example.com/admin/login"
              class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm font-mono focus:outline-none focus:border-accent resize-y"
            ></textarea>
            {#if patternTestURLs.trim()}
              <div class="space-y-0.5 max-h-48 overflow-y-auto">
                {#each patternTestURLs.split('\n').filter(Boolean) as testUrl}
                  {@const result = testPatternMatch(testUrl, includePatterns, excludePatterns)}
                  <div class="flex items-start gap-1.5 text-xs font-mono py-0.5">
                    <span class="shrink-0 {result.pass ? 'text-green-400' : 'text-red-400'}">{result.pass ? 'PASS' : 'FAIL'}</span>
                    <span class="text-fg-2 truncate" title="{testUrl}\nMatched against: {result.matchTarget}">{result.matchTarget.length > 55 ? '...' + result.matchTarget.slice(-52) : result.matchTarget}</span>
                    <span class="text-fg-2/50 shrink-0">{result.reason}</span>
                  </div>
                {/each}
              </div>
            {/if}
            <div class="text-[10px] text-fg-2/50 space-y-0.5">
              <div>Patterns are Go-compatible regex matched against the full URL path.</div>
              <div>Examples: <code class="bg-surface-3 px-0.5 rounded">/blog/</code> substring match &middot; <code class="bg-surface-3 px-0.5 rounded">/products/.*</code> prefix match &middot; <code class="bg-surface-3 px-0.5 rounded">\?page=</code> query param &middot; <code class="bg-surface-3 px-0.5 rounded">(?i)/admin</code> case-insensitive</div>
            </div>
          </div>
        </details>
      </div>
    </CollapsibleSection>

    <!-- Robots.txt & Sitemaps -->
    <CollapsibleSection title="Robots.txt & Sitemaps">
      <div class="space-y-3">
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={respectRobots} class="rounded accent-accent" />
          Respect robots.txt
        </label>
        <p class="text-xs text-fg-2 pl-6">When enabled, URLs disallowed by robots.txt will be skipped during crawl.</p>
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={showBlockedInternal} class="rounded accent-accent" />
          Show internal URLs blocked by robots.txt
        </label>
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={discoverSitemaps} class="rounded accent-accent" />
          Discover sitemaps
        </label>
        <p class="text-xs text-fg-2 pl-6">Parse XML sitemaps (from robots.txt or /sitemap.xml) to find and crawl orphan pages not reachable via internal links.</p>
      </div>
    </CollapsibleSection>

    <!-- Rate Limiting & Safety -->
    <CollapsibleSection title="Rate Limiting & Safety">
      <div class="space-y-3">
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={adaptiveRate} class="rounded accent-accent" />
          Adaptive rate limiting
        </label>
        <p class="text-xs text-fg-2 pl-6">Dynamically adjusts request delay based on server feedback (429/503 responses, response times). Recommended for large sites.</p>
        {#if adaptiveRate}
        <div class="pl-6">
          <label for="delayFactor" class="block text-xs font-medium text-fg-2 mb-1">Delay Factor (0 = auto, e.g. 5 = 5x avg response time)</label>
          <NumberInput id="delayFactor" min={0} step="0.5" bind:value={delayFactor}
            className="w-32 px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
          <p class="text-xs text-fg-2 mt-1">Heritrix-style: delay = factor × avg response time. E.g., 200ms response × factor 5 = 1s gap.</p>
        </div>
        {/if}
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={stealth} class="rounded accent-accent" />
          Stealth mode
        </label>
        <p class="text-xs text-fg-2 pl-6">Mimics Chrome TLS fingerprint and HTTP header order to evade bot detection. Use with a Chrome User-Agent.</p>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label for="maxErrors" class="block text-xs font-medium text-fg-2 mb-1">Max Errors (0 = unlimited)</label>
            <NumberInput id="maxErrors" min={0} bind:value={maxErrors}
              className="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
          </div>
          <div>
            <label for="timeout" class="block text-xs font-medium text-fg-2 mb-1">Timeout (seconds, 0 = none)</label>
            <NumberInput id="timeout" min={0} bind:value={timeoutSeconds}
              className="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
          </div>
        </div>
      </div>
    </CollapsibleSection>

    <!-- HTTP -->
    <CollapsibleSection title="HTTP & Authentication">
      <div class="space-y-3">
        <div>
          <label for="uaPreset" class="block text-xs font-medium text-fg-2 mb-1">User Agent</label>
          <select id="uaPreset" value={selectedUaPreset} onchange={onUaPresetChange}
            class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent mb-2">
            <option value="__default">Micelio/1.0 (default)</option>
            {#each uaGroups.filter(g => g !== 'Default') as group}
              <optgroup label={group}>
                {#each uaPresets.filter(p => p.group === group) as preset}
                  <option value={preset.label}>{preset.label}</option>
                {/each}
              </optgroup>
            {/each}
            <option value="__custom">Custom...</option>
          </select>
          {#if selectedUaPreset === '__custom' || (selectedUaPreset !== '__default' && selectedUaPreset !== '__custom')}
            <input id="ua" type="text" bind:value={userAgent} oninput={onUaManualEdit}
              placeholder="Enter custom user agent string"
              class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm font-mono focus:outline-none focus:border-accent" />
          {/if}
        </div>
        <div>
          <label for="headers" class="block text-xs font-medium text-fg-2 mb-1">Custom Headers (Name: Value, one per line)</label>
          <textarea id="headers" bind:value={customHeaders} rows="2" placeholder="Authorization: Bearer token&#10;X-Custom: value"
            class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm font-mono focus:outline-none focus:border-accent resize-y"></textarea>
        </div>
        <div>
          <label for="cookies" class="block text-xs font-medium text-fg-2 mb-1">Cookies</label>
          <input id="cookies" type="text" bind:value={cookies} placeholder="name=value; name2=value2"
            class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
        </div>
        <div>
          <label for="proxy" class="block text-xs font-medium text-fg-2 mb-1">Proxy</label>
          <input id="proxy" type="text" bind:value={proxy} placeholder="http://proxy:8080, socks5://proxy:1080, or tor"
            class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
        </div>
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={checkExternal} class="rounded accent-accent" />
          Check external links
        </label>
      </div>
    </CollapsibleSection>

    <!-- JavaScript -->
    <CollapsibleSection title="JavaScript Rendering">
      <div class="space-y-3">
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={jsRendering} class="rounded accent-accent" />
          Enable JS rendering (Playwright)
        </label>
        {#if jsRendering}
          <label class="flex items-center gap-2 text-sm cursor-pointer">
            <input type="checkbox" bind:checked={renderBlockResources} class="rounded accent-accent" />
            Block images/fonts/analytics
          </label>
          <div>
            <label for="renderTimeout" class="block text-xs font-medium text-fg-2 mb-1">Render timeout (seconds, 0 = default 30s)</label>
            <NumberInput id="renderTimeout" bind:value={renderTimeoutSec} min={0} max={120}
              className="w-24 px-3 py-1.5 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
          </div>
          <div>
            <label for="snippets" class="block text-xs font-medium text-fg-2 mb-1">Snippet file paths (one per line)</label>
            <textarea id="snippets" bind:value={snippets} rows="2" placeholder="/path/to/snippet.js"
              class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm font-mono focus:outline-none focus:border-accent resize-y"></textarea>
          </div>
        {/if}
      </div>
    </CollapsibleSection>

    <!-- Custom Extraction -->
    <CollapsibleSection title="Custom Extraction">
      <div class="space-y-3">
        <div>
          <label for="extractions" class="block text-xs font-medium text-fg-2 mb-1">CSS Extractions (name:selector, one per line)</label>
          <textarea id="extractions" bind:value={extractions} rows="2" placeholder="price:.product-price&#10;author:.author-name"
            class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm font-mono focus:outline-none focus:border-accent resize-y"></textarea>
        </div>
        <div>
          <label for="searches" class="block text-xs font-medium text-fg-2 mb-1">Text/Regex Searches (one per line)</label>
          <textarea id="searches" bind:value={searches} rows="2" placeholder="phone number&#10;/\d{3}-\d{3}-\d{4}/"
            class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm font-mono focus:outline-none focus:border-accent resize-y"></textarea>
        </div>
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={pageWeight} class="rounded accent-accent" />
          Analyze page weight
        </label>
      </div>
    </CollapsibleSection>

    <!-- AI Analysis -->
    <CollapsibleSection title="AI Analysis">
      <div class="space-y-3">
        <div>
          <label for="aiPrompt" class="block text-xs font-medium text-fg-2 mb-1">AI Prompt</label>
          <textarea id="aiPrompt" bind:value={aiPrompt} rows="3"
            placeholder="Analyze this page for SEO improvements. Focus on content quality and keyword optimization."
            class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent resize-y"></textarea>
        </div>
        {#if aiPrompt}
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label for="aiProvider" class="block text-xs font-medium text-fg-2 mb-1">Provider</label>
              <select id="aiProvider" bind:value={aiProvider}
                class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent">
                <option value="">Select...</option>
                <option value="openai">OpenAI</option>
                <option value="anthropic">Anthropic</option>
                <option value="ollama">Ollama</option>
              </select>
            </div>
            <div>
              <label for="aiModelInput" class="block text-xs font-medium text-fg-2 mb-1">Model (optional)</label>
              <input id="aiModelInput" type="text" bind:value={aiModel} placeholder="Default per provider"
                class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
            </div>
          </div>
          <div>
            <label for="aiKeyInput" class="block text-xs font-medium text-fg-2 mb-1">API Key</label>
            <input id="aiKeyInput" type="password" bind:value={aiKey} placeholder="sk-... or use env var"
              class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
          </div>
        {/if}
      </div>
    </CollapsibleSection>

    <!-- Performance & SEO -->
    <CollapsibleSection title="Performance & Content Analysis">
      <div class="space-y-3">
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={psi} class="rounded accent-accent" />
          PageSpeed Insights
        </label>
        {#if psi}
          <div>
            <label for="psiKeyInput" class="block text-xs font-medium text-fg-2 mb-1">PSI API Key (optional)</label>
            <input id="psiKeyInput" type="password" bind:value={psiKey}
              class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
          </div>
        {/if}
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={crux} class="rounded accent-accent" />
          Chrome UX Report (CrUX)
        </label>
        {#if crux}
          <div>
            <label for="cruxKeyInput" class="block text-xs font-medium text-fg-2 mb-1">CrUX API Key</label>
            <input id="cruxKeyInput" type="password" bind:value={cruxKey}
              class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
          </div>
        {/if}
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={ngrams} class="rounded accent-accent" />
          N-Grams analysis
        </label>
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={embeddings} class="rounded accent-accent" />
          Semantic similarity (embeddings)
        </label>
        {#if embeddings}
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label for="embModel" class="block text-xs font-medium text-fg-2 mb-1">Embedding Model</label>
              <input id="embModel" type="text" bind:value={embeddingModel} placeholder="Default per provider"
                class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
            </div>
            <div>
              <label for="simThresh" class="block text-xs font-medium text-fg-2 mb-1">Similarity Threshold</label>
              <NumberInput id="simThresh" min={0} max={1} step="0.05" bind:value={similarityThreshold}
                className="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
            </div>
          </div>
        {/if}
      </div>
    </CollapsibleSection>

    <!-- Google Integrations -->
    <CollapsibleSection title="Google Integrations">
      <div class="space-y-4">
        <div class="space-y-3">
          <label class="flex items-center gap-2 text-sm cursor-pointer">
            <input type="checkbox" bind:checked={gsc} class="rounded accent-accent" />
            Google Search Console
          </label>
          {#if gsc}
            <div class="grid grid-cols-2 gap-3 pl-6">
              <div>
                <label for="gscProp" class="block text-xs font-medium text-fg-2 mb-1">GSC Property</label>
                <input id="gscProp" type="text" bind:value={gscProperty} placeholder="Auto-detect"
                  class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
              </div>
              <div>
                <label for="gscDaysInput" class="block text-xs font-medium text-fg-2 mb-1">Lookback Days</label>
                <NumberInput id="gscDaysInput" min={1} bind:value={gscDays}
                  className="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
              </div>
              <div class="col-span-2">
                <label for="gscKey" class="block text-xs font-medium text-fg-2 mb-1">Service Account Key File</label>
                <input id="gscKey" type="text" bind:value={gscKeyFile} placeholder="/path/to/key.json"
                  class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
              </div>
            </div>
          {/if}
        </div>
        <div class="space-y-3">
          <label class="flex items-center gap-2 text-sm cursor-pointer">
            <input type="checkbox" bind:checked={ga4} class="rounded accent-accent" />
            Google Analytics 4
          </label>
          {#if ga4}
            <div class="grid grid-cols-2 gap-3 pl-6">
              <div>
                <label for="ga4Prop" class="block text-xs font-medium text-fg-2 mb-1">GA4 Property ID</label>
                <input id="ga4Prop" type="text" bind:value={ga4Property} placeholder="123456789"
                  class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
              </div>
              <div>
                <label for="ga4DaysInput" class="block text-xs font-medium text-fg-2 mb-1">Lookback Days</label>
                <NumberInput id="ga4DaysInput" min={1} bind:value={ga4Days}
                  className="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
              </div>
              <div class="col-span-2">
                <label for="ga4Key" class="block text-xs font-medium text-fg-2 mb-1">Service Account Key File</label>
                <input id="ga4Key" type="text" bind:value={ga4KeyFile} placeholder="/path/to/key.json"
                  class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
              </div>
            </div>
          {/if}
        </div>
      </div>
    </CollapsibleSection>

    <!-- Link Intelligence -->
    <CollapsibleSection title="Link Intelligence">
      <div class="space-y-3">
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={linkIntelligence} class="rounded accent-accent" />
          Enable Link Intelligence (click depth, HITS, centrality)
        </label>
        {#if linkIntelligence}
          <div class="grid grid-cols-2 gap-3 pl-6">
            <div>
              <label for="liMax" class="block text-xs font-medium text-fg-2 mb-1">Max Suggestions</label>
              <NumberInput id="liMax" min={0} max={500} bind:value={liMaxSuggestions}
                className="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
            </div>
            <div class="flex items-end pb-1">
              <label class="flex items-center gap-2 text-sm cursor-pointer">
                <input type="checkbox" bind:checked={liNoCentrality} class="rounded accent-accent" />
                Skip centrality (faster)
              </label>
            </div>
          </div>
        {/if}
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" bind:checked={sitemapOut} class="rounded accent-accent" />
          Generate XML sitemap
        </label>
      </div>
    </CollapsibleSection>

    <!-- Segmentation -->
    <CollapsibleSection title="Segmentation">
      <div>
        <label for="segments" class="block text-xs font-medium text-fg-2 mb-1">Segment Rules (name:pattern, one per line)</label>
        <textarea id="segments" bind:value={segments} rows="3" placeholder="blog:/blog/&#10;products:/products/&#10;docs:/docs/"
          class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm font-mono focus:outline-none focus:border-accent resize-y"></textarea>
      </div>
    </CollapsibleSection>

    <!-- Output & Storage -->
    <CollapsibleSection title="Output & Storage">
      <div class="space-y-3">
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label for="format" class="block text-xs font-medium text-fg-2 mb-1">Output Format</label>
            <select id="format" bind:value={outputFormat}
              class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent">
              <option value="jsonl">JSONL</option>
              <option value="csv">CSV</option>
            </select>
          </div>
          <div>
            <label for="dbPathInput" class="block text-xs font-medium text-fg-2 mb-1">Database Path (SQLite)</label>
            <input id="dbPathInput" type="text" bind:value={dbPath} placeholder="Optional: /path/to/crawl.db"
              class="w-full px-3 py-2 rounded-lg border border-border bg-surface text-fg text-sm focus:outline-none focus:border-accent" />
          </div>
        </div>
        <div class="flex flex-wrap gap-x-6 gap-y-2">
          <label class="flex items-center gap-2 text-sm cursor-pointer">
            <input type="checkbox" bind:checked={htmlReport} class="rounded accent-accent" />
            Generate HTML report
          </label>
          {#if dbPath}
            <label class="flex items-center gap-2 text-sm cursor-pointer">
              <input type="checkbox" bind:checked={resume} class="rounded accent-accent" />
              Resume previous crawl
            </label>
          {/if}
          {#if dbPath}
            <label class="flex items-center gap-2 text-sm cursor-pointer">
              <input type="checkbox" bind:checked={saveHtml} class="rounded accent-accent" />
              Save HTML source
            </label>
            <label class="flex items-center gap-2 text-sm cursor-pointer">
              <input type="checkbox" bind:checked={saveRendered} class="rounded accent-accent" disabled={!jsRendering} />
              Save rendered DOM
            </label>
          {/if}
        </div>
        {#if saveHtml || saveRendered}
          <p class="text-xs text-fg-3">HTML is stored compressed (zstd) in the SQLite database. Increases DB size significantly for large crawls.</p>
        {/if}
      </div>
    </CollapsibleSection>
  </div>

  <!-- Start Button -->
  <div class="sticky bottom-0 bg-surface/80 backdrop-blur-sm border-t border-border py-4 -mx-6 px-6">
    {#if error}
      <div class="mb-3 px-4 py-2 rounded-lg bg-danger/10 text-danger text-sm">{error}</div>
    {/if}
    {#if resumeId}
      <div class="mb-3 px-4 py-2 rounded-lg bg-green-500/10 text-green-400 text-sm">
        Resuming crawl — {resumePageCount.toLocaleString()} pages already crawled will be skipped. Edit settings below if needed.
      </div>
    {/if}
    <button
      type="button"
      class="w-full py-3 rounded-lg {resumeId ? 'bg-green-600 hover:bg-green-600/90' : 'bg-accent hover:bg-accent/90'} text-white font-semibold text-base transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
      onclick={startCrawl}
      disabled={starting}
    >
      {#if starting}
        {resumeId ? 'Resuming crawl...' : 'Starting crawl...'}
      {:else}
        {resumeId ? 'Resume Crawl' : 'Start Crawl'}
      {/if}
    </button>
  </div>
</div>
