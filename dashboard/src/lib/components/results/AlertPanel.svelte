<script lang="ts">
  let { stats }: {
    stats: Record<string, unknown>;
  } = $props();

  interface Alert {
    id: string;
    name: string;
    severity: string;
    metric: string;
    value: number;
    message: string;
  }

  interface AlertSummaryData {
    alerts: Alert[];
    critical: number;
    warnings: number;
    info: number;
    timestamp: string;
  }

  let summary = $derived((stats.alertSummary as AlertSummaryData) || null);

  function severityColor(severity: string): string {
    switch (severity) {
      case 'critical': return 'text-danger';
      case 'warning': return 'text-warning';
      default: return 'text-accent';
    }
  }

  function severityBg(severity: string): string {
    switch (severity) {
      case 'critical': return 'bg-danger/10 border-danger/30';
      case 'warning': return 'bg-warning/10 border-warning/30';
      default: return 'bg-accent/10 border-accent/30';
    }
  }

  function severityIcon(severity: string): string {
    switch (severity) {
      case 'critical': return '!!';
      case 'warning': return '!';
      default: return 'i';
    }
  }
</script>

{#if summary}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="text-xs font-medium text-fg-2">Alerts</div>
        {#if summary.critical > 0}
          <span class="px-1.5 py-0.5 rounded text-[10px] font-medium bg-danger/15 text-danger">
            {summary.critical} critical
          </span>
        {/if}
        {#if summary.warnings > 0}
          <span class="px-1.5 py-0.5 rounded text-[10px] font-medium bg-warning/15 text-warning">
            {summary.warnings} warnings
          </span>
        {/if}
        {#if summary.info > 0}
          <span class="px-1.5 py-0.5 rounded text-[10px] font-medium bg-accent/15 text-accent">
            {summary.info} info
          </span>
        {/if}
      </div>
    </div>

    <div class="space-y-2">
      {#each summary.alerts as alert}
        <div class="flex items-start gap-2 p-2 rounded border {severityBg(alert.severity)}">
          <div class="shrink-0 w-5 h-5 rounded-full flex items-center justify-center text-[9px] font-bold {severityColor(alert.severity)} border {alert.severity === 'critical' ? 'border-danger/50' : alert.severity === 'warning' ? 'border-warning/50' : 'border-accent/50'}">
            {severityIcon(alert.severity)}
          </div>
          <div class="flex-1 min-w-0">
            <div class="text-xs font-medium">{alert.name}</div>
            <div class="text-[11px] text-fg-2 mt-0.5">{alert.message}</div>
          </div>
        </div>
      {/each}
    </div>
  </div>
{/if}
