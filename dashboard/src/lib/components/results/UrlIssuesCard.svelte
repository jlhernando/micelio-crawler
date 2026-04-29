<script lang="ts">
  let { stats }: { stats: Record<string, unknown> } = $props();

  const ISSUE_LABELS: Record<string, string> = {
    'spaces-in-url': 'Spaces in URL',
    'uppercase-in-path': 'Uppercase in path',
    'too-long-url': 'URL too long',
    'double-slashes': 'Double slashes',
    'trailing-slash-inconsistency': 'Trailing slash',
    'special-characters': 'Special characters',
    'underscore-in-url': 'Underscores',
    'non-ascii': 'Non-ASCII chars',
  };

  let issues = $derived.by(() => {
    const raw = (stats.urlIssueStats as Record<string, string[]>) || {};
    return Object.entries(raw)
      .map(([type, urls]) => ({ type, label: ISSUE_LABELS[type] || type.replace(/-/g, ' '), count: urls.length }))
      .filter(i => i.count > 0)
      .sort((a, b) => b.count - a.count);
  });
</script>

<div class="rounded-md border border-border bg-surface-2 p-4">
  <div class="text-xs font-medium text-fg-2 mb-3">URL Issues</div>
  {#if issues.length > 0}
    <div class="space-y-1.5">
      {#each issues as { label, count }}
        <div class="flex items-center justify-between px-2 py-1 rounded bg-surface-3">
          <span class="text-xs text-fg-2">{label}</span>
          <span class="text-xs font-bold text-warning">{count.toLocaleString()}</span>
        </div>
      {/each}
    </div>
  {:else}
    <div class="text-xs text-success">No URL issues found</div>
  {/if}
</div>
