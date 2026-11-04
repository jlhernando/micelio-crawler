<script lang="ts">
  interface Check { id: string; category: string; title: string; status: 'pass' | 'warn' | 'fail' | 'na'; evidence?: string; fix?: string; url?: string }
  interface Bot { bot: string; owner: string; purpose: string; allowed: boolean | null; explicit?: boolean; liveStatus?: number }
  interface Category { category: string; score: number; pass: number; warn: number; fail: number; na: number }
  interface Host {
    host: string; url: string; checkedAt?: string; requests: number; error?: string; score: number; level: string;
    checks: Check[]; bots?: Bot[]; contentSignals?: string[]; categories?: Category[];
  }
  interface Readiness { score: number; level: string; hosts: Host[] }

  let { stats }: { stats: Record<string, unknown> } = $props();
  let ar = $derived(stats.agentReadiness as Readiness | undefined);
  let hostIdx = $state(0);
  let host = $derived(ar?.hosts?.[Math.min(hostIdx, (ar?.hosts?.length ?? 1) - 1)]);
  let filter = $state<'all' | 'fail' | 'warn' | 'pass' | 'na'>('all');

  const CATEGORY: Record<string, { label: string; hint: string }> = {
    discovery: { label: 'Discovery', hint: 'Can agents find your pages and files?' },
    content: { label: 'Content', hint: 'Can agents read the content without a browser?' },
    access: { label: 'Bot access', hint: 'What do your robots.txt and firewall let AI bots do?' },
    protocols: { label: 'Agent protocols', hint: 'MCP, A2A, API catalog, OAuth: only needed if agents should act on your site.' },
  };
  const STATUS_LABEL = { pass: 'Pass', warn: 'Warning', fail: 'Fail', na: 'N/A' } as const;
  const ORDER = { fail: 0, warn: 1, pass: 2, na: 3 } as const;
  const PURPOSE: Record<string, string> = { training: 'Model training', search: 'AI search', user: 'Fetches for a user' };

  const scoreClass = (s: number) => (s >= 80 ? 'text-success' : s >= 60 ? 'text-warning' : 'text-danger');
  let counts = $derived.by(() => {
    const c = { fail: 0, warn: 0, pass: 0, na: 0 };
    for (const ch of host?.checks ?? []) c[ch.status]++;
    return c;
  });
  let grouped = $derived.by(() => {
    const out: [string, Check[]][] = [];
    for (const cat of Object.keys(CATEGORY)) {
      const list = (host?.checks ?? [])
        .filter((c) => c.category === cat && (filter === 'all' || c.status === filter))
        .sort((a, b) => ORDER[a.status] - ORDER[b.status]);
      if (list.length) out.push([cat, list]);
    }
    return out;
  });
  const path = (u: string) => { try { const x = new URL(u); return x.pathname + x.search; } catch { return u; } };
</script>

{#if !ar || !host}
  <div class="rounded-md border border-border bg-surface-2 p-5 text-sm text-fg-2">
    This crawl did not check agent readiness. Turn on <b>Check agent readiness</b> in New Crawl → Robots.
  </div>
{:else}
  <div class="space-y-4" data-testid="agent-readiness-view">
    {#if ar.hosts.length > 1}
      <div class="flex flex-wrap gap-2">
        {#each ar.hosts as h, i (h.host)}
          <button type="button" class="ar-chip {i === hostIdx ? 'on' : ''}" onclick={() => (hostIdx = i)}>{h.host} <b class={scoreClass(h.score)}>{h.score}</b></button>
        {/each}
      </div>
    {/if}

    <div class="rounded-md border border-border bg-surface-2 p-5">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div class="text-xs uppercase tracking-wider text-fg-2">Agent readiness</div>
          <div class="mt-1 flex items-baseline gap-3">
            <span class="text-4xl font-bold tabular-nums {scoreClass(host.score)}">{host.score}</span>
            <span class="text-fg-2">/ 100</span>
            <span class="text-lg font-semibold">{host.level}</span>
          </div>
          <div class="mt-1 text-xs text-fg-2">
            <a href={host.url} target="_blank" rel="noreferrer" class="hover:text-fg">{host.host}</a>
            · {host.requests} requests{host.checkedAt ? ` · ${new Date(host.checkedAt).toLocaleString()}` : ''}
          </div>
          {#if host.error}<div class="mt-2 text-sm text-danger">{host.error}</div>{/if}
        </div>
        <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
          {#each host.categories ?? [] as c (c.category)}
            <div class="rounded-md border border-border bg-surface px-3 py-2 min-w-[120px]" title={CATEGORY[c.category]?.hint}>
              <div class="text-xs text-fg-2">{CATEGORY[c.category]?.label ?? c.category}</div>
              <div class="text-xl font-bold tabular-nums {c.score < 0 ? 'text-fg-2' : scoreClass(c.score)}">{c.score < 0 ? '—' : c.score}</div>
              <div class="text-[11px] text-fg-2">{c.pass} pass · {c.warn} warn · {c.fail} fail</div>
            </div>
          {/each}
        </div>
      </div>
      <p class="mt-4 text-xs text-fg-2">
        How easily AI crawlers and agents (ChatGPT, Claude, Perplexity, Gemini…) can find, read and use this site. Pass counts fully, a warning half, N/A is not scored.
        Agent protocols only count when the site publishes them.
      </p>
    </div>

    <div class="flex flex-wrap gap-2">
      <button type="button" class="ar-chip {filter === 'all' ? 'on' : ''}" onclick={() => (filter = 'all')}>All <b>{host.checks.length}</b></button>
      {#each ['fail', 'warn', 'pass', 'na'] as const as s (s)}
        <button type="button" class="ar-chip {filter === s ? 'on' : ''}" onclick={() => (filter = s)}>{STATUS_LABEL[s]} <b>{counts[s]}</b></button>
      {/each}
    </div>

    {#each grouped as [cat, list] (cat)}
      <div class="rounded-md border border-border bg-surface-2 overflow-hidden">
        <div class="px-4 py-2 border-b border-border">
          <div class="text-sm font-semibold">{CATEGORY[cat].label}</div>
          <div class="text-xs text-fg-2">{CATEGORY[cat].hint}</div>
        </div>
        <ul>
          {#each list as c (c.id)}
            <li class="flex gap-3 px-4 py-2.5 border-b border-border/60 last:border-b-0" data-check={c.id}>
              <span class="ar-pill {c.status}">{STATUS_LABEL[c.status]}</span>
              <div class="min-w-0 flex-1">
                <div class="text-sm font-medium">
                  {c.title}
                  {#if c.url}<a href={c.url} target="_blank" rel="noreferrer" class="ml-2 font-mono text-xs font-normal text-fg-2 hover:text-fg">{path(c.url)}</a>{/if}
                </div>
                {#if c.evidence}<div class="text-xs text-fg-2 break-words">{c.evidence}</div>{/if}
                {#if c.fix}<div class="mt-1 text-xs"><span class="font-medium text-fg">Fix:</span> <span class="text-fg-2">{c.fix}</span></div>{/if}
              </div>
            </li>
          {/each}
        </ul>
      </div>
    {:else}
      <div class="rounded-md border border-border bg-surface-2 p-4 text-sm text-fg-2">No checks with this status.</div>
    {/each}

    {#if host.bots?.length}
      <div class="rounded-md border border-border bg-surface-2 overflow-hidden">
        <div class="px-4 py-2 border-b border-border">
          <div class="text-sm font-semibold">AI bots in robots.txt</div>
          <div class="text-xs text-fg-2">
            What robots.txt tells each bot about {path(host.checks.find((c) => c.id === 'ai-agents-access')?.url ?? '/')}. "Own rules" means a User-agent group names the bot; the others follow the * group.
            {#if host.contentSignals?.length}<br />Content-Signal: <span class="font-mono">{host.contentSignals.join(' | ')}</span>{/if}
          </div>
        </div>
        <table class="w-full text-xs">
          <thead class="text-fg-2 text-left">
            <tr class="border-b border-border">
              <th class="p-2 font-medium">Bot</th>
              <th class="p-2 font-medium">Company</th>
              <th class="p-2 font-medium">Purpose</th>
              <th class="p-2 font-medium">robots.txt</th>
              <th class="p-2 font-medium text-right">Live request</th>
            </tr>
          </thead>
          <tbody>
            {#each host.bots as b (b.bot)}
              <tr class="border-b border-border/60 last:border-b-0">
                <td class="p-2 font-mono">{b.bot}</td>
                <td class="p-2">{b.owner}</td>
                <td class="p-2 text-fg-2">{PURPOSE[b.purpose] ?? b.purpose}</td>
                <td class="p-2">
                  {#if b.allowed === null || b.allowed === undefined}<span class="text-fg-2">Unknown</span>
                  {:else if b.allowed}<span class="text-success">Allowed</span>
                  {:else}<span class="{b.purpose === 'training' ? 'text-fg' : 'text-danger'}">Blocked</span>{/if}
                  {#if b.explicit}<span class="ml-1 ar-tag">own rules</span>{/if}
                </td>
                <td class="p-2 text-right tabular-nums">
                  {#if b.liveStatus}<span class={b.liveStatus >= 400 ? 'text-warning' : 'text-success'}>{b.liveStatus}</span>{:else}<span class="text-fg-2">—</span>{/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
{/if}

<style>
  .ar-chip { display: inline-flex; align-items: center; gap: 6px; padding: 4px 10px; border-radius: 8px; border: 1px solid var(--color-border); background: var(--color-surface-2); color: var(--color-fg-2); font-size: 12px; cursor: pointer; }
  .ar-chip:hover { border-color: var(--color-fg-2); color: var(--color-fg); }
  .ar-chip.on { border-color: var(--color-accent); color: var(--color-fg); background: var(--color-surface-3); }
  .ar-pill { flex: none; align-self: flex-start; min-width: 64px; text-align: center; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: 600; }
  .ar-pill.pass { color: var(--color-success); background: color-mix(in srgb, var(--color-success) 14%, transparent); }
  .ar-pill.warn { color: var(--color-warning); background: color-mix(in srgb, var(--color-warning) 14%, transparent); }
  .ar-pill.fail { color: var(--color-danger); background: color-mix(in srgb, var(--color-danger) 14%, transparent); }
  .ar-pill.na { color: var(--color-fg-2); background: var(--color-surface-3); }
  .ar-tag { padding: 1px 6px; border-radius: 6px; background: var(--color-surface-3); color: var(--color-fg-2); }
</style>
