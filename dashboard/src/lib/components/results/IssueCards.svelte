<script lang="ts">
  import { api } from '../../api';

  let { stats, crawlId }: { stats: Record<string, unknown>; crawlId: string } = $props();

  let expandedCard = $state<string | null>(null);
  let loadedDetails = $state<Map<string, { url: string; info?: string }[]>>(new Map());
  let detailsLoading = $state<string | null>(null);

  interface IssueCard {
    id: string;
    label: string;
    count: number;
    severity: 'critical' | 'warning' | 'info';
    details: { url: string; info?: string }[];
    issueFilter?: string; // server-side filter key for lazy loading
  }

  let issues = $derived.by((): IssueCard[] => {
    const cards: IssueCard[] = [];

    // ── Critical (P1) ──

    // 5xx pages
    const statusCodes = (stats.statusCodes as Record<string, number>) || {};
    let pages5xx = 0;
    for (const [code, count] of Object.entries(statusCodes)) {
      if (parseInt(code) >= 500) pages5xx += count;
    }
    if (pages5xx > 0) {
      cards.push({
        id: '5xx-pages', label: 'Server Errors (5xx)', count: pages5xx, severity: 'critical',
        details: [], issueFilter: '5xx',
      });
    }

    const brokenLinks = (stats.brokenLinks as { url: string; statusCode: number; foundOn: string[] }[]) || [];
    if (brokenLinks.length > 0) {
      cards.push({
        id: 'broken-links', label: 'Broken Internal Links', count: brokenLinks.length, severity: 'critical',
        details: brokenLinks.slice(0, 50).map(l => ({
          url: l.url,
          info: `${l.statusCode}${l.foundOn?.length ? ` — found on ${l.foundOn.length} page(s)` : ''}`,
        })),
      });
    }

    const brokenExternal = (stats.brokenExternalLinks as { url: string; statusCode: number }[]) || [];
    if (brokenExternal.length > 0) {
      cards.push({
        id: 'broken-external', label: 'Broken External Links', count: brokenExternal.length, severity: 'critical',
        details: brokenExternal.slice(0, 50).map(l => ({ url: l.url, info: `Status: ${l.statusCode}` })),
      });
    }

    const orphans = (stats.orphanPages as string[]) || [];
    if (orphans.length > 0) {
      cards.push({
        id: 'orphan-pages', label: 'Orphan Pages', count: orphans.length, severity: 'critical',
        details: orphans.slice(0, 50).map(url => ({ url })),
      });
    }

    const longRedirects = (stats.longRedirectChains as { url: string; hops: number }[]) || [];
    if (longRedirects.length > 0) {
      cards.push({
        id: 'long-redirect-chains', label: 'Long Redirect Chains (3+)', count: longRedirects.length, severity: 'critical',
        details: longRedirects.slice(0, 50).map(r => ({ url: r.url, info: `${r.hops} hops` })),
      });
    }

    const mixedContent = (stats.mixedContentPages as string[]) || [];
    if (mixedContent.length > 0) {
      cards.push({
        id: 'mixed-content', label: 'Mixed Content (HTTP on HTTPS)', count: mixedContent.length, severity: 'critical',
        details: mixedContent.slice(0, 50).map(url => ({ url })),
      });
    }

    const canonicalStats = stats.canonicalStats as {
      canonicalToNon200?: { url: string; canonical: string; targetStatus: number }[];
      canonicalToNonIndexable?: { url: string; canonical: string }[];
      totalWithoutCanonical?: number;
    } | null;
    if (canonicalStats?.canonicalToNon200 && canonicalStats.canonicalToNon200.length > 0) {
      cards.push({
        id: 'canonical-to-non200', label: 'Canonical Points to Error', count: canonicalStats.canonicalToNon200.length, severity: 'critical',
        details: canonicalStats.canonicalToNon200.slice(0, 50).map(c => ({ url: c.url, info: `→ ${c.canonical} (${c.targetStatus})` })),
      });
    }

    if (canonicalStats?.canonicalToNonIndexable && canonicalStats.canonicalToNonIndexable.length > 0) {
      cards.push({
        id: 'canonical-to-non-indexable', label: 'Canonical to Non-Indexable', count: canonicalStats.canonicalToNonIndexable.length, severity: 'warning',
        details: canonicalStats.canonicalToNonIndexable.slice(0, 50).map(c => ({ url: c.url, info: `→ ${c.canonical}` })),
      });
    }

    const missingTitle = (stats.pagesWithoutTitle as number) || 0;
    if (missingTitle > 0) {
      cards.push({
        id: 'missing-title', label: 'Missing Title', count: missingTitle, severity: 'critical',
        details: [], issueFilter: 'missing-title',
      });
    }

    // ── High (P2) ──

    const dupTitles = (stats.duplicateTitleCount as number) || 0;
    if (dupTitles > 0) {
      cards.push({
        id: 'duplicate-titles', label: 'Duplicate Titles', count: dupTitles, severity: 'warning',
        details: [], issueFilter: 'duplicate-titles',
      });
    }

    const dupDescs = (stats.duplicateDescriptionCount as number) || 0;
    if (dupDescs > 0) {
      cards.push({
        id: 'duplicate-descriptions', label: 'Duplicate Descriptions', count: dupDescs, severity: 'warning',
        details: [], issueFilter: 'duplicate-descriptions',
      });
    }

    const missingDesc = (stats.pagesWithoutDescription as number) || 0;
    if (missingDesc > 0) {
      cards.push({
        id: 'missing-description', label: 'Missing Description', count: missingDesc, severity: 'warning',
        details: [], issueFilter: 'missing-description',
      });
    }

    const missingH1 = (stats.pagesWithoutH1 as number) || 0;
    if (missingH1 > 0) {
      cards.push({
        id: 'missing-h1', label: 'Missing H1', count: missingH1, severity: 'warning',
        details: [], issueFilter: 'missing-h1',
      });
    }

    const multiH1 = (stats.multipleH1Count as number) || 0;
    if (multiH1 > 0) {
      cards.push({
        id: 'multiple-h1', label: 'Multiple H1 Tags', count: multiH1, severity: 'warning',
        details: [], issueFilter: 'multiple-h1',
      });
    }

    const sitemapStats = stats.sitemapStats as { nonIndexableInSitemap?: string[] } | null;
    if (sitemapStats?.nonIndexableInSitemap && sitemapStats.nonIndexableInSitemap.length > 0) {
      cards.push({
        id: 'non-indexable-in-sitemap', label: 'Non-Indexable in Sitemap', count: sitemapStats.nonIndexableInSitemap.length, severity: 'warning',
        details: sitemapStats.nonIndexableInSitemap.slice(0, 50).map(url => ({ url })),
      });
    }

    const missingAlt = (stats.imagesMissingAlt as number) || 0;
    if (missingAlt > 0) {
      cards.push({
        id: 'missing-alt', label: 'Images Missing Alt', count: missingAlt, severity: 'warning', details: [],
      });
    }

    // ── Medium (P3) ──

    const thinContent = (stats.thinContentPages as { url: string; wordCount: number }[]) || [];
    if (thinContent.length > 0) {
      cards.push({
        id: 'thin-content', label: 'Thin Content (<200 words)', count: thinContent.length, severity: 'warning',
        details: thinContent.slice(0, 50).map(p => ({ url: p.url, info: `${p.wordCount} words` })),
      });
    }

    const titleTooLong = (stats.titleTooLongCount as number) || 0;
    if (titleTooLong > 0) {
      cards.push({
        id: 'title-too-long', label: 'Title Too Long (>60 chars)', count: titleTooLong, severity: 'info',
        details: [], issueFilter: 'title-too-long',
      });
    }

    const titleTooShort = (stats.titleTooShortCount as number) || 0;
    if (titleTooShort > 0) {
      cards.push({
        id: 'title-too-short', label: 'Title Too Short (<30 chars)', count: titleTooShort, severity: 'info',
        details: [], issueFilter: 'title-too-short',
      });
    }

    const descTooLong = (stats.descriptionTooLongCount as number) || 0;
    if (descTooLong > 0) {
      cards.push({
        id: 'desc-too-long', label: 'Description Too Long (>160)', count: descTooLong, severity: 'info',
        details: [], issueFilter: 'desc-too-long',
      });
    }

    const depthDist = (stats.depthDistribution as Record<string, number>) || {};
    let deepPageCount = 0;
    for (const [depth, count] of Object.entries(depthDist)) {
      if (parseInt(depth) > 4) deepPageCount += count;
    }
    if (deepPageCount > 0) {
      cards.push({
        id: 'deep-pages', label: 'Deep Pages (depth > 4)', count: deepPageCount, severity: 'info',
        details: [], issueFilter: 'deep',
      });
    }

    const slowPages = (stats.slowPages as { url: string; responseTimeMs: number }[]) || [];
    if (slowPages.length > 0) {
      cards.push({
        id: 'slow-pages', label: 'Slow Pages (>3s)', count: slowPages.length, severity: 'info',
        details: slowPages.slice(0, 50).map(p => ({ url: p.url, info: `${(p.responseTimeMs / 1000).toFixed(1)}s` })),
      });
    }

    const hreflangIssues = (stats.hreflangIssues as { url: string; issue: string }[]) || [];
    if (hreflangIssues.length > 0) {
      cards.push({
        id: 'hreflang-issues', label: 'Hreflang Issues', count: hreflangIssues.length, severity: 'info',
        details: hreflangIssues.slice(0, 50).map(h => ({ url: h.url, info: h.issue })),
      });
    }

    const duplicates = (stats.duplicateContentGroups as { hash: string; urls: string[] }[]) || [];
    if (duplicates.length > 0) {
      const totalDupPages = duplicates.reduce((sum, g) => sum + g.urls.length, 0);
      cards.push({
        id: 'duplicate-content', label: 'Duplicate Content', count: totalDupPages, severity: 'warning',
        details: duplicates.slice(0, 20).flatMap(g =>
          g.urls.map(url => ({ url, info: `group of ${g.urls.length}` }))
        ).slice(0, 50),
      });
    }

    const redirectChains = (stats.redirectChains as { url: string; chain: unknown[] }[]) || [];
    if (redirectChains.length > 0) {
      cards.push({
        id: 'redirect-chains', label: 'All Redirect Chains', count: redirectChains.length, severity: 'info',
        details: redirectChains.slice(0, 50).map(r => ({ url: r.url, info: `${r.chain.length} hop(s)` })),
      });
    }

    // ── Low (P4) ──

    if (canonicalStats?.totalWithoutCanonical && canonicalStats.totalWithoutCanonical > 0) {
      cards.push({
        id: 'missing-canonical', label: 'Missing Canonical', count: canonicalStats.totalWithoutCanonical, severity: 'info',
        details: [], issueFilter: 'missing-canonical',
      });
    }

    const nonDescAnchors = (stats.nonDescriptiveAnchors as { url: string; text: string }[]) || [];
    if (nonDescAnchors.length > 0) {
      cards.push({
        id: 'non-descriptive-anchors', label: 'Non-Descriptive Anchors', count: nonDescAnchors.length, severity: 'info',
        details: nonDescAnchors.slice(0, 50).map(a => ({ url: a.url, info: `"${a.text}"` })),
      });
    }

    const missingOg = (stats.pagesWithoutOg as number) || 0;
    if (missingOg > 0) {
      cards.push({
        id: 'missing-og', label: 'Missing Open Graph', count: missingOg, severity: 'info', details: [],
      });
    }

    const soft404s = (stats.soft404Pages as string[]) || [];
    if (soft404s.length > 0) {
      cards.push({
        id: 'soft-404', label: 'Soft 404s', count: soft404s.length, severity: 'warning',
        details: soft404s.slice(0, 50).map(url => ({ url })),
      });
    }

    // Sort: critical first, then warning, then info; by count within severity
    const order = { critical: 0, warning: 1, info: 2 };
    cards.sort((a, b) => order[a.severity] - order[b.severity] || b.count - a.count);
    return cards;
  });

  // Severity summary
  let severityCounts = $derived.by(() => {
    const counts = { critical: 0, warning: 0, info: 0 };
    for (const issue of issues) {
      counts[issue.severity] += issue.count;
    }
    return counts;
  });

  let severityCategoryCounts = $derived.by(() => {
    const counts = { critical: 0, warning: 0, info: 0 };
    for (const issue of issues) {
      counts[issue.severity]++;
    }
    return counts;
  });

  function getDetails(card: IssueCard): { url: string; info?: string }[] {
    return loadedDetails.get(card.id) || card.details;
  }

  async function toggle(card: IssueCard) {
    if (expandedCard === card.id) {
      expandedCard = null;
      return;
    }
    expandedCard = card.id;

    // Lazy-load details from server if this issue uses a server-side filter
    if (card.issueFilter && !loadedDetails.has(card.id)) {
      detailsLoading = card.id;
      try {
        const data = await api.getCrawlPages(crawlId, { issue: card.issueFilter, limit: 50 });
        const details = (data.pages || []).map((p: Record<string, unknown>) => {
          const detail: { url: string; info?: string } = { url: p.url as string };
          // Add contextual info based on issue type
          if (card.issueFilter === '5xx') detail.info = `${p.statusCode}`;
          if (card.issueFilter === 'multiple-h1') {
            const h = p.headings as { h1: string[] } | null;
            if (h?.h1) detail.info = `${h.h1.length} H1 tags`;
          }
          if (card.issueFilter === 'title-too-long' || card.issueFilter === 'title-too-short') {
            const t = p.title as { length: number } | null;
            if (t) detail.info = `${t.length} chars`;
          }
          if (card.issueFilter === 'desc-too-long') {
            const d = p.metaDescription as { length: number } | null;
            if (d) detail.info = `${d.length} chars`;
          }
          if (card.issueFilter === 'deep') detail.info = `depth ${p.depth}`;
          if (card.issueFilter === 'duplicate-titles') {
            const t = p.title as { text: string } | null;
            if (t?.text) {
              const short = t.text.length > 60 ? t.text.slice(0, 60) + '...' : t.text;
              detail.info = `"${short}"`;
            }
          }
          if (card.issueFilter === 'duplicate-descriptions') {
            const d = p.metaDescription as { text: string } | null;
            if (d?.text) {
              const short = d.text.length > 60 ? d.text.slice(0, 60) + '...' : d.text;
              detail.info = `"${short}"`;
            }
          }
          return detail;
        });
        loadedDetails = new Map(loadedDetails).set(card.id, details);
      } catch {
        // Show empty details on failure
      }
      if (detailsLoading === card.id) detailsLoading = null;
    }
  }

  const SEVERITY_COLORS: Record<string, string> = {
    critical: 'bg-surface-2 border-border border-l-2 border-l-danger',
    warning: 'bg-surface-2 border-border border-l-2 border-l-warning',
    info: 'bg-surface-2 border-border border-l-2 border-l-accent',
  };

  const SEVERITY_DOT: Record<string, string> = {
    critical: 'bg-danger',
    warning: 'bg-warning',
    info: 'bg-accent',
  };

  const SEVERITY_COUNT: Record<string, string> = {
    critical: 'text-danger',
    warning: 'text-warning',
    info: 'text-accent',
  };
</script>

{#if issues.length > 0}
  <div>
    <!-- Severity Summary -->
    <div class="flex items-center gap-4 mb-4">
      <h3 class="text-sm font-medium text-fg-2">SEO Issues</h3>
      <div class="flex gap-3 text-xs">
        {#if severityCategoryCounts.critical > 0}
          <span class="flex items-center gap-1">
            <span class="w-2 h-2 rounded-full bg-danger"></span>
            <span class="text-danger font-semibold">{severityCounts.critical.toLocaleString()}</span>
            <span class="text-fg-2">critical ({severityCategoryCounts.critical})</span>
          </span>
        {/if}
        {#if severityCategoryCounts.warning > 0}
          <span class="flex items-center gap-1">
            <span class="w-2 h-2 rounded-full bg-warning"></span>
            <span class="text-warning font-semibold">{severityCounts.warning.toLocaleString()}</span>
            <span class="text-fg-2">warning ({severityCategoryCounts.warning})</span>
          </span>
        {/if}
        {#if severityCategoryCounts.info > 0}
          <span class="flex items-center gap-1">
            <span class="w-2 h-2 rounded-full bg-accent"></span>
            <span class="text-accent font-semibold">{severityCounts.info.toLocaleString()}</span>
            <span class="text-fg-2">info ({severityCategoryCounts.info})</span>
          </span>
        {/if}
      </div>
    </div>

    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
      {#each issues as issue}
        <button
          type="button"
          class="rounded-md p-4 text-left transition-colors cursor-pointer hover:bg-surface-3 {SEVERITY_COLORS[issue.severity]}"
          onclick={() => toggle(issue)}
        >
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-2">
              <span class="w-2 h-2 rounded-full {SEVERITY_DOT[issue.severity]}"></span>
              <span class="text-sm font-medium text-fg">{issue.label}</span>
            </div>
            <span class="text-lg font-bold {SEVERITY_COUNT[issue.severity]}">{issue.count.toLocaleString()}</span>
          </div>
        </button>
      {/each}
    </div>

    <!-- Expanded detail panel -->
    {#if expandedCard}
      {@const card = issues.find(i => i.id === expandedCard)}
      {#if card}
        {@const details = getDetails(card)}
        <div class="mt-3 rounded-md border border-border bg-surface-2 p-4">
          <div class="flex items-center justify-between mb-3">
            <h4 class="text-sm font-medium">{card.label} ({card.count.toLocaleString()})</h4>
            <button
              type="button"
              class="text-xs text-fg-2 hover:text-fg-1 cursor-pointer"
              onclick={() => expandedCard = null}
            >Close</button>
          </div>
          {#if detailsLoading === card.id}
            <div class="flex items-center gap-2 py-4 justify-center">
              <div class="w-4 h-4 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
              <span class="text-xs text-fg-2">Loading details...</span>
            </div>
          {:else if details.length > 0}
            <div class="space-y-1 max-h-64 overflow-y-auto">
              {#each details as item}
                <div class="flex items-center gap-2 py-1 px-2 rounded hover:bg-surface-3 text-xs">
                  <span class="flex-1 truncate font-mono text-fg-2">{item.url}</span>
                  {#if item.info}
                    <span class="text-fg-2 shrink-0">{item.info}</span>
                  {/if}
                </div>
              {/each}
              {#if card.count > details.length}
                <div class="text-xs text-fg-2 px-2 pt-1">...and {(card.count - details.length).toLocaleString()} more</div>
              {/if}
            </div>
          {:else}
            <div class="text-xs text-fg-2 py-2 text-center">No detailed URLs available for this issue type.</div>
          {/if}
        </div>
      {/if}
    {/if}
  </div>
{:else}
  <div class="rounded-md border border-border bg-surface-2 p-4 text-center">
    <span class="text-success font-medium">No issues found</span>
  </div>
{/if}
