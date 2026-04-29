<script lang="ts">
  let { stats, pageCount }: { stats: Record<string, unknown>; pageCount: number } = $props();

  let scoreData = $derived.by(() => {
    if (!pageCount) return { score: -1, critical: 0, warning: 0 };
    let total = 0;
    let critical = 0;
    let warning = 0;
    const ratio = (n: number) => Math.min(1, n / pageCount);

    // Indexable (25pts)
    const idx = (stats.indexabilityStats as { indexable: number })?.indexable || 0;
    total += ratio(idx) * 25;

    // Title (15pts)
    const noTitle = (stats.pagesWithoutTitle as number) || 0;
    total += ratio(pageCount - noTitle) * 15;
    critical += noTitle;

    // Description (15pts)
    const noDesc = (stats.pagesWithoutDescription as number) || 0;
    total += ratio(pageCount - noDesc) * 15;
    warning += noDesc;

    // H1 (10pts)
    const noH1 = (stats.pagesWithoutH1 as number) || 0;
    total += ratio(pageCount - noH1) * 10;
    warning += noH1;

    // Fast response (10pts)
    const buckets = stats.responseTimeBuckets as { fast: number; medium: number; slow: number; slowest: number } | null;
    if (buckets) {
      const allResp = buckets.fast + buckets.medium + buckets.slow + buckets.slowest;
      total += allResp > 0 ? (buckets.fast / allResp) * 10 : 5;
    } else total += 5;

    // Broken links (10pts)
    const brokenInt = (stats.brokenLinks as unknown[])?.length || 0;
    const brokenExt = (stats.brokenExternalLinks as unknown[])?.length || 0;
    total += Math.max(0, 10 - (brokenInt + brokenExt) * 0.5);
    critical += brokenInt + brokenExt;

    // Canonical (10pts)
    const cs = stats.canonicalStats as { selfReferencing: number; canonicalized: number } | null;
    if (cs) {
      const withCanonical = (cs.selfReferencing || 0) + (cs.canonicalized || 0);
      total += (Math.min(withCanonical, pageCount) / pageCount) * 10;
    } else total += 5;

    // Orphan pages (5pts)
    const orphans = (stats.totalOrphanPages as number) || (stats.orphanPages as string[])?.length || 0;
    total += Math.max(0, 5 - orphans * 0.25);
    critical += orphans;

    // Additional warning sources
    warning += (stats.imagesMissingAlt as number) || 0;
    warning += (stats.totalThinContentPages as number) || (stats.thinContentPages as unknown[])?.length || 0;
    warning += (stats.totalDuplicateContentGroups as number) || (stats.duplicateContentGroups as unknown[])?.length || 0;

    return { score: Math.round(Math.min(100, total)), critical, warning };
  });

  let score = $derived(scoreData.score);
  let color = $derived(score < 0 ? 'var(--color-fg-2)' : score >= 80 ? 'var(--color-success)' : score >= 60 ? 'var(--color-warning)' : 'var(--color-danger)');
  let label = $derived(score < 0 ? '—' : score >= 80 ? 'Good' : score >= 60 ? 'Needs Work' : 'Poor');

  function compact(n: number): string {
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M';
    if (n >= 10_000) return (n / 1_000).toFixed(0) + 'K';
    if (n >= 1_000) return (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'K';
    return n.toString();
  }

  const radius = 40;
  const circumference = 2 * Math.PI * radius;
  let dashOffset = $derived(score < 0 ? circumference : circumference - (score / 100) * circumference);
</script>

<div class="rounded-md border border-border bg-surface-2 p-4">
  <div class="text-xs text-fg-2">Health Score</div>
  <div class="flex items-center gap-3 mt-1">
    <svg width="56" height="56" viewBox="0 0 100 100" class="shrink-0">
      <circle cx="50" cy="50" r={radius} fill="none" stroke="currentColor" class="text-surface-3" stroke-width="8" />
      <circle cx="50" cy="50" r={radius} fill="none" stroke={color} stroke-width="8"
        stroke-dasharray={circumference} stroke-dashoffset={dashOffset}
        stroke-linecap="round" transform="rotate(-90 50 50)" class="transition-all duration-700" />
      <text x="50" y="54" text-anchor="middle" fill={color} font-size="22" font-weight="bold">{score < 0 ? '—' : score}</text>
    </svg>
    <div>
      <div class="text-sm font-semibold" style="color: {color}">{label}</div>
      {#if score >= 0}
        <div class="flex gap-1.5 mt-0.5">
          {#if scoreData.critical > 0}
            <span class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-danger/20 text-danger" title="{scoreData.critical.toLocaleString()} critical issues">{compact(scoreData.critical)}</span>
          {/if}
          {#if scoreData.warning > 0}
            <span class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-warning/20 text-warning" title="{scoreData.warning.toLocaleString()} warnings">{compact(scoreData.warning)}</span>
          {/if}
          {#if scoreData.critical === 0 && scoreData.warning === 0}
            <span class="text-xs text-success">No issues</span>
          {/if}
        </div>
      {/if}
    </div>
  </div>
</div>
