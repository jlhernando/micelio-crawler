<script lang="ts">
  import { api } from '../../api';

  interface TreeNode {
    name: string;
    path: string;
    pages: number;
    children?: TreeNode[];
    statusCodes: Record<number, number>;
    avgResponseTime: number;
    depth: number;
    isPage: boolean;
    expanded?: boolean;
    // SEO metrics
    totalPageRank: number;
    avgPageRank: number;
    totalInternalLinks: number;
    avgInternalLinks: number;
    indexable: number;
    nonIndexable: number;
    totalInlinks: number;
    // Traffic & conversions
    gscClicks?: number;
    gscImpressions?: number;
    ga4Sessions?: number;
    ga4Pageviews?: number;
    ga4Conversions?: number;
    plausibleVisitors?: number;
    plausibleConversions?: number;
  }

  let { crawlId }: { crawlId: string } = $props();

  let loading = $state(true);
  let error = $state<string | null>(null);
  let tree = $state<TreeNode | null>(null);
  let totalPages = $state(0);
  let flatNodes = $state<{ node: TreeNode; indent: number }[]>([]);
  let fetchSeq = 0;
  let sortCol = $state<string>('pages');
  let sortAsc = $state(false);

  $effect(() => {
    const seq = ++fetchSeq;
    const _id = crawlId;
    loading = true;
    error = null;
    api.getDirectoryTree(_id).then((data) => {
      if (fetchSeq !== seq) return;
      const root = data.tree as TreeNode;
      root.expanded = true;
      if (root.children) {
        for (const child of root.children) {
          child.expanded = true;
        }
      }
      tree = root;
      totalPages = data.totalPages;
      flatNodes = flattenTree(root);
      loading = false;
    }).catch((err) => {
      if (fetchSeq !== seq) return;
      error = err?.message || 'Failed to load directory tree';
      loading = false;
    });
  });

  function flattenTree(root: TreeNode): { node: TreeNode; indent: number }[] {
    const result: { node: TreeNode; indent: number }[] = [];
    function walk(node: TreeNode, indent: number) {
      result.push({ node, indent });
      if (node.expanded && node.children) {
        const sorted = [...node.children];
        if (sortCol !== 'pages' || sortAsc) {
          sortChildren(sorted);
        }
        for (const child of sorted) {
          walk(child, indent + 1);
        }
      }
    }
    walk(root, 0);
    return result;
  }

  function sortChildren(children: TreeNode[]) {
    const dir = sortAsc ? 1 : -1;
    children.sort((a, b) => {
      const aDir = (a.children?.length ?? 0) > 0;
      const bDir = (b.children?.length ?? 0) > 0;
      if (aDir !== bDir) return aDir ? -1 : 1;
      const av = getVal(a, sortCol);
      const bv = getVal(b, sortCol);
      return (av - bv) * dir;
    });
  }

  function getVal(n: TreeNode, col: string): number {
    switch (col) {
      case 'pages': return n.pages;
      case 'avgPageRank': return n.avgPageRank;
      case 'totalPageRank': return n.totalPageRank;
      case 'avgInternalLinks': return n.avgInternalLinks;
      case 'totalInlinks': return n.totalInlinks;
      case 'indexable': return n.indexable;
      case 'avgResponseTime': return n.avgResponseTime;
      case 'gscClicks': return n.gscClicks ?? 0;
      case 'gscImpressions': return n.gscImpressions ?? 0;
      case 'ga4Sessions': return n.ga4Sessions ?? 0;
      case 'ga4Conversions': return n.ga4Conversions ?? 0;
      case 'plausibleVisitors': return n.plausibleVisitors ?? 0;
      case 'plausibleConversions': return n.plausibleConversions ?? 0;
      default: return 0;
    }
  }

  function toggleNode(node: TreeNode) {
    node.expanded = !node.expanded;
    if (tree) flatNodes = flattenTree(tree);
  }

  function toggleSort(col: string) {
    if (sortCol === col) {
      sortAsc = !sortAsc;
    } else {
      sortCol = col;
      sortAsc = false;
    }
    if (tree) flatNodes = flattenTree(tree);
  }

  function statusColor(codes: Record<number, number>): string {
    const total = Object.values(codes).reduce((a, b) => a + b, 0);
    if (total === 0) return '';
    const errors = Object.entries(codes)
      .filter(([c]) => Number(c) >= 400 || Number(c) === 0)
      .reduce((a, [, v]) => a + v, 0);
    const redirects = Object.entries(codes)
      .filter(([c]) => Number(c) >= 300 && Number(c) < 400)
      .reduce((a, [, v]) => a + v, 0);
    if (errors / total > 0.3) return 'text-danger';
    if (redirects / total > 0.3) return 'text-warning';
    return 'text-success';
  }

  const DEPTH_COLORS = ['#4361ee', '#2ec4b6', '#f5a623', '#e63946', '#9b59b6', '#1abc9c'];

  function barWidth(count: number, max: number): string {
    if (max === 0) return '0%';
    return `${Math.max(2, (count / max) * 100)}%`;
  }

  function fmt(n: number): string {
    return n.toLocaleString();
  }

  function fmtDec(n: number, d = 2): string {
    if (n === 0) return '0';
    return n < 0.01 ? n.toExponential(1) : n.toFixed(d);
  }

  function pctIndexable(node: TreeNode): string {
    const total = node.indexable + node.nonIndexable;
    if (total === 0) return '-';
    return Math.round((node.indexable / total) * 100) + '%';
  }

  let maxPages = $derived(
    tree && tree.children
      ? Math.max(...tree.children.map(c => c.pages), 1)
      : 1
  );

  let hasGsc = $derived(tree ? (tree.gscClicks ?? 0) > 0 || (tree.gscImpressions ?? 0) > 0 : false);
  let hasGa4 = $derived(tree ? (tree.ga4Sessions ?? 0) > 0 : false);
  let hasPlausible = $derived(tree ? (tree.plausibleVisitors ?? 0) > 0 : false);
  let hasPageRank = $derived(tree ? tree.totalPageRank > 0 : false);

  function sortIndicator(col: string): string {
    if (sortCol !== col) return '';
    return sortAsc ? ' \u25B2' : ' \u25BC';
  }
</script>

<div class="rounded-xl border border-border bg-surface-2 p-5">
  <h3 class="text-sm font-medium text-fg-2 mb-3">Directory Tree</h3>

  {#if loading}
    <div class="flex items-center justify-center py-8 gap-2">
      <div class="w-4 h-4 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
      <span class="text-sm text-fg-2">Loading directory tree...</span>
    </div>
  {:else if error}
    <p class="text-sm text-danger text-center py-8">{error}</p>
  {:else if !tree}
    <p class="text-sm text-fg-2 text-center py-8">No directory data available.</p>
  {:else}
    <div class="max-h-[600px] overflow-auto">
      <table class="w-full text-xs">
        <thead class="sticky top-0 bg-surface-2 z-10">
          <tr class="text-[10px] text-fg-2/60 uppercase tracking-wider">
            <th class="text-left py-1 pl-1 font-medium min-w-[200px]">Directory</th>
            <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('pages')}>
              Pages{sortIndicator('pages')}
            </th>
            <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('indexable')}>
              Idx%{sortIndicator('indexable')}
            </th>
            {#if hasPageRank}
              <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('avgPageRank')}>
                Avg PR{sortIndicator('avgPageRank')}
              </th>
              <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('totalPageRank')}>
                Sum PR{sortIndicator('totalPageRank')}
              </th>
            {/if}
            <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('avgInternalLinks')}>
              Avg Links{sortIndicator('avgInternalLinks')}
            </th>
            <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('totalInlinks')}>
              Inlinks{sortIndicator('totalInlinks')}
            </th>
            <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('avgResponseTime')}>
              Resp{sortIndicator('avgResponseTime')}
            </th>
            {#if hasGsc}
              <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('gscClicks')}>
                Clicks{sortIndicator('gscClicks')}
              </th>
              <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('gscImpressions')}>
                Impr{sortIndicator('gscImpressions')}
              </th>
            {/if}
            {#if hasGa4}
              <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('ga4Sessions')}>
                Sessions{sortIndicator('ga4Sessions')}
              </th>
              <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('ga4Conversions')}>
                Conv{sortIndicator('ga4Conversions')}
              </th>
            {/if}
            {#if hasPlausible}
              <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('plausibleVisitors')}>
                Visitors{sortIndicator('plausibleVisitors')}
              </th>
              <th class="text-right py-1 px-1.5 font-medium cursor-pointer hover:text-fg-1 whitespace-nowrap" onclick={() => toggleSort('plausibleConversions')}>
                Conv{sortIndicator('plausibleConversions')}
              </th>
            {/if}
          </tr>
        </thead>
        <tbody>
          {#each flatNodes as { node, indent }}
            {@const hasChildren = (node.children?.length ?? 0) > 0}
            {@const depthColor = DEPTH_COLORS[Math.min(node.depth, DEPTH_COLORS.length - 1)]}
            <tr class="hover:bg-surface-3 group">
              <td class="py-0.5" style="padding-left: {indent * 16 + 4}px">
                <div class="flex items-center gap-1">
                  {#if hasChildren}
                    <button
                      class="w-4 h-4 flex items-center justify-center text-fg-2 hover:text-fg-1 shrink-0 cursor-pointer"
                      onclick={() => toggleNode(node)}
                      aria-expanded={node.expanded}
                      aria-label="{node.expanded ? 'Collapse' : 'Expand'} {node.name}"
                    >
                      <svg class="w-3 h-3 transition-transform {node.expanded ? 'rotate-90' : ''}" viewBox="0 0 12 12" fill="currentColor">
                        <path d="M4 2l4 4-4 4z"/>
                      </svg>
                    </button>
                  {:else}
                    <span class="w-4 shrink-0"></span>
                  {/if}

                  <span class="shrink-0 text-[11px]" style="color: {depthColor}">
                    {#if hasChildren}
                      /
                    {:else}
                      {node.isPage ? '' : '/'}
                    {/if}
                  </span>

                  <span class="font-mono text-fg-1 truncate {node.isPage ? '' : 'font-medium'}" title={node.path}>
                    {node.name === '/' ? '/' : node.name}{hasChildren ? '/' : ''}
                  </span>
                </div>
              </td>

              <td class="text-right px-1.5 text-fg-2/80 tabular-nums">
                <div class="flex items-center gap-1 justify-end">
                  <div class="w-12 h-1.5 rounded-full bg-surface-1 overflow-hidden">
                    <div
                      class="h-full rounded-full"
                      style="width: {barWidth(node.pages, maxPages)}; background: {depthColor}; opacity: 0.6"
                    ></div>
                  </div>
                  <span class="w-10 text-right">{fmt(node.pages)}</span>
                </div>
              </td>

              <td class="text-right px-1.5 tabular-nums {statusColor(node.statusCodes)}">
                {pctIndexable(node)}
              </td>

              {#if hasPageRank}
                <td class="text-right px-1.5 text-fg-2/80 tabular-nums">{fmtDec(node.avgPageRank, 4)}</td>
                <td class="text-right px-1.5 text-fg-2/80 tabular-nums">{fmtDec(node.totalPageRank, 2)}</td>
              {/if}

              <td class="text-right px-1.5 text-fg-2/80 tabular-nums">{fmtDec(node.avgInternalLinks, 1)}</td>
              <td class="text-right px-1.5 text-fg-2/80 tabular-nums">{fmt(node.totalInlinks)}</td>

              <td class="text-right px-1.5 text-fg-2/60 tabular-nums opacity-0 group-hover:opacity-100 transition-opacity">
                {#if node.avgResponseTime > 0}{node.avgResponseTime}ms{/if}
              </td>

              {#if hasGsc}
                <td class="text-right px-1.5 text-fg-2/80 tabular-nums">{fmt(node.gscClicks ?? 0)}</td>
                <td class="text-right px-1.5 text-fg-2/80 tabular-nums">{fmt(node.gscImpressions ?? 0)}</td>
              {/if}
              {#if hasGa4}
                <td class="text-right px-1.5 text-fg-2/80 tabular-nums">{fmt(node.ga4Sessions ?? 0)}</td>
                <td class="text-right px-1.5 text-fg-2/80 tabular-nums">{fmt(node.ga4Conversions ?? 0)}</td>
              {/if}
              {#if hasPlausible}
                <td class="text-right px-1.5 text-fg-2/80 tabular-nums">{fmt(node.plausibleVisitors ?? 0)}</td>
                <td class="text-right px-1.5 text-fg-2/80 tabular-nums">{fmt(node.plausibleConversions ?? 0)}</td>
              {/if}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>

    <div class="mt-3 pt-3 border-t border-border flex items-center gap-4 text-[10px] text-fg-2/60">
      <span>{totalPages.toLocaleString()} pages</span>
      <span>{tree.children?.length ?? 0} top directories</span>
      {#if hasPageRank}
        <span>Total PR: {fmtDec(tree.totalPageRank, 2)}</span>
      {/if}
      <span>
        {#each DEPTH_COLORS.slice(0, 4) as color, i}
          <span class="inline-block w-2 h-2 rounded-full mr-0.5" style="background: {color}"></span>
          <span class="mr-2">Depth {i}</span>
        {/each}
      </span>
    </div>
  {/if}
</div>
